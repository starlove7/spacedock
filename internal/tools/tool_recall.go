package tools

import (
	"context"
	"encoding/json"
	"github.com/starlove7/spacedock/internal/policy"
	"github.com/starlove7/spacedock/internal/recall"
	"github.com/starlove7/spacedock/internal/workspace"
)

func RecallTools(m *workspace.Manager, r *recall.Manager) []Tool {
	auth := func(id string, p policy.Permission) (*workspace.Workspace, error) {
		w, e := m.Get(id)
		if e != nil {
			return nil, NotFound(e)
		}
		if !policy.ContainsPermission(w.Permissions, p) {
			return nil, Denied(string(p), w.ID)
		}
		return w, nil
	}
	return []Tool{
		&BasicTool{"recall_search", "Recall search", Schema(map[string]any{"workspace_id": StringProp(), "query": StringProp(), "max_results": IntProp(0, 100)}, []string{"workspace_id", "query"}), func(_ context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID string `json:"workspace_id"`
				Query       string `json:"query"`
				MaxResults  int    `json:"max_results"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := auth(x.WorkspaceID, policy.PermissionRecallRead)
			if e != nil {
				return nil, e
			}
			return r.Search(w.RootID, x.Query, x.MaxResults)
		}},
		&BasicTool{"recall_read", "Recall read", Schema(map[string]any{"workspace_id": StringProp(), "path": StringProp()}, []string{"workspace_id", "path"}), func(_ context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID string `json:"workspace_id"`
				Path        string `json:"path"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := auth(x.WorkspaceID, policy.PermissionRecallRead)
			if e != nil {
				return nil, e
			}
			return r.Read(w.RootID, x.Path)
		}},
		&BasicTool{"recall_write", "Recall write", Schema(map[string]any{"workspace_id": StringProp(), "path": StringProp(), "mode": map[string]any{"type": "string", "enum": []string{"create", "replace", "append"}}, "content": StringProp()}, []string{"workspace_id", "path", "mode", "content"}), func(_ context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID string `json:"workspace_id"`
				Path        string `json:"path"`
				Mode        string `json:"mode"`
				Content     string `json:"content"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := auth(x.WorkspaceID, policy.PermissionRecallWrite)
			if e != nil {
				return nil, e
			}
			return r.Write(w.RootID, x.Path, x.Mode, x.Content)
		}},
		&BasicTool{"recall_delete", "Recall delete", Schema(map[string]any{"workspace_id": StringProp(), "path": StringProp()}, []string{"workspace_id", "path"}), func(_ context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID string `json:"workspace_id"`
				Path        string `json:"path"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := auth(x.WorkspaceID, policy.PermissionRecallWrite)
			if e != nil {
				return nil, e
			}
			if e = r.Delete(w.RootID, x.Path); e != nil {
				return nil, e
			}
			return map[string]any{"deleted": true}, nil
		}},
	}
}
