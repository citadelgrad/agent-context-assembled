package render_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/citadelgrad/agent-instructions-viewer/internal/compile"
	"github.com/citadelgrad/agent-instructions-viewer/internal/inspect"
	"github.com/citadelgrad/agent-instructions-viewer/internal/render"
	"github.com/citadelgrad/agent-instructions-viewer/internal/scan"
	"github.com/citadelgrad/agent-instructions-viewer/internal/tools"
)

// ---- fixtures ------------------------------------------------------------

func toolFixture(name, slug string) tools.Tool {
	return tools.Tool{
		Name:           name,
		Slug:           slug,
		PrecedenceNote: "precedence note for " + name,
		NonFileNotes:   nil,
	}
}

func resultWithFiles(name, slug string, files ...scan.MatchedFile) scan.ToolResult {
	return scan.ToolResult{Tool: toolFixture(name, slug), Files: files}
}

func chainFixture(target string) scan.Chain {
	return scan.Chain{
		Dirs:         []string{"/", target},
		GitRootIndex: -1,
	}
}

// ---- render.JSON -----------------------------------------------------

func TestJSONOmitsEmptyToolsByDefault(t *testing.T) {
	results := []scan.ToolResult{
		resultWithFiles("Claude Code", "claude-code", scan.MatchedFile{Path: "/a/CLAUDE.md", Content: "hi", Note: "note"}),
		resultWithFiles("Cursor", "cursor"), // no files
	}
	var buf bytes.Buffer
	if err := render.JSON(&buf, results, render.Options{}); err != nil {
		t.Fatalf("JSON error: %v", err)
	}

	var out []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, buf.String())
	}
	if len(out) != 1 {
		t.Fatalf("got %d tools, want 1 (empty tool should be omitted without All)", len(out))
	}
	if out[0]["slug"] != "claude-code" {
		t.Errorf("slug = %v, want claude-code", out[0]["slug"])
	}
}

func TestJSONIncludesEmptyToolsWithAll(t *testing.T) {
	results := []scan.ToolResult{
		resultWithFiles("Claude Code", "claude-code", scan.MatchedFile{Path: "/a/CLAUDE.md", Content: "hi", Note: "note"}),
		resultWithFiles("Cursor", "cursor"),
	}
	var buf bytes.Buffer
	if err := render.JSON(&buf, results, render.Options{All: true}); err != nil {
		t.Fatalf("JSON error: %v", err)
	}

	var out []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d tools, want 2 (All should include the empty one)", len(out))
	}
	files, ok := out[1]["files"].([]any)
	if !ok {
		t.Fatalf("files field wrong type: %T", out[1]["files"])
	}
	if len(files) != 0 {
		t.Errorf("cursor files = %d, want 0", len(files))
	}
}

func TestJSONFieldsRoundTripExactly(t *testing.T) {
	results := []scan.ToolResult{
		resultWithFiles("Claude Code", "claude-code", scan.MatchedFile{Path: "/a/CLAUDE.md", Content: "hello world", Note: "global"}),
	}
	var buf bytes.Buffer
	if err := render.JSON(&buf, results, render.Options{}); err != nil {
		t.Fatalf("JSON error: %v", err)
	}

	type file struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Note    string `json:"note"`
	}
	type toolOut struct {
		Tool           string `json:"tool"`
		Slug           string `json:"slug"`
		PrecedenceNote string `json:"precedenceNote"`
		Files          []file `json:"files"`
	}
	var out []toolOut
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d, want 1", len(out))
	}
	got := out[0]
	if got.Tool != "Claude Code" || got.Slug != "claude-code" || got.PrecedenceNote != "precedence note for Claude Code" {
		t.Errorf("tool-level fields mismatch: %+v", got)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "/a/CLAUDE.md" || got.Files[0].Content != "hello world" || got.Files[0].Note != "global" {
		t.Errorf("file fields mismatch: %+v", got.Files)
	}
}

// JSON output is never truncated regardless of Options.Full -- verify a
// large content blob passes through the JSON renderer whole.
func TestJSONNeverTruncatesContentRegardlessOfFullOption(t *testing.T) {
	big := strings.Repeat("x", 5000)
	results := []scan.ToolResult{
		resultWithFiles("Claude Code", "claude-code", scan.MatchedFile{Path: "/a/CLAUDE.md", Content: big, Note: "n"}),
	}
	var buf bytes.Buffer
	if err := render.JSON(&buf, results, render.Options{Full: false}); err != nil {
		t.Fatalf("JSON error: %v", err)
	}
	if !strings.Contains(buf.String(), big) {
		t.Error("JSON output truncated content even though JSON is documented to always be full")
	}
}

// ---- render.Text -------------------------------------------------------

func TestTextOmitsEmptyToolsByDefault(t *testing.T) {
	results := []scan.ToolResult{
		resultWithFiles("Claude Code", "claude-code", scan.MatchedFile{Path: "/a/CLAUDE.md", Content: "hi", Note: "note"}),
		resultWithFiles("Cursor", "cursor"),
	}
	var buf bytes.Buffer
	render.Text(&buf, results, chainFixture("/target"), render.Options{})
	out := buf.String()

	if !strings.Contains(out, "Claude Code") {
		t.Error("expected Claude Code section present")
	}
	if strings.Contains(out, "Cursor") {
		t.Error("expected Cursor (empty) section omitted without --all")
	}
}

func TestTextIncludesEmptyToolsWithAll(t *testing.T) {
	results := []scan.ToolResult{
		resultWithFiles("Cursor", "cursor"),
	}
	var buf bytes.Buffer
	render.Text(&buf, results, chainFixture("/target"), render.Options{All: true})
	out := buf.String()

	if !strings.Contains(out, "Cursor") {
		t.Error("expected Cursor section present with --all")
	}
	if !strings.Contains(out, "(no contributing files found)") {
		t.Error("expected explicit no-files message for empty tool")
	}
}

func TestTextShowsGitRootWhenPresent(t *testing.T) {
	chain := scan.Chain{Dirs: []string{"/", "/repo", "/repo/target"}, GitRootIndex: 1}
	var buf bytes.Buffer
	render.Text(&buf, nil, chain, render.Options{})
	out := buf.String()
	if !strings.Contains(out, "Detected repo root (.git): /repo\n") {
		t.Errorf("expected git root line, got:\n%s", out)
	}
}

func TestTextShowsNoGitRootMessageWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	render.Text(&buf, nil, chainFixture("/target"), render.Options{})
	out := buf.String()
	if !strings.Contains(out, "Detected repo root (.git): none found") {
		t.Errorf("expected no-git-root message, got:\n%s", out)
	}
}

func TestTextNoMatchesAnywhereShowsHint(t *testing.T) {
	results := []scan.ToolResult{
		resultWithFiles("Cursor", "cursor"),
	}
	var buf bytes.Buffer
	render.Text(&buf, results, chainFixture("/target"), render.Options{})
	out := buf.String()
	if !strings.Contains(out, "No instruction files found for any known tool at this location.") {
		t.Errorf("expected no-matches hint, got:\n%s", out)
	}
	if !strings.Contains(out, "Run with --all") {
		t.Errorf("expected --all hint, got:\n%s", out)
	}
}

func TestTextTruncatesLongContentByDefault(t *testing.T) {
	big := strings.Repeat("a", 3000)
	results := []scan.ToolResult{
		resultWithFiles("Claude Code", "claude-code", scan.MatchedFile{Path: "/a/CLAUDE.md", Content: big, Note: "n"}),
	}
	var buf bytes.Buffer
	render.Text(&buf, results, chainFixture("/target"), render.Options{})
	out := buf.String()
	if !strings.Contains(out, "truncated, use --full") {
		t.Error("expected truncation marker for content over previewLimit (2000 chars)")
	}
	if strings.Count(out, "a") >= 3000 {
		t.Error("expected content to actually be cut short")
	}
}

func TestTextDoesNotTruncateWithFullOption(t *testing.T) {
	big := strings.Repeat("a", 3000)
	results := []scan.ToolResult{
		resultWithFiles("Claude Code", "claude-code", scan.MatchedFile{Path: "/a/CLAUDE.md", Content: big, Note: "n"}),
	}
	var buf bytes.Buffer
	render.Text(&buf, results, chainFixture("/target"), render.Options{Full: true})
	out := buf.String()
	if strings.Contains(out, "truncated") {
		t.Error("expected no truncation marker with Full:true")
	}
	if !strings.Contains(out, big) {
		t.Error("expected full untruncated content with Full:true")
	}
}

func TestTextShowsEmptyFileMarker(t *testing.T) {
	results := []scan.ToolResult{
		resultWithFiles("Claude Code", "claude-code", scan.MatchedFile{Path: "/a/CLAUDE.md", Content: "   \n  ", Note: "n"}),
	}
	var buf bytes.Buffer
	render.Text(&buf, results, chainFixture("/target"), render.Options{})
	out := buf.String()
	if !strings.Contains(out, "(empty file)") {
		t.Errorf("expected empty-file marker for whitespace-only content, got:\n%s", out)
	}
}

func TestTextShowsNonFileNotes(t *testing.T) {
	tool := toolFixture("Cursor", "cursor")
	tool.NonFileNotes = []string{"personal settings live in app DB"}
	results := []scan.ToolResult{{Tool: tool}}
	var buf bytes.Buffer
	render.Text(&buf, results, chainFixture("/target"), render.Options{All: true})
	out := buf.String()
	if !strings.Contains(out, "(not scanned: personal settings live in app DB)") {
		t.Errorf("expected non-file note rendered, got:\n%s", out)
	}
}

func TestTextFileOrderAndIndexLabelsMatchInputOrder(t *testing.T) {
	results := []scan.ToolResult{
		resultWithFiles("Claude Code", "claude-code",
			scan.MatchedFile{Path: "/a/CLAUDE.md", Content: "AAA", Note: "first"},
			scan.MatchedFile{Path: "/b/CLAUDE.md", Content: "BBB", Note: "second"},
		),
	}
	var buf bytes.Buffer
	render.Text(&buf, results, chainFixture("/target"), render.Options{})
	out := buf.String()
	if !strings.Contains(out, "[1/2] /a/CLAUDE.md") {
		t.Error("expected [1/2] label for first file")
	}
	if !strings.Contains(out, "[2/2] /b/CLAUDE.md") {
		t.Error("expected [2/2] label for second file")
	}
	if strings.Index(out, "AAA") > strings.Index(out, "BBB") {
		t.Error("expected file content in input order")
	}
}

// ---- render.CompileJSON ------------------------------------------------

func compileFixture(tool, slug string, empty bool) compile.ToolCompile {
	return compile.ToolCompile{
		Tool:       tool,
		Slug:       slug,
		MergeModel: "additive concatenation",
		Empty:      empty,
	}
}

func TestCompileJSONOmitsEmptyByDefault(t *testing.T) {
	results := []compile.ToolCompile{
		compileFixture("Claude Code", "claude-code", false),
		compileFixture("Aider", "aider", true),
	}
	var buf bytes.Buffer
	if err := render.CompileJSON(&buf, results, render.Options{}); err != nil {
		t.Fatalf("CompileJSON error: %v", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d, want 1 (empty omitted)", len(out))
	}
}

func TestCompileJSONIncludesEmptyWithAll(t *testing.T) {
	results := []compile.ToolCompile{
		compileFixture("Claude Code", "claude-code", false),
		compileFixture("Aider", "aider", true),
	}
	var buf bytes.Buffer
	if err := render.CompileJSON(&buf, results, render.Options{All: true}); err != nil {
		t.Fatalf("CompileJSON error: %v", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d, want 2 (All includes empty)", len(out))
	}
}

// ---- render.CompileText ------------------------------------------------

func TestCompileTextOmitsEmptyByDefault(t *testing.T) {
	results := []compile.ToolCompile{
		compileFixture("Claude Code", "claude-code", false),
		compileFixture("Aider", "aider", true),
	}
	var buf bytes.Buffer
	render.CompileText(&buf, results, chainFixture("/target"), render.Options{})
	out := buf.String()
	if !strings.Contains(out, "## Claude Code") {
		t.Error("expected Claude Code section")
	}
	if strings.Contains(out, "## Aider") {
		t.Error("expected Aider (empty) section omitted without --all")
	}
}

func TestCompileTextIncludesEmptyWithAllAndShowsNothingToCompile(t *testing.T) {
	results := []compile.ToolCompile{
		compileFixture("Aider", "aider", true),
	}
	var buf bytes.Buffer
	render.CompileText(&buf, results, chainFixture("/target"), render.Options{All: true})
	out := buf.String()
	if !strings.Contains(out, "## Aider") {
		t.Error("expected Aider section with --all")
	}
	if !strings.Contains(out, "(no contributing files -- nothing to compile)") {
		t.Errorf("expected empty-compile message, got:\n%s", out)
	}
}

func TestCompileTextShowsMergeModelAndPrecedenceNote(t *testing.T) {
	r := compileFixture("Claude Code", "claude-code", false)
	r.PrecedenceNote = "global then ancestors then target"
	r.Assembled = "content"
	results := []compile.ToolCompile{r}
	var buf bytes.Buffer
	render.CompileText(&buf, results, chainFixture("/target"), render.Options{})
	out := buf.String()
	if !strings.Contains(out, "Merge model: additive concatenation") {
		t.Error("expected merge model line")
	}
	if !strings.Contains(out, "global then ancestors then target") {
		t.Error("expected precedence note line")
	}
}

func TestCompileTextShowsSizeAndLimitChecks(t *testing.T) {
	r := compileFixture("Codex CLI", "codex-cli", false)
	r.Assembled = "hello"
	r.CharCount = 5
	r.TokenEstimate = 1
	r.LimitChecks = []compile.LimitCheck{
		{Documented: true, Description: "32 KiB cap", LimitValue: 32 * 1024, Unit: "bytes", Measured: 100, Exceeds: false, Confidence: "confirmed"},
		{Documented: true, Description: "6000 char cap", LimitValue: 6000, Unit: "chars", Measured: 7000, Exceeds: true, Confidence: "confirmed"},
	}
	results := []compile.ToolCompile{r}
	var buf bytes.Buffer
	render.CompileText(&buf, results, chainFixture("/target"), render.Options{})
	out := buf.String()

	if !strings.Contains(out, "Estimated size: 5 chars (~1 tokens, len/4 estimate)") {
		t.Errorf("expected size line, got:\n%s", out)
	}
	if !strings.Contains(out, "Limit check [within documented limit]: 100/32768 bytes -- 32 KiB cap (confidence: confirmed)") {
		t.Errorf("expected within-limit line, got:\n%s", out)
	}
	if !strings.Contains(out, "Limit check [EXCEEDS documented limit]: 7000/6000 chars -- 6000 char cap (confidence: confirmed)") {
		t.Errorf("expected exceeds-limit line, got:\n%s", out)
	}
}

func TestCompileTextTruncationScalesWithChunkCount(t *testing.T) {
	// previewLimit is 2000 and scales by len(Chunks); with 2 chunks the
	// truncation threshold is 4000 chars, so a 3000-char single-assembled
	// blob under a 2-chunk ToolCompile must NOT be truncated (it's under the
	// scaled budget), while the same content under a 1-chunk ToolCompile
	// (scaled budget 2000) MUST be truncated.
	content := strings.Repeat("b", 3000)

	rTwoChunks := compileFixture("Claude Code", "claude-code", false)
	rTwoChunks.Assembled = content
	rTwoChunks.Chunks = []compile.Chunk{{Path: "/a"}, {Path: "/b"}}

	var buf1 bytes.Buffer
	render.CompileText(&buf1, []compile.ToolCompile{rTwoChunks}, chainFixture("/target"), render.Options{})
	if strings.Contains(buf1.String(), "truncated") {
		t.Error("2-chunk ToolCompile: 3000 chars should fit under scaled budget of 4000, expected no truncation")
	}

	rOneChunk := compileFixture("Claude Code", "claude-code", false)
	rOneChunk.Assembled = content
	rOneChunk.Chunks = []compile.Chunk{{Path: "/a"}}

	var buf2 bytes.Buffer
	render.CompileText(&buf2, []compile.ToolCompile{rOneChunk}, chainFixture("/target"), render.Options{})
	if !strings.Contains(buf2.String(), "truncated") {
		t.Error("1-chunk ToolCompile: 3000 chars should exceed scaled budget of 2000, expected truncation")
	}
}

func TestCompileTextFullOptionDisablesTruncation(t *testing.T) {
	content := strings.Repeat("b", 10000)
	r := compileFixture("Claude Code", "claude-code", false)
	r.Assembled = content
	r.Chunks = []compile.Chunk{{Path: "/a"}}

	var buf bytes.Buffer
	render.CompileText(&buf, []compile.ToolCompile{r}, chainFixture("/target"), render.Options{Full: true})
	out := buf.String()
	if strings.Contains(out, "truncated") {
		t.Error("expected no truncation with Full:true regardless of size")
	}
	if !strings.Contains(out, content) {
		t.Error("expected full content present with Full:true")
	}
}

func TestCompileTextNoMatchesAnywhereShowsHint(t *testing.T) {
	results := []compile.ToolCompile{compileFixture("Aider", "aider", true)}
	var buf bytes.Buffer
	render.CompileText(&buf, results, chainFixture("/target"), render.Options{})
	out := buf.String()
	if !strings.Contains(out, "No instruction files found for any known tool at this location.") {
		t.Errorf("expected no-matches hint, got:\n%s", out)
	}
}

// ---- render.InspectJSON -------------------------------------------------

func TestInspectJSONRoundTrip(t *testing.T) {
	reports := []inspect.Report{
		{Tool: "Claude Code", Slug: "claude-code", Mechanism: inspect.MechanismMetadataOnly, Summary: "found something", Confidence: "best-effort"},
	}
	var buf bytes.Buffer
	if err := render.InspectJSON(&buf, reports); err != nil {
		t.Fatalf("InspectJSON error: %v", err)
	}
	var out []inspect.Report
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 || out[0].Tool != "Claude Code" || out[0].Mechanism != inspect.MechanismMetadataOnly {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestInspectJSONAlwaysIncludesAllReportsNoFiltering(t *testing.T) {
	// Unlike JSON/CompileJSON, InspectJSON has no All-filtering option --
	// every report passed in must appear, since inspect must never silently
	// omit a tool (per package inspect's own doc comment).
	reports := []inspect.Report{
		{Tool: "A", Slug: "a", Mechanism: inspect.MechanismNone, Summary: "nothing", Confidence: "n/a"},
		{Tool: "B", Slug: "b", Mechanism: inspect.MechanismNone, Summary: "nothing", Confidence: "n/a"},
	}
	var buf bytes.Buffer
	if err := render.InspectJSON(&buf, reports); err != nil {
		t.Fatalf("InspectJSON error: %v", err)
	}
	var out []inspect.Report
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("got %d reports, want 2 (no filtering option exists)", len(out))
	}
}

// ---- render.InspectText -------------------------------------------------

func TestInspectTextShowsToolAndMechanism(t *testing.T) {
	reports := []inspect.Report{
		{Tool: "Claude Code", Slug: "claude-code", Mechanism: inspect.MechanismMetadataOnly, Summary: "found 2 sessions", Confidence: "best-effort"},
	}
	var buf bytes.Buffer
	render.InspectText(&buf, reports, false)
	out := buf.String()
	if !strings.Contains(out, "## Claude Code [metadata-only]") {
		t.Errorf("expected tool/mechanism header, got:\n%s", out)
	}
	if !strings.Contains(out, "found 2 sessions") {
		t.Error("expected summary line")
	}
	if !strings.Contains(out, "confidence: best-effort") {
		t.Error("expected confidence line")
	}
}

func TestInspectTextOmitsOptionalFieldsWhenEmpty(t *testing.T) {
	reports := []inspect.Report{
		{Tool: "GitHub Copilot", Slug: "github-copilot", Mechanism: inspect.MechanismDocumentedFlagNotRun, Summary: "s", Confidence: "c"},
	}
	var buf bytes.Buffer
	render.InspectText(&buf, reports, false)
	out := buf.String()
	if strings.Contains(out, "artifact:") {
		t.Error("expected no artifact line when ArtifactPath is empty")
	}
	if strings.Contains(out, "count:") {
		t.Error("expected no count line when ArtifactCount is 0")
	}
	if strings.Contains(out, "last modified:") {
		t.Error("expected no last-modified line when LastModified is zero")
	}
}

func TestInspectTextShowsArtifactCountAndLastModifiedWhenPresent(t *testing.T) {
	mtime := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	reports := []inspect.Report{
		{
			Tool: "Codex CLI", Slug: "codex-cli", Mechanism: inspect.MechanismMetadataOnly,
			Summary: "s", Confidence: "c", ArtifactPath: "/path", ArtifactCount: 3, LastModified: mtime,
		},
	}
	var buf bytes.Buffer
	render.InspectText(&buf, reports, false)
	out := buf.String()
	if !strings.Contains(out, "artifact: /path") {
		t.Error("expected artifact path line")
	}
	if !strings.Contains(out, "count: 3") {
		t.Error("expected count line")
	}
	if !strings.Contains(out, "2026-01-15T10:30:00Z") {
		t.Errorf("expected formatted last-modified line, got:\n%s", out)
	}
}

func TestInspectTextTruncatesExtractedContentByDefault(t *testing.T) {
	big := strings.Repeat("c", 3000)
	reports := []inspect.Report{
		{Tool: "Aider", Slug: "aider", Mechanism: inspect.MechanismContentConfirmed, Summary: "s", Confidence: "c", ExtractedContent: big},
	}
	var buf bytes.Buffer
	render.InspectText(&buf, reports, false)
	out := buf.String()
	if !strings.Contains(out, "--- extracted content ---") {
		t.Error("expected extracted-content header")
	}
	if !strings.Contains(out, "truncated, use --full") {
		t.Error("expected truncation marker for long extracted content")
	}
}

func TestInspectTextFullOptionShowsCompleteExtractedContent(t *testing.T) {
	big := strings.Repeat("c", 3000)
	reports := []inspect.Report{
		{Tool: "Aider", Slug: "aider", Mechanism: inspect.MechanismContentConfirmed, Summary: "s", Confidence: "c", ExtractedContent: big},
	}
	var buf bytes.Buffer
	render.InspectText(&buf, reports, true)
	out := buf.String()
	if strings.Contains(out, "truncated") {
		t.Error("expected no truncation marker with full=true")
	}
	if !strings.Contains(out, big) {
		t.Error("expected full extracted content with full=true")
	}
}

func TestInspectTextOmitsExtractedContentSectionWhenEmpty(t *testing.T) {
	reports := []inspect.Report{
		{Tool: "Cursor", Slug: "cursor", Mechanism: inspect.MechanismNone, Summary: "s", Confidence: "c"},
	}
	var buf bytes.Buffer
	render.InspectText(&buf, reports, false)
	out := buf.String()
	if strings.Contains(out, "--- extracted content ---") {
		t.Error("expected no extracted-content section when ExtractedContent is empty")
	}
}

func TestInspectTextAlwaysListsEveryReportPassedIn(t *testing.T) {
	reports := []inspect.Report{
		{Tool: "A", Slug: "a", Mechanism: inspect.MechanismNone, Summary: "s1", Confidence: "c"},
		{Tool: "B", Slug: "b", Mechanism: inspect.MechanismNone, Summary: "s2", Confidence: "c"},
		{Tool: "C", Slug: "c", Mechanism: inspect.MechanismNone, Summary: "s3", Confidence: "c"},
	}
	var buf bytes.Buffer
	render.InspectText(&buf, reports, false)
	out := buf.String()
	for _, name := range []string{"## A", "## B", "## C"} {
		if !strings.Contains(out, name) {
			t.Errorf("expected %q present (inspect must never silently omit a tool)", name)
		}
	}
}
