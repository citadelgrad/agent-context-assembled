package compile_test

import (
	"strings"
	"testing"

	"github.com/citadelgrad/actx/internal/compile"
	"github.com/citadelgrad/actx/internal/scan"
	"github.com/citadelgrad/actx/internal/tools"
)

// toolBySlug looks up a tools.Registry entry by slug, failing the test if
// absent -- keeps every test tied to the real registry rather than a
// hand-rolled tools.Tool literal that could drift from it.
func toolBySlug(t *testing.T, slug string) tools.Tool {
	t.Helper()
	for _, tool := range tools.Registry {
		if tool.Slug == slug {
			return tool
		}
	}
	t.Fatalf("no tool with slug %q in tools.Registry", slug)
	return tools.Tool{}
}

func result(t *testing.T, slug string, files ...scan.MatchedFile) scan.ToolResult {
	return scan.ToolResult{Tool: toolBySlug(t, slug), Files: files}
}

// ---- Empty handling ---------------------------------------------------

// TestCompileEmptyToolResult verifies a tool with zero matched files produces
// Empty=true and nothing else populated.
func TestCompileEmptyToolResult(t *testing.T) {
	results := []scan.ToolResult{result(t, "claude-code")}
	out := compile.Run(results)
	if len(out) != 1 {
		t.Fatalf("got %d results, want 1", len(out))
	}
	tc := out[0]
	if !tc.Empty {
		t.Error("Empty = false, want true for a tool with zero files")
	}
	if len(tc.Chunks) != 0 {
		t.Errorf("Chunks = %+v, want empty", tc.Chunks)
	}
	if tc.Assembled != "" {
		t.Errorf("Assembled = %q, want empty", tc.Assembled)
	}
	if tc.CharCount != 0 || tc.TokenEstimate != 0 {
		t.Errorf("CharCount=%d TokenEstimate=%d, want 0/0", tc.CharCount, tc.TokenEstimate)
	}
}

// TestCompileAiderIsAlwaysEmptyNA verifies Aider -- which has no auto-loaded
// instruction file per docs/research.md -- gets the "N/A" merge model label,
// and behaves like any other zero-file tool (Empty=true) since scan.go never
// matches an "instructions" file for it.
func TestCompileAiderIsNAWithNoFiles(t *testing.T) {
	results := []scan.ToolResult{result(t, "aider")}
	out := compile.Run(results)
	tc := out[0]
	if !tc.Empty {
		t.Error("expected Aider ToolCompile.Empty = true (no auto-loaded instruction file ever matched)")
	}
	if !strings.HasPrefix(tc.MergeModel, "N/A") {
		t.Errorf("MergeModel = %q, want it to start with %q", tc.MergeModel, "N/A")
	}
	if tc.LimitChecks != nil {
		t.Errorf("LimitChecks = %+v, want nil for aider", tc.LimitChecks)
	}
}

// ---- Merge model labels -------------------------------------------------

// TestMergeModelLabelsPerTool spot-checks that every tool gets a
// slug-specific (not the generic default fallback) merge-model description,
// and that the fallback branch is unreachable given all 9 registry slugs are
// explicitly cased.
func TestMergeModelLabelsPerTool(t *testing.T) {
	wantSubstr := map[string]string{
		"claude-code":    "additive concatenation",
		"codex-cli":      "AGENTS.override.md replaces AGENTS.md",
		"github-copilot": "conditional",
		"opencode":       "first-match-wins",
		"cursor":         "conditional",
		"windsurf":       "additive union",
		"cline":          "additive concatenation",
		"gemini-cli":     "additive concatenation",
		"aider":          "N/A",
	}
	for _, tool := range tools.Registry {
		t.Run(tool.Slug, func(t *testing.T) {
			want, ok := wantSubstr[tool.Slug]
			if !ok {
				t.Fatalf("test does not know an expected merge-model substring for slug %q -- registry grew a tool not covered here", tool.Slug)
			}
			results := []scan.ToolResult{result(t, tool.Slug)}
			out := compile.Run(results)
			got := out[0].MergeModel
			if !strings.Contains(got, want) {
				t.Errorf("MergeModel for %s = %q, want substring %q", tool.Slug, got, want)
			}
			if got == "additive concatenation" && tool.Slug != "claude-code" && tool.Slug != "cline" && tool.Slug != "gemini-cli" {
				t.Errorf("MergeModel for %s fell through to the generic default fallback string; every registry slug should be explicitly cased in mergeModel()", tool.Slug)
			}
		})
	}
}

// ---- Additive concatenation model (e.g. Claude Code) --------------------

// TestCompileAdditiveConcatenationOrderAndProvenance verifies chunk order
// matches file order, each chunk's Reason mirrors the source file's Note
// (provenance label), and no Condition is ever set for an additive tool.
func TestCompileAdditiveConcatenationOrderAndProvenance(t *testing.T) {
	files := []scan.MatchedFile{
		{Path: "/home/user/.claude/CLAUDE.md", Content: "global instructions", Note: "global: user-level"},
		{Path: "/repo/CLAUDE.md", Content: "repo instructions", Note: "ancestor /repo: primary"},
		{Path: "/repo/pkg/CLAUDE.md", Content: "pkg instructions", Note: "target dir: primary"},
	}
	results := []scan.ToolResult{result(t, "claude-code", files...)}
	out := compile.Run(results)
	tc := out[0]

	if tc.Empty {
		t.Fatal("Empty = true, want false")
	}
	if len(tc.Chunks) != len(files) {
		t.Fatalf("got %d chunks, want %d", len(tc.Chunks), len(files))
	}
	for i, f := range files {
		c := tc.Chunks[i]
		if c.Path != f.Path {
			t.Errorf("Chunks[%d].Path = %q, want %q (chunk order must mirror file order)", i, c.Path, f.Path)
		}
		if c.Reason != f.Note {
			t.Errorf("Chunks[%d].Reason = %q, want %q (provenance label)", i, c.Reason, f.Note)
		}
		if c.Condition != "" {
			t.Errorf("Chunks[%d].Condition = %q, want empty for an additive-model tool", i, c.Condition)
		}
	}

	// Assembled text must contain each chunk's content in file order.
	prevIdx := -1
	for _, f := range files {
		idx := strings.Index(tc.Assembled, f.Content)
		if idx == -1 {
			t.Fatalf("assembled text missing content %q", f.Content)
		}
		if idx <= prevIdx {
			t.Errorf("content %q appears out of order in assembled text", f.Content)
		}
		prevIdx = idx
	}
}

// TestCompileChunkCharCountAndTokenEstimateArithmetic verifies
// CharCount=len(Content) exactly and ChunkTokenEstimate=CharCount/4 using
// integer division (not rounded), including a case where the remainder would
// round up if not truncated.
func TestCompileChunkCharCountAndTokenEstimateArithmetic(t *testing.T) {
	content := strings.Repeat("a", 101) // 101/4 = 25.25 -> want 25, not 26
	files := []scan.MatchedFile{{Path: "/x/CLAUDE.md", Content: content, Note: "target dir: primary"}}
	results := []scan.ToolResult{result(t, "claude-code", files...)}
	out := compile.Run(results)
	tc := out[0]

	c := tc.Chunks[0]
	if c.CharCount != 101 {
		t.Errorf("CharCount = %d, want 101", c.CharCount)
	}
	if c.ChunkTokenEstimate != 25 {
		t.Errorf("ChunkTokenEstimate = %d, want 25 (101/4 truncated, not rounded)", c.ChunkTokenEstimate)
	}

	if tc.CharCount != len(tc.Assembled) {
		t.Errorf("ToolCompile.CharCount = %d, want len(Assembled) = %d", tc.CharCount, len(tc.Assembled))
	}
	if tc.TokenEstimate != tc.CharCount/4 {
		t.Errorf("ToolCompile.TokenEstimate = %d, want CharCount/4 = %d", tc.TokenEstimate, tc.CharCount/4)
	}
}

// TestCompileSeparatorBannerFormat verifies the separator banner contains the
// documented [i/n] index, SOURCE, WHY, and (when set) CONDITION lines, framed
// by 72-dash rules.
func TestCompileSeparatorBannerFormat(t *testing.T) {
	files := []scan.MatchedFile{
		{Path: "/a/CLAUDE.md", Content: "first", Note: "ancestor /a: primary"},
		{Path: "/b/CLAUDE.md", Content: "second", Note: "target dir: primary"},
	}
	results := []scan.ToolResult{result(t, "claude-code", files...)}
	out := compile.Run(results)
	assembled := out[0].Assembled

	dashes := strings.Repeat("-", 72)
	if strings.Count(assembled, dashes) < 4 {
		t.Errorf("expected at least 4 occurrences of the 72-dash rule (2 per chunk x 2 chunks), got %d", strings.Count(assembled, dashes))
	}
	if !strings.Contains(assembled, "[1/2] SOURCE: /a/CLAUDE.md") {
		t.Errorf("assembled text missing expected banner for chunk 1; got:\n%s", assembled)
	}
	if !strings.Contains(assembled, "WHY: ancestor /a: primary") {
		t.Errorf("assembled text missing expected WHY line for chunk 1; got:\n%s", assembled)
	}
	if !strings.Contains(assembled, "[2/2] SOURCE: /b/CLAUDE.md") {
		t.Errorf("assembled text missing expected banner for chunk 2; got:\n%s", assembled)
	}
	if strings.Contains(assembled, "CONDITION:") {
		t.Errorf("did not expect a CONDITION line for an additive-model tool with no frontmatter; got:\n%s", assembled)
	}
}

// TestCompileAssembledEnsuresTrailingNewlinePerChunk verifies content without
// a trailing newline gets one appended before the blank-line chunk separator,
// while content that already ends in "\n" does not get a duplicate.
func TestCompileAssembledEnsuresTrailingNewlinePerChunk(t *testing.T) {
	files := []scan.MatchedFile{
		{Path: "/a/CLAUDE.md", Content: "no trailing newline", Note: "target dir: primary"},
	}
	results := []scan.ToolResult{result(t, "claude-code", files...)}
	out := compile.Run(results)
	assembled := out[0].Assembled
	if !strings.Contains(assembled, "no trailing newline\n\n") {
		t.Errorf("expected content without trailing newline to get one appended plus a blank line; got:\n%q", assembled)
	}

	files2 := []scan.MatchedFile{
		{Path: "/a/CLAUDE.md", Content: "has trailing newline\n", Note: "target dir: primary"},
	}
	results2 := []scan.ToolResult{result(t, "claude-code", files2...)}
	out2 := compile.Run(results2)
	assembled2 := out2[0].Assembled
	if strings.Contains(assembled2, "has trailing newline\n\n\n") {
		t.Errorf("expected exactly one blank line after content that already ends in newline (no extra blank line), got:\n%q", assembled2)
	}
	if !strings.Contains(assembled2, "has trailing newline\n\n") {
		t.Errorf("expected content+single trailing blank line; got:\n%q", assembled2)
	}
}

// ---- First-match-wins model (OpenCode) -----------------------------------

// TestCompileFirstMatchWinsSingleChunk verifies OpenCode's merge model
// produces exactly the chunks scan already decided on (compile.go trusts
// scan's file list; the "only one local file" invariant is scan's job, not
// compile's) and that no condition is ever attached.
func TestCompileFirstMatchWinsSingleChunk(t *testing.T) {
	files := []scan.MatchedFile{
		{Path: "/home/user/.config/opencode/AGENTS.md", Content: "global", Note: "global: global"},
		{Path: "/repo/AGENTS.md", Content: "nearest project file", Note: "ancestor /repo: primary"},
	}
	results := []scan.ToolResult{result(t, "opencode", files...)}
	out := compile.Run(results)
	tc := out[0]

	if len(tc.Chunks) != 2 {
		t.Fatalf("got %d chunks, want 2 (global + the single ancestor file scan already selected)", len(tc.Chunks))
	}
	for _, c := range tc.Chunks {
		if c.Condition != "" {
			t.Errorf("Chunks Condition = %q, want empty for opencode (not a conditional-activation tool)", c.Condition)
		}
	}
	if tc.LimitChecks != nil {
		t.Errorf("LimitChecks = %+v, want nil for opencode", tc.LimitChecks)
	}
}

// ---- Path-conditional model: GitHub Copilot ------------------------------

func TestCompileGithubCopilotConditionFromApplyTo(t *testing.T) {
	content := "---\napplyTo: \"**/*.go\"\n---\nGo style guide"
	files := []scan.MatchedFile{
		{Path: "/repo/.github/instructions/go.instructions.md", Content: content, Note: "target dir: path-scoped"},
	}
	results := []scan.ToolResult{result(t, "github-copilot", files...)}
	out := compile.Run(results)
	c := out[0].Chunks[0]
	if !strings.Contains(c.Condition, `applies only to files matching applyTo: **/*.go`) {
		t.Errorf("Condition = %q, want it to mention the parsed applyTo glob", c.Condition)
	}
}

func TestCompileGithubCopilotUnconditionalForRepoWideFile(t *testing.T) {
	files := []scan.MatchedFile{
		{Path: "/repo/.github/copilot-instructions.md", Content: "repo-wide", Note: "target dir: repo-wide"},
	}
	results := []scan.ToolResult{result(t, "github-copilot", files...)}
	out := compile.Run(results)
	c := out[0].Chunks[0]
	if c.Condition != "" {
		t.Errorf("Condition = %q, want empty for copilot-instructions.md (unconditional)", c.Condition)
	}
}

func TestCompileGithubCopilotMissingApplyToFrontmatter(t *testing.T) {
	files := []scan.MatchedFile{
		{Path: "/repo/.github/instructions/no-frontmatter.instructions.md", Content: "no frontmatter here", Note: "target dir: path-scoped"},
	}
	results := []scan.ToolResult{result(t, "github-copilot", files...)}
	out := compile.Run(results)
	c := out[0].Chunks[0]
	if !strings.Contains(c.Condition, "scope unknown") {
		t.Errorf("Condition = %q, want the not-found/parseable fallback message", c.Condition)
	}
}

// ---- Path-conditional model: Cursor .mdc rule types ----------------------

func TestCompileCursorRuleConditions(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string // substring, "" means expect exactly ""
	}{
		{
			name:    "alwaysApply true means Always (no condition)",
			content: "---\nalwaysApply: true\n---\nbody",
			want:    "",
		},
		{
			name:    "globs set means Auto Attached",
			content: "---\nalwaysApply: false\nglobs: \"*.go\"\n---\nbody",
			want:    "Auto Attached",
		},
		{
			name:    "description without globs means Agent Requested",
			content: "---\nalwaysApply: false\ndescription: \"Use for Go files\"\n---\nbody",
			want:    "Agent Requested",
		},
		{
			name:    "alwaysApply false with no globs/description means Manual",
			content: "---\nalwaysApply: false\n---\nbody",
			want:    "Manual",
		},
		{
			name:    "no alwaysApply key at all falls through to empty",
			content: "---\ndescription: \"\"\n---\nbody",
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := []scan.MatchedFile{
				{Path: "/repo/.cursor/rules/x.mdc", Content: tt.content, Note: "target dir: rule"},
			}
			results := []scan.ToolResult{result(t, "cursor", files...)}
			out := compile.Run(results)
			c := out[0].Chunks[0]
			if tt.want == "" {
				if c.Condition != "" {
					t.Errorf("Condition = %q, want empty", c.Condition)
				}
			} else if !strings.Contains(c.Condition, tt.want) {
				t.Errorf("Condition = %q, want substring %q", c.Condition, tt.want)
			}
		})
	}
}

// TestCompileCursorNonMdcFileNeverConditional verifies Cursor's plain
// AGENTS.md alternative (not a .mdc rule file) never gets a Condition, since
// conditionFor's cursor case only inspects .mdc-suffixed paths.
func TestCompileCursorNonMdcFileNeverConditional(t *testing.T) {
	files := []scan.MatchedFile{
		{Path: "/repo/AGENTS.md", Content: "---\nalwaysApply: false\n---\nbody", Note: "target dir: simple alternative"},
	}
	results := []scan.ToolResult{result(t, "cursor", files...)}
	out := compile.Run(results)
	c := out[0].Chunks[0]
	if c.Condition != "" {
		t.Errorf("Condition = %q, want empty for a non-.mdc cursor file regardless of frontmatter content", c.Condition)
	}
}

// ---- Path-conditional model: Windsurf trigger frontmatter ----------------

func TestCompileWindsurfTriggerConditions(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "always_on has no condition", content: "---\ntrigger: always_on\n---\nbody", want: ""},
		{name: "manual", content: "---\ntrigger: manual\n---\nbody", want: "manual:"},
		{name: "model_decision", content: "---\ntrigger: model_decision\n---\nbody", want: "model_decision:"},
		{name: "glob with globs field", content: "---\ntrigger: glob\nglobs: \"*.md\"\n---\nbody", want: `glob: auto-activates only when a file matching "*.md"`},
		{name: "glob without globs field falls back to generic glob text", content: "---\ntrigger: glob\n---\nbody", want: "glob: auto-activates only when a matching file"},
		{name: "unrecognized trigger value falls through to default case", content: "---\ntrigger: something_else\n---\nbody", want: "trigger: something_else"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := []scan.MatchedFile{
				{Path: "/repo/.windsurf/rules/x.md", Content: tt.content, Note: "target dir: rule"},
			}
			results := []scan.ToolResult{result(t, "windsurf", files...)}
			out := compile.Run(results)
			c := out[0].Chunks[0]
			if tt.want == "" {
				if c.Condition != "" {
					t.Errorf("Condition = %q, want empty", c.Condition)
				}
			} else if !strings.Contains(c.Condition, tt.want) {
				t.Errorf("Condition = %q, want substring %q", c.Condition, tt.want)
			}
		})
	}
}

func TestCompileWindsurfNoTriggerFrontmatterMeansNoCondition(t *testing.T) {
	files := []scan.MatchedFile{
		{Path: "/repo/.windsurf/rules/x.md", Content: "no frontmatter at all", Note: "target dir: rule"},
	}
	results := []scan.ToolResult{result(t, "windsurf", files...)}
	out := compile.Run(results)
	c := out[0].Chunks[0]
	if c.Condition != "" {
		t.Errorf("Condition = %q, want empty when no trigger frontmatter is present", c.Condition)
	}
}

// ---- LimitChecks: only codex-cli and windsurf ----------------------------

// TestLimitChecksOnlyForCodexAndWindsurf verifies every other tool gets a nil
// (not empty-but-non-nil) LimitChecks slice.
func TestLimitChecksOnlyForCodexAndWindsurf(t *testing.T) {
	for _, tool := range tools.Registry {
		if tool.Slug == "codex-cli" || tool.Slug == "windsurf" {
			continue
		}
		t.Run(tool.Slug, func(t *testing.T) {
			files := []scan.MatchedFile{{Path: "/x/f.md", Content: "some content", Note: "target dir: primary"}}
			results := []scan.ToolResult{result(t, tool.Slug, files...)}
			out := compile.Run(results)
			if out[0].LimitChecks != nil {
				t.Errorf("LimitChecks = %+v, want nil for %s", out[0].LimitChecks, tool.Slug)
			}
		})
	}
}

// TestCodexLimitCheckWithinLimit verifies the codex-cli LimitCheck sums only
// non-global file content and reports Exceeds=false when under the 32 KiB
// default.
func TestCodexLimitCheckWithinLimit(t *testing.T) {
	files := []scan.MatchedFile{
		{Path: "/home/user/.codex/AGENTS.md", Content: strings.Repeat("g", 100000), Note: "global: user-level"},
		{Path: "/repo/AGENTS.md", Content: "small project doc", Note: "ancestor /repo: primary"},
	}
	results := []scan.ToolResult{result(t, "codex-cli", files...)}
	out := compile.Run(results)
	tc := out[0]
	if len(tc.LimitChecks) != 1 {
		t.Fatalf("got %d LimitChecks, want 1", len(tc.LimitChecks))
	}
	lc := tc.LimitChecks[0]
	if !lc.Documented {
		t.Error("Documented = false, want true")
	}
	wantMeasured := len("small project doc")
	if lc.Measured != wantMeasured {
		t.Errorf("Measured = %d, want %d (global file must be excluded from the byte count)", lc.Measured, wantMeasured)
	}
	if lc.LimitValue != 32*1024 {
		t.Errorf("LimitValue = %d, want %d", lc.LimitValue, 32*1024)
	}
	if lc.Unit != "bytes" {
		t.Errorf("Unit = %q, want %q", lc.Unit, "bytes")
	}
	if lc.Exceeds {
		t.Error("Exceeds = true, want false (well under the 32 KiB default)")
	}
}

// TestCodexLimitCheckExceedsLimit verifies Exceeds=true when non-global
// project-doc content alone crosses the 32 KiB default.
func TestCodexLimitCheckExceedsLimit(t *testing.T) {
	big := strings.Repeat("x", 40*1024) // 40 KiB > 32 KiB default
	files := []scan.MatchedFile{
		{Path: "/repo/AGENTS.md", Content: big, Note: "ancestor /repo: primary"},
	}
	results := []scan.ToolResult{result(t, "codex-cli", files...)}
	out := compile.Run(results)
	lc := out[0].LimitChecks[0]
	if lc.Measured != 40*1024 {
		t.Errorf("Measured = %d, want %d", lc.Measured, 40*1024)
	}
	if !lc.Exceeds {
		t.Error("Exceeds = false, want true (40 KiB > 32 KiB default)")
	}
}

// TestWindsurfLimitChecksGlobalRulesCap verifies the 6,000-char global cap
// only applies to a Note-prefixed "global:" file whose path contains
// "global_rules.md", and produces separate within/exceeds cases.
func TestWindsurfLimitChecksGlobalRulesCap(t *testing.T) {
	t.Run("within cap", func(t *testing.T) {
		content := strings.Repeat("a", 5000)
		files := []scan.MatchedFile{
			{Path: "/home/user/.codeium/windsurf/memories/global_rules.md", Content: content, Note: "global: global"},
		}
		results := []scan.ToolResult{result(t, "windsurf", files...)}
		out := compile.Run(results)
		if len(out[0].LimitChecks) != 1 {
			t.Fatalf("got %d LimitChecks, want 1", len(out[0].LimitChecks))
		}
		lc := out[0].LimitChecks[0]
		if lc.Unit != "characters" || lc.LimitValue != 6000 {
			t.Errorf("LimitValue/Unit = %d/%q, want 6000/characters", lc.LimitValue, lc.Unit)
		}
		if lc.Exceeds {
			t.Error("Exceeds = true, want false (5000 < 6000)")
		}
	})
	t.Run("exceeds cap", func(t *testing.T) {
		content := strings.Repeat("a", 7000)
		files := []scan.MatchedFile{
			{Path: "/home/user/.codeium/windsurf/memories/global_rules.md", Content: content, Note: "global: global"},
		}
		results := []scan.ToolResult{result(t, "windsurf", files...)}
		out := compile.Run(results)
		lc := out[0].LimitChecks[0]
		if !lc.Exceeds {
			t.Error("Exceeds = false, want true (7000 > 6000)")
		}
	})
}

// TestWindsurfLimitChecksPerFileRulesCap verifies the 12,000-char per-file cap
// applies to .windsurf/rules/*.md and .devin/rules/*.md files, with
// within/exceeds cases.
func TestWindsurfLimitChecksPerFileRulesCap(t *testing.T) {
	for _, dir := range []string{"/repo/.windsurf/rules/x.md", "/repo/.devin/rules/y.md"} {
		t.Run(dir, func(t *testing.T) {
			content := strings.Repeat("a", 13000)
			files := []scan.MatchedFile{
				{Path: dir, Content: content, Note: "target dir: rule"},
			}
			results := []scan.ToolResult{result(t, "windsurf", files...)}
			out := compile.Run(results)
			if len(out[0].LimitChecks) != 1 {
				t.Fatalf("got %d LimitChecks, want 1", len(out[0].LimitChecks))
			}
			lc := out[0].LimitChecks[0]
			if lc.LimitValue != 12000 {
				t.Errorf("LimitValue = %d, want 12000", lc.LimitValue)
			}
			if !lc.Exceeds {
				t.Error("Exceeds = false, want true (13000 > 12000)")
			}
		})
	}
}

// TestWindsurfLimitChecksSkipsNonMatchingFiles verifies a windsurf file that
// matches neither the global_rules.md pattern nor the rules-dir pattern
// produces NO LimitCheck entry for itself (not even a within-limit one).
func TestWindsurfLimitChecksSkipsNonMatchingFiles(t *testing.T) {
	files := []scan.MatchedFile{
		{Path: "/repo/.windsurfrules", Content: "legacy single file, not capped by either pattern", Note: "target dir: legacy"},
	}
	results := []scan.ToolResult{result(t, "windsurf", files...)}
	out := compile.Run(results)
	if len(out[0].LimitChecks) != 0 {
		t.Errorf("LimitChecks = %+v, want none for a file matching neither capped pattern", out[0].LimitChecks)
	}
}

// TestWindsurfLimitChecksMultipleFilesProduceMultipleChecks verifies one
// LimitCheck per matching file, in file order, when several rule files are
// present together with the global file.
func TestWindsurfLimitChecksMultipleFilesProduceMultipleChecks(t *testing.T) {
	files := []scan.MatchedFile{
		{Path: "/home/user/.codeium/windsurf/memories/global_rules.md", Content: "g", Note: "global: global"},
		{Path: "/repo/.windsurf/rules/a.md", Content: "a", Note: "target dir: rule"},
		{Path: "/repo/.windsurf/rules/b.md", Content: "b", Note: "target dir: rule"},
	}
	results := []scan.ToolResult{result(t, "windsurf", files...)}
	out := compile.Run(results)
	if len(out[0].LimitChecks) != 3 {
		t.Fatalf("got %d LimitChecks, want 3 (one per matching file)", len(out[0].LimitChecks))
	}
}
