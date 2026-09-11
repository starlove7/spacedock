package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOAuthStorePKCEOneUseRefreshRotationPersistenceAndSecretFreeState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "oauth-state.json")
	s, err := NewOAuthStore(statePath, time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	client, err := s.RegisterClient(ClientRegistration{
		ClientName:              "acceptance-client",
		RedirectURIs:            []string{"https://chatgpt.com/callback"},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
	})
	if err != nil || client.ClientID == "" {
		t.Fatalf("RegisterClient=%+v err=%v", client, err)
	}

	verifier := strings.Repeat("v", 43)
	codeChallenge := pkceS256(verifier)
	code, err := s.CreateAuthorizationCode(AuthorizationCodeInput{
		ClientID: client.ClientID, RedirectURI: client.RedirectURIs[0],
		CodeChallenge: codeChallenge, Resource: "https://spacedock.example/mcp",
		Scopes: []string{"spacedock", "workspace"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if code == "" {
		t.Fatal("empty authorization code")
	}
	if _, err := s.ExchangeAuthorizationCode(client.ClientID, code, client.RedirectURIs[0], "wrong-verifier", "https://spacedock.example/mcp"); err == nil {
		t.Fatal("wrong verifier accepted")
	}
	first, err := s.ExchangeAuthorizationCode(client.ClientID, code, client.RedirectURIs[0], verifier, "https://spacedock.example/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if first.AccessToken == "" || first.RefreshToken == "" || len(first.Scopes) != 2 {
		t.Fatalf("unexpected token pair: %+v", first)
	}
	if _, err := s.ExchangeAuthorizationCode(client.ClientID, code, client.RedirectURIs[0], verifier, "https://spacedock.example/mcp"); err == nil {
		t.Fatal("authorization code was reusable")
	}

	id, scopes, resource, _, err := s.VerifyAccessToken(first.AccessToken)
	if err != nil || id != client.ClientID || resource != "https://spacedock.example/mcp" || len(scopes) != 2 {
		t.Fatalf("VerifyAccessToken=%q,%v,%q err=%v", id, scopes, resource, err)
	}
	rotated, err := s.ExchangeRefreshToken(client.ClientID, first.RefreshToken, resource, []string{"spacedock"})
	if err != nil || rotated.RefreshToken == first.RefreshToken || len(rotated.Scopes) != 1 || rotated.Scopes[0] != "spacedock" {
		t.Fatalf("refresh rotation=%+v err=%v", rotated, err)
	}
	if _, err := s.ExchangeRefreshToken(client.ClientID, first.RefreshToken, resource, nil); err == nil {
		t.Fatal("old refresh token remained valid")
	}
	if _, err := s.ExchangeRefreshToken(client.ClientID, rotated.RefreshToken, resource, []string{"workspace"}); err == nil {
		t.Fatal("refresh scope escalation accepted")
	}

	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{first.AccessToken, first.RefreshToken, rotated.AccessToken, rotated.RefreshToken, code} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("raw secret leaked into state file: %q", secret)
		}
	}
	reloaded, err := NewOAuthStore(statePath, time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := reloaded.VerifyAccessToken(rotated.AccessToken); err != nil {
		t.Fatalf("persisted access token not reloadable: %v", err)
	}
}

func pkceS256(verifier string) string {
	// Keep the PKCE computation in the test independent of the store's verifier path.
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
