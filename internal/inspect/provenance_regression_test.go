package inspect

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeEncodedPathCollisionIsUnverifiedProjectMetadata(t *testing.T) {
	home, fixture := t.TempDir(), t.TempDir()
	a, b := filepath.Join(fixture, "foo", "bar"), filepath.Join(fixture, "foo-bar")
	mustMkdirAll(t, a)
	mustMkdirAll(t, b)
	if encodeClaudeProjectDir(a) != encodeClaudeProjectDir(b) {
		t.Fatal("invalid collision fixture")
	}
	path := filepath.Join(home, ".claude", "projects", encodeClaudeProjectDir(a), "session-a.jsonl")
	mustWriteFile(t, path, rolloutRecord("session", map[string]any{"cwd": a}))
	// We do not invent a transcript parser. Both the true project and its
	// collision get candidate metadata, with explicitly unverified scope.
	for _, target := range []string{a, b} {
		r := claudeCodeReport(home, target)
		if r.Mechanism != MechanismMetadataOnly || r.ArtifactPath != path || r.ArtifactCount != 1 || r.ExtractedContent != "" {
			t.Fatalf("metadata control: %+v", r)
		}
		if strings.Contains(r.Summary, "for this directory") || !strings.Contains(r.Summary, "unverified") {
			t.Errorf("lossy encoding cannot confirm project scope: %s", r.Summary)
		}
		if !strings.Contains(r.Confidence, "unverified") || !strings.Contains(r.Detail, "collid") {
			t.Errorf("must explain ambiguous project provenance: confidence=%s detail=%s", r.Confidence, r.Detail)
		}
	}
}
