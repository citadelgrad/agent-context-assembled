package compile_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/citadelgrad/agent-context-assembled/internal/compile"
	"github.com/citadelgrad/agent-context-assembled/internal/scan"
)

func FuzzCompileLosslessOrderedTransformation(f *testing.F) {
	for _, seed := range [][]byte{
		{},
		[]byte("one"),
		[]byte("one\x00two\n\x00three\r\n"),
		[]byte("🙂界\x00e\u0301\x00------------------------------------------------------------------------"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 32*1024 {
			t.Skip()
		}
		data = []byte(strings.ToValidUTF8(string(data), "�"))
		parts := strings.Split(string(data), "\x00")
		if len(parts) > 16 {
			parts = parts[:16]
		}

		files := make([]scan.MatchedFile, 0, len(parts))
		for i, content := range parts {
			files = append(files, scan.MatchedFile{
				Path:    fmt.Sprintf("/repo/file-%02d.md", i),
				Note:    fmt.Sprintf("generated reason %02d", i),
				Content: content,
			})
		}
		inputs := []scan.ToolResult{
			result(t, "claude-code", files...),
			result(t, "cline"),
		}
		outputs := compile.Run(inputs)
		if len(outputs) != len(inputs) || outputs[0].Slug != "claude-code" || outputs[1].Slug != "cline" {
			t.Fatalf("output tool order/count changed: %+v", outputs)
		}
		tc := outputs[0]
		if tc.Empty != (len(files) == 0) {
			t.Fatalf("Empty = %v, want %v", tc.Empty, len(files) == 0)
		}
		if len(tc.Chunks) != len(files) {
			t.Fatalf("chunks = %d, want %d", len(tc.Chunks), len(files))
		}
		for i, file := range files {
			chunk := tc.Chunks[i]
			if chunk.Path != file.Path || chunk.Reason != file.Note || chunk.Content != file.Content || chunk.Condition != "" {
				t.Fatalf("chunk %d = %+v, want lossless projection of %+v", i, chunk, file)
			}
			charCount := utf8.RuneCountInString(file.Content)
			if chunk.CharCount != charCount || chunk.ChunkTokenEstimate != charCount/4 {
				t.Fatalf("chunk %d counts = %d/%d, want %d/%d", i, chunk.CharCount, chunk.ChunkTokenEstimate, charCount, charCount/4)
			}
		}
		wantAssembled := referenceAssemble(files)
		if tc.Assembled != wantAssembled {
			t.Fatalf("assembled mismatch\ngot:  %q\nwant: %q", tc.Assembled, wantAssembled)
		}
		charCount := utf8.RuneCountInString(wantAssembled)
		if tc.CharCount != charCount || tc.TokenEstimate != charCount/4 {
			t.Fatalf("tool counts = %d/%d, want %d/%d", tc.CharCount, tc.TokenEstimate, charCount, charCount/4)
		}
		if !utf8.ValidString(tc.Assembled) {
			t.Fatal("assembled output is invalid UTF-8 for valid UTF-8 inputs")
		}
		if !outputs[1].Empty || len(outputs[1].Chunks) != 0 || outputs[1].Assembled != "" {
			t.Fatalf("empty second tool changed: %+v", outputs[1])
		}
	})
}

func referenceAssemble(files []scan.MatchedFile) string {
	var assembled strings.Builder
	for i, file := range files {
		assembled.WriteString(strings.Repeat("-", 72))
		assembled.WriteByte('\n')
		fmt.Fprintf(&assembled, "[%d/%d] SOURCE: %s\n", i+1, len(files), file.Path)
		fmt.Fprintf(&assembled, "       WHY: %s\n", file.Note)
		assembled.WriteString(strings.Repeat("-", 72))
		assembled.WriteByte('\n')
		assembled.WriteString(file.Content)
		if !strings.HasSuffix(file.Content, "\n") {
			assembled.WriteByte('\n')
		}
		assembled.WriteByte('\n')
	}
	return assembled.String()
}
