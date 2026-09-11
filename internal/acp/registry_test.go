package acp

import (
	"github.com/starlove7/spacedock/internal/config"
	"testing"
)

func TestRegistrySortedDefensiveAndNoSecrets(t *testing.T) {
	r, e := NewRegistry(config.ACPConfig{Endpoints: []config.ACPEndpointConfig{{ID: "z", Name: "Z", Command: "secret", Args: []string{"--token"}, EnvFrom: map[string]string{"TOKEN": "SOURCE"}}, {ID: "a", Name: "A"}}})
	if e != nil {
		t.Fatal(e)
	}
	xs := r.List()
	if len(xs) != 2 || xs[0].ID != "a" || xs[1].ID != "z" {
		t.Fatalf("list=%+v", xs)
	}
	e1, _ := r.Endpoint("z")
	e1.Args[0] = "mutated"
	e1.EnvFrom["TOKEN"] = "mutated"
	e2, _ := r.Endpoint("z")
	if e2.Args[0] != "--token" || e2.EnvFrom["TOKEN"] != "SOURCE" {
		t.Fatal("defensive copy failed")
	}
}
func TestRegistryDuplicateRejected(t *testing.T) {
	_, e := NewRegistry(config.ACPConfig{Endpoints: []config.ACPEndpointConfig{{ID: "x"}, {ID: "x"}}})
	if e == nil {
		t.Fatal("duplicate accepted")
	}
}
