package tools

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/starlove7/spacedock/internal/filesystem"
	"github.com/starlove7/spacedock/internal/policy"
	"github.com/starlove7/spacedock/internal/workspace"
)

func FileTools(m *workspace.Manager, s *filesystem.Service) []Tool {
	filesystemError := func(err error) error {
		if errors.Is(err, policy.ErrSensitivePath) {
			return SensitivePathDenied()
		}
		return err
	}
	get := func(id string, p policy.Permission) (*workspace.Workspace, error) {
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
		&BasicTool{"read_file", "Read file", Schema(map[string]any{"workspace_id": StringProp(), "path": StringProp(), "start_line": IntProp(0, 5000), "max_lines": IntProp(0, 5000), "max_bytes": IntProp(0, 4<<20)}, []string{"workspace_id", "path"}), func(_ context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID string `json:"workspace_id"`
				Path        string `json:"path"`
				StartLine   int    `json:"start_line"`
				MaxLines    int    `json:"max_lines"`
				MaxBytes    int    `json:"max_bytes"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := get(x.WorkspaceID, policy.PermissionFSRead)
			if e != nil {
				return nil, e
			}
			r, e := s.ReadFile(w, x.Path, x.StartLine, x.MaxLines, x.MaxBytes)
			return r, filesystemError(e)
		}},
		&BasicTool{"list_dir", "List directory", Schema(map[string]any{"workspace_id": StringProp(), "path": StringProp(), "max_entries": IntProp(0, 5000)}, []string{"workspace_id"}), func(_ context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID string `json:"workspace_id"`
				Path        string `json:"path"`
				MaxEntries  int    `json:"max_entries"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := get(x.WorkspaceID, policy.PermissionFSRead)
			if e != nil {
				return nil, e
			}
			r, e := s.ListDir(w, x.Path, x.MaxEntries)
			return r, filesystemError(e)
		}},
		&BasicTool{"list_files", "List files", Schema(map[string]any{"workspace_id": StringProp(), "path": StringProp(), "pattern": StringProp(), "max_depth": IntProp(0, 32), "max_entries": IntProp(0, 5000)}, []string{"workspace_id"}), func(_ context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID string `json:"workspace_id"`
				Path        string `json:"path"`
				Pattern     string `json:"pattern"`
				MaxDepth    int    `json:"max_depth"`
				MaxEntries  int    `json:"max_entries"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := get(x.WorkspaceID, policy.PermissionFSRead)
			if e != nil {
				return nil, e
			}
			r, e := s.ListFiles(w, x.Path, x.Pattern, x.MaxDepth, x.MaxEntries)
			return r, filesystemError(e)
		}},
		&BasicTool{"search_text", "Search text", Schema(map[string]any{"workspace_id": StringProp(), "path": StringProp(), "query": StringProp(), "regex": BoolProp(), "case_sensitive": BoolProp(), "max_results": IntProp(0, 1000)}, []string{"workspace_id", "query"}), func(_ context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID   string `json:"workspace_id"`
				Path          string `json:"path"`
				Query         string `json:"query"`
				Regex         bool   `json:"regex"`
				CaseSensitive bool   `json:"case_sensitive"`
				MaxResults    int    `json:"max_results"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := get(x.WorkspaceID, policy.PermissionFSRead)
			if e != nil {
				return nil, e
			}
			r, e := s.SearchText(w, x.Path, x.Query, x.Regex, x.CaseSensitive, x.MaxResults)
			return r, filesystemError(e)
		}},
		&BasicTool{"file_edit", "Edit file", Schema(map[string]any{"workspace_id": StringProp(), "action": map[string]any{"type": "string", "enum": []string{"write", "replace", "delete", "move"}}, "path": StringProp(), "content": StringProp(), "old_text": StringProp(), "new_text": StringProp(), "expected_matches": map[string]any{"type": "integer"}, "replace_all": BoolProp(), "new_path": StringProp(), "overwrite": BoolProp()}, []string{"workspace_id", "action", "path"}), func(_ context.Context, b json.RawMessage) (Result, error) {
			var x struct {
				WorkspaceID     string `json:"workspace_id"`
				Action          string `json:"action"`
				Path            string `json:"path"`
				Content         string `json:"content"`
				OldText         string `json:"old_text"`
				NewText         string `json:"new_text"`
				ExpectedMatches *int   `json:"expected_matches"`
				ReplaceAll      bool   `json:"replace_all"`
				NewPath         string `json:"new_path"`
				Overwrite       bool   `json:"overwrite"`
			}
			if e := Decode(b, &x); e != nil {
				return nil, Validation(e)
			}
			w, e := get(x.WorkspaceID, policy.PermissionFSWrite)
			if e != nil {
				return nil, e
			}
			em := 1
			if x.ExpectedMatches != nil {
				em = *x.ExpectedMatches
			}
			r, e := s.Edit(w, filesystem.EditRequest{Action: x.Action, Path: x.Path, Content: x.Content, OldText: x.OldText, NewText: x.NewText, ExpectedMatches: em, ReplaceAll: x.ReplaceAll, NewPath: x.NewPath, Overwrite: x.Overwrite})
			return r, filesystemError(e)
		}},
	}
}
