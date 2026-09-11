package workspace

import (
	"fmt"
	"github.com/starlove7/spacedock/internal/policy"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type WorktreeResult struct {
	Path, SourceRoot, BaseRef, BaseSHA string
	SourceDirty                        bool
}

func gitOut(dir string, args ...string) (string, error) {
	a := append([]string{"-C", dir}, args...)
	b, e := exec.Command("git", a...).Output()
	return strings.TrimSpace(string(b)), e
}
func CreateWorktree(source, base, root, id string) (WorktreeResult, error) {
	sr, e := gitOut(source, "rev-parse", "--show-toplevel")
	if e != nil {
		return WorktreeResult{}, e
	}
	source, e = filepath.EvalSymlinks(source)
	if e != nil {
		return WorktreeResult{}, e
	}
	sr, e = filepath.EvalSymlinks(sr)
	if e != nil || !policy.IsWithin(source, sr) {
		return WorktreeResult{}, fmt.Errorf("worktree source outside selected project boundary")
	}
	if base == "" {
		base = "HEAD"
	}
	sha, e := gitOut(sr, "rev-parse", "--verify", base+"^{commit}")
	if e != nil {
		return WorktreeResult{}, e
	}
	dirty, e := gitOut(sr, "status", "--porcelain=v1")
	if e != nil {
		return WorktreeResult{}, e
	}
	target := filepath.Join(root, id)
	if _, e = os.Stat(target); e == nil {
		return WorktreeResult{}, fmt.Errorf("worktree target exists")
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		return WorktreeResult{}, e
	}
	if e = exec.Command("git", "-C", sr, "worktree", "add", "--detach", target, sha).Run(); e != nil {
		_ = os.RemoveAll(target)
		return WorktreeResult{}, e
	}
	return WorktreeResult{target, sr, base, sha, dirty != ""}, nil
}
func WorktreeDirty(path string) (bool, error) {
	x, e := gitOut(path, "status", "--porcelain=v1", "--untracked-files=normal")
	return x != "", e
}
func RemoveWorktree(source, path string, force bool) error {
	root := filepath.Dir(path)
	if realRoot, e := filepath.EvalSymlinks(root); e == nil && !policy.IsWithin(realRoot, path) {
		return fmt.Errorf("worktree path outside configured root")
	}
	args := []string{"-C", source, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	return exec.Command("git", append(args, path)...).Run()
}
func ValidatePersistedWorktree(source, path, root string) error {
	sr, e := filepath.EvalSymlinks(source)
	if e != nil {
		return e
	}
	p, e := filepath.EvalSymlinks(path)
	if e != nil {
		return e
	}
	r, e := filepath.EvalSymlinks(root)
	if e != nil {
		return e
	}
	if !policy.IsWithin(r, p) || sr == "" {
		return fmt.Errorf("unsafe persisted worktree")
	}
	top, e := gitOut(p, "rev-parse", "--show-toplevel")
	if e != nil {
		return e
	}
	top, _ = filepath.EvalSymlinks(top)
	if top != p {
		return fmt.Errorf("worktree identity mismatch")
	}
	return nil
}
