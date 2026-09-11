package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name,omitempty"`
	Kind     string `json:"kind,omitempty"`
}
type Interaction struct {
	ID        string             `json:"id"`
	SessionID string             `json:"session_id"`
	Kind      string             `json:"kind"`
	Status    string             `json:"status"`
	ToolCall  map[string]any     `json:"tool_call,omitempty"`
	Options   []PermissionOption `json:"options"`
	CreatedAt time.Time          `json:"created_at"`
	ExpiresAt time.Time          `json:"expires_at"`
}
type interactionState struct {
	workspaceID string
	Interaction
	response chan string
}

func (m *SessionManager) permissionRequest(ctx context.Context, p json.RawMessage) (any, *RPCError) {
	var in struct {
		SessionID string             `json:"sessionId"`
		Options   []PermissionOption `json:"options"`
		ToolCall  json.RawMessage    `json:"toolCall"`
	}
	if json.Unmarshal(p, &in) != nil {
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil
	}
	m.mu.Lock()
	s := m.remote[in.SessionID]
	m.mu.Unlock()
	if s == nil {
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil
	}
	usable := []PermissionOption{}
	seen := map[string]bool{}
	for _, o := range in.Options {
		kind := strings.ToLower(strings.TrimSpace(o.Kind))
		if o.OptionID == "" || seen[o.OptionID] || strings.Contains(kind, "always") {
			continue
		}
		seen[o.OptionID] = true
		usable = append(usable, o)
	}
	if len(usable) > 32 {
		usable = usable[:32]
	}
	var tc map[string]any
	if len(usable) == 0 || len(in.ToolCall) == 0 || len(in.ToolCall) > 256<<10 || json.Unmarshal(in.ToolCall, &tc) != nil || tc == nil {
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil
	}
	if s.snap.PermissionPolicy == "allow_once" {
		for _, o := range usable {
			k := strings.ToLower(strings.TrimSpace(o.Kind))
			if k == "allow_once" || k == "allow-once" || k == "allowonce" || k == "allow" {
				return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": o.OptionID}}, nil
			}
		}
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil
	}
	idb := make([]byte, 10)
	if _, e := rand.Read(idb); e != nil {
		return nil, &RPCError{Code: -32603, Message: "Internal error"}
	}
	id := "acpi_" + hex.EncodeToString(idb)
	now := time.Now()
	stateCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st := &interactionState{workspaceID: s.workspace, Interaction: Interaction{ID: id, SessionID: s.snap.SessionID, Kind: "permission", Status: "pending", ToolCall: tc, Options: usable, CreatedAt: now, ExpiresAt: now.Add(30 * time.Second)}, response: make(chan string, 1)}
	m.mu.Lock()
	pending := 0
	sessionPending := 0
	for _, x := range m.interactions {
		if x.Interaction.Status == "pending" {
			pending++
		}
		if x.Interaction.SessionID == s.snap.SessionID && x.Interaction.Status == "pending" {
			sessionPending++
		}
	}
	if pending >= 32 || sessionPending >= 8 {
		m.mu.Unlock()
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil
	}
	m.interactions[id] = st
	m.mu.Unlock()
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	if active != "" {
		m.appendUpdate(active, mustJSON(map[string]any{"sessionUpdate": "permission_request", "interaction": st.Interaction}))
	}
	return m.waitInteraction(stateCtx, st), nil
}
func (m *SessionManager) waitInteraction(ctx context.Context, st *interactionState) map[string]any {
	select {
	case v := <-st.response:
		if v == "" || v == "cancelled" {
			return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
		}
		return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": v}}
	case <-ctx.Done():
		m.mu.Lock()
		if st.Status == "pending" {
			if ctx.Err() == context.DeadlineExceeded {
				st.Status = "expired"
			} else {
				st.Status = "cancelled"
			}
		}
		m.mu.Unlock()
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
	}
}
func (m *SessionManager) settleInteractionLocked(st *interactionState, response, status string) bool {
	if st.Status != "pending" {
		return false
	}
	st.Status = status
	st.response <- response
	return true
}
func (m *SessionManager) Interactions(wid, sid string, pending bool) ([]Interaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Interaction{}
	for _, v := range m.interactions {
		if v.workspaceID == wid && (sid == "" || v.Interaction.SessionID == sid) && (!pending || v.Status == "pending") {
			out = append(out, v.Interaction)
		}
	}
	return out, nil
}
func (m *SessionManager) RespondInteraction(wid, id, option string, cancel bool) (Interaction, error) {
	m.mu.Lock()
	st, ok := m.interactions[id]
	if !ok || st.workspaceID != wid {
		m.mu.Unlock()
		return Interaction{}, fmt.Errorf("unknown interaction")
	}
	if st.Status != "pending" {
		m.mu.Unlock()
		return Interaction{}, fmt.Errorf("interaction is not pending")
	}
	if !cancel {
		found := false
		for _, o := range st.Options {
			if o.OptionID == option {
				found = true
			}
		}
		if !found {
			m.mu.Unlock()
			return Interaction{}, fmt.Errorf("invalid permission response option")
		}
	}
	status := "responded"
	value := option
	if cancel {
		status, value = "cancelled", "cancelled"
	}
	if !m.settleInteractionLocked(st, value, status) {
		m.mu.Unlock()
		return Interaction{}, fmt.Errorf("interaction is not pending")
	}
	v := st.Interaction
	m.mu.Unlock()
	return v, nil
}
