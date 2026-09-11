package app

import (
	"github.com/starlove7/spacedock/internal/config"
	"testing"
)

func TestNewAndClose(t *testing.T) {
	a, e := New(config.Config{StateDir: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	if a.Commands == nil || a.ACP == nil || a.Tools == nil {
		t.Fatal("missing managers")
	}
	a.Close()
	a.Close()
}
