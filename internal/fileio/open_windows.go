package fileio

import "os"

// OpenRegular opens path read-only and rejects nonregular handles before any
// read. The caller must close the returned file. Unlike the Unix implementation,
// this Windows fallback has no nonblocking-open guarantee: os.Open may enter a
// device driver or remote filesystem before descriptor validation can reject it.
// Do not treat this API as a deadline or safe device-open sandbox on Windows.
// https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew
func OpenRegular(path string) (*os.File, error) {
	return regular(os.Open(path))
}

// OpenRegularAt opens name read-only within root and rejects nonregular handles
// before reading. It retains os.Root's confined relative-symlink traversal and
// Windows reserved-device-name rejection. The caller must close the file.
// There is no nonblocking-open guarantee on Windows.
// https://pkg.go.dev/os#Root
func OpenRegularAt(root *os.Root, name string) (*os.File, error) {
	return regular(root.Open(name))
}
