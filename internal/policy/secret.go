package policy

import (
	"fmt"
	"regexp"
	"strings"
)

var pemRE = regexp.MustCompile(`-----BEGIN [^-\n]+ PRIVATE KEY-----`)
var tokenRE = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_\-]{8,}|github_pat_[A-Za-z0-9_\-]{8,})\b`)
var secretRE = regexp.MustCompile(`(?i)\b(password|passwd|api_key|apikey|access_token|refresh_token|secret|cookie|authorization)\b\s*(?:=|:)\s*["']?([^\s,"'}]+)`)

func ValidateRecallContent(content string) error {
	if pemRE.MatchString(content) || tokenRE.MatchString(content) {
		return fmt.Errorf("content appears to contain a secret")
	}
	for _, m := range secretRE.FindAllStringSubmatch(content, -1) {
		v := m[2]
		u := strings.ToUpper(v)
		if len(v) >= 4 && v != "***" && !strings.Contains(v, "<") && !strings.Contains(v, "${") && !strings.Contains(u, "YOUR_") && !strings.Contains(u, "REDACTED") && !strings.Contains(u, "EXAMPLE") && !strings.Contains(u, "PLACEHOLDER") {
			return fmt.Errorf("content appears to contain a secret")
		}
	}
	return nil
}
