package compile_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/citadelgrad/agent-context-assembled/internal/compile"
	"github.com/citadelgrad/agent-context-assembled/internal/scan"
)

func TestCompileEagerParentsPrecedeDescendants(t *testing.T) {
	for _, childName := range []string{"A", "z"} {
		t.Run(childName, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			root := t.TempDir()
			parent := filepath.Join(root, "sub", "GEMINI.md")
			child := filepath.Join(root, "sub", childName, "GEMINI.md")
			mustWriteFile(t, parent, "PARENT-CONTENT")
			mustWriteFile(t, child, "CHILD-CONTENT")
			results, _, err := scan.Run(root, scan.Options{ToolSlugs: map[string]bool{"gemini-cli": true}})
			if err != nil {
				t.Fatal(err)
			}
			compiled := compile.Run(results)[0]
			var order []string
			for _, chunk := range compiled.Chunks {
				if chunk.Content == "PARENT-CONTENT" || chunk.Content == "CHILD-CONTENT" {
					order = append(order, chunk.Content)
				}
			}
			if len(order) != 2 || order[0] != "PARENT-CONTENT" || order[1] != "CHILD-CONTENT" {
				t.Fatalf("eager hierarchy order=%v, want parent before child regardless of spelling", order)
			}
			if strings.Index(compiled.Assembled, "PARENT-CONTENT") >= strings.Index(compiled.Assembled, "CHILD-CONTENT") {
				t.Fatal("assembled instructions reverse hierarchy order")
			}
		})
	}
}
