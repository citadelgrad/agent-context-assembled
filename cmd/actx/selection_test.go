package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestToolSelectionSkipsUnrelatedLiveReads(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	record, err := json.Marshal(map[string]any{
		"type":    "session_meta",
		"payload": map[string]any{"cwd": dir, "base_instructions": map[string]string{"text": strings.Repeat("x", 8<<20)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(os.Getenv("HOME"), ".codex", "sessions", "rollout-unselected.jsonl"), string(record)+"\n")
	for _, selection := range []string{"aider,aider", ","} {
		t.Run(selection, func(t *testing.T) {
			var out bytes.Buffer
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			err := run([]string{"--live", "--json", "--max-chars=0", "--tool=" + selection, dir}, &out)
			runtime.ReadMemStats(&after)
			if err != nil {
				t.Fatal(err)
			}
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= 4<<20 {
				t.Errorf("filtered live invocation allocated %d bytes for unrelated content; want < 4 MiB", allocated)
			}
			var reports []struct{ Slug string }
			if err := json.Unmarshal(out.Bytes(), &reports); err != nil {
				t.Fatal(err)
			}
			if selection == "," {
				if len(reports) != 0 {
					t.Fatalf("empty selection returned reports: %q", out.String())
				}
			} else if len(reports) != 1 || reports[0].Slug != "aider" {
				t.Fatalf("unexpected filtered live output %q", out.String())
			}
		})
	}
}

func TestToolSelectionSkipsUnrelatedScanReads(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".clinerules", "unselected.md"), strings.Repeat("x", 8<<20))
	mustWriteFile(t, filepath.Join(dir, "AGENTS.md"), "selected instructions")
	for _, mode := range []string{"scan", "compile"} {
		t.Run(mode, func(t *testing.T) {
			args := []string{"--json", "--max-chars=0", "--tool=codex-cli,codex-cli"}
			if mode == "compile" {
				args = append(args, "--compile")
			}
			args = append(args, dir)
			var out bytes.Buffer
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			err := run(args, &out)
			runtime.ReadMemStats(&after)
			if err != nil {
				t.Fatal(err)
			}
			// A selected tiny file should not allocate even half the unrelated
			// regular file's size. This observes real reads without timing races,
			// FIFO hangs, or depending on a new scan.Options API in the test.
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= 4<<20 {
				t.Errorf("filtered invocation allocated %d bytes for unrelated content; want < 4 MiB", allocated)
			}
			var results []struct{ Slug string }
			if err := json.Unmarshal(out.Bytes(), &results); err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Slug != "codex-cli" || !strings.Contains(out.String(), "selected instructions") {
				t.Fatalf("unexpected filtered output %q", out.String())
			}
		})
	}
}
