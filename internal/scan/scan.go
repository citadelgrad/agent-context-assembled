// Package scan implements the directory-walk + per-tool matching algorithm
// described in docs/design.md.
package scan

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/citadelgrad/agent-context-assembled/internal/budget"
	"github.com/citadelgrad/agent-context-assembled/internal/fileio"
	"github.com/citadelgrad/agent-context-assembled/internal/tools"
)

// MatchedFile is one contributing file for a tool, in application order. Content
// is always the file's full text; any preview truncation is applied later, only by
// the text renderer, so JSON output is never lossy regardless of display flags.
type MatchedFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// Note explains why this file is here / how it applies (e.g. "global", "ancestor: /a/b", "overrides AGENTS.md").
	Note string `json:"note"`
}

// ToolResult is one tool's compiled view: its spec plus the files that actually matched.
type ToolResult struct {
	Tool  tools.Tool
	Files []MatchedFile
}

// Options controls how scanning behaves;
// display-only concerns (like preview truncation) live in the render package instead.
type Options struct {
	// ToolSlugs selects tools before any of their files are scanned.
	// Nil means all tools; an empty non-nil map means none. Only true values select.
	ToolSlugs map[string]bool
}

// Chain describes the resolved ancestor chain for a target directory.
type Chain struct {
	// Dirs is ordered filesystem-root -> target (index 0 is the root).
	Dirs []string
	// GitRootIndex is the index into Dirs of the nearest ancestor containing .git,
	// or -1 if none was found.
	GitRootIndex int
	Home         string
}

// BuildChain walks from target up to the filesystem root, recording every ancestor
// directory and detecting the nearest .git boundary. See docs/design.md Step 1.
//
// Before walking, target is resolved to its physical form via filepath.EvalSymlinks
// (all symlinks in the path, including symlinked ancestor directories, fully
// resolved). This matches how a real coding-agent process resolves its cwd at
// runtime -- e.g. Node's process.cwd() always returns the physical path, never a
// symlinked/logical spelling -- so a target reached through a directory symlink
// (say /Users/scott/projects/actx where /Users/scott/projects is a symlink to
// /Volumes/qwiizlab/projects) walks the same physical ancestor chain
// (/Volumes/qwiizlab/projects/actx, /Volumes/qwiizlab/projects, /Volumes, /) that
// the real tool would consult, rather than the logical one the caller happened to
// type. A broken/unresolvable symlink anywhere along the path surfaces here as an
// error (from EvalSymlinks), which propagates up through Run's existing error
// return rather than producing partial output.
func BuildChain(target string) (Chain, error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return Chain{}, err
	}
	abs = filepath.Clean(abs)

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Chain{}, err
	}
	abs = filepath.Clean(resolved)

	var reversed []string
	cur := abs
	for {
		reversed = append(reversed, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	// reversed is target -> root; flip to root -> target.
	dirs := make([]string, len(reversed))
	for i, d := range reversed {
		dirs[len(reversed)-1-i] = d
	}

	// Prefer the .git closest to target, not closest to root: scan from the end.
	gitRootIndex := -1
	for i := len(dirs) - 1; i >= 0; i-- {
		if isDir(filepath.Join(dirs[i], ".git")) || isFile(filepath.Join(dirs[i], ".git")) {
			gitRootIndex = i
			break
		}
	}

	home, _ := os.UserHomeDir()

	return Chain{Dirs: dirs, GitRootIndex: gitRootIndex, Home: home}, nil
}

// scopedDirs returns the slice of Chain.Dirs a given tool would actually consult,
// per its ScopeMode, ordered root(or boundary)-to-target.
func scopedDirs(c Chain, t tools.Tool) []string {
	target := len(c.Dirs) - 1
	switch t.Scope {
	case tools.ScopeFilesystemRoot:
		return c.Dirs
	case tools.ScopeGitRoot:
		if c.GitRootIndex >= 0 {
			return c.Dirs[c.GitRootIndex:]
		}
		// No .git found: documented fallback is target-dir-only.
		return c.Dirs[target:]
	case tools.ScopeTargetOnly:
		return c.Dirs[target:]
	default:
		return c.Dirs[target:]
	}
}

// Run scans selected tools in registry order with one shared budget.Defaults
// input policy. Exhaustion returns an actionable error and no partial results.
func Run(target string, opts Options) ([]ToolResult, Chain, error) {
	return runWithBudget(target, opts, budget.Limits{})
}

func runWithBudget(target string, opts Options, limits budget.Limits) ([]ToolResult, Chain, error) {
	chain, err := BuildChain(target)
	if err != nil {
		return nil, Chain{}, err
	}

	s := &scanner{budget: budget.New(limits)}
	results := make([]ToolResult, 0, len(tools.Registry))
	for _, t := range tools.Registry {
		if opts.ToolSlugs != nil && !opts.ToolSlugs[t.Slug] {
			continue
		}
		results = append(results, s.scanTool(t, chain, opts))
		if s.budget.Err() != nil {
			return nil, chain, s.budget.Err()
		}
	}
	return results, chain, nil
}

type scanner struct{ budget *budget.Budget }

func scanTool(t tools.Tool, chain Chain, opts Options) ToolResult {
	return (&scanner{budget: budget.New(budget.Limits{})}).scanTool(t, chain, opts)
}

func (s *scanner) scanTool(t tools.Tool, chain Chain, opts Options) ToolResult {
	var files []MatchedFile

	// Global config first (lowest precedence in every tool's documented order).
	for _, gc := range t.GlobalConfigs {
		var matches []string
		if gc.Absolute != "" {
			if s.budget.Err() == nil && isFile(gc.Absolute) && s.budget.Match(gc.Absolute) {
				matches = []string{gc.Absolute}
			}
		} else if gc.PathFromHome != "" && chain.Home != "" {
			matches = s.expandCandidate(chain.Home, gc.PathFromHome)
		}
		for _, m := range matches {
			if content, ok := s.readFile(m); ok {
				files = append(files, MatchedFile{
					Path:    m,
					Content: content,
					Note:    "global: " + gc.Note,
				})
				if t.FirstMatchWins {
					break
				}
			}
		}
		if (t.FirstMatchWins || t.GlobalConfigMode == tools.GlobalConfigFirstExisting) && len(files) > 0 {
			break
		}
	}

	// Local files across the tool's real scope.
	dirs := scopedDirs(chain, t)
	target := chain.Dirs[len(chain.Dirs)-1]

	// scopedDirs returns root-to-target order. FirstMatchWins tools (e.g.
	// OpenCode) document "nearest project file wins", so search target-to-root
	// instead -- otherwise the farthest/outermost match would win instead of
	// the nearest one.
	if t.FirstMatchWins {
		reversed := make([]string, len(dirs))
		for i, d := range dirs {
			reversed[len(dirs)-1-i] = d
		}
		dirs = reversed
	}

	firstMatchFound := false
	for _, dir := range dirs {
		if t.FirstMatchWins && firstMatchFound {
			break
		}
		dirHadMatch := false
	localFilesLoop:
		for _, lf := range t.LocalFiles {
			matches := s.expandCandidate(dir, lf.Pattern)
			for _, m := range matches {
				content, ok := s.readFile(m)
				if !ok {
					continue
				}
				note := lf.Note
				if dir == target {
					note = "target dir: " + note
				} else {
					note = "ancestor " + dir + ": " + note
				}
				files = append(files, MatchedFile{
					Path:    m,
					Content: content,
					Note:    note,
				})
				dirHadMatch = true
				// FirstMatchWins applies within a directory too: only the
				// highest-priority LocalFiles pattern (earliest in the slice)
				// that actually exists in this directory contributes, mirroring
				// the outer ancestor-directory break below. Without this,
				// tools like Hermes (.hermes.md > AGENTS.md > CLAUDE.md >
				// .cursorrules) would have every coexisting candidate filename
				// in the same directory matched instead of just the first one,
				// contradicting their own documented PrecedenceNote.
				if t.FirstMatchWins {
					break localFilesLoop
				}
			}
		}
		if dirHadMatch {
			firstMatchFound = true
		}
	}

	// Downward scan (eager only; lazy tools are documented via NonFileNotes/PrecedenceNote
	// rather than actually walked, since "lazy" means "not loaded yet" by definition).
	if t.Downward == tools.DownwardEager {
		files = append(files, s.scanDownward(t, target)...)
	}

	return ToolResult{Tool: t, Files: files}
}

// scanDownward walks subdirectories below target (bounded depth, skipping common
// noise directories) looking for the tool's local file patterns, for tools that are
// documented to eagerly scan downward (e.g. Gemini CLI, Windsurf).
func scanDownward(t tools.Tool, target string) []MatchedFile {
	return (&scanner{budget: budget.New(budget.Limits{})}).scanDownward(t, target)
}

func (s *scanner) scanDownward(t tools.Tool, target string) []MatchedFile {
	const maxDepth = 6
	const maxDirsVisited = 500
	skip := map[string]bool{
		".git": true, "node_modules": true, "vendor": true, ".venv": true,
		"dist": true, "build": true, ".cache": true, "target": true,
	}

	var out []MatchedFile
	seen := make(map[string]bool)
	visited := 0

	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if s.budget.Err() != nil {
			return
		}
		// A regular tag makes this an intentionally excluded cache, even if
		// its directory is too wide to enumerate within the discovery policy.
		if depth > 0 {
			if info, err := os.Lstat(filepath.Join(dir, "CACHEDIR.TAG")); err == nil && info.Mode().IsRegular() {
				return
			}
		}
		if depth > maxDepth {
			s.budget.Exceed("downward depth", dir, maxDepth)
			return
		}
		if visited >= maxDirsVisited {
			s.budget.Exceed("downward directories", dir, maxDirsVisited)
			return
		}
		entries, err := s.budget.ReadDir(dir)
		if err != nil {
			return
		}
		visited++
		// depth 0 is target itself, already covered by the ancestor-chain loop
		// in scanTool (as a "target dir: " match); starting the LocalFiles
		// check here too would double-count any file living directly in
		// target rather than in a real subdirectory below it.
		if depth > 0 {
			for _, lf := range t.LocalFiles {
				matches := s.expandCandidate(dir, lf.Pattern)
				for _, m := range matches {
					if seen[m] {
						continue
					}
					content, ok := s.readFile(m)
					if !ok {
						continue
					}
					out = append(out, MatchedFile{
						Path:    m,
						Content: content,
						Note:    "subdirectory: " + lf.Note,
					})
					seen[m] = true
				}
			}
		}
		for _, e := range entries {
			if !e.IsDir() || skip[e.Name()] || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			walk(filepath.Join(dir, e.Name()), depth+1)
		}
	}
	walk(target, 0)
	// ReadDir and pattern expansion already give deterministic sibling order.
	// Keep preorder: a lexical full-path sort can put child/A/RULE before
	// child/RULE and make less-specific parent instructions apply last.
	return out
}

func readFile(path string) (string, bool) {
	return (&scanner{budget: budget.New(budget.Limits{})}).readFile(path)
}

func (s *scanner) readFile(path string) (string, bool) {
	if s.budget.Err() != nil {
		return "", false
	}
	f, err := fileio.OpenRegular(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := s.budget.Read(f, path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// expandCandidate resolves a relative registry pattern below a literal base path.
// Only pattern components are matched; metacharacters in base or in discovered
// directory names must never select sibling directories. Results are regular
// files, including symlinks to regular files, in deterministic lexical order.
//
// A "**" path segment is treated as "recurse through zero or more directories",
// matching the doublestar convention used in the tool docs this prototype models
// (e.g. Copilot's ".github/instructions/**/*.instructions.md"). filepath.Glob has no
// native support for this (it treats "**" identically to "*", i.e. one segment,
// non-recursive), so "**" segments are handled with an explicit recursive walk.
func expandCandidate(base, pattern string) []string {
	return (&scanner{budget: budget.New(budget.Limits{})}).expandCandidate(base, pattern)
}

func (s *scanner) expandCandidate(base, pattern string) []string {
	if s.budget.Err() != nil {
		return nil
	}
	if !strings.ContainsAny(pattern, "*?[") {
		path := filepath.Join(base, pattern)
		if isFile(path) && s.budget.Match(path) {
			return []string{path}
		}
		return nil
	}
	part, rest, more := strings.Cut(filepath.FromSlash(pattern), string(filepath.Separator))
	if part == "**" {
		return s.expandDoublestar(base, rest)
	}
	if !strings.ContainsAny(part, "*?[") && more {
		return s.expandCandidate(filepath.Join(base, part), rest)
	}
	entries, err := s.budget.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if s.budget.Err() != nil {
			return nil
		}
		matched, err := filepath.Match(part, entry.Name())
		if err != nil {
			return nil
		}
		if !matched {
			continue
		}
		path := filepath.Join(base, entry.Name())
		if more {
			out = append(out, s.expandCandidate(path, rest)...)
		} else if isFile(path) && s.budget.Match(path) {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// expandDoublestar walks from a literal base and applies the suffix at each level.
// Hidden directories and directory symlinks are not traversed recursively.
func expandDoublestar(base, suffix string) []string {
	return (&scanner{budget: budget.New(budget.Limits{})}).expandDoublestar(base, suffix)
}

func (s *scanner) expandDoublestar(base, suffix string) []string {
	if suffix == "" {
		suffix = "*"
	}

	var out []string
	const maxDepth = 12
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if s.budget.Err() != nil {
			return
		}
		if depth > maxDepth {
			s.budget.Exceed("doublestar depth", dir, maxDepth)
			return
		}
		out = append(out, s.expandCandidate(dir, suffix)...)
		entries, err := s.budget.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				walk(filepath.Join(dir, e.Name()), depth+1)
			}
		}
	}
	if isDir(base) {
		walk(base, 0)
	}
	sort.Strings(out)
	return out
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
