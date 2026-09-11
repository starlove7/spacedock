package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

var expectedRootPermissions = []string{
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

func initRootTestConfig(t *testing.T) (string, string) {
	t.Helper()
	d := t.TempDir()
	rootPath := filepath.Join(d, "initial-root")
	if err := os.Mkdir(rootPath, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(d, "state", "config.yaml")
	if _, err := Init(InitOptions{ConfigPath: configPath, RootPath: rootPath, RootID: "initial", RootName: "Initial"}); err != nil {
		t.Fatal(err)
	}
	return configPath, rootPath
}

func configWithoutRoots(c Config) Config {
	c.AllowedRoots = nil
	return c
}

func TestAddRootLoadsAndPreservesConfig(t *testing.T) {
	configPath, _ := initRootTestConfig(t)
	additionalPath := filepath.Join(t.TempDir(), "Second Root!")
	if err := os.Mkdir(additionalPath, 0755); err != nil {
		t.Fatal(err)
	}

	before, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	before.Server.TrustProxy = true
	before.Agents.MaxConcurrent = 7
	before.Security.SensitivePaths.AdditionalPatterns = []string{"*.local"}
	if err := saveAtomic(configPath, before); err != nil {
		t.Fatal(err)
	}
	before, err = Load(configPath)
	if err != nil {
		t.Fatal(err)
	}

	added, err := AddRoot(RootAddOptions{ConfigPath: configPath, Path: additionalPath})
	if err != nil {
		t.Fatal(err)
	}
	if added.ID != "second-root" {
		t.Fatalf("generated ID=%q, want %q", added.ID, "second-root")
	}
	if added.Name != "Second Root!" {
		t.Fatalf("generated name=%q, want %q", added.Name, "Second Root!")
	}
	canonicalPath, err := filepath.EvalSymlinks(additionalPath)
	if err != nil {
		t.Fatal(err)
	}
	if added.Path != canonicalPath {
		t.Fatalf("stored path=%q, want canonical path %q", added.Path, canonicalPath)
	}
	if !reflect.DeepEqual(added.Permissions, expectedRootPermissions) {
		t.Fatalf("permissions=%v, want exactly %v", added.Permissions, expectedRootPermissions)
	}

	after, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.AllowedRoots) != 2 {
		t.Fatalf("AllowedRoots length=%d, want 2", len(after.AllowedRoots))
	}
	if !reflect.DeepEqual(configWithoutRoots(after), configWithoutRoots(before)) {
		t.Fatalf("non-root config changed:\nbefore=%#v\nafter=%#v", configWithoutRoots(before), configWithoutRoots(after))
	}

	if runtime.GOOS != "windows" {
		st, err := os.Stat(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != 0600 {
			t.Fatalf("config mode=%o, want 0600", got)
		}
	}
}

func TestAddRootRejectsDuplicateAndMissingPaths(t *testing.T) {
	configPath, existingPath := initRootTestConfig(t)
	secondPath := filepath.Join(t.TempDir(), "second")
	if err := os.Mkdir(secondPath, 0755); err != nil {
		t.Fatal(err)
	}
	duplicatePath := existingPath
	symlinkPath := filepath.Join(t.TempDir(), "existing-link")
	if err := os.Symlink(existingPath, symlinkPath); err == nil {
		duplicatePath = symlinkPath
	} else if runtime.GOOS != "windows" {
		t.Fatalf("create canonical-path test symlink: %v", err)
	}

	tests := []struct {
		name string
		opt  RootAddOptions
		want string
	}{
		{
			name: "duplicate ID",
			opt:  RootAddOptions{ConfigPath: configPath, Path: secondPath, ID: "initial"},
			want: "duplicate",
		},
		{
			name: "duplicate canonical path",
			opt:  RootAddOptions{ConfigPath: configPath, Path: duplicatePath, ID: "other"},
			want: "duplicate",
		},
		{
			name: "missing path",
			opt:  RootAddOptions{ConfigPath: configPath, Path: filepath.Join(t.TempDir(), "missing"), ID: "missing"},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AddRoot(tc.opt)
			if err == nil {
				t.Fatal("AddRoot unexpectedly succeeded")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%q, want substring %q", err, tc.want)
			}
		})
	}

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.AllowedRoots) != 1 || loaded.AllowedRoots[0].ID != "initial" {
		t.Fatalf("failed AddRoot changed config: %#v", loaded.AllowedRoots)
	}
}

func TestRemoveRootPreservesOtherRootsAndConfig(t *testing.T) {
	configPath, _ := initRootTestConfig(t)
	base, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	base.Server.TrustProxy = true
	base.Agents.MaxConcurrent = 9
	if err := saveAtomic(configPath, base); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"second", "third"} {
		path := filepath.Join(t.TempDir(), id)
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := AddRoot(RootAddOptions{ConfigPath: configPath, Path: path, ID: id, Name: strings.Title(id)}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := RemoveRoot(RootRemoveOptions{ConfigPath: configPath, ID: "second"}); err != nil {
		t.Fatal(err)
	}
	after, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(configWithoutRoots(before), configWithoutRoots(after)) {
		t.Fatalf("non-root config changed:\nbefore=%#v\nafter=%#v", configWithoutRoots(before), configWithoutRoots(after))
	}
	if len(after.AllowedRoots) != 2 || after.AllowedRoots[0].ID != "initial" || after.AllowedRoots[1].ID != "third" {
		t.Fatalf("remaining roots=%#v, want initial and third", after.AllowedRoots)
	}

	if err := RemoveRoot(RootRemoveOptions{ConfigPath: configPath, ID: "unknown"}); err == nil {
		t.Fatal("unknown root ID unexpectedly succeeded")
	}
	unchanged, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(unchanged, after) {
		t.Fatalf("unknown removal changed config:\nbefore=%#v\nafter=%#v", after, unchanged)
	}

	if err := RemoveRoot(RootRemoveOptions{ConfigPath: configPath, ID: "initial"}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRoot(RootRemoveOptions{ConfigPath: configPath, ID: "third"}); err != nil {
		t.Fatal(err)
	}
	last, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.AllowedRoots) != 0 {
		t.Fatalf("AllowedRoots length after last removal=%d, want 0", len(last.AllowedRoots))
	}
}
