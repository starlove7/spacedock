package config

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var rootSlugRE = regexp.MustCompile(`[^a-z0-9._-]+`)

var rootDefaultPermissions = [...]string{
	"fs.read",
	"fs.write",
	"command.execute",
	"git.read",
	"workspace.manage",
	"recall.read",
	"recall.write",
	"acp.connect",
	"agent.execute",
}

func rootPermissions() []string {
	return append([]string(nil), rootDefaultPermissions[:]...)
}

func rootIDFromPath(path string) string {
	b := strings.Trim(rootSlugRE.ReplaceAllString(strings.ToLower(filepath.Base(path)), "-"), "-")
	if b == "" {
		return "workspace"
	}
	return b
}

type RootAddOptions struct {
	ConfigPath string
	Path       string
	ID         string
	Name       string
}

type RootRemoveOptions struct {
	ConfigPath string
	ID         string
}

func AddRoot(o RootAddOptions) (RootConfig, error) {
	if o.Path == "" {
		return RootConfig{}, fmt.Errorf("root path is required")
	}

	configPath, e := rootConfigPath(o.ConfigPath)
	if e != nil {
		return RootConfig{}, e
	}
	c, e := Load(configPath)
	if e != nil {
		return RootConfig{}, e
	}

	rootPath, e := canonicalRootPath(o.Path)
	if e != nil {
		return RootConfig{}, e
	}
	rootID := o.ID
	if rootID == "" {
		rootID = rootIDFromPath(rootPath)
	}
	rootName := o.Name
	if rootName == "" {
		rootName = filepath.Base(rootPath)
	}
	c.AllowedRoots = append(c.AllowedRoots, RootConfig{
		ID:          rootID,
		Name:        rootName,
		Path:        rootPath,
		Permissions: rootPermissions(),
	})
	if e = c.NormalizeAndValidate(); e != nil {
		return RootConfig{}, e
	}
	if e = saveAtomic(configPath, c); e != nil {
		return RootConfig{}, e
	}
	return c.AllowedRoots[len(c.AllowedRoots)-1], nil
}

func ListRoots(configPath string) ([]RootConfig, error) {
	configPath, e := rootConfigPath(configPath)
	if e != nil {
		return nil, e
	}
	c, e := Load(configPath)
	if e != nil {
		return nil, e
	}
	return c.AllowedRoots, nil
}

func RemoveRoot(o RootRemoveOptions) error {
	if o.ID == "" {
		return fmt.Errorf("root id is required")
	}
	configPath, e := rootConfigPath(o.ConfigPath)
	if e != nil {
		return e
	}
	c, e := Load(configPath)
	if e != nil {
		return e
	}
	index := -1
	for i, root := range c.AllowedRoots {
		if root.ID == o.ID {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("root not found: %s", o.ID)
	}
	c.AllowedRoots = append(c.AllowedRoots[:index], c.AllowedRoots[index+1:]...)
	if e = c.NormalizeAndValidate(); e != nil {
		return e
	}
	return saveAtomic(configPath, c)
}

func rootConfigPath(path string) (string, error) {
	if path == "" {
		var e error
		path, e = DefaultConfigPath()
		if e != nil {
			return "", e
		}
	}
	var e error
	path, e = ExpandHome(path)
	if e != nil {
		return "", e
	}
	return filepath.Abs(filepath.Clean(path))
}

func canonicalRootPath(path string) (string, error) {
	var e error
	path, e = ExpandHome(path)
	if e != nil {
		return "", e
	}
	path, e = filepath.Abs(filepath.Clean(path))
	if e != nil {
		return "", e
	}
	return filepath.EvalSymlinks(path)
}

func saveAtomic(path string, c Config) (e error) {
	b, e := yaml.Marshal(c)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".spacedock-config-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
		_ = os.Remove(tmp)
	}()
	if e = f.Chmod(0600); e != nil {
		return e
	}
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		closed = true
		return e
	}
	closed = true
	return os.Rename(tmp, path)
}
