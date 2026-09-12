package agent

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/starlove7/spacedock/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	markdownProfileSchema = "spacedock-agent/v1"
	maxMarkdownProfiles   = 128
	maxMarkdownFileSize   = 256 * 1024
)

type markdownProfileFrontmatter struct {
	Schema           string         `yaml:"schema"`
	ID               string         `yaml:"id"`
	Name             string         `yaml:"name"`
	Description      string         `yaml:"description"`
	Provider         string         `yaml:"provider"`
	EndpointID       string         `yaml:"endpoint_id"`
	PermissionPolicy string         `yaml:"permission_policy"`
	ModeID           string         `yaml:"mode_id"`
	ConfigOptions    map[string]any `yaml:"config_options"`
	Model            string         `yaml:"model"`
	Effort           string         `yaml:"effort"`
	WriteMode        string         `yaml:"write_mode"`
}

func parseMarkdownProfile(data []byte, scope, filename string, cfg config.Config) (config.AgentProfileConfig, error) {
	if !utf8.Valid(data) {
		return config.AgentProfileConfig{}, fmt.Errorf("%s/%s: invalid UTF-8", scope, filename)
	}
	normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return config.AgentProfileConfig{}, fmt.Errorf("%s/%s: frontmatter must start with ---", scope, filename)
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return config.AgentProfileConfig{}, fmt.Errorf("%s/%s: missing frontmatter delimiter", scope, filename)
	}

	var frontmatter markdownProfileFrontmatter
	decoder := yaml.NewDecoder(strings.NewReader(strings.Join(lines[1:closing], "\n")))
	decoder.KnownFields(true)
	if err := decoder.Decode(&frontmatter); err != nil {
		return config.AgentProfileConfig{}, fmt.Errorf("%s/%s: %w", scope, filename, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return config.AgentProfileConfig{}, fmt.Errorf("%s/%s: trailing YAML document/content", scope, filename)
		}
		return config.AgentProfileConfig{}, fmt.Errorf("%s/%s: %w", scope, filename, err)
	}
	if frontmatter.Schema != markdownProfileSchema {
		return config.AgentProfileConfig{}, fmt.Errorf("%s/%s: invalid schema", scope, filename)
	}
	if frontmatter.ID == "" || frontmatter.Provider == "" {
		return config.AgentProfileConfig{}, fmt.Errorf("%s/%s: id and provider are required", scope, filename)
	}

	profile := config.AgentProfileConfig{
		ID: frontmatter.ID, Name: frontmatter.Name, Description: frontmatter.Description,
		Provider: frontmatter.Provider, EndpointID: frontmatter.EndpointID,
		PermissionPolicy: frontmatter.PermissionPolicy, ModeID: frontmatter.ModeID,
		ConfigOptions: frontmatter.ConfigOptions, Model: frontmatter.Model,
		Effort: frontmatter.Effort, WriteMode: frontmatter.WriteMode,
		Instructions: strings.TrimSpace(strings.Join(lines[closing+1:], "\n")),
	}
	normalizedProfiles, err := config.NormalizeAgentProfiles([]config.AgentProfileConfig{profile}, cfg.ACP.Endpoints)
	if err != nil {
		return config.AgentProfileConfig{}, fmt.Errorf("%s/%s: %w", scope, filename, err)
	}
	return normalizedProfiles[0], nil
}

func loadMarkdownProfiles(dir, scope string, cfg config.Config) ([]config.AgentProfileConfig, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []config.AgentProfileConfig{}, nil
		}
		return nil, fmt.Errorf("%s: %w", scope, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".md") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) > maxMarkdownProfiles {
		return nil, fmt.Errorf("%s: too many Markdown profiles", scope)
	}
	profiles := make([]config.AgentProfileConfig, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", scope, name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s/%s: not a regular file", scope, name)
		}
		if info.Size() > maxMarkdownFileSize {
			return nil, fmt.Errorf("%s/%s: file too large", scope, name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", scope, name, err)
		}
		if len(data) > maxMarkdownFileSize {
			return nil, fmt.Errorf("%s/%s: file too large", scope, name)
		}
		profile, err := parseMarkdownProfile(data, scope, name, cfg)
		if err != nil {
			return nil, err
		}
		if seen[profile.ID] {
			return nil, fmt.Errorf("%s: duplicate profile id %q", scope, profile.ID)
		}
		seen[profile.ID] = true
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func resolveProfiles(cfg config.Config, workspaceRoot string) ([]config.AgentProfileConfig, error) {
	profiles := append([]config.AgentProfileConfig(nil), cfg.Agents.Profiles...)
	byID := make(map[string]int, len(profiles))
	for i := range profiles {
		byID[profiles[i].ID] = i
	}
	global, err := loadMarkdownProfiles(filepath.Join(cfg.StateDir, "agents"), "global Markdown agents", cfg)
	if err != nil {
		return nil, err
	}
	for _, profile := range global {
		if i, ok := byID[profile.ID]; ok {
			profiles[i] = profile
		} else {
			byID[profile.ID] = len(profiles)
			profiles = append(profiles, profile)
		}
	}
	machineOwnerIDs := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		machineOwnerIDs[profile.ID] = true
	}
	local, err := loadMarkdownProfiles(filepath.Join(workspaceRoot, ".spacedock", "agents"), "local Markdown agents", cfg)
	if err != nil {
		return nil, err
	}
	for _, profile := range local {
		if machineOwnerIDs[profile.ID] {
			return nil, fmt.Errorf("local Markdown agents: profile id %q collides with machine-owner profile", profile.ID)
		}
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	return profiles, nil
}
