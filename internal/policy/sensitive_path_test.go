package policy

import (
	"reflect"
	"strings"
	"testing"
)

func TestSensitivePathPolicyAllowsBenignPaths(t *testing.T) {
	p, err := NewSensitivePathPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"src/main.go", ".environment", "credentials.txt", "key.pem.txt"} {
		if p.Denied(path) {
			t.Errorf("benign path denied: %q", path)
		}
	}
}

func TestSensitivePathPolicyDeniesDefaultPatterns(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"ssh directory", ".ssh/config"},
		{"aws directory mixed case", ".AWS/credentials"},
		{"gnupg directory", "nested/.GnUpG/key"},
		{"env exact", ".env"},
		{"env suffix", ".env.production"},
		{"env suffix mixed case", ".ENV.PRODUCTION"},
		{"credentials", "nested/credentials"},
		{"credentials json", "nested/credentials.json"},
		{"rsa key", "nested/id_rsa"},
		{"dsa key", "nested/id_dsa"},
		{"ecdsa key", "nested/id_ecdsa"},
		{"ed25519 key", "nested/id_ed25519"},
		{"known hosts", "nested/known_hosts"},
		{"pem extension", "nested/server.PEM"},
		{"key extension", "nested/server.KeY"},
		{"pfx extension", "nested/server.PFX"},
		{"p12 extension", "nested/server.P12"},
		{"windows separators", `foo\.AWS\credentials`},
	}
	p, err := NewSensitivePathPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !p.Denied(tc.path) {
				t.Errorf("sensitive path allowed: %q", tc.path)
			}
		})
	}
}

func TestSensitivePathPolicyAdditionalPatternsMatchAnyComponent(t *testing.T) {
	p, err := NewSensitivePathPolicy([]string{"*.secret", ".npmrc"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"a.secret", "nested/a.SECRET", "nested/.npmrc", ".NPMRC/deeper/file", "normal.txt"} {
		got := p.Denied(path)
		want := path != "normal.txt"
		if got != want {
			t.Errorf("Denied(%q)=%v, want %v", path, got, want)
		}
	}
}

func TestNormalizeSensitivePathPatterns(t *testing.T) {
	got, err := NormalizeSensitivePathPatterns([]string{"  *.SECRET  ", ".npmrc", "*.secret", " .NPMRC "})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"*.secret", ".npmrc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized=%#v, want %#v", got, want)
	}

	invalid := []struct {
		name string
		in   []string
	}{
		{"blank", []string{"  "}},
		{"slash", []string{"nested/*.secret"}},
		{"backslash", []string{`nested\*.secret`}},
		{"invalid glob", []string{"[bad"}},
		{"too long", []string{strings.Repeat("a", 129)}},
		{"too many", make([]string, 65)},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NormalizeSensitivePathPatterns(tc.in); err == nil {
				t.Errorf("invalid patterns accepted: %#v", tc.in)
			}
		})
	}
}
