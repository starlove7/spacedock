package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitTestRepo(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	gitRun := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	gitRun("init", "-q")
	gitRun("config", "user.email", "test@example.invalid")
	gitRun("config", "user.name", "SpaceDock Test")
	if err := os.WriteFile(filepath.Join(d, "README"), []byte("initial\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "README")
	gitRun("commit", "-qm", "initial")
	return d
}

func TestWorktreeDetachedCreateBaseDirtyAndForceRemoval(t *testing.T) {
	source := gitTestRepo(t)
	worktreeRoot := filepath.Join(t.TempDir(), "managed worktrees")
	result, err := CreateWorktree(source, "HEAD", worktreeRoot, "ws_test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != filepath.Join(worktreeRoot, "ws_test") || result.SourceRoot != source || result.BaseRef != "HEAD" || result.BaseSHA == "" || result.SourceDirty {
		t.Fatalf("unexpected worktree result: %+v", result)
	}
	cmd := exec.Command("git", "symbolic-ref", "--short", "-q", "HEAD")
	cmd.Dir = result.Path
	branchBytes, branchErr := cmd.Output()
	if branchErr == nil {
		if branch := strings.TrimSpace(string(branchBytes)); branch != "" {
			t.Fatalf("worktree is not detached, branch=%q", branch)
		}
	} else if exitErr, ok := branchErr.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("git symbolic-ref failed unexpectedly: %v", branchErr)
	}
	if dirty, err := WorktreeDirty(result.Path); err != nil || dirty {
		t.Fatalf("new worktree dirty=%v err=%v", dirty, err)
	}
	if err := os.WriteFile(filepath.Join(result.Path, "untracked.txt"), []byte("dirty\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if dirty, err := WorktreeDirty(result.Path); err != nil || !dirty {
		t.Fatalf("dirty worktree=%v err=%v", dirty, err)
	}
	if err := RemoveWorktree(source, result.Path, false); err == nil {
		t.Fatal("dirty worktree removed without force")
	}
	if err := RemoveWorktree(source, result.Path, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after force removal: %v", err)
	}
}

func TestWorktreeRejectsParentRepositoryAndUnsafePersistedContainment(t *testing.T) {
	source := gitTestRepo(t)
	selected := filepath.Join(source, "subproject")
	if err := os.Mkdir(selected, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateWorktree(selected, "HEAD", filepath.Join(t.TempDir(), "worktrees"), "ws_parent"); err == nil || !strings.Contains(err.Error(), "project boundary") {
		t.Fatalf("parent repository was accepted: %v", err)
	}

	valid := filepath.Join(t.TempDir(), "valid")
	if _, err := CreateWorktree(source, "HEAD", valid, "ws_valid"); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePersistedWorktree(source, filepath.Join(valid, "ws_valid"), valid); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePersistedWorktree(source, filepath.Join(t.TempDir(), "outside"), valid); err == nil {
		t.Fatal("persisted worktree outside root accepted")
	}
	_ = RemoveWorktree(source, filepath.Join(valid, "ws_valid"), true)
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("git output %v: %v", args, err)
	}
	return strings.TrimSpace(string(b))
}
