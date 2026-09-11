package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e RPCError) Error() string { return e.Message }

type RequestHandler func(context.Context, string, json.RawMessage) (any, *RPCError)
type NotificationHandler func(string, json.RawMessage)
type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}
type pending struct{ ch chan rpcMsg }
type Transport struct {
	r              io.ReadCloser
	w              io.WriteCloser
	onRequest      RequestHandler
	onNotification NotificationHandler
	writeMu, mu    sync.Mutex
	pending        map[string]pending
	inbound        map[string]context.CancelFunc
	next           int64
	closed         chan struct{}
	once           sync.Once
}

func NewTransport(r io.ReadCloser, w io.WriteCloser, onRequest RequestHandler, onNotification NotificationHandler) *Transport {
	t := &Transport{r: r, w: w, onRequest: onRequest, onNotification: onNotification, pending: map[string]pending{}, inbound: map[string]context.CancelFunc{}, closed: make(chan struct{})}
	go t.read()
	return t
}
func (t *Transport) Closed() <-chan struct{} { return t.closed }
func (t *Transport) Close() error {
	t.once.Do(func() {
		close(t.closed)
		t.mu.Lock()
		ps := make([]pending, 0, len(t.pending))
		for k, p := range t.pending {
			delete(t.pending, k)
			ps = append(ps, p)
		}
		for _, c := range t.inbound {
			c()
		}
		t.inbound = map[string]context.CancelFunc{}
		t.mu.Unlock()
		for _, p := range ps {
			p.ch <- rpcMsg{Error: &RPCError{Code: -32000, Message: "transport closed"}}
		}
		_ = t.r.Close()
		_ = t.w.Close()
	})
	return nil
}
func (t *Transport) read() {
	sc := bufio.NewScanner(t.r)
	sc.Buffer(make([]byte, 4096), 4<<20)
	for sc.Scan() {
		raw := append([]byte(nil), sc.Bytes()...)
		var fields map[string]json.RawMessage
		var m rpcMsg
		if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(raw, &m) != nil || m.JSONRPC != "2.0" {
			_ = t.Close()
			return
		}
		_, hasMethod := fields["method"]
		_, hasID := fields["id"]
		if hasMethod {
			if m.Method == "$/cancel_request" {
				var p struct {
					RequestID json.RawMessage `json:"requestId"`
				}
				if json.Unmarshal(m.Params, &p) == nil {
					t.mu.Lock()
					if c := t.inbound[string(p.RequestID)]; c != nil {
						c()
					}
					t.mu.Unlock()
				}
				continue
			}
			if !hasID || string(m.ID) == "null" {
				if t.onNotification != nil {
					func() { defer func() { recover() }(); t.onNotification(m.Method, m.Params) }()
				}
				continue
			}
			go t.handleRequest(m)
			continue
		}
		_, hasResult := fields["result"]
		_, hasError := fields["error"]
		if !hasID || hasResult == hasError {
			_ = t.Close()
			return
		}
		k := string(m.ID)
		t.mu.Lock()
		p, ok := t.pending[k]
		if ok {
			delete(t.pending, k)
		}
		t.mu.Unlock()
		if ok {
			p.ch <- m
		}
	}
	_ = t.Close()
}
func (t *Transport) handleRequest(m rpcMsg) {
	k := string(m.ID)
	ctx, cancel := context.WithCancel(context.Background())
	t.mu.Lock()
	if _, exists := t.inbound[k]; exists {
		t.mu.Unlock()
		_ = t.send(rpcMsg{JSONRPC: "2.0", ID: m.ID, Error: &RPCError{Code: -32600, Message: "invalid request"}})
		cancel()
		return
	}
	t.inbound[k] = cancel
	t.mu.Unlock()
	defer func() { cancel(); t.mu.Lock(); delete(t.inbound, k); t.mu.Unlock() }()
	var result any
	var e *RPCError
	if t.onRequest == nil {
		e = &RPCError{Code: -32601, Message: "Method not found"}
	} else {
		func() {
			defer func() {
				if recover() != nil {
					e = &RPCError{Code: -32603, Message: "Internal error"}
				}
			}()
			result, e = t.onRequest(ctx, m.Method, m.Params)
		}()
	}
	if e != nil {
		_ = t.send(rpcMsg{JSONRPC: "2.0", ID: m.ID, Error: e})
	} else {
		encoded, me := json.Marshal(result)
		if me != nil {
			_ = t.send(rpcMsg{JSONRPC: "2.0", ID: m.ID, Error: &RPCError{Code: -32603, Message: "Internal error"}})
		} else {
			_ = t.send(rpcMsg{JSONRPC: "2.0", ID: m.ID, Result: encoded})
		}
	}
}
func (t *Transport) send(m rpcMsg) error {
	b, e := json.Marshal(m)
	if e != nil {
		return e
	}
	if len(b) > 4<<20 {
		return fmt.Errorf("ACP message exceeds 4 MiB")
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	select {
	case <-t.closed:
		return fmt.Errorf("transport closed")
	default:
	}
	if _, e = t.w.Write(append(b, '\n')); e != nil {
		_ = t.Close()
	}
	return e
}
func (t *Transport) Request(ctx context.Context, method string, params any, out any) error {
	pr, e := json.Marshal(params)
	if e != nil {
		return e
	}
	id := atomic.AddInt64(&t.next, 1)
	raw, _ := json.Marshal(id)
	k := string(raw)
	ch := make(chan rpcMsg, 1)
	t.mu.Lock()
	t.pending[k] = pending{ch}
	t.mu.Unlock()
	if e = t.send(rpcMsg{JSONRPC: "2.0", ID: raw, Method: method, Params: pr}); e != nil {
		t.mu.Lock()
		delete(t.pending, k)
		t.mu.Unlock()
		return e
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return *m.Error
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(m.Result, out)
	case <-ctx.Done():
		t.mu.Lock()
		_, ok := t.pending[k]
		delete(t.pending, k)
		t.mu.Unlock()
		if ok {
			_ = t.Notify("$/cancel_request", map[string]any{"requestId": id})
		}
		return ctx.Err()
	case <-t.closed:
		return fmt.Errorf("transport closed")
	}
}
func (t *Transport) Notify(method string, params any) error {
	p, e := json.Marshal(params)
	if e != nil {
		return e
	}
	return t.send(rpcMsg{JSONRPC: "2.0", Method: method, Params: p})
}
func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
