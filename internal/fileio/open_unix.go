//go:build unix

package fileio

import (
	"os"
	"syscall"
)

// OpenRegular opens path read-only, following ordinary symbolic links, and
// rejects nonregular descriptors. The caller must close the returned file.
// See the package documentation for the scope of the nonblocking guarantee.
func OpenRegular(path string) (*os.File, error) {
	// POSIX open: O_RDONLY|O_NONBLOCK does not wait for a FIFO writer.
	// O_NOFOLLOW would unnecessarily reject regular-file symbolic links.
	// https://pubs.opengroup.org/onlinepubs/9799919799/functions/open.html
	return regular(os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0))
}

// OpenRegularAt opens name read-only within root, following relative symlinks
// that remain in root, and rejects nonregular descriptors. The caller must
// close the returned file. Root confinement is supplied by os.Root, not by
// joining strings or performing a separate path-based preflight.
func OpenRegularAt(root *os.Root, name string) (*os.File, error) {
	// Root.OpenFile forwards O_NONBLOCK to openat and performs its own
	// confined symlink traversal; do not replace it with os.OpenFile.
	// https://pkg.go.dev/os#Root.OpenFile
	// https://go.dev/src/os/root_unix.go
	return regular(root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0))
}
