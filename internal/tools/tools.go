// Package tools defines the static registry of AI coding-agent instruction-file
// conventions used by the scanner. Each entry encodes what research.md documented
// for that tool: canonical filenames, how far it really walks up/down the directory
// tree, and where its global/user-level config lives.
package tools

// ScopeMode describes how far up the ancestor chain a tool actually looks in real
// life, per docs/research.md. This lets the scanner avoid presenting a directory as
// "in effect" for a tool that would never have read it.
type ScopeMode int

const (
	// ScopeFilesystemRoot: tool walks all the way to the filesystem root (Claude Code, Gemini CLI).
	ScopeFilesystemRoot ScopeMode = iota
	// ScopeGitRoot: tool stops at the detected .git boundary and does not walk further up (Codex CLI, OpenCode).
	// Falls back to target-dir-only if no .git is found, matching each tool's documented behavior.
	ScopeGitRoot
	// ScopeTargetOnly: tool only ever looks in a single directory, no ancestor walk (Cursor, Cline, Windsurf's legacy file).
	ScopeTargetOnly
)

// DownwardMode describes whether a tool also looks into subdirectories below the target.
type DownwardMode int

const (
	// DownwardNone: tool never looks below the target directory.
	DownwardNone DownwardMode = iota
	// DownwardLazy: tool loads subdirectory files lazily as the agent reads files there
	// (Claude Code, Amp). The scanner still reports these as "reachable" since a real
	// session touching that subtree would load them.
	DownwardLazy
	// DownwardEager: tool proactively scans subdirectories at startup (Gemini CLI, Windsurf).
	DownwardEager
)

// GlobalConfigMode describes how a tool selects among global config candidates.
type GlobalConfigMode int

const (
	// GlobalConfigAdditive includes every existing global candidate.
	GlobalConfigAdditive GlobalConfigMode = iota
	// GlobalConfigFirstExisting includes only the first existing candidate.
	GlobalConfigFirstExisting
)

// LocalFile is one canonical filename or glob pattern this tool looks for in a
// directory, tagged with a note on how it composes with siblings at the same level.
type LocalFile struct {
	// Pattern is a filename (e.g. "CLAUDE.md") or a glob relative to the directory
	// being checked (e.g. ".cursor/rules/*.mdc", ".clinerules/*").
	Pattern string
	// Note is a short human-readable annotation, e.g. "overrides AGENTS.md in same dir".
	Note string
}

// GlobalConfig is a candidate path (relative to $HOME, or absolute) for a tool's
// user-level config. GlobalConfigMode determines whether candidates stack or
// act as ordered fallbacks; local FirstMatchWins semantics are independent.
type GlobalConfig struct {
	// PathFromHome is joined onto the user's home directory. Mutually exclusive with Absolute.
	PathFromHome string
	// Absolute is used verbatim (e.g. managed/enterprise paths). Mutually exclusive with PathFromHome.
	Absolute string
	Note     string
}

// Tool is the full spec for one AI coding agent's instruction-file conventions.
type Tool struct {
	// Name is the display name, e.g. "Claude Code".
	Name string
	// Slug is a short machine-friendly identifier, e.g. "claude-code".
	Slug string

	// LocalFiles are the canonical filenames/globs checked at each applicable directory.
	LocalFiles []LocalFile
	// Scope controls how far up the ancestor chain LocalFiles are checked.
	Scope ScopeMode
	// Downward controls whether/how subdirectories below the target are also checked.
	Downward DownwardMode
	// FirstMatchWins: if true, only the nearest directory with a match contributes
	// (OpenCode's documented "first project-level match wins"). If false, every
	// matching directory in scope contributes (additive concatenation, the common case).
	FirstMatchWins bool

	// GlobalConfigs are the tool's global/user-level config file candidates.
	GlobalConfigs []GlobalConfig
	// GlobalConfigMode controls whether candidates are additive or fallbacks.
	GlobalConfigMode GlobalConfigMode

	// PrecedenceNote is a short one-line description of application order, echoed in
	// output so users don't have to cross-reference docs/research.md while reading results.
	PrecedenceNote string

	// NonFileNotes are notable layers this prototype cannot detect on disk (e.g.
	// Cursor/Copilot personal settings stored in app databases, not plain files).
	NonFileNotes []string
}

// Registry is every tool this prototype knows about, in a stable display order.
// This order is deliberate (roughly: file-hierarchy tools first, then IDE-rule
// tools, then the odd-one-out Aider) rather than alphabetical, so related tools
// group together when --all is used.
var Registry = []Tool{
	{
		Name: "Claude Code",
		Slug: "claude-code",
		LocalFiles: []LocalFile{
			{Pattern: "CLAUDE.md", Note: "project/ancestor instructions"},
			{Pattern: "CLAUDE.local.md", Note: "personal override, applied after CLAUDE.md in same dir"},
		},
		Scope:          ScopeFilesystemRoot,
		Downward:       DownwardLazy,
		FirstMatchWins: false,
		GlobalConfigs: []GlobalConfig{
			{PathFromHome: ".claude/CLAUDE.md", Note: "user-level"},
		},
		PrecedenceNote: "Additive concatenation: managed -> user (~/.claude/CLAUDE.md) -> ancestors root-to-target (CLAUDE.md then CLAUDE.local.md per dir). Subdirectory CLAUDE.md files load lazily as files are read there; shown here as reachable, not necessarily loaded yet.",
		NonFileNotes: []string{
			"Managed/enterprise policy files (e.g. /etc/claude-code/CLAUDE.md, managed-settings.json claudeMd key) are not scanned by this prototype.",
			"@import syntax inside a CLAUDE.md is not expanded by this prototype; content is shown as-is.",
		},
	},
	{
		Name: "OpenAI Codex CLI",
		Slug: "codex-cli",
		LocalFiles: []LocalFile{
			{Pattern: "AGENTS.md", Note: "project doc"},
			{Pattern: "AGENTS.override.md", Note: "overrides AGENTS.md in same dir"},
		},
		Scope:          ScopeGitRoot,
		Downward:       DownwardNone,
		FirstMatchWins: false,
		GlobalConfigs: []GlobalConfig{
			{PathFromHome: ".codex/AGENTS.override.md", Note: "global override, wins over AGENTS.md if present"},
			{PathFromHome: ".codex/AGENTS.md", Note: "global instructions"},
		},
		GlobalConfigMode: GlobalConfigFirstExisting,
		PrecedenceNote:   "Additive concatenation from git root down to target (root text first); AGENTS.override.md wins over AGENTS.md within the same directory. Codex does not walk above the git root (falls back to target-dir-only if no .git found).",
	},
	{
		Name: "GitHub Copilot",
		Slug: "github-copilot",
		LocalFiles: []LocalFile{
			{Pattern: ".github/copilot-instructions.md", Note: "repo-wide"},
			{Pattern: ".github/instructions/**/*.instructions.md", Note: "path-scoped via applyTo frontmatter"},
			{Pattern: "AGENTS.md", Note: "recognized directly by VS Code Copilot as an opt-in alternative"},
		},
		Scope:          ScopeGitRoot,
		Downward:       DownwardNone,
		FirstMatchWins: false,
		GlobalConfigs:  nil,
		PrecedenceNote: "Additive: repo-wide + path-scoped + AGENTS.md are all provided together; personal (web/VS Code settings) > repo > org priority is advisory for conflicts, not exclusionary.",
		NonFileNotes: []string{
			"Personal instructions (github.com profile, or VS Code settings.json) are stored outside the repo and are not scanned by this prototype.",
			"Organization-level custom instructions (Business/Enterprise) live on github.com and are not scanned by this prototype.",
		},
	},
	{
		Name: "OpenCode",
		Slug: "opencode",
		LocalFiles: []LocalFile{
			{Pattern: "AGENTS.md", Note: "primary"},
			{Pattern: "CLAUDE.md", Note: "compat fallback"},
			{Pattern: "CONTEXT.md", Note: "legacy fallback"},
		},
		Scope:          ScopeGitRoot,
		Downward:       DownwardNone,
		FirstMatchWins: true,
		GlobalConfigs: []GlobalConfig{
			{PathFromHome: ".config/opencode/AGENTS.md", Note: "global"},
			{PathFromHome: ".claude/CLAUDE.md", Note: "Claude-compat global, read when compat mode active"},
		},
		GlobalConfigMode: GlobalConfigFirstExisting,
		PrecedenceNote:   "Global file + one nearest project-level match (first ancestor, walking target-to-root, that has AGENTS.md/CLAUDE.md/CONTEXT.md wins; ancestors are NOT stacked). Does not walk above the git root.",
	},
	{
		Name: "Cursor",
		Slug: "cursor",
		LocalFiles: []LocalFile{
			{Pattern: ".cursor/rules/*.mdc", Note: "rule types: Always/Auto Attached/Agent Requested/Manual via frontmatter"},
			{Pattern: "AGENTS.md", Note: "simple alternative to .cursor/rules, nestable in monorepos"},
			{Pattern: ".cursorrules", Note: "legacy, undocumented in current official docs"},
		},
		Scope:          ScopeTargetOnly,
		Downward:       DownwardNone,
		FirstMatchWins: false,
		GlobalConfigs:  nil,
		PrecedenceNote: "Additive merge, documented order Team Rules -> Project Rules -> User Rules (earlier wins on conflict). Nested .cursor/rules/ in subdirectories is disputed/unconfirmed in official docs; this prototype only checks the target directory.",
		NonFileNotes: []string{
			"User Rules and Team Rules are configured in Cursor's settings UI, not plain files, and are not scanned by this prototype.",
		},
	},
	{
		Name: "Windsurf",
		Slug: "windsurf",
		LocalFiles: []LocalFile{
			{Pattern: ".windsurf/rules/*.md", Note: "trigger: always_on/manual/model_decision/glob via frontmatter"},
			{Pattern: ".devin/rules/*.md", Note: "post-rebrand preferred location"},
			{Pattern: ".windsurfrules", Note: "legacy single file, ~6000 char cap, always active"},
		},
		Scope:          ScopeGitRoot,
		Downward:       DownwardEager,
		FirstMatchWins: false,
		GlobalConfigs: []GlobalConfig{
			{PathFromHome: ".codeium/windsurf/memories/global_rules.md", Note: "global, ~6000 char cap, always active"},
		},
		PrecedenceNote: "Additive union: global rules + all workspace rules found from git root down to target and in subdirectories below target, deduplicated by shortest relative path.",
	},
	{
		Name: "Cline",
		Slug: "cline",
		LocalFiles: []LocalFile{
			{Pattern: ".clinerules/*", Note: "current, directory of files all combined"},
			{Pattern: ".clinerules", Note: "legacy single file"},
			{Pattern: "AGENTS.md", Note: "also recognized as a rule source"},
		},
		Scope:          ScopeTargetOnly,
		Downward:       DownwardNone,
		FirstMatchWins: false,
		GlobalConfigs: []GlobalConfig{
			{PathFromHome: "Documents/Cline/Rules", Note: "global (macOS/Windows documented path)"},
		},
		PrecedenceNote: "Additive: global + workspace combined; workspace rules take precedence on conflict. No documented ancestor walk-up; monorepo subdirectory nesting is an open feature request, not implemented.",
	},
	{
		Name: "Gemini CLI",
		Slug: "gemini-cli",
		LocalFiles: []LocalFile{
			{Pattern: "GEMINI.md", Note: "default filename (configurable via context.fileName, can include AGENTS.md)"},
		},
		Scope:          ScopeFilesystemRoot,
		Downward:       DownwardEager,
		FirstMatchWins: false,
		GlobalConfigs: []GlobalConfig{
			{PathFromHome: ".gemini/GEMINI.md", Note: "global"},
		},
		PrecedenceNote: "Additive concatenation with provenance headers: global -> ancestors (root or .git boundary, whichever hit first, down to target) -> subdirectories below target (capped at 200 dirs by default). More specific supplements/overrides more general.",
	},
	{
		Name: "Aider",
		Slug: "aider",
		LocalFiles: []LocalFile{
			{Pattern: ".aider.conf.yml", Note: "config file, NOT an auto-loaded instruction file"},
		},
		Scope:          ScopeTargetOnly,
		Downward:       DownwardNone,
		FirstMatchWins: false,
		GlobalConfigs: []GlobalConfig{
			{PathFromHome: ".aider.conf.yml", Note: "home dir config, loaded first (lowest precedence)"},
		},
		PrecedenceNote: "Aider auto-loads NO instruction file. .aider.conf.yml is a config file (not instructions) checked at exactly 3 fixed locations: home dir -> git repo root -> cwd, last-loaded wins. CONVENTIONS.md-style files must be named explicitly via --read or the config's read: key; this prototype cannot know that name in advance, so it is not scanned.",
		NonFileNotes: []string{
			"Instruction content (e.g. CONVENTIONS.md) is never auto-discovered by Aider; it must be named explicitly via --read or a read: key in .aider.conf.yml, so this prototype cannot enumerate it generically.",
		},
	},
	{
		Name: "Hermes Agent",
		Slug: "hermes",
		LocalFiles: []LocalFile{
			{Pattern: ".hermes.md", Note: "Hermes-native project file, highest priority"},
			{Pattern: "HERMES.md", Note: "Hermes-native project file, same priority as .hermes.md"},
			{Pattern: "AGENTS.md", Note: "compat fallback; docs also describe a live-session ancestor walk (up to 5 parent dirs) this static scan does not perform -- see NonFileNotes"},
			{Pattern: "CLAUDE.md", Note: "compat fallback; docs also describe a live-session ancestor walk (up to 5 parent dirs) this static scan does not perform -- see NonFileNotes"},
			{Pattern: ".cursorrules", Note: "compat fallback; documented as cwd-only, no ancestor walk"},
		},
		Scope:          ScopeTargetOnly,
		Downward:       DownwardLazy,
		FirstMatchWins: true,
		GlobalConfigs: []GlobalConfig{
			{PathFromHome: ".hermes/SOUL.md", Note: "agent personality/identity definition, injected into the system prompt"},
		},
		PrecedenceNote: "Only one project-context filename is loaded per session, in priority order .hermes.md/HERMES.md > AGENTS.md > CLAUDE.md > .cursorrules (first match wins), plus the global SOUL.md. Each loaded file is truncated independently (not a shared budget) to context_file_max_chars (default 20,000 characters; docs confirm head/tail truncation but not the exact split). At startup all five filenames are discovered from cwd; docs additionally describe AGENTS.md/CLAUDE.md being rechecked in up to 5 parent directories during a live session, which this static single-target scan does not perform -- see NonFileNotes.",
		NonFileNotes: []string{
			"Per official docs (hermes-agent.nousresearch.com/docs/user-guide/features/context-files), AGENTS.md and CLAUDE.md are additionally rechecked in up to 5 parent directories during a live session (via a component the docs call the SubdirectoryHintTracker); .cursorrules is explicitly documented as cwd-only, and .hermes.md/HERMES.md's ancestor behavior is not separately documented. This prototype only scans the target directory (Scope=ScopeTargetOnly) and has no ScopeMode for a bounded, session-driven ancestor walk -- ScopeGitRoot/ScopeFilesystemRoot would overstate how far Hermes actually looks from a fresh cwd.",
			"Skills live at ~/.hermes/skills/ and use progressive disclosure: a name+description index (~3k tokens) is injected into the system prompt at session start, with full SKILL.md content fetched on demand only when a skill is actually used. Not modeled by this prototype -- no tool in this registry scans on-demand skill directories (not even Claude Code's own .claude/skills/), so this is a separate, cross-tool feature request rather than a Hermes-only gap.",
			"`hermes prompt-size` (hermes-agent.nousresearch.com/docs/reference/cli-commands) reports the fixed prompt budget for a fresh session -- what gets sent on every API call before any conversation content -- but does not print the resolved instruction-file/context text itself.",
		},
	},
}
