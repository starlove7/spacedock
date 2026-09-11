package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/starlove7/spacedock/internal/app"
	"github.com/starlove7/spacedock/internal/config"
	"github.com/starlove7/spacedock/internal/tools"
)

func TestFileToolsSensitivePathStructuredErrors(t *testing.T) {
	root := t.TempDir()
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "normal.txt"), []byte("normal\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := app.New(config.Config{
		StateDir: stateDir,
		AllowedRoots: []config.RootConfig{{
			ID:          "r",
			Path:        root,
			Permissions: []string{"workspace.manage", "fs.read", "fs.write"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)

	opened, err := a.Tools.Call(context.Background(), "workspace_open", json.RawMessage(`{"root_id":"r"}`))
	if err != nil {
		t.Fatal(err)
	}
	openedMap, ok := opened.(map[string]any)
	if !ok {
		t.Fatalf("workspace_open result type=%T, value=%v", opened, opened)
	}
	workspaceID, ok := openedMap["workspace_id"].(string)
	if !ok || workspaceID == "" {
		t.Fatalf("workspace_open missing workspace_id: %#v", openedMap)
	}

	assertSensitiveToolError := func(t *testing.T, tool string, request any) {
		t.Helper()
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		_, err = a.Tools.Call(context.Background(), tool, body)
		var structured *tools.Error
		if !errors.As(err, &structured) {
			t.Fatalf("tool=%s error=%v, want *tools.Error", tool, err)
		}
		if structured.Code != "PERMISSION_DENIED" || structured.Message != "sensitive path access denied" || structured.Category != "permission" || structured.Retryable {
			t.Fatalf("tool=%s structured error=%+v", tool, structured)
		}
		if structured.Details["reason"] != "sensitive_path" {
			t.Fatalf("tool=%s details=%v, want reason=sensitive_path", tool, structured.Details)
		}
	}

	assertSensitiveToolError(t, "read_file", map[string]any{
		"workspace_id": workspaceID,
		"path":         ".env",
	})
	assertSensitiveToolError(t, "file_edit", map[string]any{
		"workspace_id": workspaceID,
		"action":       "write",
		"path":         "credentials.json",
		"content":      "should not exist",
	})
	if _, err := os.Lstat(filepath.Join(root, "credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("blocked credentials.json write created file: %v", err)
	}

	if _, err := a.Tools.Call(context.Background(), "read_file", mustJSON(map[string]any{
		"workspace_id": workspaceID,
		"path":         "normal.txt",
	})); err != nil {
		t.Fatalf("normal read failed: %v", err)
	}
	if _, err := a.Tools.Call(context.Background(), "file_edit", mustJSON(map[string]any{
		"workspace_id": workspaceID,
		"action":       "write",
		"path":         "normal-new.txt",
		"content":      "normal write",
	})); err != nil {
		t.Fatalf("normal write failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "normal-new.txt")); err != nil {
		t.Fatalf("normal write did not create file: %v", err)
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
