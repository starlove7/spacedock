package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/starlove7/spacedock/internal/buildinfo"
	"github.com/starlove7/spacedock/internal/config"
	"github.com/starlove7/spacedock/internal/workspace"
)

const (
	codexMaxMessageBytes = 4 << 20
	codexMaxTurnItems    = 10_000
	codexMaxStderrBytes  = 32 << 10
)

type codexRPCMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

type codexPending struct{ ch chan codexRPCMessage }

type codexTurn struct {
	threadID string
	turnID   string
	items    []json.RawMessage
	done     chan json.RawMessage
}

type codexProviderSession struct {
	profile config.AgentProfileConfig
	root    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  *tailBuffer

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]codexPending
	nextID  int64
	thread  string
	active  *codexTurn
	fatal   error
	closed  chan struct{}
	once    sync.Once
}

type tailBuffer struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.max {
		b.data = append([]byte(nil), b.data[len(b.data)-b.max:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.data...))
}

func newCodexProviderSession(ctx context.Context, agents config.AgentsConfig, profile config.AgentProfileConfig, w *workspace.Workspace) (providerSession, error) {
	command := strings.TrimSpace(agents.Codex.Command)
	if command == "" {
		command = "codex"
	}
	executable, err := resolveCodexCommand(command)
	if err != nil {
		return nil, err
	}
	cmd := codexCommand(executable, "app-server")
	cmd.Dir = w.Root
	cmd.Env = codexEnvironment(os.Environ())
	setCodexProcessGroup(cmd)
	stderr := &tailBuffer{max: codexMaxStderrBytes}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start Codex CLI: %w", err)
	}
	s := &codexProviderSession{
		profile: profile,
		root:    w.Root,
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		pending: map[string]codexPending{},
		closed:  make(chan struct{}),
	}
	go s.readLoop()
	go func() {
		err := cmd.Wait()
		s.fail(fmt.Errorf("Codex app-server exited: %w", normalizeExitError(err)))
	}()
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var initialized any
	if err := s.request(initCtx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "spacedock", "title": "SpaceDock", "version": buildinfo.Version},
		"capabilities": map[string]any{},
	}, &initialized); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("initialize Codex app-server: %w", err)
	}
	if err := s.notify("initialized", nil); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("initialize Codex app-server: %w", err)
	}
	return s, nil
}

func (s *codexProviderSession) Provider() string { return "codex" }
func (s *codexProviderSession) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.thread
}

func (s *codexProviderSession) Run(ctx context.Context, prompt string) (providerTurnResult, error) {
	if err := ctx.Err(); err != nil {
		return providerTurnResult{}, err
	}
	s.mu.Lock()
	threadID := s.thread
	if s.active != nil {
		s.mu.Unlock()
		return providerTurnResult{}, fmt.Errorf("Codex thread already has an active turn")
	}
	s.mu.Unlock()

	method := "thread/start"
	threadParams := map[string]any{
		"cwd":            s.root,
		"approvalPolicy": "never",
		"sandbox":        codexSandbox(s.profile.WriteMode),
	}
	if threadID != "" {
		method = "thread/resume"
		threadParams["threadId"] = threadID
	}
	if strings.TrimSpace(s.profile.Model) != "" {
		threadParams["model"] = strings.TrimSpace(s.profile.Model)
	}
	var threadResponse struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := s.request(ctx, method, threadParams, &threadResponse); err != nil {
		return providerTurnResult{}, err
	}
	if strings.TrimSpace(threadResponse.Thread.ID) == "" {
		return providerTurnResult{}, fmt.Errorf("Codex app-server did not return a thread id")
	}
	threadID = threadResponse.Thread.ID
	turn := &codexTurn{threadID: threadID, done: make(chan json.RawMessage, 1), items: make([]json.RawMessage, 0, 32)}
	s.mu.Lock()
	if s.active != nil {
		s.mu.Unlock()
		return providerTurnResult{}, fmt.Errorf("Codex thread already has an active turn")
	}
	s.thread = threadID
	s.active = turn
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.active == turn {
			s.active = nil
		}
		s.mu.Unlock()
	}()

	turnParams := map[string]any{
		"threadId":       threadID,
		"input":          []map[string]any{{"type": "text", "text": prompt}},
		"approvalPolicy": "never",
		"sandboxPolicy":  codexSandboxPolicy(s.profile.WriteMode),
	}
	if strings.TrimSpace(s.profile.Model) != "" {
		turnParams["model"] = strings.TrimSpace(s.profile.Model)
	}
	if strings.TrimSpace(s.profile.Effort) != "" {
		turnParams["effort"] = strings.TrimSpace(s.profile.Effort)
	}
	var turnResponse struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := s.request(ctx, "turn/start", turnParams, &turnResponse); err != nil {
		return providerTurnResult{ProviderSessionID: threadID}, err
	}
	s.mu.Lock()
	if s.active == turn {
		turn.turnID = turnResponse.Turn.ID
	}
	s.mu.Unlock()

	select {
	case params := <-turn.done:
		response, failure := s.parseCompletedTurn(params, turn)
		if failure != "" {
			return providerTurnResult{ProviderSessionID: threadID}, fmt.Errorf("Codex turn failed: %s", failure)
		}
		if strings.TrimSpace(response) == "" {
			return providerTurnResult{ProviderSessionID: threadID}, fmt.Errorf("Codex did not return a final assistant response")
		}
		response, truncated := truncateResponse(strings.TrimSpace(response))
		return providerTurnResult{Response: response, ResponseTruncated: truncated, ProviderSessionID: threadID}, nil
	case <-ctx.Done():
		return providerTurnResult{ProviderSessionID: threadID}, ctx.Err()
	case <-s.closed:
		return providerTurnResult{ProviderSessionID: threadID}, fmt.Errorf("%w: %v", ErrProviderInterrupted, s.fatalError())
	}
}

func (s *codexProviderSession) Cancel() error { return s.Close() }

func (s *codexProviderSession) Close() error {
	s.once.Do(func() {
		_ = s.stdin.Close()
		if s.cmd != nil && s.cmd.Process != nil {
			_ = killCodexProcessTree(s.cmd)
		}
		s.fail(fmt.Errorf("Codex app-server closed"))
	})
	return nil
}

func (s *codexProviderSession) request(ctx context.Context, method string, params any, out any) error {
	s.mu.Lock()
	if s.fatal != nil {
		err := s.fatal
		s.mu.Unlock()
		return err
	}
	s.nextID++
	id := s.nextID
	key := fmt.Sprintf("%d", id)
	ch := make(chan codexRPCMessage, 1)
	s.pending[key] = codexPending{ch: ch}
	s.mu.Unlock()
	message := map[string]any{"id": id, "method": method}
	if params != nil {
		message["params"] = params
	}
	if err := s.write(message); err != nil {
		s.mu.Lock()
		delete(s.pending, key)
		s.mu.Unlock()
		return err
	}
	select {
	case response := <-ch:
		if len(response.Error) > 0 && string(response.Error) != "null" {
			return fmt.Errorf("Codex app-server %s: %s", method, codexProtocolError(response.Error))
		}
		if out == nil {
			return nil
		}
		if len(response.Result) == 0 {
			return fmt.Errorf("Codex app-server %s returned no result", method)
		}
		return json.Unmarshal(response.Result, out)
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, key)
		s.mu.Unlock()
		return ctx.Err()
	case <-s.closed:
		return fmt.Errorf("%w: %v", ErrProviderInterrupted, s.fatalError())
	}
}

func (s *codexProviderSession) notify(method string, params any) error {
	message := map[string]any{"method": method}
	if params != nil {
		message["params"] = params
	}
	return s.write(message)
}

func (s *codexProviderSession) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > codexMaxMessageBytes {
		return fmt.Errorf("Codex app-server message exceeds 4 MiB")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.closed:
		return fmt.Errorf("Codex app-server closed")
	default:
	}
	_, err = s.stdin.Write(append(data, '\n'))
	return err
}

func (s *codexProviderSession) readLoop() {
	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 4096), codexMaxMessageBytes)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var fields map[string]json.RawMessage
		var message codexRPCMessage
		if json.Unmarshal(line, &fields) != nil || json.Unmarshal(line, &message) != nil {
			s.fail(fmt.Errorf("Codex app-server emitted malformed JSON"))
			return
		}
		id, hasID := fields["id"]
		_, hasMethod := fields["method"]
		if hasID && !hasMethod {
			key := codexIDKey(id)
			s.mu.Lock()
			pending, ok := s.pending[key]
			if ok {
				delete(s.pending, key)
			}
			s.mu.Unlock()
			if ok {
				pending.ch <- message
			}
			continue
		}
		if hasID && hasMethod {
			_ = s.write(map[string]any{"id": json.RawMessage(id), "error": map[string]any{"code": -32601, "message": "Unsupported app-server request: " + message.Method}})
			continue
		}
		if !hasMethod {
			continue
		}
		s.handleEvent(message.Method, message.Params)
	}
	if err := scanner.Err(); err != nil {
		s.fail(fmt.Errorf("Codex app-server stdout: %w", err))
		return
	}
	s.fail(fmt.Errorf("Codex app-server stdout closed"))
}

func (s *codexProviderSession) handleEvent(method string, params json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn := s.active
	if turn == nil {
		return
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(params, &envelope) != nil {
		return
	}
	threadID := rawString(envelope["threadId"])
	turnID := rawString(envelope["turnId"])
	if turnID == "" {
		var turnObject struct {
			ID string `json:"id"`
		}
		if raw := envelope["turn"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &turnObject)
			turnID = turnObject.ID
		}
	}
	if threadID != "" && threadID != turn.threadID {
		return
	}
	if turn.turnID != "" && turnID != "" && turnID != turn.turnID {
		return
	}
	if item := envelope["item"]; len(item) > 0 && string(item) != "null" {
		turn.items = append(turn.items, append(json.RawMessage(nil), item...))
		if len(turn.items) > codexMaxTurnItems {
			turn.items = turn.items[len(turn.items)-codexMaxTurnItems:]
		}
	}
	if method == "turn/completed" {
		select {
		case turn.done <- append(json.RawMessage(nil), params...):
		default:
		}
	}
}

func (s *codexProviderSession) parseCompletedTurn(params json.RawMessage, turn *codexTurn) (string, string) {
	var envelope struct {
		Turn struct {
			Status string            `json:"status"`
			Items  []json.RawMessage `json:"items"`
			Error  struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(params, &envelope)
	items := envelope.Turn.Items
	if len(items) == 0 {
		s.mu.Lock()
		items = append([]json.RawMessage(nil), turn.items...)
		s.mu.Unlock()
	}
	if len(items) > codexMaxTurnItems {
		items = items[len(items)-codexMaxTurnItems:]
	}
	response := ""
	for _, raw := range items {
		var item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &item) == nil && (item.Type == "agentMessage" || item.Type == "agent_message") && item.Text != "" {
			response = item.Text
		}
	}
	if envelope.Turn.Status == "failed" {
		if strings.TrimSpace(envelope.Turn.Error.Message) != "" {
			return response, strings.TrimSpace(envelope.Turn.Error.Message)
		}
		return response, "Codex turn failed"
	}
	return response, ""
}

func (s *codexProviderSession) fail(err error) {
	if err == nil {
		err = fmt.Errorf("Codex app-server closed")
	}
	stderr := strings.TrimSpace(s.stderr.String())
	if stderr != "" {
		err = fmt.Errorf("%w\nstderr:\n%s", err, stderr)
	}
	s.mu.Lock()
	if s.fatal != nil {
		s.mu.Unlock()
		return
	}
	s.fatal = err
	pending := make([]codexPending, 0, len(s.pending))
	for key, value := range s.pending {
		delete(s.pending, key)
		pending = append(pending, value)
	}
	s.mu.Unlock()
	for _, value := range pending {
		value.ch <- codexRPCMessage{Error: json.RawMessage(`{"message":"transport closed"}`)}
	}
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
}

func (s *codexProviderSession) fatalError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fatal == nil {
		return fmt.Errorf("Codex app-server closed")
	}
	return s.fatal
}

func resolveCodexCommand(command string) (string, error) {
	if filepath.IsAbs(command) || strings.ContainsAny(command, `/\\`) {
		st, err := os.Stat(command)
		if err != nil || !st.Mode().IsRegular() {
			return "", fmt.Errorf("Codex executable not found: %s", command)
		}
		return command, nil
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf("Codex executable not found: %s", command)
	}
	return resolved, nil
}

func codexCommand(executable string, args ...string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		ext := strings.ToLower(filepath.Ext(executable))
		if ext == ".cmd" || ext == ".bat" {
			commandLine := `"` + strings.ReplaceAll(executable, `"`, `\"`) + `"`
			for _, arg := range args {
				commandLine += ` "` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
			}
			return exec.Command("cmd.exe", "/D", "/S", "/C", commandLine)
		}
	}
	return exec.Command(executable, args...)
}

func codexEnvironment(source []string) []string {
	out := make([]string, 0, len(source))
	for _, value := range source {
		if strings.HasPrefix(value, "CODEX_INTERNAL_ORIGINATOR_OVERRIDE=") {
			continue
		}
		out = append(out, value)
	}
	return out
}

func codexSandbox(writeMode string) string {
	switch writeMode {
	case "allowed":
		return "workspace-write"
	case "full_access":
		return "danger-full-access"
	default:
		return "read-only"
	}
}

func codexSandboxPolicy(writeMode string) map[string]any {
	switch writeMode {
	case "allowed":
		return map[string]any{"type": "workspaceWrite", "networkAccess": true}
	case "full_access":
		return map[string]any{"type": "dangerFullAccess"}
	default:
		return map[string]any{"type": "readOnly"}
	}
}

func codexIDKey(raw json.RawMessage) string {
	var number json.Number
	if json.Unmarshal(raw, &number) == nil && number.String() != "" {
		return number.String()
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}

func rawString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return ""
}

func codexProtocolError(raw json.RawMessage) string {
	var value struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value.Message) != "" {
		if value.Code != nil {
			return fmt.Sprintf("%v: %s", value.Code, value.Message)
		}
		return value.Message
	}
	return strings.TrimSpace(string(raw))
}

func normalizeExitError(err error) error {
	if err == nil {
		return fmt.Errorf("process exited")
	}
	return err
}

func setCodexProcessGroup(cmd *exec.Cmd) {
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

func killCodexProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
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
