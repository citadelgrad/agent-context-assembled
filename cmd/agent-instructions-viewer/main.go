// Command agent-instructions-viewer shows the full compiled set of AI coding-agent
// instruction files that apply at a given directory, across every major AI coding
// agent, by walking the directory tree and each tool's global config location and
// applying that tool's own real discovery/precedence rules. See docs/design.md.
package main

import (
	"flag"
	"fmt"
	"os"

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
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agent-instructions-viewer [path] [flags]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Shows the compiled set of AI coding-agent instruction files that apply at [path]")
		fmt.Fprintln(os.Stderr, "(default: current directory), across Claude Code, Codex CLI, GitHub Copilot,")
		fmt.Fprintln(os.Stderr, "OpenCode, Cursor, Windsurf, Cline, Aider, and Gemini CLI.")
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

	results, chain, err := scan.Run(target, scan.Options{})
	if err != nil {
		return fmt.Errorf("scanning %q: %w", target, err)
	}

	renderOpts := render.Options{All: *all, JSON: *jsonOut, Full: *full}
	if *jsonOut {
		return render.JSON(os.Stdout, results, renderOpts)
	}
	render.Text(os.Stdout, results, chain, renderOpts)
	return nil
}
