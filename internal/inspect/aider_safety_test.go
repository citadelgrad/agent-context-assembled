package inspect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAiderHistoryReadBudgetIsExplicit(t *testing.T) {
	// This is an actx safety budget, not an asserted upstream Aider limit.
	const budget = 16 * 1024 * 1024
	for _, size := range []int64{budget, budget + 1} {
		project := t.TempDir()
		path := filepath.Join(project, ".aider.llm.history")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(size); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		// A large LLM log must not disappear behind the chat fallback.
		mustWriteFile(t, filepath.Join(project, ".aider.chat.history.md"), "chat control")
		r := aiderReport(project)
		if size == budget {
			if r.Mechanism != MechanismContentConfirmed || len(r.ExtractedContent) != budget {
				t.Errorf("boundary control: mechanism=%s content bytes=%d", r.Mechanism, len(r.ExtractedContent))
			}
			continue
		}
		if r.Mechanism != MechanismMetadataOnly || r.ExtractedContent != "" || r.ArtifactPath != path || r.LastModified.IsZero() {
			t.Errorf("oversized history: mechanism=%s content bytes=%d path=%s", r.Mechanism, len(r.ExtractedContent), r.ArtifactPath)
		}
		if !strings.Contains(r.Detail, "16 MiB actx safety budget") || !strings.Contains(r.Detail, "not an Aider limit") {
			t.Errorf("must explain actx's size limit honestly: %s", r.Detail)
		}
	}
}

func TestAiderHistoryCannotEscapeProjectThroughSymlinks(t *testing.T) {
	for _, kind := range []string{"relative escape", "absolute escape", "parent symlink escape", "in-project relative control"} {
		t.Run(kind, func(t *testing.T) {
			fixture := t.TempDir()
			project := filepath.Join(fixture, "project")
			mustMkdirAll(t, project)
			outside := filepath.Join(fixture, "outside")
			mustWriteFile(t, outside, "OUTSIDE_SYNTHETIC_SENTINEL")
			linkTarget := "../outside"
			switch kind {
			case "absolute escape":
				linkTarget = outside
			case "parent symlink escape":
				if err := os.Symlink("..", filepath.Join(project, "parent")); err != nil {
					t.Fatal(err)
				}
				linkTarget = "parent/outside"
			case "in-project relative control":
				mustWriteFile(t, filepath.Join(project, "logs", "messages"), "LOCAL_CONTROL")
				linkTarget = "logs/messages"
			}
			if err := os.Symlink(linkTarget, filepath.Join(project, ".aider.llm.history")); err != nil {
				t.Fatal(err)
			}
			r := aiderReport(project)
			if kind == "in-project relative control" {
				if r.Mechanism != MechanismContentConfirmed || r.ExtractedContent != "LOCAL_CONTROL" {
					t.Fatalf("in-project regular symlink must remain readable: %+v", r)
				}
			} else if r.Mechanism == MechanismContentConfirmed || strings.Contains(r.ExtractedContent, "OUTSIDE_SYNTHETIC_SENTINEL") {
				t.Fatalf("outside-project content disclosed: %+v", r)
			}
		})
	}
}
