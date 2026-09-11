package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Resolver struct{ root string }

func NewResolver(root string) (*Resolver, error) {
	c, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	i, err := os.Stat(c)
	if err != nil || !i.IsDir() {
		return nil, fmt.Errorf("root is not a directory")
	}
	return &Resolver{root: c}, nil
}
func (r *Resolver) Root() string { return r.root }
func (r *Resolver) ValidateRelative(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" || strings.IndexByte(rel, 0) >= 0 {
		return "", fmt.Errorf("invalid path")
	}
	if rel == "." {
		return ".", nil
	}
	if strings.HasPrefix(rel, "~") || strings.HasPrefix(rel, "\\\\") || strings.HasPrefix(rel, "//") {
		return "", fmt.Errorf("path must be relative")
	}
	if filepath.IsAbs(filepath.FromSlash(rel)) || regexp.MustCompile(`^[A-Za-z]:`).MatchString(rel) || regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`).MatchString(rel) {
		return "", fmt.Errorf("URI path rejected")
	}
	p := filepath.Clean(filepath.FromSlash(rel))
	if p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes root")
	}
	return p, nil
}
func IsWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func (r *Resolver) ResolveExisting(rel string) (string, error) {
	p, err := r.ValidateRelative(rel)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(r.root, p)
	info, err := os.Lstat(joined)
	if err != nil {
		return "", err
	}
	_ = info
	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", err
	}
	if !IsWithin(r.root, real) {
		return "", fmt.Errorf("path escapes root")
	}
	return real, nil
}
func (r *Resolver) ResolveForWrite(rel string) (string, error) {
	p, err := r.ValidateRelative(rel)
	if err != nil {
		return "", err
	}
	target := filepath.Join(r.root, p)
	if _, e := os.Lstat(target); e == nil {
		real, e := filepath.EvalSymlinks(target)
		if e != nil {
			return "", e
		}
		if !IsWithin(r.root, real) {
			return "", fmt.Errorf("path escapes root")
		}
		return real, nil
	} else if !os.IsNotExist(e) {
		return "", e
	}
	parent := filepath.Dir(target)
	missing := []string{filepath.Base(target)}
	for {
		if info, e := os.Stat(parent); e == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("parent is not a directory")
			}
			break
		} else if !os.IsNotExist(e) {
			return "", e
		}
		missing = append(missing, filepath.Base(parent))
		next := filepath.Dir(parent)
		if next == parent {
			return "", fmt.Errorf("invalid parent")
		}
		parent = next
	}
	real, e := filepath.EvalSymlinks(parent)
	if e != nil {
		return "", e
	}
	if !IsWithin(r.root, real) {
		return "", fmt.Errorf("path escapes root")
	}
	for i := len(missing) - 1; i >= 0; i-- {
		real = filepath.Join(real, missing[i])
	}
	if !IsWithin(r.root, real) {
		return "", fmt.Errorf("path escapes root")
	}
	return real, nil
}
