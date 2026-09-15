package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/starlove7/spacedock/internal/acp"
	"github.com/starlove7/spacedock/internal/config"
	"github.com/starlove7/spacedock/internal/workspace"
)

const fakeAgentScript = `IFS= read -r line || exit 0
printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{},"agentInfo":{"name":"fake-agent","title":"Fake","version":"1"},"authMethods":[]}}'
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$FAKE_AGENT_LOG"
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"session/new"'*) printf '%s\n' "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"sessionId\":\"agent-remote\"}}";;
    *'"method":"session/prompt"'*)
      printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agent-remote","update":{"sessionUpdate":"agent_message_chunk","content":{"text":"agent-response"}}}}'
      printf '%s\n' "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"stopReason\":\"end_turn\"}}";;
    *'"method":"session/close"'*) printf '%s\n' "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}";;
  esac
done`

func agentFixture(t *testing.T, max int) (*Manager, *workspace.Workspace, *acp.SessionManager, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "agent.log")
	t.Setenv("FAKE_AGENT_LOG", logPath)
	registry, err := acp.NewRegistry(config.ACPConfig{Endpoints: []config.ACPEndpointConfig{{ID: "fake", Name: "Fake", Command: "/bin/sh", Args: []string{"-c", fakeAgentScript}}}})
	if err != nil {
		t.Fatal(err)
	}
	acpManager := acp.NewSessionManager(registry)
	c := config.Config{Agents: config.AgentsConfig{MaxConcurrent: max, Profiles: []config.AgentProfileConfig{{ID: "worker", Name: "Worker", Provider: "acp", EndpointID: "fake", Instructions: "Follow rules", PermissionPolicy: "auto"}}}}
	return NewManager(c, acpManager), &workspace.Workspace{ID: "w", Root: t.TempDir()}, acpManager, logPath
}

func waitAgentTerminal(t *testing.T, ctx context.Context, am *Manager, workspaceID, agentID string) ShowResult {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	var shown ShowResult
	for time.Now().Before(deadline) {
		var err error
		shown, err = am.Show(ctx, workspaceID, agentID, 100*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if shown.Agent.Status != StatusRunning {
			return shown
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("agent did not reach terminal state: %+v", shown)
	return shown
}

func TestACPAgentRunShowContinueStopOwnershipAndInstructionPrefix(t *testing.T) {
	am, w, acpManager, logPath := agentFixture(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	record, err := am.Run(ctx, w, "worker", "Do the task")
	if err != nil {
		t.Fatal(err)
	}
	if record.Provider != "acp" || record.ProviderSessionID == "" {
		t.Fatalf("agent provider record=%+v", record)
	}
	snapshot, err := acpManager.Get(w.ID, record.ProviderSessionID)
	if err != nil || snapshot.PermissionPolicy != "allow_once" {
		t.Fatalf("agent ACP permission policy=%q err=%v", snapshot.PermissionPolicy, err)
	}
	shown := waitAgentTerminal(t, ctx, am, w.ID, record.ID)
	if shown.Agent.Status != StatusReady || shown.Response != "agent-response" || shown.ResponseTruncated || shown.Run.Status != StatusReady {
		t.Fatalf("show result=%+v", shown)
	}
	if _, err := am.Show(ctx, "other", record.ID, 0); err == nil {
		t.Fatal("cross-workspace Show accepted")
	}
	continued, err := am.Continue(ctx, w.ID, record.ID, "Continue this task")
	if err != nil {
		t.Fatal(err)
	}
	if continued.ProviderSessionID != record.ProviderSessionID || continued.Status != StatusRunning || continued.ActiveRunID == record.ActiveRunID {
		t.Fatalf("continue record=%+v original=%+v", continued, record)
	}
	shown = waitAgentTerminal(t, ctx, am, w.ID, record.ID)
	if shown.Agent.Status != StatusReady {
		t.Fatalf("continued show=%+v", shown)
	}
	if _, err := am.Continue(ctx, "other", record.ID, "no"); err == nil {
		t.Fatal("cross-workspace Continue accepted")
	}
	if _, err := am.Stop(ctx, w.ID, record.ID); err != nil {
		t.Fatal(err)
	}
	if stopped, err := am.Stop(ctx, w.ID, record.ID); err != nil || stopped.Status != StatusStopped {
		t.Fatalf("idempotent Stop=%+v err=%v", stopped, err)
	}
	if _, err := am.Continue(ctx, w.ID, record.ID, "stopped"); err == nil {
		t.Fatal("stopped agent continued")
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), `Follow rules\n\nTask:\nDo the task`) {
		t.Fatalf("instructions were not prepended in prompt log: %s", log)
	}
	acpManager.CloseAll()
}

func TestAgentGlobalTurnConcurrencySlotIsHeldUntilTerminal(t *testing.T) {
	release := filepath.Join(t.TempDir(), "release")
	t.Setenv("FAKE_RELEASE", release)
	const blockingScript = `IFS= read -r line || exit 0
printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{},"agentInfo":{"name":"fake","version":"1"},"authMethods":[]}}'
while IFS= read -r line; do
 id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
 case "$line" in
  *'"method":"session/new"'*) printf '%s\n' "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"sessionId\":\"remote\"}}";;
  *'"method":"session/prompt"'*) while [ ! -f "$FAKE_RELEASE" ]; do sleep 0.01; done; printf '%s\n' "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}";;
 esac
done`
	registry, err := acp.NewRegistry(config.ACPConfig{Endpoints: []config.ACPEndpointConfig{{ID: "fake", Command: "/bin/sh", Args: []string{"-c", blockingScript}}}})
	if err != nil {
		t.Fatal(err)
	}
	acpManager := acp.NewSessionManager(registry)
	am := NewManager(config.Config{Agents: config.AgentsConfig{MaxConcurrent: 1, Profiles: []config.AgentProfileConfig{{ID: "worker", Provider: "acp", EndpointID: "fake", PermissionPolicy: "manual"}}}}, acpManager)
	w := &workspace.Workspace{ID: "w", Root: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := am.Run(ctx, w, "worker", "hold")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := am.Run(ctx, w, "worker", "second"); err == nil || !strings.Contains(err.Error(), "concurrency") {
		t.Fatalf("second turn was not blocked: %v", err)
	}
	if err := os.WriteFile(release, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	shown := waitAgentTerminal(t, ctx, am, w.ID, first.ID)
	if shown.Agent.Status != StatusReady {
		t.Fatalf("released turn=%+v", shown)
	}
	acpManager.CloseAll()
}

func TestACPResponseUTF8TruncationAndWorkspaceClose(t *testing.T) {
	response, truncated := acpResponse([]acp.Event{{Type: "agent_message_chunk", Update: mustAgentJSON(map[string]any{"content": strings.Repeat("🙂", 1<<19)})}})
	if !truncated || len([]byte(response)) > 1<<20 || !utf8.ValidString(response) {
		t.Fatalf("response truncation len=%d truncated=%v utf8=%v", len([]byte(response)), truncated, utf8.ValidString(response))
	}
	am, w, acpManager, _ := agentFixture(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	record, err := am.Run(ctx, w, "worker", "close me")
	if err != nil {
		t.Fatal(err)
	}
	_ = waitAgentTerminal(t, ctx, am, w.ID, record.ID)
	am.CloseWorkspace(w.ID)
	if _, records, err := am.List(w); err != nil || len(records) != 0 {
		t.Fatalf("CloseWorkspace retained records=%+v", records)
	}
	am.CloseAll()
	acpManager.CloseAll()
}

func mustAgentJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
