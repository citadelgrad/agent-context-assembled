package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/citadelgrad/agent-context-assembled/internal/tools"
)

func TestBuildChainPhysicalAncestorsAndNearestGit(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "physical", "repo", "target")
	if err := os.MkdirAll(physical, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "physical", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "logical")
	if err := os.Symlink(filepath.Join(root, "physical"), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	physical, err := filepath.EvalSymlinks(physical)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := BuildChain(filepath.Join(link, "repo", "target"))
	if err != nil {
		t.Fatal(err)
	}
	if len(chain.Dirs) == 0 || chain.Dirs[len(chain.Dirs)-1] != physical || !filepath.IsAbs(chain.Dirs[0]) {
		t.Fatalf("invalid physical chain: %+v", chain)
	}
	for i := 1; i < len(chain.Dirs); i++ {
		if filepath.Dir(chain.Dirs[i]) != chain.Dirs[i-1] {
			t.Fatalf("non-parent adjacency: %v", chain.Dirs)
		}
	}
	physicalRoot := filepath.Dir(filepath.Dir(physical))
	if chain.GitRootIndex < 0 || chain.Dirs[chain.GitRootIndex] != physicalRoot {
		t.Fatalf("wrong outer git boundary: %+v", chain)
	}
	if err := os.MkdirAll(filepath.Join(physicalRoot, "repo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	chain, err = BuildChain(filepath.Join(link, "repo", "target"))
	if err != nil {
		t.Fatal(err)
	}
	if chain.Dirs[chain.GitRootIndex] != filepath.Join(physicalRoot, "repo") {
		t.Fatalf("nearer git marker not selected: %+v", chain)
	}
}

func TestScanDownwardSafetyDeterminismAndBoundaries(t *testing.T) {
	root := t.TempDir()
	tool := tools.Tool{LocalFiles: []tools.LocalFile{{Pattern: "RULE.md", Note: "rule"}}}
	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "RULE.md"))
	for _, dir := range []string{"ok", ".hidden", "node_modules", "cache"} {
		write(filepath.Join(root, dir, "RULE.md"))
	}
	write(filepath.Join(root, "cache", "CACHEDIR.TAG"))
	cur := root
	for depth := 1; depth <= 7; depth++ {
		cur = filepath.Join(cur, "d")
		write(filepath.Join(cur, "RULE.md"))
	}
	got := scanDownward(tool, root)
	again := scanDownward(tool, root)
	if !reflect.DeepEqual(got, again) || !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].Path < got[j].Path }) {
		t.Fatal("downward scan not sorted/deterministic")
	}
	seen := map[string]bool{}
	for _, file := range got {
		if seen[file.Path] {
			t.Fatal("duplicate")
		}
		seen[file.Path] = true
		rel, _ := filepath.Rel(root, file.Path)
		if rel == "RULE.md" || strings.HasPrefix(rel, "..") || strings.Contains(rel, ".hidden") || strings.Contains(rel, "node_modules") || strings.Contains(rel, "cache") {
			t.Fatalf("unsafe/ineligible result %q", rel)
		}
	}
	if !seen[filepath.Join(root, "ok", "RULE.md")] {
		t.Fatal("eligible file missing")
	}
	depth6 := root
	for i := 0; i < 6; i++ {
		depth6 = filepath.Join(depth6, "d")
	}
	if !seen[filepath.Join(depth6, "RULE.md")] || seen[filepath.Join(depth6, "d", "RULE.md")] {
		t.Fatal("depth 6/7 boundary wrong")
	}
}

func TestScanDownwardVisitLimitIs500Directories(t *testing.T) {
	root := t.TempDir()
	tool := tools.Tool{LocalFiles: []tools.LocalFile{{Pattern: "RULE.md"}}}
	for i := 0; i < 501; i++ {
		dir := filepath.Join(root, fmt.Sprintf("d%03d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "RULE.md"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := scanDownward(tool, root)
	// Target itself consumes one visit, leaving 499 eligible child directories.
	if len(got) != 499 {
		t.Fatalf("got %d files, want 499 under 500-directory visit cap", len(got))
	}
}

func TestScanDownwardDeduplicatesOverlappingPatterns(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "nested")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "RULE.md")
	if err := os.WriteFile(path, []byte("rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := tools.Tool{LocalFiles: []tools.LocalFile{
		{Pattern: "RULE.md", Note: "exact"},
		{Pattern: "*.md", Note: "glob"},
	}}
	got := scanDownward(tool, root)
	if len(got) != 1 || got[0].Path != path {
		t.Fatalf("overlapping patterns returned %+v, want one %q", got, path)
	}
}
