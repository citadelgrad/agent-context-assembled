package scan

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/citadelgrad/agent-context-assembled/internal/tools"
)

func TestScanLiteralDirectories(t *testing.T) {
	for _, name := range []string{"repo-normal", "repo[ab]", "repo?", "repo*", "repo**", "repo[", `repo\name`} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && (name == "repo?" || name == "repo*" || name == "repo**" || name == `repo\name`) {
				t.Skip("not a valid Windows directory name")
			}
			for _, tc := range []struct{ pattern, file string }{
				{"RULE.md", "RULE.md"},
				{"rules/*.md", "rules/policy.md"},
				{"rules/**/*.md", "rules/nest[ab]/policy.md"},
			} {
				for _, source := range []string{"local", "home", "downward"} {
					t.Run(source+"/"+tc.pattern, func(t *testing.T) {
						root := tempDir(t)
						literal := filepath.Join(root, name)
						intended := filepath.Join(literal, tc.file)
						mustWriteFile(t, intended, "INTENDED")
						mustWriteFile(t, filepath.Join(root, "repoa", tc.file), "FOREIGN")
						tool := tools.Tool{Scope: tools.ScopeTargetOnly}
						chain := Chain{Dirs: []string{literal}, GitRootIndex: -1}
						switch source {
						case "home":
							chain.Home = literal
							tool.GlobalConfigs = []tools.GlobalConfig{{PathFromHome: tc.pattern}}
						case "local":
							tool.LocalFiles = []tools.LocalFile{{Pattern: tc.pattern}}
						case "downward":
							// The scan root itself is also literal; the matched file
							// lives below it, not in the ancestor-chain stage.
							tool.LocalFiles = []tools.LocalFile{{Pattern: tc.pattern}}
							tool.Downward = tools.DownwardEager
							tool.Scope = tools.ScopeTargetOnly
							intended = filepath.Join(literal, "child", tc.file)
							mustWriteFile(t, intended, "INTENDED")
							got := scanDownward(tool, literal)
							assertPaths(t, got, []string{intended})
							return
						}
						got := scanTool(tool, chain, Options{})
						assertPaths(t, got.Files, []string{intended})
						if got.Files[0].Content != "INTENDED" {
							t.Fatalf("foreign content selected: %+v", got.Files)
						}
					})
				}
			}
		})
	}
}
