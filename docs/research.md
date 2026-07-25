# Research: AI Coding Agent Instruction File Conventions

This document records, per tool, exactly which instruction/memory files each AI coding agent
discovers, where it looks, how it merges/prioritizes multiple files, and any include/import
syntax. Findings come from official docs, official repos/source, and (where flagged) reputable
secondary sources. Anything not confirmable from an authoritative source is marked
**UNCONFIRMED**.

Research date: 2026-07-25.

---

## Claude Code (Anthropic)

Source: [code.claude.com/docs/en/memory](https://code.claude.com/docs/en/memory)

- **Filenames**: `CLAUDE.md` (primary), `CLAUDE.local.md` (personal/local override, same directory,
  loaded alongside — not deprecated). `./.claude/CLAUDE.md` is equivalent to `./CLAUDE.md`.
  `.claude/rules/*.md` supports modular topic files with frontmatter. Claude Code does **not**
  natively read `AGENTS.md` ("Claude Code reads `CLAUDE.md`, not `AGENTS.md`"); interop is via a
  `CLAUDE.md` containing `@AGENTS.md` or a symlink.
- **Discovery scope**: Walks **up** from cwd checking every ancestor directory for
  `CLAUDE.md`/`CLAUDE.local.md` — not capped at repo root, continues toward filesystem root. Also
  recurses **down** into subdirectories, but lazily: nested `CLAUDE.md`/`CLAUDE.local.md` only enter
  context when Claude reads files in that subtree. `.claude/rules/*.md` discovered recursively.
- **Global/user/enterprise config**: User: `~/.claude/CLAUDE.md`, `~/.claude/rules/`. Managed/
  enterprise (cannot be excluded by users): macOS
  `/Library/Application Support/ClaudeCode/CLAUDE.md`; Linux/WSL `/etc/claude-code/CLAUDE.md`;
  Windows `C:\Program Files\ClaudeCode\CLAUDE.md`; or inline via `claudeMd` key in
  `managed-settings.json`.
- **Merge/precedence**: **Additive concatenation, not override** — all discovered files are
  concatenated into context. Order (broadest -> most specific): Managed policy -> User
  (`~/.claude/CLAUDE.md`) -> Project (`./CLAUDE.md`) -> Local (`./CLAUDE.local.md`). Directory-tree
  order is root-to-cwd, so instructions closer to the launch directory are read last (i.e. most
  recent/salient). Within one directory, `CLAUDE.local.md` is appended after `CLAUDE.md`.
- **Import syntax**: `@path/to/import`, usable inline anywhere in a CLAUDE.md. Supports relative
  (resolved relative to the *containing file*, not cwd) and absolute paths. Recursive, max depth 4
  hops. Skips fenced code blocks/code spans. Imports resolving outside the working directory
  trigger a one-time per-project approval dialog; user-scope imports never prompt. All imports load
  eagerly at launch (not lazy).
- **Special conventions**: `claudeMdExcludes` setting excludes specific ancestor CLAUDE.md files by
  path/glob in monorepos (merges across user/project/local layers; can't exclude managed-policy
  files). `.claude/rules/*.md` supports YAML frontmatter with a `paths` glob field (brace expansion
  supported) for conditional loading, analogous to Copilot's `applyTo`. `/init` can ingest other
  tools' rule files (`AGENTS.md`, `.cursor/rules/`, `.github/copilot-instructions.md`,
  `.windsurfrules`, etc.) into a generated CLAUDE.md. `--add-dir` directories don't auto-load their
  CLAUDE.md unless `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1`. Root CLAUDE.md survives
  `/compact`; nested ones don't auto-reinject. HTML comments stripped before injection.

---

## OpenAI Codex CLI

Sources: `github.com/openai/codex` (`codex-rs/core/src/project_doc.rs`, `config/mod.rs`, pinned at
commit `99f47d6e9a3546c14c43af99c7a58fa6bd130548`), `docs/agents_md.md` in that repo,
`learn.chatgpt.com/docs/agent-configuration/agents-md`.

- **Filenames**: `AGENTS.md` (default, `DEFAULT_PROJECT_DOC_FILENAME`). `AGENTS.override.md`
  (`LOCAL_PROJECT_DOC_FILENAME`) takes precedence over `AGENTS.md` **within the same directory**.
  Configurable fallback filenames via `project_doc_fallback_filenames` (checked only if neither of
  the above exists in that directory).
- **Discovery scope**: Does **not** walk above the git root. Per source comment: determines repo
  root by walking upward from cwd until a `.git` directory/file is found, then collects every
  `AGENTS.md` from repo root down to cwd (inclusive) — "We do not walk past the Git root." If no
  `.git` found, only cwd is used. Reads the linear chain root->cwd, not a full subtree scan.
  UNCONFIRMED: a `project_root_markers` config key surfaced in GitHub issue titles (#12128,
  #12539) but not verified in source.
- **Global/user config**: `~/.codex/` (`codex_home`, overridable via `CODEX_HOME`). Global
  instructions: `~/.codex/AGENTS.override.md` then `~/.codex/AGENTS.md` (first non-empty wins) ->
  becomes `Config::user_instructions`. Settings file: `~/.codex/config.toml`.
- **Merge/precedence**: Additive concatenation across the directory chain, but override within one
  directory (`.override.md` beats `.md`). Final assembly order: global (`~/.codex`) instructions ->
  separator `"\n\n--- project-doc ---\n\n"` -> concatenated project docs joined root->cwd via
  `"\n\n"` (root text first, cwd/nested text last) -> skills section -> optional
  hierarchical-agents meta message. Byte budget `project_doc_max_bytes` (default 32 KiB,
  `PROJECT_DOC_MAX_BYTES`) is shared and consumed root->cwd — if exceeded, nested/local content is
  truncated/dropped while root content is preserved (opposite of "closest wins").
- **Import syntax**: None. No `@import` mechanism; files are concatenated as opaque text blobs.
- **Special conventions**: `child_agents_md` feature flag (`[features]` in `config.toml`) makes
  Codex append a compiled-in message describing AGENTS.md scope/precedence to the agent even absent
  any AGENTS.md file. Symlinks honored as valid AGENTS.md files.
  `project_doc_max_bytes = 0` disables project-doc loading entirely. UNCONFIRMED: a
  `--print-instructions` CLI flag claimed by unreliable blog sources; whether `/init` scaffolds
  AGENTS.md.

---

## GitHub Copilot (VS Code / Copilot Chat)

Sources: [docs.github.com — add-repository-instructions-in-your-ide](https://docs.github.com/en/copilot/how-tos/configure-custom-instructions-in-your-ide/add-repository-instructions-in-your-ide),
[docs.github.com — add-personal-instructions](https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-personal-instructions),
[docs.github.com — add-organization-instructions](https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-organization-instructions),
[code.visualstudio.com — custom-instructions](https://code.visualstudio.com/docs/agent-customization/custom-instructions),
GitHub changelog (2025-04-17, 2025-08-28, 2026-04-02, 2026-06-18).

- **Filenames**: `.github/copilot-instructions.md` (repo-wide, single file, repo root — "Create
  the `.github` directory if it does not already exist"). `.github/instructions/NAME.instructions.md`
  (path-scoped; filename must end `.instructions.md`; can live in subdirectories of
  `.github/instructions`). VS Code's Copilot also natively recognizes `AGENTS.md` as a repo
  instructions source alongside the `.github` file (added per changelog 2025-08-28 for the coding
  agent, and via VS Code settings for Chat).
- **Discovery scope**: Repo-wide file (`copilot-instructions.md`) is root-only. Path-scoped
  `.instructions.md` files are searched **recursively** under `.github/instructions` (or wherever
  `chat.instructionsFilesLocations` points). Parent-directory walking is a separate, gated feature
  via `chat.useCustomizationsInParentRepositories`. Related VS Code settings:
  `chat.instructionsFilesLocations`, `chat.useAgentsMdFile`, `chat.useNestedAgentsMdFiles`,
  `chat.useClaudeMdFile` — VS Code's Copilot can optionally read nested AGENTS.md files and even
  CLAUDE.md directly (exact default values UNCONFIRMED — worth a live spot-check as these are
  actively evolving settings).
- **Personal/user-level instructions**: On github.com, via Copilot Chat profile menu -> "Personal
  instructions" — applies to every conversation on the GitHub website (web-scoped). Separately in
  VS Code: Settings UI "Copilot Chat: Custom Instructions" or Command Palette -> "Copilot: Open
  Custom Instructions" — exact underlying `settings.json` key name UNCONFIRMED.
- **Org-level (Business/Enterprise)**: Configured at github.com Organization -> Settings ->
  Copilot -> Custom instructions; restricted to org owners on Business/Enterprise; GA per
  2026-04-02 changelog. Scope: "currently only supported for Copilot Chat on GitHub.com, Copilot
  code review on GitHub.com and Copilot coding agent on GitHub.com."
- **Precedence/merge**: Explicitly documented: "Personal instructions take the highest priority.
  Repository instructions come next, and then organization instructions are prioritized last.
  However, all sets of relevant instructions are provided to Copilot." I.e. priority ordering is
  advisory for conflict resolution, but the mechanism is **additive**, not exclusionary.
  Repo-wide vs. path-scoped instructions are also additive/combined with no stated precedence: "If
  the path you specify matches a file that Copilot is working on, and a repository-wide custom
  instructions file also exists, then the instructions from both files are used."
- **`applyTo` frontmatter**: Required block, e.g. `applyTo: "app/models/**/*.rb"`; multiple
  patterns comma-separated (`applyTo: "**/*.ts,**/*.tsx"`). Confirmed glob semantics: `*` = current
  dir only, `**`/`**/*` = all dirs recursively, `src/**/*.py` = recursive within `src`. Additional
  confirmed field: `excludeAgent: "code-review"` or `"cloud-agent"`. A `description` frontmatter
  field is commonly mentioned in secondary sources but not directly confirmed in fetched official
  doc text (UNCONFIRMED).

---

## OpenCode (opencode.ai, sst project, now `anomalyco/opencode`)

Sources: [opencode.ai/docs/rules](https://opencode.ai/docs/rules/),
[opencode.ai/docs/config](https://opencode.ai/docs/config/), GitHub repo source
(`packages/opencode/src/session/instruction.ts`), issues #4479, #6479, #6316, #11534.

- **Filenames**: `AGENTS.md` (primary/current standard). `CLAUDE.md` read as a compat fallback for
  Claude Code migrants (toggleable via env flags). `CONTEXT.md` is a legacy/deprecated filename
  from before AGENTS.md adoption, still read for backward compat. `opencode.json`/`.jsonc` is a
  separate config file with its own `instructions` field (see below).
- **Discovery scope**: Walks **upward from cwd**, bounded by the git repo root (`ctx.worktree`),
  per source. **First project-level match wins** — source comment: "The first project-level match
  wins so we don't stack AGENTS.md/CLAUDE.md from every ancestor" — i.e. it does NOT concatenate
  every ancestor's file, unlike Claude Code. Confirmed limitation (issue #4479): search stops at
  the git repository root and cannot find files in parent directories above it. No automatic
  subdirectory/monorepo package discovery (issue #6316 open feature request) — per-package
  instructions require explicit glob entries in `instructions[]` config. UNCONFIRMED: exact
  upper-bound behavior outside git repos (issue #6479 reports an unexpected parent-of-repo read).
- **Global/user config**: `~/.config/opencode/AGENTS.md` and `~/.config/opencode/opencode.json`.
  Claude-compat: `~/.claude/CLAUDE.md` and `~/.claude/skills/` also read when compat mode active.
  `OPENCODE_CONFIG_DIR` env var can add a profile-specific config dir.
- **Merge/precedence**: `opencode.json` config values are deep-merged (docs: "merged together, not
  replaced"). Precedence low->high: remote `.well-known/opencode` config -> global
  `~/.config/opencode/opencode.json` -> `OPENCODE_CONFIG` custom path -> project `opencode.json` ->
  `.opencode/` dirs -> `OPENCODE_CONFIG_CONTENT` inline -> managed/system config -> macOS MDM.
  Instruction *files* themselves are NOT deep-merged the same way: only one global file wins, and
  only one file-type wins per directory level (AGENTS.md vs CLAUDE.md vs CONTEXT.md). Net behavior:
  one nearest-ancestor project file + one global file + any `instructions[]` entries are combined
  additively into the system prompt with "Instructions from: {path}" attribution headers.
- **Include/import syntax**: `opencode.json` -> `instructions` array: entries can be local paths,
  glob patterns (e.g. `.cursor/rules/*.md`, letting it reuse another tool's rule files), or
  HTTP(S) URLs (5s timeout, fetched remotely). An in-body `@path/file.md` reference convention
  inside AGENTS.md itself could not be corroborated from source (UNCONFIRMED, possibly conflated
  with Claude Code's `@import`).
- **Special conventions**: `opencode init` auto-generates a project-root AGENTS.md via project
  analysis; docs recommend committing it. JSON Schemas at `opencode.ai/config.json` and
  `opencode.ai/tui.json`.

---

## Cursor

Source: [cursor.com/docs/context/rules](https://cursor.com/docs/context/rules), plus forum threads
flagged where official docs are silent/contradictory.

- **Filenames**: Current: `.mdc` files in `.cursor/rules/` at repo root, version-controlled. Also
  current: `AGENTS.md` — plain markdown, no metadata, described in docs as "a simple alternative to
  `.cursor/rules`." Legacy: single `.cursorrules` file at repo root — notably absent from current
  official docs entirely (its "legacy"/deprecated status is community-sourced only;
  UNCONFIRMED as an official deprecation statement).
- **Discovery scope**: Docs only demonstrate root-level `.cursor/rules/`; no explicit official
  statement on whether nested `.cursor/rules/` in subdirectories is read for monorepos. Forum
  threads directly conflict on this (UNCONFIRMED/unresolved). What IS officially confirmed: `AGENTS.md`
  files are explicitly nestable in any subdirectory, with "more specific instructions taking
  precedence" for monorepos. No evidence of walking up parent directories beyond the project root.
- **Global/user-level rules**: Settings -> Customize -> Rules ("User Rules"); apply across all
  projects, scoped to Agent Chat. Storage backend not officially documented (community claim: SQLite
  `state.vscdb` — UNCONFIRMED officially).
- **Merge/precedence**: Docs state explicit order: "Team Rules -> Project Rules -> User Rules. All
  applicable rules are merged; earlier sources take precedence when guidance conflicts." I.e.
  additive/merged, not simple nearest-file-wins. No official statement on ordering among multiple
  simultaneously-matching `.mdc` files at the same level.
- **`@file` references**: `@filename.ts` inside a rule body pulls that file's contents into context
  alongside the rule.
- **Frontmatter / rule types**:

  | Frontmatter | Rule type | Behavior |
  |---|---|---|
  | `alwaysApply: true` | Always | Applied every chat session |
  | `alwaysApply: false` + `globs:` | Auto Attached | Auto-attaches when a matching file is in context |
  | `alwaysApply: false` + `description`, no globs | Agent Requested | Model reads description, pulls in when relevant |
  | `alwaysApply: false`, no globs, no description | Manual | Only included via explicit `@`-mention in chat |

  Official monorepo mechanism is nested `AGENTS.md`; nested `.cursor/rules/` monorepo behavior
  remains unresolved/conflicting per forums.

---

## Windsurf (Codeium / Cognition)

**Caveat**: as of this research, `docs.windsurf.com` 307-redirects to `docs.devin.ai`. Third-party
sources describe a Cognition rebrand of Windsurf to "Devin Desktop" (exact date UNCONFIRMED from a
primary press release). Current docs present `.devin/rules/` as preferred, with `.windsurf/rules/`
and `.windsurfrules` as legacy fallbacks that "still work." Findings below reflect the final
Windsurf-branded behavior plus current post-rebrand docs, since users may still encounter either
naming.

Sources: [docs.devin.ai/desktop/cascade/memories](https://docs.devin.ai/desktop/cascade/memories),
official Wave 8 announcement (devin.ai/blog), design.dev and skillwright.app (secondary,
cross-checked, flagged where they disagree).

- **Filenames**: Legacy `.windsurfrules` — single file at repo root, no frontmatter, all content
  always active. `.windsurf/rules/*.md` introduced ~Wave 8 (~May 2025) — directory of multiple
  Markdown files, each with YAML frontmatter enabling activation modes. Current preferred
  (post-rebrand): `.devin/rules/*.md` > `.windsurf/rules/*.md` (fallback) > `.windsurfrules`
  (legacy). All three still read; no forced migration.
- **Discovery scope**: Discovers rules from the current workspace **and its subdirectories**
  (relevant for monorepos). For git repos, also **searches up to the git root** to find rules in
  parent directories — i.e. walks both up (to git root) and down (into subdirectories).
  Same-named rules found in multiple locations are deduplicated and shown with the shortest
  relative path. No documented cap on number of rule files (UNCONFIRMED).
- **Global/user-level rules**: `~/.codeium/windsurf/memories/global_rules.md` — single file,
  applies across all workspaces, always active (no activation-mode selection). Path still uses the
  legacy `codeium/windsurf` namespace even post-rebrand.
- **Merge/precedence and size limits**: Additive concatenation — global rules + all
  discovered/deduplicated workspace rules load together; docs describe multi-repo scenarios as the
  "union of all corresponding sets of Rules." Exact algorithm when `.windsurfrules` AND
  `.windsurf/rules/*.md` coexist simultaneously in one repo is UNCONFIRMED. Character limits (3 of
  4 sources agree): `global_rules.md` 6,000-character hard cap (silent truncation beyond); each
  `.windsurf/rules/*.md` (or `.devin/rules/*.md`) file 12,000 characters, independent per-file
  budget. One outlier source states the inverse — deprioritized given 3-source agreement otherwise.
  Legacy `.windsurfrules` cap also reported as 6,000 characters by third-party sources only
  (UNCONFIRMED from a primary source, since docs.windsurf.com no longer serves historical content).
- **Include/import syntax**: No evidence found of any `@include`/`@import`/file-reference directive
  in rule files. The closest analog is `@`-mentioning a rule by name in chat for Manual-mode
  activation — that's rule invocation, not a file-include mechanism.
- **Activation modes / frontmatter**: Four modes via `trigger` field in YAML frontmatter:
  `trigger: always_on` (injected into every prompt), `trigger: manual` (only via explicit
  `@`-mention by name), `trigger: model_decision` (description always visible, full body retrieved
  only if judged relevant), `trigger: glob` (auto-activates when a file matching `globs:` pattern
  is opened/edited, e.g. `globs: **/*.test.ts`). `description` required for `model_decision`;
  `globs` required for `glob` mode. `global_rules.md` and legacy `.windsurfrules` do NOT use
  frontmatter/trigger modes — unconditionally always-on. No official step-by-step migration doc
  found; only general "both formats still work" backward-compatibility statements (UNCONFIRMED
  beyond that).

---

## Cline (VS Code extension, formerly Claude Dev)

Sources: [docs.cline.bot/customization/cline-rules](https://docs.cline.bot/customization/cline-rules),
[docs.cline.bot/features/multiroot-workspace](https://docs.cline.bot/features/multiroot-workspace),
[docs.cline.bot/customization/clineignore](https://docs.cline.bot/customization/clineignore),
GitHub issues #4642, #5153, discussion #1703.

- **Filenames**: Legacy single `.clinerules` file at project root. Current/recommended:
  `.clinerules/` **directory** of multiple `.md`/`.txt` files, all combined into the system prompt
  (numeric prefixes like `01-coding.md` are optional, not enforced ordering; directory support
  shipped in v3.7). Both forms still detected, alongside `.cursorrules` and `.windsurfrules` for
  cross-tool compat. `AGENTS.md` is also now a recognized rule source (`AGENTS.md`,
  `~/.agents/AGENTS.md`).
- **Discovery scope**: Rules live in `.clinerules/` at the project root — no documented walk-up to
  parent directories (UNCONFIRMED whether it walks up within a single non-multi-root workspace).
  Multi-root workspaces: Cline rules **only work in the primary (first) workspace folder** — other
  folders' `.clinerules/` are ignored by design/limitation. No documented automatic pickup of
  nested subdirectory `.clinerules/` for monorepos (open feature request, issue #4642).
- **Global/user-level config**: Documented location `~/Documents/Cline/Rules` (macOS/Windows).
  Global Workflows: `Documents/Cline/Workflows/`. Disputed on Linux/WSL: an open bug (#5153)
  reports the actual path is `~/Cline/Rules/`, not `~/Documents/Cline/Rules` — unresolved.
- **Merge/precedence**: Global + workspace rules are combined (additive); on conflict, "workspace
  rules take precedence." Multiple files inside `.clinerules/` are all processed and combined into
  one unified rule set; exact concatenation order (alphabetical vs. numeric-prefix) is not stated
  (UNCONFIRMED). Workflows differ: local `.clinerules/workflows/` overrides global workflow of the
  same name (not merged).
- **Include/import syntax**: No shipped `@file.md`-style import syntax. A community-proposed
  `!include` syntax was discussed but superseded by the directory-based `.clinerules/` model
  instead of being implemented.
- **Special conventions**: Workflows (`.clinerules/workflows/*.md`) become slash commands
  (`deploy.md` -> `/deploy`); injected only into the invoking message (one-shot), unlike persistent
  rules. `.clineignore` is a gitignore-syntax file-access restriction, not instructions, marked
  "(deprecate soon)" in docs; not a security boundary. Per-file toggle UI exists for every detected
  rule file (global, workspace, `.cursorrules`, `.windsurfrules`, `AGENTS.md`). Frontmatter: YAML
  `paths:` field takes a glob array; rule activates if ANY pattern matches ANY in-context file (OR
  logic); empty `paths: []` disables without deleting; malformed YAML fails open. Only primary
  workspace folder's rules are honored for monorepos (limited, tracked as enhancement #4642).
  "Memory Bank" is a documented methodology built on a `memory-bank/` markdown folder plus a
  `.clinerules/memory-bank.md` instruction file, not a distinct built-in mechanism.

---

## Aider

Sources: [aider.chat/docs/usage/conventions.html](https://aider.chat/docs/usage/conventions.html),
[aider.chat/docs/config/aider_conf.html](https://aider.chat/docs/config/aider_conf.html),
[aider.chat/docs/config/options.html](https://aider.chat/docs/config/options.html),
[aider.chat/docs/config/dotenv.html](https://aider.chat/docs/config/dotenv.html),
[aider.chat/docs/config/adv-model-settings.html](https://aider.chat/docs/config/adv-model-settings.html).

- **Filenames**: `CONVENTIONS.md` is just a commonly-used name, **not auto-loaded**. Must be
  explicitly included via `/read CONVENTIONS.md`, `aider --read CONVENTIONS.md`, or
  `read: CONVENTIONS.md` in `.aider.conf.yml`. No AGENTS.md/CLAUDE.md-style auto-load exists
  anywhere in Aider — this is the one tool in this survey with **zero automatic instruction-file
  discovery**.
- **Discovery scope (config file, not instructions)**: `.aider.conf.yml` is looked for in exactly
  three fixed locations, loaded in this order: home directory, the root of the git repo, the
  current directory. "If the files above exist, they will be loaded in that order." `--config
  <file>` overrides discovery entirely. This is NOT a full ancestor walk — just these three fixed
  points.
- **Global/user config**: Home directory location stated only as "your home directory" (no literal
  path given in docs — UNCONFIRMED beyond that phrasing). `.env` follows the same 3-tier pattern
  (home -> git root -> cwd), plus a 4th override: `--env-file <filename>`. "Files loaded last will
  take priority."
- **Merge/precedence**: Merged, with later-loaded files winning among the three
  `.aider.conf.yml`/`.env` locations: home dir -> git repo root -> cwd (increasing precedence). Same
  rule applies to `.aider.model.settings.yml`. Precedence of CLI flags vs. config/env is not
  explicitly documented (UNCONFIRMED from official docs; Aider uses `configargparse`, whose typical
  order is CLI > env > config > defaults, but that is inference, not a cited claim).
- **Include/import syntax**: `--read <file>` (repeatable) is the closest thing to an include
  mechanism — "specify a read-only file." YAML equivalent: `read: [CONVENTIONS.md, anotherfile.txt]`.
  Nothing loads without being explicitly named — there is no auto-discovery to speak of for actual
  instruction content, only for the config file itself.
- **Special conventions**: `.aider.model.settings.yml` (per-model settings) and
  `.aider.model.metadata.json` (context-window/cost data for unknown models) follow similar
  discovery patterns; the latter's exact discovery-location behavior is UNCONFIRMED.
  `.aiderignore` (gitignore syntax) is file exclusion, not instructions; defaults to
  `.aiderignore` in the git root.

---

## Gemini CLI (Google)

Sources: [github.com/google-gemini/gemini-cli/blob/main/docs/cli/gemini-md.md](https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/gemini-md.md),
[.../docs/reference/configuration.md](https://github.com/google-gemini/gemini-cli/blob/main/docs/reference/configuration.md),
[.../docs/reference/memport.md](https://github.com/google-gemini/gemini-cli/blob/main/docs/reference/memport.md),
[.../docs/reference/commands.md](https://github.com/google-gemini/gemini-cli/blob/main/docs/reference/commands.md).

- **Filenames**: Default `GEMINI.md`, configurable via `context.fileName` (string | string[]) in
  `.gemini/settings.json` (default unset). Array form explicitly supports mixing in `AGENTS.md`:
  `{"context": {"fileName": ["AGENTS.md", "CONTEXT.md", "GEMINI.md"]}}`.
- **Discovery scope**: Three tiers, walking both up and down:
  1. **Global**: `~/.gemini/<configured-filename>` (e.g. `~/.gemini/GEMINI.md`).
  2. **Project root + ancestors**: searches cwd, then each parent, up to the dir containing a
     boundary marker (default `.git`, configurable via `context.memoryBoundaryMarkers`) or the home
     directory, whichever comes first.
  3. **Downward scan**: also scans subdirectories below cwd (descendants included), respecting
     `.gitignore`/`.geminiignore` (`context.fileFiltering.respectGitIgnore` /
     `.respectGeminiIgnore`, both default `true`). Breadth capped at 200 dirs by default
     (`context.discoveryMaxDirs`).
  Separate "just-in-time" behavior: when a tool accesses a file/dir, the CLI scans that directory
  and its ancestors up to a "trusted root" for additional GEMINI.md files.
- **Global config**: `~/.gemini/settings.json` + `~/.gemini/GEMINI.md`. Relevant keys under
  `context.*`: `fileName`, `discoveryMaxDirs` (default 200), `memoryBoundaryMarkers` (default
  `[".git"]`, restart required; empty array disables upward traversal), `includeDirectories`
  (default `[]`), `loadMemoryFromIncludeDirectories` (default `false`), `importFormat`,
  `includeDirectoryTree` (default `true`), `fileFiltering.respectGitIgnore`/`respectGeminiIgnore`
  (both default `true`), `fileFiltering.customIgnoreFilePaths`.
- **Merge/precedence**: Concatenation with provenance headers: "Content from files lower in this
  list (more specific) typically overrides or supplements content from files higher up (more
  general)." Order: (1) global, (2) project root + ancestors, (3) subdirectories below cwd. "The
  contents of all found context files are concatenated (with separators indicating their origin
  and path)." Full result inspectable via `/memory show`.
- **Import syntax**: `@` + path, confirmed. Relative (`@./file.md`, `@../file.md`) and absolute
  (`@/abs/path/file.md`) both supported. Recursive/nested imports allowed with circular-import
  protection; default max import depth 5. `@` inside code blocks/inline code is ignored. Missing-file
  imports fail gracefully with an inserted error comment.
- **Special conventions**: `/memory show` displays the full concatenated hierarchical memory
  content; `/memory refresh` (also called `/memory reload` in one doc page — a documented
  inconsistency within the repo) reloads all GEMINI.md files; `/memory list` lists paths in use.
  `.geminiignore` is gitignore-style at project root, requires restart to take effect. No
  frontmatter support documented anywhere in GEMINI.md handling (UNCONFIRMED/not supported). No
  `--all-files` flag; relevant flag is `--include-directories <dir1,dir2,...>`. In untrusted
  folders, automatic memory loading from local-settings-specified directories is disabled.

---

## The cross-tool "AGENTS.md" spec

Source: [agents.md](https://agents.md/) (direct fetch), [factory.ai/news/agents-md](https://factory.ai/news/agents-md),
[socket.dev/blog/agents-md-gains-traction](https://socket.dev/blog/agents-md-gains-traction-as-an-open-format-for-ai-coding-agents),
[linuxfoundation.org — AAIF announcement](https://www.linuxfoundation.org/press/linux-foundation-announces-the-formation-of-the-agentic-ai-foundation).

- **Origin/maintainership**: Launched August 19, 2025, as a working group of OpenAI (Codex),
  Sourcegraph (Amp), Google (Jules), Cursor, and Factory. On December 9, 2025, governance moved to
  the Linux Foundation's new **Agentic AI Foundation (AAIF)** — OpenAI donated AGENTS.md, Anthropic
  donated MCP, Block donated goose. Founding members: AWS, Anthropic, Block, Bloomberg, Cloudflare,
  Google, Microsoft, OpenAI. agents.md now states it is "stewarded by the Agentic AI Foundation
  under the Linux Foundation" and claims 60,000+ open-source projects use it.
- **Spec details**: File name `AGENTS.md` (all-caps). Framed as "a README for agents." Location:
  root by default; explicitly supports monorepo nesting — "Place another AGENTS.md inside each
  package. Agents automatically read the nearest file in the directory tree, so the closest one
  takes precedence." Format: plain Markdown, no required schema. Recommended (not required)
  sections: project overview, build/test commands, code style, testing instructions, security
  notes, PR/commit guidance. **Precedence rule (verbatim): "The closest AGENTS.md to the edited
  file wins; explicit user chat prompts override everything."** — nearest-file-wins/override model,
  not a merge, with live user prompts as the top layer. Minor interop wrinkle: Cursor's own docs
  describe nested files as "combined... with more specific instructions taking precedence" —
  phrasing suggests some merging rather than strict override, a small divergence from the spec's
  own language.
- **Tools with confirmed explicit/documented support**: OpenAI Codex (origin project — reads across
  global/project-root/cwd layers per this research's own Codex source-code analysis above). Google
  Jules (auto-detects root AGENTS.md by default, no config needed). Cursor (documented as "a simple
  alternative to `.cursor/rules`," supported in root + subdirectories). GitHub Copilot (Copilot
  coding agent added support per changelog Aug 28, 2025; Copilot Code Review added it per changelog
  Jun 18, 2026). Factory.ai (founding collaborator). Aider supports it only via its generic
  `--read`/`CONVENTIONS.md`-style explicit-file mechanism, not automatic filename detection — treat
  as "compatible via config," not automatic. OpenCode (primary filename, confirmed above). Zed
  (recommended go-forward name in its fallback chain, confirmed above). Amp (primary filename,
  confirmed above). JetBrains Junie (`.junie/AGENTS.md` / root `AGENTS.md`, confirmed below). Other
  tools repeatedly named in secondary sources (Windsurf, RooCode, Devin, Kilo Code, Warp, Augment,
  Ona, Semgrep, UiPath) were not independently verified against each vendor's own docs in this pass
  — flagged as reported-but-unconfirmed.
- **Tools using a proprietary filename, and their AGENTS.md interop**:
  - **Claude Code**: uses CLAUDE.md. Open, unresolved GitHub feature request
    (anthropics/claude-code#6235, "Support AGENTS.md," opened Aug 21 2025) with no maintainer
    response visible as of this research. No AGENTS.md mentions found in Claude Code's changelog
    through v2.1.220. No evidence Anthropic has shipped any AGENTS.md fallback/read support in
    Claude Code itself.
  - **Gemini CLI**: uses GEMINI.md by default, but `context.fileName` accepts an array (e.g.
    `["AGENTS.md","CONTEXT.md","GEMINI.md"]`) — opt-in interop, not default. Community requests to
    default to AGENTS.md remain open. Notable contrast: Jules (Google) natively auto-detects
    AGENTS.md with no config, while Gemini CLI (also Google) defaults to GEMINI.md and only reads
    AGENTS.md if configured.

---

## Other notable tools with confirmed instruction-file conventions

### Zed editor
- Checks a fallback chain, first match wins: `.rules`, `.cursorrules`, `.windsurfrules`,
  `.clinerules`, `.github/copilot-instructions.md`, `AGENT.md`, `AGENTS.md`, `CLAUDE.md`,
  `GEMINI.md`. AGENTS.md is the recommended go-forward name.
- Scope: project-level only; nested/subdirectory AGENTS.md is an open feature request
  (zed-industries/zed #53332), not implemented.
- Global config: `~/.config/zed/AGENTS.md` (mac/Linux), `%APPDATA%\Zed\AGENTS.md` (Windows).
- Precedence: project instructions override personal AGENTS.md; among project files, first chain
  match wins.
- Source: [zed.dev/docs/ai/instructions](https://zed.dev/docs/ai/instructions)

### Amp (ampcode.com, Sourcegraph)
- Primary filename `AGENTS.md` (migrated from singular `AGENT.md` Aug 20, 2025); falls back to
  `AGENT.md` or `CLAUDE.md` if absent.
- Scope: root + all parent dirs up to `$HOME` always included; nested subtree AGENTS.md files
  loaded lazily when a file in that subtree is read.
- Global config: `$HOME/.config/amp/AGENTS.md`, `$HOME/.config/AGENTS.md`, plus system-wide
  `/etc/ampcode/AGENTS.md`.
- Precedence: additive, not override — all applicable levels included simultaneously
  (`amp config agents-md list` shows the active set).
- Sources: [ampcode.com/manual](https://ampcode.com/manual), [ampcode.com/news/AGENTS.md](https://ampcode.com/news/AGENTS.md)

### Continue.dev
- Current convention: `.continue/rules/` folder (Markdown w/ YAML frontmatter, or YAML files);
  rules can also be inlined in `config.yaml` or referenced from Continue Hub. No credible evidence
  for `.continuerules` as a current/documented filename.
- Scope: project-level folder only, loaded in lexicographic order; glob-scoped per rule. No
  documentation on parent-dir walking or global config.
- Precedence: not documented when multiple rules coexist.
- Sources: [docs.continue.dev/customize/deep-dives/rules](https://docs.continue.dev/customize/deep-dives/rules)

### Amazon Q Developer (AWS)
- "Project Rules": Markdown under `.amazonq/rules/` at project root (scanned recursively via
  `**/*.md`).
- Global config: `~/.aws/amazonq/global_context.json` (all profiles) and
  `~/.aws/amazonq/profiles/<name>/context.json` (per-profile).
- Precedence: global + profile context layer together; project rules load as additional context on
  top; no fine-grained override rule documented.
- Source: docs.aws.amazon.com/amazonq (direct fetch returned a JS shell only; findings from
  AWS-domain search snippets — treat scope/precedence details as lower-confidence than other
  entries in this doc).

### JetBrains AI Assistant / Junie
- Current preferred: `.junie/AGENTS.md`, falling back to root `AGENTS.md`; legacy
  `.junie/guidelines.md` or `.junie/guidelines/` still supported.
- Scope: project root only, no parent-walk or nested/per-module support. Search order:
  `.junie/AGENTS.md` -> root `AGENTS.md` -> legacy guidelines.
- Global config: `~/.junie/AGENTS.md` or `%USERPROFILE%\.junie\AGENTS.md`.
- Precedence: project wins on conflict; if both global and project exist, both are included
  (deduplicated, clearly marked).
- Sources: [junie.jetbrains.com/docs/guidelines-and-memory.html](https://junie.jetbrains.com/docs/guidelines-and-memory.html)

### Replit Agent
- Filename: `replit.md`, auto-created/updated by the Agent.
- Scope: project root only — docs explicitly state subdirectory copies aren't detected.
- "Global" equivalent: UI-configured "Custom Instructions" (workspace/org level, Pro/Enterprise
  only), not a file.
- Precedence: not explicitly documented across the three layers (Custom Instructions, replit.md,
  Skills via `/.agents/skills/<name>/SKILL.md`).
- Sources: [docs.replit.com/replitai/replit-dot-md](https://docs.replit.com/replitai/replit-dot-md)

### Tools checked with no credible evidence found
JetBrains AI Assistant (non-Junie) and Amazon Q's org-wide policy features beyond project rules
were not further verifiable; no separate convention found or claimed beyond what's listed above.

---

## Summary table

| Tool | File(s) | Walk up? | Walk down? | Global config | Merge model |
|---|---|---|---|---|---|
| Claude Code | `CLAUDE.md`, `CLAUDE.local.md`, `.claude/rules/*.md` | Yes, to filesystem root | Yes, lazily | `~/.claude/CLAUDE.md` | Additive concat, root-to-cwd order |
| Codex CLI | `AGENTS.md`, `AGENTS.override.md` | To git root only | No (linear chain only) | `~/.codex/AGENTS.md` | Additive concat root->cwd; override same-dir |
| GitHub Copilot | `.github/copilot-instructions.md`, `.github/instructions/*.instructions.md`, `AGENTS.md` (opt-in) | Root only (parent-repo walk gated behind a setting) | Yes, for `.instructions.md` | VS Code/github.com personal settings | Additive; personal > repo > org priority for conflicts |
| OpenCode | `AGENTS.md`, `CLAUDE.md` (compat), `CONTEXT.md` (legacy) | To git root only | No (open feature request) | `~/.config/opencode/AGENTS.md` | First-match-wins per level; additive across levels |
| Cursor | `.cursor/rules/*.mdc`, `AGENTS.md` | Root only (no confirmed walk-up) | AGENTS.md yes; `.mdc` disputed | Settings UI "User Rules" | Additive merge, Team > Project > User |
| Windsurf | `.devin/rules/*.md` / `.windsurf/rules/*.md` / `.windsurfrules` | To git root | Yes, into subdirectories | `~/.codeium/windsurf/memories/global_rules.md` | Additive union, dedup by shortest path |
| Cline | `.clinerules/`, `.clinerules` (legacy), `AGENTS.md` | Not documented | Not documented (monorepo nesting is an open request) | `~/Documents/Cline/Rules` | Additive; workspace overrides global on conflict |
| Aider | none auto-loaded (`CONVENTIONS.md` by convention only) | N/A for instructions; config file uses fixed 3-point lookup | No | "home directory" (unspecified path) | Last-loaded-wins among 3 fixed config locations |
| Gemini CLI | `GEMINI.md` (configurable name incl. `AGENTS.md`) | Yes, to `.git`/home | Yes, capped at 200 dirs | `~/.gemini/GEMINI.md` | Additive concat with provenance headers, general->specific |

