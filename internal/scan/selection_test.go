package scan

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/citadelgrad/agent-context-assembled/internal/tools"
)

func TestRunToolSelection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	target := tempDir(t)
	// Bound every registry entry to fabricated data, including the nil/all case.
	original := tools.Registry
	tools.Registry = []tools.Tool{
		{Slug: "first", Scope: tools.ScopeTargetOnly, LocalFiles: []tools.LocalFile{{Pattern: "first.md"}}},
		{Slug: "second", Scope: tools.ScopeTargetOnly, LocalFiles: []tools.LocalFile{{Pattern: "second.md"}}},
	}
	t.Cleanup(func() { tools.Registry = original })
	mustWriteFile(t, filepath.Join(target, "first.md"), "first")
	mustWriteFile(t, filepath.Join(target, "second.md"), "second")

	for _, tc := range []struct {
		name  string
		slugs map[string]bool
		want  []string
	}{
		{"nil means all", nil, []string{"first", "second"}},
		{"empty means none", map[string]bool{}, []string{}},
		{"subset", map[string]bool{"second": true}, []string{"second"}},
		{"false excluded", map[string]bool{"first": false, "second": true}, []string{"second"}},
		{"unknown means none", map[string]bool{"unknown": true}, []string{}},
		{"registry order", map[string]bool{"second": true, "first": true}, []string{"first", "second"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results, chain, err := Run(target, Options{ToolSlugs: tc.slugs})
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(results))
			for _, result := range results {
				got = append(got, result.Tool.Slug)
				if len(result.Files) != 1 || result.Files[0].Content != result.Tool.Slug {
					t.Fatalf("selected tool scan = %+v", result)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("selected tools = %v, want %v", got, tc.want)
			}
			if chain.Dirs[len(chain.Dirs)-1] != target {
				t.Fatalf("selection changed target chain: %+v", chain)
			}
		})
	}
}
