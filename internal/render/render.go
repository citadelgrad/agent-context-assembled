// Package render turns scan, compile, and inspect results into terminal text
// or JSON output.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/citadelgrad/agent-context-assembled/internal/compile"
	"github.com/citadelgrad/agent-context-assembled/internal/inspect"
	"github.com/citadelgrad/agent-context-assembled/internal/scan"
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

func truncatePreview(content string, limit int) (string, bool) {
	if limit < 0 {
		limit = 0
	}
	byteOffset := 0
	for range limit {
		if byteOffset >= len(content) {
			return content, false
		}
		_, size := utf8.DecodeRuneInString(content[byteOffset:])
		byteOffset += size
	}
	if byteOffset >= len(content) {
		return content, false
	}
	return content[:byteOffset], true
}

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
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

// Text writes a readable, grouped-by-tool terminal view.
func Text(w io.Writer, results []scan.ToolResult, chain scan.Chain, opts Options) {
	target := chain.Dirs[len(chain.Dirs)-1]
	fmt.Fprintf(w, "Compiled AI agent instructions for: %s\n", SafeText(target))
	if chain.GitRootIndex >= 0 {
		fmt.Fprintf(w, "Detected repo root (.git): %s\n", SafeText(chain.Dirs[chain.GitRootIndex]))
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
		fmt.Fprintf(w, "## %s\n", SafeText(r.Tool.Name))
		fmt.Fprintf(w, "%s\n", SafeText(r.Tool.PrecedenceNote))
		for _, n := range r.Tool.NonFileNotes {
			fmt.Fprintf(w, "  (not scanned: %s)\n", SafeText(n))
		}
		if len(r.Files) == 0 {
			fmt.Fprintln(w, "  (no contributing files found)")
			continue
		}
		fmt.Fprintln(w)
		for i, f := range r.Files {
			fmt.Fprintf(w, "--- [%d/%d] %s\n", i+1, len(r.Files), SafeText(f.Path))
			fmt.Fprintf(w, "    %s\n", SafeText(f.Note))
			body := f.Content
			truncated := false
			if !opts.Full {
				body, truncated = truncatePreview(body, previewLimit)
			}
			body = SafeText(body)
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

// CompileJSON writes the compiled-context view (see internal/compile) as JSON.
func CompileJSON(w io.Writer, results []compile.ToolCompile, opts Options) error {
	out := results
	if !opts.All {
		out = make([]compile.ToolCompile, 0, len(results))
		for _, r := range results {
			if !r.Empty {
				out = append(out, r)
			}
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

// CompileText writes a readable rendering of the compiled-context view: per
// tool, its merge model, the assembled text (with per-chunk separators
// already embedded by internal/compile), a token estimate, and any
// documented-limit check results.
func CompileText(w io.Writer, results []compile.ToolCompile, chain scan.Chain, opts Options) {
	target := chain.Dirs[len(chain.Dirs)-1]
	fmt.Fprintf(w, "Compiled context preview for: %s\n", SafeText(target))
	fmt.Fprintln(w, "(This assembles matched files in each tool's real merge order. It approximates what a tool")
	fmt.Fprintln(w, "would load, built purely from on-disk files -- it is not a byte-for-byte simulation of the")
	fmt.Fprintln(w, "tool's own internal prompt assembly. Token counts are len(content)/4 estimates, not exact.)")
	fmt.Fprintln(w, strings.Repeat("=", 72))

	shown := 0
	for _, r := range results {
		if !opts.All && r.Empty {
			continue
		}
		shown++
		fmt.Fprintln(w)
		fmt.Fprintf(w, "## %s\n", SafeText(r.Tool))
		fmt.Fprintf(w, "Merge model: %s\n", SafeText(r.MergeModel))
		if r.PrecedenceNote != "" {
			fmt.Fprintf(w, "%s\n", SafeText(r.PrecedenceNote))
		}
		if r.Empty {
			fmt.Fprintln(w, "  (no contributing files -- nothing to compile)")
			continue
		}
		fmt.Fprintln(w)

		body := r.Assembled
		truncated := false
		if !opts.Full {
			// For compile mode, scale the preview budget with chunk count so
			// multi-file tools aren't unfairly cut to a single-file's worth of
			// text; still bounded so --full is meaningfully different.
			limit := previewLimit * len(r.Chunks)
			if limit < previewLimit {
				limit = previewLimit
			}
			body, truncated = truncatePreview(body, limit)
		}
		for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
			fmt.Fprintf(w, "%s\n", SafeText(line))
		}
		if truncated {
			fmt.Fprintln(w, "... (truncated, use --full for complete assembled content)")
		}

		fmt.Fprintln(w)
		fmt.Fprintf(w, "Estimated size: %d chars (~%d tokens, len/4 estimate)\n", r.CharCount, r.TokenEstimate)
		for _, lc := range r.LimitChecks {
			status := "within documented limit"
			if lc.Exceeds {
				status = "EXCEEDS documented limit"
			}
			fmt.Fprintf(w, "Limit check [%s]: %d/%d %s -- %s (confidence: %s)\n",
				status, lc.Measured, lc.LimitValue, SafeText(lc.Unit), SafeText(lc.Description), SafeText(lc.Confidence))
		}
	}

	if shown == 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "No instruction files found for any known tool at this location.")
		fmt.Fprintln(w, "Run with --all to list every supported tool, including ones with no matches.")
	}
}

// InspectJSON writes runtime-introspection reports (see internal/inspect) as JSON.
func InspectJSON(w io.Writer, reports []inspect.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(reports)
}

// InspectText writes a readable rendering of runtime-introspection reports.
// Every known tool always appears, even when nothing was found -- this
// feature must never silently omit a tool.
func InspectText(w io.Writer, reports []inspect.Report, full bool) {
	fmt.Fprintln(w, "Runtime context introspection (best-effort; formats/paths can change across tool versions)")
	fmt.Fprintln(w, strings.Repeat("=", 72))
	for _, r := range reports {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "## %s [%s]\n", SafeText(r.Tool), SafeText(string(r.Mechanism)))
		fmt.Fprintf(w, "%s\n", SafeText(r.Summary))
		if r.ArtifactPath != "" {
			fmt.Fprintf(w, "  artifact: %s\n", SafeText(r.ArtifactPath))
		}
		if r.ArtifactCount > 0 {
			fmt.Fprintf(w, "  count: %d\n", r.ArtifactCount)
		}
		if !r.LastModified.IsZero() {
			fmt.Fprintf(w, "  last modified: %s\n", r.LastModified.Format("2006-01-02T15:04:05Z07:00"))
		}
		if r.Detail != "" {
			fmt.Fprintf(w, "  detail: %s\n", SafeText(r.Detail))
		}
		if r.Confidence != "" {
			fmt.Fprintf(w, "  confidence: %s\n", SafeText(r.Confidence))
		}
		if r.ExtractedContent != "" {
			fmt.Fprintln(w, "  --- extracted content ---")
			body := r.ExtractedContent
			truncated := false
			if !full {
				body, truncated = truncatePreview(body, previewLimit)
			}
			for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
				fmt.Fprintf(w, "  %s\n", SafeText(line))
			}
			if truncated {
				fmt.Fprintln(w, "  ... (truncated, use --full for complete content)")
			}
		}
	}
}
