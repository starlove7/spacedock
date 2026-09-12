package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/starlove7/spacedock/internal/config"
	"github.com/starlove7/spacedock/internal/workspace"
)

func validCodexMarkdown(id, name, body string) string {
	return "---\nschema: spacedock-agent/v1\nid: " + id + "\nname: " + name + "\nprovider: codex\nmodel: o3\neffort: high\n---\n" + body + "\n"
}

func TestParseMarkdownProfileCodexAndCRLF(t *testing.T) {
	data := []byte(validCodexMarkdown("codex", "Codex", "Use care."))
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"LF", data}, {"CRLF", []byte(strings.ReplaceAll(string(data), "\n", "\r\n"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := parseMarkdownProfile(tc.data, "test", "profile.md", config.Config{})
			if err != nil || p.Instructions != "Use care." || p.WriteMode != "read_only" {
				t.Fatalf("profile=%+v err=%v", p, err)
			}
		})
	}
}

func TestParseMarkdownProfileACP(t *testing.T) {
	cfg := config.Config{ACP: config.ACPConfig{Endpoints: []config.ACPEndpointConfig{{ID: "copilot"}}}}
	p, err := parseMarkdownProfile([]byte("---\nschema: spacedock-agent/v1\nid: copilot\nprovider: acp\nendpoint_id: copilot\n---\nhello"), "test", "p.md", cfg)
	if err != nil || p.PermissionPolicy != "manual" {
		t.Fatalf("profile=%+v err=%v", p, err)
	}
}

func TestParseMarkdownProfileRejectsInvalidForms(t *testing.T) {
	valid := validCodexMarkdown("id", "Name", "body")
	cases := map[string][]byte{
		"invalid UTF8": {0xff}, "missing opening": []byte("x\n---"), "missing closing": []byte("---\nid: id"),
		"unknown key":  []byte(strings.Replace(valid, "model: o3", "instructions: nope", 1)),
		"wrong schema": []byte(strings.Replace(valid, "spacedock-agent/v1", "wrong", 1)),
		"missing id":   []byte(strings.Replace(valid, "id: id\n", "", 1)), "missing provider": []byte(strings.Replace(valid, "provider: codex\n", "", 1)),
		"malformed YAML": []byte("---\nschema: [\n---\nbody"),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseMarkdownProfile(data, "test", "p.md", config.Config{}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func writeProfile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMarkdownProfilesGuards(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		got, err := loadMarkdownProfiles(filepath.Join(t.TempDir(), "none"), "x", config.Config{})
		if err != nil || len(got) != 0 {
			t.Fatalf("got=%v err=%v", got, err)
		}
	})
	t.Run("ignore txt", func(t *testing.T) {
		d := t.TempDir()
		writeProfile(t, d, "x.txt", validCodexMarkdown("x", "X", "b"))
		got, err := loadMarkdownProfiles(d, "x", config.Config{})
		if err != nil || len(got) != 0 {
			t.Fatalf("got=%v err=%v", got, err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		d := t.TempDir()
		target := filepath.Join(d, "target")
		writeProfile(t, d, "target", validCodexMarkdown("x", "X", "b"))
		if err := os.Symlink(target, filepath.Join(d, "x.md")); err != nil {
			t.Fatal(err)
		}
		if _, err := loadMarkdownProfiles(d, "x", config.Config{}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("directory", func(t *testing.T) {
		d := t.TempDir()
		if err := os.Mkdir(filepath.Join(d, "x.md"), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := loadMarkdownProfiles(d, "x", config.Config{}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("oversized", func(t *testing.T) {
		d := t.TempDir()
		writeProfile(t, d, "x.md", strings.Repeat("x", maxMarkdownFileSize+1))
		if _, err := loadMarkdownProfiles(d, "x", config.Config{}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("too many", func(t *testing.T) {
		d := t.TempDir()
		for i := 0; i < 129; i++ {
			writeProfile(t, d, strings.Repeat("x", i)+".md", validCodexMarkdown("x"+strings.Repeat("a", i), "X", "b"))
		}
		if _, err := loadMarkdownProfiles(d, "x", config.Config{}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		d := t.TempDir()
		writeProfile(t, d, "a.md", validCodexMarkdown("same", "A", "a"))
		writeProfile(t, d, "b.md", validCodexMarkdown("same", "B", "b"))
		if _, err := loadMarkdownProfiles(d, "x", config.Config{}); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestResolveProfilesMergeCollisionSortAndHotReload(t *testing.T) {
	d := t.TempDir()
	global := filepath.Join(d, "agents")
	local := filepath.Join(d, "root", ".spacedock", "agents")
	if err := os.MkdirAll(global, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(local, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := config.AgentProfileConfig{ID: "legacy", Provider: "codex", Name: "Legacy"}
	legacySame := config.AgentProfileConfig{ID: "same", Provider: "codex", Name: "Legacy Same"}
	cfg := config.Config{StateDir: d, Agents: config.AgentsConfig{Profiles: []config.AgentProfileConfig{legacy, legacySame}}}
	writeProfile(t, global, "same.md", validCodexMarkdown("same", "Global", "g"))
	writeProfile(t, global, "new.md", validCodexMarkdown("global-new", "New", "n"))
	writeProfile(t, local, "local.md", validCodexMarkdown("local-new", "Local", "l"))
	profiles, err := resolveProfiles(cfg, filepath.Join(d, "root"))
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 4 || profiles[0].ID != "global-new" || profiles[1].ID != "legacy" || profiles[2].ID != "local-new" || profiles[3].ID != "same" || profiles[3].Name != "Global" || profiles[3].Instructions != "g" {
		t.Fatalf("profiles=%+v", profiles)
	}
	writeProfile(t, global, "same.md", validCodexMarkdown("same", "Changed", "changed"))
	profiles, err = resolveProfiles(cfg, filepath.Join(d, "root"))
	if err != nil || profiles[3].Name != "Changed" || profiles[3].Instructions != "changed" {
		t.Fatalf("profiles=%+v err=%v", profiles, err)
	}
	writeProfile(t, local, "legacy.md", validCodexMarkdown("legacy", "L", "l"))
	if _, err = resolveProfiles(cfg, filepath.Join(d, "root")); err == nil {
		t.Fatal("legacy collision accepted")
	}
	os.Remove(filepath.Join(local, "legacy.md"))
	writeProfile(t, local, "same.md", validCodexMarkdown("same", "L", "l"))
	if _, err = resolveProfiles(cfg, filepath.Join(d, "root")); err == nil {
		t.Fatal("global collision accepted")
	}
}

func TestManagerListPropagatesProfileResolutionError(t *testing.T) {
	d := t.TempDir()
	local := filepath.Join(d, ".spacedock", "agents")
	if err := os.MkdirAll(local, 0700); err != nil {
		t.Fatal(err)
	}
	writeProfile(t, local, "x.md", validCodexMarkdown("legacy", "L", "l"))
	am := NewManager(config.Config{StateDir: filepath.Join(d, "state"), Agents: config.AgentsConfig{MaxConcurrent: 1, Profiles: []config.AgentProfileConfig{{ID: "legacy", Provider: "codex"}}}}, nil)
	_, _, err := am.List(&workspace.Workspace{ID: "w", Root: d})
	if err == nil {
		t.Fatal("expected resolution error")
	}
}
