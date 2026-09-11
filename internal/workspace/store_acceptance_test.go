package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceStoreRejectsCorruptDuplicatesAndWritesDeterministically(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "workspaces.json")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	if err := s.Put(StoredWorkspace{ID: "ws-b", RootID: "root", ProjectPath: "b", Mode: "checkout", Root: "/tmp/b", OpenedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(StoredWorkspace{ID: "ws-a", RootID: "root", ProjectPath: "a", Mode: "checkout", Root: "/tmp/a", OpenedAt: at}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema_version":1,"workspaces":[{"schema_version":1,"id":"ws-a","root_id":"root","project_path":"a","mode":"checkout","root":"/tmp/a","managed":false,"opened_at":"2026-09-10T12:00:00Z"},{"schema_version":1,"id":"ws-b","root_id":"root","project_path":"b","mode":"checkout","root":"/tmp/b","managed":false,"opened_at":"2026-09-10T12:00:00Z"}]}`
	if string(b) != want {
		t.Fatalf("non-deterministic file=%s want=%s", b, want)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("state mode/stat=%v/%v", st, err)
	}

	corruptCases := map[string]string{
		"bad schema":   `{"schema_version":2,"workspaces":[]}`,
		"duplicate id": `{"schema_version":1,"workspaces":[{"schema_version":1,"id":"same"},{"schema_version":1,"id":"same"}]}`,
		"invalid json": "not-json",
	}
	for name, content := range corruptCases {
		p := filepath.Join(d, name+".json")
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(p); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestWorkspaceStorePutDeleteRollbackOnWriteFailure(t *testing.T) {
	d := t.TempDir()
	s, err := NewStore(filepath.Join(d, "workspaces.json"))
	if err != nil {
		t.Fatal(err)
	}
	v := StoredWorkspace{ID: "ws-a", RootID: "root", ProjectPath: ".", Mode: "checkout", Root: "/tmp/root"}
	if err := s.Put(v); err != nil {
		t.Fatal(err)
	}
	s.path = filepath.Join(d, "missing", "workspaces.json")
	if err := s.Put(StoredWorkspace{ID: "ws-b"}); err == nil {
		t.Fatal("Put write failure was not reported")
	}
	if got := s.List(); len(got) != 1 || got[0].ID != "ws-a" {
		t.Fatalf("Put rollback failed: %+v", got)
	}
	if err := s.Delete("ws-a"); err == nil {
		t.Fatal("Delete write failure was not reported")
	}
	if got := s.List(); len(got) != 1 || got[0].ID != "ws-a" {
		t.Fatalf("Delete rollback failed: %+v", got)
	}

	// Ensure the failure test did not leave a partially written state file in the bad directory.
	if _, err := os.Stat(filepath.Dir(s.path)); !os.IsNotExist(err) && !strings.Contains(err.Error(), "permission") {
		t.Fatalf("unexpected failure fixture state: %v", err)
	}
}
