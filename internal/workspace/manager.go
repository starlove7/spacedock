package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/starlove7/spacedock/internal/config"
	"github.com/starlove7/spacedock/internal/policy"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Workspace struct {
	ID            string              `json:"id"`
	RootID        string              `json:"root_id"`
	RootName      string              `json:"root_name"`
	ProjectPath   string              `json:"project_path"`
	Root          string              `json:"root"`
	Mode          string              `json:"mode"`
	SourceRoot    string              `json:"source_root,omitempty"`
	BaseRef       string              `json:"base_ref,omitempty"`
	BaseSHA       string              `json:"base_sha,omitempty"`
	Managed       bool                `json:"managed"`
	SourceDirty   bool                `json:"source_dirty,omitempty"`
	Permissions   []policy.Permission `json:"permissions"`
	Resolver      *policy.Resolver    `json:"-"`
	Instructions  []Instruction       `json:"instructions"`
	RecallContext string              `json:"recall_context"`
	OpenedAt      time.Time           `json:"opened_at"`
}
type OpenOptions struct{ RootID, Path, Mode, BaseRef string }
type RecallLoader interface {
	Context(string, int) (string, error)
}
type CloseHook interface{ CloseWorkspace(string) }
type RootSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Permissions []string `json:"permissions"`
}
type Manager struct {
	cfg    config.Config
	recall RecallLoader
	store  *Store
	mu     sync.RWMutex
	ws     map[string]*Workspace
	hooks  []CloseHook
}

func NewManager(c config.Config, r RecallLoader) (*Manager, error) {
	s, e := NewStore(filepath.Join(c.StateDir, "workspaces.json"))
	if e != nil {
		return nil, e
	}
	m := &Manager{cfg: c, recall: r, store: s, ws: map[string]*Workspace{}}
	for _, v := range s.List() {
		w, e := m.restore(v)
		if e != nil {
			return nil, fmt.Errorf("unsafe/stale persisted workspace %s: %w", v.ID, e)
		}
		m.ws[w.ID] = w
	}
	return m, nil
}
func (m *Manager) AddCloseHook(h CloseHook) { m.mu.Lock(); m.hooks = append(m.hooks, h); m.mu.Unlock() }
func (m *Manager) ListRoots() []RootSummary {
	out := []RootSummary{}
	for _, r := range m.cfg.AllowedRoots {
		out = append(out, RootSummary{r.ID, r.Name, r.Path, r.Permissions})
	}
	return out
}
func (m *Manager) List() []Workspace {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Workspace, 0, len(m.ws))
	for _, w := range m.ws {
		x := *w
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func newID() (string, error) {
	b := make([]byte, 10)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return "ws_" + hex.EncodeToString(b), nil
}
func (m *Manager) permissions(r config.RootConfig) ([]policy.Permission, error) {
	out := []policy.Permission{}
	for _, x := range r.Permissions {
		p, e := policy.ParsePermission(x)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	if !policy.ContainsPermission(out, policy.PermissionWorkspaceManage) {
		return nil, fmt.Errorf("workspace.manage permission required")
	}
	return out, nil
}
func (m *Manager) Open(o OpenOptions) (*Workspace, error) {
	if o.Mode == "" {
		o.Mode = "checkout"
	}
	if o.Mode != "checkout" && o.Mode != "worktree" {
		return nil, fmt.Errorf("unsupported workspace mode")
	}
	if o.Mode == "checkout" && o.BaseRef != "" {
		return nil, fmt.Errorf("base_ref only valid for worktree")
	}
	r, ok := m.cfg.RootByID(o.RootID)
	if !ok {
		return nil, fmt.Errorf("unknown root")
	}
	ps, e := m.permissions(r)
	if e != nil {
		return nil, e
	}
	rr, e := policy.NewResolver(r.Path)
	if e != nil {
		return nil, e
	}
	p, e := rr.ResolveExisting(func() string {
		if o.Path == "" {
			return "."
		}
		return o.Path
	}())
	if e != nil {
		return nil, e
	}
	if st, e := os.Stat(p); e != nil || !st.IsDir() {
		return nil, fmt.Errorf("project is not directory")
	}
	rel, _ := filepath.Rel(r.Path, p)
	id, e := newID()
	if e != nil {
		return nil, e
	}
	w := &Workspace{ID: id, RootID: r.ID, RootName: r.Name, ProjectPath: filepath.ToSlash(rel), Root: p, Mode: o.Mode, Permissions: ps, OpenedAt: time.Now()}
	var cleanup func()
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()
	if o.Mode == "worktree" {
		x, e := CreateWorktree(p, o.BaseRef, m.cfg.Worktree.Root, id)
		if e != nil {
			return nil, e
		}
		w.Root = x.Path
		w.SourceRoot = x.SourceRoot
		w.BaseRef = x.BaseRef
		w.BaseSHA = x.BaseSHA
		w.Managed = true
		w.SourceDirty = x.SourceDirty
		cleanup = func() {
			realRoot, er := filepath.EvalSymlinks(m.cfg.Worktree.Root)
			if er == nil && policy.IsWithin(realRoot, w.Root) {
				_ = RemoveWorktree(w.SourceRoot, w.Root, true)
			}
		}
	}
	w.Resolver, e = policy.NewResolver(w.Root)
	if e != nil {
		return nil, e
	}
	w.Instructions, e = LoadInstructions(w.Resolver)
	if e != nil {
		return nil, e
	}
	if m.recall != nil {
		w.RecallContext, e = m.recall.Context(w.RootID, 32768)
		if e != nil {
			return nil, e
		}
	}
	if e = m.store.Put(m.stored(w)); e != nil {
		return nil, e
	}
	m.mu.Lock()
	m.ws[id] = w
	m.mu.Unlock()
	cleanup = nil
	return w, nil
}
func (m *Manager) stored(w *Workspace) StoredWorkspace {
	return StoredWorkspace{SchemaVersion: 1, ID: w.ID, RootID: w.RootID, ProjectPath: w.ProjectPath, Mode: w.Mode, Root: w.Root, SourceRoot: w.SourceRoot, BaseRef: w.BaseRef, BaseSHA: w.BaseSHA, Managed: w.Managed, OpenedAt: w.OpenedAt}
}
func (m *Manager) restore(v StoredWorkspace) (*Workspace, error) {
	r, ok := m.cfg.RootByID(v.RootID)
	if !ok {
		return nil, fmt.Errorf("unknown root")
	}
	ps, e := m.permissions(r)
	if e != nil {
		return nil, e
	}
	rr, e := policy.NewResolver(r.Path)
	if e != nil {
		return nil, e
	}
	p, e := rr.ResolveExisting(v.ProjectPath)
	if e != nil {
		return nil, e
	}
	if v.Mode == "checkout" {
		if p != v.Root {
			return nil, fmt.Errorf("checkout root mismatch")
		}
	} else if v.Mode == "worktree" {
		storedSource, er := filepath.EvalSymlinks(v.SourceRoot)
		if er != nil || storedSource != p {
			return nil, fmt.Errorf("worktree source mismatch")
		}
		if e = ValidatePersistedWorktree(p, v.Root, m.cfg.Worktree.Root); e != nil {
			return nil, e
		}
	} else {
		return nil, fmt.Errorf("invalid persisted mode")
	}
	w := &Workspace{ID: v.ID, RootID: v.RootID, RootName: r.Name, ProjectPath: v.ProjectPath, Root: v.Root, Mode: v.Mode, SourceRoot: v.SourceRoot, BaseRef: v.BaseRef, BaseSHA: v.BaseSHA, Managed: v.Managed, Permissions: ps, OpenedAt: v.OpenedAt}
	w.Resolver, e = policy.NewResolver(w.Root)
	if e != nil {
		return nil, e
	}
	w.Instructions, e = LoadInstructions(w.Resolver)
	if e != nil {
		return nil, e
	}
	if m.recall != nil {
		w.RecallContext, e = m.recall.Context(w.RootID, 32768)
	}
	return w, e
}
func (m *Manager) Get(id string) (*Workspace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w, ok := m.ws[id]
	if !ok {
		return nil, fmt.Errorf("unknown workspace")
	}
	return w, nil
}
func (m *Manager) Close(id string) error               { return m.finish(id, false, false) }
func (m *Manager) Discard(id string, force bool) error { return m.finish(id, true, force) }
func (m *Manager) finish(id string, discard, force bool) error {
	m.mu.RLock()
	w, ok := m.ws[id]
	hooks := append([]CloseHook(nil), m.hooks...)
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown workspace")
	}
	if discard && !w.Managed {
		return fmt.Errorf("workspace discard is only valid for managed worktrees")
	}
	if w.Managed {
		dirty, e := WorktreeDirty(w.Root)
		if e != nil {
			return e
		}
		if dirty && !force {
			return fmt.Errorf("WORKTREE_DIRTY")
		}
	}
	for _, h := range hooks {
		h.CloseWorkspace(id)
	}
	if w.Managed {
		if realRoot, err := filepath.EvalSymlinks(m.cfg.Worktree.Root); err != nil || !policy.IsWithin(realRoot, w.Root) {
			return fmt.Errorf("worktree path outside configured root")
		}
		if e := RemoveWorktree(w.SourceRoot, w.Root, force); e != nil {
			return e
		}
	}
	if e := m.store.Delete(id); e != nil {
		return e
	}
	m.mu.Lock()
	delete(m.ws, id)
	m.mu.Unlock()
	return nil
}
func (m *Manager) Shutdown() {
	m.mu.RLock()
	xs := make([]*Workspace, 0, len(m.ws))
	for _, w := range m.ws {
		xs = append(xs, w)
	}
	hooks := append([]CloseHook(nil), m.hooks...)
	m.mu.RUnlock()
	for _, w := range xs {
		for _, h := range hooks {
			h.CloseWorkspace(w.ID)
		}
	}
}
func (m *Manager) CloseAll() { m.Shutdown() }

var _ = strings.TrimSpace
