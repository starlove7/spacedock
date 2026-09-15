package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/starlove7/spacedock/internal/acp"
	"github.com/starlove7/spacedock/internal/config"
	"github.com/starlove7/spacedock/internal/workspace"
)

func minWait(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	if d > 25*time.Second {
		return 25 * time.Second
	}
	return d
}

type Status string

const (
	StatusRunning     Status = "running"
	StatusReady       Status = "ready"
	StatusFailed      Status = "failed"
	StatusStopped     Status = "stopped"
	StatusInterrupted Status = "interrupted"
)

type Record struct {
	ID                string    `json:"id"`
	WorkspaceID       string    `json:"workspace_id"`
	ProfileID         string    `json:"profile_id"`
	ProfileName       string    `json:"profile_name"`
	Provider          string    `json:"provider"`
	ProviderSessionID string    `json:"provider_session_id,omitempty"`
	ActiveRunID       string    `json:"active_run_id"`
	Status            Status    `json:"status"`
	StartedAt         time.Time `json:"started_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	LastError         string    `json:"last_error"`
}

type RunSnapshot struct {
	RunID       string     `json:"run_id"`
	Status      Status     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	Error       string     `json:"error,omitempty"`
	Provider    string     `json:"provider"`
	WorkspaceID string     `json:"workspace_id"`
}

type ShowResult struct {
	Agent             Record      `json:"agent"`
	Run               RunSnapshot `json:"run"`
	Response          string      `json:"response"`
	ResponseTruncated bool        `json:"response_truncated"`
}

type runState struct {
	mu                sync.Mutex
	snapshot          RunSnapshot
	response          string
	responseTruncated bool
	done              chan struct{}
	cancel            context.CancelFunc
}

type turnLease struct {
	once    sync.Once
	release func()
}

type Manager struct {
	cfg      config.Config
	acp      *acp.SessionManager
	mu       sync.Mutex
	items    map[string]Record
	sessions map[string]providerSession
	runs     map[string]*runState
	sem      chan struct{}
	leases   map[string]*turnLease
}

func NewManager(c config.Config, a *acp.SessionManager) *Manager {
	return &Manager{
		cfg:      c,
		acp:      a,
		items:    map[string]Record{},
		sessions: map[string]providerSession{},
		runs:     map[string]*runState{},
		sem:      make(chan struct{}, c.Agents.MaxConcurrent),
		leases:   map[string]*turnLease{},
	}
}

func (m *Manager) List(w *workspace.Workspace) ([]config.AgentProfileConfig, []Record, error) {
	profiles, err := resolveProfiles(m.cfg, w.Root)
	if err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Record{}
	for _, record := range m.items {
		if record.WorkspaceID == w.ID {
			out = append(out, record)
		}
	}
	return profiles, out, nil
}

func (m *Manager) Run(ctx context.Context, w *workspace.Workspace, profileID, prompt string) (Record, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return Record{}, fmt.Errorf("prompt required")
	}
	profiles, err := resolveProfiles(m.cfg, w.Root)
	if err != nil {
		return Record{}, err
	}
	var profile config.AgentProfileConfig
	ok := false
	for _, candidate := range profiles {
		if candidate.ID == profileID {
			profile = candidate
			ok = true
			break
		}
	}
	if !ok {
		return Record{}, fmt.Errorf("unknown agent profile")
	}
	if !m.acquire() {
		return Record{}, fmt.Errorf("agent concurrency limit reached")
	}
	session, err := m.newProviderSession(ctx, w, profile)
	if err != nil {
		<-m.sem
		return Record{}, err
	}
	agentID, err := randomID("agent_")
	if err != nil {
		_ = session.Close()
		<-m.sem
		return Record{}, err
	}
	if strings.TrimSpace(profile.Instructions) != "" {
		prompt = strings.TrimSpace(profile.Instructions) + "\n\nTask:\n" + prompt
	}
	now := time.Now()
	record := Record{
		ID:                agentID,
		WorkspaceID:       w.ID,
		ProfileID:         profile.ID,
		ProfileName:       profile.Name,
		Provider:          profile.Provider,
		ProviderSessionID: session.SessionID(),
		Status:            StatusRunning,
		StartedAt:         now,
		UpdatedAt:         now,
	}
	m.mu.Lock()
	m.items[agentID] = record
	m.sessions[agentID] = session
	m.mu.Unlock()
	return m.startTurn(record, session, prompt, true)
}

func (m *Manager) Continue(ctx context.Context, workspaceID, agentID, prompt string) (Record, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return Record{}, fmt.Errorf("prompt required")
	}
	m.mu.Lock()
	record, ok := m.items[agentID]
	session := m.sessions[agentID]
	m.mu.Unlock()
	if !ok || record.WorkspaceID != workspaceID {
		return Record{}, fmt.Errorf("unknown agent")
	}
	if record.Status == StatusRunning {
		return Record{}, fmt.Errorf("agent is running")
	}
	if record.Status == StatusStopped {
		return Record{}, fmt.Errorf("agent is stopped")
	}
	if session == nil {
		return Record{}, fmt.Errorf("agent provider session is unavailable")
	}
	if !m.acquire() {
		return Record{}, fmt.Errorf("agent concurrency limit reached")
	}
	return m.startTurn(record, session, prompt, true)
}

func (m *Manager) startTurn(record Record, session providerSession, prompt string, slotHeld bool) (Record, error) {
	runID, err := randomID("run_")
	if err != nil {
		if slotHeld {
			<-m.sem
		}
		return Record{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	run := &runState{
		snapshot: RunSnapshot{RunID: runID, Status: StatusRunning, StartedAt: now, Provider: session.Provider(), WorkspaceID: record.WorkspaceID},
		done:     make(chan struct{}),
		cancel:   cancel,
	}
	record.ActiveRunID = runID
	record.Status = StatusRunning
	record.LastError = ""
	record.UpdatedAt = now
	lease := &turnLease{release: func() { <-m.sem }}
	m.mu.Lock()
	m.items[record.ID] = record
	m.runs[runID] = run
	m.leases[runID] = lease
	m.mu.Unlock()
	go m.executeTurn(record.ID, session, run, lease, ctx, prompt)
	return record, nil
}

func (m *Manager) executeTurn(agentID string, session providerSession, run *runState, lease *turnLease, ctx context.Context, prompt string) {
	result, err := session.Run(ctx, prompt)
	status := StatusReady
	errorText := ""
	if err != nil {
		switch {
		case ctx.Err() != nil || errors.Is(err, context.Canceled):
			status = StatusStopped
		case errors.Is(err, ErrProviderInterrupted):
			status = StatusInterrupted
		default:
			status = StatusFailed
			errorText = err.Error()
		}
	}
	now := time.Now()
	run.mu.Lock()
	run.snapshot.Status = status
	run.snapshot.EndedAt = &now
	run.snapshot.Error = errorText
	run.response = result.Response
	run.responseTruncated = result.ResponseTruncated
	close(run.done)
	run.mu.Unlock()

	m.mu.Lock()
	record, ok := m.items[agentID]
	if ok && record.ActiveRunID == run.snapshot.RunID {
		record.Status = status
		record.UpdatedAt = now
		record.LastError = errorText
		if result.ProviderSessionID != "" {
			record.ProviderSessionID = result.ProviderSessionID
		} else if session.SessionID() != "" {
			record.ProviderSessionID = session.SessionID()
		}
		m.items[agentID] = record
	}
	delete(m.leases, run.snapshot.RunID)
	m.mu.Unlock()
	lease.once.Do(lease.release)
}

func (m *Manager) Show(ctx context.Context, workspaceID, agentID string, wait time.Duration) (ShowResult, error) {
	m.mu.Lock()
	record, ok := m.items[agentID]
	run := m.runs[record.ActiveRunID]
	m.mu.Unlock()
	if !ok || record.WorkspaceID != workspaceID {
		return ShowResult{}, fmt.Errorf("unknown agent")
	}
	if run == nil {
		return ShowResult{}, fmt.Errorf("agent run is unavailable")
	}
	wait = minWait(wait)
	if wait > 0 {
		run.mu.Lock()
		running := run.snapshot.Status == StatusRunning
		done := run.done
		run.mu.Unlock()
		if running {
			timer := time.NewTimer(wait)
			select {
			case <-done:
				timer.Stop()
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ShowResult{}, ctx.Err()
			}
		}
	}
	run.mu.Lock()
	snapshot := run.snapshot
	response := run.response
	truncated := run.responseTruncated
	run.mu.Unlock()
	m.mu.Lock()
	record = m.items[agentID]
	m.mu.Unlock()
	return ShowResult{Agent: record, Run: snapshot, Response: response, ResponseTruncated: truncated}, nil
}

func (m *Manager) Stop(_ context.Context, workspaceID, agentID string) (Record, error) {
	m.mu.Lock()
	record, ok := m.items[agentID]
	if !ok || record.WorkspaceID != workspaceID {
		m.mu.Unlock()
		return Record{}, fmt.Errorf("unknown agent")
	}
	if record.Status == StatusStopped {
		m.mu.Unlock()
		return record, nil
	}
	session := m.sessions[agentID]
	run := m.runs[record.ActiveRunID]
	m.mu.Unlock()
	if run != nil {
		run.cancel()
	}
	var stopErr error
	if session != nil {
		if err := session.Cancel(); err != nil && !errors.Is(err, context.Canceled) {
			stopErr = err
		}
		if err := session.Close(); err != nil && stopErr == nil {
			stopErr = err
		}
	}
	now := time.Now()
	m.mu.Lock()
	record = m.items[agentID]
	record.Status = StatusStopped
	record.UpdatedAt = now
	m.items[agentID] = record
	delete(m.sessions, agentID)
	m.mu.Unlock()
	return record, stopErr
}

func (m *Manager) CloseWorkspace(workspaceID string) {
	m.mu.Lock()
	ids := []string{}
	for id, record := range m.items {
		if record.WorkspaceID == workspaceID {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		_, _ = m.Stop(context.Background(), workspaceID, id)
	}
	m.mu.Lock()
	for _, id := range ids {
		if record, ok := m.items[id]; ok {
			delete(m.runs, record.ActiveRunID)
		}
		delete(m.items, id)
		delete(m.sessions, id)
	}
	m.mu.Unlock()
}

func (m *Manager) CloseAll() {
	m.mu.Lock()
	ids := make([]struct{ workspaceID, agentID string }, 0, len(m.items))
	for id, record := range m.items {
		ids = append(ids, struct{ workspaceID, agentID string }{record.WorkspaceID, id})
	}
	m.mu.Unlock()
	for _, id := range ids {
		_, _ = m.Stop(context.Background(), id.workspaceID, id.agentID)
	}
	m.mu.Lock()
	m.items = map[string]Record{}
	m.sessions = map[string]providerSession{}
	m.runs = map[string]*runState{}
	m.mu.Unlock()
}

func (m *Manager) acquire() bool {
	select {
	case m.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (m *Manager) newProviderSession(ctx context.Context, w *workspace.Workspace, profile config.AgentProfileConfig) (providerSession, error) {
	switch profile.Provider {
	case "codex":
		return newCodexProviderSession(ctx, m.cfg.Agents, profile, w)
	case "acp":
		snapshot, err := m.acp.Connect(ctx, w, acp.ConnectOptions{EndpointID: profile.EndpointID, ModeID: profile.ModeID, ConfigOptions: profile.ConfigOptions, PermissionPolicy: agentACPPermissionPolicy(profile.PermissionPolicy)})
		if err != nil {
			return nil, err
		}
		return newACPProviderSession(m.acp, w.ID, snapshot), nil
	default:
		return nil, fmt.Errorf("unsupported agent provider: %s", profile.Provider)
	}
}

func agentACPPermissionPolicy(profilePolicy string) string {
	switch profilePolicy {
	case "", "auto":
		return "allow_once"
	default:
		return profilePolicy
	}
}

func randomID(prefix string) (string, error) {
	bytes := make([]byte, 10)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(bytes), nil
}
