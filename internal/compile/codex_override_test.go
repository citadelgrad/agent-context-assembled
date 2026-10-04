package compile_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/citadelgrad/agent-context-assembled/internal/compile"
	"github.com/citadelgrad/agent-context-assembled/internal/scan"
)

func TestCompileCodexOverrideReplacesOnlySameDirectoryBase(t *testing.T) {
	for _, mode := range []string{"both", "base-only", "override-only"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			root, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			sub := filepath.Join(root, "sub")
			target := filepath.Join(sub, "leaf")
			global := filepath.Join(home, ".codex", "AGENTS.md")
			ancestor := filepath.Join(root, "AGENTS.md")
			base := filepath.Join(sub, "AGENTS.md")
			override := filepath.Join(sub, "AGENTS.override.md")
			leaf := filepath.Join(target, "AGENTS.md")
			mustWriteFile(t, global, strings.Repeat("g", 40000))
			mustWriteFile(t, ancestor, "R")
			mustWriteFile(t, leaf, "C")
			if mode != "override-only" {
				mustWriteFile(t, base, strings.Repeat("b", 40000))
			}
			if mode != "base-only" {
				mustWriteFile(t, override, "O")
			}
			results, _, err := scan.Run(target, scan.Options{ToolSlugs: map[string]bool{"codex-cli": true}})
			if err != nil || len(results) != 1 {
				t.Fatalf("scan: results=%v err=%v", results, err)
			}
			raw := append([]scan.MatchedFile(nil), results[0].Files...)
			if mode == "both" && len(raw) != 5 {
				t.Fatalf("raw inventory should retain base and override: %+v", raw)
			}
			compiled := compile.Run(results)[0]
			middle := override
			wantBytes := len("ROC")
			if mode == "base-only" {
				middle = base
				wantBytes = len("RC") + 40000
			}
			want := []string{global, ancestor, middle, leaf}
			got := make([]string, len(compiled.Chunks))
			for i, chunk := range compiled.Chunks {
				got[i] = chunk.Path
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("effective paths=%v, want %v", got, want)
			}
			if len(compiled.LimitChecks) != 1 || compiled.LimitChecks[0].Measured != wantBytes || compiled.LimitChecks[0].Exceeds != (mode == "base-only") {
				t.Errorf("effective project byte budget=%+v, want measured=%d", compiled.LimitChecks, wantBytes)
			}
			if mode != "base-only" && strings.Contains(compiled.Assembled, strings.Repeat("b", 32)) {
				t.Error("discarded base content leaked into assembly")
			}
			if !reflect.DeepEqual(results[0].Files, raw) {
				t.Error("compile mutated raw scan inventory")
			}
		})
	}
}
