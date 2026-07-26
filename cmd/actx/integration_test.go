package main

// Integration tests: these build the real actx binary
// and exercise it as a subprocess (real flag parsing, real os.Exit/stderr
// behavior, real stdout stream) -- unlike main_test.go, which calls run()
// in-process. Both are useful: main_test.go is faster and covers more cases,
// this file proves the actual compiled artifact behaves the same way.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildBinary compiles the CLI once per test binary run and returns its path.
func buildBinary(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "actx")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return binPath
}

// runBinary execs the built binary with a fabricated, empty $HOME (so the
// subprocess never reads this developer machine's real ~/.claude, ~/.codex,
// etc.) and returns stdout, stderr, and the exit code.
func runBinary(t *testing.T, binPath string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run binary: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func TestIntegrationDefaultModeListsMatchedFile(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), "hello from claude md")

	out, errOut, code := runBinary(t, bin, dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, errOut)
	}
	if !strings.Contains(out, "Claude Code") {
		t.Errorf("stdout missing %q, got: %s", "Claude Code", out)
	}
	if !strings.Contains(out, "CLAUDE.md") {
		t.Errorf("stdout missing matched filename, got: %s", out)
	}
}

func TestIntegrationJSONModeProducesValidJSON(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "AGENTS.md"), "agents content")

	out, errOut, code := runBinary(t, bin, "--json", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, errOut)
	}
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, out)
	}
	if len(parsed) == 0 {
		t.Errorf("expected at least one tool entry in JSON output, got none")
	}
}

func TestIntegrationCompileModeAssemblesContext(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), "compiled content marker XYZ")

	out, errOut, code := runBinary(t, bin, "--compile", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, errOut)
	}
	if !strings.Contains(out, "compiled content marker XYZ") {
		t.Errorf("expected assembled content in --compile output, got: %s", out)
	}

	jsonOut, errOut, code := runBinary(t, bin, "--compile", "--json", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, errOut)
	}
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil {
		t.Fatalf("--compile --json stdout is not valid JSON: %v\noutput: %s", err, jsonOut)
	}
}

func TestIntegrationLiveModeRuns(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	out, errOut, code := runBinary(t, bin, "--live", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, errOut)
	}
	if !strings.Contains(out, "Claude Code") {
		t.Errorf("expected --live output to mention Claude Code, got: %s", out)
	}

	jsonOut, errOut, code := runBinary(t, bin, "--live", "--json", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, errOut)
	}
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil {
		t.Fatalf("--live --json stdout is not valid JSON: %v\noutput: %s", err, jsonOut)
	}
}

func TestIntegrationAllFlagIncludesEmptyTools(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir() // no instruction files at all

	withoutAll, _, code := runBinary(t, bin, dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	withAll, _, code := runBinary(t, bin, "--all", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if len(withAll) <= len(withoutAll) {
		t.Errorf("--all output (%d bytes) should be longer than default output (%d bytes) when no files match", len(withAll), len(withoutAll))
	}
}

func TestIntegrationNonexistentPathFailsWithNonzeroExit(t *testing.T) {
	bin := buildBinary(t)

	out, errOut, code := runBinary(t, bin, "/this/path/does/not/exist/anywhere")
	if code == 0 {
		t.Fatalf("expected nonzero exit code for nonexistent path, got 0; stdout: %s", out)
	}
	if errOut == "" {
		t.Errorf("expected an error message on stderr, got none")
	}
}

func TestIntegrationFileInsteadOfDirectoryFails(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	filePath := filepath.Join(dir, "notadir.txt")
	mustWriteFile(t, filePath, "i am a file, not a directory")

	_, errOut, code := runBinary(t, bin, filePath)
	if code == 0 {
		t.Fatalf("expected nonzero exit code when path is a file, got 0")
	}
	if errOut == "" {
		t.Errorf("expected an error message on stderr, got none")
	}
}

func TestIntegrationHelpFlagPrintsUsage(t *testing.T) {
	bin := buildBinary(t)

	out, errOut, code := runBinary(t, bin, "--help")
	combined := out + errOut
	if !strings.Contains(combined, "Usage:") {
		t.Errorf("expected usage text on --help, got stdout: %s stderr: %s", out, errOut)
	}
	if !strings.Contains(combined, "--compile") || !strings.Contains(combined, "--live") {
		t.Errorf("expected usage text to document --compile and --live, got: %s", combined)
	}
	_ = code // flag.ContinueOnError + Usage still exits nonzero on -h; not asserted here, just that usage prints.
}
