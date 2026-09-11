package tools

import (
	"context"
	"encoding/json"
	"github.com/starlove7/spacedock/internal/command"
	"github.com/starlove7/spacedock/internal/policy"
	"github.com/starlove7/spacedock/internal/workspace"
)

func CommandTools(m *workspace.Manager, c *command.Manager) []Tool {
	return []Tool{
		&BasicTool{"exec_command", "Execute command", Schema(map[string]any{"workspace_id": StringProp(), "command": StringProp(), "working_directory": StringProp(), "timeout_ms": IntProp(0, 86400000), "yield_ms": IntProp(0, 30000), "stdin": StringProp(), "keep_stdin": BoolProp()}, []string{"workspace_id", "command"}), func(ctx context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID      string `json:"workspace_id"`
				Command          string `json:"command"`
				WorkingDirectory string `json:"working_directory"`
				TimeoutMS        int    `json:"timeout_ms"`
				YieldMS          int    `json:"yield_ms"`
				Stdin            string `json:"stdin"`
				KeepStdin        bool   `json:"keep_stdin"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := m.Get(x.WorkspaceID)
			if e != nil {
				return nil, NotFound(e)
			}
			if !policy.ContainsPermission(w.Permissions, policy.PermissionCommandExecute) {
				return nil, Denied(string(policy.PermissionCommandExecute), w.ID)
			}
			return c.Exec(ctx, w, command.ExecRequest{Command: x.Command, WorkingDirectory: x.WorkingDirectory, TimeoutMS: x.TimeoutMS, YieldMS: x.YieldMS, Stdin: x.Stdin, KeepStdin: x.KeepStdin})
		}},
		&BasicTool{"session_observe", "Observe command", Schema(map[string]any{"workspace_id": StringProp(), "session_id": StringProp(), "max_bytes": IntProp(0, 2<<20)}, []string{"workspace_id", "session_id"}), func(_ context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID string `json:"workspace_id"`
				SessionID   string `json:"session_id"`
				MaxBytes    int    `json:"max_bytes"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := m.Get(x.WorkspaceID)
			if e != nil {
				return nil, NotFound(e)
			}
			if !policy.ContainsPermission(w.Permissions, policy.PermissionCommandExecute) {
				return nil, Denied(string(policy.PermissionCommandExecute), w.ID)
			}
			return c.Observe(w.ID, x.SessionID, x.MaxBytes)
		}},
		&BasicTool{"session_act", "Act on command", Schema(map[string]any{"workspace_id": StringProp(), "session_id": StringProp(), "action": map[string]any{"type": "string", "enum": []string{"stdin", "interrupt", "terminate"}}, "chars": StringProp(), "max_bytes": IntProp(0, 2<<20)}, []string{"workspace_id", "session_id", "action"}), func(_ context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID string `json:"workspace_id"`
				SessionID   string `json:"session_id"`
				Action      string `json:"action"`
				Chars       string `json:"chars"`
				MaxBytes    int    `json:"max_bytes"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := m.Get(x.WorkspaceID)
			if e != nil {
				return nil, NotFound(e)
			}
			if !policy.ContainsPermission(w.Permissions, policy.PermissionCommandExecute) {
				return nil, Denied(string(policy.PermissionCommandExecute), w.ID)
			}
			return c.ActWithMax(w.ID, x.SessionID, x.Action, x.Chars, x.MaxBytes)
		}},
	}
}
