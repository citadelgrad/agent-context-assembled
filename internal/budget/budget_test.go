package budget

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type measuredReader struct {
	r     io.Reader
	bytes int
}

func (r *measuredReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.bytes += n
	return n, err
}

func TestReadAcquiresOnlyAllowanceAndOneProbe(t *testing.T) {
	r := &measuredReader{r: strings.NewReader("abcdefghijkl")}
	b := New(Limits{FileBytes: 3})
	data, err := b.Read(r, "synthetic")
	if err == nil || len(data) != 0 || r.bytes != 4 {
		t.Fatalf("read beyond limit+1 or exposed prefix: %d bytes, data=%q err=%v", r.bytes, data, err)
	}
	_, again := b.Read(r, "must not read")
	if again != err || r.bytes != 4 {
		t.Fatalf("sticky exhaustion performed another read: bytes=%d err=%v", r.bytes, again)
	}
}

func TestDirectoryBudgetStopsAcrossBatches(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 130; i++ {
		if err := os.WriteFile(filepath.Join(root, strings.Repeat("a", i+1)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := New(Limits{Entries: 129}).ReadDir(root)
	if err == nil || len(entries) != 0 {
		t.Fatalf("second batch must not yield a successful prefix: %d %v", len(entries), err)
	}
	entries, err = New(Limits{Entries: 130}).ReadDir(root)
	if err != nil || len(entries) != 130 {
		t.Fatalf("exact multi-batch control: %d %v", len(entries), err)
	}
}

func TestCountBudgetsIncludeEmptyInputsAndStop(t *testing.T) {
	root := t.TempDir()
	t.Run("directories", func(t *testing.T) {
		b := New(Limits{Directories: 1})
		if _, err := b.ReadDir(root); err != nil {
			t.Fatal(err)
		}
		if _, err := b.ReadDir(root); err == nil {
			t.Fatal("directory limit ignored")
		}
	})
	t.Run("files", func(t *testing.T) {
		b := New(Limits{Files: 1})
		if _, err := b.Read(strings.NewReader(""), "empty"); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Read(strings.NewReader(""), "empty2"); err == nil {
			t.Fatal("file limit ignored")
		}
	})
	t.Run("matches", func(t *testing.T) {
		b := New(Limits{Matches: 1})
		if !b.Match("a") || b.Match("b") || b.Err() == nil {
			t.Fatal("match limit ignored")
		}
		if _, err := b.ReadDir(root); err == nil {
			t.Fatal("exhaustion did not stop subsequent input")
		}
	})
}

func TestReadDirCapsAcquisitionAndSharesEntries(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"z", "a", "m"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b := New(Limits{Entries: 3})
	entries, err := b.ReadDir(root)
	if err != nil || len(entries) != 3 || entries[0].Name() != "a" || entries[2].Name() != "z" {
		t.Fatalf("exact boundary must be sorted: %v %v", entries, err)
	}
	if entries, err := b.ReadDir(root); err == nil || len(entries) != 0 || !strings.Contains(err.Error(), "directory entries") {
		t.Fatalf("second directory must share budget, not return prefix: %d %v", len(entries), err)
	}
	b = New(Limits{Entries: 2})
	if entries, err := b.ReadDir(root); err == nil || len(entries) != 0 {
		t.Fatalf("wide directory returned partial success: %d %v", len(entries), err)
	}
}

func TestReadRejectsOversizedInputAndSharesAggregate(t *testing.T) {
	b := New(Limits{FileBytes: 3, TotalBytes: 5})
	if got, err := b.Read(strings.NewReader("abc"), "first"); err != nil || string(got) != "abc" {
		t.Fatalf("exact file boundary: %q %v", got, err)
	}
	if got, err := b.Read(strings.NewReader("de"), "second"); err != nil || string(got) != "de" {
		t.Fatalf("exact aggregate boundary: %q %v", got, err)
	}
	if got, err := b.Read(strings.NewReader("f"), "third"); err == nil || len(got) != 0 || !strings.Contains(err.Error(), "aggregate bytes") {
		t.Fatalf("aggregate must fail closed: %q %v", got, err)
	}
	b = New(Limits{FileBytes: 3})
	if got, err := b.Read(strings.NewReader("abcd"), "large"); err == nil || len(got) != 0 || !strings.Contains(err.Error(), "file bytes") {
		t.Fatalf("oversize must fail closed: %q %v", got, err)
	}
}
