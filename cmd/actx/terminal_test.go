package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode"
)

func assertSafeCLIText(t *testing.T, s string) {
	t.Helper()
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			t.Fatalf("raw terminal control U+%04X in captured CLI output", r)
		}
	}
}

func TestRunTextModesEscapeControlsAndKeepJSONContent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows rejects control bytes in filenames")
	}
	isolateHome(t)
	dir := filepath.Join(t.TempDir(), "project\x1b[2J")
	const content = "readable 界🙂\tline\nnext\x1b]52;c;payload\a\r\u009b31m"
	mustWriteFile(t, filepath.Join(dir, "AGENTS.md"), content)
	record, err := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{
		"cwd": dir, "base_instructions": map[string]string{"text": content},
	}})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(os.Getenv("HOME"), ".codex", "sessions", "rollout-test.jsonl"), string(record)+"\n")
	for _, mode := range []string{"scan", "compile", "live"} {
		t.Run(mode, func(t *testing.T) {
			flags := []string{"--tool=codex-cli", "--full"}
			if mode != "scan" {
				flags = append(flags, "--"+mode)
			}
			invoke := func(extra ...string) string {
				t.Helper()
				args := append(append([]string{}, flags...), extra...)
				var out bytes.Buffer
				if err := run(append(args, dir), &out); err != nil {
					t.Fatal(err)
				}
				return out.String()
			}
			text := invoke("--max-chars=0")
			assertSafeCLIText(t, text)
			if !strings.Contains(text, `next\x1b]52;c;payload\x07\x0d\x9b31m`) || !strings.Contains(text, "readable 界🙂\tline") {
				t.Fatalf("text escaped incorrectly: %q", text)
			}
			var decoded []map[string]any
			encoded := invoke("--json", "--max-chars=0")
			if err := json.Unmarshal([]byte(encoded), &decoded); err != nil || len(decoded) != 1 {
				t.Fatalf("bad JSON output: %q; error %v", encoded, err)
			}
			var got string
			switch mode {
			case "scan":
				got = decoded[0]["files"].([]any)[0].(map[string]any)["content"].(string)
			case "compile":
				got = decoded[0]["assembled"].(string)
			case "live":
				got = decoded[0]["extractedContent"].(string)
			}
			if !strings.Contains(got, content) {
				t.Fatalf("JSON lost original content: %q", got)
			}
			path := filepath.Join(t.TempDir(), "overflow.txt")
			assertSafeCLIText(t, invoke("--max-chars=1", "--out="+path))
			spilled, err := os.ReadFile(path)
			if err != nil || string(spilled) != text {
				t.Fatalf("overflow differs from safe inline text: error %v", err)
			}
		})
	}
}

func TestCLITextEdgesEscapeTerminalControls(t *testing.T) {
	t.Run("errors", func(t *testing.T) {
		bin := buildBinary(t)
		missing := filepath.Join(t.TempDir(), "missing\x1b[2J\u009b31m")
		for _, tc := range []struct {
			arg  string
			code int
		}{{"--bad\x1b]52;c;payload\a", 2}, {missing, 1}} {
			out, stderr, code := runBinary(t, bin, tc.arg)
			if code != tc.code || out != "" {
				t.Fatalf("exit %d stdout %q; want %d and no stdout", code, out, tc.code)
			}
			assertSafeCLIText(t, stderr)
			if !strings.Contains(stderr, `\x1b`) {
				t.Fatalf("error lost the escaped control: %q", stderr)
			}
			_, jsonErr, code := runBinary(t, bin, "--json", tc.arg)
			var decoded map[string]string
			if err := json.Unmarshal([]byte(jsonErr), &decoded); err != nil || code != tc.code {
				t.Fatalf("invalid JSON error: %v exit %d stderr %q", err, code, jsonErr)
			}
			if !strings.Contains(decoded["error"], "\x1b") {
				t.Fatalf("JSON error lost original terminal-control semantics: %q", jsonErr)
			}
		}
	})
	t.Run("overflow-notice", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows rejects control bytes in filenames")
		}
		path := filepath.Join(t.TempDir(), "out\x1b]52;c;payload\a\u009b31m.txt")
		for _, jsonMode := range []bool{false, true} {
			var out bytes.Buffer
			err := writeSizeGuarded(&out, 1, jsonMode, path, func(buf io.Writer) error {
				io.WriteString(buf, "full output")
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(path)
			if err != nil || string(content) != "full output" {
				t.Fatalf("overflow contents = %q, error = %v", content, err)
			}
			if jsonMode {
				var notice outputOverflow
				if err := json.Unmarshal(out.Bytes(), &notice); err != nil || notice.OutputFile != path {
					t.Fatalf("JSON path semantics changed: %q, error = %v", out.String(), err)
				}
			} else {
				assertSafeCLIText(t, out.String())
				if !strings.Contains(out.String(), `out\x1b]52;c;payload\x07\x9b31m.txt`) {
					t.Fatalf("missing escaped notice path: %q", out.String())
				}
			}
		}
	})
}
