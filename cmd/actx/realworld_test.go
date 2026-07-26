//go:build realworld

// Real-world smoke test: exercises the compiled actx binary against every
// real project directory on this machine, across a matrix of flag
// combinations, looking for crashes, invalid JSON, unexpected exit codes, and
// slow runs. Deliberately excluded from the default `go test ./...` run
// (opt-in via -tags realworld) since it reads this machine's real project
// directories rather than hermetic fixtures, so results vary by machine and
// by whatever happens to be on disk that day.
//
// Run:
//
//	go test -tags realworld -timeout 30m -v ./cmd/actx/ -run TestRealWorldProjects
//
// Env vars:
//
//	ACTX_REALWORLD_ROOT   directory whose immediate subdirectories are each
//	                      treated as one project to test (default: /Volumes/qwiizlab/projects)
//	ACTX_REALWORLD_REPORT if set, a JSON-lines file path to write one record
//	                      per invocation to, for offline analysis
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const realWorldPerRunTimeout = 30 * time.Second

// invocation is one flag combination to try against a project directory.
type invocation struct {
	label string
	args  []string
}

func realWorldInvocations(dir string) []invocation {
	return []invocation{
		{"default", []string{dir}},
		{"json", []string{"--json", dir}},
		{"all-json", []string{"--all", "--json", dir}},
		{"full", []string{"--full", dir}},
		{"compile", []string{"--compile", dir}},
		{"compile-json", []string{"--compile", "--json", dir}},
		{"live", []string{"--live", dir}},
		{"live-json", []string{"--live", "--json", dir}},
		{"tool-claude-code", []string{"--tool=claude-code", dir}},
	}
}

// record is one invocation's outcome, written to the JSON-lines report when
// ACTX_REALWORLD_REPORT is set.
type record struct {
	Project    string `json:"project"`
	Label      string `json:"label"`
	Args       string `json:"args"`
	ExitCode   int    `json:"exitCode"`
	TimedOut   bool   `json:"timedOut"`
	ElapsedMS  int64  `json:"elapsedMs"`
	StdoutLen  int    `json:"stdoutLen"`
	StderrLen  int    `json:"stderrLen"`
	StdoutHead string `json:"stdoutHead,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	Anomaly    string `json:"anomaly,omitempty"`
}

func runBinaryWithTimeout(homeDir, binPath string, timeout time.Duration, args ...string) (stdout, stderr string, exitCode int, timedOut bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return outBuf.String(), errBuf.String(), -1, true
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode(), false
		}
		return outBuf.String(), errBuf.String(), -1, false
	}
	return outBuf.String(), errBuf.String(), 0, false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("... [%d more bytes]", len(s)-n)
}

func TestRealWorldProjects(t *testing.T) {
	root := os.Getenv("ACTX_REALWORLD_ROOT")
	if root == "" {
		root = "/Volumes/qwiizlab/projects"
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("real-world root %q not available: %v", root, err)
	}

	var dirs []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dirs = append(dirs, filepath.Join(root, e.Name()))
	}
	sort.Strings(dirs)
	if len(dirs) == 0 {
		t.Skipf("no project subdirectories found under %q", root)
	}

	bin := buildBinary(t)
	home := t.TempDir() // shared fake $HOME: no invocation should touch this machine's real ~/.claude, ~/.codex, etc.

	var reportFile *os.File
	if path := os.Getenv("ACTX_REALWORLD_REPORT"); path != "" {
		reportFile, err = os.Create(path)
		if err != nil {
			t.Fatalf("creating report file: %v", err)
		}
		defer reportFile.Close()
	}

	type slow struct {
		project string
		label   string
		d       time.Duration
	}
	var (
		total     int
		hardCount int
		softCount int
		slowest   []slow
	)

	for _, dir := range dirs {
		project := filepath.Base(dir)
		for _, inv := range realWorldInvocations(dir) {
			total++
			start := time.Now()
			stdout, stderr, code, timedOut := runBinaryWithTimeout(home, bin, realWorldPerRunTimeout, inv.args...)
			elapsed := time.Since(start)

			if elapsed > 3*time.Second {
				slowest = append(slowest, slow{project, inv.label, elapsed})
			}

			rec := record{
				Project:   project,
				Label:     inv.label,
				Args:      strings.Join(inv.args, " "),
				ExitCode:  code,
				TimedOut:  timedOut,
				ElapsedMS: elapsed.Milliseconds(),
				StdoutLen: len(stdout),
				StderrLen: len(stderr),
			}

			combined := stdout + "\n" + stderr
			isJSONMode := strings.Contains(rec.Args, "--json")

			switch {
			case timedOut:
				hardCount++
				rec.Anomaly = "timeout"
				rec.Stderr = truncate(stderr, 2000)
				t.Errorf("[%s/%s] timed out after %s (args: %v)", project, inv.label, elapsed, inv.args)
			case strings.Contains(combined, "panic:") || strings.Contains(combined, "goroutine "):
				hardCount++
				rec.Anomaly = "panic"
				rec.StdoutHead = truncate(stdout, 2000)
				rec.Stderr = truncate(stderr, 2000)
				t.Errorf("[%s/%s] crashed (args: %v)\nstdout: %s\nstderr: %s", project, inv.label, inv.args, truncate(stdout, 1000), truncate(stderr, 1000))
			case code < 0 || code > 2:
				hardCount++
				rec.Anomaly = "bad-exit-code"
				rec.Stderr = truncate(stderr, 2000)
				t.Errorf("[%s/%s] unexpected exit code %d (args: %v)\nstderr: %s", project, inv.label, code, inv.args, truncate(stderr, 1000))
			case code == 0 && isJSONMode && !json.Valid([]byte(stdout)):
				hardCount++
				rec.Anomaly = "invalid-json"
				rec.StdoutHead = truncate(stdout, 2000)
				t.Errorf("[%s/%s] --json produced invalid JSON (args: %v)\nstdout: %s", project, inv.label, inv.args, truncate(stdout, 1000))
			case code == 0 && stderr != "":
				hardCount++
				rec.Anomaly = "unexpected-stderr"
				rec.Stderr = truncate(stderr, 2000)
				t.Errorf("[%s/%s] exit 0 but wrote to stderr (args: %v)\nstderr: %s", project, inv.label, inv.args, stderr)
			case code == 1:
				softCount++
				rec.Anomaly = "runtime-error"
				rec.Stderr = truncate(stderr, 2000)
				t.Logf("[%s/%s] runtime error (exit 1, args: %v): %s", project, inv.label, inv.args, strings.TrimSpace(stderr))
			case code == 2:
				softCount++
				rec.Anomaly = "usage-error"
				rec.Stderr = truncate(stderr, 2000)
				t.Logf("[%s/%s] usage error (exit 2, args: %v): %s", project, inv.label, inv.args, strings.TrimSpace(stderr))
			default:
				rec.StdoutHead = truncate(stdout, 300)
			}

			if reportFile != nil {
				line, _ := json.Marshal(rec)
				reportFile.Write(line)
				reportFile.Write([]byte("\n"))
			}
		}
	}

	sort.Slice(slowest, func(i, j int) bool { return slowest[i].d > slowest[j].d })
	if len(slowest) > 15 {
		slowest = slowest[:15]
	}

	t.Logf("=== real-world summary ===")
	t.Logf("root: %s", root)
	t.Logf("projects: %d, invocations: %d", len(dirs), total)
	t.Logf("hard anomalies (crash/timeout/bad-exit/invalid-json/unexpected-stderr): %d", hardCount)
	t.Logf("soft observations (exit 1 or 2): %d", softCount)
	if len(slowest) > 0 {
		t.Logf("slowest runs (>3s):")
		for _, s := range slowest {
			t.Logf("  %8s  %-18s %s", s.d.Round(time.Millisecond), s.label, s.project)
		}
	}
}
