package tools

import (
	"context"
	"encoding/json"
	"github.com/starlove7/spacedock/internal/acp"
	"github.com/starlove7/spacedock/internal/policy"
	"github.com/starlove7/spacedock/internal/workspace"
	"time"
)

func ACPTools(m *workspace.Manager, a *acp.SessionManager, r *acp.Registry) []Tool {
	auth := func(id string) (*workspace.Workspace, error) {
		w, e := m.Get(id)
		if e != nil {
			return nil, NotFound(e)
		}
		if !policy.ContainsPermission(w.Permissions, policy.PermissionACPConnect) {
			return nil, Denied(string(policy.PermissionACPConnect), id)
		}
		return w, nil
	}
	return []Tool{&BasicTool{"acp_list", "List ACP endpoints", Schema(map[string]any{"workspace_id": StringProp()}, []string{"workspace_id"}), func(_ context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		if _, e := auth(x.WorkspaceID); e != nil {
			return nil, e
		}
		return map[string]any{"endpoints": r.List()}, nil
	}}, &BasicTool{"acp_connect", "Create an ACP session", Schema(map[string]any{"workspace_id": StringProp(), "endpoint_id": StringProp(), "mode_id": StringProp(), "config_options": map[string]any{"type": "object", "additionalProperties": map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "boolean"}}}}, "permission_policy": map[string]any{"type": "string", "enum": []string{"manual", "allow_once"}}}, []string{"workspace_id", "endpoint_id"}), func(ctx context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID      string         `json:"workspace_id"`
			EndpointID       string         `json:"endpoint_id"`
			ModeID           string         `json:"mode_id"`
			ConfigOptions    map[string]any `json:"config_options"`
			PermissionPolicy string         `json:"permission_policy"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		w, e := auth(x.WorkspaceID)
		if e != nil {
			return nil, e
		}
		return a.Connect(ctx, w, acp.ConnectOptions{EndpointID: x.EndpointID, ModeID: x.ModeID, ConfigOptions: x.ConfigOptions, PermissionPolicy: x.PermissionPolicy})
	}}, &BasicTool{"acp_capabilities", "Show ACP capabilities", Schema(map[string]any{"workspace_id": StringProp(), "session_id": StringProp()}, []string{"workspace_id", "session_id"}), func(_ context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID string `json:"workspace_id"`
			SessionID   string `json:"session_id"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		if _, e := auth(x.WorkspaceID); e != nil {
			return nil, e
		}
		return a.Get(x.WorkspaceID, x.SessionID)
	}}, &BasicTool{"acp_disconnect", "Disconnect ACP session", Schema(map[string]any{"workspace_id": StringProp(), "session_id": StringProp()}, []string{"workspace_id", "session_id"}), func(_ context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID string `json:"workspace_id"`
			SessionID   string `json:"session_id"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		if _, e := auth(x.WorkspaceID); e != nil {
			return nil, e
		}
		if e := a.Disconnect(x.WorkspaceID, x.SessionID); e != nil {
			return nil, e
		}
		return map[string]any{"session_id": x.SessionID, "disconnected": true}, nil
	}}, &BasicTool{"acp_prompt", "Start an ACP prompt", Schema(map[string]any{"workspace_id": StringProp(), "session_id": StringProp(), "prompt": StringProp()}, []string{"workspace_id", "session_id", "prompt"}), func(_ context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID string `json:"workspace_id"`
			SessionID   string `json:"session_id"`
			Prompt      string `json:"prompt"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		if _, e := auth(x.WorkspaceID); e != nil {
			return nil, e
		}
		return a.StartPrompt(context.Background(), x.WorkspaceID, x.SessionID, x.Prompt)
	}}, &BasicTool{"acp_events", "Read ACP prompt events", Schema(map[string]any{"workspace_id": StringProp(), "run_id": StringProp(), "after_seq": IntProp(0, 1<<31), "limit": IntProp(1, 200), "wait_ms": IntProp(0, 25000)}, []string{"workspace_id", "run_id"}), func(ctx context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID string `json:"workspace_id"`
			RunID       string `json:"run_id"`
			After       uint64 `json:"after_seq"`
			Limit       int    `json:"limit"`
			WaitMS      int    `json:"wait_ms"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		if _, e := auth(x.WorkspaceID); e != nil {
			return nil, e
		}
		return a.PromptEvents(ctx, x.WorkspaceID, x.RunID, x.After, x.Limit, time.Duration(x.WaitMS)*time.Millisecond)
	}}, &BasicTool{"acp_cancel", "Cancel ACP prompt", Schema(map[string]any{"workspace_id": StringProp(), "session_id": StringProp(), "run_id": StringProp()}, []string{"workspace_id", "session_id"}), func(ctx context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID string `json:"workspace_id"`
			SessionID   string `json:"session_id"`
			RunID       string `json:"run_id"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		if _, e := auth(x.WorkspaceID); e != nil {
			return nil, e
		}
		return map[string]any{"cancelled": true}, a.CancelPrompt(ctx, x.WorkspaceID, x.SessionID, x.RunID)
	}}, &BasicTool{"acp_interactions", "List ACP permission interactions", Schema(map[string]any{"workspace_id": StringProp(), "session_id": StringProp(), "pending_only": BoolProp()}, []string{"workspace_id"}), func(_ context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID string `json:"workspace_id"`
			SessionID   string `json:"session_id"`
			PendingOnly *bool  `json:"pending_only"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		if _, e := auth(x.WorkspaceID); e != nil {
			return nil, e
		}
		pending := true
		if x.PendingOnly != nil {
			pending = *x.PendingOnly
		}
		return a.Interactions(x.WorkspaceID, x.SessionID, pending)
	}}, &BasicTool{"acp_respond", "Respond to ACP permission interaction", Schema(map[string]any{"workspace_id": StringProp(), "interaction_id": StringProp(), "option_id": StringProp(), "cancel": BoolProp()}, []string{"workspace_id", "interaction_id"}), func(_ context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID   string `json:"workspace_id"`
			InteractionID string `json:"interaction_id"`
			OptionID      string `json:"option_id"`
			Cancel        bool   `json:"cancel"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		if _, e := auth(x.WorkspaceID); e != nil {
			return nil, e
		}
		return a.RespondInteraction(x.WorkspaceID, x.InteractionID, x.OptionID, x.Cancel)
	}}}
}
