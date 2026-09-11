package git

import (
	"context"
	"errors"
	"fmt"
	"github.com/starlove7/spacedock/internal/workspace"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var ErrNotRepository = errors.New("not a git repository")

type CommandError struct {
	Err    error
	Output string
}

func (e *CommandError) Error() string { return e.Err.Error() }
func (e *CommandError) Unwrap() error { return e.Err }

type Service struct{}

func NewService() *Service { return &Service{} }

type StatusEntry struct {
	XY   string `json:"xy"`
	Path string `json:"path"`
}
type StatusResult struct {
	Branch  string        `json:"branch"`
	Entries []StatusEntry `json:"entries"`
}
type DiffResult struct {
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}
type LogEntry struct {
	Hash        string `json:"hash"`
	AuthorName  string `json:"author_name"`
	AuthorEmail string `json:"author_email"`
	AuthoredAt  string `json:"authored_at"`
	Subject     string `json:"subject"`
}
type LogResult struct {
	Entries []LogEntry `json:"entries"`
}
type bounded struct {
	mu  sync.Mutex
	b   []byte
	max int
	tr  bool
}

func (w *bounded) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.b)+len(p) > w.max {
		n := w.max - len(w.b)
		if n > 0 {
			w.b = append(w.b, p[:n]...)
		}
		w.tr = true
		return len(p), nil
	}
	w.b = append(w.b, p...)
	return len(p), nil
}
func (w *bounded) bytes() ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.b...), w.tr
}
func run(ctx context.Context, w *workspace.Workspace, args ...string) ([]byte, bool, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	check := exec.CommandContext(cctx, "git", "-C", w.Root, "rev-parse", "--is-inside-work-tree")
	b, e := check.Output()
	if e != nil || strings.TrimSpace(string(b)) != "true" {
		return nil, false, ErrNotRepository
	}
	out := &bounded{max: (4 << 20) + 1}
	a := append([]string{"-C", w.Root}, args...)
	cmd := exec.CommandContext(cctx, "git", a...)
	cmd.Stdout = out
	cmd.Stderr = out
	e = cmd.Run()
	b, tr := out.bytes()
	if e != nil {
		return b, tr, &CommandError{Err: e, Output: strings.TrimSpace(string(b))}
	}
	return b, tr, nil
}
func (s *Service) Status(ctx context.Context, w *workspace.Workspace) (StatusResult, error) {
	b, _, e := run(ctx, w, "status", "--porcelain=v1", "-b")
	if e != nil {
		return StatusResult{}, e
	}
	r := StatusResult{}
	for _, l := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if strings.HasPrefix(l, "## ") {
			r.Branch = strings.TrimPrefix(l, "## ")
		} else if len(l) >= 3 {
			r.Entries = append(r.Entries, StatusEntry{l[:2], l[3:]})
		}
	}
	return r, nil
}
func (s *Service) Diff(ctx context.Context, w *workspace.Workspace, p string, staged bool, max int) (DiffResult, error) {
	a := []string{"diff", "--no-ext-diff", "--unified=3"}
	if staged {
		a = append(a, "--cached")
	}
	if p != "" {
		v, e := w.Resolver.ValidateRelative(p)
		if e != nil {
			return DiffResult{}, e
		}
		a = append(a, "--", v)
	}
	b, tr, e := run(ctx, w, a...)
	if e != nil {
		return DiffResult{}, e
	}
	if max <= 0 {
		max = 262144
	}
	if max > 4<<20 {
		max = 4 << 20
	}
	if len(b) > max {
		b = b[:max]
		tr = true
	}
	return DiffResult{string(b), tr}, nil
}
func (s *Service) Log(ctx context.Context, w *workspace.Workspace, limit int) (LogResult, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	b, _, e := run(ctx, w, "log", fmt.Sprintf("-%d", limit), "--pretty=format:%H%x1f%an%x1f%ae%x1f%aI%x1f%s%x1e")
	if e != nil {
		return LogResult{}, e
	}
	r := LogResult{}
	for _, x := range strings.Split(string(b), "\x1e") {
		x = strings.TrimSuffix(x, "\n")
		if x == "" {
			continue
		}
		f := strings.SplitN(x, "\x1f", 5)
		if len(f) == 5 {
			r.Entries = append(r.Entries, LogEntry{f[0], f[1], f[2], f[3], f[4]})
		}
	}
	return r, nil
}
