package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/starlove7/spacedock/internal/workspace"
	"sort"
	"sync"
	"time"
)

type ConnectOptions struct {
	EndpointID       string
	ModeID           string
	ConfigOptions    map[string]any
	PermissionPolicy string
}
type SessionSnapshot struct {
	SessionID        string           `json:"session_id"`
	WorkspaceID      string           `json:"workspace_id"`
	EndpointID       string           `json:"endpoint_id"`
	EndpointName     string           `json:"endpoint_name"`
	RemoteSessionID  string           `json:"remote_session_id"`
	Initialize       InitializeResult `json:"initialize"`
	ModeID           string           `json:"mode_id,omitempty"`
	ConfigOptions    map[string]any   `json:"config_options,omitempty"`
	PermissionPolicy string           `json:"permission_policy"`
}
type session struct {
	workspace string
	client    *Client
	snap      SessionSnapshot
	mu        sync.Mutex
	active    string
}
type SessionManager struct {
	r            *Registry
	mu           sync.Mutex
	s            map[string]*session
	remote       map[string]*session
	runs         map[string]*runState
	interactions map[string]*interactionState
	closed       bool
	closedCh     chan struct{}
}

func NewSessionManager(r *Registry) *SessionManager {
	return &SessionManager{r: r, s: map[string]*session{}, remote: map[string]*session{}, runs: map[string]*runState{}, interactions: map[string]*interactionState{}, closedCh: make(chan struct{})}
}
func sid() (string, error) {
	b := make([]byte, 10)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return "acp_" + hex.EncodeToString(b), nil
}
func (m *SessionManager) Connect(ctx context.Context, w *workspace.Workspace, o ConnectOptions) (SessionSnapshot, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return SessionSnapshot{}, fmt.Errorf("ACP manager closed")
	}
	m.mu.Unlock()
	if o.PermissionPolicy == "" {
		o.PermissionPolicy = "manual"
	}
	if o.PermissionPolicy != "manual" && o.PermissionPolicy != "allow_once" {
		return SessionSnapshot{}, fmt.Errorf("invalid permission policy")
	}
	e, ok := m.r.Endpoint(o.EndpointID)
	if !ok {
		return SessionSnapshot{}, fmt.Errorf("unknown ACP endpoint")
	}
	cl, er := StartClient(ctx, e, w.Root, m.handleRequest, m.handleNotification)
	if er != nil {
		return SessionSnapshot{}, er
	}
	var nr struct {
		SessionID string `json:"sessionId"`
	}
	if er = cl.Request(ctx, "session/new", map[string]any{"cwd": w.Root, "mcpServers": []any{}}, &nr); er != nil || nr.SessionID == "" {
		_ = cl.Close()
		if er == nil {
			er = fmt.Errorf("ACP session/new missing sessionId")
		}
		return SessionSnapshot{}, er
	}
	local, er := sid()
	if er != nil {
		_ = cl.Close()
		return SessionSnapshot{}, er
	}
	s := &session{workspace: w.ID, client: cl, snap: SessionSnapshot{SessionID: local, WorkspaceID: w.ID, EndpointID: e.ID, EndpointName: e.Name, RemoteSessionID: nr.SessionID, Initialize: cl.Initialize(), ModeID: o.ModeID, ConfigOptions: o.ConfigOptions, PermissionPolicy: o.PermissionPolicy}}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = cl.Close()
		return SessionSnapshot{}, fmt.Errorf("ACP manager closed")
	}
	m.remote[nr.SessionID] = s
	m.mu.Unlock()
	if o.ModeID != "" {
		if er = cl.Request(ctx, "session/set_mode", map[string]any{"sessionId": nr.SessionID, "modeId": o.ModeID}, &struct{}{}); er != nil {
			m.removeSession(s)
			_ = cl.Close()
			return SessionSnapshot{}, er
		}
	}
	keys := make([]string, 0, len(o.ConfigOptions))
	for k := range o.ConfigOptions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := o.ConfigOptions[k]
		switch v.(type) {
		case string, bool:
		default:
			m.removeSession(s)
			_ = cl.Close()
			return SessionSnapshot{}, fmt.Errorf("config option must be string or bool")
		}
		p := map[string]any{"sessionId": nr.SessionID, "configId": k, "value": v}
		if _, ok := v.(bool); ok {
			p["type"] = "boolean"
		}
		if er = cl.Request(ctx, "session/set_config_option", p, &struct{}{}); er != nil {
			m.removeSession(s)
			_ = cl.Close()
			return SessionSnapshot{}, er
		}
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = cl.Close()
		return SessionSnapshot{}, fmt.Errorf("ACP manager closed")
	}
	m.s[local] = s
	m.mu.Unlock()
	return s.snap, nil
}
func (m *SessionManager) removeSession(s *session) {
	m.mu.Lock()
	delete(m.s, s.snap.SessionID)
	delete(m.remote, s.snap.RemoteSessionID)
	m.mu.Unlock()
}
func (m *SessionManager) Get(wid, id string) (SessionSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.s[id]
	if !ok || s.workspace != wid {
		return SessionSnapshot{}, fmt.Errorf("unknown ACP session")
	}
	return s.snap, nil
}
func (m *SessionManager) Disconnect(wid, id string) error {
	m.mu.Lock()
	s, ok := m.s[id]
	if !ok || s.workspace != wid {
		m.mu.Unlock()
		return fmt.Errorf("unknown ACP session")
	}
	m.mu.Unlock()
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	if active != "" {
		_ = m.CancelPrompt(context.Background(), wid, id, active)
	}
	_, _ = m.RespondAll(wid, id)
	ctx, c := context.WithTimeout(context.Background(), 2*time.Second)
	defer c()
	var out any
	_ = s.client.Request(ctx, "session/close", map[string]any{"sessionId": s.snap.RemoteSessionID}, &out)
	m.removeSession(s)
	return s.client.Close()
}
func (m *SessionManager) CloseWorkspace(wid string) {
	m.mu.Lock()
	ids := []string{}
	for id, s := range m.s {
		if s.workspace == wid {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.Disconnect(wid, id)
	}
	m.mu.Lock()
	for id, r := range m.runs {
		if r.workspaceID == wid {
			delete(m.runs, id)
		}
	}
	m.mu.Unlock()
}
func (m *SessionManager) CloseAll() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.closedCh)
	xs := []*session{}
	runs := []*runState{}
	for id, s := range m.s {
		delete(m.s, id)
		delete(m.remote, s.snap.RemoteSessionID)
		xs = append(xs, s)
	}
	for _, i := range m.interactions {
		m.settleInteractionLocked(i, "cancelled", "cancelled")
	}
	for _, r := range m.runs {
		runs = append(runs, r)
	}
	m.mu.Unlock()
	for _, r := range runs {
		r.cancel()
		m.finish(r, RunCancelled, "", nil)
	}
	for _, s := range xs {
		_ = s.client.Close()
	}
}
func (m *SessionManager) handleNotification(method string, p json.RawMessage) {
	if method != "session/update" {
		return
	}
	var v struct {
		SessionID string          `json:"sessionId"`
		Update    json.RawMessage `json:"update"`
	}
	if json.Unmarshal(p, &v) != nil {
		return
	}
	m.mu.Lock()
	s := m.remote[v.SessionID]
	m.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	id := s.active
	s.mu.Unlock()
	if id != "" {
		m.appendUpdate(id, v.Update)
	}
}
func (m *SessionManager) handleRequest(ctx context.Context, method string, p json.RawMessage) (any, *RPCError) {
	if method == "session/request_permission" {
		return m.permissionRequest(ctx, p)
	}
	return nil, &RPCError{Code: -32601, Message: "Method not found"}
}
func (m *SessionManager) RespondAll(wid, sid string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, v := range m.interactions {
		if v.workspaceID == wid && v.Interaction.SessionID == sid && v.Status == "pending" {
			m.settleInteractionLocked(v, "cancelled", "cancelled")
			n++
		}
	}
	return n, nil
}
