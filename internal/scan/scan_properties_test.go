package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/citadelgrad/agent-context-assembled/internal/tools"
)

func FuzzScopedDirsModel(f *testing.F) {
	f.Add(uint8(4), int8(2), uint8(0))
	f.Add(uint8(1), int8(-1), uint8(1))
	f.Fuzz(func(t *testing.T, countRaw uint8, gitRaw int8, scopeRaw uint8) {
		count := int(countRaw%12) + 1
		dirs := make([]string, count)
		for i := range dirs {
			dirs[i] = fmt.Sprintf("/d/%02d", i)
		}
		git := int(gitRaw)
		if git < -1 || git >= count {
			git = -1
		}
		scope := tools.ScopeMode(scopeRaw % 3)
		chain := Chain{Dirs: dirs, GitRootIndex: git}
		got := scopedDirs(chain, tools.Tool{Scope: scope})
		var want []string
		switch scope {
		case tools.ScopeFilesystemRoot:
			want = dirs
		case tools.ScopeGitRoot:
			if git >= 0 {
				want = dirs[git:]
			} else {
				want = dirs[count-1:]
			}
		case tools.ScopeTargetOnly:
			want = dirs[count-1:]
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("scope=%v count=%d git=%d got=%v want=%v", scope, count, git, got, want)
		}
	})
}

func TestScanToolPrecedenceModel(t *testing.T) {
	root := t.TempDir()
	dirs := []string{root, filepath.Join(root, "outer"), filepath.Join(root, "outer", "target")}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	home := t.TempDir()
	global1, global2 := filepath.Join(home, "g1.md"), filepath.Join(home, "g2.md")
	for path, content := range map[string]string{
		global1: "g1", global2: "g2",
		filepath.Join(dirs[1], "first.md"):  "outer-first",
		filepath.Join(dirs[2], "first.md"):  "target-first",
		filepath.Join(dirs[2], "second.md"): "target-second",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	chain := Chain{Dirs: dirs, GitRootIndex: 1, Home: home}
	baseTool := tools.Tool{
		Scope:         tools.ScopeFilesystemRoot,
		GlobalConfigs: []tools.GlobalConfig{{PathFromHome: "g1.md", Note: "one"}, {PathFromHome: "g2.md", Note: "two"}},
		LocalFiles:    []tools.LocalFile{{Pattern: "first.md", Note: "first"}, {Pattern: "second.md", Note: "second"}},
	}
	additive := scanTool(baseTool, chain, Options{})
	assertPaths(t, additive.Files, []string{global1, global2, filepath.Join(dirs[1], "first.md"), filepath.Join(dirs[2], "first.md"), filepath.Join(dirs[2], "second.md")})
	baseTool.FirstMatchWins = true
	first := scanTool(baseTool, chain, Options{})
	assertPaths(t, first.Files, []string{global1, filepath.Join(dirs[2], "first.md")})
}

func TestCodexGlobalFallbackUsesFirstExistingCandidate(t *testing.T) {
	home := t.TempDir()
	target := t.TempDir()
	for path, content := range map[string]string{
		filepath.Join(home, ".codex", "AGENTS.override.md"): "override",
		filepath.Join(home, ".codex", "AGENTS.md"):          "fallback",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var codex tools.Tool
	for _, tool := range tools.Registry {
		if tool.Slug == "codex-cli" {
			codex = tool
			break
		}
	}
	got := scanTool(codex, Chain{Dirs: []string{target}, GitRootIndex: -1, Home: home}, Options{})
	if len(got.Files) != 1 || !strings.HasSuffix(got.Files[0].Path, "AGENTS.override.md") {
		t.Fatalf("Codex globals = %+v, want override only", got.Files)
	}
}

func assertPaths(t *testing.T, files []MatchedFile, want []string) {
	t.Helper()
	got := make([]string, len(files))
	for i, file := range files {
		got[i] = file.Path
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths=%v, want %v", got, want)
	}
}
