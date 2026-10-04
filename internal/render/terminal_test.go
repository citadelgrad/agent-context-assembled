package render_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/citadelgrad/agent-context-assembled/internal/compile"
	"github.com/citadelgrad/agent-context-assembled/internal/inspect"
	"github.com/citadelgrad/agent-context-assembled/internal/render"
	"github.com/citadelgrad/agent-context-assembled/internal/scan"
)

const hostileText = "界🙂\treadable\nnext\x1b[2J\x1b]52;c;payload\a\r\b\x00\x7f\u0085\u009b31m"
const escapedText = "界🙂\treadable\nnext\\x1b[2J\\x1b]52;c;payload\\x07\\x0d\\x08\\x00\\x7f\\x85\\x9b31m"

func TestTextShowsControlOnlyContentRatherThanEmptyFile(t *testing.T) {
	results := []scan.ToolResult{resultWithFiles("test", "test", scan.MatchedFile{Path: "AGENTS.md", Content: "\r\u0085"})}
	var text bytes.Buffer
	render.Text(&text, results, chainFixture("/target"), render.Options{})
	if !strings.Contains(text.String(), `\x0d\x85`) {
		t.Fatalf("control-only file must remain visible, got %q", text.String())
	}
}

func TestSafeTextControlsAndInvalidBytes(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{"plain \\path <tag> 界🙂\n\tend", "plain \\path <tag> 界🙂\n\tend"},
		{hostileText, escapedText},
		{"\x9b31m\x9d52;c;text\x9c", `\x9b31m\x9d52;c;text\x9c`},
		{"\xff\xfe\xc0\xaf", `\xff\xfe\xc0\xaf`},
		{"\u009d52;c;text\u009c", `\x9d52;c;text\x9c`},
		{"\x01\x0b\x0c\x0e\x1f", `\x01\x0b\x0c\x0e\x1f`},
	} {
		if got := render.SafeText(tc.input); got != tc.want {
			t.Errorf("SafeText(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
	var controls strings.Builder
	for r := rune(0); r <= 0x9f; r++ {
		controls.WriteRune(r)
	}
	assertTerminalSafe(t, render.SafeText(controls.String()))
}

func TestCompileTextEscapesTerminalControlsWithoutChangingJSON(t *testing.T) {
	results := []compile.ToolCompile{{
		Tool: hostileText, MergeModel: hostileText, PrecedenceNote: hostileText,
		Assembled:   hostileText,
		LimitChecks: []compile.LimitCheck{{Unit: hostileText, Description: hostileText, Confidence: hostileText}},
	}}
	for _, full := range []bool{false, true} {
		var text bytes.Buffer
		render.CompileText(&text, results, chainFixture(hostileText), render.Options{Full: full})
		assertTerminalSafe(t, text.String())
		if strings.Count(text.String(), escapedText) != 8 {
			t.Fatalf("not all compile fields were escaped and preserved: %q", text.String())
		}
	}
	var encoded bytes.Buffer
	if err := render.CompileJSON(&encoded, results, render.Options{}); err != nil {
		t.Fatal(err)
	}
	var decoded []compile.ToolCompile
	if err := json.Unmarshal(encoded.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, results) {
		t.Fatalf("compile JSON semantics changed: %q", encoded.String())
	}
}

func TestInspectTextEscapesTerminalControlsWithoutChangingJSON(t *testing.T) {
	reports := []inspect.Report{{
		Tool: hostileText, Mechanism: inspect.Mechanism(hostileText), Summary: hostileText,
		Detail: hostileText, ArtifactPath: hostileText, Confidence: hostileText, ExtractedContent: hostileText,
	}}
	for _, full := range []bool{false, true} {
		var text bytes.Buffer
		render.InspectText(&text, reports, full)
		assertTerminalSafe(t, text.String())
		if strings.Count(text.String(), escapedText) != 6 || !strings.Contains(text.String(), "  "+strings.ReplaceAll(escapedText, "\n", "\n  ")) {
			t.Fatalf("not all live fields were escaped and preserved: %q", text.String())
		}
	}
	var encoded bytes.Buffer
	if err := render.InspectJSON(&encoded, reports); err != nil {
		t.Fatal(err)
	}
	var decoded []inspect.Report
	if err := json.Unmarshal(encoded.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, reports) {
		t.Fatalf("live JSON semantics changed: %q", encoded.String())
	}
}

func assertTerminalSafe(t *testing.T, got string) {
	t.Helper()
	for _, r := range got {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			t.Fatalf("raw terminal control U+%04X in captured output", r)
		}
	}
}

func TestTextEscapesTerminalControlsWithoutChangingJSON(t *testing.T) {
	result := resultWithFiles(hostileText, "test", scan.MatchedFile{
		Path: hostileText, Content: hostileText, Note: hostileText,
	})
	result.Tool.PrecedenceNote = hostileText
	result.Tool.NonFileNotes = []string{hostileText}
	results := []scan.ToolResult{result}
	chain := scan.Chain{Dirs: []string{hostileText}, GitRootIndex: 0}
	for _, full := range []bool{false, true} {
		var text bytes.Buffer
		render.Text(&text, results, chain, render.Options{Full: full})
		assertTerminalSafe(t, text.String())
		for _, prefix := range []string{"Compiled AI agent instructions for: ", "Detected repo root (.git): ", "## ", "--- [1/1] ", "    ", "  (not scanned: "} {
			if !strings.Contains(text.String(), prefix+escapedText) {
				t.Errorf("missing escaped field %q in %q", prefix, text.String())
			}
		}
		// Content is indented line by line; newlines/tabs and Unicode remain readable.
		if !strings.Contains(text.String(), "    "+strings.ReplaceAll(escapedText, "\n", "\n    ")) {
			t.Errorf("escaped content lost layout: %q", text.String())
		}
	}
	var encoded bytes.Buffer
	if err := render.JSON(&encoded, results, render.Options{}); err != nil {
		t.Fatal(err)
	}
	var decoded []struct {
		Tool           string
		PrecedenceNote string
		Files          []struct{ Path, Content, Note string }
	}
	if err := json.Unmarshal(encoded.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || len(decoded[0].Files) != 1 {
		t.Fatalf("unexpected JSON shape: %q", encoded.String())
	}
	r := decoded[0]
	if r.Tool != hostileText || r.PrecedenceNote != hostileText || r.Files[0].Path != hostileText || r.Files[0].Content != hostileText || r.Files[0].Note != hostileText {
		t.Fatalf("JSON semantics changed: %q", encoded.String())
	}
}
