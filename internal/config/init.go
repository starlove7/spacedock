package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type InitOptions struct {
	ConfigPath, RootPath, RootID, RootName string
	PublicBaseURL                          string
	Force                                  bool
}
type InitResult struct {
	ConfigPath string
	TokenPath  string
	RootPath   string
}

func Init(o InitOptions) (InitResult, error) {
	var e error
	if o.ConfigPath == "" {
		o.ConfigPath, e = DefaultConfigPath()
		if e != nil {
			return InitResult{}, e
		}
	}
	o.ConfigPath, e = ExpandHome(o.ConfigPath)
	if e != nil {
		return InitResult{}, e
	}
	o.ConfigPath, e = filepath.Abs(filepath.Clean(o.ConfigPath))
	if e != nil {
		return InitResult{}, e
	}
	if o.RootPath == "" {
		o.RootPath, e = os.Getwd()
		if e != nil {
			return InitResult{}, e
		}
	}
	o.RootPath, e = filepath.Abs(filepath.Clean(o.RootPath))
	if e != nil {
		return InitResult{}, e
	}
	o.RootPath, e = filepath.EvalSymlinks(o.RootPath)
	if e != nil {
		return InitResult{}, e
	}
	st, e := os.Stat(o.RootPath)
	if e != nil || !st.IsDir() || filepath.Dir(o.RootPath) == o.RootPath {
		return InitResult{}, fmt.Errorf("invalid root path")
	}
	if o.RootID == "" {
		b := strings.Trim(regexp.MustCompile(`[^a-z0-9._-]+`).ReplaceAllString(strings.ToLower(filepath.Base(o.RootPath)), "-"), "-")
		if b == "" {
			b = "workspace"
		}
		o.RootID = b
	}
	if !idRE.MatchString(o.RootID) {
		return InitResult{}, fmt.Errorf("invalid root id")
	}
	if o.RootName == "" {
		o.RootName = filepath.Base(o.RootPath)
	}
	if _, e = os.Stat(o.ConfigPath); e == nil && !o.Force {
		return InitResult{}, fmt.Errorf("config already exists")
	}
	if e != nil && !os.IsNotExist(e) {
		return InitResult{}, e
	}
	state := filepath.Dir(o.ConfigPath)
	if e = os.MkdirAll(state, 0700); e != nil {
		return InitResult{}, e
	}
	_ = os.Chmod(state, 0700)
	token := filepath.Join(state, "oauth-owner.token")
	if _, e = os.Stat(token); os.IsNotExist(e) {
		raw := make([]byte, 32)
		if _, e = rand.Read(raw); e != nil {
			return InitResult{}, e
		}
		if e = os.WriteFile(token, []byte(hex.EncodeToString(raw)+"\n"), 0600); e != nil {
			return InitResult{}, e
		}
	} else if e != nil {
		return InitResult{}, e
	}
	if e = validateTokenFile(token); e != nil {
		return InitResult{}, e
	}
	c := Config{StateDir: state, Server: ServerConfig{Host: "127.0.0.1", Port: 8766, PublicBaseURL: o.PublicBaseURL}, Worktree: WorktreeConfig{Root: filepath.Join(state, "worktrees")}, AllowedRoots: []RootConfig{{ID: o.RootID, Name: o.RootName, Path: o.RootPath, Permissions: []string{"fs.read", "fs.write", "command.execute", "git.read", "workspace.manage", "recall.read", "recall.write", "acp.connect", "agent.execute"}}}}
	if e = c.NormalizeAndValidate(); e != nil {
		return InitResult{}, e
	}
	b, e := yaml.Marshal(c)
	if e != nil {
		return InitResult{}, e
	}
	f, e := os.CreateTemp(state, ".spacedock-config-")
	if e != nil {
		return InitResult{}, e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_ = f.Chmod(0600)
	if _, e = f.Write(b); e == nil {
		e = f.Close()
	} else {
		_ = f.Close()
	}
	if e == nil {
		e = os.Rename(tmp, o.ConfigPath)
	}
	if e != nil {
		return InitResult{}, e
	}
	return InitResult{o.ConfigPath, token, o.RootPath}, nil
}
