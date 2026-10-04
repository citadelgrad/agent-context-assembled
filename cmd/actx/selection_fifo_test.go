//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestFilteredCLICompletesWithUnrelatedFIFO(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "AGENTS.md"), "codex instructions")
	pipe := filepath.Join(dir, ".clinerules", "unrelated.pipe")
	if err := os.MkdirAll(filepath.Dir(pipe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, withFIFO := range []bool{true, false} {
		if !withFIFO {
			if err := os.Remove(pipe); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, bin, "--json", "--tool=codex-cli", "--max-chars=0", dir)
		cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err := cmd.Run()
		cancel()
		if err != nil {
			t.Fatalf("with FIFO %v: %v stderr %q (subprocess bounded to 5 seconds)", withFIFO, err, stderr.String())
		}
		var results []struct{ Slug string }
		if err := json.Unmarshal(out.Bytes(), &results); err != nil || len(results) != 1 || results[0].Slug != "codex-cli" {
			t.Fatalf("with FIFO %v: invalid filtered output %q, error %v", withFIFO, out.String(), err)
		}
	}
}
