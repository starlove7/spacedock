package tools

import (
	"context"
	"encoding/json"
	"github.com/starlove7/spacedock/internal/agent"
	"github.com/starlove7/spacedock/internal/policy"
	"github.com/starlove7/spacedock/internal/workspace"
	"time"
)

func AgentTools(wm *workspace.Manager, am *agent.Manager) []Tool {
	auth := func(id string) (*workspace.Workspace, error) {
		w, e := wm.Get(id)
		if e != nil {
			return nil, e
		}
		if !policy.ContainsPermission(w.Permissions, policy.PermissionAgentExecute) {
			return nil, Denied(string(policy.PermissionAgentExecute), id)
		}
		return w, nil
	}
	return []Tool{&BasicTool{"agent_list", "List bounded workers", Schema(map[string]any{"workspace_id": StringProp()}, []string{"workspace_id"}), func(_ context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		w, e := auth(x.WorkspaceID)
		if e != nil {
			return nil, e
		}
		p, a, e := am.List(w)
		if e != nil {
			return nil, e
		}
		return map[string]any{"profiles": p, "agents": a}, nil
	}}, &BasicTool{"agent_run", "Run a bounded worker; host retains final judgment", Schema(map[string]any{"workspace_id": StringProp(), "profile_id": StringProp(), "prompt": StringProp()}, []string{"workspace_id", "profile_id", "prompt"}), func(ctx context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID string `json:"workspace_id"`
			ProfileID   string `json:"profile_id"`
			Prompt      string `json:"prompt"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		w, e := auth(x.WorkspaceID)
		if e != nil {
			return nil, e
		}
		return am.Run(ctx, w, x.ProfileID, x.Prompt)
	}}, &BasicTool{"agent_show", "Observe worker; not acceptance", Schema(map[string]any{"workspace_id": StringProp(), "agent_id": StringProp(), "wait_ms": IntProp(0, 25000)}, []string{"workspace_id", "agent_id"}), func(ctx context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID string `json:"workspace_id"`
			AgentID     string `json:"agent_id"`
			WaitMS      int    `json:"wait_ms"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		if _, e := auth(x.WorkspaceID); e != nil {
			return nil, e
		}
		return am.Show(ctx, x.WorkspaceID, x.AgentID, time.Duration(x.WaitMS)*time.Millisecond)
	}}, &BasicTool{"agent_continue", "Continue the same worker provider session", Schema(map[string]any{"workspace_id": StringProp(), "agent_id": StringProp(), "prompt": StringProp()}, []string{"workspace_id", "agent_id", "prompt"}), func(ctx context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID string `json:"workspace_id"`
			AgentID     string `json:"agent_id"`
			Prompt      string `json:"prompt"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		if _, e := auth(x.WorkspaceID); e != nil {
			return nil, e
		}
		return am.Continue(ctx, x.WorkspaceID, x.AgentID, x.Prompt)
	}}, &BasicTool{"agent_stop", "Stop a worker turn", Schema(map[string]any{"workspace_id": StringProp(), "agent_id": StringProp()}, []string{"workspace_id", "agent_id"}), func(ctx context.Context, b json.RawMessage) (Result, error) {
		var x struct {
			WorkspaceID string `json:"workspace_id"`
			AgentID     string `json:"agent_id"`
		}
		if e := Decode(b, &x); e != nil {
			return nil, Validation(e)
		}
		if _, e := auth(x.WorkspaceID); e != nil {
			return nil, e
		}
		return am.Stop(ctx, x.WorkspaceID, x.AgentID)
	}}}
}
