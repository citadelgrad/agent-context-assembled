package inspect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/citadelgrad/agent-context-assembled/internal/scan"
)

// Tests in this file are written as package inspect (in-package, not
// inspect_test) specifically so the unexported per-tool worker functions
// (claudeCodeReport, codexCLIReport, readCodexRollout, etc.) can be exercised
// directly against fabricated t.TempDir() fixtures, never touching whatever
// happens to be on the real developer machine's actual ~/.claude, ~/.codex,
// ~/.gemini, ~/.windsurf, or VS Code globalStorage directories. Every test
// that supplies a "home" either passes a t.TempDir() value explicitly as the
// home parameter, or (for Run() itself) isolates $HOME via t.Setenv.

// ---- helpers ---------------------------------------------------------

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

// ---- encodeClaudeProjectDir --------------------------------------------

func TestEncodeClaudeProjectDirReplacesSlashesWithDashes(t *testing.T) {
	got := encodeClaudeProjectDir("/Users/scott/projects/foo")
	want := "-Users-scott-projects-foo"
	if got != want {
		t.Errorf("encodeClaudeProjectDir = %q, want %q", got, want)
	}
}

func TestEncodeClaudeProjectDirDotPrefixedSegmentProducesDoubleDash(t *testing.T) {
	// A path segment starting with '.' (e.g. a hidden dir component) becomes
	// "-." after the leading '/' is replaced, i.e. two dashes appear
	// wherever a '/' is immediately followed by content that itself starts
	// with '.'; document via a concrete case matching the source comment's
	// claim ("a dot-prefixed path segment producing a double dash").
	got := encodeClaudeProjectDir("/Users/scott/.hidden/foo")
	want := "-Users-scott-.hidden-foo"
	if got != want {
		t.Errorf("encodeClaudeProjectDir = %q, want %q", got, want)
	}
}

// ---- claudeCodeReport ---------------------------------------------------

func TestClaudeCodeReportEmptyHomeMeansNone(t *testing.T) {
	r := claudeCodeReport("", "/some/target")
	if r.Mechanism != MechanismNone {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismNone)
	}
	if r.Slug != "claude-code" {
		t.Errorf("Slug = %q, want claude-code", r.Slug)
	}
	if r.Confidence == "" {
		t.Error("Confidence should always be set")
	}
}

func TestClaudeCodeReportNoProjectDirMeansDocumentedFlagNotRun(t *testing.T) {
	home := t.TempDir()
	target := "/Volumes/some/target"
	r := claudeCodeReport(home, target)
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
	wantPath := filepath.Join(home, ".claude", "projects", encodeClaudeProjectDir(target))
	if r.ArtifactPath != wantPath {
		t.Errorf("ArtifactPath = %q, want %q", r.ArtifactPath, wantPath)
	}
}

func TestClaudeCodeReportProjectDirExistsButEmptyMeansDocumentedFlagNotRun(t *testing.T) {
	home := t.TempDir()
	target := "/Volumes/some/target"
	projDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectDir(target))
	mustMkdirAll(t, projDir)

	r := claudeCodeReport(home, target)
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v (empty dir should behave like no dir)", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
}

func TestClaudeCodeReportNonJSONLFilesMeansMetadataOnlyNoTranscripts(t *testing.T) {
	home := t.TempDir()
	target := "/Volumes/some/target"
	projDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectDir(target))
	mustWriteFile(t, filepath.Join(projDir, "notes.txt"), "not a transcript")

	r := claudeCodeReport(home, target)
	if r.Mechanism != MechanismMetadataOnly {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismMetadataOnly)
	}
	if r.ArtifactCount != 0 {
		t.Errorf("ArtifactCount = %d, want 0 (only non-.jsonl file present)", r.ArtifactCount)
	}
}

func TestClaudeCodeReportFindsLatestJSONLByModTime(t *testing.T) {
	home := t.TempDir()
	target := "/Volumes/some/target"
	projDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectDir(target))

	older := filepath.Join(projDir, "older.jsonl")
	newer := filepath.Join(projDir, "newer.jsonl")
	mustWriteFile(t, older, `{"type":"summary"}`)
	oldTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, newer, `{"type":"summary"}`)
	newTime := time.Now()
	if err := os.Chtimes(newer, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	r := claudeCodeReport(home, target)
	if r.Mechanism != MechanismMetadataOnly {
		t.Fatalf("Mechanism = %v, want %v", r.Mechanism, MechanismMetadataOnly)
	}
	if r.ArtifactCount != 2 {
		t.Errorf("ArtifactCount = %d, want 2", r.ArtifactCount)
	}
	if r.ArtifactPath != newer {
		t.Errorf("ArtifactPath = %q, want %q (most recently modified)", r.ArtifactPath, newer)
	}
	if r.ExtractedContent != "" {
		t.Error("Claude Code report never populates ExtractedContent (format not parsed for content, per source comment)")
	}
	if r.LastModified.IsZero() {
		t.Error("LastModified should be set to the latest file's mtime")
	}
}

// ---- codexCLIReport / readCodexRollout ----------------------------------

func TestCodexCLIReportEmptyHomeMeansNone(t *testing.T) {
	r := codexCLIReport("", "/some/target")
	if r.Mechanism != MechanismNone {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismNone)
	}
}

func TestCodexCLIReportNoSessionsDirMeansDocumentedFlagNotRun(t *testing.T) {
	home := t.TempDir()
	r := codexCLIReport(home, "/some/target")
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
	wantPath := filepath.Join(home, ".codex", "sessions")
	if r.ArtifactPath != wantPath {
		t.Errorf("ArtifactPath = %q, want %q", r.ArtifactPath, wantPath)
	}
}

func TestCodexCLIReportNoMatchingCWDMeansMetadataOnly(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "myproject")
	sessionsRoot := filepath.Join(home, ".codex", "sessions", "2026", "01", "01")

	line := `{"type":"session_meta","payload":{"cwd":"/somewhere/else"}}`
	mustWriteFile(t, filepath.Join(sessionsRoot, "rollout-20260101-abc.jsonl"), line+"\n")

	r := codexCLIReport(home, target)
	if r.Mechanism != MechanismMetadataOnly {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismMetadataOnly)
	}
	if r.ArtifactCount != 1 {
		t.Errorf("ArtifactCount = %d, want 1", r.ArtifactCount)
	}
	if r.ExtractedContent != "" {
		t.Error("no cwd match: ExtractedContent should be empty")
	}
}

func TestCodexCLIReportMatchingCWDMeansContentConfirmed(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "myproject")
	sessionsRoot := filepath.Join(home, ".codex", "sessions", "2026", "01", "01")

	line1 := `{"type":"session_meta","payload":{"cwd":"` + target + `","base_instructions":{"text":"You are a coding agent."}}}`
	line2 := `{"type":"turn_context","payload":{"cwd":"` + target + `","user_instructions":"--- project-doc ---\nSome AGENTS.md content"}}`
	content := line1 + "\n" + line2 + "\n"
	path := filepath.Join(sessionsRoot, "rollout-20260101-abc.jsonl")
	mustWriteFile(t, path, content)

	r := codexCLIReport(home, target)
	if r.Mechanism != MechanismContentConfirmed {
		t.Fatalf("Mechanism = %v, want %v", r.Mechanism, MechanismContentConfirmed)
	}
	if r.ArtifactPath != path {
		t.Errorf("ArtifactPath = %q, want %q", r.ArtifactPath, path)
	}
	if !strings.Contains(r.ExtractedContent, "You are a coding agent.") {
		t.Errorf("ExtractedContent missing base_instructions text: %q", r.ExtractedContent)
	}
	if !strings.Contains(r.ExtractedContent, "Some AGENTS.md content") {
		t.Errorf("ExtractedContent missing user_instructions text: %q", r.ExtractedContent)
	}
	if !strings.Contains(r.ExtractedContent, "base_instructions") || !strings.Contains(r.ExtractedContent, "user_instructions") {
		t.Errorf("ExtractedContent should label both sections: %q", r.ExtractedContent)
	}
}

func TestCodexCLIReportPicksMostRecentMatchingSession(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "myproject")
	sessionsRoot := filepath.Join(home, ".codex", "sessions", "2026", "01", "01")

	oldPath := filepath.Join(sessionsRoot, "rollout-old.jsonl")
	newPath := filepath.Join(sessionsRoot, "rollout-new.jsonl")
	mustWriteFile(t, oldPath, `{"type":"session_meta","payload":{"cwd":"`+target+`","base_instructions":{"text":"OLD"}}}`+"\n")
	mustWriteFile(t, newPath, `{"type":"session_meta","payload":{"cwd":"`+target+`","base_instructions":{"text":"NEW"}}}`+"\n")

	oldTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	newTime := time.Now()
	if err := os.Chtimes(newPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	r := codexCLIReport(home, target)
	if r.Mechanism != MechanismContentConfirmed {
		t.Fatalf("Mechanism = %v, want %v", r.Mechanism, MechanismContentConfirmed)
	}
	if r.ArtifactPath != newPath {
		t.Errorf("ArtifactPath = %q, want %q (most recent match)", r.ArtifactPath, newPath)
	}
	if !strings.Contains(r.ExtractedContent, "NEW") {
		t.Errorf("expected content from most recent session, got %q", r.ExtractedContent)
	}
	if r.ArtifactCount != 2 {
		t.Errorf("ArtifactCount = %d, want 2 (both sessions matched target cwd)", r.ArtifactCount)
	}
}

func TestCodexCLIReportIgnoresNonRolloutPrefixedFiles(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "myproject")
	sessionsRoot := filepath.Join(home, ".codex", "sessions", "2026", "01", "01")
	// Doesn't start with "rollout-" -- must be ignored per WalkDir filter.
	mustWriteFile(t, filepath.Join(sessionsRoot, "notes-abc.jsonl"), `{"type":"session_meta","payload":{"cwd":"`+target+`"}}`+"\n")

	r := codexCLIReport(home, target)
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v (non-rollout-prefixed file should be ignored entirely)", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
}

func TestReadCodexRolloutTurnContextOverridesCWD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-x.jsonl")
	content := `{"type":"session_meta","payload":{"cwd":"/first"}}` + "\n" +
		`{"type":"turn_context","payload":{"cwd":"/second","user_instructions":"hi"}}` + "\n"
	mustWriteFile(t, path, content)

	cwd, base, user, ok := readCodexRollout(path)
	if !ok {
		t.Fatal("readCodexRollout ok = false, want true")
	}
	if cwd != "/second" {
		t.Errorf("cwd = %q, want %q (later turn_context should win -- last non-empty cwd wins)", cwd, "/second")
	}
	if base != "" {
		t.Errorf("base = %q, want empty", base)
	}
	if user != "hi" {
		t.Errorf("user = %q, want %q", user, "hi")
	}
}

func TestReadCodexRolloutEmptyCWDDoesNotOverridePreviousCWD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-x.jsonl")
	// turn_context line has no "cwd" field at all (empty string), so the
	// earlier session_meta cwd should be preserved, not blanked out.
	content := `{"type":"session_meta","payload":{"cwd":"/first"}}` + "\n" +
		`{"type":"turn_context","payload":{"user_instructions":"hi"}}` + "\n"
	mustWriteFile(t, path, content)

	cwd, _, user, ok := readCodexRollout(path)
	if !ok {
		t.Fatal("readCodexRollout ok = false, want true")
	}
	if cwd != "/first" {
		t.Errorf("cwd = %q, want %q (empty cwd in later record must not clear earlier value)", cwd, "/first")
	}
	if user != "hi" {
		t.Errorf("user = %q, want %q", user, "hi")
	}
}

func TestReadCodexRolloutSkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-x.jsonl")
	content := "not json at all\n" +
		`{"type":"session_meta","payload":{"cwd":"/ok"}}` + "\n" +
		"\n" // blank line should also be skipped without error
	mustWriteFile(t, path, content)

	cwd, _, _, ok := readCodexRollout(path)
	if !ok {
		t.Fatal("readCodexRollout ok = false, want true (valid line should still be found)")
	}
	if cwd != "/ok" {
		t.Errorf("cwd = %q, want /ok", cwd)
	}
}

func TestReadCodexRolloutNoCWDMeansNotOK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-x.jsonl")
	mustWriteFile(t, path, `{"type":"session_meta","payload":{}}`+"\n")

	_, _, _, ok := readCodexRollout(path)
	if ok {
		t.Error("ok = true, want false when no record ever sets cwd")
	}
}

func TestReadCodexRolloutMissingFileReturnsNotOK(t *testing.T) {
	_, _, _, ok := readCodexRollout(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if ok {
		t.Error("ok = true, want false for a nonexistent file")
	}
}

func TestReadCodexRolloutScannerErrorRejectsPartialState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-over-limit.jsonl")
	valid := `{"type":"session_meta","payload":{"cwd":"/partial"}}` + "\n"
	overLimit := strings.Repeat("x", 16*1024*1024+1) + "\n"
	mustWriteFile(t, path, valid+overLimit)

	cwd, base, user, ok := readCodexRollout(path)
	if ok || cwd != "" || base != "" || user != "" {
		t.Fatalf("scanner error returned partial state cwd=%q base=%q user=%q ok=%v", cwd, base, user, ok)
	}
}

// Session metadata is the current fixed-persona snapshot (docs/research.md,
// Runtime introspection), not a last-nonempty merge. An empty replacement is
// not evidence of the earlier persona. This replaces the old preservation oracle.
func TestReadCodexRolloutEmptyBaseClearsPreviousBase(t *testing.T) {
	content := strings.Join([]string{
		rolloutRecord("session_meta", map[string]any{
			"cwd": "/project", "base_instructions": map[string]any{"text": "keep this"},
		}),
		rolloutRecord("session_meta", map[string]any{
			"cwd": "/project", "base_instructions": map[string]any{"text": ""},
		}),
	}, "\n")
	_, base, _, ok := readCodexRolloutReader(strings.NewReader(content))
	if !ok || base != "" {
		t.Fatalf("base=%q ok=%v, want empty current base", base, ok)
	}
}

// ---- openCodeReport ------------------------------------------------------

func TestOpenCodeReportNoHomeInfoStillDocumentedFlagNotRun(t *testing.T) {
	r := openCodeReport("")
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
	if r.ArtifactPath != "" {
		t.Errorf("ArtifactPath = %q, want empty", r.ArtifactPath)
	}
}

func TestOpenCodeReportNoDataDirMeansDocumentedFlagNotRun(t *testing.T) {
	home := t.TempDir()
	r := openCodeReport(home)
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
}

func TestOpenCodeReportDataDirPresentMeansMetadataOnly(t *testing.T) {
	home := t.TempDir()
	dbDir := filepath.Join(home, ".local", "share", "opencode")
	mustMkdirAll(t, dbDir)

	r := openCodeReport(home)
	if r.Mechanism != MechanismMetadataOnly {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismMetadataOnly)
	}
	if r.ArtifactPath != dbDir {
		t.Errorf("ArtifactPath = %q, want %q", r.ArtifactPath, dbDir)
	}
}

// ---- aiderReport -----------------------------------------------------

func TestAiderReportNoFilesMeansDocumentedFlagNotRun(t *testing.T) {
	target := t.TempDir()
	r := aiderReport(target)
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
}

func TestAiderReportChatHistoryOnlyMeansMetadataOnly(t *testing.T) {
	target := t.TempDir()
	mustWriteFile(t, filepath.Join(target, ".aider.chat.history.md"), "# chat log\nuser: hi\n")

	r := aiderReport(target)
	if r.Mechanism != MechanismMetadataOnly {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismMetadataOnly)
	}
	if r.ExtractedContent != "" {
		t.Error("chat history alone should never populate ExtractedContent (no system prompt in it)")
	}
}

func TestAiderReportLLMHistoryMeansContentConfirmedAndTakesPriority(t *testing.T) {
	target := t.TempDir()
	// Write both files: llm history must take priority over chat history.
	mustWriteFile(t, filepath.Join(target, ".aider.chat.history.md"), "# chat log\n")
	llmContent := "SYSTEM: you are aider.\nUSER: hello\n"
	mustWriteFile(t, filepath.Join(target, ".aider.llm.history"), llmContent)

	r := aiderReport(target)
	if r.Mechanism != MechanismContentConfirmed {
		t.Fatalf("Mechanism = %v, want %v", r.Mechanism, MechanismContentConfirmed)
	}
	if r.ExtractedContent != llmContent {
		t.Errorf("ExtractedContent = %q, want %q", r.ExtractedContent, llmContent)
	}
	if !strings.HasSuffix(r.ArtifactPath, ".aider.llm.history") {
		t.Errorf("ArtifactPath = %q, want it to point at .aider.llm.history (priority over chat history)", r.ArtifactPath)
	}
}

func TestAiderReportRequiresReadableRegularArtifacts(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, path string)
		mechanism Mechanism
		content   string
	}{
		{name: "absent", setup: func(t *testing.T, path string) {}, mechanism: MechanismDocumentedFlagNotRun},
		{name: "regular", setup: func(t *testing.T, path string) { mustWriteFile(t, path, "content") }, mechanism: MechanismContentConfirmed, content: "content"},
		// An empty artifact proves only existence, not instruction content
		// (MechanismContentConfirmed's contract and docs/research.md).
		{name: "empty regular", setup: func(t *testing.T, path string) { mustWriteFile(t, path, "") }, mechanism: MechanismMetadataOnly},
		{name: "directory", setup: func(t *testing.T, path string) { mustMkdirAll(t, path) }, mechanism: MechanismDocumentedFlagNotRun},
		// Preserve ordinary relative links to regular files within the project.
		// The old outside-project absolute-link fixture bypassed project scope;
		// escaping links now have their own rejection regression test.
		{name: "symlink to in-project file", setup: func(t *testing.T, path string) {
			target := filepath.Join(filepath.Dir(path), "target")
			mustWriteFile(t, target, "linked")
			mustMkdirAll(t, filepath.Dir(path))
			if err := os.Symlink("target", path); err != nil {
				t.Fatal(err)
			}
		}, mechanism: MechanismContentConfirmed, content: "linked"},
		{name: "symlink to directory", setup: func(t *testing.T, path string) {
			target := t.TempDir()
			mustMkdirAll(t, filepath.Dir(path))
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}, mechanism: MechanismDocumentedFlagNotRun},
		{name: "broken symlink", setup: func(t *testing.T, path string) {
			mustMkdirAll(t, filepath.Dir(path))
			if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), path); err != nil {
				t.Fatal(err)
			}
		}, mechanism: MechanismDocumentedFlagNotRun},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			path := filepath.Join(target, ".aider.llm.history")
			tt.setup(t, path)
			r := aiderReport(target)
			if r.Mechanism != tt.mechanism || r.ExtractedContent != tt.content {
				t.Fatalf("report mechanism/content = %v/%q, want %v/%q", r.Mechanism, r.ExtractedContent, tt.mechanism, tt.content)
			}
		})
	}
}

// ---- geminiCLIReport -----------------------------------------------------

func TestGeminiCLIReportNoHomeDataMeansDocumentedFlagNotRun(t *testing.T) {
	home := t.TempDir()
	r := geminiCLIReport(home, "/some/target")
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
}

func TestGeminiCLIReportEmptyHomeMeansDocumentedFlagNotRun(t *testing.T) {
	// Unlike claudeCodeReport/codexCLIReport, geminiCLIReport has no explicit
	// "home == ''" special case -- filepath.Join/os.ReadDir simply fail
	// gracefully and it falls through to the default DocumentedFlagNotRun.
	r := geminiCLIReport("", "/some/target")
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
}

func TestGeminiCLIReportSessionLogsPresentMeansMetadataOnly(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "somehash123", "chats")
	mustWriteFile(t, filepath.Join(chatsDir, "session1.json"), `{}`)
	mustWriteFile(t, filepath.Join(chatsDir, "session2.json"), `{}`)

	r := geminiCLIReport(home, "/some/target")
	if r.Mechanism != MechanismMetadataOnly {
		t.Fatalf("Mechanism = %v, want %v", r.Mechanism, MechanismMetadataOnly)
	}
	if r.ArtifactCount != 2 {
		t.Errorf("ArtifactCount = %d, want 2", r.ArtifactCount)
	}
	wantPath := filepath.Join(home, ".gemini", "tmp")
	if r.ArtifactPath != wantPath {
		t.Errorf("ArtifactPath = %q, want %q", r.ArtifactPath, wantPath)
	}
}

func TestGeminiCLIReportProjectHashDirWithoutChatsSubdirIsSkipped(t *testing.T) {
	home := t.TempDir()
	// A project-hash dir exists but has no "chats" subdirectory -- should be
	// skipped (os.ReadDir on chatsDir errors, loop continues) without panicking.
	mustMkdirAll(t, filepath.Join(home, ".gemini", "tmp", "somehash123"))

	r := geminiCLIReport(home, "/some/target")
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v (no chats subdir anywhere)", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
}

// geminiCLIReport receives absTarget but never reads it: unlike Codex CLI's
// cwd-matching, Gemini CLI's ~/.gemini/tmp/<project_hash> naming isn't
// documented, so there's no confirmed way to derive project_hash from a
// target path and scope the count to it -- session logs are intentionally
// counted globally across all project hashes instead (see the doc comment
// on geminiCLIReport). This test pins that behavior down directly: two calls
// with different absTarget values (including a garbage one) must produce
// byte-identical reports given the same home fixture.
func TestGeminiCLIReportAbsTargetParameterIsUnusedByImplementation(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "somehash123", "chats")
	mustWriteFile(t, filepath.Join(chatsDir, "session1.json"), `{}`)

	r1 := geminiCLIReport(home, "/some/target/that/exists")
	r2 := geminiCLIReport(home, "/completely/different/nonexistent/garbage/path")

	r1.LastModified = time.Time{}
	r2.LastModified = time.Time{}
	b1, _ := json.Marshal(r1)
	b2, _ := json.Marshal(r2)
	if string(b1) != string(b2) {
		t.Errorf("expected identical reports regardless of absTarget (parameter appears unused), got:\n%s\nvs\n%s", b1, b2)
	}
}

// ---- githubCopilotReport ---------------------------------------------

func TestGithubCopilotReportIsStaticDocumentedFlagNotRun(t *testing.T) {
	r := githubCopilotReport()
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
	if r.Slug != "github-copilot" {
		t.Errorf("Slug = %q, want github-copilot", r.Slug)
	}
	if r.ArtifactPath != "" {
		t.Errorf("ArtifactPath = %q, want empty (no on-disk artifact, purely a UI panel)", r.ArtifactPath)
	}
}

// ---- cursorReport ------------------------------------------------------

func TestCursorReportNoHomeMeansNone(t *testing.T) {
	r := cursorReport("")
	if r.Mechanism != MechanismNone {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismNone)
	}
}

func TestCursorReportNoStateDBMeansNone(t *testing.T) {
	home := t.TempDir()
	r := cursorReport(home)
	if r.Mechanism != MechanismNone {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismNone)
	}
	if r.ArtifactPath != "" {
		t.Errorf("ArtifactPath = %q, want empty", r.ArtifactPath)
	}
}

func TestCursorReportStateDBPresentMeansMetadataOnly(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	mustWriteFile(t, path, "fake sqlite content")

	r := cursorReport(home)
	if r.Mechanism != MechanismMetadataOnly {
		t.Fatalf("Mechanism = %v, want %v", r.Mechanism, MechanismMetadataOnly)
	}
	if r.ArtifactPath != path {
		t.Errorf("ArtifactPath = %q, want %q", r.ArtifactPath, path)
	}
}

func TestCursorReportRejectsDirectoryStateDBAndUsesNextCandidate(t *testing.T) {
	home := t.TempDir()
	first := filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	second := filepath.Join(home, ".config", "Cursor", "User", "globalStorage", "state.vscdb")
	mustMkdirAll(t, first)
	mustWriteFile(t, second, "sqlite")

	r := cursorReport(home)
	if r.Mechanism != MechanismMetadataOnly || r.ArtifactPath != second {
		t.Fatalf("report = %+v, want second regular candidate %q", r, second)
	}
}

func TestCursorReportChecksXDGPathWhenMacPathAbsent(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "Cursor", "User", "globalStorage", "state.vscdb")
	mustWriteFile(t, path, "fake sqlite content")

	r := cursorReport(home)
	if r.Mechanism != MechanismMetadataOnly {
		t.Fatalf("Mechanism = %v, want %v", r.Mechanism, MechanismMetadataOnly)
	}
	if r.ArtifactPath != path {
		t.Errorf("ArtifactPath = %q, want %q (XDG-style path)", r.ArtifactPath, path)
	}
}

// ---- windsurfReport ------------------------------------------------------

func TestWindsurfReportNoHomeMeansDocumentedFlagNotRun(t *testing.T) {
	r := windsurfReport("")
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
}

func TestWindsurfReportNoTranscriptsDirMeansDocumentedFlagNotRun(t *testing.T) {
	home := t.TempDir()
	r := windsurfReport(home)
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
}

func TestWindsurfReportTranscriptsPresentMeansMetadataOnly(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".windsurf", "transcripts")
	mustWriteFile(t, filepath.Join(dir, "traj1.jsonl"), "{}\n")
	mustWriteFile(t, filepath.Join(dir, "traj2.jsonl"), "{}\n")
	// A non-.jsonl file in the same dir should not be counted.
	mustWriteFile(t, filepath.Join(dir, "readme.txt"), "not a transcript")

	r := windsurfReport(home)
	if r.Mechanism != MechanismMetadataOnly {
		t.Fatalf("Mechanism = %v, want %v", r.Mechanism, MechanismMetadataOnly)
	}
	if r.ArtifactCount != 2 {
		t.Errorf("ArtifactCount = %d, want 2 (only .jsonl files counted)", r.ArtifactCount)
	}
}

func TestWindsurfReportEmptyTranscriptsDirMeansDocumentedFlagNotRun(t *testing.T) {
	home := t.TempDir()
	mustMkdirAll(t, filepath.Join(home, ".windsurf", "transcripts"))

	r := windsurfReport(home)
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v (empty dir should behave like absent)", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
}

// ---- clineReport -------------------------------------------------------

func TestClineReportNoHomeMeansDocumentedFlagNotRun(t *testing.T) {
	r := clineReport("")
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
}

func TestClineReportNoTasksDirMeansDocumentedFlagNotRun(t *testing.T) {
	home := t.TempDir()
	r := clineReport(home)
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
}

func TestClineReportTasksPresentMeansMetadataOnly(t *testing.T) {
	home := t.TempDir()
	tasksDir := filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "tasks")
	mustWriteFile(t, filepath.Join(tasksDir, "task1", "api_conversation_history.json"), `[]`)

	r := clineReport(home)
	if r.Mechanism != MechanismMetadataOnly {
		t.Fatalf("Mechanism = %v, want %v", r.Mechanism, MechanismMetadataOnly)
	}
	if r.ArtifactPath != tasksDir {
		t.Errorf("ArtifactPath = %q, want %q", r.ArtifactPath, tasksDir)
	}
	if r.ArtifactCount != 1 {
		t.Errorf("ArtifactCount = %d, want 1", r.ArtifactCount)
	}
}

func TestClineReportChecksXDGPathWhenMacPathAbsent(t *testing.T) {
	home := t.TempDir()
	tasksDir := filepath.Join(home, ".config", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "tasks")
	mustWriteFile(t, filepath.Join(tasksDir, "task1", "api_conversation_history.json"), `[]`)

	r := clineReport(home)
	if r.Mechanism != MechanismMetadataOnly {
		t.Fatalf("Mechanism = %v, want %v", r.Mechanism, MechanismMetadataOnly)
	}
	if r.ArtifactPath != tasksDir {
		t.Errorf("ArtifactPath = %q, want %q (XDG-style path)", r.ArtifactPath, tasksDir)
	}
}

func TestClineReportEmptyTasksDirMeansDocumentedFlagNotRun(t *testing.T) {
	home := t.TempDir()
	mustMkdirAll(t, filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "tasks"))

	r := clineReport(home)
	if r.Mechanism != MechanismDocumentedFlagNotRun {
		t.Errorf("Mechanism = %v, want %v (empty tasks dir should behave like absent)", r.Mechanism, MechanismDocumentedFlagNotRun)
	}
}

// ---- hermesReport ----------------------------------------------------------

// TestHermesReportRealScanPlusLiveSessionsMeansMetadataOnly verifies
// hermesReport reports MechanismMetadataOnly with the session entry count,
// using a fabricated but real ~/.hermes/sessions/ directory alongside a REAL
// scan.Result produced by actually scanning a target directory that contains
// .hermes.md, per actx-3l6.
//
// Note: hermesReport's own signature is `hermesReport(home string) Report`
// -- it does not take a scan.Result parameter (or any "live mode" flag).
// This is intentional per this package's doc comment above: inspect is
// deliberately decoupled from scan/compile's filesystem-prediction, since
// its job is introspecting real runtime artifacts instead. The scan.Result
// is produced here purely to faithfully exercise the Gherkin scenario's
// "given a real scan.Result produced by scanning a target directory
// containing .hermes.md" clause; hermesReport itself is then called
// directly with the isolated HOME, which is the actual "live mode" input it
// accepts.
func TestHermesReportRealScanPlusLiveSessionsMeansMetadataOnly(t *testing.T) {
	fakeHome := t.TempDir()
	target := t.TempDir()
	mustWriteFile(t, filepath.Join(target, ".hermes.md"), "hermes native content")

	// Produce a real scan.Result to fulfil the "given a real scan.Result
	// produced by scanning a target directory containing .hermes.md" clause.
	results, _, err := scan.Run(target, scan.Options{})
	if err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	var sawHermesMatch bool
	for _, r := range results {
		if r.Tool.Slug == "hermes" && len(r.Files) > 0 {
			sawHermesMatch = true
		}
	}
	if !sawHermesMatch {
		t.Fatal("test setup invalid: real scan of target dir did not find the .hermes.md file for the hermes tool")
	}

	sessionsDir := filepath.Join(fakeHome, ".hermes", "sessions")
	mustWriteFile(t, filepath.Join(sessionsDir, "session-1.jsonl"), `{"session":"one"}`)
	mustWriteFile(t, filepath.Join(sessionsDir, "session-2.jsonl"), `{"session":"two"}`)

	r := hermesReport(fakeHome)
	if r.Mechanism != MechanismMetadataOnly {
		t.Fatalf("Mechanism = %v, want %v", r.Mechanism, MechanismMetadataOnly)
	}
	if r.ArtifactCount != 2 {
		t.Errorf("ArtifactCount = %d, want 2 (session entry count)", r.ArtifactCount)
	}
	if r.ArtifactPath != sessionsDir {
		t.Errorf("ArtifactPath = %q, want %q", r.ArtifactPath, sessionsDir)
	}
}

// ---- Run() ---------------------------------------------------------------

// TestRunReturnsExactlyTenReportsHermetically fabricates a fully isolated
// $HOME so that Run() (which internally calls os.UserHomeDir()) never
// touches the real developer machine's actual ~/.claude, ~/.codex, etc.
// directories. It only asserts structural invariants (count, non-empty
// fields, slug set) -- never anything about what's really on this machine.
func TestRunReturnsExactlyTenReportsHermetically(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	target := t.TempDir()
	reports := Run(target)

	if len(reports) != 10 {
		t.Fatalf("Run returned %d reports, want 10", len(reports))
	}

	wantSlugs := []string{
		"claude-code", "codex-cli", "opencode", "aider", "gemini-cli",
		"github-copilot", "cursor", "windsurf", "cline", "hermes",
	}
	gotSlugs := make([]string, len(reports))
	for i, r := range reports {
		gotSlugs[i] = r.Slug
		if r.Tool == "" {
			t.Errorf("report[%d]: Tool is empty", i)
		}
		if r.Summary == "" {
			t.Errorf("report[%d] (%s): Summary is empty", i, r.Slug)
		}
		if r.Confidence == "" {
			t.Errorf("report[%d] (%s): Confidence is empty", i, r.Slug)
		}
		if r.Mechanism == "" {
			t.Errorf("report[%d] (%s): Mechanism is empty", i, r.Slug)
		}
	}
	for i, want := range wantSlugs {
		if i >= len(gotSlugs) || gotSlugs[i] != want {
			t.Errorf("report[%d] slug = %q, want %q (Run's fixed ordering)", i, gotSlugs[i], want)
		}
	}
}

// TestRunWithFreshHomeAndTargetFindsNothing confirms that against a totally
// empty, fabricated $HOME and target directory, every report resolves to a
// "nothing found" mechanism (None or DocumentedFlagNotRun) -- never
// MetadataOnly or ContentConfirmed, since no artifacts exist anywhere in
// this hermetic fixture. This guards against any tool accidentally matching
// on real ambient machine state (a regression of the leak this codebase
// must avoid).
func TestRunWithFreshHomeAndTargetFindsNothing(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	target := t.TempDir()

	for _, r := range Run(target) {
		if r.Mechanism == MechanismMetadataOnly || r.Mechanism == MechanismContentConfirmed {
			t.Errorf("%s: Mechanism = %v in a fully empty hermetic fixture; want None or DocumentedFlagNotRun (possible ambient-machine leak)", r.Slug, r.Mechanism)
		}
		if r.ArtifactCount != 0 {
			t.Errorf("%s: ArtifactCount = %d, want 0", r.Slug, r.ArtifactCount)
		}
	}
}

// TestRunPicksUpFabricatedCodexSessionEndToEnd exercises Run() end-to-end
// (not calling codexCLIReport directly) to confirm the home/target plumbing
// from Run into codexCLIReport works, using a fabricated session that
// matches the target directory exactly.
func TestRunPicksUpFabricatedCodexSessionEndToEnd(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	target := t.TempDir()

	absTarget, err := filepath.Abs(target)
	if err != nil {
		t.Fatal(err)
	}
	sessionsRoot := filepath.Join(fakeHome, ".codex", "sessions", "2026", "01", "01")
	mustWriteFile(t, filepath.Join(sessionsRoot, "rollout-x.jsonl"),
		`{"type":"session_meta","payload":{"cwd":"`+absTarget+`","base_instructions":{"text":"hi"}}}`+"\n")

	reports := Run(target)
	var codex Report
	found := false
	for _, r := range reports {
		if r.Slug == "codex-cli" {
			codex = r
			found = true
		}
	}
	if !found {
		t.Fatal("no codex-cli report found")
	}
	if codex.Mechanism != MechanismContentConfirmed {
		t.Errorf("Mechanism = %v, want %v", codex.Mechanism, MechanismContentConfirmed)
	}
}
