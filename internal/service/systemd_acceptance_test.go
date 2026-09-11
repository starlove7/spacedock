//go:build linux

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeSystemctl(t *testing.T) (string, string) {
	t.Helper()
	d := t.TempDir()
	logPath := filepath.Join(d, "systemctl.log")
	bin := filepath.Join(d, "systemctl")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$SYSTEMCTL_LOG\"\ncase \"$*\" in\n  *' --no-pager --full status '*) echo fake-status;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return d, logPath
}

func TestSystemdInstallActionOrderUnitEscapingAndLifecycleCommands(t *testing.T) {
	binDir, logPath := fakeSystemctl(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SYSTEMCTL_LOG", logPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	configDir := filepath.Join(t.TempDir(), "config dir")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, `weird\"% config.yaml`)
	if err := os.WriteFile(configPath, []byte("state_dir: /tmp\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(configPath); err != nil {
		t.Fatal(err)
	}

	unitPath := filepath.Join(home, ".config", "systemd", "user", "spacedock.service")
	unit, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(unit)
	if !strings.Contains(text, "[Service]\nType=simple\n") || !strings.Contains(text, "Restart=on-failure\n") {
		t.Fatalf("unit missing service contract: %s", text)
	}
	line := ""
	for _, x := range strings.Split(text, "\n") {
		if strings.HasPrefix(x, "ExecStart=") {
			line = x
		}
	}
	if line == "" || !strings.Contains(line, "ExecStart=\"") || !strings.Contains(line, " --config \"") || strings.Contains(line, `ExecStart=\"`) {
		t.Fatalf("ExecStart does not use real double-quoted args: %q", line)
	}
	if !strings.Contains(line, `%%`) || !strings.Contains(line, `\\`) || !strings.Contains(line, `\"`) {
		t.Fatalf("ExecStart did not escape percent/backslash/quote safely: %q", line)
	}

	if err := Start(); err != nil {
		t.Fatal(err)
	}
	if err := Stop(); err != nil {
		t.Fatal(err)
	}
	if err := Restart(); err != nil {
		t.Fatal(err)
	}
	var status strings.Builder
	if err := Status(&status); err != nil || status.String() != "fake-status\n" {
		t.Fatalf("Status output=%q err=%v", status.String(), err)
	}
	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Fatalf("unit still exists after uninstall: %v", err)
	}

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	want := []string{
		"--user daemon-reload",
		"--user enable --now spacedock.service",
		"--user start spacedock.service",
		"--user stop spacedock.service",
		"--user restart spacedock.service",
		"--user --no-pager --full status spacedock.service",
		"--user disable --now spacedock.service",
		"--user daemon-reload",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("systemctl call order=%q want=%q", lines, want)
	}
}
