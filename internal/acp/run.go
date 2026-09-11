package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type RunStatus string

const (
	RunRunning     RunStatus = "running"
	RunCompleted   RunStatus = "completed"
	RunCancelled   RunStatus = "cancelled"
	RunInterrupted RunStatus = "interrupted"
	RunFailed      RunStatus = "failed"
)

type Event struct {
	Seq        uint64          `json:"seq"`
	Type       string          `json:"type"`
	SessionID  string          `json:"session_id"`
	RunID      string          `json:"run_id"`
	CreatedAt  time.Time       `json:"created_at"`
	Update     json.RawMessage `json:"update,omitempty"`
	StopReason string          `json:"stop_reason,omitempty"`
	Error      string          `json:"error,omitempty"`
}
type PromptStartResult struct {
	RunID     string    `json:"run_id"`
	SessionID string    `json:"session_id"`
	Status    RunStatus `json:"status"`
	StartedAt time.Time `json:"started_at"`
}
type PromptEventsResult struct {
	RunID        string     `json:"run_id"`
	SessionID    string     `json:"session_id"`
	Status       RunStatus  `json:"status"`
	Events       []Event    `json:"events"`
	NextSeq      uint64     `json:"next_seq"`
	FirstSeq     uint64     `json:"first_seq"`
	LatestSeq    uint64     `json:"latest_seq"`
	DroppedCount uint64     `json:"dropped_count"`
	HasMore      bool       `json:"has_more"`
	Truncated    bool       `json:"truncated"`
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	StopReason   string     `json:"stop_reason,omitempty"`
	Error        string     `json:"error,omitempty"`
}
type runState struct {
	workspaceID string
	PromptStartResult
	nextSeq         uint64
	events          []Event
	eventBytes      int
	notify          chan struct{}
	finalized       chan struct{}
	finishOnce      sync.Once
	cancel          context.CancelFunc
	mu              sync.Mutex
	stop, errorText string
	endedAt         *time.Time
}

func newRun(wid string, p PromptStartResult, c context.CancelFunc) *runState {
	return &runState{workspaceID: wid, PromptStartResult: p, nextSeq: 1, notify: make(chan struct{}, 1), finalized: make(chan struct{}), cancel: c}
}
func (m *SessionManager) StartPrompt(_ context.Context, wid, sid, text string) (PromptStartResult, error) {
	text = strings.TrimSpace(text)
	if text == "" || len([]byte(text)) > 256<<10 {
		return PromptStartResult{}, fmt.Errorf("invalid prompt")
	}
	m.mu.Lock()
	s, ok := m.s[sid]
	if !ok || s.workspace != wid {
		m.mu.Unlock()
		return PromptStartResult{}, fmt.Errorf("unknown ACP session")
	}
	s.mu.Lock()
	if s.active != "" {
		s.mu.Unlock()
		m.mu.Unlock()
		return PromptStartResult{}, fmt.Errorf("ACP session already has active run")
	}
	b := make([]byte, 10)
	if _, e := rand.Read(b); e != nil {
		s.mu.Unlock()
		m.mu.Unlock()
		return PromptStartResult{}, e
	}
	id := "run_" + hex.EncodeToString(b)
	ctx, cancel := context.WithCancel(context.Background())
	p := PromptStartResult{id, sid, RunRunning, time.Now()}
	r := newRun(wid, p, cancel)
	m.runs[id] = r
	s.active = id
	s.mu.Unlock()
	m.mu.Unlock()
	go m.runPrompt(ctx, s, r, text)
	return p, nil
}
func (m *SessionManager) runPrompt(ctx context.Context, s *session, r *runState, text string) {
	var out struct {
		StopReason string `json:"stopReason"`
	}
	e := s.client.Request(ctx, "session/prompt", map[string]any{"sessionId": s.snap.RemoteSessionID, "prompt": []map[string]any{{"type": "text", "text": text}}}, &out)
	status := RunCompleted
	if e != nil {
		status = RunFailed
		if ctx.Err() != nil {
			status = RunCancelled
		} else {
			select {
			case <-s.client.Closed():
				status = RunInterrupted
			default:
			}
		}
	}
	m.finish(r, status, out.StopReason, e)
}
func (m *SessionManager) finish(r *runState, status RunStatus, stop string, e error) {
	r.finishOnce.Do(func() {
		now := time.Now()
		r.mu.Lock()
		r.Status = status
		r.stop = stop
		if e != nil {
			r.errorText = e.Error()
		}
		r.endedAt = &now
		m.addEventLocked(r, Event{Type: map[RunStatus]string{RunCompleted: "completed", RunCancelled: "cancelled", RunInterrupted: "interrupted", RunFailed: "error"}[status], SessionID: r.SessionID, RunID: r.RunID, CreatedAt: now, StopReason: stop, Error: r.errorText})
		r.mu.Unlock()
		m.mu.Lock()
		if s := m.s[r.SessionID]; s != nil {
			s.mu.Lock()
			if s.active == r.RunID {
				s.active = ""
			}
			s.mu.Unlock()
		}
		m.mu.Unlock()
		close(r.finalized)
		select {
		case r.notify <- struct{}{}:
		default:
		}
	})
}
func (m *SessionManager) addEventLocked(r *runState, e Event) {
	e.Seq = r.nextSeq
	r.nextSeq++
	if e.Update != nil && len(e.Update) > 512<<10 {
		original := len(e.Update)
		n := min(64<<10, len(e.Update))
		for n > 0 && !utf8.Valid(e.Update[:n]) {
			n--
		}
		preview := e.Update[:n]
		e.Update = mustJSON(map[string]any{"truncated": true, "original_bytes": original, "retained_bytes": len(preview), "preview": string(preview)})
	}
	r.events = append(r.events, e)
	r.eventBytes += eventSize(e)
	for len(r.events) > 512 || r.eventBytes > 4<<20 {
		r.eventBytes -= eventSize(r.events[0])
		r.events = r.events[1:]
	}
}
func eventSize(e Event) int { return 128 + len(e.Update) + len(e.StopReason) + len(e.Error) }
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func (m *SessionManager) appendUpdate(id string, u json.RawMessage) {
	m.mu.Lock()
	r := m.runs[id]
	m.mu.Unlock()
	if r == nil {
		return
	}
	var x struct {
		SessionUpdate string `json:"sessionUpdate"`
	}
	_ = json.Unmarshal(u, &x)
	if x.SessionUpdate == "agent_thought_chunk" {
		return
	}
	typ := x.SessionUpdate
	if typ == "" {
		typ = "session_update"
	}
	r.mu.Lock()
	if r.Status != RunRunning {
		r.mu.Unlock()
		return
	}
	m.addEventLocked(r, Event{Type: typ, SessionID: r.SessionID, RunID: id, CreatedAt: time.Now(), Update: append(json.RawMessage(nil), u...)})
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}
func (m *SessionManager) PromptEvents(ctx context.Context, wid, id string, after uint64, limit int, wait time.Duration) (PromptEventsResult, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	m.mu.Lock()
	r := m.runs[id]
	m.mu.Unlock()
	if r == nil || r.workspaceID != wid {
		return PromptEventsResult{}, fmt.Errorf("unknown ACP run")
	}
	if wait > 25*time.Second {
		wait = 25 * time.Second
	}
	if wait < 0 {
		wait = 0
	}
	deadline := time.Now().Add(wait)
	for {
		r.mu.Lock()
		latest := r.nextSeq - 1
		if after > latest {
			r.mu.Unlock()
			return PromptEventsResult{}, fmt.Errorf("after sequence is ahead of latest")
		}
		first := uint64(0)
		if len(r.events) > 0 {
			first = r.events[0].Seq
		}
		events := []Event{}
		for _, e := range r.events {
			if e.Seq > after && len(events) < limit {
				events = append(events, e)
			}
		}
		if first == 0 {
			first = r.nextSeq
		}
		res := PromptEventsResult{RunID: id, SessionID: r.SessionID, Status: r.Status, Events: events, NextSeq: after, FirstSeq: first, LatestSeq: latest, DroppedCount: first - 1, Truncated: after < first-1, StartedAt: r.StartedAt, EndedAt: r.endedAt, StopReason: r.stop, Error: r.errorText}
		if len(events) > 0 {
			res.NextSeq = events[len(events)-1].Seq
		}
		res.HasMore = res.NextSeq < latest
		running := r.Status == RunRunning
		r.mu.Unlock()
		if !running || len(events) > 0 || wait <= 0 {
			return res, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return res, nil
		}
		timer := time.NewTimer(remaining)
		select {
		case <-r.notify:
			timer.Stop()
		case <-r.finalized:
			timer.Stop()
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return PromptEventsResult{}, ctx.Err()
		}
	}
}
func (m *SessionManager) CancelPrompt(_ context.Context, wid, sid, id string) error {
	m.mu.Lock()
	s, ok := m.s[sid]
	if !ok || s.workspace != wid {
		m.mu.Unlock()
		return fmt.Errorf("unknown ACP session")
	}
	if id == "" {
		s.mu.Lock()
		id = s.active
		s.mu.Unlock()
	}
	r := m.runs[id]
	m.mu.Unlock()
	if r == nil || r.workspaceID != wid || r.SessionID != sid {
		return fmt.Errorf("unknown ACP run")
	}
	r.mu.Lock()
	status := r.Status
	r.mu.Unlock()
	if status != RunRunning {
		return fmt.Errorf("run is already terminal")
	}
	_ = s.client.Notify("session/cancel", map[string]any{"sessionId": s.snap.RemoteSessionID})
	r.cancel()
	m.finish(r, RunCancelled, "", nil)
	return nil
}
