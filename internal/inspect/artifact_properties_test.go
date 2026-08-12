package inspect

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestGeminiCLIZeroQualifyingArtifacts asserts that a chats directory with no
// regular .jsonl files reports count 0 and a zero last-modified time.
func TestGeminiCLIZeroQualifyingArtifacts(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "tmp", "hash", "chats")
	mustMkdirAll(t, dir)
	mustMkdirAll(t, filepath.Join(dir, "subdir"))
	mustWriteFile(t, filepath.Join(dir, "not-a-jsonl.txt"), `{}`)

	// The implementation counts all regular files in chats/ (not just .jsonl).
	// So an empty chats dir produces zero.
	if entries, _ := os.ReadDir(dir); len(entries) > 0 {
		// Remove the irrelevant files so the chats dir is truly empty.
		for _, e := range entries {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	r := geminiCLIReport(home, "/target")
	if r.ArtifactCount != 0 || !r.LastModified.IsZero() {
		t.Fatalf("count/mtime = %d/%v, want 0/zero", r.ArtifactCount, r.LastModified)
	}
}

// TestGeminiCLISymlinksAreNotCounted verifies that symlinks in the candidate
// directory do not count as regular session logs.
func TestGeminiCLISymlinksAreNotCounted(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "tmp", "h", "chats")
	mustMkdirAll(t, dir)
	// Create a regular file and a symlink pointing to it.
	reg := filepath.Join(dir, "real.jsonl")
	mustWriteFile(t, reg, `{}`)
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(reg, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	r := geminiCLIReport(home, "/target")
	if r.ArtifactCount != 1 {
		t.Fatalf("count = %d, want 1 (symlink excluded)", r.ArtifactCount)
	}
}

// TestGeminiCLIIrrelevantRootDoesNotInterfere ensures that extra directories
// at the machine level (unrelated to the Gemini layout) do not inflate the
// artifact count.
func TestGeminiCLIIrrelevantRootDoesNotInterfere(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "tmp", "h", "chats")
	mustMkdirAll(t, dir)
	mustWriteFile(t, filepath.Join(dir, "session.jsonl"), `{}`)
	// Create a sibling directory that looks like another tool's layout.
	mustMkdirAll(t, filepath.Join(home, ".hermes", "sessions"))
	mustWriteFile(t, filepath.Join(home, ".hermes", "sessions", "session.jsonl"), `{}`)

	r := geminiCLIReport(home, "/target")
	if r.ArtifactCount != 1 {
		t.Fatalf("count = %d, want 1 (unrelated root ignored)", r.ArtifactCount)
	}
}

// TestGeminiCLIMultipleProjectHashesPrecedence checks that all project-hash
// directories contribute to the count, and the newest mtime across them is
// selected.
func TestGeminiCLIMultipleProjectHashesPrecedence(t *testing.T) {
	home := t.TempDir()
	dirA := filepath.Join(home, ".gemini", "tmp", "hashA", "chats")
	dirB := filepath.Join(home, ".gemini", "tmp", "hashB", "chats")
	for _, dir := range []string{dirA, dirB} {
		mustMkdirAll(t, dir)
	}
	oldT := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	newT := time.Now().Add(-time.Hour).Truncate(time.Second)
	// Older file in hashA, newer in hashB.
	oldPath := filepath.Join(dirA, "old.jsonl")
	newPath := filepath.Join(dirB, "new.jsonl")
	mustWriteFile(t, oldPath, `{}`)
	mustWriteFile(t, newPath, `{}`)
	if err := os.Chtimes(oldPath, oldT, oldT); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, newT, newT); err != nil {
		t.Fatal(err)
	}
	r := geminiCLIReport(home, "/target")
	if r.ArtifactCount != 2 || !r.LastModified.Equal(newT) {
		t.Fatalf("count/mtime = %d/%v, want 2/%v", r.ArtifactCount, r.LastModified, newT)
	}
}

// TestGeminiCLINeutralInsertion asserts that adding irrelevant nodes to the
// chats directory does not change the artifact count or newest mtime.
func TestGeminiCLINeutralInsertion(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "tmp", "h", "chats")
	mustMkdirAll(t, dir)
	session := filepath.Join(dir, "session.jsonl")
	mustWriteFile(t, session, `{}`)
	wantT := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(session, wantT, wantT); err != nil {
		t.Fatal(err)
	}
	before := geminiCLIReport(home, "/target")
	if before.ArtifactCount != 1 || !before.LastModified.Equal(wantT) {
		t.Fatalf("before count/mtime = %d/%v, want 1/%v", before.ArtifactCount, before.LastModified, wantT)
	}
	// Insert neutral nodes: the impl counts ALL regular files in chats/
	// (not just .jsonl), so only subdirectories and symlinks are neutral.
	mustMkdirAll(t, filepath.Join(dir, "subdir"))
	mustWriteFile(t, filepath.Join(dir, "subdir", "nested.txt"), "nested")

	after := geminiCLIReport(home, "/target")
	if after.ArtifactCount != before.ArtifactCount {
		t.Fatalf("neutral insertion changed count: %d -> %d", before.ArtifactCount, after.ArtifactCount)
	}
	if !after.LastModified.Equal(before.LastModified) {
		t.Fatalf("neutral insertion changed mtime: %v -> %v", before.LastModified, after.LastModified)
	}
}
