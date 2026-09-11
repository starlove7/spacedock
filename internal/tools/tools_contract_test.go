package tools_test

import (
	"encoding/json"
	"github.com/starlove7/spacedock/internal/app"
	"github.com/starlove7/spacedock/internal/config"
	"reflect"
	"strings"
	"testing"
)

func TestAppRegistersAllMCPToolsAndStrictSchemas(t *testing.T) {
	a, e := app.New(config.Config{StateDir: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	xs := a.Tools.Tools()
	if len(xs) != 33 {
		t.Fatalf("tools=%d", len(xs))
	}
	want := []string{
		"acp_cancel", "acp_capabilities", "acp_connect", "acp_disconnect", "acp_events", "acp_interactions", "acp_list", "acp_prompt", "acp_respond",
		"agent_continue", "agent_list", "agent_run", "agent_show", "agent_stop",
		"exec_command", "file_edit", "git_diff", "git_log", "git_status",
		"list_dir", "list_files", "read_file", "recall_delete", "recall_read",
		"recall_search", "recall_write", "search_text", "session_act", "session_observe", "workspace_close", "workspace_discard", "workspace_list", "workspace_open",
	}
	got := make([]string, 0, len(xs))
	for _, x := range xs {
		got = append(got, x.Name())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tool names=%v, want=%v", got, want)
	}
	for _, x := range xs {
		var s map[string]any
		if e := json.Unmarshal(x.InputSchema(), &s); e != nil || s["type"] != "object" || s["additionalProperties"] != false {
			t.Errorf("%s schema=%s", x.Name(), x.InputSchema())
		}
	}
	for _, tc := range []struct {
		name     string
		required []string
	}{
		{"workspace_open", []string{"root_id"}},
		{"workspace_discard", []string{"workspace_id"}},
		{"acp_prompt", []string{"workspace_id", "session_id", "prompt"}},
		{"acp_events", []string{"workspace_id", "run_id"}},
		{"acp_cancel", []string{"workspace_id", "session_id"}},
		{"acp_respond", []string{"workspace_id", "interaction_id"}},
		{"agent_run", []string{"workspace_id", "profile_id", "prompt"}},
		{"agent_show", []string{"workspace_id", "agent_id"}},
	} {
		tool, ok := a.Tools.Get(tc.name)
		if !ok {
			t.Fatalf("missing tool %s", tc.name)
		}
		var schema struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
			t.Fatal(err)
		}
		for _, field := range tc.required {
			found := false
			for _, required := range schema.Required {
				if required == field {
					found = true
				}
			}
			if !found {
				t.Errorf("%s missing required field %s", tc.name, field)
			}
		}
	}
	for _, tc := range []struct {
		name string
		path string
		want any
	}{
		{"acp_connect", "properties.permission_policy.enum", []any{"manual", "allow_once"}},
		{"agent_show", "properties.wait_ms.minimum", float64(0)},
		{"agent_show", "properties.wait_ms.maximum", float64(25000)},
		{"acp_events", "properties.limit.maximum", float64(200)},
		{"acp_events", "properties.wait_ms.maximum", float64(25000)},
	} {
		tool, _ := a.Tools.Get(tc.name)
		var schema map[string]any
		_ = json.Unmarshal(tool.InputSchema(), &schema)
		var value any = schema
		for _, part := range strings.Split(tc.path, ".") {
			m, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("%s missing schema path %s", tc.name, tc.path)
			}
			value, ok = m[part]
			if !ok {
				t.Fatalf("%s missing schema path %s", tc.name, tc.path)
			}
		}
		if !reflect.DeepEqual(value, tc.want) {
			t.Errorf("%s schema %s=%v want=%v", tc.name, tc.path, value, tc.want)
		}
	}
}
