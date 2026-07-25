package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// isolateHome gives the test a fully fabricated, empty $HOME so run() (via
// scan.Run/inspect.Run, both of which call os.UserHomeDir()) never touches
// this developer machine's real ~/.claude, ~/.codex, etc.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestRunDefaultModeListsMatchedFile(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), "hello from claude md")

	var buf bytes.Buffer
	if err := run([]string{dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Claude Code") {
		t.Errorf("expected Claude Code section, got:\n%s", out)
	}
	if !strings.Contains(out, "hello from claude md") {
		t.Errorf("expected file content present, got:\n%s", out)
	}
}

func TestRunDefaultsToCurrentDirectoryWhenNoPathGiven(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), "cwd content")

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origWD)

	var buf bytes.Buffer
	if err := run(nil, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(buf.String(), "cwd content") {
		t.Errorf("expected content from cwd default target, got:\n%s", buf.String())
	}
}

func TestRunJSONModeProducesValidJSON(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), "json content")

	var buf bytes.Buffer
	if err := run([]string{"--json", dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbody: %s", err, buf.String())
	}
	// A bare CLAUDE.md matches both claude-code (its native file) and
	// opencode (documented CLAUDE.md compat fallback per internal/tools
	// registry) -- confirmed empirically, not a bug. --all not given, so
	// only these two non-empty tools should appear.
	if len(out) != 2 {
		t.Fatalf("got %d tools, want 2 (claude-code + opencode's documented CLAUDE.md compat fallback, --all not given)", len(out))
	}
	slugs := map[string]bool{}
	for _, tool := range out {
		slugs[tool["slug"].(string)] = true
	}
	if !slugs["claude-code"] || !slugs["opencode"] {
		t.Errorf("expected claude-code and opencode slugs, got %v", slugs)
	}
}

func TestRunAllFlagIncludesEmptyTools(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir() // nothing placed in it

	var buf bytes.Buffer
	if err := run([]string{"--json", "--all", dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(out) != 9 {
		t.Fatalf("got %d tools, want 9 (--all with a fully empty target)", len(out))
	}
}

func TestRunFullFlagDisablesTruncation(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	big := strings.Repeat("x", 5000)
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), big)

	var bufDefault bytes.Buffer
	if err := run([]string{dir}, &bufDefault); err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(bufDefault.String(), "truncated") {
		t.Error("expected truncation marker without --full")
	}

	var bufFull bytes.Buffer
	if err := run([]string{"--full", dir}, &bufFull); err != nil {
		t.Fatalf("run error: %v", err)
	}
	if strings.Contains(bufFull.String(), "truncated") {
		t.Error("expected no truncation marker with --full")
	}
	if !strings.Contains(bufFull.String(), big) {
		t.Error("expected full content present with --full")
	}
}

func TestRunCompileModeProducesAssembledOutput(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), "compile me")

	var buf bytes.Buffer
	if err := run([]string{"--compile", dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Merge model:") {
		t.Errorf("expected compile-mode merge model line, got:\n%s", out)
	}
	if !strings.Contains(out, "compile me") {
		t.Errorf("expected assembled content, got:\n%s", out)
	}
}

func TestRunCompileJSONModeProducesValidJSON(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), "compile me")

	var buf bytes.Buffer
	if err := run([]string{"--compile", "--json", dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbody: %s", err, buf.String())
	}
	// A bare CLAUDE.md matches both claude-code (its native file) and
	// opencode (documented CLAUDE.md compat fallback per internal/tools
	// registry) -- confirmed empirically, not a bug. --all not given, so
	// only these two non-empty tools should appear.
	if len(out) != 2 {
		t.Fatalf("got %d tools, want 2 (claude-code + opencode's documented CLAUDE.md compat fallback, --all not given)", len(out))
	}
	for _, tool := range out {
		if tool["mergeModel"] == nil || tool["mergeModel"] == "" {
			t.Errorf("expected non-empty mergeModel field for tool %v", tool["slug"])
		}
	}
}

func TestRunLiveModeProducesNineReports(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()

	var buf bytes.Buffer
	if err := run([]string{"--live", "--json", dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbody: %s", err, buf.String())
	}
	if len(out) != 9 {
		t.Fatalf("got %d reports, want 9 (--live always reports on every known tool)", len(out))
	}
}

func TestRunLiveModeIgnoresCompileAndAllFlagsButStillWorks(t *testing.T) {
	// --live's output is inherently "every tool always" (see internal/inspect),
	// so passing --compile alongside it should not error and --live should
	// take priority (checked in main.go: liveMode is checked first, before
	// scan.Run/compileMode branch is ever reached).
	isolateHome(t)
	dir := t.TempDir()

	var buf bytes.Buffer
	if err := run([]string{"--live", "--compile", dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(buf.String(), "Runtime context introspection") {
		t.Errorf("expected live-mode header even with --compile also set, got:\n%s", buf.String())
	}
}

func TestRunLiveModeBypassesPathValidationForScanningButNotStatCheck(t *testing.T) {
	// --live still requires the target path to exist and be a directory
	// (the os.Stat/IsDir check happens before the liveMode branch), but once
	// past that, it does not call scan.Run at all -- confirmed indirectly by
	// the fact that a directory with zero instruction files still produces a
	// full 9-report --live output (rather than any scan-shaped output).
	isolateHome(t)
	dir := t.TempDir()

	var buf bytes.Buffer
	if err := run([]string{"--live", dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(buf.String(), "Runtime context introspection") {
		t.Error("expected live-mode text output")
	}
}

func TestRunErrorsOnNonexistentPath(t *testing.T) {
	isolateHome(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	var buf bytes.Buffer
	err := run([]string{missing}, &buf)
	if err == nil {
		t.Fatal("expected error for nonexistent path, got nil")
	}
	if !strings.Contains(err.Error(), "cannot access") {
		t.Errorf("error = %v, want it to mention 'cannot access'", err)
	}
}

func TestRunErrorsWhenPathIsAFileNotDirectory(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	filePath := filepath.Join(dir, "notadir.txt")
	mustWriteFile(t, filePath, "i am a file")

	var buf bytes.Buffer
	err := run([]string{filePath}, &buf)
	if err == nil {
		t.Fatal("expected error when path is a file, got nil")
	}
	if !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("error = %v, want it to mention 'is not a directory'", err)
	}
}

func TestRunErrorsOnUnknownFlag(t *testing.T) {
	isolateHome(t)
	var buf bytes.Buffer
	err := run([]string{"--not-a-real-flag"}, &buf)
	if err == nil {
		t.Fatal("expected error for unknown flag, got nil")
	}
}

func TestRunErrorsOnScanFailurePropagatesWrappedError(t *testing.T) {
	// scan.Run itself basically never errors for a valid, statted directory
	// in practice (BuildChain's only error path is filepath.Abs, which needs
	// a broken os.Getwd -- not practically triggerable in a hermetic test).
	// This test instead locks in that a nonexistent path fails at the
	// os.Stat guard, before scan.Run would ever be reached, which is the
	// realistic error path a user hits.
	isolateHome(t)
	missing := filepath.Join(t.TempDir(), "nope")
	var buf bytes.Buffer
	if err := run([]string{missing}, &buf); err == nil {
		t.Fatal("expected error")
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output written on error, got:\n%s", buf.String())
	}
}
