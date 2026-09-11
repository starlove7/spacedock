package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/starlove7/spacedock/internal/policy"
)

func writeSensitiveFixture(t *testing.T, wRoot string) {
	t.Helper()
	for _, dir := range []string{".ssh", ".aws", "benign-dir"} {
		if err := os.MkdirAll(filepath.Join(wRoot, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"main.go":             "package main\n",
		".env":                "needle from env\n",
		".env.production":     "needle from production env\n",
		".ssh/id_rsa":         "needle from rsa\n",
		".aws/credentials":    "needle from credentials\n",
		"cert.pem":            "needle from cert\n",
		"benign.txt":          "needle from benign\n",
		"benign-dir/file.txt": "benign directory\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(wRoot, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func entryPaths(entries []Entry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	return paths
}

func hasPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

func TestSensitiveFilesystemReadListSearchContracts(t *testing.T) {
	w := testWS(t)
	writeSensitiveFixture(t, w.Root)
	s := testService(t, nil)

	if got, err := s.ReadFile(w, "main.go", 1, 20, 1024); err != nil || got.Content != "package main\n" {
		t.Fatalf("normal read failed: result=%+v err=%v", got, err)
	}
	if got, err := s.ListDir(w, "benign-dir", 10); err != nil || len(got.Entries) != 1 || got.Entries[0].Path != "benign-dir/file.txt" {
		t.Fatalf("normal list failed: result=%+v err=%v", got, err)
	}
	if got, err := s.SearchText(w, ".", "needle from benign", false, false, 100); err != nil || len(got.Matches) != 1 || got.Matches[0].Path != "benign.txt" {
		t.Fatalf("normal search failed: result=%+v err=%v", got, err)
	}
	if _, err := s.Edit(w, EditRequest{Action: "write", Path: "new.txt", Content: "normal"}); err != nil {
		t.Fatalf("normal write failed: %v", err)
	}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"read sensitive path", func() error {
			_, err := s.ReadFile(w, ".env", 1, 20, 1024)
			return err
		}},
		{"list sensitive directory", func() error {
			_, err := s.ListDir(w, ".ssh", 10)
			return err
		}},
		{"list files sensitive base", func() error {
			_, err := s.ListFiles(w, ".aws", "*", 8, 100)
			return err
		}},
		{"search sensitive base", func() error {
			_, err := s.SearchText(w, ".env", "needle", false, false, 100)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, policy.ErrSensitivePath) {
				t.Fatalf("error=%v, want ErrSensitivePath", err)
			}
		})
	}

	rootList, err := s.ListDir(w, ".", 100)
	if err != nil {
		t.Fatal(err)
	}
	rootPaths := entryPaths(rootList.Entries)
	for _, hidden := range []string{".env", ".env.production", ".ssh", ".aws", "cert.pem"} {
		if hasPath(rootPaths, hidden) {
			t.Errorf("sensitive ListDir entry exposed: %q; entries=%v", hidden, rootPaths)
		}
	}
	if !hasPath(rootPaths, "benign.txt") || !hasPath(rootPaths, "benign-dir") {
		t.Errorf("benign ListDir entries missing: %v", rootPaths)
	}

	limited, err := s.ListDir(w, ".", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Entries) != 1 || limited.Entries[0].Path != "benign-dir" {
		t.Errorf("hidden entries consumed max_entries: %+v", limited)
	}

	listed, err := s.ListFiles(w, ".", "*", 8, 100)
	if err != nil {
		t.Fatal(err)
	}
	listFilesPaths := entryPaths(listed.Entries)
	for _, hidden := range []string{".env", ".env.production", ".ssh/id_rsa", ".aws/credentials", "cert.pem"} {
		if hasPath(listFilesPaths, hidden) {
			t.Errorf("sensitive ListFiles entry exposed: %q; entries=%v", hidden, listFilesPaths)
		}
	}
	if !hasPath(listFilesPaths, "benign.txt") || !hasPath(listFilesPaths, "main.go") {
		t.Errorf("benign ListFiles entries missing: %v", listFilesPaths)
	}

	searched, err := s.SearchText(w, ".", "needle", false, false, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range searched.Matches {
		if strings.HasPrefix(match.Path, ".ssh/") || strings.HasPrefix(match.Path, ".aws/") || strings.HasPrefix(match.Path, ".env") || match.Path == "cert.pem" {
			t.Errorf("sensitive SearchText match exposed: %+v", match)
		}
	}
	if searched.FilesScanned != 4 || searched.FilesSkipped != 0 {
		t.Errorf("sensitive files counted in search accounting: scanned=%d skipped=%d result=%+v", searched.FilesScanned, searched.FilesSkipped, searched)
	}
}

func TestSensitiveFilesystemMutationsAreBlockedBeforeMutation(t *testing.T) {
	w := testWS(t)
	writeSensitiveFixture(t, w.Root)
	s := testService(t, nil)
	if err := os.Remove(filepath.Join(w.Root, ".env.production")); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{".env.production", ".EnV.Local"} {
		_, err := s.Edit(w, EditRequest{Action: "write", Path: path, Content: "do not write"})
		if !errors.Is(err, policy.ErrSensitivePath) {
			t.Errorf("write %q error=%v, want ErrSensitivePath", path, err)
		}
		if _, statErr := os.Lstat(filepath.Join(w.Root, path)); !os.IsNotExist(statErr) {
			t.Errorf("blocked write created %q: stat error=%v", path, statErr)
		}
	}

	envPath := filepath.Join(w.Root, ".env")
	envBefore, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"replace", "delete"} {
		_, err := s.Edit(w, EditRequest{Action: action, Path: ".env", OldText: "needle", NewText: "changed", ExpectedMatches: 1, ReplaceAll: true})
		if !errors.Is(err, policy.ErrSensitivePath) {
			t.Errorf("%s sensitive file error=%v, want ErrSensitivePath", action, err)
		}
		contents, readErr := os.ReadFile(envPath)
		if readErr != nil || string(contents) != string(envBefore) {
			t.Errorf("%s changed sensitive file: readErr=%v contents=%q", action, readErr, contents)
		}
	}

	if _, err := s.Edit(w, EditRequest{Action: "move", Path: ".env", NewPath: "moved.txt"}); !errors.Is(err, policy.ErrSensitivePath) {
		t.Errorf("sensitive source move error=%v, want ErrSensitivePath", err)
	}
	if _, err := os.Stat(filepath.Join(w.Root, "moved.txt")); !os.IsNotExist(err) {
		t.Errorf("sensitive source move created destination: %v", err)
	}
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf("sensitive source move removed source: %v", err)
	}

	if _, err := s.Edit(w, EditRequest{Action: "move", Path: "main.go", NewPath: "credentials.json"}); !errors.Is(err, policy.ErrSensitivePath) {
		t.Errorf("sensitive destination move error=%v, want ErrSensitivePath", err)
	}
	if _, err := os.Stat(filepath.Join(w.Root, "main.go")); err != nil {
		t.Errorf("sensitive destination move removed source: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.Root, "credentials.json")); !os.IsNotExist(err) {
		t.Errorf("sensitive destination move created destination: %v", err)
	}
}

func TestSensitiveFilesystemSymlinkProtection(t *testing.T) {
	w := testWS(t)
	writeSensitiveFixture(t, w.Root)
	s := testService(t, nil)

	if err := os.Symlink(filepath.Join(w.Root, ".env"), filepath.Join(w.Root, "benign-alias.txt")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := s.ReadFile(w, "benign-alias.txt", 1, 20, 1024); !errors.Is(err, policy.ErrSensitivePath) {
		t.Errorf("sensitive target alias read error=%v, want ErrSensitivePath", err)
	}
	for _, list := range []struct {
		name string
		call func() ([]string, error)
	}{
		{"ListDir", func() ([]string, error) {
			r, err := s.ListDir(w, ".", 100)
			return entryPaths(r.Entries), err
		}},
		{"ListFiles", func() ([]string, error) {
			r, err := s.ListFiles(w, ".", "*", 8, 100)
			return entryPaths(r.Entries), err
		}},
	} {
		t.Run(list.name, func(t *testing.T) {
			paths, err := list.call()
			if err != nil {
				t.Fatal(err)
			}
			if hasPath(paths, "benign-alias.txt") {
				t.Errorf("sensitive target alias exposed: %v", paths)
			}
		})
	}
	searched, err := s.SearchText(w, ".", "needle", false, false, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range searched.Matches {
		if match.Path == "benign-alias.txt" {
			t.Errorf("sensitive target alias search match exposed: %+v", match)
		}
	}

	if err := os.Symlink(filepath.Join(w.Root, ".ssh"), filepath.Join(w.Root, "benign-dir-alias")); err != nil {
		t.Skipf("symlink directory creation unavailable: %v", err)
	}
	_, err = s.Edit(w, EditRequest{Action: "write", Path: "benign-dir-alias/new.keyless.txt", Content: "blocked"})
	if !errors.Is(err, policy.ErrSensitivePath) {
		t.Errorf("write through sensitive directory alias error=%v, want ErrSensitivePath", err)
	}
	if _, statErr := os.Lstat(filepath.Join(w.Root, "benign-dir-alias", "new.keyless.txt")); !os.IsNotExist(statErr) {
		t.Errorf("write through sensitive directory alias created file: %v", statErr)
	}
}

func TestAdditionalSensitivePatternsApplyToFilesystem(t *testing.T) {
	w := testWS(t)
	if err := os.WriteFile(filepath.Join(w.Root, "top.secret"), []byte("additional needle\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Root, "normal.txt"), []byte("additional needle\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := testService(t, []string{"*.secret"})
	if _, err := s.ReadFile(w, "top.secret", 1, 20, 1024); !errors.Is(err, policy.ErrSensitivePath) {
		t.Errorf("additional explicit read error=%v, want ErrSensitivePath", err)
	}
	if _, err := s.Edit(w, EditRequest{Action: "write", Path: "new.secret", Content: "blocked"}); !errors.Is(err, policy.ErrSensitivePath) {
		t.Errorf("additional write error=%v, want ErrSensitivePath", err)
	}
	if _, err := os.Stat(filepath.Join(w.Root, "new.secret")); !os.IsNotExist(err) {
		t.Errorf("additional blocked write created file: %v", err)
	}
	listed, err := s.ListFiles(w, ".", "*", 8, 100)
	if err != nil {
		t.Fatal(err)
	}
	if hasPath(entryPaths(listed.Entries), "top.secret") {
		t.Errorf("additional sensitive file exposed by ListFiles: %+v", listed)
	}
	searched, err := s.SearchText(w, ".", "additional needle", false, false, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range searched.Matches {
		if match.Path == "top.secret" {
			t.Errorf("additional sensitive file exposed by SearchText: %+v", match)
		}
	}
}
