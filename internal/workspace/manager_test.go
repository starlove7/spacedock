package workspace

import (
	"errors"
	"github.com/starlove7/spacedock/internal/config"
	"os"
	"path/filepath"
	"testing"
)

type recallStub struct {
	ctx string
	err error
}

func (r recallStub) Context(string, int) (string, error) { return r.ctx, r.err }

type hookStub struct {
	n  int
	id string
}

func (h *hookStub) CloseWorkspace(id string) { h.n++; h.id = id }
func wsConfig(root string, perms []string) config.Config {
	return config.Config{StateDir: filepath.Join(root, ".state"), AllowedRoots: []config.RootConfig{{ID: "root", Name: "Root", Path: root, Permissions: perms}}}
}
func TestManagerOpenCloseContracts(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "AGENTS.md"), []byte("rules"), 0600)
	m, err := NewManager(wsConfig(d, []string{"fs.read"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open(OpenOptions{RootID: "root"}); err == nil {
		t.Error("open without workspace.manage accepted")
	}
	m, err = NewManager(wsConfig(d, []string{"workspace.manage"}), recallStub{ctx: "ctx"})
	if err != nil {
		t.Fatal(err)
	}
	a, e := m.Open(OpenOptions{RootID: "root"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := m.Open(OpenOptions{RootID: "root"})
	if e != nil || a.ID == b.ID {
		t.Errorf("fresh IDs: %q %q %v", a.ID, b.ID, e)
	}
	if a.RecallContext != "ctx" {
		t.Error("recall context missing")
	}
	h := &hookStub{}
	m.AddCloseHook(h)
	if err := m.Close(a.ID); err != nil {
		t.Fatal(err)
	}
	if h.n != 1 || h.id != a.ID {
		t.Errorf("hook=%+v", h)
	}
	if err := m.Close(b.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(a.ID); err == nil {
		t.Error("unknown close accepted")
	}
	failRoot := t.TempDir()
	m, err = NewManager(wsConfig(failRoot, []string{"workspace.manage"}), recallStub{err: errors.New("recall down")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open(OpenOptions{RootID: "root"}); err == nil {
		t.Error("recall load failure ignored")
	}
}
