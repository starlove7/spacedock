package recall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecallIsolationWriteModesAndValidation(t *testing.T) {
	m, e := NewManager(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a, e := m.Write("root-a", "notes.md", "create", "first")
	if e != nil || a.Content != "first" {
		t.Fatalf("create: %#v %v", a, e)
	}
	if _, e = m.Write("root-a", "notes.md", "create", "again"); e == nil {
		t.Error("duplicate create accepted")
	}
	x, e := m.Write("root-a", "notes.md", "append", "second")
	if e != nil || x.Content != "first\nsecond" {
		t.Fatalf("append: %#v %v", x, e)
	}
	if _, e = m.Write("root-a", "notes.md", "replace", "third"); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Read("root-b", "notes.md"); e == nil {
		t.Error("root isolation broken")
	}
	for _, c := range []string{"password: actual-secret", "ghp_12345678abcdefgh", string([]byte{0xff})} {
		if _, e := m.Write("root-a", "bad.md", "create", c); e == nil {
			t.Errorf("invalid content accepted: %q", c)
		}
	}
	if _, e := m.Write("root-a", "x.txt", "create", "x"); e == nil {
		t.Error("non-markdown accepted")
	}
}

func TestRecallSymlinkContextSearchAndPermissions(t *testing.T) {
	state := t.TempDir()
	m, _ := NewManager(state)
	if _, e := m.Write("r", "a.md", "create", "needle a"); e != nil {
		t.Fatalf("setup create a: %v", e)
	}
	if _, e := m.Write("r", "nested/b.md", "create", "needle b"); e != nil {
		t.Fatalf("setup create nested: %v", e)
	}
	os.WriteFile(filepath.Join(state, "outside.md"), []byte("needle outside"), 0600)
	os.Symlink(filepath.Join(state, "outside.md"), filepath.Join(state, "recall", "r", "external.md"))
	if _, e := m.Read("r", "external.md"); e == nil {
		t.Error("external symlink read accepted")
	}
	if _, e := m.Search("r", "needle", 100); e != nil {
		t.Fatal(e)
	} else {
		hits, _ := m.Search("r", "needle", 100)
		for _, h := range hits {
			if h.Path == "external.md" {
				t.Error("external symlink searched")
			}
		}
	}
	ctx, e := m.Context("r", 100)
	if e != nil {
		t.Fatal(e)
	}
	if len(ctx) > 100 || !strings.Contains(ctx, "Recall:") {
		t.Errorf("context bounds/format: %q", ctx)
	}
	st, _ := os.Stat(filepath.Join(state, "recall", "r"))
	if st.Mode().Perm() != 0700 {
		t.Errorf("root dir mode=%o", st.Mode().Perm())
	}
	fs, _ := os.Stat(filepath.Join(state, "recall", "r", "a.md"))
	if fs.Mode().Perm() != 0600 {
		t.Errorf("file mode=%o", fs.Mode().Perm())
	}
}
