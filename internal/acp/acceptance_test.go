package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/starlove7/spacedock/internal/config"
	"github.com/starlove7/spacedock/internal/workspace"
)

func TestTransportInboundPanicUnknownAndOversizedMessageBound(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	trn := NewTransport(rwc{inR, nil}, rwc{nil, outW}, func(_ context.Context, method string, _ json.RawMessage) (any, *RPCError) {
		if method == "panic" {
			panic("boom")
		}
		return nil, &RPCError{Code: -32601, Message: "Method not found"}
	}, nil)
	if _, err := io.WriteString(inW, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"unknown\"}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"panic\"}\n"); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(outR)
	codes := map[float64]float64{}
	for len(codes) < 2 && sc.Scan() {
		var msg struct {
			ID    float64 `json:"id"`
			Error *struct {
				Code float64 `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Error != nil {
			codes[msg.ID] = msg.Error.Code
		}
	}
	if codes[1] != -32601 || codes[2] != -32603 {
		t.Fatalf("inbound error codes=%v", codes)
	}
	_ = inW.Close()
	_ = trn.Close()

	closedInput := strings.NewReader("\n")
	trn = NewTransport(rwc{closedInput, nil}, rwc{nil, io.Discard}, nil, nil)
	if err := trn.Notify("oversized", strings.Repeat("x", 4<<20)); err == nil {
		t.Fatal("oversized ACP message accepted")
	}
}

func TestTransportRejectsDuplicateInboundRequestID(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	release := make(chan struct{})
	trn := NewTransport(rwc{inR, nil}, rwc{nil, outW}, func(_ context.Context, _ string, _ json.RawMessage) (any, *RPCError) {
		<-release
		return map[string]any{"ok": true}, nil
	}, nil)
	go func() {
		_, _ = io.WriteString(inW, "{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"slow\"}\n{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"duplicate\"}\n")
	}()
	reader := bufio.NewReader(outR)
	first, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var duplicate struct {
		Error *RPCError `json:"error"`
	}
	if err := json.Unmarshal([]byte(first), &duplicate); err != nil {
		t.Fatal(err)
	}
	if duplicate.Error == nil || duplicate.Error.Code != -32600 {
		t.Fatalf("duplicate response=%s", first)
	}
	close(release)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	_ = inW.Close()
	_ = trn.Close()
}

func TestACPInitializeSessionPromptFiltersThoughtAndCompletes(t *testing.T) {
	script := `IFS= read -r line || exit 0
printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{},"agentInfo":{"name":"fake","title":"Fake","version":"1"},"authMethods":[]}}'
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"session/new"'*) printf '%s\n' "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"sessionId\":\"remote-accept\"}}";;
    *'"method":"session/prompt"'*)
      printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"remote-accept","update":{"sessionUpdate":"agent_thought_chunk","content":"secret thought"}}}'
      printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"remote-accept","update":{"sessionUpdate":"agent_message_chunk","content":{"text":"hello"}}}}'
      printf '%s\n' "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"stopReason\":\"end_turn\"}}";;
    *'"method":"session/close"'*) printf '%s\n' "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}";;
  esac
done`
	r, err := NewRegistry(config.ACPConfig{Endpoints: []config.ACPEndpointConfig{{ID: "fake", Name: "Fake", Command: "/bin/sh", Args: []string{"-c", script}}}})
	if err != nil {
		t.Fatal(err)
	}
	m := NewSessionManager(r)
	w := &workspace.Workspace{ID: "workspace-1", Root: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snap, err := m.Connect(ctx, w, ConnectOptions{EndpointID: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if snap.RemoteSessionID != "remote-accept" || snap.Initialize.AgentInfo.Name != "fake" {
		t.Fatalf("session snapshot=%+v", snap)
	}
	started, err := m.StartPrompt(ctx, w.ID, snap.SessionID, "run task")
	if err != nil {
		t.Fatal(err)
	}
	after := uint64(0)
	all := []Event{}
	var result PromptEventsResult
	for result.Status == "" || result.Status == RunRunning {
		result, err = m.PromptEvents(ctx, w.ID, started.RunID, after, 200, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, result.Events...)
		if len(result.Events) > 0 {
			after = result.Events[len(result.Events)-1].Seq
		}
	}
	if result.Status != RunCompleted || len(all) < 2 {
		t.Fatalf("prompt result=%+v events=%+v", result, all)
	}
	for _, event := range all {
		if event.Type == "agent_thought_chunk" || strings.Contains(string(event.Update), "secret thought") {
			t.Fatalf("thought chunk leaked: %+v", event)
		}
	}
	if all[0].Type != "agent_message_chunk" || all[len(all)-1].Type != "completed" {
		t.Fatalf("event sequence=%+v", all)
	}
	if err := m.Disconnect(w.ID, snap.SessionID); err != nil {
		t.Fatal(err)
	}
}

func TestACPEventPaginationLongPollCursorAndTerminalInvariants(t *testing.T) {
	m := NewSessionManager(nil)
	r := newRun("w", PromptStartResult{RunID: "run-events", SessionID: "sid", Status: RunRunning, StartedAt: time.Now()}, func() {})
	m.runs[r.RunID] = r
	for i := 0; i < 600; i++ {
		m.appendUpdate(r.RunID, mustJSON(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"text": "x"}}))
	}
	page, err := m.PromptEvents(context.Background(), "w", r.RunID, 0, 200, 0)
	if err != nil || len(page.Events) != 200 || !page.HasMore || !page.Truncated || page.FirstSeq <= 1 || page.DroppedCount == 0 {
		t.Fatalf("first page=%+v err=%v", page, err)
	}
	next, err := m.PromptEvents(context.Background(), "w", r.RunID, page.NextSeq, 200, 0)
	if err != nil || len(next.Events) == 0 || next.NextSeq <= page.NextSeq {
		t.Fatalf("next page=%+v err=%v", next, err)
	}
	start := time.Now()
	if _, err := m.PromptEvents(context.Background(), "w", r.RunID, 100000, 200, time.Second); err == nil || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("ahead cursor did not fail immediately: err=%v elapsed=%v", err, time.Since(start))
	}

	started := time.Now()
	timeoutPage, err := m.PromptEvents(context.Background(), "w", r.RunID, r.nextSeq-1, 200, 20*time.Millisecond)
	if err != nil || time.Since(started) < 15*time.Millisecond || timeoutPage.Status != RunRunning {
		t.Fatalf("long poll timeout page=%+v err=%v elapsed=%v", timeoutPage, err, time.Since(started))
	}
	before := len(r.events)
	m.finish(r, RunCompleted, "end_turn", nil)
	m.appendUpdate(r.RunID, mustJSON(map[string]any{"sessionUpdate": "agent_message_chunk", "content": "late"}))
	if len(r.events) != before || r.events[len(r.events)-1].Type != "completed" {
		t.Fatalf("terminal update invariant events=%d before=%d last=%+v", len(r.events), before, r.events[len(r.events)-1])
	}

	// A fresh running run verifies that a notification wakes a long poll.
	r2 := newRun("w", PromptStartResult{RunID: "run-wake", SessionID: "sid", Status: RunRunning, StartedAt: time.Now()}, func() {})
	m.runs[r2.RunID] = r2
	got := make(chan PromptEventsResult, 1)
	go func() {
		v, _ := m.PromptEvents(context.Background(), "w", r2.RunID, 0, 200, time.Second)
		got <- v
	}()
	time.Sleep(20 * time.Millisecond)
	m.appendUpdate(r2.RunID, mustJSON(map[string]any{"sessionUpdate": "agent_message_chunk", "content": "wake"}))
	select {
	case v := <-got:
		if len(v.Events) != 1 {
			t.Fatalf("wake events=%+v", v.Events)
		}
	case <-time.After(time.Second):
		t.Fatal("long poll was not woken by update")
	}
	r3 := newRun("w", PromptStartResult{RunID: "run-large", SessionID: "sid", Status: RunRunning, StartedAt: time.Now()}, func() {})
	m.runs[r3.RunID] = r3
	large := mustJSON(map[string]any{"sessionUpdate": "agent_message_chunk", "content": strings.Repeat("🙂", 200000)})
	m.appendUpdate(r3.RunID, large)
	largePage, err := m.PromptEvents(context.Background(), "w", r3.RunID, 0, 1, 0)
	if err != nil || len(largePage.Events) != 1 || !strings.Contains(string(largePage.Events[0].Update), `"truncated":true`) {
		t.Fatalf("oversized UTF-8 update=%+v err=%v", largePage, err)
	}
}

func TestPermissionInteractionManualAllowOnceLimitsAndCancellation(t *testing.T) {
	manual := NewSessionManager(nil)
	manual.remote["remote"] = &session{workspace: "w", snap: SessionSnapshot{SessionID: "local", WorkspaceID: "w", RemoteSessionID: "remote", PermissionPolicy: "manual"}}
	params := json.RawMessage(`{"sessionId":"remote","options":[{"optionId":"once","kind":"allow_once"},{"optionId":"always","kind":"allow_always"},{"optionId":"reject","kind":"reject"}],"toolCall":{"name":"shell"}}`)
	result := make(chan any, 1)
	go func() { v, _ := manual.permissionRequest(context.Background(), params); result <- v }()
	var interactions []Interaction
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		interactions, _ = manual.Interactions("w", "local", true)
		if len(interactions) == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(interactions) != 1 || len(interactions[0].Options) != 2 {
		t.Fatalf("manual interaction=%+v", interactions)
	}
	if _, err := manual.RespondInteraction("w", interactions[0].ID, "always", false); err == nil {
		t.Fatal("always option remained selectable")
	}
	if _, err := manual.RespondInteraction("w", interactions[0].ID, "reject", false); err != nil {
		t.Fatal(err)
	}
	selected := <-result
	if !strings.Contains(string(mustJSON(selected)), `"optionId":"reject"`) {
		t.Fatalf("manual response=%v", selected)
	}

	allow := NewSessionManager(nil)
	allow.remote["remote"] = &session{workspace: "w", snap: SessionSnapshot{SessionID: "local", WorkspaceID: "w", RemoteSessionID: "remote", PermissionPolicy: "allow_once"}}
	v, rpcErr := allow.permissionRequest(context.Background(), json.RawMessage(`{"sessionId":"remote","options":[{"optionId":"always","kind":"allow_always"},{"optionId":"once","kind":"allow_once"}],"toolCall":{"name":"shell"}}`))
	if rpcErr != nil || !strings.Contains(string(mustJSON(v)), `"optionId":"once"`) {
		t.Fatalf("allow_once response=%v rpcErr=%v", v, rpcErr)
	}
	if _, rpcErr = allow.permissionRequest(context.Background(), json.RawMessage(`{"sessionId":"remote","options":[{"optionId":"once","kind":"allow_once"}],"toolCall":[]}`)); rpcErr != nil {
		t.Fatal(rpcErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := NewSessionManager(nil)
	cancelled.remote["remote"] = &session{workspace: "w", snap: SessionSnapshot{SessionID: "local", WorkspaceID: "w", RemoteSessionID: "remote", PermissionPolicy: "manual"}}
	result = make(chan any, 1)
	go func() { v, _ := cancelled.permissionRequest(ctx, params); result <- v }()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if pending, _ := cancelled.Interactions("w", "local", true); len(pending) == 1 {
			cancel()
			break
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not settle permission")
	}
}

func TestPermissionInteractionSessionAndGlobalPendingLimits(t *testing.T) {
	m := NewSessionManager(nil)
	m.remote["remote"] = &session{workspace: "w", snap: SessionSnapshot{SessionID: "local", WorkspaceID: "w", RemoteSessionID: "remote", PermissionPolicy: "manual"}}
	for i := 0; i < 8; i++ {
		m.interactions[string(rune('a'+i))] = &interactionState{workspaceID: "w", Interaction: Interaction{SessionID: "local", Status: "pending"}}
	}
	v, err := m.permissionRequest(context.Background(), json.RawMessage(`{"sessionId":"remote","options":[{"optionId":"x","kind":"reject"}],"toolCall":{"name":"x"}}`))
	if err != nil || !strings.Contains(string(mustJSON(v)), `"cancelled"`) {
		t.Fatalf("per-session limit response=%v err=%v", v, err)
	}
	m.interactions = map[string]*interactionState{}
	for i := 0; i < 32; i++ {
		m.interactions[string(rune(i+1))] = &interactionState{workspaceID: "w", Interaction: Interaction{SessionID: "other", Status: "pending"}}
	}
	v, err = m.permissionRequest(context.Background(), json.RawMessage(`{"sessionId":"remote","options":[{"optionId":"x","kind":"reject"}],"toolCall":{"name":"x"}}`))
	if err != nil || !strings.Contains(string(mustJSON(v)), `"cancelled"`) {
		t.Fatalf("global limit response=%v err=%v", v, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	st := &interactionState{Interaction: Interaction{Status: "pending"}, response: make(chan string, 1)}
	got := m.waitInteraction(ctx, st)
	if !strings.Contains(string(mustJSON(got)), `"cancelled"`) || st.Status != "expired" {
		t.Fatalf("timeout interaction=%v status=%s", got, st.Status)
	}
}
