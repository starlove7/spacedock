package filesystem

import (
	"github.com/starlove7/spacedock/internal/policy"
	"github.com/starlove7/spacedock/internal/workspace"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testWS(t *testing.T) *workspace.Workspace {
	d := t.TempDir()
	r, e := policy.NewResolver(d)
	if e != nil {
		t.Fatal(e)
	}
	return &workspace.Workspace{Root: d, Resolver: r}
}
func TestFilesystemReadListSearchContracts(t *testing.T) {
	w := testWS(t)
	s := NewService()
	os.Mkdir(filepath.Join(w.Root, "zdir"), 0755)
	os.WriteFile(filepath.Join(w.Root, "b.txt"), []byte("b\n"), 0644)
	os.WriteFile(filepath.Join(w.Root, "a.txt"), []byte("a\n"), 0644)
	got, e := s.ListDir(w, ".", 0)
	if e != nil || len(got.Entries) != 3 {
		t.Fatalf("list: %#v %v", got, e)
	}
	if got.Entries[0].Name != "a.txt" {
		t.Errorf("not deterministic sorted: %#v", got.Entries)
	}
	os.WriteFile(filepath.Join(w.Root, "nul"), []byte{'a', 0, 'b'}, 0600)
	if _, e := s.ReadFile(w, "nul", 1, 400, 262144); e == nil {
		t.Error("NUL file accepted")
	}
	os.WriteFile(filepath.Join(w.Root, "bad"), []byte{0xff}, 0600)
	if _, e := s.ReadFile(w, "bad", 1, 400, 262144); e == nil {
		t.Error("invalid UTF-8 accepted")
	}
	content := strings.Repeat("x", 262150) + "\nNEXT\n"
	os.WriteFile(filepath.Join(w.Root, "long.txt"), []byte(content), 0600)
	r, e := s.ReadFile(w, "long.txt", 1, 5000, 262144)
	if e != nil {
		t.Fatal(e)
	}
	if !r.Truncated || r.NextStartLine < 2 {
		t.Errorf("trunc metadata: %+v", r)
	}
	os.Mkdir(filepath.Join(w.Root, "vendor"), 0755)
	os.WriteFile(filepath.Join(w.Root, "vendor", "skip.go"), []byte("needle"), 0600)
	os.Mkdir(filepath.Join(w.Root, "deep"), 0755)
	os.WriteFile(filepath.Join(w.Root, "deep", "hit.go"), []byte("needle"), 0600)
	lf, e := s.ListFiles(w, ".", "*.go", 1, 500)
	if e != nil {
		t.Fatal(e)
	}
	for _, x := range lf.Entries {
		if strings.Contains(x.Path, "vendor") || strings.Contains(x.Path, "deep") {
			t.Errorf("depth/excluded pruning failed: %#v", lf.Entries)
		}
	}
	sr, e := s.SearchText(w, ".", "needle", false, false, 100)
	if e != nil {
		t.Fatal(e)
	}
	for _, x := range sr.Matches {
		if strings.Contains(x.Path, "vendor") {
			t.Error("excluded dir searched")
		}
	}
	if _, e := s.SearchText(w, ".", "[", true, false, 100); e == nil {
		t.Error("invalid regex accepted")
	}
}

func TestFilesystemEditContracts(t *testing.T) {
	w := testWS(t)
	s := NewService()
	p := filepath.Join(w.Root, "f.txt")
	os.WriteFile(p, []byte("one one"), 0600)
	zero, e := s.Edit(w, EditRequest{Action: "replace", Path: "f.txt", OldText: "missing", NewText: "x", ExpectedMatches: 0})
	if e != nil || zero.Matches != 0 || zero.Changed {
		t.Errorf("zero-match replace assertion: result=%+v err=%v", zero, e)
	}
	if _, e := s.Edit(w, EditRequest{Action: "replace", Path: "f.txt", OldText: "one", NewText: "two", ExpectedMatches: 2, ReplaceAll: true}); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "two two" {
		t.Errorf("replace=%q", b)
	}
	if _, e := s.Edit(w, EditRequest{Action: "write", Path: "f.txt", Content: "new"}); e == nil {
		t.Error("overwrite without flag accepted")
	}
	if _, e := s.Edit(w, EditRequest{Action: "write", Path: "f.txt", Content: "new", Overwrite: true}); e != nil {
		t.Fatal(e)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0600 {
		t.Errorf("overwrite mode=%o", st.Mode().Perm())
	}
	outside := filepath.Join(t.TempDir(), "out")
	os.WriteFile(outside, []byte("x"), 0600)
	os.Symlink(outside, filepath.Join(w.Root, "link"))
	for _, a := range []string{"write", "replace", "delete"} {
		if _, e := s.Edit(w, EditRequest{Action: a, Path: "link", Content: "x", OldText: "x", NewText: "y", ExpectedMatches: 1}); e == nil {
			t.Errorf("%s final symlink accepted", a)
		}
	}
	if _, e := s.Edit(w, EditRequest{Action: "move", Path: "f.txt", NewPath: "link", Overwrite: true}); e == nil {
		t.Error("move to final symlink accepted")
	}
}
