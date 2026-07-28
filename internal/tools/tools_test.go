package tools

import (
	"strings"
	"testing"
)

// TestRegistryNoDuplicateNamesOrSlugs ensures every tool in the registry has a
// unique Name and Slug -- duplicates would silently corrupt any lookup by
// either key and indicate a copy-paste error in the static spec table.
func TestRegistryNoDuplicateNamesOrSlugs(t *testing.T) {
	seenNames := map[string]bool{}
	seenSlugs := map[string]bool{}
	for _, tool := range Registry {
		if seenNames[tool.Name] {
			t.Errorf("duplicate tool Name %q in Registry", tool.Name)
		}
		seenNames[tool.Name] = true

		if seenSlugs[tool.Slug] {
			t.Errorf("duplicate tool Slug %q in Registry", tool.Slug)
		}
		seenSlugs[tool.Slug] = true
	}
}

// TestRegistryRequiredFields checks that every tool has the minimum fields a
// consumer (scan/compile/render) would need populated: non-empty Name/Slug,
// at least one LocalFile pattern, a valid Scope/Downward enum value, and a
// non-empty PrecedenceNote (since render.go always prints it).
func TestRegistryRequiredFields(t *testing.T) {
	for _, tool := range Registry {
		t.Run(tool.Slug, func(t *testing.T) {
			if strings.TrimSpace(tool.Name) == "" {
				t.Error("Name is empty")
			}
			if strings.TrimSpace(tool.Slug) == "" {
				t.Error("Slug is empty")
			}
			if len(tool.LocalFiles) == 0 {
				t.Error("LocalFiles is empty; every tool should check at least one local pattern")
			}
			for i, lf := range tool.LocalFiles {
				if strings.TrimSpace(lf.Pattern) == "" {
					t.Errorf("LocalFiles[%d].Pattern is empty", i)
				}
			}
			if !isValidScopeMode(tool.Scope) {
				t.Errorf("Scope %v is not a valid ScopeMode enum value", tool.Scope)
			}
			if !isValidDownwardMode(tool.Downward) {
				t.Errorf("Downward %v is not a valid DownwardMode enum value", tool.Downward)
			}
			if strings.TrimSpace(tool.PrecedenceNote) == "" {
				t.Error("PrecedenceNote is empty; render.go always prints this")
			}
			for i, gc := range tool.GlobalConfigs {
				if gc.PathFromHome == "" && gc.Absolute == "" {
					t.Errorf("GlobalConfigs[%d] has neither PathFromHome nor Absolute set", i)
				}
				if gc.PathFromHome != "" && gc.Absolute != "" {
					t.Errorf("GlobalConfigs[%d] has both PathFromHome and Absolute set (should be mutually exclusive)", i)
				}
			}
		})
	}
}

// TestRegistryNonEmpty is a basic sanity check that the registry was actually
// populated -- a regression here (e.g. an accidental empty slice) would make
// every other package silently do nothing.
func TestRegistryNonEmpty(t *testing.T) {
	if len(Registry) == 0 {
		t.Fatal("Registry is empty")
	}
	// The project documents exactly 10 surveyed tools (see docs/research.md);
	// pin the count so an accidental deletion/duplication is caught.
	const wantCount = 10
	if len(Registry) != wantCount {
		t.Errorf("Registry has %d tools, want %d (per docs/research.md's 10 surveyed tools)", len(Registry), wantCount)
	}
}

// TestGlobalConfigPathsNonEmptyWhereDeclared verifies that every tool
// declaring a GlobalConfigs entry gives it a usable path source, and that
// tools known (per docs/research.md) to have a documented global config path
// actually declare at least one GlobalConfigs entry.
func TestGlobalConfigPathsNonEmptyWhereDeclared(t *testing.T) {
	// Per docs/research.md, GitHub Copilot and Cursor have no file-based
	// global config (personal/user-level settings live in app/website
	// settings UI, not a plain file this prototype can scan) -- so they're
	// the tools expected to have zero GlobalConfigs. Every other tool has a
	// documented global file/dir.
	expectNoGlobalConfig := map[string]bool{
		"github-copilot": true,
		"cursor":         true,
	}

	for _, tool := range Registry {
		hasGlobal := len(tool.GlobalConfigs) > 0
		if expectNoGlobalConfig[tool.Slug] {
			if hasGlobal {
				t.Errorf("%s: expected no GlobalConfigs, got %d", tool.Slug, len(tool.GlobalConfigs))
			}
			continue
		}
		if !hasGlobal {
			t.Errorf("%s: expected at least one GlobalConfigs entry (per docs/research.md), got none", tool.Slug)
		}
	}
}

// TestKnownToolsPresent locks in that specific, expected tools are present in
// the registry by slug, guarding against an accidental rename or removal.
func TestKnownToolsPresent(t *testing.T) {
	want := []string{
		"claude-code",
		"codex-cli",
		"github-copilot",
		"opencode",
		"cursor",
		"windsurf",
		"cline",
		"gemini-cli",
		"aider",
		"hermes",
	}
	bySlug := map[string]Tool{}
	for _, tool := range Registry {
		bySlug[tool.Slug] = tool
	}
	for _, slug := range want {
		if _, ok := bySlug[slug]; !ok {
			t.Errorf("expected tool with slug %q to be present in Registry", slug)
		}
	}
}

// TestScopeModeConstants pins the expected relative ordering/values of the
// ScopeMode enum so a reordering doesn't silently change every tool's scope
// semantics without a compile error.
func TestScopeModeConstants(t *testing.T) {
	if ScopeFilesystemRoot != 0 {
		t.Errorf("ScopeFilesystemRoot = %d, want 0 (iota base)", ScopeFilesystemRoot)
	}
	if ScopeGitRoot == ScopeFilesystemRoot {
		t.Error("ScopeGitRoot must differ from ScopeFilesystemRoot")
	}
	if ScopeTargetOnly == ScopeFilesystemRoot || ScopeTargetOnly == ScopeGitRoot {
		t.Error("ScopeTargetOnly must differ from the other ScopeMode values")
	}
}

// TestDownwardModeConstants is the DownwardMode analog of
// TestScopeModeConstants.
func TestDownwardModeConstants(t *testing.T) {
	if DownwardNone != 0 {
		t.Errorf("DownwardNone = %d, want 0 (iota base)", DownwardNone)
	}
	if DownwardLazy == DownwardNone {
		t.Error("DownwardLazy must differ from DownwardNone")
	}
	if DownwardEager == DownwardNone || DownwardEager == DownwardLazy {
		t.Error("DownwardEager must differ from the other DownwardMode values")
	}
}

func isValidScopeMode(s ScopeMode) bool {
	switch s {
	case ScopeFilesystemRoot, ScopeGitRoot, ScopeTargetOnly:
		return true
	default:
		return false
	}
}

func isValidDownwardMode(d DownwardMode) bool {
	switch d {
	case DownwardNone, DownwardLazy, DownwardEager:
		return true
	default:
		return false
	}
}
