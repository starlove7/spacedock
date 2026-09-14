package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestAntigravityBuiltinACP verifies the Antigravity launch contract copied from DevSpace.
func TestAntigravityBuiltinACP(t *testing.T) {
	root := t.TempDir()
	state := t.TempDir()
	token := filepath.Join(state, "token")
	if err := os.WriteFile(token, []byte(strings.Repeat("t", 24)), 0600); err != nil {
		t.Fatal(err)
	}
	commandName := "agy_acp_server.par"
	if runtime.GOOS == "windows" {
		commandName = "agy_acp_server.exe"
	}
	command := filepath.Join(state, commandName)
	if err := os.WriteFile(command, []byte("stub"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", state)
	t.Setenv("PATH", state)
	t.Setenv("ANTIGRAVITY_COMMAND", "")
	t.Setenv("AGY_ACP_COMMAND", "")

	c := Config{
		StateDir:     state,
		Server:       ServerConfig{Host: "127.0.0.1", OAuth: OAuthConfig{OwnerTokenFile: token}},
		AllowedRoots: []RootConfig{{ID: "r", Path: root}},
		ACP:          ACPConfig{Endpoints: []ACPEndpointConfig{{ID: "antigravity", Builtin: "ANTIGRAVITY"}}},
	}
	if err := c.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	ep := c.ACP.Endpoints[0]
	if ep.Command != command {
		t.Fatalf("command=%q, want %q", ep.Command, command)
	}
	if ep.Builtin != "antigravity" {
		t.Fatalf("builtin=%q", ep.Builtin)
	}
	wantArgs := []string(nil)
	if runtime.GOOS == "linux" {
		wantArgs = []string{"--uid="}
	}
	if !reflect.DeepEqual(ep.Args, wantArgs) {
		t.Fatalf("args=%#v, want %#v", ep.Args, wantArgs)
	}
}

// TestAntigravityBuiltinACPWrapperFallback verifies the authenticated per-user wrapper is preferred
// even when systemd PATH does not contain ~/.local/bin.
func TestAntigravityBuiltinACPWrapperFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix per-user wrapper layout")
	}
	root := t.TempDir()
	state := t.TempDir()
	home := t.TempDir()
	token := filepath.Join(state, "token")
	if err := os.WriteFile(token, []byte(strings.Repeat("t", 24)), 0600); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(home, ".local", "bin", "agy_acp_server")
	if err := os.MkdirAll(filepath.Dir(wrapper), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Join(state, "empty-path"))
	t.Setenv("ANTIGRAVITY_COMMAND", "")
	t.Setenv("AGY_ACP_COMMAND", "")

	c := Config{
		StateDir:     state,
		Server:       ServerConfig{Host: "127.0.0.1", OAuth: OAuthConfig{OwnerTokenFile: token}},
		AllowedRoots: []RootConfig{{ID: "r", Path: root}},
		ACP:          ACPConfig{Endpoints: []ACPEndpointConfig{{ID: "antigravity", Builtin: "antigravity"}}},
	}
	if err := c.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	ep := c.ACP.Endpoints[0]
	if ep.Command != wrapper {
		t.Fatalf("command=%q, want wrapper %q", ep.Command, wrapper)
	}
	if ep.Args != nil {
		t.Fatalf("wrapper must keep args nil so it can supply its own UID/runtime setup: %#v", ep.Args)
	}
}

// TestAntigravityBuiltinACPOverrides verifies explicit config wins and environment aliases are supported.
func TestAntigravityBuiltinACPOverrides(t *testing.T) {
	root := t.TempDir()
	state := t.TempDir()
	token := filepath.Join(state, "token")
	if err := os.WriteFile(token, []byte(strings.Repeat("t", 24)), 0600); err != nil {
		t.Fatal(err)
	}
	explicit := filepath.Join(state, "explicit-antigravity")
	envCommand := filepath.Join(state, "env-antigravity")
	aliasCommand := filepath.Join(state, "alias-antigravity")
	for _, path := range []string{explicit, envCommand, aliasCommand} {
		if err := os.WriteFile(path, []byte("stub"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ANTIGRAVITY_COMMAND", envCommand)
	t.Setenv("AGY_ACP_COMMAND", aliasCommand)

	newConfig := func(endpoint ACPEndpointConfig) Config {
		return Config{
			StateDir:     state,
			Server:       ServerConfig{Host: "127.0.0.1", OAuth: OAuthConfig{OwnerTokenFile: token}},
			AllowedRoots: []RootConfig{{ID: "r", Path: root}},
			ACP:          ACPConfig{Endpoints: []ACPEndpointConfig{endpoint}},
		}
	}

	explicitArgs := []string{}
	c := newConfig(ACPEndpointConfig{ID: "antigravity", Builtin: "antigravity", Command: explicit, Args: explicitArgs})
	if err := c.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if c.ACP.Endpoints[0].Command != explicit {
		t.Fatalf("explicit command=%q, want %q", c.ACP.Endpoints[0].Command, explicit)
	}
	if c.ACP.Endpoints[0].Args == nil || len(c.ACP.Endpoints[0].Args) != 0 {
		t.Fatalf("explicit empty args not preserved: %#v", c.ACP.Endpoints[0].Args)
	}

	c = newConfig(ACPEndpointConfig{ID: "antigravity", Builtin: "antigravity"})
	if err := c.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if c.ACP.Endpoints[0].Command != envCommand {
		t.Fatalf("ANTIGRAVITY_COMMAND=%q, want %q", c.ACP.Endpoints[0].Command, envCommand)
	}

	t.Setenv("ANTIGRAVITY_COMMAND", "")
	c = newConfig(ACPEndpointConfig{ID: "antigravity", Builtin: "antigravity"})
	if err := c.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if c.ACP.Endpoints[0].Command != aliasCommand {
		t.Fatalf("AGY_ACP_COMMAND=%q, want %q", c.ACP.Endpoints[0].Command, aliasCommand)
	}
}

// TestUnknownACPBuiltinRejectsConfig keeps built-in launch behavior closed to known adapters.
func TestUnknownACPBuiltinRejectsConfig(t *testing.T) {
	root := t.TempDir()
	state := t.TempDir()
	token := filepath.Join(state, "token")
	if err := os.WriteFile(token, []byte(strings.Repeat("t", 24)), 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{
		StateDir:     state,
		Server:       ServerConfig{Host: "127.0.0.1", OAuth: OAuthConfig{OwnerTokenFile: token}},
		AllowedRoots: []RootConfig{{ID: "r", Path: root}},
		ACP:          ACPConfig{Endpoints: []ACPEndpointConfig{{ID: "bad", Builtin: "unknown"}}},
	}
	if err := c.NormalizeAndValidate(); err == nil || !strings.Contains(err.Error(), "unknown ACP builtin") {
		t.Fatalf("unexpected error: %v", err)
	}
}
