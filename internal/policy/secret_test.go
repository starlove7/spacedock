package policy

import "testing"

func TestValidateRecallContentSecretsAndPlaceholders(t *testing.T) {
	for _, s := range []string{"-----BEGIN RSA PRIVATE KEY-----", "token ghp_12345678abcdefgh", "password: real-secret", "api_key=abcd1234"} {
		if err := ValidateRecallContent(s); err == nil {
			t.Errorf("secret accepted: %q", s)
		}
	}
	for _, s := range []string{"password: <PASSWORD>", "api_key: YOUR_API_KEY", "secret: REDACTED", "token: ${TOKEN}", "normal notes"} {
		if err := ValidateRecallContent(s); err != nil {
			t.Errorf("placeholder rejected: %q: %v", s, err)
		}
	}
}
