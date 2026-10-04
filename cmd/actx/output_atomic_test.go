package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type interruptedOutputReader struct{}

func (interruptedOutputReader) Read([]byte) (int, error) { return 0, errOutputSink }

func TestPartialOverflowCopyFailureCleansUpAndPreservesDestination(t *testing.T) {
	for _, configured := range []bool{false, true} {
		dir := t.TempDir()
		t.Setenv("TMPDIR", dir)
		path := ""
		if configured {
			path = filepath.Join(dir, "old")
			if err := os.WriteFile(path, []byte("keep"), 0640); err != nil {
				t.Fatal(err)
			}
		}
		reader := io.MultiReader(bytes.NewBufferString("partial"), interruptedOutputReader{})
		if _, err := prepareOverflowFile(path, ".txt", reader); !errors.Is(err, errOutputSink) {
			t.Fatalf("copy error lost: %v", err)
		}
		want := 0
		if configured {
			want = 1
			old, err := os.ReadFile(path)
			if err != nil || string(old) != "keep" {
				t.Fatalf("copy error changed destination: %q %v", old, err)
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != want {
			t.Fatalf("copy error leaked: %v %v", entries, err)
		}
	}
}

func TestSpoolCreationFailureIsPromptAndBelowCapNeverOpensOut(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(dir, "missing"))
	path := filepath.Join(dir, "also-missing", "out")
	writes := 0
	block := bytes.Repeat([]byte("x"), 128*1024)
	err := writeSizeGuarded(io.Discard, 1, false, path, func(w io.Writer) error {
		for range 10 {
			writes++
			_, _ = w.Write(block)
		}
		return nil
	})
	if err == nil || writes != 3 {
		t.Fatalf("creation failure was not prompt: err=%v writes=%d", err, writes)
	}
	err = writeSizeGuarded(io.Discard, 100, false, path, func(w io.Writer) error { _, err := io.WriteString(w, "small"); return err })
	if err != nil {
		t.Fatalf("below cap touched invalid output path: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unexpected files: %v %v", entries, err)
	}
}

func TestFailedOverflowNoticePreservesPublishedFileAndCleansTemps(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "temporary", true: "configured"}[configured], func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("TMPDIR", dir)
			path := ""
			if configured {
				path = filepath.Join(dir, "old")
				if err := os.WriteFile(path, []byte("keep"), 0640); err != nil {
					t.Fatal(err)
				}
			}
			err := writeSizeGuarded(&failingOutput{}, 1, false, path, func(w io.Writer) error {
				_, err := w.Write(bytes.Repeat([]byte("x"), 300*1024))
				return err
			})
			if !errors.Is(err, errOutputSink) {
				t.Fatalf("got %v", err)
			}
			if configured {
				published, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(published, bytes.Repeat([]byte("x"), 300*1024)) {
					t.Errorf("failed notice lost complete published output: len=%d err=%v", len(published), err)
				}
			}
			entries, err := os.ReadDir(dir)
			want := 0
			if configured {
				want = 1
			}
			if err != nil || len(entries) != want {
				t.Errorf("failed notice leaked files: %v %v", entries, err)
			}
		})
	}
}
