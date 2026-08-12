// Command actx shows the full compiled set of AI coding-agent
// instruction files that apply at a given directory, across every major AI coding
// agent, by walking the directory tree and each tool's global config location and
// applying that tool's own real discovery/precedence rules. See docs/design.md.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/citadelgrad/agent-context-assembled/internal/compile"
	"github.com/citadelgrad/agent-context-assembled/internal/inspect"
	"github.com/citadelgrad/agent-context-assembled/internal/render"
	"github.com/citadelgrad/agent-context-assembled/internal/scan"
	"github.com/citadelgrad/agent-context-assembled/internal/tools"
)

// defaultMaxChars caps total rendered output size so a single invocation
// can't silently dump an unbounded amount of text into a calling agent's
// context window. This is an actx-side safety default, not a claim about any
// tool's own documented limit (see internal/compile's LimitChecks for those,
// which are only ever added when sourced from a tool's own docs) -- it's
// chosen to roughly match the single-tool-call context budget (~20k tokens)
// common in agent harnesses. Override with --max-chars; 0 disables the cap.
const defaultMaxChars = 80000

// outputOverflow is the structured notice written in place of the real output
// when it exceeds --max-chars in --json mode.
type outputOverflow struct {
	Truncated     bool   `json:"truncated"`
	Reason        string `json:"reason"`
	CharCount     int    `json:"charCount"`
	TokenEstimate int    `json:"tokenEstimate"`
	OutputFile    string `json:"outputFile"`
}

// writeSizeGuarded renders into an in-memory buffer first so the total output
// size can be checked before any of it reaches w. Output at or under maxChars
// (or when maxChars <= 0) passes through unchanged. Oversized output is
// instead spilled to outputPath when configured, or a temp file otherwise,
// with a short notice written to w in its
// place -- structured JSON in --json mode (so output stays valid, parseable
// JSON even when oversized), or a short plain-text notice otherwise -- so a
// large scan can never silently fill a caller's context window.
func writeSizeGuarded(w io.Writer, maxChars int, jsonMode bool, outputPath string, render func(buf *bytes.Buffer) error) error {
	var buf bytes.Buffer
	if err := render(&buf); err != nil {
		return err
	}
	charCount := utf8.RuneCount(buf.Bytes())
	if maxChars <= 0 || charCount <= maxChars {
		_, err := w.Write(buf.Bytes())
		return err
	}

	ext := ".txt"
	if jsonMode {
		ext = ".json"
	}
	actualPath, err := writeOverflowFile(outputPath, ext, buf.Bytes())
	if err != nil {
		return fmt.Errorf("output exceeded %d chars but could not write overflow file: %w", maxChars, err)
	}

	tokenEstimate := charCount / 4
	if jsonMode {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(outputOverflow{
			Truncated:     true,
			Reason:        fmt.Sprintf("output exceeded the %d-char safety cap (~%d tokens); re-run with --max-chars=0 to print it directly, or a higher --max-chars value", maxChars, maxChars/4),
			CharCount:     charCount,
			TokenEstimate: tokenEstimate,
			OutputFile:    actualPath,
		})
	}
	fmt.Fprintf(w, "Output too large: %d chars (~%d tokens) exceeds the %d-char safety cap.\n", charCount, tokenEstimate, maxChars)
	fmt.Fprintf(w, "Full output written to: %s\n", actualPath)
	fmt.Fprintln(w, "Re-run with --max-chars=0 to print it directly instead, or a higher --max-chars value.")
	return nil
}

func writeOverflowFile(outputPath, ext string, content []byte) (string, error) {
	if outputPath == "" {
		f, err := os.CreateTemp("", "actx-output-*"+ext)
		if err != nil {
			return "", err
		}
		path := f.Name()
		if _, err := f.Write(content); err != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return "", fmt.Errorf("writing %s: %w", path, err)
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("closing %s: %w", path, err)
		}
		return path, nil
	}

	mode := os.FileMode(0o600)
	if info, err := os.Stat(outputPath); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return "", err
	}
	dir := filepath.Dir(outputPath)
	tmp, err := os.CreateTemp(dir, ".actx-output-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return "", err
	}
	if _, err := tmp.Write(content); err != nil {
		cleanup()
		return "", fmt.Errorf("writing %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", fmt.Errorf("syncing %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("closing %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("replacing %s: %w", outputPath, err)
	}
	return outputPath, nil
}

func main() {
	jsonRequested := wantsJSON(os.Args[1:])
	err := run(os.Args[1:], os.Stdout)
	if err == nil {
		return
	}
	if err == flag.ErrHelp {
		// Usage has already been printed by fs.Usage(); a help request is not
		// a failure.
		os.Exit(0)
	}
	if jsonRequested {
		enc := json.NewEncoder(os.Stderr)
		enc.SetEscapeHTML(false)
		enc.Encode(map[string]string{"error": err.Error()})
	} else {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	os.Exit(exitCode(err))
}

// wantsJSON pre-scans the raw args for --json/-json (including the
// --json=<value>/-json=<value> forms the flag package also accepts for bool
// flags) so error formatting can match the requested output mode even when
// flag parsing itself fails (i.e. before run() gets a chance to inspect the
// parsed *jsonOut value). Like flag.Parse, the last occurrence wins if --json
// is passed more than once.
func wantsJSON(args []string) bool {
	want := false
	for _, a := range args {
		name, value, hasValue := strings.Cut(a, "=")
		if name != "-json" && name != "--json" {
			continue
		}
		if !hasValue {
			want = true
			continue
		}
		if b, err := strconv.ParseBool(value); err == nil {
			want = b
		}
	}
	return want
}

// usageError marks errors caused by invalid CLI invocation (bad flags,
// unknown tool slugs) as distinct from runtime errors (bad path, scan
// failure), so main can report a different exit code for each.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// exitCode differentiates usage errors (exit 2, matching the flag package's
// own ExitOnError convention) from runtime errors (exit 1).
func exitCode(err error) int {
	var ue *usageError
	if errors.As(err, &ue) {
		return 2
	}
	return 1
}

func versionString() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return "actx " + info.Main.Version
	}
	return "actx dev"
}

// run implements the CLI, writing all non-error output to w (os.Stdout in
// main; a buffer in tests) so behavior can be verified hermetically without
// capturing the real process stdout.
func run(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("actx", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output structured JSON instead of text")
	all := fs.Bool("all", false, "include every supported tool, even ones with no contributing files (in --live mode every tool is always included, with or without --all)")
	full := fs.Bool("full", false, "show full file content in text output instead of a truncated preview")
	compileMode := fs.Bool("compile", false, "show the assembled 'effective compiled context' per tool instead of the raw file list")
	liveMode := fs.Bool("live", false, "best-effort runtime introspection: look for real session artifacts showing what a tool actually loaded (see docs/research.md)")
	toolFilter := fs.String("tool", "", "comma-separated tool slugs to include, e.g. claude-code,codex-cli (default: all supported tools); pass 'list' to print valid slugs and exit")
	maxChars := fs.Int("max-chars", defaultMaxChars, "safety cap on total output size in characters; output over this is written to a file with a short notice in its place instead of stdout (0 disables the cap)")
	outputPath := fs.String("out", "", "write output exceeding --max-chars to this persistent file instead of an OS-managed temp file")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.SetOutput(io.Discard) // suppress the flag package's own auto error line (e.g. "flag provided but not defined"); fs.Usage below prints ours to stderr directly
	fs.Usage = func() {
		// By the time Usage is called, any auto error line has already been
		// written to the discarded sink above; re-enable stderr so
		// fs.PrintDefaults() below (which also writes via fs.Output()) is
		// actually visible instead of silently discarded.
		fs.SetOutput(os.Stderr)
		fmt.Fprintln(os.Stderr, "Usage: actx [flags] [path]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Shows the compiled set of AI coding-agent instruction files that apply at [path]")
		fmt.Fprintln(os.Stderr, "(default: current directory), across Claude Code, Codex CLI, GitHub Copilot,")
		fmt.Fprintln(os.Stderr, "OpenCode, Cursor, Windsurf, Cline, Aider, Gemini CLI, and Hermes.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Modes:")
		fmt.Fprintln(os.Stderr, "  (default)   list matched instruction files per tool, in real application order")
		fmt.Fprintln(os.Stderr, "  --compile   assemble matched files into an 'effective compiled context' preview per tool,")
		fmt.Fprintln(os.Stderr, "              with a token-count estimate and documented-size-limit checks where sourced")
		fmt.Fprintln(os.Stderr, "  --live      best-effort: look for real on-disk session artifacts / documented debug")
		fmt.Fprintln(os.Stderr, "              mechanisms showing what a tool actually loaded at runtime (not predicted")
		fmt.Fprintln(os.Stderr, "              from disk files like the other two modes); every tool is always reported")
		fmt.Fprintln(os.Stderr, "              (even a 'none found' result), regardless of --all")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Agent/scripting notes:")
		fmt.Fprintln(os.Stderr, "  - Flags must come before [path] (Go's flag parser stops at the first non-flag arg);")
		fmt.Fprintln(os.Stderr, "    e.g. 'actx --json .', not 'actx . --json'.")
		fmt.Fprintln(os.Stderr, "  - On failure, errors go to stderr; under --json they're a JSON object {\"error\": \"...\"}")
		fmt.Fprintln(os.Stderr, "    instead of plain text. Exit codes: 0 success, 1 runtime error, 2 usage error.")
		fmt.Fprintln(os.Stderr, "  - Every tool has a stable 'slug' field in JSON output; use --tool to filter by it.")
		fmt.Fprintf(os.Stderr, "  - Output over --max-chars (default %d, ~%d tokens) is redirected to a file with a\n", defaultMaxChars, defaultMaxChars/4)
		fmt.Fprintln(os.Stderr, "    short notice in its place, so a large scan can't fill an agent's context window unbounded.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return err
		}
		return &usageError{err}
	}

	if fs.NArg() > 1 {
		return &usageError{fmt.Errorf("unexpected extra argument(s) %v after path %q -- flags must come before [path], e.g. \"actx --json %s\" not \"actx %s --json\"", fs.Args()[1:], fs.Arg(0), fs.Arg(0), fs.Arg(0))}
	}

	if *showVersion {
		fmt.Fprintln(w, versionString())
		return nil
	}

	if *toolFilter == "list" {
		slugs := make([]string, 0, len(tools.Registry))
		for _, t := range tools.Registry {
			slugs = append(slugs, t.Slug)
		}
		if *jsonOut {
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			enc.SetEscapeHTML(false)
			return enc.Encode(slugs)
		}
		for _, s := range slugs {
			fmt.Fprintln(w, s)
		}
		return nil
	}

	var wantSlugs map[string]bool
	if *toolFilter != "" {
		wantSlugs = make(map[string]bool)
		for _, s := range strings.Split(*toolFilter, ",") {
			if s = strings.TrimSpace(s); s != "" {
				wantSlugs[s] = true
			}
		}
		valid := make(map[string]bool, len(tools.Registry))
		for _, t := range tools.Registry {
			valid[t.Slug] = true
		}
		for s := range wantSlugs {
			if !valid[s] {
				return &usageError{fmt.Errorf("unknown tool slug %q (run --tool=list to see valid slugs)", s)}
			}
		}
	}

	target := "."
	if fs.NArg() > 0 {
		target = fs.Arg(0)
	}

	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("cannot access %q: %w", target, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", target)
	}

	renderOpts := render.Options{All: *all, JSON: *jsonOut, Full: *full}

	if *liveMode {
		reports := filterReports(inspect.Run(target), wantSlugs)
		if *jsonOut {
			return writeSizeGuarded(w, *maxChars, true, *outputPath, func(buf *bytes.Buffer) error {
				return render.InspectJSON(buf, reports)
			})
		}
		return writeSizeGuarded(w, *maxChars, false, *outputPath, func(buf *bytes.Buffer) error {
			render.InspectText(buf, reports, *full)
			return nil
		})
	}

	results, chain, err := scan.Run(target, scan.Options{})
	if err != nil {
		return fmt.Errorf("scanning %q: %w", target, err)
	}
	results = filterResults(results, wantSlugs)

	if *compileMode {
		compiled := compile.Run(results)
		if *jsonOut {
			return writeSizeGuarded(w, *maxChars, true, *outputPath, func(buf *bytes.Buffer) error {
				return render.CompileJSON(buf, compiled, renderOpts)
			})
		}
		return writeSizeGuarded(w, *maxChars, false, *outputPath, func(buf *bytes.Buffer) error {
			render.CompileText(buf, compiled, chain, renderOpts)
			return nil
		})
	}

	if *jsonOut {
		return writeSizeGuarded(w, *maxChars, true, *outputPath, func(buf *bytes.Buffer) error {
			return render.JSON(buf, results, renderOpts)
		})
	}
	return writeSizeGuarded(w, *maxChars, false, *outputPath, func(buf *bytes.Buffer) error {
		render.Text(buf, results, chain, renderOpts)
		return nil
	})
}

// filterResults narrows results to the tools named in want, by slug. A nil
// want (i.e. --tool was not passed) is a no-op.
func filterResults(results []scan.ToolResult, want map[string]bool) []scan.ToolResult {
	if want == nil {
		return results
	}
	out := make([]scan.ToolResult, 0, len(want))
	for _, r := range results {
		if want[r.Tool.Slug] {
			out = append(out, r)
		}
	}
	return out
}

// filterReports narrows reports to the tools named in want, by slug. A nil
// want (i.e. --tool was not passed) is a no-op.
func filterReports(reports []inspect.Report, want map[string]bool) []inspect.Report {
	if want == nil {
		return reports
	}
	out := make([]inspect.Report, 0, len(want))
	for _, r := range reports {
		if want[r.Slug] {
			out = append(out, r)
		}
	}
	return out
}
