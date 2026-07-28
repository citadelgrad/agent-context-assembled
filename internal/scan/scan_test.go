package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/citadelgrad/actx/internal/tools"
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

// ---- symlink resolution -----------------------------------------------------

// TestBuildChainResolvesSymlinkedAncestorToPhysicalPath verifies BuildChain
// resolves a target reached through a symlinked ancestor directory to its
// physical path before walking, matching how a real coding-agent process
// resolves its cwd at runtime (e.g. Node's process.cwd()). This is the direct
// regression test for actx-87l: /Users/scott/projects/actx (logical, via a
// symlinked "projects" dir) must model the same ancestor chain as
// /Volumes/qwiizlab/projects/actx (physical).
func TestBuildChainResolvesSymlinkedAncestorToPhysicalPath(t *testing.T) {
	tmp := t.TempDir()
	physicalRoot := filepath.Join(tmp, "physical")
	physicalTarget := filepath.Join(physicalRoot, "projects", "actx")
	mustMkdirAll(t, physicalTarget)

	logicalRoot := filepath.Join(tmp, "logical")
	mustMkdirAll(t, tmp)
	if err := os.Symlink(filepath.Join(physicalRoot, "projects"), logicalRoot); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	logicalTarget := filepath.Join(logicalRoot, "actx")

	physicalChain, err := BuildChain(physicalTarget)
	if err != nil {
		t.Fatalf("BuildChain(physical): %v", err)
	}
	logicalChain, err := BuildChain(logicalTarget)
	if err != nil {
		t.Fatalf("BuildChain(logical): %v", err)
	}

	if len(logicalChain.Dirs) != len(physicalChain.Dirs) {
		t.Fatalf("logical chain has %d dirs, physical has %d; want identical chains: logical=%v physical=%v",
			len(logicalChain.Dirs), len(physicalChain.Dirs), logicalChain.Dirs, physicalChain.Dirs)
	}
	for i := range physicalChain.Dirs {
		if logicalChain.Dirs[i] != physicalChain.Dirs[i] {
			t.Errorf("Dirs[%d]: logical=%q, physical=%q; want identical (physical) paths", i, logicalChain.Dirs[i], physicalChain.Dirs[i])
		}
	}
	last := logicalChain.Dirs[len(logicalChain.Dirs)-1]
	if last != physicalTarget {
		t.Errorf("resolved target = %q, want physical path %q (no logical/symlink spelling)", last, physicalTarget)
	}
}

// TestScanExcludesCLAUDEMdOnlyOnLogicalAncestry is the end-to-end regression
// test for actx-87l: a CLAUDE.md placed only on the logical (symlink)
// ancestry -- above the symlink itself, never reachable by walking the
// physical tree -- must NOT appear in Claude Code's results when scanning via
// the symlinked path, while a CLAUDE.md on the physical ancestry must still
// appear. This models the exact bug report scenario: /Users/scott/projects is
// a symlink to /Volumes/qwiizlab/projects, /Users/scott/CLAUDE.md exists (logical
// ancestor only), and the physical workspace CLAUDE.md must still be included.
func TestScanExcludesCLAUDEMdOnlyOnLogicalAncestry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()

	// Physical tree: /tmp/.../volumes/qwiizlab/projects/actx
	volumes := filepath.Join(tmp, "volumes")
	physicalWorkspace := filepath.Join(volumes, "qwiizlab", "projects")
	physicalTarget := filepath.Join(physicalWorkspace, "actx")
	mustMkdirAll(t, physicalTarget)
	mustWriteFile(t, filepath.Join(physicalWorkspace, "CLAUDE.md"), "physical workspace instructions")

	// Logical tree: /tmp/.../users/scott/CLAUDE.md (ancestor of the symlink,
	// never reachable once "projects" resolves to the physical dir) and
	// /tmp/.../users/scott/projects -> physical "qwiizlab/projects" symlink.
	usersScott := filepath.Join(tmp, "users", "scott")
	mustMkdirAll(t, usersScott)
	mustWriteFile(t, filepath.Join(usersScott, "CLAUDE.md"), "logical-only ancestor instructions -- must be excluded")
	logicalProjects := filepath.Join(usersScott, "projects")
	if err := os.Symlink(physicalWorkspace, logicalProjects); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	logicalTarget := filepath.Join(logicalProjects, "actx")

	results, _, err := Run(logicalTarget, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "claude-code")

	if hasPath(r.Files, filepath.Join(usersScott, "CLAUDE.md")) {
		t.Errorf("logical-only ancestor CLAUDE.md (%s) must be excluded once the symlinked target resolves to its physical path; got files: %+v", filepath.Join(usersScott, "CLAUDE.md"), r.Files)
	}
	if !hasPathWithContent(r.Files, filepath.Join(physicalWorkspace, "CLAUDE.md"), "physical workspace instructions") {
		t.Errorf("expected physical workspace CLAUDE.md to be included; got files: %+v", r.Files)
	}
}

// TestScanSymlinkedTargetIncludesGlobalAndPhysicalInOrder verifies AC3: with a
// symlinked target, the global ~/.claude/CLAUDE.md and the physical
// workspace/repo CLAUDE.md files are both still included, in correct
// precedence order (global first, then ancestors root-to-target).
func TestScanSymlinkedTargetIncludesGlobalAndPhysicalInOrder(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	mustWriteFile(t, filepath.Join(fakeHome, ".claude", "CLAUDE.md"), "global claude instructions")

	tmp := t.TempDir()
	physicalRepo := filepath.Join(tmp, "physicalrepo")
	physicalTarget := filepath.Join(physicalRepo, "pkg")
	mustMkdirAll(t, physicalTarget)
	mustMkdirAll(t, filepath.Join(physicalRepo, ".git"))
	mustWriteFile(t, filepath.Join(physicalRepo, "CLAUDE.md"), "physical repo instructions")

	symlinkDir := filepath.Join(tmp, "alias")
	if err := os.Symlink(physicalRepo, symlinkDir); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	logicalTarget := filepath.Join(symlinkDir, "pkg")

	results, _, err := Run(logicalTarget, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "claude-code")

	globalIdx := indexOfPath(r.Files, filepath.Join(fakeHome, ".claude", "CLAUDE.md"))
	repoIdx := indexOfPath(r.Files, filepath.Join(physicalRepo, "CLAUDE.md"))
	if globalIdx == -1 {
		t.Fatalf("expected global CLAUDE.md to be included; got files: %+v", r.Files)
	}
	if repoIdx == -1 {
		t.Fatalf("expected physical repo CLAUDE.md to be included; got files: %+v", r.Files)
	}
	if globalIdx > repoIdx {
		t.Errorf("expected global CLAUDE.md (idx %d) before physical repo CLAUDE.md (idx %d)", globalIdx, repoIdx)
	}
}

// TestBuildChainNonSymlinkTargetUnaffected verifies AC4 (no regression): a
// target with no symlinks anywhere in its path resolves to exactly the same
// chain as before (filepath.Abs + Clean), since EvalSymlinks on an
// already-physical path is a no-op.
func TestBuildChainNonSymlinkTargetUnaffected(t *testing.T) {
	tmp := t.TempDir()
	nested := filepath.Join(tmp, "a", "b", "c")
	mustMkdirAll(t, nested)

	chain, err := BuildChain(nested)
	if err != nil {
		t.Fatalf("BuildChain: %v", err)
	}
	last := chain.Dirs[len(chain.Dirs)-1]
	wantTarget, _ := filepath.Abs(nested)
	wantTarget = filepath.Clean(wantTarget)
	if last != wantTarget {
		t.Errorf("resolved target = %q, want %q (non-symlink target must be byte-for-byte unaffected)", last, wantTarget)
	}
}

// TestBuildChainBrokenSymlinkReturnsError verifies AC5: a target that is (or
// is reached through) a broken/unresolvable symlink returns an error from
// BuildChain -- the same structured-error path Run/main already use for any
// other unusable target -- instead of silently producing a partial chain.
func TestBuildChainBrokenSymlinkReturnsError(t *testing.T) {
	tmp := t.TempDir()
	broken := filepath.Join(tmp, "broken-link")
	if err := os.Symlink(filepath.Join(tmp, "does-not-exist"), broken); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if _, err := BuildChain(broken); err == nil {
		t.Fatal("BuildChain(broken symlink) = nil error, want an error")
	}

	// Same via the target-only Run entry point, so the error is confirmed to
	// propagate out of the public API a caller (main.go) actually uses.
	if _, _, err := Run(broken, Options{}); err == nil {
		t.Fatal("Run(broken symlink) = nil error, want an error")
	}
}

// TestBuildChainBrokenSymlinkedAncestorReturnsError covers the ancestor-only
// variant of AC5: the target directory itself is real, but an ancestor
// directory on its path is a broken symlink, so the full path cannot resolve.
func TestBuildChainBrokenSymlinkedAncestorReturnsError(t *testing.T) {
	tmp := t.TempDir()
	broken := filepath.Join(tmp, "broken-link")
	if err := os.Symlink(filepath.Join(tmp, "does-not-exist"), broken); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	target := filepath.Join(broken, "sub")

	if _, err := BuildChain(target); err == nil {
		t.Fatal("BuildChain(target under broken symlinked ancestor) = nil error, want an error")
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
// outer and an inner directory (within scope) have a match, only the nearer
// one (closest to target) contributes, per docs/design.md's "nearest project
// file only" / docs/research.md's "First-match-wins per level".
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
	if !hasPath(r.Files, filepath.Join(tree.pkg, "AGENTS.md")) {
		t.Errorf("opencode should pick the nearer pkg-level AGENTS.md (nearest-to-target wins); got files: %+v", r.Files)
	}
	if hasPath(r.Files, filepath.Join(tree.repo, "AGENTS.md")) {
		t.Errorf("opencode should NOT include the farther repo-level AGENTS.md once a nearer match has been found (first-match-wins, not stacked); got files: %+v", r.Files)
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

// TestScanDownwardEagerDoesNotDuplicateTargetDirOwnFile verifies a
// DownwardEager tool's own-directory match (already found by the
// ancestor-chain loop in scanTool) isn't re-added a second time by
// scanDownward's walk, which also visits target itself at depth 0.
func TestScanDownwardEagerDoesNotDuplicateTargetDirOwnFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	target := filepath.Join(tmp, "proj")
	mustMkdirAll(t, target)
	mustWriteFile(t, filepath.Join(target, "GEMINI.md"), "gemini instructions in target dir")

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "gemini-cli")
	path := filepath.Join(target, "GEMINI.md")
	count := 0
	for _, f := range r.Files {
		if f.Path == path {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected target dir's own GEMINI.md to appear exactly once, got %d; files: %+v", count, r.Files)
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

// TestScanDownwardEagerSkipsTargetDirectory verifies scanDownward skips a
// directory literally named "target" (Rust/Cargo build output), the same way
// it already skips .git, node_modules, and vendor (actx-7vc), while still
// finding legitimate matches in sibling, non-skipped subdirectories.
func TestScanDownwardEagerSkipsTargetDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	target := filepath.Join(tmp, "proj")
	mustMkdirAll(t, target)
	mustWriteFile(t, filepath.Join(target, "target", "debug", "GEMINI.md"), "should be skipped")
	mustWriteFile(t, filepath.Join(target, "keep", "GEMINI.md"), "should be found")

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "gemini-cli")
	if hasPath(r.Files, filepath.Join(target, "target", "debug", "GEMINI.md")) {
		t.Error("should not descend into target/ (Rust/Cargo build output)")
	}
	if !hasPath(r.Files, filepath.Join(target, "keep", "GEMINI.md")) {
		t.Error("should still find files in ordinary sibling subdirectories")
	}
}

// TestScanDownwardEagerDoesNotSkipTargetsLookalikeWithoutTag verifies a
// directory merely named similarly to "target" (e.g. "targets", plural) is
// walked normally: the skip is an exact-name match, not a prefix match, and
// there's no CACHEDIR.TAG here either.
func TestScanDownwardEagerDoesNotSkipTargetsLookalikeWithoutTag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	target := filepath.Join(tmp, "proj")
	mustMkdirAll(t, target)
	mustWriteFile(t, filepath.Join(target, "targets", "GEMINI.md"), "should be found: \"targets\" != \"target\"")

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "gemini-cli")
	if !hasPath(r.Files, filepath.Join(target, "targets", "GEMINI.md")) {
		t.Errorf(`gemini-cli should still walk into "targets" (not exactly "target", no CACHEDIR.TAG); got files: %+v`, r.Files)
	}
}

// TestScanDownwardEagerSkipsCachedirTaggedDirectory verifies scanDownward
// generically skips any directory containing a CACHEDIR.TAG regular file
// directly inside it (the Cache Directory Tagging Standard, actx-7vc),
// covering build/cache dirs beyond the hardcoded name list -- while still
// finding legitimate matches in sibling, non-skipped subdirectories.
func TestScanDownwardEagerSkipsCachedirTaggedDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	target := filepath.Join(tmp, "proj")
	cacheDir := filepath.Join(target, "buildcache")
	mustMkdirAll(t, cacheDir)
	mustWriteFile(t, filepath.Join(cacheDir, "CACHEDIR.TAG"), "Signature: 8a477f597d28d172789f06886806bc55\n")
	mustWriteFile(t, filepath.Join(cacheDir, "nested", "GEMINI.md"), "should be skipped")
	mustWriteFile(t, filepath.Join(target, "keep", "GEMINI.md"), "should be found")

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "gemini-cli")
	if hasPath(r.Files, filepath.Join(cacheDir, "nested", "GEMINI.md")) {
		t.Error("should not descend into a CACHEDIR.TAG-tagged directory")
	}
	if !hasPath(r.Files, filepath.Join(target, "keep", "GEMINI.md")) {
		t.Error("should still find files in ordinary sibling subdirectories")
	}
}

// TestScanDownwardEagerCachedirTagMustBeDirectChild verifies the CACHEDIR.TAG
// check only fires when the tag file sits directly inside the candidate
// directory (not an ancestor or descendant) -- a tag file one level deeper,
// inside a grandchild rather than the child itself, must not cause the child
// to be skipped.
func TestScanDownwardEagerCachedirTagMustBeDirectChild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	target := filepath.Join(tmp, "proj")
	mustMkdirAll(t, target)
	// CACHEDIR.TAG lives in outer/nested/, not directly in outer/ itself --
	// outer/ must still be walked normally.
	mustWriteFile(t, filepath.Join(target, "outer", "nested", "CACHEDIR.TAG"), "Signature: 8a477f597d28d172789f06886806bc55\n")
	mustWriteFile(t, filepath.Join(target, "outer", "GEMINI.md"), "should be found: CACHEDIR.TAG is not directly in outer/")

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "gemini-cli")
	if !hasPath(r.Files, filepath.Join(target, "outer", "GEMINI.md")) {
		t.Errorf("outer/ should be walked normally since CACHEDIR.TAG is not directly inside it; got files: %+v", r.Files)
	}
}

// TestScanDownwardEagerCachedirTagMustBeRegularFile verifies a directory
// literally named CACHEDIR.TAG does not tag its parent as a cache directory
// -- only a regular file does, per the Cache Directory Tagging Standard.
func TestScanDownwardEagerCachedirTagMustBeRegularFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	target := filepath.Join(tmp, "proj")
	mustMkdirAll(t, target)
	// CACHEDIR.TAG is a directory here, not a regular file -- must not trigger the skip.
	mustWriteFile(t, filepath.Join(target, "weird", "CACHEDIR.TAG", "placeholder.txt"), "irrelevant")
	mustWriteFile(t, filepath.Join(target, "weird", "GEMINI.md"), "should be found")

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "gemini-cli")
	if !hasPath(r.Files, filepath.Join(target, "weird", "GEMINI.md")) {
		t.Errorf("weird/ should be walked normally since CACHEDIR.TAG there is a directory, not a regular file; got files: %+v", r.Files)
	}
}

// TestScanDownwardEagerSkipsLargeTargetDirectoryFast is a regression test for
// actx-7vc: pre-fix, scanDownward walked into Rust's target/ build directory
// and only stopped after hitting its maxDirsVisited=500 cap, having opened
// hundreds of subdirectories (one os.ReadDir syscall each) for zero matched
// files. This builds a target/ directory with more than 500 subdirectories
// directly inside it, each holding a "trap" GEMINI.md one level deeper that
// must never be reached, and asserts the scan both returns quickly and never
// descends past target/ itself.
func TestScanDownwardEagerSkipsLargeTargetDirectoryFast(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	target := filepath.Join(tmp, "proj")
	mustMkdirAll(t, target)

	const numEntries = 600
	for i := 0; i < numEntries; i++ {
		mustWriteFile(t, filepath.Join(target, "target", fmt.Sprintf("artifact%d", i), "GEMINI.md"), "should never be reached")
	}

	start := time.Now()
	results, _, err := Run(target, Options{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("scanDownward took %s for a target/ dir with %d entries; expected it to skip target/ by name and return quickly", elapsed, numEntries)
	}

	r := findResult(t, results, "gemini-cli")
	for i := 0; i < numEntries; i++ {
		trap := filepath.Join(target, "target", fmt.Sprintf("artifact%d", i), "GEMINI.md")
		if hasPath(r.Files, trap) {
			t.Fatalf("scanDownward descended into target/artifact%d despite the target/ skip; found %s", i, trap)
		}
	}
}

// TestScanDownwardEagerSkipsLargeCachedirTaggedDirectoryFast is the
// CACHEDIR.TAG counterpart of TestScanDownwardEagerSkipsLargeTargetDirectoryFast
// (actx-7vc): a directory that isn't named "target" but is tagged with
// CACHEDIR.TAG and contains more than 500 entries must still be skipped
// quickly, without descending into any of them.
func TestScanDownwardEagerSkipsLargeCachedirTaggedDirectoryFast(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	target := filepath.Join(tmp, "proj")
	cacheDir := filepath.Join(target, "buildcache")
	mustMkdirAll(t, cacheDir)
	mustWriteFile(t, filepath.Join(cacheDir, "CACHEDIR.TAG"), "Signature: 8a477f597d28d172789f06886806bc55\n")

	const numEntries = 600
	for i := 0; i < numEntries; i++ {
		mustWriteFile(t, filepath.Join(cacheDir, fmt.Sprintf("artifact%d", i), "GEMINI.md"), "should never be reached")
	}

	start := time.Now()
	results, _, err := Run(target, Options{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("scanDownward took %s for a CACHEDIR.TAG-tagged dir with %d entries; expected a fast skip", elapsed, numEntries)
	}

	r := findResult(t, results, "gemini-cli")
	for i := 0; i < numEntries; i++ {
		trap := filepath.Join(cacheDir, fmt.Sprintf("artifact%d", i), "GEMINI.md")
		if hasPath(r.Files, trap) {
			t.Fatalf("scanDownward descended into the CACHEDIR.TAG-tagged dir despite the tag; found %s", trap)
		}
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

// ---- Hermes tool (real tools.Registry entry) end-to-end scan tests --------
//
// These tests drive the REAL "hermes" entry in tools.Registry (defined in
// internal/tools/tools.go) through BuildChain/Run end-to-end, per actx-3l6,
// rather than a synthetic fixture tool like the tests above. They pin down
// Hermes's documented 5-filename local-file priority order
// (.hermes.md/HERMES.md > AGENTS.md > CLAUDE.md > .cursorrules), its
// ScopeTargetOnly scope (no ancestor walk), and its ~/.hermes/SOUL.md
// GlobalConfig.

// TestScanHermesHighestPriorityLocalFileWinsWhenMultiplePresent verifies
// that when both .hermes.md and AGENTS.md exist in the target directory,
// only .hermes.md (the higher-priority local file) is matched.
func TestScanHermesHighestPriorityLocalFileWinsWhenMultiplePresent(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	target := t.TempDir()
	mustWriteFile(t, filepath.Join(target, ".hermes.md"), "hermes-native content")
	mustWriteFile(t, filepath.Join(target, "AGENTS.md"), "agents compat content")

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "hermes")

	if len(r.Files) != 1 {
		t.Fatalf("got %d hermes files, want exactly 1 (first-match-wins within a directory): %+v", len(r.Files), r.Files)
	}
	want := filepath.Join(target, ".hermes.md")
	if r.Files[0].Path != want {
		t.Errorf("Files[0].Path = %q, want %q (.hermes.md is highest priority)", r.Files[0].Path, want)
	}
	if hasPath(r.Files, filepath.Join(target, "AGENTS.md")) {
		t.Errorf("AGENTS.md should not be included once .hermes.md (higher priority) has matched; got files: %+v", r.Files)
	}
}

// TestScanHermesPriorityOrderRespectedAcrossAllFilenames verifies that with
// AGENTS.md, CLAUDE.md, and .cursorrules all present (but no .hermes.md or
// HERMES.md), only AGENTS.md -- the highest-priority filename among those
// present -- is matched.
func TestScanHermesPriorityOrderRespectedAcrossAllFilenames(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	target := t.TempDir()
	mustWriteFile(t, filepath.Join(target, "AGENTS.md"), "agents content")
	mustWriteFile(t, filepath.Join(target, "CLAUDE.md"), "claude content")
	mustWriteFile(t, filepath.Join(target, ".cursorrules"), "cursorrules content")

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "hermes")

	if len(r.Files) != 1 {
		t.Fatalf("got %d hermes files, want exactly 1 (first-match-wins within a directory): %+v", len(r.Files), r.Files)
	}
	want := filepath.Join(target, "AGENTS.md")
	if r.Files[0].Path != want {
		t.Errorf("Files[0].Path = %q, want %q (AGENTS.md is highest priority among those present)", r.Files[0].Path, want)
	}
}

// TestScanHermesLowestPriorityFileUsedWhenOnlyOnePresent verifies that
// .cursorrules (the lowest-priority Hermes local file) is used when it is
// the only Hermes candidate file present.
func TestScanHermesLowestPriorityFileUsedWhenOnlyOnePresent(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	target := t.TempDir()
	mustWriteFile(t, filepath.Join(target, ".cursorrules"), "cursorrules only content")

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "hermes")
	want := filepath.Join(target, ".cursorrules")
	if !hasPathWithContent(r.Files, want, "cursorrules only content") {
		t.Errorf("expected .cursorrules to be used when it's the only Hermes file present; got files: %+v", r.Files)
	}
	if len(r.Files) != 1 {
		t.Errorf("expected exactly 1 hermes match, got %d: %+v", len(r.Files), r.Files)
	}
}

// TestScanHermesScopeTargetOnlyIgnoresAncestors verifies Hermes's
// ScopeTargetOnly scope never looks at an ancestor directory, even one
// directly above the target with a matching HERMES.md file.
func TestScanHermesScopeTargetOnlyIgnoresAncestors(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	tree := buildTestTree(t)
	mustWriteFile(t, filepath.Join(tree.repo, "HERMES.md"), "repo-level, should be invisible to hermes")

	results, _, err := Run(tree.pkg, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "hermes")
	if hasPath(r.Files, filepath.Join(tree.repo, "HERMES.md")) {
		t.Errorf("hermes (ScopeTargetOnly) should not see ancestor HERMES.md; got files: %+v", r.Files)
	}
	if len(r.Files) != 0 {
		t.Errorf("expected zero hermes matches (no local target-dir file, no global config, empty fake home); got: %+v", r.Files)
	}
}

// TestScanHermesGlobalSoulIncludedIndependentOfTargetContents verifies the
// global ~/.hermes/SOUL.md GlobalConfig is included even when the target
// directory has no local Hermes files at all.
func TestScanHermesGlobalSoulIncludedIndependentOfTargetContents(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	mustWriteFile(t, filepath.Join(fakeHome, ".hermes", "SOUL.md"), "agent soul/personality")

	target := t.TempDir()

	results, chain, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if chain.Home != fakeHome {
		t.Fatalf("chain.Home = %q, want %q", chain.Home, fakeHome)
	}
	r := findResult(t, results, "hermes")
	want := filepath.Join(fakeHome, ".hermes", "SOUL.md")
	if !hasPathWithContent(r.Files, want, "agent soul/personality") {
		t.Errorf("expected global SOUL.md to be included even with no local target-dir files; got: %+v", r.Files)
	}
	idx := indexOfPath(r.Files, want)
	if idx == -1 {
		t.Fatalf("SOUL.md not found in files: %+v", r.Files)
	}
	if !strings.HasPrefix(r.Files[idx].Note, "global:") {
		t.Errorf("Note = %q, want a %q-prefixed note for the global SOUL.md match", r.Files[idx].Note, "global:")
	}
}

// TestScanHermesNoFilesAnywhereMeansNoMatchesNoError verifies that with
// nothing on disk anywhere (no local files, no global config), the hermes
// tool reports zero matches and Run does not error.
func TestScanHermesNoFilesAnywhereMeansNoMatchesNoError(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	target := t.TempDir()

	results, _, err := Run(target, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := findResult(t, results, "hermes")
	if len(r.Files) != 0 {
		t.Errorf("expected zero hermes matches with nothing on disk anywhere; got: %+v", r.Files)
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
