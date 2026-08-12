package inspect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexCLIReportLatestMatchingDiscoveryProperties(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	root := filepath.Join(home, ".codex", "sessions", "2026", "01", "01")
	oldPath := filepath.Join(root, "rollout-old.jsonl")
	newPath := filepath.Join(root, "nested", "rollout-new.jsonl")
	oldContent := `{"type":"session_meta","payload":{"cwd":"` + target + `","base_instructions":{"text":"OLD"}}}` + "\n"
	newContent := `{"type":"turn_context","payload":{"cwd":"` + target + `","user_instructions":"NEW"}}` + "\n"
	mustWriteFile(t, oldPath, oldContent)
	mustWriteFile(t, newPath, newContent)
	oldTime, newTime := time.Now().Add(-time.Hour).Truncate(time.Second), time.Now().Truncate(time.Second)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(root, "rollout-unmatched.jsonl"), `{"type":"session_meta","payload":{"cwd":"/other"}}}`)
	mustWriteFile(t, filepath.Join(root, "notes.jsonl"), oldContent)
	mustWriteFile(t, filepath.Join(root, "rollout-wrong.txt"), oldContent)
	mustWriteFile(t, filepath.Join(root, "rollout-malformed.jsonl"), `{malformed`)
	mustMkdirAll(t, filepath.Join(root, "rollout-directory.jsonl"))

	r := codexCLIReport(home, target)
	if r.ArtifactCount != 2 || r.ArtifactPath != newPath || !r.LastModified.Equal(newTime) || !strings.Contains(r.ExtractedContent, "NEW") || strings.Contains(r.ExtractedContent, "OLD") {
		t.Fatalf("unexpected report: %+v", r)
	}
}

func TestCodexCLIReportEqualMTimeUsesLexicalPathTieBreak(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	root := filepath.Join(home, ".codex", "sessions")
	a, b := filepath.Join(root, "rollout-a.jsonl"), filepath.Join(root, "rollout-b.jsonl")
	mustWriteFile(t, a, `{"type":"session_meta","payload":{"cwd":"`+target+`","base_instructions":{"text":"A"}}}`)
	mustWriteFile(t, b, `{"type":"session_meta","payload":{"cwd":"`+target+`","base_instructions":{"text":"B"}}}`)
	stamp := time.Now().Truncate(time.Second)
	for _, p := range []string{a, b} {
		if err := os.Chtimes(p, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ {
		r := codexCLIReport(home, target)
		if r.ArtifactPath != a || !strings.Contains(r.ExtractedContent, "A") {
			t.Fatalf("iteration %d nondeterministic report: %+v", i, r)
		}
	}
}
