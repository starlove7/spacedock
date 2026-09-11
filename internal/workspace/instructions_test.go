package workspace

import (
	"github.com/starlove7/spacedock/internal/policy"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadInstructionsOrderPriorityAndEscape(t *testing.T) {
	d := t.TempDir()
	for _, n := range []string{"README.md", "CLAUDE.md", "AGENTS.md"} {
		os.WriteFile(filepath.Join(d, n), []byte(n), 0600)
	}
	r, _ := policy.NewResolver(d)
	got, err := LoadInstructions(r)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"AGENTS.md", "CLAUDE.md", "README.md"}
	for i, w := range want {
		if got[i].Name != w || got[i].Priority != 300-i*100 {
			t.Errorf("%d got %#v", i, got[i])
		}
	}
	out := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(out, []byte("x"), 0600)
	os.Remove(filepath.Join(d, "README.md"))
	os.Symlink(out, filepath.Join(d, "README.md"))
	if _, err := LoadInstructions(r); err == nil {
		t.Error("instruction escape accepted")
	}
}
