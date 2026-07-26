// Command actx shows the full compiled set of AI coding-agent
// instruction files that apply at a given directory, across every major AI coding
// agent, by walking the directory tree and each tool's global config location and
// applying that tool's own real discovery/precedence rules. See docs/design.md.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/citadelgrad/actx/internal/compile"
	"github.com/citadelgrad/actx/internal/inspect"
	"github.com/citadelgrad/actx/internal/render"
	"github.com/citadelgrad/actx/internal/scan"
	"github.com/citadelgrad/actx/internal/tools"
)

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
		enc.Encode(map[string]string{"error": err.Error()})
	} else {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	os.Exit(exitCode(err))
}

// wantsJSON pre-scans the raw args for --json/-json so error formatting can
// match the requested output mode even when flag parsing itself fails (i.e.
// before run() gets a chance to inspect the parsed *jsonOut value).
func wantsJSON(args []string) bool {
	for _, a := range args {
		if a == "-json" || a == "--json" {
			return true
		}
	}
	return false
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
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.SetOutput(io.Discard) // suppress the flag package's own error/usage printing; fs.Usage below prints ours to stderr directly
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: actx [path] [flags]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Shows the compiled set of AI coding-agent instruction files that apply at [path]")
		fmt.Fprintln(os.Stderr, "(default: current directory), across Claude Code, Codex CLI, GitHub Copilot,")
		fmt.Fprintln(os.Stderr, "OpenCode, Cursor, Windsurf, Cline, Aider, and Gemini CLI.")
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
		fmt.Fprintln(os.Stderr, "  - On failure, errors go to stderr; under --json they're a JSON object {\"error\": \"...\"}")
		fmt.Fprintln(os.Stderr, "    instead of plain text. Exit codes: 0 success, 1 runtime error, 2 usage error.")
		fmt.Fprintln(os.Stderr, "  - Every tool has a stable 'slug' field in JSON output; use --tool to filter by it.")
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

	if *showVersion {
		fmt.Fprintln(w, versionString())
		return nil
	}

	if *toolFilter == "list" {
		for _, t := range tools.Registry {
			fmt.Fprintln(w, t.Slug)
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
			return render.InspectJSON(w, reports)
		}
		render.InspectText(w, reports, *full)
		return nil
	}

	results, chain, err := scan.Run(target, scan.Options{})
	if err != nil {
		return fmt.Errorf("scanning %q: %w", target, err)
	}
	results = filterResults(results, wantSlugs)

	if *compileMode {
		compiled := compile.Run(results)
		if *jsonOut {
			return render.CompileJSON(w, compiled, renderOpts)
		}
		render.CompileText(w, compiled, chain, renderOpts)
		return nil
	}

	if *jsonOut {
		return render.JSON(w, results, renderOpts)
	}
	render.Text(w, results, chain, renderOpts)
	return nil
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
