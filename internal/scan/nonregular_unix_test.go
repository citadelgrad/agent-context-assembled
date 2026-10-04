//go:build unix

package scan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/citadelgrad/agent-context-assembled/internal/tools"
)

// Keep writerless FIFO regressions bounded even if discovery or readFile regresses.
// A blocked helper is killed and waited on; no goroutine or FIFO writer is leaked.
func TestNonregularInstructionsDoNotBlock(t *testing.T) {
	if dir := os.Getenv("ACTX_SPECIAL_TEST_DIR"); dir != "" {
		want := os.Getenv("ACTX_SPECIAL_TEST_WANT") == "regular"
		if os.Getenv("ACTX_SPECIAL_TEST_MODE") == "read" {
			content, ok := readFile(filepath.Join(dir, "RULE.md"))
			if ok != want || (want && content != "regular") {
				t.Fatalf("readFile = %q, %v; want regular=%v", content, ok, want)
			}
			return
		}
		tool := tools.Tool{Scope: tools.ScopeTargetOnly, LocalFiles: []tools.LocalFile{{Pattern: "RULE.md"}}}
		got := scanTool(tool, Chain{Dirs: []string{dir}, GitRootIndex: -1}, Options{})
		if want {
			if len(got.Files) != 1 || got.Files[0].Content != "regular" {
				t.Fatalf("regular file not preserved: %+v", got.Files)
			}
		} else if len(got.Files) != 0 {
			t.Fatalf("nonregular file matched: %+v", got.Files)
		}
		return
	}
	for _, kind := range []string{"regular", "regular-symlink", "fifo", "fifo-symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "RULE.md")
			if kind == "fifo-symlink" || kind == "regular-symlink" {
				path = filepath.Join(dir, "destination")
				if err := os.Symlink(path, filepath.Join(dir, "RULE.md")); err != nil {
					t.Fatal(err)
				}
			}
			want := "nonregular"
			switch kind {
			case "regular", "regular-symlink":
				mustWriteFile(t, path, "regular")
				want = "regular"
			case "fifo", "fifo-symlink":
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				mustMkdirAll(t, path)
			}
			for _, mode := range []string{"scan", "read"} {
				t.Run(mode, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNonregularInstructionsDoNotBlock$", "-test.timeout=10s")
					cmd.Env = append(os.Environ(), "ACTX_SPECIAL_TEST_DIR="+dir, "ACTX_SPECIAL_TEST_MODE="+mode, "ACTX_SPECIAL_TEST_WANT="+want)
					out, err := cmd.CombinedOutput()
					if ctx.Err() != nil {
						t.Fatalf("%s blocked on %s instruction (helper killed and waited): %s", mode, kind, out)
					}
					if err != nil {
						t.Fatalf("helper: %v\n%s", err, out)
					}
				})
			}
		})
	}
}
