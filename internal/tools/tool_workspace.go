package tools

import (
	"context"
	"encoding/json"
	"github.com/starlove7/spacedock/internal/policy"
	"github.com/starlove7/spacedock/internal/workspace"
)

func workspaceResult(w *workspace.Workspace) map[string]any {
	return map[string]any{"workspace_id": w.ID, "root_id": w.RootID, "root_name": w.RootName, "project_path": w.ProjectPath, "mode": w.Mode, "permissions": w.Permissions, "instructions": w.Instructions, "recall_context": w.RecallContext, "opened_at": w.OpenedAt, "source_root": w.SourceRoot, "base_ref": w.BaseRef, "base_sha": w.BaseSHA, "source_dirty": w.SourceDirty}
}
func WorkspaceTools(m *workspace.Manager) []Tool {
	auth := func(id string) (*workspace.Workspace, error) {
		w, e := m.Get(id)
		if e != nil {
			return nil, NotFound(e)
		}
		if !policy.ContainsPermission(w.Permissions, policy.PermissionWorkspaceManage) {
			return nil, Denied(string(policy.PermissionWorkspaceManage), id)
		}
		return w, nil
	}
	return []Tool{&BasicTool{"workspace_list", "List configured roots and active workspaces", Schema(map[string]any{}, nil), func(context.Context, json.RawMessage) (Result, error) {
		return map[string]any{"roots": m.ListRoots(), "workspaces": m.List()}, nil
	}}, &BasicTool{"workspace_open", "Open a checkout or managed Git worktree workspace", Schema(map[string]any{"root_id": StringProp(), "path": StringProp(), "mode": map[string]any{"type": "string", "enum": []string{"checkout", "worktree"}}, "base_ref": StringProp()}, []string{"root_id"}), func(_ context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			RootID  string `json:"root_id"`
			Path    string `json:"path"`
			Mode    string `json:"mode"`
			BaseRef string `json:"base_ref"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		w, e := m.Open(workspace.OpenOptions{RootID: x.RootID, Path: x.Path, Mode: x.Mode, BaseRef: x.BaseRef})
		if e != nil {
			return nil, e
		}
		return workspaceResult(w), nil
	}}, &BasicTool{"workspace_close", "Close workspace", Schema(map[string]any{"workspace_id": StringProp()}, []string{"workspace_id"}), func(_ context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		if _, e := auth(x.WorkspaceID); e != nil {
			return nil, e
		}
		if e := m.Close(x.WorkspaceID); e != nil {
			return nil, e
		}
		return map[string]any{"workspace_id": x.WorkspaceID, "closed": true}, nil
	}}, &BasicTool{"workspace_discard", "Discard managed worktree", Schema(map[string]any{"workspace_id": StringProp(), "force": BoolProp()}, []string{"workspace_id"}), func(_ context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID string `json:"workspace_id"`
			Force       bool   `json:"force"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		if _, e := auth(x.WorkspaceID); e != nil {
			return nil, e
		}
		if e := m.Discard(x.WorkspaceID, x.Force); e != nil {
			return nil, e
		}
		return map[string]any{"workspace_id": x.WorkspaceID, "discarded": true}, nil
	}}}
}
