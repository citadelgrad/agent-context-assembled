// Package inspect implements best-effort "runtime introspection" — looking
// for real, on-disk (or documented-flag-driven) artifacts that reveal what a
// given AI coding agent actually loaded into context in a real session,
// rather than predicting it from the instruction files on disk (that's what
// internal/scan and internal/compile do).
//
// This is fundamentally different in character from the rest of this tool:
// scan/compile are deterministic given the filesystem; inspect depends on
// undocumented or semi-documented artifacts whose format/location can change
// across tool versions without notice. Every Report is explicit about
// confidence and never silently omits a tool — if nothing is known/findable,
// the report says so.
//
// See docs/research.md's "Runtime introspection" section per tool for the
// sourcing behind each of these mechanisms.
package inspect

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Mechanism categorizes how confident/complete a tool's runtime-introspection
// story is, so callers/renderers can treat them differently.
type Mechanism string

const (
	// MechanismContentConfirmed means we found a real on-disk artifact and
	// were able to extract actual instruction/system-prompt content from it.
	MechanismContentConfirmed Mechanism = "content-confirmed"
	// MechanismMetadataOnly means we found a real on-disk artifact (session
	// file(s)) but the format is too undocumented/fragile to reliably parse
	// for content, so we only report its existence/count/mtime.
	MechanismMetadataOnly Mechanism = "metadata-only"
	// MechanismDocumentedFlagNotRun means a real, documented mechanism exists
	// (a flag/command/env var) but it requires the tool to be re-run with
	// that option; this CLI cannot retroactively extract it from a past
	// session, so it just tells the user how to invoke it themselves.
	MechanismDocumentedFlagNotRun Mechanism = "documented-flag-not-run"
	// MechanismNone means no documented or discoverable mechanism is known at
	// all — explicitly reported, never silently omitted.
	MechanismNone Mechanism = "none"
)

// Report is one tool's runtime-introspection result.
type Report struct {
	Tool      string    `json:"tool"`
	Slug      string    `json:"slug"`
	Mechanism Mechanism `json:"mechanism"`
	// Summary is a one-line human-readable headline, always present.
	Summary string `json:"summary"`
	// Detail is a longer explanation: what was checked, what was/wasn't
	// found, and how to get more (a command to run, a flag to pass, etc).
	Detail string `json:"detail"`
	// ArtifactPath is the file or directory path examined, if any.
	ArtifactPath string `json:"artifactPath,omitempty"`
	// ArtifactCount is how many candidate session/log files were found.
	ArtifactCount int `json:"artifactCount,omitempty"`
	// LastModified is the most recent mtime among found artifacts, if any.
	LastModified time.Time `json:"lastModified,omitempty"`
	// ExtractedContent holds actual recovered instruction/system-prompt text,
	// only populated when Mechanism == MechanismContentConfirmed.
	ExtractedContent string `json:"extractedContent,omitempty"`
	// Confidence labels how reliable/sourced this finding is (e.g.
	// "confirmed: source-verified field", "best-effort: format undocumented,
	// may break across versions").
	Confidence string `json:"confidence"`
}

// Run produces a Report for every known tool, scoped to targetDir where a
// tool's artifact can be matched to a project directory. Never returns fewer
// than the full known tool list — every tool gets an explicit report.
func Run(targetDir string) []Report {
	home, _ := os.UserHomeDir()
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		absTarget = targetDir
	}

	return []Report{
		claudeCodeReport(home, absTarget),
		codexCLIReport(home, absTarget),
		openCodeReport(home),
		aiderReport(absTarget),
		geminiCLIReport(home, absTarget),
		githubCopilotReport(),
		cursorReport(home),
		windsurfReport(home),
		clineReport(home),
		hermesReport(home),
	}
}

// ---- Claude Code ----------------------------------------------------------

// encodeClaudeProjectDir mirrors Claude Code's own encoding of a project's
// absolute path into a directory name under ~/.claude/projects/: every '/' is
// replaced with '-'. Confirmed by direct testing (including a dot-prefixed
// path segment producing a double dash), not from official docs (undocumented
// implementation detail).
func encodeClaudeProjectDir(absPath string) string {
	return strings.ReplaceAll(absPath, "/", "-")
}

func claudeCodeReport(home, absTarget string) Report {
	base := Report{
		Tool: "Claude Code",
		Slug: "claude-code",
	}
	if home == "" {
		base.Mechanism = MechanismNone
		base.Summary = "Could not determine home directory; cannot locate ~/.claude/projects/."
		base.Confidence = "n/a"
		return base
	}

	projDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectDir(absTarget))
	entries, err := os.ReadDir(projDir)
	if err != nil || len(entries) == 0 {
		base.Mechanism = MechanismDocumentedFlagNotRun
		base.ArtifactPath = projDir
		base.Summary = "No Claude Code session transcripts found for this directory."
		base.Detail = "Checked " + projDir + " (Claude Code's project-directory encoding: absolute path with '/' -> '-', confirmed by testing, not officially documented). " +
			"For the actual compiled system prompt as sent to the API, Claude Code documents OTEL_LOG_RAW_API_BODIES=1 (or =file:<dir> for untruncated output), " +
			"which requires OpenTelemetry logging enabled (CLAUDE_CODE_ENABLE_TELEMETRY=1 + OTLP exporter config) and emits full request/response JSON including the system prompt " +
			"(see code.claude.com/docs/en/monitoring-usage). The /context slash command shows a token/category size breakdown of what's in context right now, " +
			"but not verbatim text (code.claude.com/docs/en/context-window: \"System prompt... You never see it.\"). No ANTHROPIC_LOG-style env var dumps it directly."
		base.Confidence = "confirmed: documented mechanisms exist but none can be retroactively read from a past session by this CLI"
		return base
	}

	var jsonlFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			jsonlFiles = append(jsonlFiles, e)
		}
	}
	if len(jsonlFiles) == 0 {
		base.Mechanism = MechanismMetadataOnly
		base.ArtifactPath = projDir
		base.Summary = fmt.Sprintf("Found session directory but no .jsonl transcripts at %s.", projDir)
		base.Confidence = "best-effort"
		return base
	}

	sort.Slice(jsonlFiles, func(i, j int) bool {
		ii, _ := jsonlFiles[i].Info()
		jj, _ := jsonlFiles[j].Info()
		if ii == nil || jj == nil {
			return false
		}
		return ii.ModTime().After(jj.ModTime())
	})
	latest := jsonlFiles[0]
	info, _ := latest.Info()
	var mtime time.Time
	if info != nil {
		mtime = info.ModTime()
	}

	base.Mechanism = MechanismMetadataOnly
	base.ArtifactPath = filepath.Join(projDir, latest.Name())
	base.ArtifactCount = len(jsonlFiles)
	base.LastModified = mtime
	base.Summary = fmt.Sprintf("Found %d Claude Code session transcript(s) for this directory; most recent modified %s.",
		len(jsonlFiles), mtime.Format(time.RFC3339))
	base.Detail = "Investigated transcript structure directly: the JSONL transcript does NOT contain the compiled system prompt/CLAUDE.md text verbatim as a " +
		"distinct field anywhere — only incidental filename mentions inside tool calls/results. So this CLI reports session existence/timing/hook-output metadata " +
		"honestly rather than claiming to show 'the real system prompt' from the transcript, which would be inaccurate. Sibling directories tool-results/ (hook stdout " +
		"capture) and subagents/ (nested Task-tool transcripts) may exist alongside the main session file at " + projDir + ". " +
		"To actually capture the compiled system prompt as sent, re-run with OTEL_LOG_RAW_API_BODIES=1 (needs OTel export configured) or use /context for a size/category breakdown (not verbatim text)."
	base.Confidence = "confirmed: transcript format personally inspected; does not contain verbatim system prompt (documented OTel mechanism is the only verbatim path)"
	return base
}

// ---- Codex CLI --------------------------------------------------------

// codexRollout mirrors just the fields we need from a Codex CLI rollout JSONL
// line. Codex rollout files interleave several distinct record shapes on one
// line each; we only care about session_meta and turn_context lines.
type codexRolloutLine struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexSessionMetaPayload struct {
	CWD              string `json:"cwd"`
	BaseInstructions *struct {
		Text string `json:"text"`
	} `json:"base_instructions"`
}

type codexTurnContextPayload struct {
	CWD              string `json:"cwd"`
	UserInstructions string `json:"user_instructions"`
}

func codexCLIReport(home, absTarget string) Report {
	base := Report{
		Tool: "Codex CLI",
		Slug: "codex-cli",
	}
	if home == "" {
		base.Mechanism = MechanismNone
		base.Summary = "Could not determine home directory; cannot locate ~/.codex/sessions/."
		base.Confidence = "n/a"
		return base
	}

	sessionsRoot := filepath.Join(home, ".codex", "sessions")
	var candidates []string
	_ = filepath.WalkDir(sessionsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best-effort walk; skip unreadable entries
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") && strings.HasPrefix(d.Name(), "rollout-") {
			candidates = append(candidates, path)
		}
		return nil
	})

	if len(candidates) == 0 {
		base.Mechanism = MechanismDocumentedFlagNotRun
		base.ArtifactPath = sessionsRoot
		base.Summary = "No Codex CLI rollout session files found."
		base.Detail = "Checked " + sessionsRoot + " (organized by date: YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl, not by project directory)."
		base.Confidence = "confirmed: directory layout personally inspected"
		return base
	}

	sort.Slice(candidates, func(i, j int) bool {
		ii, ei := os.Stat(candidates[i])
		jj, ej := os.Stat(candidates[j])
		if ei != nil || ej != nil {
			return false
		}
		return ii.ModTime().After(jj.ModTime())
	})

	// Codex rollout files aren't project-scoped by naming convention (unlike
	// Claude Code) - each file's cwd must be read from its own session_meta
	// record and matched against the target directory.
	var matched string
	var matchedMTime time.Time
	var baseInstructions, userInstructions string
	var matchCount int

	for _, path := range candidates {
		cwd, baseText, userText, ok := readCodexRollout(path)
		if !ok {
			continue
		}
		if cwd != absTarget {
			continue
		}
		matchCount++
		if matched == "" {
			matched = path
			if info, err := os.Stat(path); err == nil {
				matchedMTime = info.ModTime()
			}
			baseInstructions = baseText
			userInstructions = userText
		}
	}

	if matched == "" {
		base.Mechanism = MechanismMetadataOnly
		base.ArtifactPath = sessionsRoot
		base.ArtifactCount = len(candidates)
		if info, err := os.Stat(candidates[0]); err == nil {
			base.LastModified = info.ModTime()
		}
		base.Summary = fmt.Sprintf("Found %d Codex CLI rollout session file(s), but none recorded cwd matching %s.", len(candidates), absTarget)
		base.Detail = "Codex CLI rollout files are organized by date, not project - each file's embedded cwd (from session_meta/turn_context) was checked " +
			"and none matched the target directory. Most recent rollout overall: " + candidates[0]
		base.Confidence = "best-effort: cwd-matching heuristic"
		return base
	}

	base.Mechanism = MechanismContentConfirmed
	base.ArtifactPath = matched
	base.ArtifactCount = matchCount
	base.LastModified = matchedMTime
	base.Summary = fmt.Sprintf("Found %d Codex CLI session(s) for this directory; extracted actual compiled instructions from most recent.", matchCount)

	var content strings.Builder
	if baseInstructions != "" {
		content.WriteString("=== base_instructions (fixed persona/system prompt) ===\n")
		content.WriteString(baseInstructions)
		content.WriteString("\n\n")
	}
	if userInstructions != "" {
		content.WriteString("=== user_instructions (compiled AGENTS.md project-doc payload, as actually sent) ===\n")
		content.WriteString(userInstructions)
	}
	base.ExtractedContent = content.String()
	base.Detail = "Extracted verbatim from " + matched + ": session_meta.payload.base_instructions.text (fixed persona) and " +
		"turn_context.payload.user_instructions (compiled AGENTS.md project-doc chain, confirmed to reproduce the documented '--- project-doc ---' separator). " +
		"This is the strongest runtime-introspection artifact found across all 9 tools researched: real verbatim content, not metadata."
	base.Confidence = "confirmed: fields read and cross-validated against docs/research.md's documented assembly format"
	return base
}

func readCodexRollout(path string) (cwd, baseInstructions, userInstructions string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", "", false
	}
	defer f.Close()
	return readCodexRolloutReader(f)
}

func readCodexRolloutReader(r io.Reader) (cwd, baseInstructions, userInstructions string, ok bool) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec codexRolloutLine
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		switch rec.Type {
		case "session_meta":
			var p codexSessionMetaPayload
			if err := json.Unmarshal(rec.Payload, &p); err == nil {
				if p.CWD != "" {
					cwd = p.CWD
				}
				if p.BaseInstructions != nil {
					baseInstructions = p.BaseInstructions.Text
				}
			}
		case "turn_context":
			var p codexTurnContextPayload
			if err := json.Unmarshal(rec.Payload, &p); err == nil {
				if p.CWD != "" {
					cwd = p.CWD
				}
				if p.UserInstructions != "" {
					userInstructions = p.UserInstructions
				}
			}
		}
	}
	if scanner.Err() != nil {
		return "", "", "", false
	}
	return cwd, baseInstructions, userInstructions, cwd != ""
}

// ---- OpenCode --------------------------------------------------------

func openCodeReport(home string) Report {
	base := Report{
		Tool: "OpenCode",
		Slug: "opencode",
	}
	base.Mechanism = MechanismDocumentedFlagNotRun
	base.Summary = "OpenCode has a documented live-export mechanism, but it requires running the OpenCode CLI itself; this tool cannot invoke it retroactively."
	base.Detail = "Documented: `opencode export [sessionID]` (opencode.ai/docs/cli/) dumps a session's full data as JSON to stdout. " +
		"Source (packages/opencode/src/cli/cmd/export.ts) confirms each message's info object has a 'system' field - the actual system-prompt text as sent " +
		"for that turn - with a --sanitize flag to redact it. This is a stronger mechanism than most other tools' (a real documented command exposing the actual " +
		"runtime system prompt, not a reconstruction), but it must be invoked via the opencode binary; run `opencode export <sessionID>` yourself and pipe " +
		"through `jq '.[] .info.system'` (or similar) to see it. Session/credential data lives at ~/.local/share/opencode/auth.json (docs-confirmed); " +
		"`opencode db path` prints the local DB location."
	if home != "" {
		dbDir := filepath.Join(home, ".local", "share", "opencode")
		if info, err := os.Stat(dbDir); err == nil && info.IsDir() {
			base.ArtifactPath = dbDir
			base.Mechanism = MechanismMetadataOnly
			base.Summary = "OpenCode local data directory found; use `opencode export <sessionID>` to get the actual system prompt (not parsed by this CLI)."
		}
	}
	base.Confidence = "confirmed via docs + source (packages/opencode/src/cli/cmd/export.ts); not independently re-verified against a live install by this CLI"
	return base
}

// ---- Aider --------------------------------------------------------

func aiderReport(absTarget string) Report {
	base := Report{
		Tool: "Aider",
		Slug: "aider",
	}

	llmHistory := filepath.Join(absTarget, ".aider.llm.history")
	chatHistory := filepath.Join(absTarget, ".aider.chat.history.md")

	if info, err := os.Stat(llmHistory); err == nil && info.Mode().IsRegular() {
		content, readErr := os.ReadFile(llmHistory)
		if readErr != nil {
			goto chatHistoryFallback
		}
		base.Mechanism = MechanismContentConfirmed
		base.ArtifactPath = llmHistory
		base.LastModified = info.ModTime()
		base.Summary = "Found .aider.llm.history — raw LLM message log including the system prompt as actually sent."
		base.Detail = "Aider's --llm-history-file (env AIDER_LLM_HISTORY_FILE), disabled by default, logs every message in the exact API request list " +
			"including role=='system', with no filtering (source: base_coder.py, format_messages() in aider/utils.py). Distinct from .aider.chat.history.md, " +
			"which is a human-readable reconstructed log that does NOT include the system prompt."
		base.Confidence = "confirmed: source-verified (aider.chat/docs/config/options.html + base_coder.py/utils.py)"
		base.ExtractedContent = string(content)
		return base
	}

chatHistoryFallback:
	if info, err := os.Stat(chatHistory); err == nil && info.Mode().IsRegular() {
		base.Mechanism = MechanismMetadataOnly
		base.ArtifactPath = chatHistory
		base.LastModified = info.ModTime()
		base.Summary = "Found .aider.chat.history.md, but this is a human-readable log — it does NOT include the system prompt."
		base.Detail = "Aider writes .aider.chat.history.md by default (--chat-history-file, env AIDER_CHAT_HISTORY_FILE): reconstructed/formatted text at " +
			"discrete points (user input, AI output, commits), not the raw API message list, and does not contain the system prompt. " +
			"For the actual system prompt as sent, re-run Aider with --llm-history-file (or set AIDER_LLM_HISTORY_FILE) to get .aider.llm.history."
		base.Confidence = "confirmed: source-verified (aider/io.py append_chat_history())"
		return base
	}

	base.Mechanism = MechanismDocumentedFlagNotRun
	base.Summary = "No Aider history files found in this directory."
	base.Detail = "Checked for .aider.llm.history (raw LLM messages incl. system prompt; opt-in via --llm-history-file) and .aider.chat.history.md " +
		"(human-readable, on by default, no system prompt) at " + absTarget + ". Neither exists here — Aider may not have been run in this directory, " +
		"or --chat-history-file/--llm-history-file may point elsewhere."
	base.Confidence = "confirmed: source-verified flag/filename behavior"
	return base
}

// ---- Gemini CLI --------------------------------------------------------

// absTarget is intentionally unused: Gemini CLI's ~/.gemini/tmp/<project_hash>
// directory naming isn't documented, so there's no confirmed way to derive
// project_hash from a target path and scope the count to it. This reports
// counts aggregated across all project hashes on the machine instead.
func geminiCLIReport(home, absTarget string) Report {
	base := Report{
		Tool: "Gemini CLI",
		Slug: "gemini-cli",
	}
	base.Mechanism = MechanismDocumentedFlagNotRun
	base.Summary = "Gemini CLI's /memory show reflects live loaded memory (not a fresh disk re-read), but must be run interactively; session logs' system-prompt content is unconfirmed."
	base.Detail = "/memory show (docs/reference/commands.md, docs/cli/gemini-md.md) displays 'the current hierarchical memory that has been loaded... the exact " +
		"instructional context being provided to the model' - distinct from /memory refresh, which forces a re-scan. So show is live-cached-state-accurate for " +
		"the memory-tool payload specifically (one input among several composed into the full system prompt), not a full raw-API-request dump. " +
		"--debug/-d opens a debug console (F12) with generic logging, not a documented system-prompt dump. " +
		"Session logs (~/.gemini/tmp/<project_hash>/chats/) and checkpoints (~/.gemini/tmp/<project_hash>/checkpoints, opt-in) are documented to record prompts, " +
		"responses, tool executions, and token stats - but docs do not explicitly confirm the assembled system prompt is a recorded field (flagged as an open gap, not assumed either way)."
	base.Confidence = "confirmed: docs quoted directly for /memory show vs refresh distinction; session-log system-prompt inclusion explicitly unconfirmed"

	if home != "" {
		tmpRoot := filepath.Join(home, ".gemini", "tmp")
		if entries, err := os.ReadDir(tmpRoot); err == nil {
			var newest time.Time
			count := 0
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				chatsDir := filepath.Join(tmpRoot, e.Name(), "chats")
				chatEntries, err := os.ReadDir(chatsDir)
				if err != nil {
					continue
				}
				for _, ce := range chatEntries {
					count++
					if info, err := ce.Info(); err == nil && info.ModTime().After(newest) {
						newest = info.ModTime()
					}
				}
			}
			if count > 0 {
				base.Mechanism = MechanismMetadataOnly
				base.ArtifactPath = tmpRoot
				base.ArtifactCount = count
				base.LastModified = newest
				base.Summary = fmt.Sprintf("Found %d Gemini CLI session log file(s) under %s; system-prompt field presence unconfirmed from docs, so content not parsed.", count, tmpRoot)
			}
		}
	}
	return base
}

// ---- Tools with no confirmed local artifact (placeholders, filled once
// the corresponding research returns; each explicitly reports MechanismNone
// or best current knowledge rather than being silently omitted) ----------

func githubCopilotReport() Report {
	return Report{
		Tool:      "GitHub Copilot",
		Slug:      "github-copilot",
		Mechanism: MechanismDocumentedFlagNotRun,
		Summary:   "VS Code's Chat Debug View is documented to show the full system prompt/request payload, but must be opened manually in-session; no CLI-readable file.",
		Detail: "CONFIRMED (code.visualstudio.com/docs/copilot/troubleshooting): the Chat view '...' menu -> 'Show Chat Debug View' shows 'the raw details of each " +
			"LLM request and response, including the full system prompt, user prompt, context, and tool invocation payloads.' There is also a related 'Agent Debug " +
			"Logs' (Preview) panel. This is a real, documented, strong mechanism - but it is an interactive VS Code UI panel, not a file this CLI can read from disk. " +
			"Separately, the Output panel's 'GitHub Copilot'/'GitHub Copilot Chat' channels (Developer: Set Log Level -> Trace) are documented only for " +
			"connection/extension diagnostics, not payload content (docs.github.com). 'Chat: Export Chat...' saves conversation JSON (history only, not the " +
			"system prompt). To see the real payload yourself: open the Chat view, click '...', select 'Show Chat Debug View'.",
		Confidence: "confirmed via official VS Code docs; not automatable by this CLI (interactive UI panel, no on-disk artifact)",
	}
}

func cursorReport(home string) Report {
	base := Report{
		Tool:      "Cursor",
		Slug:      "cursor",
		Mechanism: MechanismNone,
		Summary:   "No confirmed mechanism exposing the actual instructions/system-prompt payload.",
		Detail: "No official documentation describes a way to see the raw system prompt or full assembled context. Community reports (forum.cursor.com) say " +
			"chat message text is stored in a SQLite file (state.vscdb, under VS Code-style globalStorage) but Cursor staff explicitly decline to document its " +
			"schema ('format and contents... subject to change at any time') - whether compiled system-prompt/rules content appears there is UNCONFIRMED, not " +
			"just undocumented. A community feature request for a 'Context Window Inspector' confirms this doesn't exist yet. One third-party tool intercepted " +
			"traffic via an external proxy - explicitly not an official/sanctioned method, not implemented here.",
		Confidence: "not found: no official documentation; one relevant on-disk file is known to exist but its schema is explicitly undocumented by the vendor",
	}
	if home != "" {
		// Report the state.vscdb path if present, purely as a pointer for the
		// curious - never parsed, since schema is explicitly undocumented and
		// content is unconfirmed per the research above.
		candidates := []string{
			filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb"),
			filepath.Join(home, ".config", "Cursor", "User", "globalStorage", "state.vscdb"),
		}
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && info.Mode().IsRegular() {
				base.ArtifactPath = c
				base.LastModified = info.ModTime()
				base.Mechanism = MechanismMetadataOnly
				base.Summary = "Found Cursor's state.vscdb (undocumented schema - not parsed; community-reported to hold chat text, not confirmed to hold the compiled system prompt)."
				break
			}
		}
	}
	return base
}

func windsurfReport(home string) Report {
	base := Report{
		Tool:      "Windsurf",
		Slug:      "windsurf",
		Mechanism: MechanismDocumentedFlagNotRun,
		Summary:   "Windsurf/Cascade has a documented opt-in transcript hook, but it's off by default and requires manual configuration.",
		Detail: "CONFIRMED (docs.devin.ai/desktop/cascade/hooks.md): the post_cascade_response_with_transcript hook writes full conversation to " +
			"~/.windsurf/transcripts/{trajectory_id}.jsonl, documented to include tool arguments, file contents, and 'rules that were applied' (rule " +
			"names/activation mode - not necessarily the raw compiled system-prompt text itself). This is opt-in only - not enabled by default, requires " +
			"manual hook configuration. No documented UI panel shows the final compiled/assembled system prompt (checked cascade.md, memories.md, " +
			"troubleshooting/logs.md).",
		Confidence: "confirmed via official docs.devin.ai docs; opt-in feature so absent unless the user configured it",
	}
	if home != "" {
		dir := filepath.Join(home, ".windsurf", "transcripts")
		if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
			var newest time.Time
			count := 0
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
					continue
				}
				count++
				if info, err := e.Info(); err == nil && info.ModTime().After(newest) {
					newest = info.ModTime()
				}
			}
			if count > 0 {
				base.Mechanism = MechanismMetadataOnly
				base.ArtifactPath = dir
				base.ArtifactCount = count
				base.LastModified = newest
				base.Summary = fmt.Sprintf("Found %d Windsurf transcript file(s) at %s (hook was configured); content includes tool calls/rules applied, not necessarily the raw system prompt.", count, dir)
			}
		}
	}
	return base
}

func clineReport(home string) Report {
	base := Report{
		Tool:      "Cline",
		Slug:      "cline",
		Mechanism: MechanismDocumentedFlagNotRun,
		Summary:   "Cline stores task history locally, but its own docs confirm api_conversation_history.json explicitly excludes the system prompt.",
		Detail: "Task history is stored under <VS Code globalStorageFsPath>/tasks/<taskId>/ (exact path is community-sourced for the extension ID " +
			"saoudrizwan.claude-dev, not officially doc-confirmed) including api_conversation_history.json. An OFFICIAL doc (docs.cline.bot Enterprise " +
			"Solutions -> Prompt Storage) states this file holds conversation in Anthropic MessageParam format and explicitly does NOT include the system " +
			"prompt - confirmed structurally in Cline's open source: systemPrompt is passed as a separate argument from messages. A 'Cline' Output channel " +
			"exists in source but is not documented to show the full system prompt. Cline is open source, so these claims are independently verifiable, " +
			"but the conclusion is: this specific file will NOT show you the system prompt even though it's real and readable.",
		Confidence: "confirmed via official docs.cline.bot + source-level cross-check (systemPrompt/messages are separate arguments)",
	}
	if home != "" {
		candidates := []string{
			filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "tasks"),
			filepath.Join(home, ".config", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "tasks"),
		}
		for _, dir := range candidates {
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) == 0 {
				continue
			}
			var newest time.Time
			for _, e := range entries {
				if info, err := e.Info(); err == nil && info.ModTime().After(newest) {
					newest = info.ModTime()
				}
			}
			base.Mechanism = MechanismMetadataOnly
			base.ArtifactPath = dir
			base.ArtifactCount = len(entries)
			base.LastModified = newest
			base.Summary = fmt.Sprintf("Found %d Cline task folder(s) at %s; api_conversation_history.json confirmed by Cline's own docs to exclude the system prompt.", len(entries), dir)
			break
		}
	}
	return base
}

func hermesReport(home string) Report {
	base := Report{
		Tool:      "Hermes Agent",
		Slug:      "hermes",
		Mechanism: MechanismDocumentedFlagNotRun,
		Summary:   "Hermes has a documented `hermes prompt-size` command, but it reports the prompt's fixed byte budget, not its resolved content.",
		Detail: "CONFIRMED (hermes-agent.nousresearch.com/docs/reference/cli-commands): `hermes prompt-size` \"reports the fixed prompt budget for a fresh " +
			"session -- what gets sent on every API call before any conversation content\" -- it does not print the actual resolved system-prompt or " +
			"context-file text as sent to the model. The docs also list `hermes sessions` (browse/export/prune) and `hermes logs` (view/tail/filter) " +
			"commands, implying on-disk session and log storage under the Hermes home directory, but the exact path and content format are not " +
			"source-verified here, so this prototype only checks the inferred ~/.hermes/sessions/ path and reports existence/count, not parsed content.",
		Confidence: "confirmed via official docs for the CLI command; ~/.hermes/sessions/ path and content format are an inferred, not source-verified, convention",
	}
	if home != "" {
		dir := filepath.Join(home, ".hermes", "sessions")
		if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
			var newest time.Time
			for _, e := range entries {
				if info, err := e.Info(); err == nil && info.ModTime().After(newest) {
					newest = info.ModTime()
				}
			}
			base.Mechanism = MechanismMetadataOnly
			base.ArtifactPath = dir
			base.ArtifactCount = len(entries)
			base.LastModified = newest
			base.Summary = fmt.Sprintf("Found %d Hermes session file(s)/folder(s) at %s; content format not source-verified, so only existence/count is reported.", len(entries), dir)
		}
	}
	return base
}
