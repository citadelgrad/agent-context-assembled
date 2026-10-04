package inspect

import (
	"reflect"
	"testing"
)

// A dedicated run also permits a coverage-backed dispatch probe to verify
// that no unselected report function is invoked (not merely filtered later).
func TestRunSelectedOnlyCopilot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	reports := RunSelected(t.TempDir(), map[string]bool{"github-copilot": true})
	if len(reports) != 1 || reports[0].Slug != "github-copilot" {
		t.Fatalf("expected only Copilot: %+v", reports)
	}
}

func TestRunSelectedScopesReports(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	target := t.TempDir()
	all := []string{"claude-code", "codex-cli", "opencode", "aider", "gemini-cli", "github-copilot", "cursor", "windsurf", "cline", "hermes"}
	for _, tc := range []struct {
		name      string
		selection map[string]bool
		want      []string
	}{
		{"nil all control", nil, all},
		{"empty none", map[string]bool{}, []string{}},
		{"selected fixed order", map[string]bool{"aider": true, "codex-cli": true}, []string{"codex-cli", "aider"}},
		{"false and unknown excluded", map[string]bool{"aider": false, "unknown": true}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reports := RunSelected(target, tc.selection)
			got := make([]string, 0, len(reports))
			for _, report := range reports {
				got = append(got, report.Slug)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("selected reports=%v want %v", got, tc.want)
			}
		})
	}
	if !reflect.DeepEqual(Run(target), RunSelected(target, nil)) {
		t.Fatal("Run compatibility changed")
	}
}
