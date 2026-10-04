package inspect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/citadelgrad/agent-context-assembled/internal/budget"
)

func TestIncompleteJSONOnlyAddsFieldOnExhaustion(t *testing.T) {
	healthy := githubCopilotReport()
	data, err := json.Marshal(healthy)
	if err != nil || strings.Contains(string(data), `"incomplete"`) {
		t.Fatalf("healthy shape changed: %s %v", data, err)
	}
	exhausted := incompleteReport(Report{ExtractedContent: "must not leak", ArtifactCount: 2}, os.ErrPermission)
	data, err = json.Marshal(exhausted)
	if err != nil || !strings.Contains(string(data), `"incomplete":true`) || strings.Contains(string(data), "must not leak") || strings.Contains(string(data), `"artifactCount"`) {
		t.Fatalf("incomplete JSON: %s %v", data, err)
	}
}

func TestCodexUnreadableSubtreeDoesNotConfirmOlderContent(t *testing.T) {
	home, target := t.TempDir(), t.TempDir()
	root := filepath.Join(home, ".codex", "sessions")
	mustWriteFile(t, filepath.Join(root, "rollout-old.jsonl"), `{"type":"session_meta","payload":{"cwd":"`+target+`","base_instructions":{"text":"OLD"}}}`)
	blocked := filepath.Join(root, "unknown-newer")
	mustMkdirAll(t, blocked)
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(blocked, 0o700)
	if _, err := os.ReadDir(blocked); err == nil {
		t.Skip("environment bypasses permission bits")
	}
	r := codexCLIReport(home, target)
	if !r.Incomplete || r.ExtractedContent != "" {
		t.Fatalf("unreadable subtree falsely confirmed older content: %+v", r)
	}
}

func TestCodexParserFailureIsNotMissingMetadata(t *testing.T) {
	valid := `{"type":"session_meta","payload":{"cwd":"/project"}}` + "\n"
	_, _, _, ok, err := parseCodexRollout(strings.NewReader(valid+strings.Repeat("x", 129)), 128)
	if err == nil || ok {
		t.Fatalf("parser limit must be an explicit failure, ok=%v err=%v", ok, err)
	}
	cwd, _, _, ok, err := parseCodexRollout(strings.NewReader(valid), 128)
	if err != nil || !ok || cwd != "/project" {
		t.Fatalf("healthy parse: %q %v %v", cwd, ok, err)
	}
}

func TestInspectSharesBytesAcrossCodexAndAider(t *testing.T) {
	home, target := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	record := `{"type":"session_meta","payload":{"cwd":"` + target + `","base_instructions":{"text":"CODEX"}}}`
	mustWriteFile(t, filepath.Join(home, ".codex", "sessions", "rollout-one.jsonl"), record)
	mustWriteFile(t, filepath.Join(target, ".aider.llm.history"), "AIDER")
	slugs := map[string]bool{"codex-cli": true, "aider": true}
	reports := runSelectedWithBudget(target, slugs, budget.Limits{TotalBytes: int64(len(record) + 4)})
	if len(reports) != 2 || reports[0].Incomplete || reports[0].Mechanism != MechanismContentConfirmed {
		t.Fatalf("first tool: %+v", reports)
	}
	if !reports[1].Incomplete || reports[1].ExtractedContent != "" || !strings.Contains(reports[1].Detail, "aggregate bytes") {
		t.Fatalf("Aider ignored shared budget: %+v", reports[1])
	}
	reports = runSelectedWithBudget(target, slugs, budget.Limits{FileBytes: 4})
	for _, r := range reports {
		if !r.Incomplete || r.ExtractedContent != "" {
			t.Errorf("exhaustion did not stop later tool: %+v", r)
		}
	}
	r := runSelectedWithBudget(target, map[string]bool{"aider": true}, budget.Limits{FileBytes: 5, TotalBytes: 5, Files: 1})[0]
	if r.Incomplete || r.ExtractedContent != "AIDER" {
		t.Fatalf("unselected Codex charged budget: %+v", r)
	}
}

func TestInspectSharesDiscoveryAcrossSelectedTools(t *testing.T) {
	home, target := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	for _, dir := range []string{filepath.Join(home, ".claude", "projects", encodeClaudeProjectDir(target)), filepath.Join(home, ".windsurf", "transcripts")} {
		for _, name := range []string{"a.jsonl", "b.jsonl"} {
			mustWriteFile(t, filepath.Join(dir, name), "")
		}
	}
	reports := runSelectedWithBudget(target, map[string]bool{"claude-code": true, "windsurf": true}, budget.Limits{Entries: 3})
	if len(reports) != 2 || reports[0].Incomplete || !reports[1].Incomplete {
		t.Fatalf("entry accounting not shared: %+v", reports)
	}
	r := runSelectedWithBudget(target, map[string]bool{"windsurf": true}, budget.Limits{Entries: 2})[0]
	if r.Incomplete || r.ArtifactCount != 2 {
		t.Fatalf("unselected tool charged discovery: %+v", r)
	}
}

func TestInspectBoundsMetadataAndCodexDiscovery(t *testing.T) {
	for _, slug := range []string{"claude-code", "codex-cli", "gemini-cli", "windsurf", "cline", "hermes"} {
		t.Run(slug, func(t *testing.T) {
			home, target := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			rel := map[string]string{
				"claude-code": filepath.Join(".claude", "projects", encodeClaudeProjectDir(target)),
				"codex-cli":   ".codex/sessions", "gemini-cli": ".gemini/tmp", "windsurf": ".windsurf/transcripts",
				"cline": ".config/Code/User/globalStorage/saoudrizwan.claude-dev/tasks", "hermes": ".hermes/sessions",
			}[slug]
			for _, name := range []string{"rollout-z.jsonl", "rollout-a.jsonl", "rollout-m.jsonl"} {
				mustWriteFile(t, filepath.Join(home, rel, name), "x")
			}
			r := runSelectedWithBudget(target, map[string]bool{slug: true}, budget.Limits{Entries: 2})[0]
			if !r.Incomplete || r.Mechanism == MechanismContentConfirmed || r.ExtractedContent != "" || r.ArtifactCount != 0 || !strings.Contains(r.Detail, "directory entries") {
				t.Fatalf("wide directory reported complete: %+v", r)
			}
			r = RunSelected(target, map[string]bool{slug: true})[0]
			if r.Incomplete {
				t.Fatalf("healthy control incomplete: %+v", r)
			}
		})
	}
}

func TestCodexDiscoveryDirectoryAndMatchCaps(t *testing.T) {
	home, target := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{"a", "b"} {
		mustWriteFile(t, filepath.Join(home, ".codex", "sessions", name, "rollout-one.jsonl"), "{}")
	}
	for _, tc := range []struct {
		limits budget.Limits
		want   string
	}{
		{budget.Limits{Directories: 1}, "directories"}, {budget.Limits{Matches: 1}, "matches"},
	} {
		r := runSelectedWithBudget(target, map[string]bool{"codex-cli": true}, tc.limits)[0]
		if !r.Incomplete || !strings.Contains(r.Detail, tc.want) {
			t.Errorf("traversal cap ignored: %+v", r)
		}
	}
}

func TestCodexInputBudgetDoesNotConfirmOlderContent(t *testing.T) {
	home, target := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".codex", "sessions")
	old := filepath.Join(root, "rollout-old.jsonl")
	newest := filepath.Join(root, "rollout-new.jsonl")
	mustWriteFile(t, old, `{"type":"session_meta","payload":{"cwd":"`+target+`","base_instructions":{"text":"OLD"}}}`)
	mustWriteFile(t, newest, strings.Repeat(" \n", 257))
	stamp := time.Unix(1000, 0)
	if err := os.Chtimes(old, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	stamp = time.Unix(2000, 0)
	if err := os.Chtimes(newest, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	reports := runSelectedWithBudget(target, map[string]bool{"codex-cli": true}, budget.Limits{FileBytes: 512})
	if len(reports) != 1 {
		t.Fatal(reports)
	}
	r := reports[0]
	if r.Mechanism == MechanismContentConfirmed || r.ExtractedContent != "" || !strings.Contains(strings.ToLower(r.Summary), "incomplete") || !strings.Contains(r.Detail, "file bytes") {
		t.Fatalf("oversized newest must not confirm older content: %+v", r)
	}
	if err := os.Remove(newest); err != nil {
		t.Fatal(err)
	}
	r = runSelectedWithBudget(target, map[string]bool{"codex-cli": true}, budget.Limits{FileBytes: 512})[0]
	if r.Mechanism != MechanismContentConfirmed || !strings.Contains(r.ExtractedContent, "OLD") {
		t.Fatalf("healthy control: %+v", r)
	}
}
