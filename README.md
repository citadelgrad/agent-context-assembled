# agent-instructions-viewer

Shows the **full compiled set** of AI coding-agent instruction files that actually apply at a
given directory — across every major AI coding agent (Claude Code, Codex CLI, GitHub Copilot,
OpenCode, Cursor, Windsurf, Cline, Aider, Gemini CLI) — not just one tool.

Every AI coding agent has its own file(s) it reads for persistent instructions (`CLAUDE.md`,
`AGENTS.md`, `.cursor/rules/*.mdc`, `.clinerules/`, ...), its own rule for how far up the
directory tree it walks, its own global/user-level config location, and its own precedence order
when multiple files apply. This tool answers, for a real directory on disk: **"if I ran each of
these agents here, what instructions would actually be loaded, in what order, from where?"**

See [docs/research.md](docs/research.md) for the sourced research behind each tool's behavior,
and [docs/design.md](docs/design.md) for the compiled-view algorithm design (including diagrams).

## What "compiled" means, and why it differs per tool

"Compiled" here means: the full, ordered, real-world result of a tool's own discovery and merge
rules — not a generic "list every file named X up the tree" scan. Each tool gets that treatment
because their actual rules genuinely differ:

- **How far up they walk.** Claude Code and Gemini CLI walk all the way to the filesystem root.
  Codex CLI, OpenCode, and Windsurf stop at the git repository root. Cursor, Cline, and Aider
  (in the sense of an instructions file) are effectively single-directory / config-file tools.
- **Whether they walk down.** Some tools also pick up files in subdirectories below the target
  (e.g. Gemini CLI, Windsurf) — either eagerly (this tool actually scans for them) or lazily
  (loaded on-demand as the agent reads files there; this tool documents that behavior via a note
  rather than pretending it already happened).
- **How multiple files combine.** Most tools concatenate additively, least-specific first (global,
  then each ancestor root-to-target, then the target itself). OpenCode is different: only the
  *nearest* matching ancestor file wins — ancestors are not stacked.
  Copilot layers org -> repo-wide -> path-scoped -> personal.
- **Where the global config lives.** Each tool has its own user-level file/dir
  (`~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, `~/.gemini/GEMINI.md`,
  `~/.config/opencode/AGENTS.md`, `~/Documents/Cline/Rules`, `~/.codeium/windsurf/memories/global_rules.md`, ...).

Because of this, the tool does not apply one generic rule to all 9 tools. Each tool is modeled as
its own spec (scope, walk direction, precedence, global path) in
[`internal/tools/tools.go`](internal/tools/tools.go), driven directly by the sourced findings in
`docs/research.md`. The output for each tool lists its contributing files in that tool's own real
application order (earlier = applied first / least specific; later = applied last / wins on
conflict) — not alphabetical order, not discovery order.

Note that the walk itself goes all the way to the **filesystem root**, not just the repo root —
so you can see, e.g., an org-wide `CLAUDE.md` living one or more directories above a repo. Each
tool's own section, though, only shows the files that tool would truly have loaded given its real
scope (a repo-root-limited tool won't show you that org-wide file, even though the walk saw it).

## Usage

```
agent-instructions-viewer [path] [flags]
```

`path` defaults to the current directory.

Flags:

- `--json` — output structured JSON instead of text (see below for shape).
- `--all` — include every supported tool, even ones with zero contributing files. By default,
  only tools with at least one matching file are shown.
- `--full` — show full file content in text output instead of a size-limited preview (JSON output
  is always full content regardless of this flag).
- `--compile` — instead of just listing matched files, actually assemble them into an "effective
  compiled context" preview per tool, in that tool's own real merge order. See
  [Compiled-context preview](#compiled-context-preview---compile) below.
- `--live` — best-effort runtime introspection: look for real on-disk session artifacts (or name
  a documented interactive mechanism) showing what a tool *actually* loaded in a real session, as
  opposed to what it would predict from files on disk. Ignores the directory-walk/scan flags
  (`--all` has no effect); still takes `[path]` to scope tools whose artifacts can be matched to a
  project directory. See [Runtime introspection](#runtime-introspection---live) below.

### Examples

Show the compiled view for the current directory:

```
agent-instructions-viewer
```

Show it for another path, listing every supported tool (including ones with nothing found):

```
agent-instructions-viewer --all /path/to/some/repo/subdir
```

Get machine-readable output, e.g. to pipe into `jq`:

```
agent-instructions-viewer --json . | jq '.[].tool'
```

Show the assembled effective context per tool, with token estimates:

```
agent-instructions-viewer --compile
```

Same, as JSON:

```
agent-instructions-viewer --compile --json . | jq '.[] | {tool, tokenEstimate}'
```

Check what a tool actually loaded in a real session (best-effort):

```
agent-instructions-viewer --live
```

### JSON shape

`--json` prints an array with one object per tool (only tools with matches, unless `--all`):

```json
[
  {
    "tool": "Claude Code",
    "slug": "claude-code",
    "precedenceNote": "Additive concatenation: managed -> user (~/.claude/CLAUDE.md) -> ...",
    "files": [
      { "path": "/Users/you/.claude/CLAUDE.md", "content": "...", "note": "global: user-level" },
      { "path": "/path/to/repo/CLAUDE.md", "content": "...", "note": "target dir: project instructions" }
    ]
  }
]
```

`files` is always in the tool's real application order. `content` is always the complete file —
JSON output is never truncated, even when the text renderer would preview-truncate the same file.

## Compiled-context preview (`--compile`)

`--compile` goes one step further than the default mode: instead of just listing which files
contribute, it actually assembles them into the "effective compiled context" a tool would see —
in that tool's own real merge order — with clear separators showing which file each chunk came
from and why it's included. It also reports a rough token-count estimate and, for the two tools
with a sourced documented limit, whether the assembled content would exceed it.

Merge semantics differ per tool and are applied accordingly (see
[docs/design.md](docs/design.md#step-6----compile-assemble-the-effective-compiled-context) for
the full breakdown):

- **Additive concatenation** (Claude Code, Codex CLI, Cline, Gemini CLI) — every matched file is
  appended in precedence order.
- **Conditional / path-scoped** (GitHub Copilot's `applyTo`-scoped `.instructions.md`, Cursor's
  rule-type frontmatter, Windsurf's `trigger`/`globs` frontmatter) — chunks are included with an
  explicit `condition` (e.g. `applyTo: **/*.go`) rather than assumed to always apply.
- **First-match-wins** (OpenCode) — only the nearest matching file is assembled; skipped
  ancestors are noted, not silently dropped.
- **N/A** (Aider) — nothing is auto-loaded, so `--compile` reports the tool as empty with an
  explanation rather than fabricating content.

Token counts are a simple `len(content)/4` heuristic, always labeled as an **estimate**, never
presented as an exact tokenizer count. Documented-size-limit checks are only run for tools where
`docs/research.md` cites a sourced numeric limit: Codex CLI's `project_doc_max_bytes` (32 KiB
default) and Windsurf's `global_rules.md` (6,000 chars) / per-file rules (12,000 chars) caps.
Every other tool skips this check rather than inventing a number.

`--compile --json` emits one object per tool with `mergeModel`, `chunks` (each with `path`,
`reason`, optional `condition`, `content`, and per-chunk char/token counts), the full `assembled`
string, `charCount`/`tokenEstimate`, and `limitChecks` where applicable.

## Runtime introspection (`--live`)

Both modes above *predict* what a tool would load, from files on disk. `--live` asks a different
question: can we see what a tool **actually** loaded, in a real past or current session — the
ground truth, not a prediction? This is inherently best-effort and varies wildly by tool, since
it depends on undocumented or semi-documented internals that can change across tool versions.

Every one of the 9 tools always gets a report — the CLI never silently omits a tool, even when
nothing is discoverable. Each report is classified into one of four mechanisms:

| Mechanism | Meaning |
|---|---|
| `content-confirmed` | Real extracted content — a genuine artifact was found and parsed |
| `metadata-only` | An artifact exists, but its format is too undocumented/fragile to parse reliably; only path/count/last-modified are reported |
| `documented-flag-not-run` | A real, docs-confirmed mechanism exists but is interactive or must be enabled before a session runs — nothing to retroactively read |
| `none` | Nothing discoverable at all |

Highlights (full sourced detail in
[docs/research.md](docs/research.md#runtime-introspection)):

- **Codex CLI** — strongest result: session rollout JSONL files under `~/.codex/sessions/` contain
  `base_instructions` (fixed system prompt) and `user_instructions` (compiled AGENTS.md payload)
  verbatim. `--live` scopes this to the target directory via each rollout's embedded `cwd`.
- **Aider** — if `--llm-history-file` was used, `.aider.llm.history` contains the real system
  prompt verbatim (confirmed in source); the default `.aider.chat.history.md` does not.
- **Claude Code** — session transcripts exist and are located, but do not contain the verbatim
  compiled system prompt; the real mechanism (`OTEL_LOG_RAW_API_BODIES`) requires OpenTelemetry
  logging configured ahead of time, not a retroactive read.
- **OpenCode** — `opencode export <sessionID>` is a real, confirmed way to dump the actual system
  prompt, but it's a command you run, not a file this CLI can read on its own.
- **GitHub Copilot / Windsurf / Cline** — each has a real, docs-confirmed mechanism (VS Code's
  Chat Debug View; an opt-in Cascade transcript hook; Cline's task history, respectively), but
  either requires manual/interactive use, must be enabled beforehand, or (Cline, per official
  docs) explicitly excludes the system prompt from what's persisted.
- **Cursor** — no officially documented mechanism. A `state.vscdb` SQLite file is known to exist
  and hold chat data, but its schema is explicitly undocumented by Cursor; `--live` reports its
  presence only and does not attempt to parse it.

`--live --json` emits one object per tool with `mechanism`, `summary`, `detail`, `confidence`, and
(where applicable) `artifactPath`, `artifactCount`, `lastModified`, `extractedContent`.

## What it checks

For each of the 9 tools, per its own documented real-world rules:

1. The tool's global/user-level config file or directory (checked once, independent of target).
2. The tool's local instruction file pattern(s), checked across every ancestor directory that
   tool's own scope rule says it would actually consult — filesystem-root-to-target for some
   tools, git-root-to-target for others, target-only for others.
3. For tools documented to eagerly scan subdirectories below the target, a bounded recursive scan
   downward (capped depth/directory count; skips `.git`, `node_modules`, `vendor`, `.venv`,
   `dist`, `build`, `.cache`, and dotdirs).

Known gaps (see `docs/research.md` for the full list of UNCONFIRMED items and out-of-scope
behaviors): managed/enterprise-pushed config (e.g. MDM `managed-settings.json`) is not scanned;
`@import`/include syntax inside instruction files is not expanded, content is shown as-is; lazy
subdirectory loading is noted, not simulated.

## Why Go, and why no dependencies

Go was chosen for a single static, portable, cross-compilable binary with no runtime dependency
(no interpreter/VM to install, no `node_modules`, no venv) — a good fit for a CLI meant to be run
ad hoc in arbitrary directories on arbitrary machines. The implementation uses only the Go
standard library (`flag`, `os`, `path/filepath`, `encoding/json`, `sort`, `strings`, `fmt`, `io`,
`bufio`, `time`) — the problem (walk directories, match glob patterns, read files, parse JSONL,
print/encode) doesn't need anything beyond what the stdlib already provides well, including a
hand-rolled recursive-glob (`**`) matcher (`filepath.Glob` doesn't support it), a hand-rolled
minimal frontmatter reader (`internal/compile`, for the handful of `key: value` fields the
conditional-merge tools need — deliberately not a general YAML parser), and line-by-line
`encoding/json` parsing of JSONL transcripts/rollouts (`internal/inspect`). None of this needed an
external glob, YAML, or JSONL library — **`--compile` and `--live` added zero new dependencies.**

## Testing

```
go test ./...
```

Tests are hermetic (`$HOME` is redirected to a temp dir, so nothing reads this machine's real
`~/.claude`, `~/.codex`, etc.) and use only the standard `testing` package — no test-framework
dependency.

## Build

```
go build -o agent-instructions-viewer ./cmd/agent-instructions-viewer
```

## Project layout

- `cmd/agent-instructions-viewer/` — CLI entry point (flag parsing, dispatch to scan/compile/inspect+render).
- `internal/tools/` — static per-tool specs (filenames, scope, precedence, global config paths).
- `internal/scan/` — directory-walk + file-matching engine; always returns full file content.
- `internal/compile/` — `--compile`: assembles matched files per tool's real merge model, with
  chunk separators, token estimates, and documented-limit checks.
- `internal/inspect/` — `--live`: best-effort runtime introspection per tool (session/log/debug
  artifact discovery, classified by mechanism confidence).
- `internal/render/` — text and JSON renderers for all three modes; preview truncation (text-only)
  lives here.
- `docs/research.md` — Phase 1 sourced research per tool, plus a runtime-introspection section.
- `docs/design.md` — Phase 2 algorithm design, including Mermaid diagrams.
