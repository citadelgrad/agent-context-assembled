// Package budget applies actx input policies, not upstream tool limits.
package budget

import (
	"fmt"
	"io"
	"os"
	"sort"
)

// Limits applies to one invocation, shared across all selected tools.
// Positive values override Defaults; zero or negative values use Defaults.
// Files counts content reads (including empty files); Directories counts
// listing attempts (including absent paths); Entries counts all acquired
// entries, including noise and repeat listings. Matches counts candidate
// appearances, including metadata-only artifacts and repeated patterns.
// Byte/entry limits allow one extra sentinel to distinguish exact limits.
// These are work/allocation bounds, not deadlines for filesystem calls.
type Limits struct {
	FileBytes, TotalBytes                int64
	Files, Directories, Entries, Matches int
}

// Defaults is the finite actx policy, not any upstream tool's limit.
func Defaults() Limits {
	const (
		fileBytes   = 16 << 20
		totalBytes  = 64 << 20
		files       = 1024
		directories = 2048
		entries     = 32768
		matches     = 4096
	)
	return Limits{FileBytes: fileBytes, TotalBytes: totalBytes, Files: files, Directories: directories, Entries: entries, Matches: matches}
}

type Budget struct {
	limits                      Limits
	bytes                       int64
	entries                     int
	files, directories, matches int
	err                         error
}

func New(l Limits) *Budget {
	d := Defaults()
	if l.FileBytes > 0 {
		d.FileBytes = l.FileBytes
	}
	if l.TotalBytes > 0 {
		d.TotalBytes = l.TotalBytes
	}
	if l.Files > 0 {
		d.Files = l.Files
	}
	if l.Directories > 0 {
		d.Directories = l.Directories
	}
	if l.Entries > 0 {
		d.Entries = l.Entries
	}
	if l.Matches > 0 {
		d.Matches = l.Matches
	}
	return &Budget{limits: d}
}
func (b *Budget) Err() error { return b.err }

// Match charges a retained candidate, including metadata-only artifacts.
func (b *Budget) Match(path string) bool {
	if b.err != nil {
		return false
	}
	if b.matches >= b.limits.Matches {
		b.Exceed("matches", path, int64(b.limits.Matches))
		return false
	}
	b.matches++
	return true
}

// ReadDir acquires at most the remaining entry allowance plus one probe
// entry, in batches of at most 128. It sorts only a complete directory; an
// exhausted directory never exposes a filesystem-order-dependent prefix.
func (b *Budget) ReadDir(path string) ([]os.DirEntry, error) {
	if b.err != nil {
		return nil, b.err
	}
	if b.directories >= b.limits.Directories {
		return nil, b.Exceed("directories", path, int64(b.limits.Directories))
	}
	b.directories++
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var entries []os.DirEntry
	for {
		remaining := b.limits.Entries - b.entries
		n := 128
		if remaining < n {
			n = remaining + 1
		}
		batch, err := f.ReadDir(n)
		b.entries += len(batch)
		if len(batch) > remaining {
			return nil, b.Exceed("directory entries", path, int64(b.limits.Entries))
		}
		entries = append(entries, batch...)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// Exceed records the first exhausted policy. Once exhausted, no further input
// should be acquired. The diagnostic never includes file content.
func (b *Budget) Exceed(resource, path string, limit int64) error {
	if b.err == nil {
		b.err = fmt.Errorf("actx input budget exceeded: %s limit %d at %q; narrow the target/tool selection or reduce the input set", resource, limit, path)
	}
	return b.err
}

// Read bounds actual bytes, including files that grow after opening. One extra
// byte distinguishes exact-limit input from truncation. Exhaustion discards the
// partial content and is sticky across selected tools in this invocation.
func (b *Budget) Read(r io.Reader, path string) ([]byte, error) {
	if b.err != nil {
		return nil, b.err
	}
	if b.files >= b.limits.Files {
		return nil, b.Exceed("files", path, int64(b.limits.Files))
	}
	b.files++
	remaining := b.limits.TotalBytes - b.bytes
	limit := min(b.limits.FileBytes, remaining)
	// Avoid overflow for a trusted caller supplying an unusually large policy.
	if limit == int64(^uint64(0)>>1) {
		limit--
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	b.bytes += int64(len(data))
	if int64(len(data)) > limit {
		if remaining < b.limits.FileBytes {
			return nil, b.Exceed("aggregate bytes", path, b.limits.TotalBytes)
		}
		return nil, b.Exceed("file bytes", path, b.limits.FileBytes)
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}
