package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGuardUnicodeSplitWritesAndMalformedBytes(t *testing.T) {
	// Ten codepoints: A, smile, ideograph, e, combining accent, five invalid bytes.
	data := []byte("A🙂界e\u0301\xff\xc0\xaf\xe2\x82")
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	path := filepath.Join(dir, "overflow")
	for first := 0; first <= len(data); first++ {
		for second := first; second <= len(data); second++ {
			for _, cap := range []int{9, 10} {
				var out bytes.Buffer
				err := writeSizeGuarded(&out, cap, true, path, func(w io.Writer) error {
					for _, p := range [][]byte{data[:first], data[first:second], data[second:]} {
						if _, err := w.Write(p); err != nil {
							return err
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if cap == 10 {
					if !bytes.Equal(out.Bytes(), data) {
						t.Fatalf("split %d/%d changed raw bytes", first, second)
					}
					continue
				}
				var notice outputOverflow
				if err := json.Unmarshal(out.Bytes(), &notice); err != nil || notice.CharCount != 10 || notice.TokenEstimate != 2 || !notice.Truncated {
					t.Fatalf("split %d/%d count: %+v err=%v", first, second, notice, err)
				}
				var keys map[string]any
				if err := json.Unmarshal(out.Bytes(), &keys); err != nil || len(keys) != 5 {
					t.Fatalf("notice keys: %v %v", keys, err)
				}
				full, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(full, data) {
					t.Fatalf("spill bytes changed: %v", err)
				}
			}
		}
	}
	// Also cross the memory-spill and bufio read boundaries, byte by byte.
	prefix := strings.Repeat("a", 256*1024-2)
	var out bytes.Buffer
	err := writeSizeGuarded(&out, 1, true, path, func(w io.Writer) error {
		if _, err := io.WriteString(w, prefix); err != nil {
			return err
		}
		for _, b := range data {
			if _, err := w.Write([]byte{b}); err != nil {
				return err
			}
		}
		return nil
	})
	var notice outputOverflow
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &notice); err != nil || notice.CharCount != len(prefix)+10 {
		t.Fatalf("spilled Unicode count: %+v %v", notice, err)
	}
}

func TestGuardStagedFailuresAndBelowCapCleanup(t *testing.T) {
	block := bytes.Repeat([]byte("x"), 300*1024)
	renderErr := errors.New("render failed")
	for _, mode := range []string{"callback-error", "below-cap", "rename-error", "create-error"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("TMPDIR", dir)
			destinationDir := t.TempDir()
			path := filepath.Join(destinationDir, "old")
			if err := os.WriteFile(path, []byte("keep"), 0640); err != nil {
				t.Fatal(err)
			}
			cap := 1
			if mode == "below-cap" {
				cap = len(block)
			}
			destination := path
			if mode == "rename-error" {
				destination = destinationDir
			}
			if mode == "create-error" {
				destination = filepath.Join(path, "not-a-directory")
			}
			var out bytes.Buffer
			err := writeSizeGuarded(&out, cap, false, destination, func(w io.Writer) error {
				if _, err := w.Write(block); err != nil {
					return err
				}
				if mode == "callback-error" {
					return renderErr
				}
				return nil
			})
			if mode == "callback-error" && !errors.Is(err, renderErr) {
				t.Fatalf("callback error lost: %v", err)
			}
			if mode != "below-cap" && err == nil {
				t.Fatal("expected failure")
			}
			if mode == "below-cap" && (err != nil || !bytes.Equal(out.Bytes(), block)) {
				t.Fatalf("below cap changed: %v", err)
			}
			if mode == "callback-error" && out.Len() != 0 {
				t.Fatal("callback failure wrote stdout")
			}
			old, err := os.ReadFile(path)
			if err != nil || string(old) != "keep" {
				t.Fatalf("old destination changed: %q %v", old, err)
			}
			for _, check := range []string{dir, destinationDir, filepath.Dir(destinationDir)} {
				entries, err := os.ReadDir(check)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if strings.Contains(entry.Name(), "actx-") {
						t.Fatalf("temporary file leaked: %s", entry.Name())
					}
				}
			}
		})
	}
}

type shortOutput struct{}

func (shortOutput) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestGuardShortWritesAndCallbackPanics(t *testing.T) {
	for _, cap := range []int{0, 1, 100} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(fmt.Sprintf("cap=%d/json=%v", cap, jsonMode), func(t *testing.T) {
				err := writeSizeGuarded(shortOutput{}, cap, jsonMode, filepath.Join(t.TempDir(), "out"), func(w io.Writer) error { _, err := io.WriteString(w, "content"); return err })
				if !errors.Is(err, io.ErrShortWrite) {
					t.Fatalf("short write lost: %v", err)
				}
			})
		}
	}
	marker := &struct{}{}
	defer func() {
		if p := recover(); p != marker {
			t.Fatalf("callback panic changed: %v", p)
		}
	}()
	_ = writeSizeGuarded(io.Discard, 0, false, "", func(io.Writer) error { panic(marker) })
}

func TestGuardAllocationsDoNotScaleWithRenderedBytes(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	block := bytes.Repeat([]byte("x"), 4*1024*1024)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err := writeSizeGuarded(io.Discard, 1, false, "", func(w io.Writer) error {
		for range 2 {
			if _, err := w.Write(block); err != nil {
				return err
			}
		}
		return nil
	})
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("guard allocations for 8 MiB rendered: %d bytes", allocated)
	if allocated > 2*1024*1024 {
		t.Fatalf("guard retained/copied whole output: %d allocated", allocated)
	}
}
