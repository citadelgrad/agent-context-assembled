package inspect

import (
	"path/filepath"
	"strings"
	"testing"
)

// encoding/json may partially fill a destination before returning a type
// error. Reject the entire typed record rather than combining its cwd with
// previously recovered content (https://pkg.go.dev/encoding/json#Unmarshal).
func TestCodexMalformedTypedRecordsCannotMoveContentToNewProject(t *testing.T) {
	for _, record := range []string{
		`{"type":"turn_context","payload":{"cwd":"/next","user_instructions":123}}`,
		`{"type":"turn_context","payload":{"cwd":["/next"],"user_instructions":"new"}}`,
		`{"type":"session_meta","payload":{"cwd":"/next","base_instructions":123}}`,
	} {
		text := `{"type":"session_meta","payload":{"cwd":"/previous","base_instructions":{"text":"fixed"}}}` + "\n" +
			`{"type":"turn_context","payload":{"user_instructions":"previous"}}` + "\n" + record
		cwd, base, user, ok := readCodexRolloutReader(strings.NewReader(text))
		if !ok || cwd != "/previous" || base != "fixed" || user != "previous" {
			t.Errorf("invalid record partially changed state: (%q, %q, %q, %v)", cwd, base, user, ok)
		}
	}
}

func TestCodexLatestMatchingCWDWithoutContentIsMetadataOnly(t *testing.T) {
	for _, suffix := range []string{"", "\n" + `{"type":"turn_context","payload":{"user_instructions":null}}`} {
		home, target := t.TempDir(), t.TempDir()
		path := filepath.Join(home, ".codex", "sessions", "rollout-current.jsonl")
		mustWriteFile(t, path, rolloutRecord("session_meta", map[string]any{"cwd": target})+suffix)
		r := codexCLIReport(home, target)
		if r.Mechanism != MechanismMetadataOnly || r.ExtractedContent != "" {
			t.Errorf("cwd match alone is not content confirmation: %+v", r)
		}
		if r.ArtifactPath != path || r.ArtifactCount != 1 || r.LastModified.IsZero() {
			t.Errorf("lost matching-session metadata: %+v", r)
		}
		if strings.Contains(r.Summary, "extracted actual") || !strings.Contains(r.Confidence, "no instruction content") {
			t.Errorf("confidence must describe unavailable content: %+v", r)
		}
	}
}

func TestCodexSessionSnapshotDoesNotInheritProjectInstructions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		payload  map[string]any
		wantBase string
	}{
		{"absent base", map[string]any{"cwd": "/next"}, ""},
		{"null base", map[string]any{"cwd": "/next", "base_instructions": nil}, ""},
		{"empty base", map[string]any{"cwd": "/next", "base_instructions": map[string]any{"text": ""}}, ""},
		{"replacement control", map[string]any{"cwd": "/next", "base_instructions": map[string]any{"text": "CURRENT_PERSONA"}}, "CURRENT_PERSONA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := strings.Join([]string{
				rolloutRecord("session_meta", map[string]any{"cwd": "/previous", "base_instructions": map[string]any{"text": "OLD_PERSONA"}}),
				rolloutRecord("turn_context", map[string]any{"user_instructions": "OLD_PROJECT"}),
				rolloutRecord("session_meta", tc.payload),
			}, "\n")
			cwd, base, user, ok := readCodexRolloutReader(strings.NewReader(text))
			if !ok || cwd != "/next" || base != tc.wantBase || user != "" {
				t.Fatalf("got (%q, %q, %q, %v), want next cwd, %q base, no stale project", cwd, base, user, ok, tc.wantBase)
			}
		})
	}
}

// docs/research.md identifies base_instructions as the fixed session persona
// and user_instructions as the turn's compiled project-doc payload, not a patch
// to a previous turn. Missing/null/empty current instructions cannot confirm an
// older project's content. Fixtures use only this repository's supported fields.
func TestCodexTurnSnapshotDoesNotInheritProjectInstructions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{"absent", map[string]any{}, ""},
		{"null", map[string]any{"user_instructions": nil}, ""},
		{"empty", map[string]any{"user_instructions": ""}, ""},
		{"replacement control", map[string]any{"user_instructions": "CURRENT_ONLY"}, "CURRENT_ONLY"},
	} {
		for _, location := range []string{"same cwd", "changed cwd", "omitted cwd"} {
			t.Run(tc.name+"/"+location, func(t *testing.T) {
				home, target := t.TempDir(), t.TempDir()
				previous := target
				if location == "changed cwd" {
					previous = filepath.Join(t.TempDir(), "other-project")
				}
				payload := make(map[string]any)
				for key, value := range tc.payload {
					payload[key] = value
				}
				if location != "omitted cwd" {
					payload["cwd"] = target
				}
				text := strings.Join([]string{
					rolloutRecord("session_meta", map[string]any{"cwd": previous, "base_instructions": map[string]any{"text": "FIXED_PERSONA"}}),
					rolloutRecord("turn_context", map[string]any{"cwd": previous, "user_instructions": "STALE_PROJECT_SENTINEL"}),
					rolloutRecord("turn_context", payload),
				}, "\n")
				mustWriteFile(t, filepath.Join(home, ".codex", "sessions", "rollout-test.jsonl"), text)
				r := codexCLIReport(home, target)
				if r.Mechanism != MechanismContentConfirmed || !strings.Contains(r.ExtractedContent, "FIXED_PERSONA") {
					t.Fatalf("lost fixed persona control: %+v", r)
				}
				if strings.Contains(r.ExtractedContent, "STALE_PROJECT_SENTINEL") {
					t.Errorf("current turn leaked stale project instructions: %q", r.ExtractedContent)
				}
				if tc.want != "" && !strings.Contains(r.ExtractedContent, tc.want) {
					t.Errorf("missing current project instructions: %q", r.ExtractedContent)
				}
			})
		}
	}
}
