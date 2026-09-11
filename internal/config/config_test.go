package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func validYAML(root, token string) string {
	return "state_dir: " + filepath.Dir(token) + "\nserver:\n  host: 127.0.0.1\n  port: 8766\n  oauth:\n    owner_token_file: " + token + "\nallowed_roots:\n  - id: app\n    path: " + root + "\n    permissions: [fs.read, fs.read]\n"
}
func TestLoadValidationContracts(t *testing.T) {
	root := t.TempDir()
	d := t.TempDir()
	token := filepath.Join(d, "token")
	os.WriteFile(token, []byte(strings.Repeat("t", 24)), 0600)
	for name, body := range map[string]string{
		"unknown":        validYAML(root, token) + "unknown: true\n",
		"trailing":       validYAML(root, token) + "---\nstate_dir: other\n",
		"duplicate id":   validYAML(root, token) + "  - id: app\n    path: " + filepath.Join(d, "two") + "\n",
		"duplicate path": strings.Replace(validYAML(root, token), "permissions: [fs.read, fs.read]", "permissions: [fs.read]\n  - id: other\n    path: "+root, 1),
	} {
		p := filepath.Join(d, name+".yaml")
		os.WriteFile(p, []byte(body), 0600)
		if _, err := Load(p); err == nil {
			t.Errorf("%s YAML accepted", name)
		}
	}
	if err := (&Config{AllowedRoots: []RootConfig{{ID: "root", Path: "/", Permissions: []string{"fs.read"}}}}).NormalizeAndValidate(); err == nil {
		t.Error("filesystem root accepted")
	}
	c := Config{StateDir: filepath.Join(d, "state"), Server: ServerConfig{Host: "127.0.0.1", OAuth: OAuthConfig{OwnerTokenFile: token}}, AllowedRoots: []RootConfig{{ID: "app", Path: root, Permissions: []string{"fs.read", "fs.read"}}}}
	if err := c.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if len(c.AllowedRoots[0].Permissions) != 1 {
		t.Fatalf("permissions not deduped: %#v", c.AllowedRoots[0].Permissions)
	}
	c.Server.Host = "0.0.0.0"
	c.Server.PublicBaseURL = "http://example.com"
	if err := c.NormalizeAndValidate(); err == nil {
		t.Error("remote HTTP public base URL accepted")
	}
}

func TestSensitivePathConfigContracts(t *testing.T) {
	root := t.TempDir()
	d := t.TempDir()
	token := filepath.Join(d, "token")
	os.WriteFile(token, []byte(strings.Repeat("t", 24)), 0600)

	plain := filepath.Join(d, "plain.yaml")
	if err := os.WriteFile(plain, []byte(validYAML(root, token)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(plain); err != nil {
		t.Fatalf("config without security rejected: %v", err)
	}

	withPatterns := filepath.Join(d, "patterns.yaml")
	body := validYAML(root, token) + "security:\n  sensitive_paths:\n    additional_patterns:\n      - \"  *.SECRET  \"\n      - \".npmrc\"\n      - \"*.secret\"\n"
	if err := os.WriteFile(withPatterns, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(withPatterns)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"*.secret", ".npmrc"}
	if !reflect.DeepEqual(c.Security.SensitivePaths.AdditionalPatterns, want) {
		t.Fatalf("patterns=%#v, want %#v", c.Security.SensitivePaths.AdditionalPatterns, want)
	}

	for _, tc := range []struct {
		name    string
		pattern string
	}{
		{"slash", "nested/*.secret"},
		{"backslash", `nested\\*.secret`},
		{"invalid glob", "[bad"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(d, tc.name+".yaml")
			body := validYAML(root, token) + "security:\n  sensitive_paths:\n    additional_patterns:\n      - \"" + tc.pattern + "\"\n"
			if err := os.WriteFile(p, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(p); err == nil {
				t.Errorf("invalid sensitive pattern accepted: %q", tc.pattern)
			}
		})
	}

	disable := filepath.Join(d, "disable.yaml")
	if err := os.WriteFile(disable, []byte(validYAML(root, token)+"security:\n  sensitive_paths:\n    disable_defaults: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(disable); err == nil {
		t.Error("unsupported disable_defaults field accepted")
	}
}

func TestACPAndExistingTokenValidation(t *testing.T) {
	root := t.TempDir()
	d := t.TempDir()
	token := filepath.Join(d, "token")
	os.WriteFile(token, []byte(strings.Repeat("t", 24)), 0600)
	cmd := filepath.Join(d, "run")
	os.WriteFile(cmd, []byte("#!/bin/sh\n"), 0755)
	c := Config{StateDir: d, Server: ServerConfig{Host: "127.0.0.1", OAuth: OAuthConfig{OwnerTokenFile: token}}, AllowedRoots: []RootConfig{{ID: "r", Path: root}}, ACP: ACPConfig{Endpoints: []ACPEndpointConfig{{ID: "e", Command: cmd, EnvFrom: map[string]string{"OK": "PATH"}}}}}
	if err := c.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	c.ACP.Endpoints[0].EnvFrom = map[string]string{"bad-name": "PATH"}
	if err := c.NormalizeAndValidate(); err == nil {
		t.Error("invalid env_from accepted")
	}
	os.WriteFile(token, []byte("bad"), 0600)
	c.ACP.Endpoints = nil
	if err := c.NormalizeAndValidate(); err == nil {
		t.Error("existing invalid token accepted")
	}
}
