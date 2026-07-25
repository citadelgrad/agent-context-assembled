// Package render turns scan results into terminal text or JSON output.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/citadelgrad/agent-instructions-viewer/internal/scan"
)

// Options controls rendering.
type Options struct {
	// All, if true, includes tools with zero contributing files.
	All bool
	// JSON, if true, renders machine-readable JSON instead of text.
	JSON bool
	// Full, if true, disables preview truncation in the text renderer. JSON output
	// is always full regardless of this flag.
	Full bool
}

// previewLimit is the default number of characters shown per file in the text
// renderer before truncating, unless Options.Full is set.
const previewLimit = 2000

// jsonFile mirrors scan.MatchedFile for the --json output contract described in
// the README: an array per tool of {path, content} in application order. Note is
// included as bonus context; content is never truncated in JSON mode regardless of
// what preview settings were used for the text renderer.
type jsonFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Note    string `json:"note"`
}

type jsonTool struct {
	Tool           string     `json:"tool"`
	Slug           string     `json:"slug"`
	PrecedenceNote string     `json:"precedenceNote"`
	Files          []jsonFile `json:"files"`
}

// JSON writes the full compiled view as a JSON array, one object per tool.
func JSON(w io.Writer, results []scan.ToolResult, opts Options) error {
	out := make([]jsonTool, 0, len(results))
	for _, r := range results {
		if !opts.All && len(r.Files) == 0 {
			continue
		}
		files := make([]jsonFile, 0, len(r.Files))
		for _, f := range r.Files {
			files = append(files, jsonFile{Path: f.Path, Content: f.Content, Note: f.Note})
		}
		out = append(out, jsonTool{
			Tool:           r.Tool.Name,
			Slug:           r.Tool.Slug,
			PrecedenceNote: r.Tool.PrecedenceNote,
			Files:          files,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// Text writes a readable, grouped-by-tool terminal view.
func Text(w io.Writer, results []scan.ToolResult, chain scan.Chain, opts Options) {
	target := chain.Dirs[len(chain.Dirs)-1]
	fmt.Fprintf(w, "Compiled AI agent instructions for: %s\n", target)
	if chain.GitRootIndex >= 0 {
		fmt.Fprintf(w, "Detected repo root (.git): %s\n", chain.Dirs[chain.GitRootIndex])
	} else {
		fmt.Fprintln(w, "Detected repo root (.git): none found")
	}
	fmt.Fprintln(w, strings.Repeat("=", 72))

	shown := 0
	for _, r := range results {
		if !opts.All && len(r.Files) == 0 {
			continue
		}
		shown++
		fmt.Fprintln(w)
		fmt.Fprintf(w, "## %s\n", r.Tool.Name)
		fmt.Fprintf(w, "%s\n", r.Tool.PrecedenceNote)
		for _, n := range r.Tool.NonFileNotes {
			fmt.Fprintf(w, "  (not scanned: %s)\n", n)
		}
		if len(r.Files) == 0 {
			fmt.Fprintln(w, "  (no contributing files found)")
			continue
		}
		fmt.Fprintln(w)
		for i, f := range r.Files {
			fmt.Fprintf(w, "--- [%d/%d] %s\n", i+1, len(r.Files), f.Path)
			fmt.Fprintf(w, "    %s\n", f.Note)
			body := f.Content
			truncated := false
			if !opts.Full && len(body) > previewLimit {
				body = body[:previewLimit]
				truncated = true
			}
			if strings.TrimSpace(body) == "" {
				fmt.Fprintln(w, "    (empty file)")
			} else {
				for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
					fmt.Fprintf(w, "    %s\n", line)
				}
				if truncated {
					fmt.Fprintln(w, "    ... (truncated, use --full for complete content)")
				}
			}
			fmt.Fprintln(w)
		}
	}

	if shown == 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "No instruction files found for any known tool at this location.")
		fmt.Fprintln(w, "Run with --all to list every supported tool, including ones with no matches.")
	}
}
