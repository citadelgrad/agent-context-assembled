// Package scan implements the directory-walk + per-tool matching algorithm
// described in docs/design.md.
package scan

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/citadelgrad/actx/internal/tools"
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

// Options controls how scanning behaves. Reserved for future scan-time knobs;
// display-only concerns (like preview truncation) live in the render package instead.
type Options struct{}

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

// Run performs the full scan for one target directory across every known tool.
func Run(target string, opts Options) ([]ToolResult, Chain, error) {
	chain, err := BuildChain(target)
	if err != nil {
		return nil, Chain{}, err
	}

	results := make([]ToolResult, 0, len(tools.Registry))
	for _, t := range tools.Registry {
		results = append(results, scanTool(t, chain, opts))
	}
	return results, chain, nil
}

func scanTool(t tools.Tool, chain Chain, opts Options) ToolResult {
	var files []MatchedFile

	// Global config first (lowest precedence in every tool's documented order).
	for _, gc := range t.GlobalConfigs {
		path := gc.Absolute
		if path == "" && gc.PathFromHome != "" && chain.Home != "" {
			path = filepath.Join(chain.Home, gc.PathFromHome)
		}
		if path == "" {
			continue
		}
		matches := expandCandidate(path)
		for _, m := range matches {
			if content, ok := readFile(m); ok {
				files = append(files, MatchedFile{
					Path:    m,
					Content: content,
					Note:    "global: " + gc.Note,
				})
			}
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
			matches := expandCandidate(filepath.Join(dir, lf.Pattern))
			for _, m := range matches {
				content, ok := readFile(m)
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
		files = append(files, scanDownward(t, target)...)
	}

	return ToolResult{Tool: t, Files: files}
}

// scanDownward walks subdirectories below target (bounded depth, skipping common
// noise directories) looking for the tool's local file patterns, for tools that are
// documented to eagerly scan downward (e.g. Gemini CLI, Windsurf).
func scanDownward(t tools.Tool, target string) []MatchedFile {
	const maxDepth = 6
	const maxDirsVisited = 500
	skip := map[string]bool{
		".git": true, "node_modules": true, "vendor": true, ".venv": true,
		"dist": true, "build": true, ".cache": true, "target": true,
	}

	var out []MatchedFile
	visited := 0

	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > maxDepth || visited > maxDirsVisited {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		// Cache Directory Tagging Standard (https://bford.info/cachedir/): a
		// directory containing a regular file literally named CACHEDIR.TAG
		// directly inside it is a build/cache dir, same as the hardcoded skip
		// names below -- don't count it as visited and don't descend into it.
		// entries is already in hand from the ReadDir above (this is always the
		// first thing walk() does for any directory it's about to process), so
		// this reuses that read instead of doing an extra stat/lookup per
		// candidate directory. Skipped-by-name directories are never opened at
		// all by their parent (see the entries loop below), so they never reach
		// this point or increment visited either; depth > 0 keeps target itself
		// (which is scanned separately by scanTool's ancestor-chain loop, not
		// subject to skip-by-name either) from being skipped this way.
		if depth > 0 && hasCachedirTagEntry(entries) {
			return
		}
		visited++
		// depth 0 is target itself, already covered by the ancestor-chain loop
		// in scanTool (as a "target dir: " match); starting the LocalFiles
		// check here too would double-count any file living directly in
		// target rather than in a real subdirectory below it.
		if depth > 0 {
			for _, lf := range t.LocalFiles {
				matches := expandCandidate(filepath.Join(dir, lf.Pattern))
				for _, m := range matches {
					content, ok := readFile(m)
					if !ok {
						continue
					}
					out = append(out, MatchedFile{
						Path:    m,
						Content: content,
						Note:    "subdirectory: " + lf.Note,
					})
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
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// hasCachedirTagEntry reports whether entries (as returned by os.ReadDir for
// some directory) contains a regular file literally named CACHEDIR.TAG, per
// the Cache Directory Tagging Standard (https://bford.info/cachedir/). A
// directory or symlink named CACHEDIR.TAG does not count -- only a regular
// file tags its containing directory as a cache directory.
func hasCachedirTagEntry(entries []os.DirEntry) bool {
	for _, e := range entries {
		if e.Name() == "CACHEDIR.TAG" {
			return e.Type().IsRegular()
		}
	}
	return false
}

func readFile(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// expandCandidate resolves a path that may contain glob metacharacters. Plain paths
// (no glob chars) are returned as a single-element slice if they exist as a regular
// file, so callers get consistent existence-checking either way.
//
// A "**" path segment is treated as "recurse through zero or more directories",
// matching the doublestar convention used in the tool docs this prototype models
// (e.g. Copilot's ".github/instructions/**/*.instructions.md"). filepath.Glob has no
// native support for this (it treats "**" identically to "*", i.e. one segment,
// non-recursive), so "**" segments are handled with an explicit recursive walk.
func expandCandidate(pattern string) []string {
	if !strings.ContainsAny(pattern, "*?[") {
		if isFile(pattern) {
			return []string{pattern}
		}
		return nil
	}
	if strings.Contains(pattern, "**") {
		return expandDoublestar(pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range matches {
		if isFile(m) {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

// expandDoublestar handles a glob pattern containing exactly one "**" segment by
// walking every directory from the base (the portion before "**") downward and
// applying the remaining suffix pattern (the portion after "**", with its leading
// separator stripped) at each level via filepath.Glob.
func expandDoublestar(pattern string) []string {
	idx := strings.Index(pattern, "**")
	base := filepath.Dir(pattern[:idx])
	suffix := strings.TrimPrefix(pattern[idx+len("**"):], string(filepath.Separator))
	if suffix == "" {
		suffix = "*"
	}

	var out []string
	const maxDepth = 12
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > maxDepth {
			return
		}
		if matches, err := filepath.Glob(filepath.Join(dir, suffix)); err == nil {
			for _, m := range matches {
				if isFile(m) {
					out = append(out, m)
				}
			}
		}
		entries, err := os.ReadDir(dir)
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
	return err == nil && !info.IsDir()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
