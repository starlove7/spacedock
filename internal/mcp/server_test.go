package mcp

import (
	"github.com/starlove7/spacedock/internal/app"
	"github.com/starlove7/spacedock/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHTTPHealthAuthAndUnknownPath(t *testing.T) {
	d := t.TempDir()
	owner := filepath.Join(d, "owner.token")
	if e := os.WriteFile(owner, []byte("owner-token-for-server-test-1234567890\n"), 0600); e != nil {
		t.Fatal(e)
	}
	c := config.Config{StateDir: d, Server: config.ServerConfig{OAuth: config.OAuthConfig{OwnerTokenFile: owner}}}
	if e := c.NormalizeAndValidate(); e != nil {
		t.Fatal(e)
	}
	a, e := app.New(c)
	if e != nil {
		t.Fatal(e)
	}
	h, e := New(a, c).HTTPHandler()
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		path string
		code int
		auth bool
	}{{"/healthz", 200, false}, {"/mcp", 401, false}, {"/unknown", 404, false}} {
		r := httptest.NewRequest(http.MethodPost, tc.path, nil)
		r.Host = "127.0.0.1"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if tc.code != 0 && w.Code != tc.code {
			t.Errorf("%s got %d want %d", tc.path, w.Code, tc.code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.Host = "attacker.example"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("host mismatch status=%d", w.Code)
	}
}
