package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"
)

type Result any
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Execute(context.Context, json.RawMessage) (Result, error)
}
type Registry struct {
	mu    sync.RWMutex
	items map[string]Tool
}

func NewRegistry() *Registry { return &Registry{items: map[string]Tool{}} }
func (r *Registry) Register(t Tool) error {
	if t == nil || t.Name() == "" {
		return fmt.Errorf("blank tool name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[t.Name()]; ok {
		return fmt.Errorf("duplicate tool")
	}
	r.items[t.Name()] = t
	return nil
}
func (r *Registry) Tools() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o := make([]Tool, 0, len(r.items))
	for _, t := range r.items {
		o = append(o, t)
	}
	sort.Slice(o, func(i, j int) bool { return o[i].Name() < o[j].Name() })
	return o
}
func (r *Registry) Get(n string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.items[n]
	return t, ok
}
func (r *Registry) Call(c context.Context, n string, b json.RawMessage) (Result, error) {
	t, ok := r.Get(n)
	if !ok {
		return nil, fmt.Errorf("unknown tool")
	}
	return t.Execute(c, b)
}

type BasicTool struct {
	NameValue   string
	Desc        string
	SchemaValue json.RawMessage
	Fn          func(context.Context, json.RawMessage) (Result, error)
}

func (t *BasicTool) Name() string                                                 { return t.NameValue }
func (t *BasicTool) Description() string                                          { return t.Desc }
func (t *BasicTool) InputSchema() json.RawMessage                                 { return t.SchemaValue }
func (t *BasicTool) Execute(c context.Context, b json.RawMessage) (Result, error) { return t.Fn(c, b) }
func Decode(b []byte, v any) error {
	raw := bytes.TrimSpace(b)
	if len(raw) == 0 || raw[0] != '{' {
		return fmt.Errorf("input must be a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		if e == nil {
			return fmt.Errorf("trailing JSON")
		}
		return e
	}
	return nil
}
