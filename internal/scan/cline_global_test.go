package scan

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestClineGlobalRuleDirectoryPrecedesWorkspace(t *testing.T) {
	for _, withGlobal := range []bool{false, true} {
		name := "workspace-only-control"
		if withGlobal {
			name = "global-and-workspace"
		}
		t.Run(name, func(t *testing.T) {
			home := tempDir(t)
			t.Setenv("HOME", home)
			target := tempDir(t)
			local := filepath.Join(target, ".clinerules", "policy.md")
			mustWriteFile(t, local, "WORKSPACE")
			want := []string{}
			if withGlobal {
				rules := filepath.Join(home, "Documents", "Cline", "Rules")
				for _, file := range []string{"01-policy.md", "02-style.txt"} {
					path := filepath.Join(rules, file)
					mustWriteFile(t, path, "GLOBAL "+file)
					want = append(want, path)
				}
				// A global directory is a container, not an instruction itself;
				// nested workflows are not claimed as recursive rule inputs.
				mustWriteFile(t, filepath.Join(rules, "workflows", "task.md"), "NOT A RULE")
			}
			want = append(want, local)
			results, _, err := Run(target, Options{ToolSlugs: map[string]bool{"cline": true}})
			if err != nil {
				t.Fatal(err)
			}
			got := findResult(t, results, "cline")
			assertPaths(t, got.Files, want)
			for _, f := range got.Files[:len(got.Files)-1] {
				if !strings.HasPrefix(f.Note, "global:") || !strings.HasPrefix(f.Content, "GLOBAL ") {
					t.Fatalf("wrong global provenance/content: %+v", f)
				}
			}
		})
	}
}
