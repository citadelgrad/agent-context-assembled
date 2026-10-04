package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/citadelgrad/agent-context-assembled/internal/budget"
)

func TestDepthBoundaryStillExcludesTaggedCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	child := root
	for i := 0; i < 7; i++ {
		child = filepath.Join(child, "d")
	}
	mustWriteFile(t, filepath.Join(child, "CACHEDIR.TAG"), "")
	got, _, err := Run(root, Options{ToolSlugs: map[string]bool{"gemini-cli": true}})
	if err != nil || len(got) != 1 {
		t.Fatalf("intentional cache exclusion must not be reported as truncation: %d %v", len(got), err)
	}
}

func TestRunBoundsPatternDiscovery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	dir := filepath.Join(root, ".cursor", "rules")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"z.mdc", "a.mdc", "m.mdc"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{ToolSlugs: map[string]bool{"cursor": true}}
	for _, tc := range []struct {
		limits budget.Limits
		want   string
	}{
		{budget.Limits{Entries: 2}, "directory entries"},
		{budget.Limits{Matches: 2}, "matches"},
	} {
		got, _, err := runWithBudget(root, opts, tc.limits)
		if err == nil || len(got) != 0 || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("must reject incomplete discovery: results=%d err=%v", len(got), err)
		}
	}
	got, _, err := runWithBudget(root, opts, budget.Limits{Entries: 3, Matches: 3})
	if err != nil || len(got) != 1 || len(got[0].Files) != 3 || filepath.Base(got[0].Files[0].Path) != "a.mdc" {
		t.Fatalf("healthy lexical order: %+v %v", got, err)
	}
}

func TestRunRefusesIncompleteDoublestarDepth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	child := filepath.Join(root, ".github", "instructions")
	for i := 0; i < 13; i++ {
		child = filepath.Join(child, "d")
	}
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	got, _, err := Run(root, Options{ToolSlugs: map[string]bool{"github-copilot": true}})
	if err == nil || len(got) != 0 || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("silent doublestar truncation: %d %v", len(got), err)
	}
}

func TestRunSharesInputBytesAcrossSelectedTools(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	for _, name := range []string{"AGENTS.md", ".aider.conf.yml"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("abc"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{ToolSlugs: map[string]bool{"aider": true, "hermes": true}}
	for _, tc := range []struct {
		name   string
		limits budget.Limits
		want   string
	}{
		{"per-file", budget.Limits{FileBytes: 2}, "file bytes"},
		{"aggregate", budget.Limits{FileBytes: 3, TotalBytes: 5}, "aggregate bytes"},
		{"files", budget.Limits{Files: 1}, "files"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := runWithBudget(root, opts, tc.limits)
			if err == nil || len(got) != 0 || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("must fail closed: results=%d err=%v", len(got), err)
			}
		})
	}
	got, _, err := runWithBudget(root, Options{ToolSlugs: map[string]bool{"hermes": true}}, budget.Limits{FileBytes: 3, TotalBytes: 3, Files: 1})
	if err != nil || len(got) != 1 || len(got[0].Files) != 1 || got[0].Files[0].Content != "abc" {
		t.Fatalf("selected-only exact boundary: %+v %v", got, err)
	}
}

func TestRunRefusesIncompleteDepthLimitedContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	child := root
	for i := 0; i < 7; i++ {
		child = filepath.Join(child, "child")
	}
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "GEMINI.md"), []byte("not silently omitted"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _, err := Run(root, Options{ToolSlugs: map[string]bool{"gemini-cli": true}})
	if err == nil || !strings.Contains(err.Error(), "depth") || len(got) != 0 {
		t.Fatalf("truncated scan must fail closed with depth diagnostic, results=%d err=%v", len(got), err)
	}
}
