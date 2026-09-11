package acp

import (
	"context"
	"fmt"
	"github.com/starlove7/spacedock/internal/buildinfo"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Endpoint struct {
	ID      string
	Name    string
	Command string
	Args    []string
	EnvFrom map[string]string
}
type stderrTail struct {
	mu sync.Mutex
	b  []byte
}

func (s *stderrTail) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b = append(s.b, p...)
	if len(s.b) > 64<<10 {
		s.b = append([]byte(nil), s.b[len(s.b)-(64<<10):]...)
	}
	return len(p), nil
}
func (s *stderrTail) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(append([]byte(nil), s.b...))
}

type Client struct {
	cmd      *exec.Cmd
	t        *Transport
	stderr   *stderrTail
	mu       sync.Mutex
	init     InitializeResult
	closed   bool
	waitOnce sync.Once
	waitErr  error
}

func redacted(msg string, secrets []string) string {
	for _, v := range secrets {
		if v != "" {
			msg = strings.ReplaceAll(msg, v, "[REDACTED]")
		}
	}
	return msg
}
func (c *Client) diagnostic(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if d := strings.TrimSpace(c.stderr.String()); d != "" {
		msg += "; stderr: " + d
	}
	return fmt.Errorf("%s", redacted(msg, nil))
}
func StartClient(ctx context.Context, e Endpoint, cwd string, onRequest RequestHandler, onNotification NotificationHandler) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	env := os.Environ()
	secrets := []string{}
	for dst, src := range e.EnvFrom {
		v, ok := os.LookupEnv(src)
		if !ok {
			return nil, fmt.Errorf("missing environment variable %s", src)
		}
		secrets = append(secrets, v)
		prefix := dst + "="
		filtered := env[:0]
		for _, item := range env {
			if !strings.HasPrefix(item, prefix) {
				filtered = append(filtered, item)
			}
		}
		env = filtered
		env = append(env, prefix+v)
	}
	cmd := exec.Command(e.Command, e.Args...)
	cmd.Dir = cwd
	cmd.Env = env
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	errp, err := cmd.StderrPipe()
	if err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		_ = in.Close()
		_ = out.Close()
		_ = errp.Close()
		return nil, fmt.Errorf("ACP start failed: %s", redacted(err.Error(), secrets))
	}
	tail := &stderrTail{}
	c := &Client{cmd: cmd, stderr: tail}
	go func() { _, _ = io.Copy(tail, errp) }()
	go func() { c.waitOnce.Do(func() { c.waitErr = cmd.Wait() }) }()
	c.t = NewTransport(out, in, onRequest, onNotification)
	ictx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var res InitializeResult
	params := map[string]any{"protocolVersion": ProtocolVersion, "clientCapabilities": map[string]any{}, "clientInfo": AgentInfo{Name: "spacedock", Title: "SpaceDock", Version: buildinfo.Version}}
	if err = c.t.Request(ictx, "initialize", params, &res); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("ACP initialize failed: %s", redacted(appendDiag(err.Error(), tail.String()), secrets))
	}
	if res.ProtocolVersion != ProtocolVersion {
		_ = c.Close()
		return nil, fmt.Errorf("unsupported ACP protocol version %d; stderr: %s", res.ProtocolVersion, redacted(tail.String(), secrets))
	}
	if res.AgentCapabilities == nil {
		res.AgentCapabilities = map[string]any{}
	}
	if res.AuthMethods == nil {
		res.AuthMethods = []any{}
	}
	c.mu.Lock()
	c.init = res
	c.mu.Unlock()
	return c, nil
}
func appendDiag(msg, diag string) string {
	if strings.TrimSpace(diag) != "" {
		return msg + "; stderr: " + strings.TrimSpace(diag)
	}
	return msg
}
func (c *Client) Initialize() InitializeResult { c.mu.Lock(); defer c.mu.Unlock(); return c.init }
func (c *Client) Request(ctx context.Context, method string, params any, out any) error {
	return c.t.Request(ctx, method, params, out)
}
func (c *Client) Notify(method string, params any) error { return c.t.Notify(method, params) }
func (c *Client) Closed() <-chan struct{}                { return c.t.Closed() }
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	t := c.t
	cmd := c.cmd
	c.mu.Unlock()
	if t != nil {
		_ = t.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	c.waitOnce.Do(func() { c.waitErr = cmd.Wait() })
	return nil
}
