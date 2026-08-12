package inspect

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGeminiCLIReportCountsOnlyRegularSessionLogsAndUsesNewestMTime(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "tmp", "hash", "chats")
	oldPath, newPath := filepath.Join(dir, "old.json"), filepath.Join(dir, "new.json")
	mustWriteFile(t, oldPath, `{}`)
	mustWriteFile(t, newPath, `{}`)
	mustMkdirAll(t, filepath.Join(dir, "not-a-log"))
	oldTime, newTime := time.Now().Add(-time.Hour).Truncate(time.Second), time.Now().Truncate(time.Second)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	r := geminiCLIReport(home, "/target")
	if r.ArtifactCount != 2 || !r.LastModified.Equal(newTime) {
		t.Fatalf("count/mtime = %d/%v, want 2/%v", r.ArtifactCount, r.LastModified, newTime)
	}
}

func TestWindsurfReportNewestMTimeIgnoresIrrelevantEntries(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".windsurf", "transcripts")
	transcript := filepath.Join(dir, "session.jsonl")
	mustWriteFile(t, transcript, `{}`)
	mustWriteFile(t, filepath.Join(dir, "newer.txt"), "irrelevant")
	mustMkdirAll(t, filepath.Join(dir, "newest.jsonl"))
	want := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(transcript, want, want); err != nil {
		t.Fatal(err)
	}
	r := windsurfReport(home)
	if r.ArtifactCount != 1 || !r.LastModified.Equal(want) {
		t.Fatalf("count/mtime = %d/%v, want 1/%v", r.ArtifactCount, r.LastModified, want)
	}
}

func TestClineReportCountsOnlyTaskDirectoriesAndUsesNewestTaskMTime(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "tasks")
	oldTask, newTask := filepath.Join(dir, "old-task"), filepath.Join(dir, "new-task")
	mustMkdirAll(t, oldTask)
	mustMkdirAll(t, newTask)
	mustWriteFile(t, filepath.Join(dir, "not-a-task.json"), `{}`)
	oldTime, newTime := time.Now().Add(-time.Hour).Truncate(time.Second), time.Now().Truncate(time.Second)
	if err := os.Chtimes(oldTask, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newTask, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	r := clineReport(home)
	if r.ArtifactCount != 2 || !r.LastModified.Equal(newTime) {
		t.Fatalf("count/mtime = %d/%v, want 2/%v", r.ArtifactCount, r.LastModified, newTime)
	}
}

func TestHermesReportCountsOnlyRegularSessionFilesAndUsesNewestMTime(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".hermes", "sessions")
	session := filepath.Join(dir, "session.jsonl")
	mustWriteFile(t, session, `{}`)
	mustMkdirAll(t, filepath.Join(dir, "not-a-session"))
	want := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(session, want, want); err != nil {
		t.Fatal(err)
	}
	r := hermesReport(home)
	if r.ArtifactCount != 1 || !r.LastModified.Equal(want) {
		t.Fatalf("count/mtime = %d/%v, want 1/%v", r.ArtifactCount, r.LastModified, want)
	}
}
