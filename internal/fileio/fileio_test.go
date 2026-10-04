//go:build unix || windows

package fileio

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRegularFilesAndErrors(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "regular")
	if err := os.WriteFile(name, []byte("synthetic contents"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, rooted := range []bool{false, true} {
		t.Run(map[bool]string{false: "path", true: "root"}[rooted], func(t *testing.T) {
			open := func(name string) (*os.File, error) {
				if rooted {
					return OpenRegularAt(root, name)
				}
				return OpenRegular(filepath.Join(dir, name))
			}
			f, err := open("regular")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			data, err := io.ReadAll(f)
			if err != nil || string(data) != "synthetic contents" {
				t.Fatalf("read=%q err=%v", data, err)
			}
			if _, err := f.Write([]byte("not allowed")); err == nil {
				t.Fatal("descriptor must be read-only")
			}
			for _, name := range []string{".", "missing"} {
				f, err := open(name)
				if f != nil {
					f.Close()
					t.Fatalf("nonregular/missing %q returned descriptor", name)
				}
				if err == nil {
					t.Fatalf("%q returned no error", name)
				}
				if name == "missing" && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("missing error lost: %v", err)
				}
				var pathErr *os.PathError
				if !errors.As(err, &pathErr) {
					t.Fatalf("not a PathError: %T", err)
				}
			}
		})
	}
}

func TestRelativeSymlinksAndRootConfinement(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "inside")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "regular"), filepath.Join(parent, "outside")} {
		if err := os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		"relative": "regular", "chain": "relative", "escape": "../outside",
		"absolute": filepath.Join(dir, "regular"), "loop": "loop",
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, name := range []string{"relative", "chain", "absolute", "escape"} {
		f, err := OpenRegular(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("ordinary symlink %q: %v", name, err)
		}
		f.Close()
	}
	for _, name := range []string{"relative", "chain"} {
		f, err := OpenRegularAt(root, name)
		if err != nil {
			t.Fatalf("root-relative symlink %q: %v", name, err)
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil || string(data) != "synthetic" {
			t.Fatalf("root read %q: %q %v", name, data, err)
		}
	}
	for _, name := range []string{"escape", "absolute", "../outside", "loop", filepath.Join(parent, "outside")} {
		f, err := OpenRegularAt(root, name)
		if f != nil {
			f.Close()
			t.Fatalf("invalid root path %q returned descriptor", name)
		}
		if err == nil {
			t.Fatalf("invalid root path %q accepted", name)
		}
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if f, err := OpenRegularAt(root, "regular"); f != nil || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed root: f=%v err=%v", f, err)
	}
}
