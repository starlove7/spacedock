package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolverPathContracts(t *testing.T) {
	root := t.TempDir()
	r, err := NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.ResolveExisting("."); err != nil || got != root {
		t.Fatalf("root ResolveExisting: got %q, err %v", got, err)
	}
	if !IsWithin(root, root) {
		t.Fatal("root must be within itself")
	}
	for _, p := range []string{"", " ", "/tmp", "~/x", "//server/share", `C:\\x`, "https://x", "../x", "a/../../x"} {
		if _, err := r.ValidateRelative(p); err == nil {
			t.Errorf("ValidateRelative(%q) accepted", p)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveExisting("link"); err == nil {
		t.Error("final symlink escape accepted")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "dirlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveForWrite("dirlink/new.txt"); err == nil {
		t.Error("existing parent symlink escape accepted")
	}
}
