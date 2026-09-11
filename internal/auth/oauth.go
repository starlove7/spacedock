package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type ClientRegistration struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	ApplicationType         string   `json:"application_type,omitempty"`
	IssuedAt                int64    `json:"client_id_issued_at"`
}
type AuthorizationCodeInput struct {
	ClientID, RedirectURI, CodeChallenge, Resource string
	Scopes                                         []string
}
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	Scopes       []string
	Resource     string
}
type oauthClientState struct {
	Registration ClientRegistration `json:"registration"`
}
type oauthTokenState struct {
	ClientID  string    `json:"client_id"`
	Scopes    []string  `json:"scopes"`
	Resource  string    `json:"resource"`
	ExpiresAt time.Time `json:"expires_at"`
	Refresh   bool      `json:"refresh"`
}
type oauthState struct {
	SchemaVersion int                         `json:"schema_version"`
	Clients       map[string]oauthClientState `json:"clients"`
	AccessTokens  map[string]oauthTokenState  `json:"access_tokens"`
	RefreshTokens map[string]oauthTokenState  `json:"refresh_tokens"`
}
type codeState struct {
	AuthorizationCodeInput
	ExpiresAt time.Time
}
type OAuthStore struct {
	path                  string
	accessTTL, refreshTTL time.Duration
	mu                    sync.Mutex
	state                 oauthState
	codes                 map[string]codeState
	closed                bool
}

func randString(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}
func hash(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func NewOAuthStore(path string, accessTTL, refreshTTL time.Duration) (*OAuthStore, error) {
	s := &OAuthStore{path: path, accessTTL: accessTTL, refreshTTL: refreshTTL, codes: map[string]codeState{}, state: oauthState{SchemaVersion: 1, Clients: map[string]oauthClientState{}, AccessTokens: map[string]oauthTokenState{}, RefreshTokens: map[string]oauthTokenState{}}}
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil {
		return nil, e
	}
	if len(b) > 4<<20 {
		return nil, fmt.Errorf("OAuth persistent state corrupt")
	}
	if e = json.Unmarshal(b, &s.state); e != nil || s.state.SchemaVersion != 1 || s.state.Clients == nil || s.state.AccessTokens == nil || s.state.RefreshTokens == nil {
		return nil, fmt.Errorf("OAuth persistent state corrupt")
	}
	s.pruneLocked()
	return s, nil
}
func (s *OAuthStore) pruneLocked() {
	now := time.Now()
	for k, v := range s.state.AccessTokens {
		if now.After(v.ExpiresAt) {
			delete(s.state.AccessTokens, k)
		}
	}
	for k, v := range s.state.RefreshTokens {
		if now.After(v.ExpiresAt) {
			delete(s.state.RefreshTokens, k)
		}
	}
}
func (s *OAuthStore) persistLocked() error {
	b, e := json.Marshal(s.state)
	if e != nil {
		return e
	}
	d := filepath.Dir(s.path)
	f, e := os.CreateTemp(d, ".oauth-state-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(name, s.path)
	}
	return e
}
func (s *OAuthStore) RegisterClient(r ClientRegistration) (ClientRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.state.Clients) >= 1024 {
		return r, fmt.Errorf("client limit reached")
	}
	id, e := randString("spc_")
	if e != nil {
		return r, e
	}
	r.ClientID = id
	r.IssuedAt = time.Now().Unix()
	s.state.Clients[id] = oauthClientState{r}
	if e := s.persistLocked(); e != nil {
		delete(s.state.Clients, id)
		return ClientRegistration{}, e
	}
	return r, nil
}
func (s *OAuthStore) Client(id string) (ClientRegistration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.state.Clients[id]
	return x.Registration, ok
}
func (s *OAuthStore) CreateAuthorizationCode(in AuthorizationCodeInput) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.state.Clients[in.ClientID]; !ok {
		return "", fmt.Errorf("unknown client")
	}
	code, e := randString("code_")
	if e != nil {
		return "", e
	}
	s.codes[hash(code)] = codeState{in, time.Now().Add(5 * time.Minute)}
	return code, nil
}
func (s *OAuthStore) issueLocked(clientID string, scopes []string, resource string) (TokenPair, error) {
	s.pruneLocked()
	if len(s.state.AccessTokens) >= 2048 || len(s.state.RefreshTokens) >= 2048 {
		return TokenPair{}, fmt.Errorf("token limit reached")
	}
	a, e := randString("")
	if e != nil {
		return TokenPair{}, e
	}
	r, e := randString("")
	if e != nil {
		return TokenPair{}, e
	}
	now := time.Now()
	v := oauthTokenState{clientID, append([]string(nil), scopes...), resource, now.Add(s.accessTTL), false}
	accessKey, refreshKey := hash(a), hash(r)
	s.state.AccessTokens[accessKey] = v
	s.state.RefreshTokens[refreshKey] = oauthTokenState{clientID, append([]string(nil), scopes...), resource, now.Add(s.refreshTTL), true}
	if e = s.persistLocked(); e != nil {
		delete(s.state.AccessTokens, accessKey)
		delete(s.state.RefreshTokens, refreshKey)
		return TokenPair{}, e
	}
	return TokenPair{a, r, int(s.accessTTL / time.Second), scopes, resource}, nil
}
func (s *OAuthStore) ExchangeAuthorizationCode(clientID, code, redirectURI, verifier, resource string) (TokenPair, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := hash(code)
	c, ok := s.codes[k]
	if !ok || time.Now().After(c.ExpiresAt) || c.ClientID != clientID || c.RedirectURI != redirectURI || c.Resource != resource {
		return TokenPair{}, fmt.Errorf("invalid authorization code")
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(challenge), []byte(c.CodeChallenge)) != 1 {
		return TokenPair{}, fmt.Errorf("invalid code verifier")
	}
	delete(s.codes, k)
	return s.issueLocked(clientID, c.Scopes, resource)
}
func (s *OAuthStore) ExchangeRefreshToken(clientID, raw, resource string, scopes []string) (TokenPair, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := hash(raw)
	v, ok := s.state.RefreshTokens[k]
	if !ok || !v.Refresh || time.Now().After(v.ExpiresAt) || v.ClientID != clientID || v.Resource != resource {
		return TokenPair{}, fmt.Errorf("invalid refresh token")
	}
	if len(scopes) == 0 {
		scopes = v.Scopes
	}
	for _, x := range scopes {
		found := false
		for _, old := range v.Scopes {
			if x == old {
				found = true
			}
		}
		if !found {
			return TokenPair{}, fmt.Errorf("invalid refresh scope")
		}
	}
	old := v
	delete(s.state.RefreshTokens, k)
	pair, e := s.issueLocked(clientID, scopes, resource)
	if e != nil {
		s.state.RefreshTokens[k] = old
	}
	return pair, e
}
func (s *OAuthStore) VerifyAccessToken(raw string) (string, []string, string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := hash(raw)
	v, ok := s.state.AccessTokens[k]
	if !ok || v.Refresh || time.Now().After(v.ExpiresAt) {
		if ok {
			delete(s.state.AccessTokens, k)
			_ = s.persistLocked()
		}
		return "", nil, "", time.Time{}, fmt.Errorf("invalid access token")
	}
	return v.ClientID, append([]string(nil), v.Scopes...), v.Resource, v.ExpiresAt, nil
}
