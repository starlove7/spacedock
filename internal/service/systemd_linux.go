//go:build linux

package service

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func run(args ...string) error {
	return exec.Command("systemctl", append([]string{"--user"}, args...)...).Run()
}
func quoteSystemdArg(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	return strconv.Quote(s)
}
func Install(configPath string) error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	exe, e = filepath.EvalSymlinks(exe)
	if e != nil {
		return e
	}
	exe, _ = filepath.Abs(exe)
	configPath, _ = filepath.Abs(filepath.Clean(configPath))
	if st, e := os.Stat(configPath); e != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("invalid config path")
	}
	h, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	d := filepath.Join(h, ".config", "systemd", "user")
	if e = os.MkdirAll(d, 0700); e != nil {
		return e
	}
	unit := fmt.Sprintf("[Unit]\nDescription=SpaceDock MCP Server\nAfter=network-online.target\n\n[Service]\nType=simple\nExecStart=%s serve --config %s\nRestart=on-failure\nRestartSec=2\n\n[Install]\nWantedBy=default.target\n", quoteSystemdArg(exe), quoteSystemdArg(configPath))
	f, e := os.CreateTemp(d, ".spacedock-")
	if e != nil {
		return e
	}
	n := f.Name()
	defer os.Remove(n)
	_ = f.Chmod(0600)
	if _, e = f.WriteString(unit); e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(n, filepath.Join(d, "spacedock.service"))
	}
	if e != nil {
		return e
	}
	if e = run("daemon-reload"); e != nil {
		return e
	}
	return run("enable", "--now", "spacedock.service")
}
func Start() error   { return run("start", "spacedock.service") }
func Stop() error    { return run("stop", "spacedock.service") }
func Restart() error { return run("restart", "spacedock.service") }
func Status(out io.Writer) error {
	c := exec.Command("systemctl", "--user", "--no-pager", "--full", "status", "spacedock.service")
	c.Stdout = out
	c.Stderr = out
	return c.Run()
}
func Uninstall() error {
	_ = run("disable", "--now", "spacedock.service")
	h, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	_ = os.Remove(filepath.Join(h, ".config", "systemd", "user", "spacedock.service"))
	return run("daemon-reload")
}
