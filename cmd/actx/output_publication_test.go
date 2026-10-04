package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type publicationObserver struct{ observe func() }

func (w publicationObserver) Write(p []byte) (int, error) {
	w.observe()
	return len(p), nil
}

func TestOverflowNoticeOnlyAfterFilePublication(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		dir := t.TempDir()
		t.Setenv("TMPDIR", dir)
		path := filepath.Join(dir, "out")
		if err := os.WriteFile(path, []byte("old content"), 0600); err != nil {
			t.Fatal(err)
		}
		writes := 0
		observer := publicationObserver{observe: func() {
			writes++
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "new content" {
				t.Errorf("notice exposed before output was ready: content=%q err=%v", data, err)
			}
		}}
		err := writeSizeGuarded(observer, 1, jsonMode, path, func(w io.Writer) error {
			_, err := io.WriteString(w, "new content")
			return err
		})
		if err != nil || writes == 0 {
			t.Fatalf("missing successful notice: writes=%d err=%v", writes, err)
		}
	}
}

func TestFailedOverflowPublicationEmitsNoNotice(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	destination := filepath.Join(dir, "directory-not-file")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := writeSizeGuarded(&out, 1, true, destination, func(w io.Writer) error {
		_, err := io.WriteString(w, "new content")
		return err
	})
	if err == nil {
		t.Fatal("expected publication failure")
	}
	if out.Len() != 0 {
		t.Fatalf("failed publication emitted a misleading success notice: %q", out.String())
	}
}
