package tools

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/starlove7/spacedock/internal/git"
	"github.com/starlove7/spacedock/internal/policy"
	"github.com/starlove7/spacedock/internal/workspace"
)

func GitTools(m *workspace.Manager, s *git.Service) []Tool {
	mapErr := func(e error) error {
		if errors.Is(e, git.ErrNotRepository) {
			return New("NOT_GIT_REPOSITORY", "workspace is not a git repository", "validation", false, nil)
		}
		return Execution(e)
	}
	get := func(id string) (*workspace.Workspace, error) {
		w, e := m.Get(id)
		if e != nil {
			return nil, NotFound(e)
		}
		if !policy.ContainsPermission(w.Permissions, policy.PermissionGitRead) {
			return nil, Denied(string(policy.PermissionGitRead), w.ID)
		}
		return w, nil
	}
	return []Tool{
		&BasicTool{"git_status", "Git status", Schema(map[string]any{"workspace_id": StringProp()}, []string{"workspace_id"}), func(ctx context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID string `json:"workspace_id"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := get(x.WorkspaceID)
			if e != nil {
				return nil, e
			}
			r, e := s.Status(ctx, w)
			if e != nil {
				return nil, mapErr(e)
			}
			return r, nil
		}},
		&BasicTool{"git_diff", "Git diff", Schema(map[string]any{"workspace_id": StringProp(), "path": StringProp(), "staged": BoolProp(), "max_bytes": IntProp(0, 4<<20)}, []string{"workspace_id"}), func(ctx context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID string `json:"workspace_id"`
				Path        string `json:"path"`
				Staged      bool   `json:"staged"`
				MaxBytes    int    `json:"max_bytes"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := get(x.WorkspaceID)
			if e != nil {
				return nil, e
			}
			r, e := s.Diff(ctx, w, x.Path, x.Staged, x.MaxBytes)
			if e != nil {
				return nil, mapErr(e)
			}
			return r, nil
		}},
		&BasicTool{"git_log", "Git log", Schema(map[string]any{"workspace_id": StringProp(), "limit": IntProp(0, 100)}, []string{"workspace_id"}), func(ctx context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID string `json:"workspace_id"`
				Limit       int    `json:"limit"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := get(x.WorkspaceID)
			if e != nil {
				return nil, e
			}
			r, e := s.Log(ctx, w, x.Limit)
			if e != nil {
				return nil, mapErr(e)
			}
			return r, nil
		}},
	}
}
