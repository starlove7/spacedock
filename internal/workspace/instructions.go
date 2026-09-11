package workspace

import (
	"fmt"
	"github.com/starlove7/spacedock/internal/policy"
	"os"
	"strings"
	"unicode/utf8"
)

type Instruction struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Priority int    `json:"priority"`
	Content  string `json:"content"`
}

func LoadInstructions(r *policy.Resolver) ([]Instruction, error) {
	out := []Instruction{}
	for _, x := range []struct {
		n string
		p int
	}{{"AGENTS.md", 300}, {"CLAUDE.md", 200}, {"README.md", 100}} {
		p, e := r.ResolveExisting(x.n)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		st, e := os.Stat(p)
		if e != nil {
			return nil, e
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("instruction is not regular")
		}
		if st.Size() > 64*1024 {
			return nil, fmt.Errorf("instruction too large")
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return nil, e
		}
		if !utf8.Valid(b) {
			return nil, fmt.Errorf("instruction is not UTF-8")
		}
		out = append(out, Instruction{Name: x.n, Path: x.n, Priority: x.p, Content: string(b)})
	}
	return out, nil
}

var _ = strings.TrimSpace
