// Command agent-instructions-viewer shows the full compiled set of AI coding-agent
// instruction files that apply at a given directory, across every major AI coding
// agent, by walking the directory tree and each tool's global config location and
// applying that tool's own real discovery/precedence rules. See docs/design.md.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/citadelgrad/agent-instructions-viewer/internal/compile"
	"github.com/citadelgrad/agent-instructions-viewer/internal/inspect"
	"github.com/citadelgrad/agent-instructions-viewer/internal/render"
	"github.com/citadelgrad/agent-instructions-viewer/internal/scan"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("agent-instructions-viewer", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output structured JSON instead of text")
	all := fs.Bool("all", false, "include every supported tool, even ones with no contributing files")
	full := fs.Bool("full", false, "show full file content in text output instead of a truncated preview")
	compileMode := fs.Bool("compile", false, "show the assembled 'effective compiled context' per tool instead of the raw file list")
	liveMode := fs.Bool("live", false, "best-effort runtime introspection: look for real session artifacts showing what a tool actually loaded (see docs/research.md)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-instructions-viewer [path] [flags]")
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
		fmt.Fprintln(os.Stderr, "              from disk files like the other two modes); ignores [path] scanning flags")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
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
		reports := inspect.Run(target)
		if *jsonOut {
			return render.InspectJSON(os.Stdout, reports)
		}
		render.InspectText(os.Stdout, reports, *full)
		return nil
	}

	results, chain, err := scan.Run(target, scan.Options{})
	if err != nil {
		return fmt.Errorf("scanning %q: %w", target, err)
	}

	if *compileMode {
		compiled := compile.Run(results)
		if *jsonOut {
			return render.CompileJSON(os.Stdout, compiled, renderOpts)
		}
		render.CompileText(os.Stdout, compiled, chain, renderOpts)
		return nil
	}

	if *jsonOut {
		return render.JSON(os.Stdout, results, renderOpts)
	}
	render.Text(os.Stdout, results, chain, renderOpts)
	return nil
}
