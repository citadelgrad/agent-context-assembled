package compile

import (
	"fmt"
	"strings"
	"testing"

	"github.com/citadelgrad/agent-context-assembled/internal/scan"
	"github.com/citadelgrad/agent-context-assembled/internal/tools"
)

func FuzzFrontmatterAndRuleConditionsNeverPanic(f *testing.F) {
	for _, seed := range []struct {
		content string
		path    string
	}{
		{"", "rule.mdc"},
		{"---\napplyTo: \"**/*.go\"\n---\nbody", "x.instructions.md"},
		{"---\r\nTRIGGER: glob\r\nglobs: '*.md'\r\n---\r\nbody", ".windsurf/rules/x.md"},
		{"---\nalwaysApply: false\ndescription: e\u0301界🙂\n---\nbody", ".cursor/rules/x.mdc"},
		{"---\nkey: first:second\nkey: duplicate\n", "AGENTS.md"},
	} {
		f.Add(seed.content, seed.path)
	}

	f.Fuzz(func(t *testing.T, content, path string) {
		if len(content) > 64*1024 || len(path) > 1024 {
			t.Skip()
		}
		for _, key := range []string{"applyTo", "trigger", "globs", "alwaysApply", "description", "missing"} {
			frontmatterField(content, key)
		}
		for _, tool := range []tools.Tool{
			{Slug: "github-copilot"},
			{Slug: "cursor"},
			{Slug: "windsurf"},
		} {
			file := scan.MatchedFile{Path: path, Content: content}
			got := conditionFor(tool, file)
			if again := conditionFor(tool, file); again != got {
				t.Fatalf("conditionFor is nondeterministic: %q then %q", got, again)
			}
		}
		if got := conditionFor(tools.Tool{Slug: "github-copilot"}, scan.MatchedFile{Path: path + ".txt", Content: content}); got != "" {
			t.Fatalf("non-instructions Copilot path produced condition %q", got)
		}
		if got := conditionFor(tools.Tool{Slug: "cursor"}, scan.MatchedFile{Path: path + ".txt", Content: content}); got != "" {
			t.Fatalf("non-mdc Cursor path produced condition %q", got)
		}
		if got := cursorRuleCondition(content); got != cursorRuleCondition(content) {
			t.Fatal("cursorRuleCondition is nondeterministic")
		}
	})
}

func FuzzFrontmatterFieldMetamorphic(f *testing.F) {
	f.Add("applyTo", "**/*.go", "unrelated", "value", false)
	f.Add("trigger", "glob:with:colons", "description", "🙂界", true)
	f.Add("description", "e\u0301", "globs", "*.md", false)

	f.Fuzz(func(t *testing.T, key, value, otherKey, otherValue string, crlf bool) {
		if !safeFrontmatterAtom(key) || !safeFrontmatterValue(value) ||
			!safeFrontmatterAtom(otherKey) || !safeFrontmatterValue(otherValue) || strings.EqualFold(key, otherKey) {
			t.Skip()
		}
		newline := "\n"
		if crlf {
			newline = "\r\n"
		}
		wrapped := value
		if !strings.ContainsAny(value, "\"'") {
			wrapped = `"` + value + `"`
		}
		content := strings.Join([]string{"---", "  " + strings.ToUpper(key) + "  : " + wrapped, "---", "body"}, newline)
		got, ok := frontmatterField(content, key)
		if !ok || got != value {
			t.Fatalf("frontmatterField(%q, %q) = %q,%v; want %q,true", content, key, got, ok, value)
		}
		withUnrelated := strings.Join([]string{"---", fmt.Sprintf("%s: %s", otherKey, otherValue), key + ": " + wrapped, "---", "body"}, newline)
		if got, ok := frontmatterField(withUnrelated, key); !ok || got != value {
			t.Fatalf("unrelated field changed result: %q,%v; want %q,true", got, ok, value)
		}
		afterClose := content + newline + key + ": changed"
		if got, ok := frontmatterField(afterClose, key); !ok || got != value {
			t.Fatalf("field after closing delimiter changed result: %q,%v; want %q,true", got, ok, value)
		}
		if got, ok := frontmatterField("prefix"+newline+content, key); ok || got != "" {
			t.Fatalf("non-leading delimiter returned %q,%v", got, ok)
		}
	})
}

func safeFrontmatterAtom(value string) bool {
	if value == "" || len(value) > 64 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func safeFrontmatterValue(value string) bool {
	return len(value) <= 256 && !strings.ContainsAny(value, "\"'\r\n\x00") && strings.TrimSpace(value) == value
}
