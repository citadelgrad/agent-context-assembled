// Package compile turns scan.ToolResult data into an "effective compiled
// context" preview per tool: the contributing files assembled in that tool's
// real merge order, with separators explaining where each chunk came from and
// why it's positioned there, plus a rough token-count estimate and (only for
// tools with a documented size/byte limit) a check against that limit.
//
// This is deliberately NOT a byte-for-byte simulation of what a tool sends to
// its model. It is a best-effort, clearly-labeled approximation built purely
// from the same on-disk scan used by the default view. See docs/design.md for
// the full rationale and docs/research.md for the per-tool merge semantics
// this package encodes.
package compile

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/citadelgrad/agent-context-assembled/internal/scan"
	"github.com/citadelgrad/agent-context-assembled/internal/tools"
)

// Chunk is one contiguous piece of a tool's compiled context, corresponding to
// exactly one contributing file.
type Chunk struct {
	Path string `json:"path"`
	// Reason explains why this chunk is here and at this position, e.g.
	// "global: user-level" or "target dir: project instructions".
	Reason string `json:"reason"`
	// Condition is non-empty only for tools whose real merge semantics make a
	// chunk's application conditional rather than unconditional (currently:
	// GitHub Copilot's applyTo-scoped .instructions.md files, and any rule
	// file whose parsed frontmatter indicates conditional activation). Empty
	// means "always applied when this tool loads at all."
	Condition string `json:"condition,omitempty"`
	Content   string `json:"content"`
	// CharCount is the number of Unicode code points in Content;
	// ChunkTokenEstimate is CharCount/4, both
	// pre-computed for convenience in JSON consumers.
	CharCount          int `json:"charCount"`
	ChunkTokenEstimate int `json:"chunkTokenEstimate"`
}

// LimitCheck reports the result of comparing assembled content against a
// tool's documented size/byte limit. Only populated for tools with a sourced
// limit in docs/research.md; other tools leave Documented false and this
// struct is omitted from output.
type LimitCheck struct {
	// Documented is true only if docs/research.md cites a specific numeric
	// limit for this tool from a source (see Confidence).
	Documented bool `json:"documented"`
	// Description explains the limit in human terms, e.g. "Codex CLI
	// project_doc_max_bytes, default 32 KiB, applies to the project-doc
	// portion only".
	Description string `json:"description,omitempty"`
	// LimitValue is the numeric limit (bytes or characters, see Unit).
	LimitValue int `json:"limitValue,omitempty"`
	// Unit is "bytes" or "characters".
	Unit string `json:"unit,omitempty"`
	// Measured is the actual measured size of the content this limit applies to.
	Measured int `json:"measured,omitempty"`
	// Exceeds is true if Measured > LimitValue.
	Exceeds bool `json:"exceeds"`
	// Confidence flags sourcing strength, e.g. "confirmed" (primary source /
	// source code) or "3-of-4 secondary sources" (see docs/research.md).
	Confidence string `json:"confidence,omitempty"`
}

// ToolCompile is one tool's fully assembled compiled-context preview.
type ToolCompile struct {
	Tool           string       `json:"tool"`
	Slug           string       `json:"slug"`
	MergeModel     string       `json:"mergeModel"`
	PrecedenceNote string       `json:"precedenceNote"`
	Chunks         []Chunk      `json:"chunks"`
	Assembled      string       `json:"assembled"`
	CharCount      int          `json:"charCount"`
	TokenEstimate  int          `json:"tokenEstimate"`
	LimitChecks    []LimitCheck `json:"limitChecks,omitempty"`
	// Empty is true if there were no contributing files at all.
	Empty bool `json:"empty"`
}

// tokenEstimate is a simple, clearly-approximate heuristic: ~4 characters per
// token. This is NOT a real tokenizer count (no tool in this survey publishes
// one we could call locally without a dependency) - always labeled as an
// estimate in output.
func tokenEstimate(charCount int) int {
	return charCount / 4
}

// separator renders the "why is this chunk here" banner shown between chunks
// in the assembled text, so a human reading the compiled view can tell which
// file produced which text and why it's positioned there.
func separator(i, n int, c Chunk) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", strings.Repeat("-", 72))
	fmt.Fprintf(&b, "[%d/%d] SOURCE: %s\n", i+1, n, c.Path)
	fmt.Fprintf(&b, "       WHY: %s\n", c.Reason)
	if c.Condition != "" {
		fmt.Fprintf(&b, "       CONDITION: %s\n", c.Condition)
	}
	fmt.Fprintf(&b, "%s\n", strings.Repeat("-", 72))
	return b.String()
}

// Run builds the compiled view for every tool in a scan result set.
func Run(results []scan.ToolResult) []ToolCompile {
	out := make([]ToolCompile, 0, len(results))
	for _, r := range results {
		out = append(out, compileTool(r))
	}
	return out
}

func compileTool(r scan.ToolResult) ToolCompile {
	if r.Tool.Slug == "codex-cli" {
		r.Files = codexEffectiveFiles(r.Files)
	}
	tc := ToolCompile{
		Tool:           r.Tool.Name,
		Slug:           r.Tool.Slug,
		MergeModel:     mergeModel(r.Tool),
		PrecedenceNote: r.Tool.PrecedenceNote,
	}

	if len(r.Files) == 0 {
		tc.Empty = true
		return tc
	}

	var assembled strings.Builder
	for i, f := range r.Files {
		cond := conditionFor(r.Tool, f)
		chunk := Chunk{
			Path:      f.Path,
			Reason:    f.Note,
			Condition: cond,
			Content:   f.Content,
		}
		chunk.CharCount = utf8.RuneCountInString(chunk.Content)
		chunk.ChunkTokenEstimate = tokenEstimate(chunk.CharCount)
		tc.Chunks = append(tc.Chunks, chunk)

		assembled.WriteString(separator(i, len(r.Files), chunk))
		assembled.WriteString(chunk.Content)
		if !strings.HasSuffix(chunk.Content, "\n") {
			assembled.WriteString("\n")
		}
		assembled.WriteString("\n")
	}

	tc.Assembled = assembled.String()
	tc.CharCount = utf8.RuneCountInString(tc.Assembled)
	tc.TokenEstimate = tokenEstimate(tc.CharCount)
	tc.LimitChecks = limitChecks(r)
	return tc
}

// codexEffectiveFiles resolves local overrides without changing the raw inventory.
// The same selected files feed both assembly and the project-doc byte budget.
func codexEffectiveFiles(files []scan.MatchedFile) []scan.MatchedFile {
	overridden := make(map[string]bool)
	for _, f := range files {
		if !strings.HasPrefix(f.Note, "global:") && filepath.Base(f.Path) == "AGENTS.override.md" {
			overridden[filepath.Dir(f.Path)] = true
		}
	}
	out := make([]scan.MatchedFile, 0, len(files))
	for _, f := range files {
		if !strings.HasPrefix(f.Note, "global:") && filepath.Base(f.Path) == "AGENTS.md" && overridden[filepath.Dir(f.Path)] {
			continue
		}
		out = append(out, f)
	}
	return out
}

// mergeModel gives a one-line, tool-specific label for how chunks actually
// combine, echoed in output so the compiled view is self-explanatory without
// cross-referencing docs/research.md.
func mergeModel(t tools.Tool) string {
	switch t.Slug {
	case "claude-code":
		return "additive concatenation (root-to-target order; every chunk unconditionally applied)"
	case "codex-cli":
		return "additive concatenation (global -> project docs git-root-to-target; AGENTS.override.md replaces AGENTS.md within a directory rather than stacking with it)"
	case "github-copilot":
		return "additive, but conditional: copilot-instructions.md and AGENTS.md apply unconditionally; each .instructions.md chunk applies ONLY to files matching its applyTo glob(s), not to the whole conversation"
	case "opencode":
		return "global file + first-match-wins single nearest project file (ancestors are NOT stacked; only one local chunk ever appears)"
	case "cursor":
		return "additive merge; activation is conditional per rule type (Always/Auto Attached/Agent Requested/Manual) parsed from frontmatter where present"
	case "windsurf":
		return "additive union of global + all discovered workspace rules, deduplicated; activation is conditional per rule's trigger frontmatter (always_on/manual/model_decision/glob)"
	case "cline":
		return "additive concatenation of global + all files in .clinerules/ (or the legacy single file)"
	case "gemini-cli":
		return "additive concatenation with provenance headers (global -> ancestors -> subdirectories below target), general-to-specific"
	case "aider":
		return "N/A: Aider auto-loads no instruction file; the config file shown here is not injected as agent instructions"
	case "hermes":
		return "first-match-wins: only one local file loads per session (.hermes.md/HERMES.md > AGENTS.md > CLAUDE.md > .cursorrules), concatenated with the global SOUL.md into the system prompt; each loaded file truncated independently to context_file_max_chars, not a shared budget"
	default:
		return "additive concatenation"
	}
}

// conditionFor returns a non-empty string describing when a chunk actually
// applies, for tools whose real semantics make application conditional
// (frontmatter-gated) rather than blanket. It parses just enough of a leading
// YAML-ish frontmatter block (delimited by "---" lines) to extract the one or
// two fields each tool's docs describe - not a general YAML parser.
func conditionFor(t tools.Tool, f scan.MatchedFile) string {
	switch t.Slug {
	case "github-copilot":
		if strings.HasSuffix(f.Path, ".instructions.md") {
			if applyTo, ok := frontmatterField(f.Content, "applyTo"); ok {
				return fmt.Sprintf("applies only to files matching applyTo: %s", applyTo)
			}
			return "path-scoped (applyTo frontmatter not found/parseable; treat as scope unknown, not global)"
		}
		return ""
	case "cursor":
		if strings.HasSuffix(f.Path, ".mdc") {
			return cursorRuleCondition(f.Content)
		}
		return ""
	case "windsurf":
		if trig, ok := frontmatterField(f.Content, "trigger"); ok {
			switch trig {
			case "always_on":
				return ""
			case "manual":
				return "manual: only injected when explicitly @-mentioned by name in chat"
			case "model_decision":
				return "model_decision: description always visible; full body retrieved only if the model judges it relevant"
			case "glob":
				if globs, ok := frontmatterField(f.Content, "globs"); ok {
					return fmt.Sprintf("glob: auto-activates only when a file matching %q is open/edited", globs)
				}
				return "glob: auto-activates only when a matching file is open/edited"
			default:
				return fmt.Sprintf("trigger: %s", trig)
			}
		}
		return ""
	default:
		return ""
	}
}

func cursorRuleCondition(content string) string {
	alwaysApply, hasAlways := frontmatterField(content, "alwaysApply")
	globs, hasGlobs := frontmatterField(content, "globs")
	desc, hasDesc := frontmatterField(content, "description")
	switch {
	case hasAlways && alwaysApply == "true":
		return ""
	case hasGlobs && globs != "":
		return fmt.Sprintf("Auto Attached: applies only when a file matching %q is in context", globs)
	case hasDesc && desc != "":
		return fmt.Sprintf("Agent Requested: model reads description (%q) and decides whether to pull this in", desc)
	case hasAlways:
		return "Manual: only included via explicit @-mention in chat"
	default:
		return ""
	}
}

// frontmatterField does a minimal, line-based scan for "key: value" inside a
// leading "---"-delimited block at the very start of content. It does not
// handle multi-line YAML values, quoting edge cases, or nested structures -
// good enough for the single flat fields these tools' docs describe
// (applyTo, trigger, globs, alwaysApply, description), not a general parser.
func frontmatterField(content, key string) (string, bool) {
	lines := strings.Split(content, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return "", false
	}
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(parts[0]), key) {
			v := strings.TrimSpace(parts[1])
			v = strings.Trim(v, `"'`)
			return v, true
		}
	}
	return "", false
}

// limitChecks compares assembled content against any documented size/byte
// limit for this tool. Only tools with a sourced numeric limit in
// docs/research.md get an entry; all others are skipped entirely per project
// policy (no invented numbers).
func limitChecks(r scan.ToolResult) []LimitCheck {
	switch r.Tool.Slug {
	case "codex-cli":
		return []LimitCheck{codexLimitCheck(r)}
	case "windsurf":
		return windsurfLimitChecks(r)
	case "hermes":
		return hermesLimitChecks(r)
	default:
		return nil
	}
}

// codexProjectDocMaxBytes is Codex CLI's documented default
// project_doc_max_bytes (32 KiB), sourced in docs/research.md from
// codex-rs/core/src/config/mod.rs. This is a default, not a hard-coded
// constant in the protocol - a repo's config.toml could change it, which this
// prototype has no way to detect, so the check is clearly labeled as
// "against the documented default."
const codexProjectDocMaxBytes = 32 * 1024

func codexLimitCheck(r scan.ToolResult) LimitCheck {
	// The byte budget applies only to the project-doc portion (AGENTS.md /
	// AGENTS.override.md chain), not the global ~/.codex instructions or the
	// skills/meta sections. Sum just the local (non-global) file contents.
	measured := 0
	for _, f := range r.Files {
		if strings.HasPrefix(f.Note, "global:") {
			continue
		}
		measured += len(f.Content)
	}
	return LimitCheck{
		Documented:  true,
		Description: "Codex CLI project_doc_max_bytes default (applies to the concatenated AGENTS.md/AGENTS.override.md project-doc chain only, not global ~/.codex instructions; a repo's config.toml can override this default, which this prototype cannot detect)",
		LimitValue:  codexProjectDocMaxBytes,
		Unit:        "bytes",
		Measured:    measured,
		Exceeds:     measured > codexProjectDocMaxBytes,
		Confidence:  "confirmed in codex-rs source (config/mod.rs)",
	}
}

// Windsurf's character caps, per docs/research.md: global_rules.md 6,000 chars
// (hard cap, silent truncation beyond); each individual .windsurf/rules/*.md
// or .devin/rules/*.md file 12,000 chars, independent per-file budget. Sourced
// from 3-of-4 secondary sources agreeing (docs.windsurf.com no longer serves
// historical content post-rebrand) - flagged at "3-of-4 secondary sources"
// confidence, not "confirmed."
const (
	windsurfGlobalRulesCharCap = 6000
	windsurfPerFileRulesCap    = 12000
)

func windsurfLimitChecks(r scan.ToolResult) []LimitCheck {
	var checks []LimitCheck
	for _, f := range r.Files {
		if strings.HasPrefix(f.Note, "global:") && strings.Contains(f.Path, "global_rules.md") {
			measured := utf8.RuneCountInString(f.Content)
			checks = append(checks, LimitCheck{
				Documented:  true,
				Description: fmt.Sprintf("Windsurf global_rules.md character cap (%s)", f.Path),
				LimitValue:  windsurfGlobalRulesCharCap,
				Unit:        "characters",
				Measured:    measured,
				Exceeds:     measured > windsurfGlobalRulesCharCap,
				Confidence:  "3-of-4 secondary sources agree (no primary source available post-rebrand)",
			})
		} else if strings.HasSuffix(f.Path, ".md") && (strings.Contains(f.Path, "/.windsurf/rules/") || strings.Contains(f.Path, "/.devin/rules/")) {
			measured := utf8.RuneCountInString(f.Content)
			checks = append(checks, LimitCheck{
				Documented:  true,
				Description: fmt.Sprintf("Windsurf per-file rules character cap (%s)", f.Path),
				LimitValue:  windsurfPerFileRulesCap,
				Unit:        "characters",
				Measured:    measured,
				Exceeds:     measured > windsurfPerFileRulesCap,
				Confidence:  "3-of-4 secondary sources agree (no primary source available post-rebrand)",
			})
		}
	}
	return checks
}

// hermesContextFileMaxChars is Hermes's documented default context_file_max_chars
// (hermes-agent.nousresearch.com/docs/user-guide/configuration), applied
// independently (not a shared budget) to every file it names: SOUL.md,
// .hermes.md, AGENTS.md, CLAUDE.md, and .cursorrules. Docs confirm head/tail
// truncation applies but do not publish the exact split, so this check only
// tests against the documented default character count.
const hermesContextFileMaxChars = 20000

func hermesLimitChecks(r scan.ToolResult) []LimitCheck {
	var checks []LimitCheck
	for _, f := range r.Files {
		measured := utf8.RuneCountInString(f.Content)
		checks = append(checks, LimitCheck{
			Documented:  true,
			Description: fmt.Sprintf("Hermes context_file_max_chars default, applied independently per file (%s)", f.Path),
			LimitValue:  hermesContextFileMaxChars,
			Unit:        "characters",
			Measured:    measured,
			Exceeds:     measured > hermesContextFileMaxChars,
			Confidence:  "confirmed via official docs (user-guide/configuration); exact head/tail truncation split not published",
		})
	}
	return checks
}
