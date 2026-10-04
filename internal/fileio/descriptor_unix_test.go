//go:build unix

package fileio

import (
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
)

func TestRejectedDescriptorsAreClosed(t *testing.T) {
	// Disable GC so a missing Close cannot be hidden by finalizers.
	oldGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(oldGC)
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	count := func() int {
		f, err := os.Open("/dev/fd")
		if err != nil {
			t.Skipf("descriptor enumeration unavailable: %v", err)
		}
		defer f.Close()
		// Read names only: statting entries can race the directory stream's
		// own descriptor handling on older Go releases (notably Go 1.26).
		entries, err := f.Readdirnames(-1)
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := count()
	for range 64 {
		for _, rooted := range []bool{false, true} {
			var f *os.File
			var err error
			if rooted {
				f, err = OpenRegularAt(root, ".")
			} else {
				f, err = OpenRegular(dir)
			}
			if f != nil {
				f.Close()
				t.Fatal("directory descriptor escaped")
			}
			if err == nil {
				t.Fatal("directory accepted")
			}
		}
	}
	if after := count(); after != before {
		t.Fatalf("descriptor leak: before=%d after=%d", before, after)
	}
}

func TestOpenedDescriptorSurvivesPathReplacement(t *testing.T) {
	for _, rooted := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "candidate")
		if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		var f *os.File
		if rooted {
			f, err = OpenRegularAt(root, "candidate")
		} else {
			f, err = OpenRegular(path)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(f)
		if err != nil || string(data) != "original" {
			t.Fatalf("rooted=%v data=%q err=%v", rooted, data, err)
		}
	}
}
