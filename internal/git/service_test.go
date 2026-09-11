package git

import (
	"context"
	"errors"
	"github.com/starlove7/spacedock/internal/policy"
	"github.com/starlove7/spacedock/internal/workspace"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitWS(t *testing.T) *workspace.Workspace {
	d := t.TempDir()
	r, _ := policy.NewResolver(d)
	return &workspace.Workspace{ID: "w", Root: d, Resolver: r}
}
func gitRun(t *testing.T, d string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", d}, args...)...)
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("git: %v %s", e, b)
	}
}
func TestServiceNonRepoStatusAndDirectInvocation(t *testing.T) {
	w := gitWS(t)
	_, e := NewService().Status(context.Background(), w)
	if !errors.Is(e, ErrNotRepository) {
		t.Fatalf("err=%v", e)
	}
	gitRun(t, w.Root, "init", "-q")
	gitRun(t, w.Root, "config", "user.email", "t@example.com")
	gitRun(t, w.Root, "config", "user.name", "Tester")
	if e = os.WriteFile(filepath.Join(w.Root, "a.txt"), []byte("one\n"), 0644); e != nil {
		t.Fatal(e)
	}
	r, e := NewService().Status(context.Background(), w)
	if e != nil || r.Branch == "" || len(r.Entries) != 1 || r.Entries[0].Path != "a.txt" {
		t.Fatalf("status=%+v err=%v", r, e)
	}
}
func TestServiceDiffValidationTruncationAndLogFields(t *testing.T) {
	w := gitWS(t)
	gitRun(t, w.Root, "init", "-q")
	gitRun(t, w.Root, "config", "user.email", "t@example.com")
	gitRun(t, w.Root, "config", "user.name", "Tester")
	os.WriteFile(filepath.Join(w.Root, "a"), []byte("one\n"), 0644)
	gitRun(t, w.Root, "add", "a")
	gitRun(t, w.Root, "commit", "-qm", "first subject")
	os.WriteFile(filepath.Join(w.Root, "a"), []byte("one\ntwo\n"), 0644)
	if _, e := NewService().Diff(context.Background(), w, "../bad", false, 10); e == nil {
		t.Fatal("path escape accepted")
	}
	d, e := NewService().Diff(context.Background(), w, "a", false, 5)
	if e != nil || len(d.Content) != 5 || !d.Truncated {
		t.Fatalf("diff=%+v err=%v", d, e)
	}
	l, e := NewService().Log(context.Background(), w, 1)
	if e != nil || len(l.Entries) != 1 || l.Entries[0].Subject != "first subject" {
		t.Fatalf("log=%+v err=%v", l, e)
	}
}
