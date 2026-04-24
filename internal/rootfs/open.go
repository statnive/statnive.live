// Package rootfs centralizes TOCTOU-safe file loaders for the binary's
// startup-path config readers (master secret, bloom filter, TLS PEM, …).
// Every caller uses os.OpenRoot underneath so a symlink in the target
// directory pointing outside the directory fails the open rather than
// silently following. Go 1.24+.
package rootfs

import (
	"os"
	"path/filepath"
)

// Open returns a read-only *os.File opened via os.OpenRoot on its
// parent directory — identical semantics to os.Open, minus symlink
// escape. On all supported OSes (Linux, macOS, Windows) the returned
// FD is independent of the directory handle used to resolve it, so the
// root is closed immediately and the caller only tracks the file.
func Open(path string) (*os.File, error) {
	return open(path, func(root *os.Root, name string) (*os.File, error) {
		return root.Open(name)
	})
}

// OpenFile is the O_CREATE / O_WRONLY / O_TRUNC variant — mirrors
// os.OpenFile. Used by the bloom filter's SaveTo to create the temp
// file atomically within the bloom directory.
func OpenFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	return open(path, func(root *os.Root, name string) (*os.File, error) {
		return root.OpenFile(name, flag, perm)
	})
}

func open(path string, do func(*os.Root, string) (*os.File, error)) (*os.File, error) {
	dir, name := filepath.Split(path)
	if dir == "" {
		dir = "."
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}

	defer func() { _ = root.Close() }()

	return do(root, name)
}
