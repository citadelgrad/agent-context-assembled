package render_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/citadelgrad/agent-context-assembled/internal/compile"
	"github.com/citadelgrad/agent-context-assembled/internal/inspect"
	"github.com/citadelgrad/agent-context-assembled/internal/render"
	"github.com/citadelgrad/agent-context-assembled/internal/scan"
)

func FuzzJSONRenderingProjectionAndFiltering(f *testing.F) {
	for _, seed := range [][]byte{{}, []byte("a"), []byte("🙂\x00界\n\x00e\u0301")} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 16*1024 {
			t.Skip()
		}
		parts := strings.Split(strings.ToValidUTF8(string(data), "�"), "\x00")
		if len(parts) > 12 {
			parts = parts[:12]
		}
		results := make([]scan.ToolResult, 0, len(parts)+1)
		for i, content := range parts {
			files := []scan.MatchedFile(nil)
			if i%3 != 0 {
				files = []scan.MatchedFile{{Path: fmt.Sprintf("/file-%d<&>.md", i), Content: content, Note: fmt.Sprintf("note-%d<&>", i)}}
			}
			results = append(results, resultWithFiles(fmt.Sprintf("Tool %d", i), fmt.Sprintf("slug-%d", i), files...))
		}
		for _, all := range []bool{false, true} {
			var buf bytes.Buffer
			if err := render.JSON(&buf, results, render.Options{All: all, Full: false}); err != nil {
				t.Fatal(err)
			}
			var fullBuf bytes.Buffer
			if err := render.JSON(&fullBuf, results, render.Options{All: all, Full: true}); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf.Bytes(), fullBuf.Bytes()) {
				t.Fatal("Full changed JSON output")
			}
			var got []struct {
				Tool  string                                 `json:"tool"`
				Slug  string                                 `json:"slug"`
				Files []struct{ Path, Content, Note string } `json:"files"`
			}
			if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
				t.Fatalf("invalid JSON: %v: %s", err, buf.Bytes())
			}
			want := 0
			for _, result := range results {
				if all || len(result.Files) > 0 {
					want++
				}
			}
			if len(got) != want {
				t.Fatalf("tools = %d, want %d", len(got), want)
			}
			j := 0
			for _, result := range results {
				if !all && len(result.Files) == 0 {
					continue
				}
				if got[j].Tool != result.Tool.Name || got[j].Slug != result.Tool.Slug || len(got[j].Files) != len(result.Files) {
					t.Fatalf("projection mismatch at %d: %+v vs %+v", j, got[j], result)
				}
				for k, file := range result.Files {
					if got[j].Files[k].Path != file.Path || got[j].Files[k].Content != file.Content || got[j].Files[k].Note != file.Note {
						t.Fatalf("file projection mismatch: %+v vs %+v", got[j].Files[k], file)
					}
				}
				j++
			}
			if bytes.Contains(buf.Bytes(), []byte(`\u003c`)) || bytes.Contains(buf.Bytes(), []byte(`\u003e`)) || bytes.Contains(buf.Bytes(), []byte(`\u0026`)) {
				t.Fatal("HTML escaping was enabled")
			}
			for _, compileAll := range []bool{false, true} {
				content := ""
				if len(parts) > 0 {
					content = string(parts[0])
				}
				toolCompiles := []compile.ToolCompile{
					{Tool: "T", Slug: "t", Assembled: content, Chunks: []compile.Chunk{{Path: "/x.md", Content: content}}},
				}
				var cBuf, fBuf bytes.Buffer
				if err := render.CompileJSON(&cBuf, toolCompiles, render.Options{All: compileAll, Full: false}); err != nil {
					t.Fatal(err)
				}
				if err := render.CompileJSON(&fBuf, toolCompiles, render.Options{All: compileAll, Full: true}); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(cBuf.Bytes(), fBuf.Bytes()) {
					t.Fatalf("Full changed CompileJSON output for All=%v", compileAll)
				}
				if bytes.Contains(cBuf.Bytes(), []byte(`\u003c`)) || bytes.Contains(cBuf.Bytes(), []byte(`\u003e`)) || bytes.Contains(cBuf.Bytes(), []byte(`\u0026`)) {
					t.Fatal("CompileJSON HTML escaping was enabled")
				}
			}
			if len(parts) == 0 {
				var emptyRaw bytes.Buffer
				if err := render.JSON(&emptyRaw, results, render.Options{All: true}); err != nil {
					t.Fatal(err)
				}
				if strings.TrimSpace(emptyRaw.String()) != "[]" {
					t.Fatalf("empty input shape = %q, want []", emptyRaw.String())
				}
				var emptyCompile bytes.Buffer
				if err := render.CompileJSON(&emptyCompile, nil, render.Options{All: all}); err != nil {
					t.Fatal(err)
				}
				if strings.TrimSpace(emptyCompile.String()) != "null" {
					t.Fatalf("compile empty shape = %q, want null", emptyCompile.String())
				}
				var emptyInspect bytes.Buffer
				if err := render.InspectJSON(&emptyInspect, nil); err != nil {
					t.Fatal(err)
				}
				if strings.TrimSpace(emptyInspect.String()) != "null" {
					t.Fatalf("inspect empty shape = %q, want null", emptyInspect.String())
				}
			}
		}
	})
}

func TestJSONEmptyOutputShapes(t *testing.T) {
	var raw bytes.Buffer
	if err := render.JSON(&raw, nil, render.Options{All: true}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(raw.String()) != "[]" {
		t.Fatalf("raw nil input = %q, want []", raw.String())
	}
	var compiled bytes.Buffer
	if err := render.CompileJSON(&compiled, nil, render.Options{All: true}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(compiled.String()) != "null" {
		t.Fatalf("compiled nil input = %q, want null", compiled.String())
	}
	var inspected bytes.Buffer
	if err := render.InspectJSON(&inspected, nil); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(inspected.String()) != "null" {
		t.Fatalf("inspect nil input = %q, want null", inspected.String())
	}
}

func FuzzCompileAndInspectJSONRoundTrip(f *testing.F) {
	f.Add("🙂<&>", "body\n")
	f.Fuzz(func(t *testing.T, label, content string) {
		if len(label)+len(content) > 16*1024 {
			t.Skip()
		}
		label, content = strings.ToValidUTF8(label, "�"), strings.ToValidUTF8(content, "�")
		compiled := []compile.ToolCompile{{Tool: label, Slug: "slug", Assembled: content, Chunks: []compile.Chunk{{Path: label, Content: content}}}}
		var compileBuf bytes.Buffer
		if err := render.CompileJSON(&compileBuf, compiled, render.Options{All: true}); err != nil {
			t.Fatal(err)
		}
		var compiledGot []compile.ToolCompile
		if err := json.Unmarshal(compileBuf.Bytes(), &compiledGot); err != nil || len(compiledGot) != 1 || compiledGot[0].Assembled != content {
			t.Fatalf("compile round trip: %+v %v", compiledGot, err)
		}
		reports := []inspect.Report{{Tool: label, Slug: "slug", Summary: content}}
		var inspectBuf bytes.Buffer
		if err := render.InspectJSON(&inspectBuf, reports); err != nil {
			t.Fatal(err)
		}
		var reportsGot []inspect.Report
		if err := json.Unmarshal(inspectBuf.Bytes(), &reportsGot); err != nil || len(reportsGot) != 1 || reportsGot[0] != reports[0] {
			t.Fatalf("inspect round trip: %+v %v", reportsGot, err)
		}
	})
}
