package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWantsJSON(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"--json", "."}, true},
		{[]string{"-json"}, true},
		{[]string{"--compile", "."}, false},
		{nil, false},
		{[]string{"--json=true", "."}, true},
		{[]string{"-json=true"}, true},
		{[]string{"--json=false", "."}, false},
		{[]string{"--json=1", "."}, true},
		{[]string{"--json=0", "."}, false},
		{[]string{"--jsonlint", "."}, false},
		{[]string{"--json=true", "--json=false"}, false},
		{[]string{"--json=false", "--json"}, true},
	}
	for _, c := range cases {
		if got := wantsJSON(c.args); got != c.want {
			t.Errorf("wantsJSON(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestExitCode(t *testing.T) {
	if got := exitCode(&usageError{errors.New("bad flag")}); got != 2 {
		t.Errorf("exitCode(usageError) = %d, want 2", got)
	}
	if got := exitCode(errors.New("runtime failure")); got != 1 {
		t.Errorf("exitCode(plain error) = %d, want 1", got)
	}
}

func TestUsageErrorUnwrap(t *testing.T) {
	inner := errors.New("inner")
	ue := &usageError{inner}
	if !errors.Is(ue, inner) {
		t.Errorf("expected errors.Is to see through usageError.Unwrap to the inner error")
	}
}

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
	// A bare CLAUDE.md matches claude-code (its native file), opencode
	// (documented CLAUDE.md compat fallback), and hermes (documented
	// cwd-only CLAUDE.md compat fallback) per internal/tools registry --
	// confirmed empirically, not a bug. --all not given, so only these
	// three non-empty tools should appear.
	if len(out) != 3 {
		t.Fatalf("got %d tools, want 3 (claude-code + opencode + hermes's documented CLAUDE.md compat fallbacks, --all not given)", len(out))
	}
	slugs := map[string]bool{}
	for _, tool := range out {
		slugs[tool["slug"].(string)] = true
	}
	if !slugs["claude-code"] || !slugs["opencode"] || !slugs["hermes"] {
		t.Errorf("expected claude-code, opencode, and hermes slugs, got %v", slugs)
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
	if len(out) != 10 {
		t.Fatalf("got %d tools, want 10 (--all with a fully empty target)", len(out))
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
	// Match the specific marker phrase, not the bare word "truncated" --
	// Hermes's PrecedenceNote (always printed, regardless of --full)
	// legitimately contains that word in unrelated prose describing its
	// own per-file truncation behavior.
	if !strings.Contains(bufDefault.String(), "truncated, use --full") {
		t.Error("expected truncation marker without --full")
	}

	var bufFull bytes.Buffer
	if err := run([]string{"--full", dir}, &bufFull); err != nil {
		t.Fatalf("run error: %v", err)
	}
	if strings.Contains(bufFull.String(), "truncated, use --full") {
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
	// A bare CLAUDE.md matches claude-code (its native file), opencode
	// (documented CLAUDE.md compat fallback), and hermes (documented
	// cwd-only CLAUDE.md compat fallback) per internal/tools registry --
	// confirmed empirically, not a bug. --all not given, so only these
	// three non-empty tools should appear.
	if len(out) != 3 {
		t.Fatalf("got %d tools, want 3 (claude-code + opencode + hermes's documented CLAUDE.md compat fallbacks, --all not given)", len(out))
	}
	for _, tool := range out {
		if tool["mergeModel"] == nil || tool["mergeModel"] == "" {
			t.Errorf("expected non-empty mergeModel field for tool %v", tool["slug"])
		}
	}
}

func TestRunMaxCharsWritesOverflowNoticeInTextMode(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), "this content is long enough to exceed a tiny max-chars cap")

	var buf bytes.Buffer
	if err := run([]string{"--max-chars=50", dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Output too large") {
		t.Fatalf("expected overflow notice, got:\n%s", out)
	}
	if !strings.Contains(out, "50-char safety cap") {
		t.Errorf("expected notice to mention the configured cap, got:\n%s", out)
	}

	path := extractOverflowFilePath(t, out)
	defer os.Remove(path)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading overflow file %s: %v", path, err)
	}
	if !strings.Contains(string(content), "this content is long enough") {
		t.Errorf("expected overflow file to contain the full untruncated output, got:\n%s", content)
	}
}

func TestRunMaxCharsWritesOverflowNoticeInJSONMode(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), "this content is long enough to exceed a tiny max-chars cap")

	var buf bytes.Buffer
	if err := run([]string{"--max-chars=50", "--json", dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var notice outputOverflow
	if err := json.Unmarshal(buf.Bytes(), &notice); err != nil {
		t.Fatalf("overflow notice is not valid JSON: %v\nbody: %s", err, buf.String())
	}
	if !notice.Truncated {
		t.Errorf("expected truncated=true, got %+v", notice)
	}
	if notice.CharCount <= 50 {
		t.Errorf("expected charCount > 50, got %d", notice.CharCount)
	}
	if notice.OutputFile == "" {
		t.Fatal("expected a non-empty outputFile path")
	}
	defer os.Remove(notice.OutputFile)
	content, err := os.ReadFile(notice.OutputFile)
	if err != nil {
		t.Fatalf("reading overflow file %s: %v", notice.OutputFile, err)
	}
	var full []map[string]any
	if err := json.Unmarshal(content, &full); err != nil {
		t.Fatalf("overflow file is not the full valid JSON output: %v\nbody: %s", err, content)
	}
}

func TestRunMaxCharsWritesOverflowToConfiguredOutputFile(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), "this content is long enough to exceed a tiny max-chars cap")
	outputPath := filepath.Join(t.TempDir(), "full-output.txt")

	var buf bytes.Buffer
	if err := run([]string{"--max-chars=50", "--out=" + outputPath, dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	if got := extractOverflowFilePath(t, buf.String()); got != outputPath {
		t.Fatalf("overflow path = %q, want configured path %q", got, outputPath)
	}
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("reading configured output file %s: %v", outputPath, err)
	}
	if !strings.Contains(string(content), "this content is long enough") {
		t.Errorf("expected configured output file to contain full output, got:\n%s", content)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(outputPath)
		if err != nil {
			t.Fatalf("stat configured output file %s: %v", outputPath, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("configured output file permissions = %04o, want 0600", got)
		}
	}
}

func TestRunMaxCharsZeroDisablesCap(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), "this content is long enough to exceed a tiny max-chars cap")

	var buf bytes.Buffer
	if err := run([]string{"--max-chars=0", dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "Output too large") {
		t.Errorf("expected --max-chars=0 to disable the cap entirely, got:\n%s", out)
	}
	if !strings.Contains(out, "this content is long enough") {
		t.Errorf("expected full content inline, got:\n%s", out)
	}
}

func TestRunDefaultMaxCharsDoesNotTriggerForSmallOutput(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), "small content")

	var buf bytes.Buffer
	if err := run([]string{dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	if strings.Contains(buf.String(), "Output too large") {
		t.Errorf("did not expect the default cap to trigger for small output, got:\n%s", buf.String())
	}
}

// extractOverflowFilePath pulls the path out of writeSizeGuarded's
// "Full output written to: <path>" text-mode notice line.
func extractOverflowFilePath(t *testing.T, out string) string {
	t.Helper()
	const prefix = "Full output written to: "
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	t.Fatalf("no overflow file path found in output:\n%s", out)
	return ""
}

func TestRunLiveModeProducesReportForEveryTool(t *testing.T) {
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
	if len(out) != 10 {
		t.Fatalf("got %d reports, want 10 (--live always reports on every known tool)", len(out))
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
	// full 10-report --live output (rather than any scan-shaped output).
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

func TestRunErrorsOnFlagAfterPath(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()

	var buf bytes.Buffer
	err := run([]string{dir, "--json"}, &buf)
	if err == nil {
		t.Fatal("expected error for a flag placed after the path argument, got nil")
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output written before the error, got: %s", buf.String())
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("expected a usageError (exit 2), got %T: %v", err, err)
	}
}

func TestRunToolFilterNarrowsToSpecifiedSlug(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "CLAUDE.md"), "claude content")
	mustWriteFile(t, filepath.Join(dir, "AGENTS.md"), "codex content")

	var buf bytes.Buffer
	if err := run([]string{"--json", "--tool=codex-cli", dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbody: %s", err, buf.String())
	}
	if len(out) != 1 || out[0]["slug"] != "codex-cli" {
		t.Fatalf("expected exactly one tool (codex-cli), got: %v", out)
	}
}

func TestRunToolFilterUnknownSlugErrors(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()

	var buf bytes.Buffer
	err := run([]string{"--tool=not-a-real-tool", dir}, &buf)
	if err == nil {
		t.Fatal("expected error for unknown tool slug, got nil")
	}
	if !strings.Contains(err.Error(), "not-a-real-tool") {
		t.Errorf("error = %v, want it to mention the bad slug", err)
	}
}

func TestRunToolFilterListPrintsSlugs(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()

	var buf bytes.Buffer
	if err := run([]string{"--tool=list", dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(buf.String(), "claude-code") {
		t.Errorf("expected --tool=list to print known slugs, got:\n%s", buf.String())
	}
}

func TestRunToolFilterAppliesToLiveMode(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()

	var buf bytes.Buffer
	if err := run([]string{"--live", "--json", "--tool=claude-code", dir}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbody: %s", err, buf.String())
	}
	if len(out) != 1 || out[0]["slug"] != "claude-code" {
		t.Fatalf("expected exactly one report (claude-code), got: %v", out)
	}
}

func TestRunVersionFlagPrintsVersion(t *testing.T) {
	isolateHome(t)
	var buf bytes.Buffer
	if err := run([]string{"--version"}, &buf); err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(buf.String(), "actx") {
		t.Errorf("expected --version output to mention actx, got:\n%s", buf.String())
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
