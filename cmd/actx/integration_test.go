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

func TestIntegrationHelpFlagPrintsUsageAndExitsZero(t *testing.T) {
	bin := buildBinary(t)

	out, errOut, code := runBinary(t, bin, "--help")
	combined := out + errOut
	if !strings.Contains(combined, "Usage:") {
		t.Errorf("expected usage text on --help, got stdout: %s stderr: %s", out, errOut)
	}
	if !strings.Contains(combined, "--compile") || !strings.Contains(combined, "--live") {
		t.Errorf("expected usage text to document --compile and --live, got: %s", combined)
	}
	// These lines only come from fs.PrintDefaults(); if fs.Output() is ever
	// left pointed at a discard sink when PrintDefaults runs, they vanish
	// silently while the rest of the (hand-written) usage text still prints.
	if !strings.Contains(combined, "-tool string") {
		t.Errorf("expected usage text to include the auto-generated -tool flag description, got: %s", combined)
	}
	if !strings.Contains(combined, "-version") {
		t.Errorf("expected usage text to include the auto-generated -version flag description, got: %s", combined)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0 for --help (a help request is not a failure)", code)
	}
}

func TestIntegrationUnknownFlagExitsTwoWithSingleErrorLine(t *testing.T) {
	bin := buildBinary(t)

	_, errOut, code := runBinary(t, bin, "--not-a-real-flag")
	if code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
	if strings.Count(errOut, "flag provided but not defined") != 1 {
		t.Errorf("expected exactly one occurrence of the error message on stderr, got: %s", errOut)
	}
}

func TestIntegrationJSONErrorIsStructuredJSON(t *testing.T) {
	bin := buildBinary(t)

	_, errOut, code := runBinary(t, bin, "--json", "/this/path/does/not/exist/anywhere")
	if code != 1 {
		t.Errorf("exit code = %d, want 1 for a runtime error", code)
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(errOut), &parsed); err != nil {
		t.Fatalf("stderr under --json is not a valid JSON object: %v\nstderr: %s", err, errOut)
	}
	if parsed["error"] == "" {
		t.Errorf("expected non-empty \"error\" field, got: %v", parsed)
	}
}

func TestIntegrationJSONEqualsTrueErrorIsStructuredJSON(t *testing.T) {
	bin := buildBinary(t)

	// A flag-parse failure (not a runtime error) with --json passed in the
	// "=value" form the flag package also accepts for bool flags; the error
	// must still come out as JSON, matching the bare "--json" form. stderr
	// also carries the usage text on a flag-parse failure (printed by
	// fs.Usage regardless of --json), so only the final line is the error
	// object, same as TestIntegrationUnknownFlagExitsTwoWithSingleErrorLine.
	_, errOut, code := runBinary(t, bin, "--json=true", "--not-a-real-flag")
	if code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
	lines := strings.Split(strings.TrimRight(errOut, "\n"), "\n")
	lastLine := lines[len(lines)-1]
	var parsed map[string]string
	if err := json.Unmarshal([]byte(lastLine), &parsed); err != nil {
		t.Fatalf("last stderr line under --json=true is not a valid JSON object: %v\nline: %s\nfull stderr: %s", err, lastLine, errOut)
	}
	if parsed["error"] == "" {
		t.Errorf("expected non-empty \"error\" field, got: %v", parsed)
	}
}

func TestIntegrationUnknownToolSlugExitsTwo(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	_, errOut, code := runBinary(t, bin, "--tool=bogus-slug", dir)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 for an unknown --tool slug", code)
	}
	if !strings.Contains(errOut, "bogus-slug") {
		t.Errorf("expected error to mention the bad slug, got: %s", errOut)
	}
}

func TestIntegrationToolFilterFlagNarrowsOutput(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), "claude content")
	mustWriteFile(t, filepath.Join(dir, "AGENTS.md"), "codex content")

	out, errOut, code := runBinary(t, bin, "--json", "--tool=codex-cli", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, errOut)
	}
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, out)
	}
	if len(parsed) != 1 || parsed[0]["slug"] != "codex-cli" {
		t.Fatalf("expected exactly one tool (codex-cli), got: %v", parsed)
	}
}

func TestIntegrationVersionFlagPrintsVersionAndExitsZero(t *testing.T) {
	bin := buildBinary(t)

	out, errOut, code := runBinary(t, bin, "--version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, errOut)
	}
	if !strings.Contains(out, "actx") {
		t.Errorf("expected --version output to mention actx, got: %s", out)
	}
}
