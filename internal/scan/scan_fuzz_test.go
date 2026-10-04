package scan

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func FuzzExpandCandidateFilesystemInvariants(f *testing.F) {
	f.Add("a", "md", false)
	f.Add("nested", "instructions.md", true)
	f.Add("hidden", "txt", false)
	f.Fuzz(func(t *testing.T, segment, suffix string, doublestar bool) {
		if !safePathSegment(segment) || !safePathSegment(suffix) {
			t.Skip()
		}
		root := t.TempDir()
		paths := []string{
			filepath.Join(root, segment+"."+suffix),
			filepath.Join(root, "one", segment+"."+suffix),
			filepath.Join(root, "one", "two", segment+"."+suffix),
		}
		for _, path := range paths {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(filepath.Join(root, ".hidden"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".hidden", segment+"."+suffix), []byte("hidden"), 0o644); err != nil {
			t.Fatal(err)
		}
		pattern := segment + "." + suffix
		want := paths[:1]
		if doublestar {
			pattern = filepath.Join("**", segment+"."+suffix)
			want = paths
		}
		got := expandCandidate(root, pattern)
		gotAgain := expandCandidate(root, pattern)
		if !reflect.DeepEqual(got, gotAgain) {
			t.Fatal("expansion is nondeterministic")
		}
		if !sort.StringsAreSorted(got) {
			t.Fatalf("results not sorted: %v", got)
		}
		seen := map[string]bool{}
		for _, path := range got {
			if seen[path] {
				t.Fatalf("duplicate result %q", path)
			}
			seen[path] = true
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("non-regular/missing result %q: %v", path, err)
			}
			rel, err := filepath.Rel(root, path)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
				t.Fatalf("result escaped root: %q", path)
			}
			if filepath.Base(path) != segment+"."+suffix {
				t.Fatalf("result does not match suffix: %q", path)
			}
			if strings.Contains(rel, string(filepath.Separator)+".hidden"+string(filepath.Separator)) {
				t.Fatalf("hidden dir result returned: %q", path)
			}
		}
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		missing := expandCandidate(root, "missing-"+segment)
		if len(missing) != 0 {
			t.Fatalf("missing plain path returned %v", missing)
		}
	})
}

func FuzzExpandCandidateArbitraryBoundedPatternNeverPanics(f *testing.F) {
	for _, seed := range []string{"", "*", "**", "[", "{a,b}", "🙂?.md"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, pattern string) {
		if len(pattern) > 256 || strings.ContainsAny(pattern, "/\\\x00") || filepath.VolumeName(pattern) != "" {
			t.Skip()
		}
		root := t.TempDir()
		for _, match := range expandCandidate(root, pattern) {
			rel, err := filepath.Rel(root, match)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Fatalf("pattern %q escaped root via %q", pattern, match)
			}
		}
	})
}

func safePathSegment(value string) bool {
	if value == "" || len(value) > 32 || value == "." || value == ".." || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}
