package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type StoredWorkspace struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	RootID        string    `json:"root_id"`
	ProjectPath   string    `json:"project_path"`
	Mode          string    `json:"mode"`
	Root          string    `json:"root"`
	SourceRoot    string    `json:"source_root,omitempty"`
	BaseRef       string    `json:"base_ref,omitempty"`
	BaseSHA       string    `json:"base_sha,omitempty"`
	Managed       bool      `json:"managed"`
	OpenedAt      time.Time `json:"opened_at"`
}
type storeFile struct {
	SchemaVersion int               `json:"schema_version"`
	Workspaces    []StoredWorkspace `json:"workspaces"`
}
type Store struct {
	path  string
	mu    sync.Mutex
	items map[string]StoredWorkspace
}

func NewStore(path string) (*Store, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	_ = os.Chmod(filepath.Dir(path), 0700)
	s := &Store{path: path, items: map[string]StoredWorkspace{}}
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil {
		return nil, e
	}
	if len(b) > 4<<20 {
		return nil, fmt.Errorf("workspace state corrupt")
	}
	var f storeFile
	if e = json.Unmarshal(b, &f); e != nil || f.SchemaVersion != 1 {
		return nil, fmt.Errorf("workspace state corrupt")
	}
	for _, v := range f.Workspaces {
		if v.ID == "" || v.SchemaVersion != 1 {
			return nil, fmt.Errorf("workspace state corrupt")
		}
		if _, ok := s.items[v.ID]; ok {
			return nil, fmt.Errorf("duplicate workspace")
		}
		s.items[v.ID] = v
	}
	return s, nil
}
func (s *Store) List() []StoredWorkspace {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := make([]StoredWorkspace, 0, len(s.items))
	for _, v := range s.items {
		o = append(o, v)
	}
	sort.Slice(o, func(i, j int) bool { return o[i].ID < o[j].ID })
	return o
}
func (s *Store) Put(v StoredWorkspace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v.SchemaVersion = 1
	old, existed := s.items[v.ID]
	s.items[v.ID] = v
	if e := s.writeLocked(); e != nil {
		if existed {
			s.items[v.ID] = old
		} else {
			delete(s.items, v.ID)
		}
		return e
	}
	return nil
}
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, existed := s.items[id]
	delete(s.items, id)
	if e := s.writeLocked(); e != nil {
		if existed {
			s.items[id] = old
		}
		return e
	}
	return nil
}
func (s *Store) writeLocked() error {
	o := make([]StoredWorkspace, 0, len(s.items))
	for _, v := range s.items {
		o = append(o, v)
	}
	sort.Slice(o, func(i, j int) bool { return o[i].ID < o[j].ID })
	b, e := json.Marshal(storeFile{1, o})
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(s.path), ".workspaces-")
	if e != nil {
		return e
	}
	n := f.Name()
	defer os.Remove(n)
	_ = f.Chmod(0600)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(n, s.path)
	}
	return e
}
