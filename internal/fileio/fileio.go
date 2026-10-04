// Package fileio opens read-only regular files and validates the opened
// descriptor, rather than trusting a path-based stat performed before opening.
// Callers own returned files and must close them.
//
// On Unix, opens use O_NONBLOCK so a concurrent replacement with a FIFO cannot
// wait for a writer. This is not an I/O deadline: path lookup, filesystem
// metadata, regular-file reads, and arbitrary device drivers may still block.
// On Windows, descriptor validation still precedes reads, but opening uses
// ordinary blocking os.Open/Root.Open; there is no nonblocking-open guarantee.
// Other non-Unix platforms fail closed with errors.ErrUnsupported.
package fileio

import (
	"errors"
	"os"
)

var errNotRegular = errors.New("not a regular file")

// regular validates the descriptor that will be read, closing it on failure.
func regular(f *os.File, err error) (*os.File, error) {
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, &os.PathError{Op: "open", Path: f.Name(), Err: errNotRegular}
	}
	return f, nil
}
