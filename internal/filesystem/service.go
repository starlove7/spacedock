package filesystem

import (
	"fmt"
	"github.com/starlove7/spacedock/internal/workspace"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

type Service struct{}

func NewService() *Service { return &Service{} }

type ReadResult struct {
	Path          string `json:"path"`
	Content       string `json:"content"`
	SizeBytes     int64  `json:"size_bytes"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	TotalLines    int    `json:"total_lines"`
	Truncated     bool   `json:"truncated"`
	NextStartLine int    `json:"next_start_line,omitempty"`
}
type Entry struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Type      string    `json:"type"`
	SizeBytes int64     `json:"size_bytes"`
	Modified  time.Time `json:"modified"`
}
type ListResult struct {
	Path      string  `json:"path"`
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated"`
}
type Match struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Text   string `json:"text"`
}
type SearchResult struct {
	Matches      []Match `json:"matches"`
	Truncated    bool    `json:"truncated"`
	FilesScanned int     `json:"files_scanned"`
	FilesSkipped int     `json:"files_skipped"`
}
type EditRequest struct {
	Action          string
	Path            string
	Content         string
	OldText         string
	NewText         string
	ExpectedMatches int
	ReplaceAll      bool
	NewPath         string
	Overwrite       bool
}
type EditResult struct {
	Action    string `json:"action"`
	Path      string `json:"path"`
	NewPath   string `json:"new_path,omitempty"`
	Changed   bool   `json:"changed"`
	Matches   int    `json:"matches,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
}

func clamp(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
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
func lexical(w *workspace.Workspace, rel string) (string, error) {
	p, e := w.Resolver.ValidateRelative(rel)
	if e != nil {
		return "", e
	}
	return filepath.Join(w.Resolver.Root(), p), nil
}
func finalSymlink(w *workspace.Workspace, rel string) (bool, error) {
	p, e := lexical(w, rel)
	if e != nil {
		return false, e
	}
	i, e := os.Lstat(p)
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	return i.Mode()&os.ModeSymlink != 0, nil
}
func readRegular(p string, max int64) ([]byte, os.FileInfo, error) {
	st, e := os.Stat(p)
	if e != nil || !st.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("not a regular file")
	}
	if st.Size() > max {
		return nil, nil, fmt.Errorf("file too large")
	}
	b, e := os.ReadFile(p)
	return b, st, e
}

func (s *Service) ReadFile(w *workspace.Workspace, p string, start, maxLines, maxBytes int) (ReadResult, error) {
	x, e := w.Resolver.ResolveExisting(p)
	if e != nil {
		return ReadResult{}, e
	}
	b, st, e := readRegular(x, 8<<20)
	if e != nil || !utf8.Valid(b) || bytesNUL(b) {
		return ReadResult{}, fmt.Errorf("invalid text file")
	}
	start = clamp(start, 1, 5000)
	maxLines = clamp(maxLines, 400, 5000)
	maxBytes = clamp(maxBytes, 262144, 4<<20)
	lines := strings.Split(string(b), "\n")
	if start > len(lines) {
		start = len(lines) + 1
	}
	end := start + maxLines - 1
	if end > len(lines) {
		end = len(lines)
	}
	content := ""
	if start <= len(lines) {
		content = strings.Join(lines[start-1:end], "\n")
	}
	truncated := len(content) > maxBytes
	content = safePrefix(content, maxBytes)
	if truncated {
		represented := strings.Count(content, "\n")
		if !strings.HasSuffix(content, "\n") {
			represented++
		}
		end = start + represented - 1
	}
	r := ReadResult{p, content, st.Size(), start, end, len(lines), truncated, 0}
	if r.Truncated {
		r.NextStartLine = end + 1
	}
	return r, nil
}
func bytesNUL(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

func (s *Service) ListDir(w *workspace.Workspace, p string, max int) (ListResult, error) {
	if p == "" {
		p = "."
	}
	x, e := w.Resolver.ResolveExisting(p)
	if e != nil {
		return ListResult{}, e
	}
	ents, e := os.ReadDir(x)
	if e != nil {
		return ListResult{}, e
	}
	max = clamp(max, 200, 5000)
	r := ListResult{Path: p}
	for i, ent := range ents {
		if i >= max {
			r.Truncated = true
			break
		}
		info, _ := ent.Info()
		typ := "file"
		if ent.Type()&os.ModeSymlink != 0 {
			typ = "symlink"
		} else if ent.IsDir() {
			typ = "directory"
		}
		rel, _ := filepath.Rel(w.Root, filepath.Join(x, ent.Name()))
		size := int64(0)
		mod := time.Time{}
		if info != nil {
			size = info.Size()
			mod = info.ModTime()
		}
		r.Entries = append(r.Entries, Entry{ent.Name(), filepath.ToSlash(rel), typ, size, mod})
	}
	return r, nil
}

var skippedDirs = map[string]bool{".git": true, ".hg": true, ".svn": true, "node_modules": true, "vendor": true, "dist": true, "build": true, ".cache": true}

func walkFiles(base string, currentDepth, maxDepth int, fn func(string, os.FileInfo) error) error {
	ents, e := os.ReadDir(base)
	if e != nil {
		return e
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	for _, ent := range ents {
		p := filepath.Join(base, ent.Name())
		li, e := os.Lstat(p)
		if e != nil {
			continue
		}
		if li.IsDir() {
			if skippedDirs[ent.Name()] {
				continue
			}
			if maxDepth > 0 && currentDepth >= maxDepth {
				continue
			}
			if e = walkFiles(p, currentDepth+1, maxDepth, fn); e != nil {
				return e
			}
			continue
		}
		if li.Mode()&os.ModeSymlink != 0 {
			if target, se := os.Stat(p); se == nil && target.IsDir() {
				continue
			}
		}
		if e = fn(p, li); e != nil {
			if e == filepath.SkipAll {
				return e
			}
			return e
		}
	}
	return nil
}
func (s *Service) ListFiles(w *workspace.Workspace, p, pat string, depth, max int) (ListResult, error) {
	if p == "" {
		p = "."
	}
	if pat == "" {
		pat = "*"
	}
	base, e := w.Resolver.ResolveExisting(p)
	if e != nil {
		return ListResult{}, e
	}
	depth = clamp(depth, 8, 32)
	max = clamp(max, 500, 5000)
	r := ListResult{Path: p}
	e = walkFiles(base, 0, depth, func(x string, info os.FileInfo) error {
		rel, _ := filepath.Rel(base, x)
		if strings.Count(filepath.ToSlash(rel), "/")+1 > depth {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			rp, _ := filepath.Rel(w.Root, x)
			resolved, e := w.Resolver.ResolveExisting(filepath.ToSlash(rp))
			if e != nil {
				return nil
			}
			targetInfo, e := os.Stat(resolved)
			if e != nil || !targetInfo.Mode().IsRegular() {
				return nil
			}
			info = targetInfo
		}
		ok, me := path.Match(pat, filepath.Base(x))
		if me != nil {
			return me
		}
		if ok {
			rp, _ := filepath.Rel(w.Root, x)
			r.Entries = append(r.Entries, Entry{filepath.Base(x), filepath.ToSlash(rp), "file", info.Size(), info.ModTime()})
		}
		return nil
	})
	if e != nil {
		return ListResult{}, e
	}
	sort.Slice(r.Entries, func(i, j int) bool { return r.Entries[i].Path < r.Entries[j].Path })
	if len(r.Entries) > max {
		r.Entries = r.Entries[:max]
		r.Truncated = true
	}
	return r, nil
}
func (s *Service) SearchText(w *workspace.Workspace, p, q string, rx, cs bool, max int) (SearchResult, error) {
	if p == "" {
		p = "."
	}
	if q == "" {
		return SearchResult{}, fmt.Errorf("query required")
	}
	base, e := w.Resolver.ResolveExisting(p)
	if e != nil {
		return SearchResult{}, e
	}
	var re *regexp.Regexp
	if rx {
		qq := q
		if !cs {
			qq = "(?i)" + qq
		}
		re, e = regexp.Compile(qq)
		if e != nil {
			return SearchResult{}, e
		}
	}
	max = clamp(max, 100, 1000)
	r := SearchResult{}
	e = walkFiles(base, 0, 0, func(x string, info os.FileInfo) error {
		r.FilesScanned++
		rel, _ := filepath.Rel(w.Root, x)
		if info.Mode()&os.ModeSymlink != 0 {
			var er error
			x, er = w.Resolver.ResolveExisting(filepath.ToSlash(rel))
			if er != nil {
				r.FilesSkipped++
				return nil
			}
			info, er = os.Stat(x)
			if er != nil || !info.Mode().IsRegular() {
				r.FilesSkipped++
				return nil
			}
		}
		if info.Size() > 2<<20 {
			r.FilesSkipped++
			return nil
		}
		b, er := os.ReadFile(x)
		if er != nil || !utf8.Valid(b) || bytesNUL(b) {
			r.FilesSkipped++
			return nil
		}
		for i, line := range strings.Split(string(b), "\n") {
			col := 0
			match := false
			if re != nil {
				loc := re.FindStringIndex(line)
				if loc != nil {
					match = true
					col = loc[0] + 1
				}
			} else {
				needle := q
				line2 := line
				if !cs {
					needle = strings.ToLower(needle)
					line2 = strings.ToLower(line)
				}
				col = strings.Index(line2, needle) + 1
				match = col > 0
			}
			if match {
				r.Matches = append(r.Matches, Match{filepath.ToSlash(rel), i + 1, col, line})
				if len(r.Matches) >= max {
					r.Truncated = true
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if e != nil && e != filepath.SkipAll {
		return SearchResult{}, e
	}
	return r, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".spacedock-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
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
func (s *Service) Edit(w *workspace.Workspace, req EditRequest) (EditResult, error) {
	switch req.Action {
	case "write":
		sy, e := finalSymlink(w, req.Path)
		if e != nil {
			return EditResult{}, e
		}
		if sy {
			return EditResult{}, fmt.Errorf("final symlink rejected")
		}
		p, e := w.Resolver.ResolveForWrite(req.Path)
		if e != nil {
			return EditResult{}, e
		}
		st, e := os.Lstat(p)
		if e != nil && !os.IsNotExist(e) {
			return EditResult{}, e
		}
		if e == nil && !req.Overwrite {
			return EditResult{}, fmt.Errorf("destination exists")
		}
		if e == nil && !st.Mode().IsRegular() {
			return EditResult{}, fmt.Errorf("not regular")
		}
		if e = os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			return EditResult{}, e
		}
		mode := os.FileMode(0644)
		if e == nil && st != nil {
			mode = st.Mode().Perm()
		}
		if e = atomicWrite(p, []byte(req.Content), mode); e != nil {
			return EditResult{}, e
		}
		return EditResult{"write", req.Path, "", true, 0, int64(len(req.Content))}, nil
	case "replace":
		sy, e := finalSymlink(w, req.Path)
		if e != nil {
			return EditResult{}, e
		}
		if sy {
			return EditResult{}, fmt.Errorf("final symlink rejected")
		}
		p, e := w.Resolver.ResolveExisting(req.Path)
		if e != nil {
			return EditResult{}, e
		}
		b, st, e := readRegular(p, 8<<20)
		if e != nil || !utf8.Valid(b) {
			return EditResult{}, fmt.Errorf("invalid file")
		}
		if req.OldText == "" {
			return EditResult{}, fmt.Errorf("old text required")
		}
		n := strings.Count(string(b), req.OldText)
		if n != req.ExpectedMatches {
			return EditResult{}, fmt.Errorf("expected %d matches, got %d", req.ExpectedMatches, n)
		}
		out := string(b)
		if req.ReplaceAll {
			out = strings.ReplaceAll(out, req.OldText, req.NewText)
		} else {
			out = strings.Replace(out, req.OldText, req.NewText, 1)
		}
		if e = atomicWrite(p, []byte(out), st.Mode().Perm()); e != nil {
			return EditResult{}, e
		}
		return EditResult{"replace", req.Path, "", out != string(b), n, int64(len(out))}, nil
	case "delete":
		sy, e := finalSymlink(w, req.Path)
		if e != nil {
			return EditResult{}, e
		}
		if sy {
			return EditResult{}, fmt.Errorf("final symlink rejected")
		}
		p, e := w.Resolver.ResolveExisting(req.Path)
		if e != nil {
			return EditResult{}, e
		}
		st, e := os.Stat(p)
		if e != nil || !st.Mode().IsRegular() {
			return EditResult{}, fmt.Errorf("not regular")
		}
		if e = os.Remove(p); e != nil {
			return EditResult{}, e
		}
		return EditResult{"delete", req.Path, "", true, 0, 0}, nil
	case "move":
		if req.NewPath == "" {
			return EditResult{}, fmt.Errorf("new path required")
		}
		sy, e := finalSymlink(w, req.Path)
		if e != nil {
			return EditResult{}, e
		}
		if sy {
			return EditResult{}, fmt.Errorf("source symlink rejected")
		}
		sy, e = finalSymlink(w, req.NewPath)
		if e != nil {
			return EditResult{}, e
		}
		if sy {
			return EditResult{}, fmt.Errorf("destination symlink rejected")
		}
		src, e := w.Resolver.ResolveExisting(req.Path)
		if e != nil {
			return EditResult{}, e
		}
		sst, e := os.Stat(src)
		if e != nil || !sst.Mode().IsRegular() {
			return EditResult{}, fmt.Errorf("source not regular")
		}
		dst, e := w.Resolver.ResolveForWrite(req.NewPath)
		if e != nil {
			return EditResult{}, e
		}
		if d, e2 := os.Lstat(dst); e2 == nil && !req.Overwrite {
			return EditResult{}, fmt.Errorf("destination exists")
		} else if e2 == nil && !d.Mode().IsRegular() {
			return EditResult{}, fmt.Errorf("destination not regular")
		} else if e2 != nil && !os.IsNotExist(e2) {
			return EditResult{}, e2
		}
		if e = os.MkdirAll(filepath.Dir(dst), 0755); e != nil {
			return EditResult{}, e
		}
		e = os.Rename(src, dst)
		if e != nil {
			if le, ok := e.(*os.LinkError); !ok || le.Err != syscall.EXDEV {
				return EditResult{}, e
			}
			if e = copyAtomic(src, dst, sst.Mode().Perm()); e != nil {
				return EditResult{}, e
			}
			if e = os.Remove(src); e != nil {
				return EditResult{}, e
			}
		}
		return EditResult{"move", req.Path, req.NewPath, true, 0, 0}, nil
	default:
		return EditResult{}, fmt.Errorf("invalid edit action")
	}
}
func copyAtomic(src, dst string, mode os.FileMode) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	f, e := os.CreateTemp(filepath.Dir(dst), ".spacedock-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		_, e = io.Copy(f, in)
	}
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(tmp, dst)
	}
	return e
}
