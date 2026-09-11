package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesSecureFilesAndPreservesToken(t *testing.T) {
	d := t.TempDir()
	root := filepath.Join(d, "My Workspace!")
	os.Mkdir(root, 0755)
	cp := filepath.Join(d, "cfg", "config.yaml")
	r, err := Init(InitOptions{ConfigPath: cp, RootPath: root})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(r.TokenPath)
	if len(strings.TrimSpace(string(first))) < 24 {
		t.Fatalf("token too short")
	}
	if st, _ := os.Stat(r.TokenPath); st.Mode().Perm() != 0600 {
		t.Errorf("token mode=%o", st.Mode().Perm())
	}
	if st, _ := os.Stat(filepath.Dir(cp)); st.Mode().Perm() != 0700 {
		t.Errorf("state mode=%o", st.Mode().Perm())
	}
	if !strings.Contains(r.RootPath, "My Workspace!") {
		t.Error("root path changed unexpectedly")
	}
	loaded, err := Load(r.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(loaded.AllowedRoots[0].Permissions, "agent.execute") {
		t.Fatalf("init permissions missing agent.execute: %v", loaded.AllowedRoots[0].Permissions)
	}
	if err := os.WriteFile(r.TokenPath, first, 0600); err != nil {
		t.Fatal(err)
	}
	r2, err := Init(InitOptions{ConfigPath: cp, RootPath: root, RootID: "my-root", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(r2.TokenPath)
	if string(first) != string(second) {
		t.Error("force rotated existing token")
	}
	badDir := filepath.Join(d, "bad-state")
	bad := filepath.Join(badDir, "bad.yaml")
	if err := os.MkdirAll(badDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "oauth-owner.token"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(InitOptions{ConfigPath: bad, RootPath: root, Force: true}); err == nil {
		t.Error("invalid existing token accepted")
	}
}

func TestInitRejectsInvalidRootID(t *testing.T) {
	d := t.TempDir()
	root := filepath.Join(d, "root")
	os.Mkdir(root, 0755)
	if _, err := Init(InitOptions{ConfigPath: filepath.Join(d, "c.yaml"), RootPath: root, RootID: "bad/id"}); err == nil {
		t.Error("invalid root id accepted")
	}
}
