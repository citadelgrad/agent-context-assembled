//go:build !unix && !windows

package fileio

import (
	"errors"
	"os"
)

// OpenRegular fails closed on platforms without a supported implementation.
func OpenRegular(path string) (*os.File, error) {
	return nil, &os.PathError{Op: "open", Path: path, Err: errors.ErrUnsupported}
}

// OpenRegularAt fails closed on platforms without a supported implementation.
func OpenRegularAt(root *os.Root, name string) (*os.File, error) {
	return nil, &os.PathError{Op: "open", Path: name, Err: errors.ErrUnsupported}
}
