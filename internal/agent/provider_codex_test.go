package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/starlove7/spacedock/internal/config"
	"github.com/starlove7/spacedock/internal/workspace"
)

const fakeCodexScript = `#!/bin/sh
printf 'argv:%s\n' "$*" >> "$FAKE_CODEX_LOG"
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$FAKE_CODEX_LOG"
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*) printf '%s\n' "{\"id\":$id,\"result\":{}}";;
    *'"method":"thread/start"'*) printf '%s\n' "{\"id\":$id,\"result\":{\"thread\":{\"id\":\"thread-1\"}}}";;
    *'"method":"thread/resume"'*) printf '%s\n' "{\"id\":$id,\"result\":{\"thread\":{\"id\":\"thread-1\"}}}";;
    *'"method":"turn/start"'*)
      count=$(grep -c '"method":"turn/start"' "$FAKE_CODEX_LOG")
      printf '%s\n' "{\"id\":$id,\"result\":{\"turn\":{\"id\":\"turn-$count\"}}}"
      printf '%s\n' "{\"method\":\"turn/completed\",\"params\":{\"threadId\":\"thread-1\",\"turn\":{\"id\":\"turn-$count\",\"status\":\"completed\",\"items\":[{\"type\":\"agentMessage\",\"text\":\"codex-response-$count\"}]}}}"
      ;;
  esac
done`

func fakeCodexCommand(t *testing.T) (string, string) {
	t.Helper()
	d := t.TempDir()
	command := filepath.Join(d, "codex")
	logPath := filepath.Join(d, "codex.log")
	if err := os.WriteFile(command, []byte(fakeCodexScript), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CODEX_LOG", logPath)
	return command, logPath
}

func TestCodexCLIProviderRunContinueAndConfiguration(t *testing.T) {
	command, logPath := fakeCodexCommand(t)
	cfg := config.Config{Agents: config.AgentsConfig{
		MaxConcurrent: 1,
		Codex:         config.CodexProviderConfig{Command: command},
		Profiles: []config.AgentProfileConfig{{
			ID:           "codex-worker",
			Name:         "Codex Worker",
			Provider:     "codex",
			Instructions: "Follow the patch spec",
			Model:        "gpt-test",
			Effort:       "high",
			WriteMode:    "allowed",
		}},
	}}
	manager := NewManager(cfg, nil)
	w := &workspace.Workspace{ID: "w", Root: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	record, err := manager.Run(ctx, w, "codex-worker", "implement one")
	if err != nil {
		t.Fatal(err)
	}
	shown := waitAgentTerminal(t, ctx, manager, w.ID, record.ID)
	if shown.Agent.Provider != "codex" || shown.Agent.ProviderSessionID != "thread-1" || shown.Response != "codex-response-1" || shown.Agent.Status != StatusReady {
		t.Fatalf("first codex result=%+v", shown)
	}
	continued, err := manager.Continue(ctx, w.ID, record.ID, "continue two")
	if err != nil {
		t.Fatal(err)
	}
	if continued.ProviderSessionID != "thread-1" || continued.Status != StatusRunning {
		t.Fatalf("continued record=%+v", continued)
	}
	shown = waitAgentTerminal(t, ctx, manager, w.ID, record.ID)
	if shown.Response != "codex-response-2" || shown.Agent.ProviderSessionID != "thread-1" {
		t.Fatalf("second codex result=%+v", shown)
	}
	if _, err := manager.Stop(ctx, w.ID, record.ID); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(data)
	for _, expected := range []string{
		"argv:app-server",
		`"method":"thread/start"`,
		`"method":"thread/resume"`,
		`"sandbox":"workspace-write"`,
		`"type":"workspaceWrite"`,
		`"networkAccess":true`,
		`"model":"gpt-test"`,
		`"effort":"high"`,
		`Follow the patch spec\n\nTask:\nimplement one`,
		`continue two`,
	} {
		if !strings.Contains(logText, expected) {
			t.Errorf("Codex log missing %q:\n%s", expected, logText)
		}
	}
	if strings.Count(logText, `Follow the patch spec\n\nTask:\n`) != 1 {
		t.Fatalf("instructions were reapplied on continue:\n%s", logText)
	}
}

func TestCodexCommandResolutionAndEnvironment(t *testing.T) {
	command, _ := fakeCodexCommand(t)
	resolved, err := resolveCodexCommand(command)
	if err != nil || resolved != command {
		t.Fatalf("resolve absolute command=%q err=%v", resolved, err)
	}
	if _, err := resolveCodexCommand(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing Codex command accepted")
	}
	env := codexEnvironment([]string{"PATH=/bin", "CODEX_INTERNAL_ORIGINATOR_OVERRIDE=bad", "HOME=/tmp"})
	if strings.Contains(strings.Join(env, "\n"), "CODEX_INTERNAL_ORIGINATOR_OVERRIDE=") {
		t.Fatalf("originator override leaked into Codex environment: %v", env)
	}
}
