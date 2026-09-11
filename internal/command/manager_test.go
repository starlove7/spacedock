package command

import (
	"context"
	"github.com/starlove7/spacedock/internal/policy"
	"github.com/starlove7/spacedock/internal/workspace"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testWorkspace(t *testing.T, id string) *workspace.Workspace {
	t.Helper()
	d := t.TempDir()
	r, e := policy.NewResolver(d)
	if e != nil {
		t.Fatal(e)
	}
	return &workspace.Workspace{ID: id, Root: d, Resolver: r, Permissions: []policy.Permission{policy.PermissionCommandExecute}}
}

func TestManagerWorkspaceRelativeCWDAndSeparatedOutput(t *testing.T) {
	w := testWorkspace(t, "w1")
	if e := os.Mkdir(filepath.Join(w.Root, "sub"), 0755); e != nil {
		t.Fatal(e)
	}
	s, e := NewManager().Exec(context.Background(), w, ExecRequest{Command: "pwd; printf out; printf err >&2", WorkingDirectory: "sub", YieldMS: 1000})
	if e != nil {
		t.Fatal(e)
	}
	if s.Status != "exited" || !strings.Contains(s.Stdout, filepath.Join(w.Root, "sub")) || !strings.Contains(s.Stdout, "out") || s.Stderr != "err" {
		t.Fatalf("snapshot=%+v", s)
	}
}
func TestManagerOwnershipAndLimits(t *testing.T) {
	w := testWorkspace(t, "w1")
	m := NewManager()
	s, e := m.Exec(context.Background(), w, ExecRequest{Command: "printf 1234567890", YieldMS: 1000})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Observe("other", s.SessionID, 3); e == nil {
		t.Fatal("cross-workspace observe accepted")
	}
	x, e := m.Observe(w.ID, s.SessionID, 3)
	if e != nil || x.Stdout != "123" || !x.StdoutTruncated {
		t.Fatalf("observe=%+v err=%v", x, e)
	}
}

func TestBoundedRetainsHeadAndTailWhenMaximumIsExceeded(t *testing.T) {
	b := &bounded{max: 10}
	if n, err := b.Write([]byte("0123456789abcdefghij")); err != nil || n != 20 {
		t.Fatalf("write n=%d err=%v", n, err)
	}

	got, truncated := b.snapshot()
	if len(got) > b.max {
		t.Fatalf("retained length=%d, max=%d", len(got), b.max)
	}
	if !truncated {
		t.Fatal("expected truncated=true")
	}
	if got != "01234fghij" {
		t.Fatalf("retained=%q", got)
	}
}

func TestManagerStdinTerminateTimeoutAndCallerCancel(t *testing.T) {
	w := testWorkspace(t, "w1")
	m := NewManager()
	s, e := m.Exec(context.Background(), w, ExecRequest{Command: "read x; echo $x", Stdin: "ok\n", YieldMS: 1000})
	if e != nil || s.Status != "exited" || !strings.Contains(s.Stdout, "ok") {
		t.Fatalf("stdin=%+v %v", s, e)
	}
	s, e = m.Exec(context.Background(), w, ExecRequest{Command: "sleep 10", KeepStdin: true, YieldMS: 0})
	if e != nil {
		t.Fatal(e)
	}
	_, e = m.Act(w.ID, s.SessionID, "terminate", "")
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	var x Snapshot
	for time.Now().Before(deadline) {
		x, _ = m.Observe(w.ID, s.SessionID, 0)
		if x.Status == "killed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if x.Status != "killed" {
		t.Fatalf("terminate status=%s", x.Status)
	}
	s, e = m.Exec(context.Background(), w, ExecRequest{Command: "sleep 10", TimeoutMS: 100, YieldMS: 200})
	if e != nil {
		t.Fatal(e)
	}
	if s.Status != "timed_out" {
		t.Fatalf("timeout status=%s", s.Status)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s, e = m.Exec(ctx, w, ExecRequest{Command: "sleep 1", YieldMS: 0})
	cancel()
	if e != nil {
		t.Fatal(e)
	}
	x, e = m.Observe(w.ID, s.SessionID, 0)
	if e != nil || x.Status == "killed" {
		t.Fatalf("caller cancellation killed background: %+v %v", x, e)
	}
}
func TestManagerCloseWorkspaceOnlyOwnsSessions(t *testing.T) {
	m := NewManager()
	a, b := testWorkspace(t, "a"), testWorkspace(t, "b")
	sa, _ := m.Exec(context.Background(), a, ExecRequest{Command: "sleep 10", YieldMS: 0})
	sb, _ := m.Exec(context.Background(), b, ExecRequest{Command: "sleep 10", YieldMS: 0})
	m.CloseWorkspace(a.ID)
	if _, e := m.Observe(a.ID, sa.SessionID, 0); e == nil {
		t.Fatal("closed session retained")
	}
	if _, e := m.Observe(b.ID, sb.SessionID, 0); e != nil {
		t.Fatalf("other workspace removed: %v", e)
	}
	m.CloseWorkspace(b.ID)
}
