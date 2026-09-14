package config

import (
	"fmt"
	"github.com/starlove7/spacedock/internal/policy"
	"gopkg.in/yaml.v3"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

type Config struct {
	StateDir     string         `yaml:"state_dir"`
	Server       ServerConfig   `yaml:"server"`
	Worktree     WorktreeConfig `yaml:"worktree"`
	Agents       AgentsConfig   `yaml:"agents"`
	AllowedRoots []RootConfig   `yaml:"allowed_roots"`
	ACP          ACPConfig      `yaml:"acp"`
	Security     SecurityConfig `yaml:"security"`
}
type SecurityConfig struct {
	SensitivePaths SensitivePathsConfig `yaml:"sensitive_paths"`
}
type SensitivePathsConfig struct {
	AdditionalPatterns []string `yaml:"additional_patterns"`
}
type ServerConfig struct {
	Host          string      `yaml:"host"`
	Port          int         `yaml:"port"`
	PublicBaseURL string      `yaml:"public_base_url"`
	AllowedHosts  []string    `yaml:"allowed_hosts"`
	TrustProxy    bool        `yaml:"trust_proxy"`
	OAuth         OAuthConfig `yaml:"oauth"`
}
type OAuthConfig struct {
	OwnerTokenFile         string   `yaml:"owner_token_file"`
	AccessTokenTTLSeconds  int      `yaml:"access_token_ttl_seconds"`
	RefreshTokenTTLSeconds int      `yaml:"refresh_token_ttl_seconds"`
	Scopes                 []string `yaml:"scopes"`
	AllowedResourceURLs    []string `yaml:"allowed_resource_urls"`
	AllowedRedirectHosts   []string `yaml:"allowed_redirect_hosts"`
}
type WorktreeConfig struct {
	Root string `yaml:"root"`
}
type AgentsConfig struct {
	MaxConcurrent int                  `yaml:"max_concurrent"`
	Codex         CodexProviderConfig  `yaml:"codex"`
	Profiles      []AgentProfileConfig `yaml:"profiles,omitempty"`
}
type CodexProviderConfig struct {
	Command string `yaml:"command"`
}
type AgentProfileConfig struct {
	ID               string         `yaml:"id"`
	Name             string         `yaml:"name"`
	Description      string         `yaml:"description"`
	Provider         string         `yaml:"provider"`
	EndpointID       string         `yaml:"endpoint_id"`
	Instructions     string         `yaml:"instructions"`
	PermissionPolicy string         `yaml:"permission_policy"`
	ModeID           string         `yaml:"mode_id"`
	ConfigOptions    map[string]any `yaml:"config_options"`
	Model            string         `yaml:"model"`
	Effort           string         `yaml:"effort"`
	WriteMode        string         `yaml:"write_mode"`
}
type RootConfig struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name"`
	Path        string   `yaml:"path"`
	Permissions []string `yaml:"permissions"`
}
type ACPConfig struct {
	Endpoints []ACPEndpointConfig `yaml:"endpoints"`
}
type ACPEndpointConfig struct {
	ID      string            `yaml:"id"`
	Name    string            `yaml:"name"`
	Builtin string            `yaml:"builtin,omitempty"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	EnvFrom map[string]string `yaml:"env_from"`
}

var idRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var envRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func ExpandHome(p string) (string, error) {
	if p == "~" {
		return os.UserHomeDir()
	}
	if strings.HasPrefix(p, "~/") {
		h, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		return filepath.Join(h, p[2:]), nil
	}
	return p, nil
}
func DefaultConfigPath() (string, error) {
	h, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(h, ".spacedock", "config.yaml"), nil
}
func Load(path string) (Config, error) {
	p, e := ExpandHome(path)
	if e != nil {
		return Config{}, e
	}
	p, e = filepath.Abs(filepath.Clean(p))
	if e != nil {
		return Config{}, e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return Config{}, e
	}
	var c Config
	d := yaml.NewDecoder(strings.NewReader(string(b)))
	d.KnownFields(true)
	if e = d.Decode(&c); e != nil {
		return Config{}, e
	}
	var x any
	if e = d.Decode(&x); e != io.EOF {
		return Config{}, fmt.Errorf("trailing YAML document")
	}
	if e = c.NormalizeAndValidate(); e != nil {
		return Config{}, e
	}
	return c, nil
}
func (c Config) MCPURL() string { return strings.TrimRight(c.Server.PublicBaseURL, "/") + "/mcp" }
func (c Config) ListenAddress() string {
	return net.JoinHostPort(c.Server.Host, fmt.Sprint(c.Server.Port))
}
func (c Config) RootByID(id string) (RootConfig, bool) {
	for _, r := range c.AllowedRoots {
		if r.ID == id {
			return r, true
		}
	}
	return RootConfig{}, false
}
func isLoopback(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
func validateURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme == "" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("invalid public URL")
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("invalid URL path")
	}
	if u.Scheme != "https" {
		if u.Scheme != "http" || !isLoopback(u.Hostname()) {
			return nil, fmt.Errorf("URL must use https, or http on loopback")
		}
	}
	u.Path = ""
	u.RawPath = ""
	return u, nil
}
func validateTokenFile(p string) error {
	st, e := os.Stat(p)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() || st.Size() > 4096 {
		return fmt.Errorf("invalid token file")
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return e
	}
	if len(strings.TrimSpace(string(b))) < 24 {
		return fmt.Errorf("invalid owner token")
	}
	return nil
}

// executableFile reports whether path can be launched directly on this platform.
func executableFile(path string) bool {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || st.Mode().Perm()&0111 != 0
}

// resolveACPExecutable resolves a configured path or PATH command to an absolute executable path.
func resolveACPExecutable(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("ACP command is empty")
	}
	if filepath.IsAbs(command) || strings.ContainsAny(command, `/\\`) {
		candidate := command
		if !filepath.IsAbs(candidate) {
			var err error
			candidate, err = filepath.Abs(filepath.Clean(candidate))
			if err != nil {
				return "", err
			}
		} else {
			candidate = filepath.Clean(candidate)
		}
		if !executableFile(candidate) {
			return "", fmt.Errorf("ACP command is not executable file: %s", candidate)
		}
		return candidate, nil
	}
	candidate, err := exec.LookPath(command)
	if err != nil {
		return "", err
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	if !executableFile(candidate) {
		return "", fmt.Errorf("ACP command is not executable file: %s", candidate)
	}
	return filepath.Clean(candidate), nil
}

// antigravityDefaultCommands returns launch candidates in preference order.
// The installed wrapper is preferred because it may inject runtime libraries and
// identity arguments required by the authenticated Antigravity installation.
func antigravityDefaultCommands() []string {
	if runtime.GOOS == "windows" {
		return []string{"agy_acp_server.exe"}
	}
	out := []string{"agy_acp_server"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, filepath.Join(home, ".local", "bin", "agy_acp_server"))
	}
	out = append(out, "agy_acp_server.par")
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, filepath.Join(home, ".local", "share", "agy_acp_server", "agy_acp_server.par"))
	}
	return out
}

// normalizeAntigravityEndpoint applies DevSpace-compatible Antigravity ACP launch defaults.
func normalizeAntigravityEndpoint(ep *ACPEndpointConfig) error {
	command := strings.TrimSpace(ep.Command)
	if command == "" {
		command = strings.TrimSpace(os.Getenv("ANTIGRAVITY_COMMAND"))
	}
	if command == "" {
		command = strings.TrimSpace(os.Getenv("AGY_ACP_COMMAND"))
	}

	var resolved string
	var err error
	if command != "" {
		resolved, err = resolveACPExecutable(command)
	} else {
		for _, candidate := range antigravityDefaultCommands() {
			resolved, err = resolveACPExecutable(candidate)
			if err == nil {
				break
			}
		}
	}
	if err != nil || resolved == "" {
		if err == nil {
			err = fmt.Errorf("no Antigravity executable candidate resolved")
		}
		return fmt.Errorf("Antigravity ACP command not found: %w", err)
	}
	ep.Command = resolved
	// The packaged agy_acp_server wrapper supplies its own UID/runtime setup.
	// Only a direct Linux .par launch needs the DevSpace-compatible empty UID flag.
	if ep.Args == nil && runtime.GOOS == "linux" && filepath.Base(resolved) == "agy_acp_server.par" {
		ep.Args = []string{"--uid="}
	}
	return nil
}
func NormalizeAgentProfiles(profiles []AgentProfileConfig, endpoints []ACPEndpointConfig) ([]AgentProfileConfig, error) {
	seen := map[string]bool{}
	for i := range profiles {
		p := &profiles[i]
		if !idRE.MatchString(p.ID) || seen[p.ID] {
			return nil, fmt.Errorf("invalid or duplicate agent profile id")
		}
		seen[p.ID] = true
		if p.Name == "" {
			p.Name = p.ID
		}
		p.Provider = strings.ToLower(strings.TrimSpace(p.Provider))
		p.EndpointID = strings.TrimSpace(p.EndpointID)
		p.Model = strings.TrimSpace(p.Model)
		p.Effort = strings.TrimSpace(p.Effort)
		p.WriteMode = strings.ToLower(strings.TrimSpace(p.WriteMode))
		switch p.Provider {
		case "codex":
			if p.EndpointID != "" || p.PermissionPolicy != "" || p.ModeID != "" || len(p.ConfigOptions) != 0 {
				return nil, fmt.Errorf("codex agent profile contains ACP-only fields")
			}
			if p.WriteMode == "" {
				p.WriteMode = "read_only"
			}
			if p.WriteMode != "read_only" && p.WriteMode != "allowed" && p.WriteMode != "full_access" {
				return nil, fmt.Errorf("invalid codex write mode")
			}
		case "acp":
			if p.EndpointID == "" {
				return nil, fmt.Errorf("ACP agent profile requires endpoint_id")
			}
			if p.Model != "" || p.Effort != "" || p.WriteMode != "" {
				return nil, fmt.Errorf("ACP agent profile contains Codex-only fields")
			}
			if p.PermissionPolicy == "" {
				p.PermissionPolicy = "manual"
			}
			if p.PermissionPolicy != "manual" && p.PermissionPolicy != "allow_once" {
				return nil, fmt.Errorf("invalid agent permission policy")
			}
			found := false
			for _, ep := range endpoints {
				if ep.ID == p.EndpointID {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("unknown agent endpoint")
			}
			for _, v := range p.ConfigOptions {
				switch v.(type) {
				case string, bool:
				default:
					return nil, fmt.Errorf("agent config options must be string or bool")
				}
			}
		default:
			return nil, fmt.Errorf("agent profile provider must be codex or acp")
		}
	}
	return profiles, nil
}
func (c *Config) NormalizeAndValidate() error {
	var e error
	c.Security.SensitivePaths.AdditionalPatterns, e = policy.NormalizeSensitivePathPatterns(c.Security.SensitivePaths.AdditionalPatterns)
	if e != nil {
		return e
	}
	c.StateDir, e = ExpandHome(c.StateDir)
	if e != nil {
		return e
	}
	if c.StateDir == "" {
		p, _ := DefaultConfigPath()
		c.StateDir = filepath.Dir(p)
	}
	c.StateDir, e = filepath.Abs(filepath.Clean(c.StateDir))
	if e != nil {
		return e
	}
	if e = os.MkdirAll(c.StateDir, 0700); e != nil {
		return e
	}
	_ = os.Chmod(c.StateDir, 0700)
	c.Server.Host = strings.Trim(strings.TrimSpace(c.Server.Host), "[]")
	if c.Server.Host == "" {
		c.Server.Host = "127.0.0.1"
	}
	if !isLoopback(c.Server.Host) {
		return fmt.Errorf("server host must be loopback")
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8766
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port")
	}
	if c.Server.PublicBaseURL == "" {
		c.Server.PublicBaseURL = "http://" + net.JoinHostPort(c.Server.Host, fmt.Sprint(c.Server.Port))
	}
	u, e := validateURL(c.Server.PublicBaseURL)
	if e != nil {
		return e
	}
	c.Server.PublicBaseURL = strings.TrimRight(u.String(), "/")
	allowed := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true, strings.ToLower(c.Server.Host): true, strings.ToLower(u.Hostname()): true}
	for _, h := range c.Server.AllowedHosts {
		h = strings.ToLower(strings.Trim(h, "[] "))
		if h != "" {
			allowed[h] = true
		}
	}
	c.Server.AllowedHosts = nil
	for h := range allowed {
		c.Server.AllowedHosts = append(c.Server.AllowedHosts, h)
	}
	sort.Strings(c.Server.AllowedHosts)
	if c.Server.OAuth.OwnerTokenFile == "" {
		c.Server.OAuth.OwnerTokenFile = filepath.Join(c.StateDir, "oauth-owner.token")
	}
	c.Server.OAuth.OwnerTokenFile, e = ExpandHome(c.Server.OAuth.OwnerTokenFile)
	if e != nil {
		return e
	}
	c.Server.OAuth.OwnerTokenFile, e = filepath.Abs(filepath.Clean(c.Server.OAuth.OwnerTokenFile))
	if e != nil {
		return e
	}
	if e = validateTokenFile(c.Server.OAuth.OwnerTokenFile); e != nil {
		return e
	}
	if c.Server.OAuth.AccessTokenTTLSeconds == 0 {
		c.Server.OAuth.AccessTokenTTLSeconds = 3600
	}
	if c.Server.OAuth.RefreshTokenTTLSeconds == 0 {
		c.Server.OAuth.RefreshTokenTTLSeconds = 2592000
	}
	if c.Server.OAuth.AccessTokenTTLSeconds < 60 || c.Server.OAuth.AccessTokenTTLSeconds > 86400 || c.Server.OAuth.RefreshTokenTTLSeconds < c.Server.OAuth.AccessTokenTTLSeconds || c.Server.OAuth.RefreshTokenTTLSeconds > 31536000 {
		return fmt.Errorf("invalid OAuth TTL")
	}
	if len(c.Server.OAuth.Scopes) == 0 {
		c.Server.OAuth.Scopes = []string{"spacedock"}
	}
	c.Server.OAuth.Scopes, e = norm(c.Server.OAuth.Scopes, 16)
	if e != nil {
		return e
	}
	for i := range c.Server.OAuth.AllowedResourceURLs {
		v, er := validateURL(c.Server.OAuth.AllowedResourceURLs[i])
		if er != nil {
			return er
		}
		c.Server.OAuth.AllowedResourceURLs[i] = strings.TrimRight(v.String(), "/")
	}
	if len(c.Server.OAuth.AllowedRedirectHosts) == 0 {
		c.Server.OAuth.AllowedRedirectHosts = []string{"chatgpt.com", "localhost", "127.0.0.1", "::1"}
	}
	c.Server.OAuth.AllowedRedirectHosts, e = norm(c.Server.OAuth.AllowedRedirectHosts, 64)
	if e != nil {
		return e
	}
	if c.Worktree.Root == "" {
		c.Worktree.Root = filepath.Join(c.StateDir, "worktrees")
	}
	c.Worktree.Root, e = ExpandHome(c.Worktree.Root)
	if e != nil {
		return e
	}
	c.Worktree.Root, e = filepath.Abs(filepath.Clean(c.Worktree.Root))
	if e != nil {
		return e
	}
	if filepath.Dir(c.Worktree.Root) == c.Worktree.Root {
		return fmt.Errorf("filesystem root is not allowed")
	}
	if e = os.MkdirAll(c.Worktree.Root, 0700); e != nil {
		return e
	}
	_ = os.Chmod(c.Worktree.Root, 0700)
	if c.Agents.MaxConcurrent == 0 {
		c.Agents.MaxConcurrent = 4
	}
	if c.Agents.MaxConcurrent < 1 || c.Agents.MaxConcurrent > 16 {
		return fmt.Errorf("invalid agent concurrency")
	}
	c.Agents.Codex.Command = strings.TrimSpace(c.Agents.Codex.Command)
	if c.Agents.Codex.Command == "" {
		c.Agents.Codex.Command = "codex"
	}
	c.Agents.Profiles, e = NormalizeAgentProfiles(c.Agents.Profiles, c.ACP.Endpoints)
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	seenPaths := map[string]bool{}
	for i := range c.AllowedRoots {
		r := &c.AllowedRoots[i]
		if !idRE.MatchString(r.ID) || seen[r.ID] {
			return fmt.Errorf("invalid or duplicate root id")
		}
		seen[r.ID] = true
		if r.Name == "" {
			r.Name = r.ID
		}
		r.Path, e = ExpandHome(r.Path)
		if e != nil {
			return e
		}
		r.Path, e = filepath.Abs(filepath.Clean(r.Path))
		if e != nil {
			return e
		}
		r.Path, e = filepath.EvalSymlinks(r.Path)
		if e != nil {
			return e
		}
		if seenPaths[r.Path] {
			return fmt.Errorf("duplicate root path")
		}
		seenPaths[r.Path] = true
		st, er := os.Stat(r.Path)
		if er != nil || !st.IsDir() || filepath.Dir(r.Path) == r.Path {
			return fmt.Errorf("invalid root path")
		}
		out := []string{}
		for _, v := range r.Permissions {
			p, er := policy.ParsePermission(v)
			if er != nil {
				return er
			}
			if !contains(out, string(p)) {
				out = append(out, string(p))
			}
		}
		r.Permissions = out
	}
	seen = map[string]bool{}
	for i := range c.ACP.Endpoints {
		ep := &c.ACP.Endpoints[i]
		if !idRE.MatchString(ep.ID) || seen[ep.ID] {
			return fmt.Errorf("invalid or duplicate endpoint id")
		}
		seen[ep.ID] = true
		if ep.Name == "" {
			ep.Name = ep.ID
		}
		ep.Builtin = strings.ToLower(strings.TrimSpace(ep.Builtin))
		ep.Command = strings.TrimSpace(ep.Command)
		switch ep.Builtin {
		case "":
			if !filepath.IsAbs(ep.Command) {
				return fmt.Errorf("ACP command must be absolute")
			}
			if !executableFile(ep.Command) {
				return fmt.Errorf("ACP command is not executable file")
			}
		case "antigravity":
			if err := normalizeAntigravityEndpoint(ep); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown ACP builtin %q", ep.Builtin)
		}
		for k, v := range ep.EnvFrom {
			if !envRE.MatchString(k) || !envRE.MatchString(v) {
				return fmt.Errorf("invalid env_from")
			}
		}
	}
	return nil
}
func norm(in []string, max int) ([]string, error) {
	if len(in) < 1 || len(in) > max {
		return nil, fmt.Errorf("invalid values")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || strings.ContainsAny(v, " \t\r\n") {
			return nil, fmt.Errorf("invalid value")
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out, nil
}
func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
