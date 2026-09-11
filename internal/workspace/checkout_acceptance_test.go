package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/starlove7/spacedock/internal/config"
)

func TestCheckoutContainmentPersistenceRestoreAndWorktreeLifecycle(t *testing.T) {
	root := gitTestRepo(t)
	state := t.TempDir()
	worktreeRoot := filepath.Join(t.TempDir(), "configured worktrees")
	c := config.Config{StateDir: state, Worktree: config.WorktreeConfig{Root: worktreeRoot}, AllowedRoots: []config.RootConfig{{ID: "root", Name: "Root", Path: root, Permissions: []string{"workspace.manage"}}}}
	m, err := NewManager(c, recallStub{ctx: "restored-context"})
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := m.Open(OpenOptions{RootID: "root", Path: "."})
	if err != nil || checkout.ProjectPath != "." || checkout.Mode != "checkout" || checkout.Managed {
		t.Fatalf("checkout=%+v err=%v", checkout, err)
	}
	for _, path := range []string{"../", "/tmp", "file:outside"} {
		if _, err := m.Open(OpenOptions{RootID: "root", Path: path}); err == nil {
			t.Fatalf("checkout unsafe path accepted: %q", path)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open(OpenOptions{RootID: "root", Path: "escape"}); err == nil {
		t.Fatal("checkout symlink escape accepted")
	}
	if err := m.Close(checkout.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	checkout, err = m.Open(OpenOptions{RootID: "root"})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := NewManager(c, recallStub{ctx: "restored-context"})
	if err != nil {
		t.Fatal(err)
	}
	list := m2.List()
	if len(list) != 1 || list[0].ID != checkout.ID || list[0].RecallContext != "restored-context" {
		t.Fatalf("checkout persistence list=%+v", list)
	}
	if err := m2.Close(checkout.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(checkout.ID); err != nil {
		t.Fatal(err)
	}

	worktree, err := m.Open(OpenOptions{RootID: "root", Mode: "worktree", BaseRef: "HEAD"})
	if err != nil || !worktree.Managed || worktree.BaseRef != "HEAD" || worktree.BaseSHA == "" || worktree.SourceRoot != root {
		t.Fatalf("worktree=%+v err=%v", worktree, err)
	}
	if err := os.WriteFile(filepath.Join(worktree.Root, "dirty.txt"), []byte("dirty"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(worktree.ID); err == nil || !strings.Contains(err.Error(), "WORKTREE_DIRTY") {
		t.Fatalf("dirty close err=%v", err)
	}
	if err := m.Discard(worktree.ID, false); err == nil || !strings.Contains(err.Error(), "WORKTREE_DIRTY") {
		t.Fatalf("dirty discard err=%v", err)
	}
	if _, err := m.Get(worktree.ID); err != nil {
		t.Fatalf("dirty lifecycle removed workspace: %v", err)
	}
	if err := m.Discard(worktree.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(worktree.Root); !os.IsNotExist(err) {
		t.Fatalf("forced discard left worktree: %v", err)
	}

	clean, err := m.Open(OpenOptions{RootID: "root", Mode: "worktree"})
	if err != nil {
		t.Fatal(err)
	}
	m3, err := NewManager(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if restored := m3.List(); len(restored) != 1 || restored[0].ID != clean.ID {
		t.Fatalf("worktree restart restore=%+v", restored)
	}
	if err := m3.Close(clean.ID); err != nil {
		t.Fatal(err)
	}
	m4, err := NewManager(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	m4.store.path = filepath.Join(state, "missing", "workspaces.json")
	if _, err := m4.Open(OpenOptions{RootID: "root", Mode: "worktree"}); err == nil {
		t.Fatal("store failure was not reported")
	}
	entries, err := os.ReadDir(worktreeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("orphan worktree remains after store failure: %v", entries)
	}
}
