package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRootRemoveArgs(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	tests := []struct {
		name       string
		args       []string
		wantID     string
		wantConfig string
	}{
		{name: "positional ID", args: []string{"root-1", "--config", configPath}, wantID: "root-1", wantConfig: configPath},
		{name: "id then config", args: []string{"--id", "root-1", "--config", configPath}, wantID: "root-1", wantConfig: configPath},
		{name: "config then id", args: []string{"--config", configPath, "--id", "root-1"}, wantID: "root-1", wantConfig: configPath},
		{name: "equals flags", args: []string{"--id=root-1", "--config=" + configPath}, wantID: "root-1", wantConfig: configPath},
		{name: "config between positional and end", args: []string{"root-1", "--config=" + configPath}, wantID: "root-1", wantConfig: configPath},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotConfig, err := parseRootRemoveArgs(tc.args)
			if err != nil {
				t.Fatalf("parseRootRemoveArgs() error=%v", err)
			}
			if gotID != tc.wantID || gotConfig != tc.wantConfig {
				t.Fatalf("parseRootRemoveArgs()=(%q, %q), want (%q, %q)", gotID, gotConfig, tc.wantID, tc.wantConfig)
			}
		})
	}
}

func TestParseRootRemoveArgsRejectsInvalidInput(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "positional and id flag", args: []string{"root-1", "--id", "root-2", "--config", configPath}},
		{name: "missing root ID", args: []string{"--config", configPath}},
		{name: "unknown flag", args: []string{"root-1", "--verbose", "--config", configPath}},
		{name: "missing config value", args: []string{"root-1", "--config"}},
		{name: "missing id value", args: []string{"--id", "--config", configPath}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := parseRootRemoveArgs(tc.args); err == nil {
				t.Fatal("parseRootRemoveArgs unexpectedly succeeded")
			}
		})
	}
}

func runSpaceDock(t *testing.T, binary, workDir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = workDir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("command %q failed: %v\noutput:\n%s", strings.Join(args, " "), err, output.String())
	}
	t.Logf("command=%q exit_code=0 output=%q", strings.Join(args, " "), output.String())
	return output.String()
}

func TestRootCLIFlowUsesOnlyTemporaryConfigAndRoots(t *testing.T) {
	workDir := t.TempDir()
	initialRoot := filepath.Join(workDir, "initial-root")
	additionalRoot := filepath.Join(workDir, "additional-root")
	for _, path := range []string{initialRoot, additionalRoot} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(workDir, "state", "config.yaml")
	binary := filepath.Join(workDir, "spacedock")

	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir, build.Stdout, build.Stderr = workDirForPackage(t), new(bytes.Buffer), new(bytes.Buffer)
	if err := build.Run(); err != nil {
		t.Fatalf("go build failed: %v\nstdout:\n%s\nstderr:\n%s", err, build.Stdout.(*bytes.Buffer).String(), build.Stderr.(*bytes.Buffer).String())
	}
	t.Logf("command=go build -o %s . exit_code=0", binary)

	runSpaceDock(t, binary, workDir, "init", "--config", configPath, "--root", initialRoot)
	runSpaceDock(t, binary, workDir, "root", "add", "--config", configPath, "--path", additionalRoot, "--id", "additional", "--name", "Additional")
	listBeforeRemove := runSpaceDock(t, binary, workDir, "root", "list", "--config", configPath)
	if !strings.Contains(listBeforeRemove, "ID\tNAME\tPATH\tPERMISSIONS") {
		t.Fatalf("root list missing header: %q", listBeforeRemove)
	}
	for _, id := range []string{"initial-root", "additional"} {
		if !strings.Contains(listBeforeRemove, id+"\t") {
			t.Fatalf("root list missing %q: %q", id, listBeforeRemove)
		}
	}

	runSpaceDock(t, binary, workDir, "root", "remove", "additional", "--config", configPath)
	listAfterRemove := runSpaceDock(t, binary, workDir, "root", "list", "--config", configPath)
	if !strings.Contains(listAfterRemove, "ID\tNAME\tPATH\tPERMISSIONS") || !strings.Contains(listAfterRemove, "initial-root\t") {
		t.Fatalf("root list after remove missing header or remaining root: %q", listAfterRemove)
	}
	if strings.Contains(listAfterRemove, "additional\t") {
		t.Fatalf("removed root still listed: %q", listAfterRemove)
	}
}

func workDirForPackage(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
