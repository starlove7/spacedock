package command

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/starlove7/spacedock/internal/workspace"
	"io"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ExecRequest struct {
	Command, WorkingDirectory string
	TimeoutMS, YieldMS        int
	Stdin                     string
	KeepStdin                 bool
}
type Snapshot struct {
	SessionID       string    `json:"session_id"`
	Status          string    `json:"status"`
	Stdout          string    `json:"stdout"`
	Stderr          string    `json:"stderr"`
	StdoutTruncated bool      `json:"stdout_truncated"`
	StderrTruncated bool      `json:"stderr_truncated"`
	ExitCode        *int      `json:"exit_code,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	ElapsedMS       int64     `json:"elapsed_ms"`
}

type bounded struct {
	mu        sync.Mutex
	data      []byte
	max       int
	truncated bool
}

func (b *bounded) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.data)+len(p) <= b.max && !b.truncated {
		b.data = append(b.data, p...)
		return len(p), nil
	}
	head := b.max / 2
	all := append(append([]byte(nil), b.data...), p...)
	if len(all) <= b.max {
		b.data = all
		return len(p), nil
	}
	tail := b.max - head
	if len(all) < head+tail {
		head = len(all) - tail
	}
	b.data = append(append([]byte(nil), all[:head]...), all[len(all)-tail:]...)
	b.truncated = true
	return len(p), nil
}
func (b *bounded) snapshot() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.data...)), b.truncated
}

type session struct {
	id, ws                      string
	cmd                         *exec.Cmd
	stdin                       io.WriteCloser
	out, err                    *bounded
	started                     time.Time
	mu                          sync.Mutex
	snap                        Snapshot
	done                        chan struct{}
	killed, timedOut, stdinOpen bool
	expires                     time.Time
}
type Manager struct {
	mu sync.Mutex
	s  map[string]*session
}

func NewManager() *Manager { return &Manager{s: map[string]*session{}} }
func newID() (string, error) {
	b := make([]byte, 10)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return "cmd_" + hex.EncodeToString(b), nil
}

func setProcessGroup(cmd *exec.Cmd) {
	if runtime.GOOS == "windows" {
		return
	}
	attr := &syscall.SysProcAttr{}
	field := reflect.ValueOf(attr).Elem().FieldByName("Setpgid")
	if field.IsValid() && field.Kind() == reflect.Bool && field.CanSet() {
		field.SetBool(true)
	}
	cmd.SysProcAttr = attr
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil {
		return fmt.Errorf("command is nil")
	}
	if cmd.Process == nil {
		return fmt.Errorf("command process is nil")
	}

	if runtime.GOOS == "windows" {
		if err := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", cmd.Process.Pid), "/T", "/F").Run(); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}

	if process, err := os.FindProcess(-cmd.Process.Pid); err == nil {
		if err := process.Kill(); err == nil {
			return nil
		}
	}
	return cmd.Process.Kill()
}

func (m *Manager) pruneLocked() {
	now := time.Now()
	for id, s := range m.s {
		s.mu.Lock()
		expired := s.expires.IsZero() == false && now.After(s.expires)
		done := s.snap.Status != "running"
		s.mu.Unlock()
		if done && expired {
			delete(m.s, id)
		}
	}
}
func (m *Manager) Exec(ctx context.Context, w *workspace.Workspace, r ExecRequest) (Snapshot, error) {
	if strings.TrimSpace(r.Command) == "" {
		return Snapshot{}, fmt.Errorf("command required")
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	wd := w.Root
	if r.WorkingDirectory != "" {
		var e error
		wd, e = w.Resolver.ResolveExisting(r.WorkingDirectory)
		if e != nil {
			return Snapshot{}, e
		}
		st, e := os.Stat(wd)
		if e != nil || !st.IsDir() {
			return Snapshot{}, fmt.Errorf("working directory is not a directory")
		}
	}
	tm := r.TimeoutMS
	if tm == 0 {
		tm = 600000
	}
	if tm < 100 {
		tm = 100
	}
	if tm > 86400000 {
		tm = 86400000
	}
	ym := r.YieldMS
	if ym == 0 {
		ym = 5000
	}
	if ym < 0 {
		ym = 0
	}
	if ym > 30000 {
		ym = 30000
	}
	id, e := newID()
	if e != nil {
		return Snapshot{}, e
	}
	cctx, cancel := context.WithTimeout(context.Background(), time.Duration(tm)*time.Millisecond)
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.CommandContext(cctx, "cmd.exe", "/D", "/S", "/C", r.Command)
	} else {
		c = exec.CommandContext(cctx, "/bin/sh", "-lc", r.Command)
	}
	setProcessGroup(c)
	c.Cancel = func() error { return killProcessTree(c) }
	c.Dir = wd
	c.Env = append(os.Environ(), "NO_COLOR=1", "PAGER=cat", "GIT_PAGER=cat", "SPACEDOCK_WORKSPACE_ID="+w.ID, "SPACEDOCK_WORKSPACE_ROOT="+w.Root)
	out, er := &bounded{max: 1 << 20}, &bounded{max: 1 << 20}
	c.Stdout = out
	c.Stderr = er
	in, e := c.StdinPipe()
	if e != nil {
		cancel()
		return Snapshot{}, e
	}
	if e = c.Start(); e != nil {
		cancel()
		_ = in.Close()
		return Snapshot{}, e
	}
	now := time.Now()
	s := &session{id: id, ws: w.ID, cmd: c, stdin: in, out: out, err: er, started: now, done: make(chan struct{}), stdinOpen: true, snap: Snapshot{SessionID: id, Status: "running", StartedAt: now}}
	m.mu.Lock()
	m.pruneLocked()
	m.s[id] = s
	m.mu.Unlock()
	if r.Stdin != "" {
		_, _ = in.Write([]byte(r.Stdin))
	}
	if !r.KeepStdin {
		_ = in.Close()
		s.mu.Lock()
		s.stdinOpen = false
		s.mu.Unlock()
	}
	go m.wait(s, cctx, cancel)
	t := time.NewTimer(time.Duration(ym) * time.Millisecond)
	defer t.Stop()
	select {
	case <-s.done:
	case <-t.C:
	}
	return m.snapshot(s), nil
}
func (m *Manager) wait(s *session, cctx context.Context, cancel context.CancelFunc) {
	e := s.cmd.Wait()
	cancel()
	s.mu.Lock()
	if s.killed {
		s.snap.Status = "killed"
	} else if s.timedOut || cctx.Err() == context.DeadlineExceeded {
		s.snap.Status = "timed_out"
	} else if e != nil {
		s.snap.Status = "failed"
	} else {
		s.snap.Status = "exited"
	}
	if s.cmd.ProcessState != nil {
		x := s.cmd.ProcessState.ExitCode()
		s.snap.ExitCode = &x
	}
	if !s.killed {
		s.snap.ElapsedMS = time.Since(s.started).Milliseconds()
	}
	s.expires = time.Now().Add(5 * time.Minute)
	close(s.done)
	s.mu.Unlock()
}
func (m *Manager) snapshot(s *session) Snapshot {
	s.mu.Lock()
	x := s.snap
	s.mu.Unlock()
	x.Stdout, x.StdoutTruncated = m.slice(s.out, 0)
	x.Stderr, x.StderrTruncated = m.slice(s.err, 0)
	if x.Status == "running" {
		x.ElapsedMS = time.Since(s.started).Milliseconds()
	}
	return x
}
func (m *Manager) slice(b *bounded, max int) (string, bool) {
	v, tr := b.snapshot()
	if max <= 0 || len(v) <= max {
		return v, tr
	}
	return v[:max], true
}
func (m *Manager) observe(wid, sid string, max int) (Snapshot, error) {
	m.mu.Lock()
	m.pruneLocked()
	s, ok := m.s[sid]
	m.mu.Unlock()
	if !ok || s.ws != wid {
		return Snapshot{}, fmt.Errorf("unknown command session")
	}
	if max <= 0 {
		max = 262144
	}
	if max > 2<<20 {
		max = 2 << 20
	}
	x := m.snapshot(s)
	x.Stdout, x.StdoutTruncated = m.slice(s.out, max)
	x.Stderr, x.StderrTruncated = m.slice(s.err, max)
	return x, nil
}
func (m *Manager) Observe(wid, sid string, max int) (Snapshot, error) {
	return m.observe(wid, sid, max)
}
func (m *Manager) Act(wid, sid, a, chars string) (Snapshot, error) {
	return m.act(wid, sid, a, chars, 0)
}

func (m *Manager) ActWithMax(wid, sid, a, chars string, max int) (Snapshot, error) {
	return m.act(wid, sid, a, chars, max)
}

func (m *Manager) act(wid, sid, a, chars string, max int) (Snapshot, error) {
	m.mu.Lock()
	m.pruneLocked()
	s, ok := m.s[sid]
	m.mu.Unlock()
	if !ok || s.ws != wid {
		return Snapshot{}, fmt.Errorf("unknown command session")
	}
	s.mu.Lock()
	if s.snap.Status != "running" {
		x := s.snap
		s.mu.Unlock()
		return x, fmt.Errorf("session is not running")
	}
	var e error
	switch a {
	case "stdin":
		if !s.stdinOpen {
			e = fmt.Errorf("stdin is closed")
		} else {
			_, e = s.stdin.Write([]byte(chars))
		}
	case "interrupt":
		e = s.cmd.Process.Signal(os.Interrupt)
	case "terminate":
		e = killProcessTree(s.cmd)
		if e == nil {
			s.killed = true
			s.snap.Status = "killed"
			s.snap.ElapsedMS = time.Since(s.started).Milliseconds()
		}
	default:
		e = fmt.Errorf("invalid action")
	}
	s.mu.Unlock()
	if e != nil {
		return Snapshot{}, e
	}
	return m.observe(wid, sid, max)
}
func (m *Manager) CloseWorkspace(wid string) {
	m.mu.Lock()
	var ids []string
	for id, s := range m.s {
		if s.ws == wid {
			ids = append(ids, id)
			s.mu.Lock()
			if s.snap.Status == "running" {
				s.killed = true
				_ = killProcessTree(s.cmd)
			}
			s.mu.Unlock()
		}
	}
	for _, id := range ids {
		delete(m.s, id)
	}
	m.mu.Unlock()
}
func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.s {
		s.mu.Lock()
		if s.snap.Status == "running" {
			s.killed = true
			_ = killProcessTree(s.cmd)
		}
		s.mu.Unlock()
	}
}
