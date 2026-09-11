package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	internalauth "github.com/starlove7/spacedock/internal/auth"
	"github.com/starlove7/spacedock/internal/config"
	"html/template"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type OAuthHTTP struct {
	cfg    config.Config
	store  *internalauth.OAuthStore
	owner  string
	mu     sync.Mutex
	limits map[string]rateWindow
}
type rateWindow struct {
	at time.Time
	n  int
}

func NewOAuthHTTP(c config.Config) (*OAuthHTTP, error) {
	b, e := os.ReadFile(c.Server.OAuth.OwnerTokenFile)
	if e != nil {
		return nil, e
	}
	s, e := internalauth.NewOAuthStore(c.StateDir+"/oauth-state.json", time.Duration(c.Server.OAuth.AccessTokenTTLSeconds)*time.Second, time.Duration(c.Server.OAuth.RefreshTokenTTLSeconds)*time.Second)
	if e != nil {
		return nil, e
	}
	return &OAuthHTTP{cfg: c, store: s, owner: strings.TrimSpace(string(b)), limits: map[string]rateWindow{}}, nil
}
func (o *OAuthHTTP) Close() error { return nil }
func (o *OAuthHTTP) allowedResource(v string) bool {
	if v == o.cfg.MCPURL() {
		return true
	}
	for _, x := range o.cfg.Server.OAuth.AllowedResourceURLs {
		if x == v {
			return true
		}
	}
	return false
}
func (o *OAuthHTTP) ip(r *http.Request) string {
	if o.cfg.Server.TrustProxy {
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		if len(parts) > 0 {
			if ip := net.ParseIP(strings.TrimSpace(parts[0])); ip != nil {
				return ip.String()
			}
		}
		if h, _, e := net.SplitHostPort(r.RemoteAddr); e == nil {
			return h
		}
		return r.RemoteAddr
	}
	h, _, e := net.SplitHostPort(r.RemoteAddr)
	if e == nil {
		return h
	}
	return r.RemoteAddr
}
func (o *OAuthHTTP) limit(r *http.Request, max int, d time.Duration) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	k := o.ip(r) + fmt.Sprint(max)
	v := o.limits[k]
	now := time.Now()
	if now.Sub(v.at) >= d {
		v = rateWindow{at: now}
	}
	v.n++
	o.limits[k] = v
	return v.n <= max
}
func (o *OAuthHTTP) RegisterRoutes(m *http.ServeMux) {
	m.HandleFunc("/.well-known/oauth-protected-resource/mcp", o.metadataResource)
	m.HandleFunc("/.well-known/oauth-authorization-server", o.metadataServer)
	m.HandleFunc("/register", o.register)
	m.HandleFunc("/oauth/authorize", o.authorize)
	m.HandleFunc("/oauth/token", o.token)
}
func method(w http.ResponseWriter, r *http.Request, allow string) bool {
	allowed := false
	switch allow {
	case "GET, HEAD":
		allowed = r.Method == http.MethodGet || r.Method == http.MethodHead
	case "POST":
		allowed = r.Method == http.MethodPost
	case "GET, POST":
		allowed = r.Method == http.MethodGet || r.Method == http.MethodPost
	}
	if allowed {
		return true
	}
	w.Header().Set("Allow", allow)
	w.WriteHeader(http.StatusMethodNotAllowed)
	return false
}
func jsonOut(w http.ResponseWriter, v any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func oauthErr(w http.ResponseWriter, code, desc string, status int) {
	jsonOut(w, map[string]string{"error": code, "error_description": desc}, status)
}
func (o *OAuthHTTP) metadataResource(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET, HEAD") {
		return
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	jsonOut(w, map[string]any{"resource": o.cfg.MCPURL(), "authorization_servers": []string{o.cfg.Server.PublicBaseURL}, "scopes_supported": o.cfg.Server.OAuth.Scopes, "bearer_methods_supported": []string{"header"}}, http.StatusOK)
}
func (o *OAuthHTTP) metadataServer(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET, HEAD") {
		return
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	jsonOut(w, map[string]any{"issuer": o.cfg.Server.PublicBaseURL, "authorization_endpoint": o.cfg.Server.PublicBaseURL + "/oauth/authorize", "token_endpoint": o.cfg.Server.PublicBaseURL + "/oauth/token", "registration_endpoint": o.cfg.Server.PublicBaseURL + "/register", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "resource_indicators_supported": true, "authorization_response_iss_parameter_supported": true}, http.StatusOK)
}
func (o *OAuthHTTP) register(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	if !o.limit(r, 30, time.Minute) {
		oauthErr(w, "rate_limit", "registration rate limit exceeded", 429)
		return
	}
	mt, _, ce := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ce != nil || mt != "application/json" {
		oauthErr(w, "invalid_client_metadata", "Content-Type must be application/json", 400)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var in struct {
		ClientName              string   `json:"client_name"`
		RedirectURIs            []string `json:"redirect_uris"`
		ApplicationType         string   `json:"application_type"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(&in); e != nil || len(in.RedirectURIs) < 1 || len(in.RedirectURIs) > 10 {
		oauthErr(w, "invalid_client_metadata", "invalid client metadata", 400)
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		oauthErr(w, "invalid_client_metadata", "invalid client metadata", 400)
		return
	}
	for _, raw := range in.RedirectURIs {
		u, e := url.Parse(raw)
		if e != nil || u.Fragment != "" || u.User != nil || u.Host == "" || (!strings.EqualFold(u.Scheme, "https") && !(strings.EqualFold(u.Scheme, "http") && configLoop(u.Hostname()))) {
			oauthErr(w, "invalid_redirect_uri", "invalid redirect URI", 400)
			return
		}
		ok := false
		for _, h := range o.cfg.Server.OAuth.AllowedRedirectHosts {
			if strings.EqualFold(h, u.Hostname()) {
				ok = true
			}
		}
		if !ok {
			oauthErr(w, "invalid_redirect_uri", "invalid redirect URI", 400)
			return
		}
	}
	if in.TokenEndpointAuthMethod != "" && in.TokenEndpointAuthMethod != "none" {
		oauthErr(w, "invalid_client_metadata", "invalid client metadata", 400)
		return
	}
	if len(in.GrantTypes) == 0 {
		in.GrantTypes = []string{"authorization_code", "refresh_token"}
	}
	if len(in.ResponseTypes) == 0 {
		in.ResponseTypes = []string{"code"}
	}
	in.GrantTypes = dedupe(in.GrantTypes)
	in.ResponseTypes = dedupe(in.ResponseTypes)
	if !allowedValues(in.GrantTypes, []string{"authorization_code", "refresh_token"}) || !allowedValues(in.ResponseTypes, []string{"code"}) {
		oauthErr(w, "invalid_client_metadata", "unsupported grant or response type", 400)
		return
	}
	if in.ApplicationType == "" {
		if configLoop(mustURLHost(in.RedirectURIs[0])) {
			in.ApplicationType = "native"
		} else {
			in.ApplicationType = "web"
		}
	}
	if in.ApplicationType != "native" && in.ApplicationType != "web" {
		oauthErr(w, "invalid_client_metadata", "invalid application type", 400)
		return
	}
	v, e := o.store.RegisterClient(internalauth.ClientRegistration{ClientName: in.ClientName, RedirectURIs: in.RedirectURIs, GrantTypes: in.GrantTypes, ResponseTypes: in.ResponseTypes, TokenEndpointAuthMethod: "none", ApplicationType: in.ApplicationType})
	if e != nil {
		jsonOut(w, map[string]string{"error": "server_error"}, 500)
		return
	}
	jsonOut(w, v, 201)
}
func configLoop(h string) bool {
	return strings.EqualFold(h, "localhost") || net.ParseIP(h) != nil && net.ParseIP(h).IsLoopback()
}
func mustURLHost(s string) string { u, _ := url.Parse(s); return u.Hostname() }
func allowedValues(values, allowed []string) bool {
	seen := map[string]bool{}
	for _, v := range values {
		ok := false
		for _, a := range allowed {
			if v == a {
				ok = true
			}
		}
		if !ok || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}
func dedupe(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
func (o *OAuthHTTP) authorize(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET, POST") {
		return
	}
	if r.Method == "POST" && !o.limit(r, 10, 5*time.Minute) {
		http.Error(w, "rate limit", 429)
		return
	}
	q := r.URL.Query()
	if r.Method == "POST" {
		mt, _, ce := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if ce != nil || mt != "application/x-www-form-urlencoded" {
			oauthErr(w, "invalid_request", "Content-Type must be application/x-www-form-urlencoded", 400)
			return
		}
		if strings.Contains(r.URL.RawQuery, "owner_token=") {
			oauthErr(w, "invalid_request", "owner_token must be in form body", 400)
			return
		}
		if e := r.ParseForm(); e != nil {
			oauthErr(w, "invalid_request", "invalid form", 400)
			return
		}
		q = r.Form
		if len(q["owner_token"]) > 1 {
			oauthErr(w, "invalid_request", "duplicate owner_token", 400)
			return
		}
	}
	client, ok := o.store.Client(q.Get("client_id"))
	if !ok {
		oauthErr(w, "invalid_request", "invalid authorization request", 400)
		return
	}
	valid := q.Get("response_type") == "code" && q.Get("code_challenge_method") == "S256" && q.Get("code_challenge") != "" && o.allowedResource(q.Get("resource"))
	redirectOK := false
	for _, x := range client.RedirectURIs {
		if x == q.Get("redirect_uri") {
			redirectOK = true
		}
	}
	valid = valid && redirectOK
	if !valid {
		oauthErr(w, "invalid_request", "invalid authorization request", 400)
		return
	}
	requested := strings.Fields(q.Get("scope"))
	configured := map[string]bool{}
	for _, s := range o.cfg.Server.OAuth.Scopes {
		configured[s] = true
	}
	if len(requested) == 0 {
		requested = append([]string(nil), o.cfg.Server.OAuth.Scopes...)
	}
	validated := []string{}
	seenScope := map[string]bool{}
	for _, s := range requested {
		if !configured[s] {
			oauthErr(w, "invalid_scope", "scope is not allowed", 400)
			return
		}
		if !seenScope[s] {
			seenScope[s] = true
			validated = append(validated, s)
		}
	}
	if r.Method == "GET" {
		o.renderApproval(w, q, http.StatusOK)
		return
	}
	if subtle.ConstantTimeCompare([]byte(o.owner), []byte(q.Get("owner_token"))) != 1 {
		o.renderApproval(w, q, http.StatusUnauthorized)
		return
	}
	code, e := o.store.CreateAuthorizationCode(internalauth.AuthorizationCodeInput{ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), CodeChallenge: q.Get("code_challenge"), Resource: q.Get("resource"), Scopes: validated})
	if e != nil {
		http.Error(w, "server error", 500)
		return
	}
	u, _ := url.Parse(q.Get("redirect_uri"))
	v := u.Query()
	v.Set("code", code)
	if q.Get("state") != "" {
		v.Set("state", q.Get("state"))
	}
	v.Set("iss", o.cfg.Server.PublicBaseURL)
	u.RawQuery = v.Encode()
	http.Redirect(w, r, u.String(), 302)
}
func (o *OAuthHTTP) renderApproval(w http.ResponseWriter, q url.Values, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "<form method=post action=/oauth/authorize>")
	for k, v := range q {
		if k != "owner_token" && len(v) > 0 {
			_, _ = io.WriteString(w, `<input type="hidden" name="`+template.HTMLEscapeString(k)+`" value="`+template.HTMLEscapeString(v[0])+`">`)
		}
	}
	_, _ = io.WriteString(w, "<label>Owner token <input name=owner_token type=password></label><button>Approve</button></form>")
}
func (o *OAuthHTTP) token(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	mt, _, ce := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ce != nil || mt != "application/x-www-form-urlencoded" {
		oauthErr(w, "invalid_request", "Content-Type must be application/x-www-form-urlencoded", 400)
		return
	}
	if e := r.ParseForm(); e != nil {
		oauthErr(w, "invalid_request", "invalid form", 400)
		return
	}
	resource := r.Form.Get("resource")
	if resource == "" {
		oauthErr(w, "invalid_request", "missing required parameter", 400)
		return
	}
	if !o.allowedResource(resource) {
		oauthErr(w, "invalid_target", "resource mismatch", 400)
		return
	}
	var p internalauth.TokenPair
	var e error
	grant := r.Form.Get("grant_type")
	if grant != "authorization_code" && grant != "refresh_token" {
		oauthErr(w, "unsupported_grant_type", "unsupported grant type", 400)
		return
	}
	switch grant {
	case "authorization_code":
		if r.Form.Get("client_id") == "" || r.Form.Get("code") == "" || r.Form.Get("redirect_uri") == "" || r.Form.Get("code_verifier") == "" || resource == "" {
			oauthErr(w, "invalid_request", "missing required parameter", 400)
			return
		}
		p, e = o.store.ExchangeAuthorizationCode(r.Form.Get("client_id"), r.Form.Get("code"), r.Form.Get("redirect_uri"), r.Form.Get("code_verifier"), resource)
	case "refresh_token":
		if r.Form.Get("client_id") == "" || r.Form.Get("refresh_token") == "" || resource == "" {
			oauthErr(w, "invalid_request", "missing required parameter", 400)
			return
		}
		p, e = o.store.ExchangeRefreshToken(r.Form.Get("client_id"), r.Form.Get("refresh_token"), resource, strings.Fields(r.Form.Get("scope")))
	}
	if e != nil {
		oauthErr(w, "invalid_grant", "invalid grant", 400)
		return
	}
	jsonOut(w, map[string]any{"access_token": p.AccessToken, "token_type": "Bearer", "expires_in": p.ExpiresIn, "refresh_token": p.RefreshToken, "scope": strings.Join(p.Scopes, " ")}, 200)
}
func (o *OAuthHTTP) Protect(next http.Handler) http.Handler {
	return sdkauth.RequireBearerToken(func(ctx context.Context, raw string, r *http.Request) (*sdkauth.TokenInfo, error) {
		id, sc, res, exp, e := o.store.VerifyAccessToken(raw)
		if e != nil || !o.allowedResource(res) {
			return nil, sdkauth.ErrInvalidToken
		}
		return &sdkauth.TokenInfo{Scopes: sc, Expiration: exp, UserID: id, Extra: map[string]any{"resource": res}}, nil
	}, &sdkauth.RequireBearerTokenOptions{ResourceMetadataURL: o.cfg.Server.PublicBaseURL + "/.well-known/oauth-protected-resource/mcp"})(next)
}
