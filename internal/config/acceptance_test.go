package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func baseConfigForAcceptance(t *testing.T) Config {
	t.Helper()
	d := t.TempDir()
	token := filepath.Join(d, "owner.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("t", 24)), 0600); err != nil {
		t.Fatal(err)
	}
	return Config{StateDir: filepath.Join(d, "state"), Server: ServerConfig{OAuth: OAuthConfig{OwnerTokenFile: token}}, AllowedRoots: []RootConfig{{ID: "root", Path: d, Permissions: []string{"workspace.manage", "agent.execute"}}}}
}

func TestConfigDefaultsURLsPermissionsAndWorktreeRoot(t *testing.T) {
	c := baseConfigForAcceptance(t)
	if err := c.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if c.Server.Host != "127.0.0.1" || c.Server.Port != 8766 || c.Server.PublicBaseURL != "http://127.0.0.1:8766" {
		t.Fatalf("server defaults=%+v", c.Server)
	}
	if c.Server.OAuth.AccessTokenTTLSeconds != 3600 || c.Server.OAuth.RefreshTokenTTLSeconds != 2592000 || len(c.Server.OAuth.Scopes) != 1 || c.Server.OAuth.Scopes[0] != "spacedock" {
		t.Fatalf("OAuth defaults=%+v", c.Server.OAuth)
	}
	if c.Worktree.Root != filepath.Join(c.StateDir, "worktrees") || c.Agents.MaxConcurrent != 4 || c.Agents.Codex.Command != "codex" {
		t.Fatalf("worktree/agent defaults=%+v/%+v", c.Worktree, c.Agents)
	}
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		found := false
		for _, got := range c.Server.AllowedHosts {
			if got == host {
				found = true
			}
		}
		if !found {
			t.Errorf("default allowed host missing: %s (%v)", host, c.Server.AllowedHosts)
		}
	}
	if !contains(c.AllowedRoots[0].Permissions, "agent.execute") {
		t.Fatalf("agent.execute permission missing: %v", c.AllowedRoots[0].Permissions)
	}
	if c.MCPURL() != "http://127.0.0.1:8766/mcp" {
		t.Fatalf("MCPURL=%q", c.MCPURL())
	}
}

func TestConfigPublicURLAndAgentProfileValidation(t *testing.T) {
	for name, raw := range map[string]string{
		"remote http":     "http://remote.example",
		"query":           "https://remote.example/path?x=1",
		"fragment":        "https://remote.example/#x",
		"userinfo":        "https://user:pass@remote.example",
		"nonroot path":    "https://remote.example/api",
		"relative":        "remote.example",
		"loopback scheme": "ftp://127.0.0.1",
	} {
		c := baseConfigForAcceptance(t)
		c.Server.PublicBaseURL = raw
		if err := c.NormalizeAndValidate(); err == nil {
			t.Errorf("%s accepted: %s", name, raw)
		}
	}
	accepted := baseConfigForAcceptance(t)
	accepted.Server.PublicBaseURL = "https://remote.example/"
	if err := accepted.NormalizeAndValidate(); err != nil || accepted.Server.PublicBaseURL != "https://remote.example" {
		t.Fatalf("remote HTTPS validation=%v URL=%q", err, accepted.Server.PublicBaseURL)
	}

	endpoint := filepath.Join(t.TempDir(), "fake-acp")
	if err := os.WriteFile(endpoint, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	valid := baseConfigForAcceptance(t)
	valid.ACP.Endpoints = []ACPEndpointConfig{{ID: "ep", Command: endpoint}}
	valid.Agents.Profiles = []AgentProfileConfig{{ID: "worker", Provider: "acp", EndpointID: "ep", ConfigOptions: map[string]any{"verbose": true}}}
	if err := valid.NormalizeAndValidate(); err != nil || valid.Agents.Profiles[0].Name != "worker" || valid.Agents.Profiles[0].PermissionPolicy != "auto" {
		t.Fatalf("valid ACP profile normalization=%v profile=%+v", err, valid.Agents.Profiles)
	}
	for _, permissionPolicy := range []string{"auto", "manual", "allow_once"} {
		c := baseConfigForAcceptance(t)
		c.ACP.Endpoints = []ACPEndpointConfig{{ID: "ep", Command: endpoint}}
		c.Agents.Profiles = []AgentProfileConfig{{ID: "worker", Provider: "acp", EndpointID: "ep", PermissionPolicy: permissionPolicy}}
		if err := c.NormalizeAndValidate(); err != nil || c.Agents.Profiles[0].PermissionPolicy != permissionPolicy {
			t.Errorf("ACP permission policy %q normalization=%v profile=%+v", permissionPolicy, err, c.Agents.Profiles)
		}
	}
	for name, mutate := range map[string]func(*Config){
		"unknown endpoint": func(c *Config) { c.Agents.Profiles[0].EndpointID = "missing" },
		"bad policy":       func(c *Config) { c.Agents.Profiles[0].PermissionPolicy = "always" },
		"bad option":       func(c *Config) { c.Agents.Profiles[0].ConfigOptions = map[string]any{"n": 1} },
		"ACP codex field":  func(c *Config) { c.Agents.Profiles[0].Model = "gpt-5.6-luna" },
		"duplicate profile": func(c *Config) {
			c.Agents.Profiles = append(c.Agents.Profiles, AgentProfileConfig{ID: "worker", Provider: "acp", EndpointID: "ep"})
		},
	} {
		c := baseConfigForAcceptance(t)
		c.ACP.Endpoints = []ACPEndpointConfig{{ID: "ep", Command: endpoint}}
		c.Agents.Profiles = []AgentProfileConfig{{ID: "worker", Provider: "acp", EndpointID: "ep"}}
		mutate(&c)
		if err := c.NormalizeAndValidate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	codex := baseConfigForAcceptance(t)
	codex.Agents.Profiles = []AgentProfileConfig{{ID: "codex-worker", Provider: "codex", Model: "gpt-5.6-luna", Effort: "medium", WriteMode: "allowed"}}
	if err := codex.NormalizeAndValidate(); err != nil {
		t.Fatalf("valid Codex profile rejected: %v", err)
	}
	if codex.Agents.Profiles[0].WriteMode != "allowed" {
		t.Fatalf("codex write mode=%q", codex.Agents.Profiles[0].WriteMode)
	}
	defaultCodex := baseConfigForAcceptance(t)
	defaultCodex.Agents.Profiles = []AgentProfileConfig{{ID: "codex-worker", Provider: "codex"}}
	if err := defaultCodex.NormalizeAndValidate(); err != nil || defaultCodex.Agents.Profiles[0].WriteMode != "read_only" {
		t.Fatalf("default Codex profile normalization=%v profile=%+v", err, defaultCodex.Agents.Profiles)
	}
	for name, mutate := range map[string]func(*Config){
		"missing provider":        func(c *Config) { c.Agents.Profiles[0].Provider = "" },
		"codex endpoint":          func(c *Config) { c.Agents.Profiles[0].EndpointID = "ep" },
		"codex ACP options":       func(c *Config) { c.Agents.Profiles[0].ConfigOptions = map[string]any{"model": "x"} },
		"codex permission policy": func(c *Config) { c.Agents.Profiles[0].PermissionPolicy = "manual" },
		"codex bad write mode":    func(c *Config) { c.Agents.Profiles[0].WriteMode = "unsafe" },
	} {
		c := baseConfigForAcceptance(t)
		c.ACP.Endpoints = []ACPEndpointConfig{{ID: "ep", Command: endpoint}}
		c.Agents.Profiles = []AgentProfileConfig{{ID: "codex-worker", Provider: "codex"}}
		mutate(&c)
		if err := c.NormalizeAndValidate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
