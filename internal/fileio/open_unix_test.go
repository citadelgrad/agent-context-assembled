//go:build unix

package fileio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenRegularRejectsFIFOReplacement(t *testing.T) {
	probeReplacement(t, "regular", false)
}

func TestOpenRegularAtRejectsFIFOReplacement(t *testing.T) {
	probeReplacement(t, "root", false)
}

func TestOpenRegularRejectsSymlinkFIFOReplacement(t *testing.T) {
	probeReplacement(t, "regular-link", false)
	probeReplacement(t, "root-link", false)
}

func TestRawRootOpenFIFOControl(t *testing.T) {
	probeReplacement(t, "raw-root", true)
}

func TestRawOpenFIFOControl(t *testing.T) {
	probeReplacement(t, "raw", true)
}

// Only the child enters a potentially blocking open. CommandContext kills it
// at the deadline and CombinedOutput waits/reaps it; no goroutine is abandoned.
func probeReplacement(t *testing.T, mode string, wantBlocked bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFIFOReplacementChild$", "-test.v")
	cmd.Env = append(os.Environ(), "ACTX_FILEIO_CHILD="+mode, "ACTX_FILEIO_DIR="+t.TempDir())
	output, err := cmd.CombinedOutput()
	if !bytes.Contains(output, []byte("regular stat then FIFO replacement complete")) {
		t.Fatalf("child failed before open: %v\n%s", err, output)
	}
	blocked := errors.Is(ctx.Err(), context.DeadlineExceeded)
	if blocked != wantBlocked {
		t.Fatalf("open blocked=%v, want %v; child exit=%v\n%s", blocked, wantBlocked, err, output)
	}
	if !wantBlocked && err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
	t.Logf("mode=%s blocked=%v child_exit=%v (child reaped)", mode, blocked, err)
}

func TestFIFOReplacementChild(t *testing.T) {
	mode := os.Getenv("ACTX_FILEIO_CHILD")
	if mode == "" {
		return
	}
	name := filepath.Join(os.Getenv("ACTX_FILEIO_DIR"), "candidate")
	if err := os.WriteFile(name, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	openName := name
	if mode == "regular-link" || mode == "root-link" {
		openName = filepath.Join(filepath.Dir(name), "link")
		if err := os.Symlink("candidate", openName); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(openName)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("preflight not regular: info=%v err=%v", info, err)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(name, 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Println("regular stat then FIFO replacement complete")
	var f *os.File
	switch mode {
	case "raw":
		f, err = os.Open(name)
	case "regular", "regular-link":
		f, err = OpenRegular(openName)
	case "root", "raw-root", "root-link":
		root, openErr := os.OpenRoot(filepath.Dir(name))
		if openErr != nil {
			t.Fatal(openErr)
		}
		defer root.Close()
		if mode == "raw-root" {
			f, err = root.Open(filepath.Base(name))
		} else {
			f, err = OpenRegularAt(root, filepath.Base(openName))
		}
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	if f != nil {
		f.Close()
		t.Fatal("nonregular descriptor returned")
	}
	if err == nil {
		t.Fatal("FIFO must be rejected")
	}
}
