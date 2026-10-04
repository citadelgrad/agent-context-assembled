package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"testing"
)

func TestOutputSpoolHasFiniteByteLimit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	path := filepath.Join(t.TempDir(), "old")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	block := bytes.Repeat([]byte("x"), 256*1024)
	writes := 0
	err := writeSizeGuarded(io.Discard, 1, false, path, func(w io.Writer) error {
		for range 258 {
			writes++
			_, _ = w.Write(block)
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "67108864-byte output staging limit") {
		t.Fatalf("expected explicit finite spool diagnostic, got %v", err)
	}
	if writes != 257 {
		t.Fatalf("limit was not enforced promptly: %d writes", writes)
	}
	old, err := os.ReadFile(path)
	if err != nil || string(old) != "keep" {
		t.Fatalf("old output changed: %q %v", old, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed staging leaked: %v %v", entries, err)
	}
}

func TestIgnoredSinkFailureStopsRendererPromptly(t *testing.T) {
	writes := 0
	err := writeSizeGuarded(&failingOutput{}, 0, false, "", func(w io.Writer) error {
		for range 100 {
			writes++
			_, _ = w.Write([]byte("x")) // text renderers historically ignore write errors
		}
		return nil
	})
	if !errors.Is(err, errOutputSink) {
		t.Fatalf("got %v, want sink failure", err)
	}
	if writes != 1 {
		t.Fatalf("continued rendering after failure: %d writes", writes)
	}
}

func TestCappedOutputSpoolsBeforeRenderingCompletes(t *testing.T) {
	spoolDir := t.TempDir()
	t.Setenv("TMPDIR", spoolDir)
	output := filepath.Join(t.TempDir(), "output")
	block := bytes.Repeat([]byte("x"), 128*1024)
	err := writeSizeGuarded(io.Discard, 1, false, output, func(w io.Writer) error {
		for i := range 8 {
			if _, err := w.Write(block); err != nil {
				return err
			}
			entries, err := os.ReadDir(spoolDir)
			if err != nil {
				t.Fatal(err)
			}
			if i < 2 && len(entries) != 0 {
				t.Fatal("spool created below 256 KiB")
			}
			if i >= 2 && len(entries) != 1 {
				t.Fatalf("after %d KiB: got %d temporary spools, want 1", (i+1)*128, len(entries))
			}
			if i >= 2 {
				info, err := entries[0].Info()
				if err != nil {
					t.Fatal(err)
				}
				if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
					t.Fatalf("staging mode=%o, want 0600", info.Mode().Perm())
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil || info.Size() != 1024*1024 {
		t.Fatalf("full output: %v %v", info, err)
	}
	entries, err := os.ReadDir(spoolDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging leaked: %v %v", entries, err)
	}
}

func TestUnlimitedOutputStreamsDuringRendering(t *testing.T) {
	var delivered bytes.Buffer
	err := writeSizeGuarded(&delivered, 0, false, "", func(w io.Writer) error {
		for range 4 {
			if _, err := io.WriteString(w, "🙂界"); err != nil {
				return err
			}
			if delivered.Len() == 0 {
				t.Fatal("unlimited output retained until render completes instead of streaming")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if delivered.String() != "🙂界🙂界🙂界🙂界" {
		t.Fatalf("output = %q", delivered.String())
	}
}
