package policy

import (
	"errors"
	"path"
	"strings"
)

var ErrSensitivePath = errors.New("sensitive path access denied")

type SensitivePathPolicy struct {
	additionalPatterns []string
}

func NormalizeSensitivePathPatterns(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > 64 {
		return nil, errors.New("too many sensitive path patterns")
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.ToLower(strings.TrimSpace(raw))
		if v == "" {
			return nil, errors.New("empty sensitive path pattern")
		}
		if len(v) > 128 {
			return nil, errors.New("sensitive path pattern too long")
		}
		if strings.ContainsAny(v, `/\\`) {
			return nil, errors.New("sensitive path pattern must match one component")
		}
		if _, err := path.Match(v, "component"); err != nil {
			return nil, err
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out, nil
}

func NewSensitivePathPolicy(additional []string) (*SensitivePathPolicy, error) {
	normalized, err := NormalizeSensitivePathPatterns(additional)
	if err != nil {
		return nil, err
	}
	return &SensitivePathPolicy{additionalPatterns: normalized}, nil
}

func (p *SensitivePathPolicy) Denied(rel string) bool {
	rel = path.Clean(strings.ReplaceAll(rel, `\`, `/`))
	for _, component := range strings.Split(rel, "/") {
		component = strings.ToLower(component)
		switch component {
		case ".ssh", ".aws", ".gnupg", ".env", "credentials", "credentials.json", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "known_hosts":
			return true
		}
		if strings.HasPrefix(component, ".env.") || strings.HasSuffix(component, ".pem") || strings.HasSuffix(component, ".key") || strings.HasSuffix(component, ".pfx") || strings.HasSuffix(component, ".p12") {
			return true
		}
		for _, pattern := range p.additionalPatterns {
			if ok, _ := path.Match(pattern, component); ok {
				return true
			}
		}
	}
	return false
}
