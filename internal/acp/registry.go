package acp

import (
	"fmt"
	"github.com/starlove7/spacedock/internal/config"
	"sort"
)

type EndpointSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Registry struct{ eps map[string]Endpoint }

func NewRegistry(c config.ACPConfig) (*Registry, error) {
	r := &Registry{eps: map[string]Endpoint{}}
	for _, e := range c.Endpoints {
		if _, ok := r.eps[e.ID]; ok {
			return nil, fmt.Errorf("duplicate endpoint")
		}
		r.eps[e.ID] = Endpoint{ID: e.ID, Name: e.Name, Command: e.Command, Args: append([]string(nil), e.Args...), EnvFrom: copyEnv(e.EnvFrom)}
	}
	return r, nil
}
func (r *Registry) List() []EndpointSummary {
	out := []EndpointSummary{}
	for _, e := range r.eps {
		out = append(out, EndpointSummary{e.ID, e.Name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func copyEnv(in map[string]string) map[string]string {
	o := map[string]string{}
	for k, v := range in {
		o[k] = v
	}
	return o
}
func (r *Registry) Endpoint(id string) (Endpoint, bool) {
	e, ok := r.eps[id]
	if !ok {
		return Endpoint{}, false
	}
	e.Args = append([]string(nil), e.Args...)
	e.EnvFrom = copyEnv(e.EnvFrom)
	return e, true
}
