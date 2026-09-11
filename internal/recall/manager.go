package recall

import (
	"fmt"
	"github.com/starlove7/spacedock/internal/policy"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type SearchHit struct {
	Path        string `json:"path"`
	Line        int    `json:"line"`
	Snippet     string `json:"snippet"`
	Occurrences int    `json:"occurrences"`
}
type Document struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type Manager struct{ root string }

func NewManager(state string) (*Manager, error) {
	p := filepath.Join(state, "recall")
	if e := os.MkdirAll(p, 0700); e != nil {
		return nil, e
	}
	if e := os.Chmod(p, 0700); e != nil {
		return nil, e
	}
	return &Manager{p}, nil
}
func (m *Manager) dir(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, "/\\") || id == "." || id == ".." {
		return "", fmt.Errorf("invalid root id")
	}
	p := filepath.Join(m.root, id)
	if e := os.MkdirAll(p, 0700); e != nil {
		return "", e
	}
	if e := os.Chmod(p, 0700); e != nil {
		return "", e
	}
	return p, nil
}
func (m *Manager) resolver(id string) (*policy.Resolver, error) {
	d, e := m.dir(id)
	if e != nil {
		return nil, e
	}
	r, e := policy.NewResolver(d)
	if e != nil {
		return nil, e
	}
	base, e := filepath.EvalSymlinks(m.root)
	if e != nil || !policy.IsWithin(base, r.Root()) {
		return nil, fmt.Errorf("recall path escapes root")
	}
	return r, nil
}
func safePrefix(s string, n int) string {
	b := []byte(s)
	if len(b) <= n {
		return s
	}
	b = b[:n]
	for !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b)
}
func recallFiles(root string) ([]string, error) {
	var out []string
	var walk func(string) error
	walk = func(d string) error {
		ents, e := os.ReadDir(d)
		if e != nil {
			return e
		}
		sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
		for _, ent := range ents {
			p := filepath.Join(d, ent.Name())
			li, e := os.Lstat(p)
			if e != nil {
				continue
			}
			if li.IsDir() {
				if e = walk(p); e != nil {
					return e
				}
				continue
			}
			if !strings.EqualFold(filepath.Ext(ent.Name()), ".md") {
				continue
			}
			if li.Mode()&os.ModeSymlink != 0 {
				if target, se := os.Stat(p); se == nil && target.IsDir() {
					continue
				}
				out = append(out, p)
			} else if li.Mode().IsRegular() {
				out = append(out, p)
			}
		}
		return nil
	}
	if e := walk(root); e != nil {
		return nil, e
	}
	sort.Slice(out, func(i, j int) bool { return filepath.ToSlash(out[i]) < filepath.ToSlash(out[j]) })
	return out, nil
}
func (m *Manager) Context(id string, max int) (string, error) {
	r, e := m.resolver(id)
	if e != nil {
		return "", e
	}
	d := r.Root()
	files, e := recallFiles(d)
	if e != nil {
		return "", e
	}
	priority := []string{"decisions.md", "runbook.md", "troubleshooting.md", "notes.md"}
	sort.SliceStable(files, func(i, j int) bool {
		ri, rj := 10, 10
		for n, p := range priority {
			if filepath.Dir(files[i]) == d && filepath.Base(files[i]) == p {
				ri = n
			}
			if filepath.Dir(files[j]) == d && filepath.Base(files[j]) == p {
				rj = n
			}
		}
		if ri != rj {
			return ri < rj
		}
		return filepath.ToSlash(files[i]) < filepath.ToSlash(files[j])
	})
	out := ""
	marker := "\n...[recall truncated]"
	for _, p := range files {
		rp, e := filepath.Rel(d, p)
		if e != nil {
			return "", e
		}
		readp := p
		li, e := os.Lstat(p)
		if e != nil {
			return "", e
		}
		if li.Mode()&os.ModeSymlink != 0 {
			rr, er := policy.NewResolver(d)
			if er != nil {
				return "", er
			}
			readp, er = rr.ResolveExisting(filepath.ToSlash(rp))
			if er != nil {
				continue
			}
		}
		b, e := os.ReadFile(readp)
		if e != nil {
			return "", e
		}
		if !utf8.Valid(b) {
			return "", fmt.Errorf("invalid UTF-8 recall document")
		}
		part := "## Recall: " + filepath.ToSlash(rp) + "\n" + string(b)
		if len(out)+len(part) > max {
			remain := max - len(out)
			if remain > 0 {
				if remain < len(marker) {
					out += safePrefix(part, remain)
				} else {
					out += safePrefix(part, remain-len(marker)) + marker
				}
			}
			if len(out) > max {
				out = safePrefix(out, max)
			}
			break
		}
		out += part
	}
	return out, nil
}
func (m *Manager) Read(id, rel string) (Document, error) {
	r, e := m.resolver(id)
	if e != nil {
		return Document{}, e
	}
	p, e := r.ResolveExisting(rel)
	if e != nil {
		return Document{}, e
	}
	if !strings.EqualFold(filepath.Ext(rel), ".md") {
		return Document{}, fmt.Errorf("recall path must be markdown")
	}
	b, e := os.ReadFile(p)
	st, es := os.Stat(p)
	if e != nil || es != nil || !st.Mode().IsRegular() || !utf8.Valid(b) {
		return Document{}, fmt.Errorf("invalid recall document")
	}
	return Document{rel, string(b)}, nil
}
func finalSymlink(r *policy.Resolver, rel string) (bool, error) {
	p, e := r.ValidateRelative(rel)
	if e != nil {
		return false, e
	}
	i, e := os.Lstat(filepath.Join(r.Root(), p))
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	return i.Mode()&os.ModeSymlink != 0, nil
}
func atomic(path string, data []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".spacedock-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(tmp, path)
	}
	return e
}
func (m *Manager) Write(id, rel, mode, content string) (Document, error) {
	if e := policy.ValidateRecallContent(content); e != nil {
		return Document{}, e
	}
	if !utf8.ValidString(content) {
		return Document{}, fmt.Errorf("content is not UTF-8")
	}
	r, e := m.resolver(id)
	if e != nil {
		return Document{}, e
	}
	if !strings.EqualFold(filepath.Ext(rel), ".md") {
		return Document{}, fmt.Errorf("recall path must be markdown")
	}
	sy, e := finalSymlink(r, rel)
	if e != nil {
		return Document{}, e
	}
	if sy {
		return Document{}, fmt.Errorf("final symlink rejected")
	}
	p, e := r.ResolveForWrite(rel)
	if e != nil {
		return Document{}, e
	}
	st, e := os.Stat(p)
	if e != nil && !os.IsNotExist(e) {
		return Document{}, e
	}
	exists := e == nil
	if exists && !st.Mode().IsRegular() {
		return Document{}, fmt.Errorf("invalid recall document")
	}
	var data string
	switch mode {
	case "create":
		if exists {
			return Document{}, fmt.Errorf("document exists")
		}
		data = content
	case "replace":
		if !exists {
			return Document{}, fmt.Errorf("document does not exist")
		}
		data = content
	case "append":
		if exists {
			b, er := os.ReadFile(p)
			if er != nil || !utf8.Valid(b) {
				return Document{}, fmt.Errorf("invalid recall document")
			}
			data = string(b)
			if data != "" && !strings.HasSuffix(data, "\n") {
				data += "\n"
			}
		}
		data += content
	default:
		return Document{}, fmt.Errorf("invalid write mode")
	}
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return Document{}, e
	}
	if e = atomic(p, []byte(data)); e != nil {
		return Document{}, e
	}
	return Document{rel, data}, nil
}
func (m *Manager) Delete(id, rel string) error {
	r, e := m.resolver(id)
	if e != nil {
		return e
	}
	if !strings.EqualFold(filepath.Ext(rel), ".md") {
		return fmt.Errorf("recall path must be markdown")
	}
	sy, e := finalSymlink(r, rel)
	if e != nil {
		return e
	}
	if sy {
		return fmt.Errorf("final symlink rejected")
	}
	p, e := r.ResolveExisting(rel)
	if e != nil {
		return e
	}
	st, e := os.Stat(p)
	if e != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("invalid recall document")
	}
	return os.Remove(p)
}
func (m *Manager) Search(id, q string, max int) ([]SearchHit, error) {
	if q == "" {
		return nil, fmt.Errorf("query required")
	}
	if max <= 0 {
		max = 20
	}
	if max > 100 {
		max = 100
	}
	r, e := m.resolver(id)
	if e != nil {
		return nil, e
	}
	d := r.Root()
	files, e := recallFiles(d)
	if e != nil {
		return nil, e
	}
	var out []SearchHit
	for _, p := range files {
		rp, e := filepath.Rel(d, p)
		if e != nil {
			return nil, e
		}
		readp := p
		li, e := os.Lstat(p)
		if e != nil {
			return nil, e
		}
		if li.Mode()&os.ModeSymlink != 0 {
			rr, er := policy.NewResolver(d)
			if er != nil {
				return nil, er
			}
			readp, e = rr.ResolveExisting(filepath.ToSlash(rp))
			if e != nil {
				continue
			}
		}
		b, e := os.ReadFile(readp)
		if e != nil {
			return nil, e
		}
		if !utf8.Valid(b) {
			return nil, fmt.Errorf("invalid UTF-8 recall document")
		}
		for i, line := range strings.Split(string(b), "\n") {
			n := strings.Count(strings.ToLower(line), strings.ToLower(q))
			if n > 0 {
				out = append(out, SearchHit{filepath.ToSlash(rp), i + 1, safePrefix(strings.TrimSpace(line), 500), n})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Occurrences != out[j].Occurrences {
			return out[i].Occurrences > out[j].Occurrences
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}
