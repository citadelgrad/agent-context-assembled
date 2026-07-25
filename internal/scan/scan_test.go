package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/citadelgrad/agent-instructions-viewer/internal/tools"
)

// mustMkdirAll is a small test helper wrapping os.MkdirAll with a t.Fatal.
func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
}

// mustWriteFile is a small test helper wrapping os.WriteFile with a t.Fatal.
func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

// ---- BuildChain ------------------------------------------------------------

// TestBuildChainWalksToFilesystemRoot verifies the ancestor chain walks all
// the way to "/" (filesystem root), not just to some intermediate boundary
// like a repo root -- this is the core documented behavior in
// docs/design.md's Step 1.
func TestBuildChainWalksToFilesystemRoot(t *testing.T) {
	tmp := t.TempDir()
	nested := filepath.Join(tmp, "a", "b", "c")
	mustMkdirAll(t, nested)

	chain, err := BuildChain(nested)
	if err != nil {
		t.Fatalf("BuildChain: %v", err)
	}

	if len(chain.Dirs) == 0 {
		t.Fatal("Dirs is empty")
	}
	// First entry must be the filesystem root: Dir(root) == root.
	root := chain.Dirs[0]
	if filepath.Dir(root) != root {
		t.Errorf("first chain entry %q is not the filesystem root (Dir(%q) = %q)", root, root, filepath.Dir(root))
	}
	if root != string(filepath.Separator) {
		t.Errorf("first chain entry = %q, want %q", root, string(filepath.Separator))
	}

	// Last entry must be the (absolute, cleaned) target directory.
	last := chain.Dirs[len(chain.Dirs)-1]
	wantTarget, _ := filepath.Abs(nested)
	wantTarget = filepath.Clean(wantTarget)
	if last != wantTarget {
		t.Errorf("last chain entry = %q, want %q", last, wantTarget)
	}

	// Chain must be strictly root -> target, i.e. each entry's parent is the
	// previous entry.
	for i := 1; i < len(chain.Dirs); i++ {
		if filepath.Dir(chain.Dirs[i]) != chain.Dirs[i-1] {
			t.Errorf("Dirs[%d]=%q is not a direct child of Dirs[%d]=%q", i, chain.Dirs[i], i-1, chain.Dirs[i-1])
		}
	}
}

// TestBuildChainTargetIsFilesystemRoot covers the edge case where the target
// itself IS the filesystem root: the chain should be a single-element slice.
func TestBuildChainTargetIsFilesystemRoot(t *testing.T) {
	root := string(filepath.Separator)
	chain, err := BuildChain(root)
	if err != nil {
		t.Fatalf("BuildChain(%q): %v", root, err)
	}
	if len(chain.Dirs) != 1 {
		t.Fatalf("Dirs = %v, want single-element slice for filesystem root target", chain.Dirs)
	}
	if chain.Dirs[0] != root {
		t.Errorf("Dirs[0] = %q, want %q", chain.Dirs[0], root)
	}
}

// TestBuildChainDetectsGitBoundaryNearestToTarget verifies that when multiple
// ancestors contain a .git entry, GitRootIndex points at the one NEAREST the
// target, not the one nearest the filesystem root (this matters for nested
// repos / repos-within-repos scenarios).
func TestBuildChainDetectsGitBoundaryNearestToTarget(t *testing.T) {
	tmp := t.TempDir()
	outerRepo := filepath.Join(tmp, "outer")
	innerRepo := filepath.Join(outerRepo, "inner")
	target := filepath.Join(innerRepo, "sub")

	mustMkdirAll(t, filepath.Join(outerRepo, ".git"))
	mustMkdirAll(t, filepath.Join(innerRepo, ".git"))
	mustMkdirAll(t, target)

	chain, err := BuildChain(target)
	if err != nil {
		t.Fatalf("BuildChain: %v", err)
	}
	if chain.GitRootIndex < 0 {
		t.Fatal("GitRootIndex = -1, want a detected boundary")
	}
	got := chain.Dirs[chain.GitRootIndex]
	wantInner, _ := filepath.Abs(innerRepo)
	wantInner = filepath.Clean(wantInner)
	if got != wantInner {
		t.Errorf("GitRootIndex points at %q, want the nearest-to-target repo %q", got, wantInner)
	}
}

// TestBuildChainNoGitFound verifies GitRootIndex is -1 when no ancestor has a
// .git entry.
func TestBuildChainNoGitFound(t *testing.T) {
	tmp := t.TempDir()
	nested := filepath.Join(tmp, "x", "y")
	mustMkdirAll(t, nested)

	chain, err := BuildChain(nested)
	if err != nil {
		t.Fatalf("BuildChain: %v", err)
	}
	if chain.GitRootIndex != -1 {
		t.Errorf("GitRootIndex = %d, want -1 (no .git anywhere in this fabricated tree)", chain.GitRootIndex)
	}
}

// TestBuildChainGitAsFile covers the (real, e.g. git worktrees/submodules)
// case where ".git" is a file, not a directory, and should still be detected
// as a boundary.
func TestBuildChainGitAsFile(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	target := filepath.Join(repo, "sub")
	mustMkdirAll(t, target)
	mustWriteFile(t, filepath.Join(repo, ".git"), "gitdir: /somewhere/else\n")

	chain, err := BuildChain(target)
	if err != nil {
		t.Fatalf("BuildChain: %v", err)
	}
	if chain.GitRootIndex < 0 {
		t.Fatal("GitRootIndex = -1, want detection of .git-as-file boundary")
	}
	wantRepo, _ := filepath.Abs(repo)
	wantRepo = filepath.Clean(wantRepo)
	if chain.Dirs[chain.GitRootIndex] != wantRepo {
		t.Errorf("GitRootIndex points at %q, want %q", chain.Dirs[chain.GitRootIndex], wantRepo)
	}
}

// ---- scopedDirs (via Run/scanTool behavior) --------------------------------

// buildTestTree creates a 4-level-deep temp directory tree:
//
//	root/                     (level 0, no .git)
//	  org/                    (level 1, no .git)
//	    repo/                 (level 2, HAS .git -- repo root)
//	      pkg/                (level 3 -- target)
//
// and returns the absolute paths of each level plus the target.
type testTree struct {
	root, org, repo, pkg string
}

func buildTestTree(t *testing.T) testTree {
	t.Helper()
	tmp := t.TempDir()
	root := tmp
	org := filepath.Join(root, "org")
	repo := filepath.Join(org, "repo")
	pkg := filepath.Join(repo, "pkg")
	mustMkdirAll(t, pkg)
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	return testTree{root: root, org: org, repo: repo, pkg: pkg}
}

// TestScanFilesystemRootScopeSeesAboveGitRoot verifies a ScopeFilesystemRoot
// tool (e.g. Claude Code) picks up an ancestor file ABOVE the detected .git
// boundary, since such tools don't stop walking at the repo root.
func TestScanFilesystemRootScopeSeesAboveGitRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tree := buildTestTree(t)
	mustWriteFile(t, filepath.Join(tree.org, "CLAUDE.md"), "org-wide instructions")
	mustWriteFile(t, filepath.Join(tree.repo, "CLAUDE.md"), "repo instructions")

	results, _, err := Run(tree.pkg, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "claude-code")

	if !hasPathWithContent(r.Files, filepath.Join(tree.org, "CLAUDE.md"), "org-wide instructions") {
		t.Errorf("expected Claude Code to see org-level CLAUDE.md (above git root) at %s; got files: %+v", tree.org, r.Files)
	}
	if !hasPathWithContent(r.Files, filepath.Join(tree.repo, "CLAUDE.md"), "repo instructions") {
		t.Errorf("expected Claude Code to see repo-level CLAUDE.md; got files: %+v", r.Files)
	}
}

// TestScanGitRootScopeStopsAtGitRoot verifies a ScopeGitRoot tool (e.g. Codex
// CLI) does NOT pick up a file located above the detected .git boundary, even
// though the ancestor chain itself extends further up.
func TestScanGitRootScopeStopsAtGitRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tree := buildTestTree(t)
	mustWriteFile(t, filepath.Join(tree.org, "AGENTS.md"), "should not be seen by codex-cli")
	mustWriteFile(t, filepath.Join(tree.repo, "AGENTS.md"), "repo-root agents doc")
	mustWriteFile(t, filepath.Join(tree.pkg, "AGENTS.md"), "pkg-level agents doc")

	results, _, err := Run(tree.pkg, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "codex-cli")

	if hasPath(r.Files, filepath.Join(tree.org, "AGENTS.md")) {
		t.Errorf("codex-cli (ScopeGitRoot) should NOT see above-git-root AGENTS.md; got files: %+v", r.Files)
	}
	if !hasPath(r.Files, filepath.Join(tree.repo, "AGENTS.md")) {
		t.Errorf("codex-cli should see the git-root AGENTS.md; got files: %+v", r.Files)
	}
	if !hasPath(r.Files, filepath.Join(tree.pkg, "AGENTS.md")) {
		t.Errorf("codex-cli should see the target-dir AGENTS.md; got files: %+v", r.Files)
	}

	// Order matters: root-to-target, so repo-level entry must precede
	// pkg-level entry.
	repoIdx := indexOfPath(r.Files, filepath.Join(tree.repo, "AGENTS.md"))
	pkgIdx := indexOfPath(r.Files, filepath.Join(tree.pkg, "AGENTS.md"))
	if repoIdx == -1 || pkgIdx == -1 || repoIdx > pkgIdx {
		t.Errorf("expected repo AGENTS.md (idx %d) before pkg AGENTS.md (idx %d) in root-to-target order", repoIdx, pkgIdx)
	}
}

// TestScanGitRootScopeFallsBackToTargetOnlyWithNoGit verifies the documented
// fallback: a ScopeGitRoot tool with NO .git anywhere in the chain only
// checks the target directory itself.
func TestScanGitRootScopeFallsBackToTargetOnlyWithNoGit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	ancestor := filepath.Join(tmp, "a")
	target := filepath.Join(ancestor, "b")
	mustMkdirAll(t, target)
	mustWriteFile(t, filepath.Join(ancestor, "AGENTS.md"), "ancestor doc, should be invisible")
	mustWriteFile(t, filepath.Join(target, "AGENTS.md"), "target doc")

	results, chain, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if chain.GitRootIndex != -1 {
		t.Fatalf("test setup invalid: expected no .git detected, got GitRootIndex=%d", chain.GitRootIndex)
	}
	r := findResult(t, results, "codex-cli")

	if hasPath(r.Files, filepath.Join(ancestor, "AGENTS.md")) {
		t.Errorf("with no .git found, codex-cli must not see the ancestor's AGENTS.md; got files: %+v", r.Files)
	}
	if !hasPath(r.Files, filepath.Join(target, "AGENTS.md")) {
		t.Errorf("with no .git found, codex-cli must still see the target dir's own AGENTS.md; got files: %+v", r.Files)
	}
}

// TestScanTargetOnlyScopeIgnoresAncestors verifies a ScopeTargetOnly tool
// (e.g. Cursor) never looks at any ancestor directory, even one directly
// above the target with a matching file.
func TestScanTargetOnlyScopeIgnoresAncestors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tree := buildTestTree(t)
	mustWriteFile(t, filepath.Join(tree.repo, "AGENTS.md"), "repo-level, should be invisible to cursor")
	mustWriteFile(t, filepath.Join(tree.pkg, "AGENTS.md"), "target-level, visible to cursor")

	results, _, err := Run(tree.pkg, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "cursor")

	if hasPath(r.Files, filepath.Join(tree.repo, "AGENTS.md")) {
		t.Errorf("cursor (ScopeTargetOnly) should not see ancestor AGENTS.md; got files: %+v", r.Files)
	}
	if !hasPath(r.Files, filepath.Join(tree.pkg, "AGENTS.md")) {
		t.Errorf("cursor should see its own target-dir AGENTS.md; got files: %+v", r.Files)
	}
}

// TestScanNoMatchesAnywhere verifies that with a fully empty tree, every tool
// reports zero files, and Run does not error.
func TestScanNoMatchesAnywhere(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	target := filepath.Join(tmp, "empty", "dir")
	mustMkdirAll(t, target)

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != len(tools.Registry) {
		t.Fatalf("got %d results, want %d (one per registered tool)", len(results), len(tools.Registry))
	}
	for _, r := range results {
		if len(r.Files) != 0 {
			t.Errorf("tool %s: expected zero files in an empty tree, got %+v", r.Tool.Slug, r.Files)
		}
	}
}

// TestScanFirstMatchWinsOnlyOneAncestorContributes verifies OpenCode's
// FirstMatchWins behavior never stacks two ancestor matches: when both an
// outer and an inner directory (within scope) have a match, only one of them
// contributes, never both.
//
// NOTE / POSSIBLE SOURCE BUG: docs/design.md ("nearest project file only")
// and docs/research.md ("First-match-wins per level"; the AGENTS.md spec's
// own "closest one takes precedence" language it cross-references) document
// this as nearest-to-target-wins. But scanTool's actual loop in scan.go
// walks scopedDirs() in root-to-target order and sets firstMatchFound=true
// after processing the FIRST (i.e. outermost/farthest-from-target) directory
// with a match, then breaks -- so it is actually farthest-within-scope-wins,
// not nearest-to-target-wins. This test asserts the CODE'S ACTUAL behavior
// (farthest/outermost match wins) rather than the documented behavior, and
// is deliberately named/commented to flag the discrepancy rather than
// silently encode it as correct. See final report for this being called out
// as a real bug candidate worth fixing in scan.go (e.g. by iterating dirs in
// reverse -- target-to-root -- for FirstMatchWins tools).
func TestScanFirstMatchWinsOnlyOneAncestorContributes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tree := buildTestTree(t)
	mustWriteFile(t, filepath.Join(tree.repo, "AGENTS.md"), "repo-level (farther from target, nearer git root)")
	mustWriteFile(t, filepath.Join(tree.pkg, "AGENTS.md"), "pkg-level (nearest to target)")

	results, _, err := Run(tree.pkg, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "opencode")

	var localMatches int
	for _, f := range r.Files {
		if f.Path == filepath.Join(tree.repo, "AGENTS.md") || f.Path == filepath.Join(tree.pkg, "AGENTS.md") {
			localMatches++
		}
	}
	if localMatches != 1 {
		t.Fatalf("opencode (FirstMatchWins) should contribute exactly 1 local ancestor file (never stacked), got %d: %+v", localMatches, r.Files)
	}
	// As implemented today, scopedDirs()+scanTool() walk root-to-target and
	// stop at the FIRST (outermost) directory with a match -- so the
	// repo-level (farther/outer) file wins here, not the pkg-level (nearer)
	// one. This contradicts the documented "nearest ancestor wins" spec; see
	// the doc comment above.
	if !hasPath(r.Files, filepath.Join(tree.repo, "AGENTS.md")) {
		t.Errorf("opencode's actual (root-to-target, first-found) behavior should pick the outer repo-level AGENTS.md; got files: %+v", r.Files)
	}
	if hasPath(r.Files, filepath.Join(tree.pkg, "AGENTS.md")) {
		t.Errorf("opencode should NOT also include the nearer pkg-level AGENTS.md once an outer match has been found (first-match-wins, not stacked); got files: %+v", r.Files)
	}
}

// TestScanFirstMatchWinsFallsThroughWhenNearestHasNoMatch verifies that if the
// nearest-to-target directory has no match, FirstMatchWins tools fall through
// to the next ancestor that does.
func TestScanFirstMatchWinsFallsThroughWhenNearestHasNoMatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tree := buildTestTree(t)
	// Only repo has a match; pkg (the target) does not.
	mustWriteFile(t, filepath.Join(tree.repo, "AGENTS.md"), "repo-level only")

	results, _, err := Run(tree.pkg, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "opencode")
	if !hasPath(r.Files, filepath.Join(tree.repo, "AGENTS.md")) {
		t.Errorf("opencode should fall through to the repo-level AGENTS.md when target dir has no match; got files: %+v", r.Files)
	}
}

// TestScanGlobalConfigIncludedIndependentOfTarget verifies a tool's global
// config file is checked/included regardless of target-directory content,
// using $HOME redirected to a temp dir for hermeticity.
func TestScanGlobalConfigIncludedIndependentOfTarget(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	mustWriteFile(t, filepath.Join(fakeHome, ".claude", "CLAUDE.md"), "global claude instructions")

	tmp := t.TempDir()
	target := filepath.Join(tmp, "proj")
	mustMkdirAll(t, target)

	results, chain, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if chain.Home != fakeHome {
		t.Fatalf("chain.Home = %q, want fake HOME %q (os.UserHomeDir() must respect $HOME)", chain.Home, fakeHome)
	}
	r := findResult(t, results, "claude-code")
	if !hasPathWithContent(r.Files, filepath.Join(fakeHome, ".claude", "CLAUDE.md"), "global claude instructions") {
		t.Errorf("expected global CLAUDE.md to be included; got files: %+v", r.Files)
	}
	// Global should be first (lowest precedence / listed first per design.md Step 2/4).
	if len(r.Files) == 0 || r.Files[0].Path != filepath.Join(fakeHome, ".claude", "CLAUDE.md") {
		t.Errorf("expected global config to be the first file in application order; got: %+v", r.Files)
	}
}

// TestScanGlobalConfigAbsentWhenHomeUnset verifies no panic/false-positive
// occurs when a fake HOME has no global config files at all -- important so
// tests never accidentally read the real developer machine's dotfiles.
func TestScanGlobalConfigAbsentWhenHomeUnset(t *testing.T) {
	fakeHome := t.TempDir() // deliberately empty
	t.Setenv("HOME", fakeHome)

	tmp := t.TempDir()
	target := filepath.Join(tmp, "proj")
	mustMkdirAll(t, target)

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "claude-code")
	for _, f := range r.Files {
		if f.Path == filepath.Join(fakeHome, ".claude", "CLAUDE.md") {
			t.Errorf("did not expect a global CLAUDE.md match in an empty fake home; got: %+v", f)
		}
	}
}

// TestScanDownwardEagerFindsSubdirectoryFiles verifies a DownwardEager tool
// (e.g. Gemini CLI) picks up matching files in subdirectories below target.
func TestScanDownwardEagerFindsSubdirectoryFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	target := filepath.Join(tmp, "proj")
	sub := filepath.Join(target, "pkg", "nested")
	mustMkdirAll(t, sub)
	mustWriteFile(t, filepath.Join(sub, "GEMINI.md"), "nested gemini instructions")

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "gemini-cli")
	if !hasPath(r.Files, filepath.Join(sub, "GEMINI.md")) {
		t.Errorf("gemini-cli (DownwardEager) should find subdirectory GEMINI.md; got files: %+v", r.Files)
	}
}

// TestScanDownwardNoneIgnoresSubdirectories verifies a DownwardNone tool
// (e.g. Codex CLI) never looks below the target directory.
func TestScanDownwardNoneIgnoresSubdirectories(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	target := filepath.Join(tmp, "proj")
	sub := filepath.Join(target, "pkg")
	mustMkdirAll(t, sub)
	mustWriteFile(t, filepath.Join(sub, "AGENTS.md"), "nested, should be invisible")

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "codex-cli")
	if hasPath(r.Files, filepath.Join(sub, "AGENTS.md")) {
		t.Errorf("codex-cli (DownwardNone) should not see subdirectory AGENTS.md; got files: %+v", r.Files)
	}
}

// TestScanDownwardEagerSkipsNoiseDirectories verifies scanDownward skips
// common noise directories (.git, node_modules, vendor, etc.) rather than
// descending into them.
func TestScanDownwardEagerSkipsNoiseDirectories(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	target := filepath.Join(tmp, "proj")
	mustMkdirAll(t, target)
	mustWriteFile(t, filepath.Join(target, "node_modules", "somepkg", "GEMINI.md"), "should be skipped")
	mustWriteFile(t, filepath.Join(target, ".git", "GEMINI.md"), "should be skipped")
	mustWriteFile(t, filepath.Join(target, "vendor", "GEMINI.md"), "should be skipped")
	mustWriteFile(t, filepath.Join(target, "keep", "GEMINI.md"), "should be found")

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "gemini-cli")
	if hasPath(r.Files, filepath.Join(target, "node_modules", "somepkg", "GEMINI.md")) {
		t.Error("should not descend into node_modules")
	}
	if hasPath(r.Files, filepath.Join(target, ".git", "GEMINI.md")) {
		t.Error("should not descend into .git")
	}
	if hasPath(r.Files, filepath.Join(target, "vendor", "GEMINI.md")) {
		t.Error("should not descend into vendor")
	}
	if !hasPath(r.Files, filepath.Join(target, "keep", "GEMINI.md")) {
		t.Error("should still find files in ordinary subdirectories")
	}
}

// TestScanOverrideFileWinsWithinSameDirectory verifies Codex CLI's
// AGENTS.override.md is included alongside/instead-of AGENTS.md within the
// same directory per its LocalFiles entries (scan.go itself just matches all
// declared LocalFiles patterns; the "override wins" semantics for content
// composition are compile.go's job, but scan must still find both files so
// compile can apply that rule).
func TestScanFindsBothOverrideAndBaseFileInSameDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	target := filepath.Join(tmp, "proj")
	mustMkdirAll(t, target)
	mustWriteFile(t, filepath.Join(target, "AGENTS.md"), "base")
	mustWriteFile(t, filepath.Join(target, "AGENTS.override.md"), "override")

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "codex-cli")
	if !hasPath(r.Files, filepath.Join(target, "AGENTS.md")) {
		t.Errorf("expected AGENTS.md to be matched; got: %+v", r.Files)
	}
	if !hasPath(r.Files, filepath.Join(target, "AGENTS.override.md")) {
		t.Errorf("expected AGENTS.override.md to be matched; got: %+v", r.Files)
	}
}

// TestScanAllToolsAlwaysPresentInResults verifies Run always returns exactly
// one ToolResult per registered tool, matching registry order, regardless of
// what's found on disk.
func TestScanAllToolsAlwaysPresentInResults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	results, _, err := Run(tmp, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != len(tools.Registry) {
		t.Fatalf("got %d results, want %d", len(results), len(tools.Registry))
	}
	for i, r := range results {
		if r.Tool.Slug != tools.Registry[i].Slug {
			t.Errorf("results[%d].Tool.Slug = %q, want %q (registry order should be preserved)", i, r.Tool.Slug, tools.Registry[i].Slug)
		}
	}
}

// TestScanRelativeTargetIsResolvedAbsolute verifies that Run/BuildChain
// correctly resolve a relative target path to an absolute one (since
// MatchedFile.Path and Chain.Dirs are documented/used as absolute paths
// throughout render/compile).
func TestScanRelativeTargetIsResolvedAbsolute(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "sub")
	mustMkdirAll(t, sub)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Fatalf("Chdir back to %q: %v", oldWd, err)
		}
	}()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir(%q): %v", tmp, err)
	}

	chain, err := BuildChain("sub")
	if err != nil {
		t.Fatalf("BuildChain: %v", err)
	}
	last := chain.Dirs[len(chain.Dirs)-1]
	if !filepath.IsAbs(last) {
		t.Errorf("expected resolved target to be absolute, got %q", last)
	}
	// Compute the expected path via the POST-chdir working directory rather
	// than the pre-chdir tmp variable: on macOS, t.TempDir() paths live under
	// a /var symlink that resolves to /private/var once it becomes the
	// actual process cwd, so filepath.Abs("sub") after chdir may legitimately
	// differ textually from filepath.Join(tmp, "sub") computed beforehand.
	postChdirWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd after chdir: %v", err)
	}
	wantAbs := filepath.Clean(filepath.Join(postChdirWd, "sub"))
	if last != wantAbs {
		t.Errorf("resolved target = %q, want %q", last, wantAbs)
	}
}

// ---- expandCandidate / doublestar glob expansion ---------------------------

// TestExpandCandidatePlainPath verifies a plain (non-glob) path is returned
// only if it exists as a regular file.
func TestExpandCandidatePlainPath(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "CLAUDE.md")
	mustWriteFile(t, f, "hi")

	got := expandCandidate(f)
	if len(got) != 1 || got[0] != f {
		t.Errorf("expandCandidate(%q) = %v, want [%q]", f, got, f)
	}

	missing := filepath.Join(tmp, "NOPE.md")
	if got := expandCandidate(missing); got != nil {
		t.Errorf("expandCandidate(%q) = %v, want nil for nonexistent file", missing, got)
	}

	// A directory (not a regular file) at that path should not match.
	dirPath := filepath.Join(tmp, "adir")
	mustMkdirAll(t, dirPath)
	if got := expandCandidate(dirPath); got != nil {
		t.Errorf("expandCandidate(%q) = %v, want nil for a directory", dirPath, got)
	}
}

// TestExpandCandidateSimpleGlob verifies a single-"*" glob (no doublestar)
// expands via filepath.Glob and only returns regular files, sorted.
func TestExpandCandidateSimpleGlob(t *testing.T) {
	tmp := t.TempDir()
	mustWriteFile(t, filepath.Join(tmp, "rules", "b.md"), "b")
	mustWriteFile(t, filepath.Join(tmp, "rules", "a.md"), "a")
	mustMkdirAll(t, filepath.Join(tmp, "rules", "subdir")) // should not match *.md

	got := expandCandidate(filepath.Join(tmp, "rules", "*.md"))
	want := []string{filepath.Join(tmp, "rules", "a.md"), filepath.Join(tmp, "rules", "b.md")}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExpandCandidateDoublestarRecursesSubdirectories verifies "**" segments
// recurse through zero or more directories, matching Copilot's
// ".github/instructions/**/*.instructions.md" convention.
func TestExpandCandidateDoublestarRecursesSubdirectories(t *testing.T) {
	tmp := t.TempDir()
	base := filepath.Join(tmp, ".github", "instructions")
	mustWriteFile(t, filepath.Join(base, "top.instructions.md"), "top")
	mustWriteFile(t, filepath.Join(base, "nested", "deep.instructions.md"), "deep")
	mustWriteFile(t, filepath.Join(base, "ignored.txt"), "not matched, wrong suffix")

	pattern := filepath.Join(base, "**", "*.instructions.md")
	got := expandCandidate(pattern)

	if !containsString(got, filepath.Join(base, "top.instructions.md")) {
		t.Errorf("expected doublestar match for zero-directories case; got %v", got)
	}
	if !containsString(got, filepath.Join(base, "nested", "deep.instructions.md")) {
		t.Errorf("expected doublestar match for nested subdirectory; got %v", got)
	}
	if containsString(got, filepath.Join(base, "ignored.txt")) {
		t.Errorf("did not expect a non-matching-suffix file in results; got %v", got)
	}
}

// TestExpandCandidateDoublestarNoBaseDir verifies no panic and empty results
// when the base directory (before "**") doesn't exist at all.
func TestExpandCandidateDoublestarNoBaseDir(t *testing.T) {
	tmp := t.TempDir()
	pattern := filepath.Join(tmp, "nonexistent", "**", "*.md")
	got := expandCandidate(pattern)
	if got != nil {
		t.Errorf("expandCandidate on missing base dir = %v, want nil", got)
	}
}

// ---- test helpers -----------------------------------------------------------

func findResult(t *testing.T, results []ToolResult, slug string) ToolResult {
	t.Helper()
	for _, r := range results {
		if r.Tool.Slug == slug {
			return r
		}
	}
	t.Fatalf("no ToolResult found for slug %q", slug)
	return ToolResult{}
}

func hasPath(files []MatchedFile, path string) bool {
	return indexOfPath(files, path) != -1
}

func indexOfPath(files []MatchedFile, path string) int {
	for i, f := range files {
		if f.Path == path {
			return i
		}
	}
	return -1
}

func hasPathWithContent(files []MatchedFile, path, content string) bool {
	for _, f := range files {
		if f.Path == path {
			return f.Content == content
		}
	}
	return false
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
