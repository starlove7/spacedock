package mcp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/starlove7/spacedock/internal/config"
)

func oauthHTTPFixture(t *testing.T) (*OAuthHTTP, *http.ServeMux, configFixture) {
	t.Helper()
	d := t.TempDir()
	owner := "owner-token-for-oauth-tests-1234567890"
	ownerPath := filepath.Join(d, "owner.token")
	if err := os.WriteFile(ownerPath, []byte(owner+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := configFixture{stateDir: d, baseURL: "https://spacedock.example", owner: owner, ownerPath: ownerPath}
	cfg := c.config()
	if err := cfg.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	c.baseURL = cfg.Server.PublicBaseURL
	o, err := NewOAuthHTTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	o.RegisterRoutes(mux)
	return o, mux, c
}

// configFixture keeps this test's config construction local and avoids depending on init's host paths.
type configFixture struct {
	stateDir, baseURL, owner, ownerPath string
}

func (c configFixture) config() config.Config {
	return config.Config{
		StateDir: c.stateDir,
		Server: config.ServerConfig{
			PublicBaseURL: c.baseURL,
			OAuth: config.OAuthConfig{
				OwnerTokenFile: c.ownerPath,
				Scopes:         []string{"spacedock", "workspace"},
				AllowedRedirectHosts: []string{
					"chatgpt.com", "localhost", "127.0.0.1", "::1",
				},
			},
		},
	}
}

func TestOAuthHTTPMetadataDCRValidationOwnerApprovalAndTokenFlows(t *testing.T) {
	_, mux, c := oauthHTTPFixture(t)
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource/mcp"} {
		r := httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != http.StatusOK || !strings.Contains(r.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("metadata GET %s: status=%d content-type=%q", path, r.Code, r.Header().Get("Content-Type"))
		}
		r = httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest(http.MethodHead, path, nil))
		if r.Code != http.StatusOK || r.Body.Len() != 0 {
			t.Fatalf("metadata HEAD %s: status=%d body=%q", path, r.Code, r.Body.String())
		}
		r = httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest(http.MethodPut, path, nil))
		if r.Code != http.StatusMethodNotAllowed || r.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("metadata PUT %s: status=%d allow=%q", path, r.Code, r.Header().Get("Allow"))
		}
	}

	dcr := func(body string, contentType string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		mux.ServeHTTP(r, req)
		return r
	}
	if r := dcr(`{"redirect_uris":["https://chatgpt.com/callback"],"unknown":true}`, "application/json"); r.Code != http.StatusBadRequest {
		t.Fatalf("DCR unknown field status=%d body=%s", r.Code, r.Body.String())
	}
	if r := dcr(`{"redirect_uris":["https://chatgpt.com/callback"]}`, "text/plain"); r.Code != http.StatusBadRequest {
		t.Fatalf("DCR content type status=%d", r.Code)
	}
	for _, redirect := range []string{"ftp://localhost/callback", "https://not-allowed.example/callback", "http://chatgpt.com/callback"} {
		if r := dcr(`{"redirect_uris":["`+redirect+`"]}`, "application/json"); r.Code != http.StatusBadRequest {
			t.Errorf("DCR redirect %s accepted with status=%d", redirect, r.Code)
		}
	}
	registered := dcr(`{"client_name":"acceptance","redirect_uris":["https://chatgpt.com/callback"]}`, "application/json")
	if registered.Code != http.StatusCreated {
		t.Fatalf("DCR success status=%d body=%s", registered.Code, registered.Body.String())
	}
	var client struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(registered.Body.Bytes(), &client); err != nil || client.ClientID == "" {
		t.Fatalf("DCR response=%s err=%v", registered.Body.String(), err)
	}

	verifier := strings.Repeat("v", 43)
	challenge := pkceS256ForHTTP(verifier)
	values := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ClientID},
		"redirect_uri":          {"https://chatgpt.com/callback"},
		"scope":                 {"spacedock"},
		"resource":              {c.config().MCPURL()},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"opaque-state"},
	}
	get := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+values.Encode(), nil)
	mux.ServeHTTP(get, getReq)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "Owner token") {
		t.Fatalf("owner approval GET status=%d body=%s", get.Code, get.Body.String())
	}

	bad := url.Values{}
	for k, v := range values {
		bad[k] = append([]string(nil), v...)
	}
	bad.Set("owner_token", "wrong-owner-token")
	wrong := httptest.NewRecorder()
	wrongReq := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(bad.Encode()))
	wrongReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(wrong, wrongReq)
	if wrong.Code != http.StatusUnauthorized || strings.Contains(wrong.Body.String(), "wrong-owner-token") || strings.Contains(wrong.Body.String(), c.owner) {
		t.Fatalf("wrong owner response status=%d body=%s", wrong.Code, wrong.Body.String())
	}

	approved := httptest.NewRecorder()
	approveReq := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(values.Encode()+"&owner_token="+url.QueryEscape(c.owner)))
	approveReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(approved, approveReq)
	if approved.Code != http.StatusFound {
		t.Fatalf("approved status=%d body=%s", approved.Code, approved.Body.String())
	}
	location, err := url.Parse(approved.Header().Get("Location"))
	if err != nil || location.Query().Get("code") == "" || location.Query().Get("state") != "opaque-state" || location.Query().Get("iss") != c.baseURL {
		t.Fatalf("approved location=%q err=%v", approved.Header().Get("Location"), err)
	}

	token := httptest.NewRecorder()
	tokenValues := url.Values{"grant_type": {"authorization_code"}, "client_id": {client.ClientID}, "code": {location.Query().Get("code")}, "redirect_uri": {"https://chatgpt.com/callback"}, "code_verifier": {verifier}, "resource": {c.config().MCPURL()}}
	tokenReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(tokenValues.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(token, tokenReq)
	if token.Code != http.StatusOK {
		t.Fatalf("authorization_code token status=%d body=%s", token.Code, token.Body.String())
	}
	var pair struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(token.Body.Bytes(), &pair); err != nil || pair.AccessToken == "" || pair.RefreshToken == "" || pair.TokenType != "Bearer" || pair.Scope != "spacedock" {
		t.Fatalf("token response=%s err=%v", token.Body.String(), err)
	}

	refresh := httptest.NewRecorder()
	refreshValues := url.Values{"grant_type": {"refresh_token"}, "client_id": {client.ClientID}, "refresh_token": {pair.RefreshToken}, "resource": {c.config().MCPURL()}, "scope": {"spacedock"}}
	refreshReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(refreshValues.Encode()))
	refreshReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(refresh, refreshReq)
	if refresh.Code != http.StatusOK {
		t.Fatalf("refresh token status=%d body=%s", refresh.Code, refresh.Body.String())
	}
}

func TestOAuthHTTPInvalidScopeResourceAndBearerChallenge(t *testing.T) {
	_, mux, c := oauthHTTPFixture(t)
	registered := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(`{"redirect_uris":["http://localhost/callback"]}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(registered, req)
	if registered.Code != http.StatusCreated {
		t.Fatalf("register status=%d", registered.Code)
	}
	var client struct {
		ClientID string `json:"client_id"`
	}
	_ = json.Unmarshal(registered.Body.Bytes(), &client)
	q := url.Values{"response_type": {"code"}, "client_id": {client.ClientID}, "redirect_uri": {"http://localhost/callback"}, "scope": {"not-configured"}, "resource": {c.config().MCPURL()}, "code_challenge": {"challenge"}, "code_challenge_method": {"S256"}}
	r := httptest.NewRecorder()
	mux.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil))
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "invalid_scope") {
		t.Fatalf("invalid scope status=%d body=%s", r.Code, r.Body.String())
	}
	q.Set("scope", "spacedock")
	q.Set("resource", "https://other.example/mcp")
	r = httptest.NewRecorder()
	mux.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil))
	if r.Code != http.StatusBadRequest {
		t.Fatalf("invalid resource authorization status=%d body=%s", r.Code, r.Body.String())
	}

	protected := (http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	r = httptest.NewRecorder()
	o, _, _ := oauthHTTPFixture(t)
	o.Protect(protected).ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if r.Code != http.StatusUnauthorized || !strings.Contains(r.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("bearer challenge status=%d header=%q body=%s", r.Code, r.Header().Get("WWW-Authenticate"), r.Body.String())
	}
}

func TestOAuthHTTPRateLimitsAndTokenParameterErrors(t *testing.T) {
	_, mux, _ := oauthHTTPFixture(t)
	body := `{"redirect_uris":["http://localhost/callback"]}`
	for i := 0; i < 30; i++ {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(r, req)
		if r.Code != http.StatusCreated {
			t.Fatalf("registration %d status=%d body=%s", i+1, r.Code, r.Body.String())
		}
	}
	r := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(r, req)
	if r.Code != http.StatusTooManyRequests || !strings.Contains(r.Body.String(), "rate_limit") {
		t.Fatalf("DCR rate limit status=%d body=%s", r.Code, r.Body.String())
	}

	_, authorizeMux, c := oauthHTTPFixture(t)
	registered := httptest.NewRecorder()
	registerReq := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	registerReq.Header.Set("Content-Type", "application/json")
	authorizeMux.ServeHTTP(registered, registerReq)
	if registered.Code != http.StatusCreated {
		t.Fatalf("owner rate-limit setup registration status=%d", registered.Code)
	}
	var client struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(registered.Body.Bytes(), &client); err != nil {
		t.Fatal(err)
	}
	q := url.Values{"response_type": {"code"}, "client_id": {client.ClientID}, "redirect_uri": {"http://localhost/callback"}, "scope": {"spacedock"}, "resource": {c.config().MCPURL()}, "code_challenge": {"challenge"}, "code_challenge_method": {"S256"}, "owner_token": {"bad-owner"}}
	for i := 0; i < 10; i++ {
		r := httptest.NewRecorder()
		post := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(q.Encode()))
		post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		authorizeMux.ServeHTTP(r, post)
		if r.Code != http.StatusUnauthorized {
			t.Fatalf("owner approval %d status=%d body=%s", i+1, r.Code, r.Body.String())
		}
	}
	r = httptest.NewRecorder()
	post := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(q.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	authorizeMux.ServeHTTP(r, post)
	if r.Code != http.StatusTooManyRequests {
		t.Fatalf("owner rate limit status=%d body=%s", r.Code, r.Body.String())
	}

	for _, form := range []url.Values{
		{"grant_type": {"authorization_code"}},
		{"grant_type": {"not-supported"}, "resource": {c.config().MCPURL()}},
	} {
		r = httptest.NewRecorder()
		tokenReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
		tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		authorizeMux.ServeHTTP(r, tokenReq)
		if r.Code != http.StatusBadRequest {
			t.Fatalf("token invalid form=%v status=%d body=%s", form, r.Code, r.Body.String())
		}
	}
}

func pkceS256ForHTTP(verifier string) string {
	// The helper is intentionally local to the HTTP acceptance test.
	return base64RawSHA256ForHTTP(verifier)
}

func base64RawSHA256ForHTTP(verifier string) string {
	// Keep imports and the test fixture independent from internal auth implementation details.
	return strings.TrimSpace(string(mustSHA256Base64([]byte(verifier))))
}

func mustSHA256Base64(input []byte) []byte {
	// Filled by the standard-library helper below; this indirection keeps the call site readable.
	sum := sha256.Sum256(input)
	return []byte(base64.RawURLEncoding.EncodeToString(sum[:]))
}
