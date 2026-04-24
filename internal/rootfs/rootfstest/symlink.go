// Package rootfstest holds the shared symlink-escape fixture helper
// used by every os.OpenRoot regression test in the tree. Keeping the
// helper in its own package (rather than an exported func on rootfs
// proper) preserves the "no test-only deps in production packages"
// rule — rootfs itself stays a plain `os` shim.
package rootfstest

import (
	"os"
	"path/filepath"
	"testing"
)

// WriteSymlinkEscape writes a file with the given contents into a temp
// directory, then creates a second temp directory with a symlink
// pointing at the external file. Returns the path of the symlink, which
// a rootfs.Open-based loader MUST refuse to follow.
//
// If the platform doesn't support symlinks (Windows non-admin), the
// test is skipped rather than failed — every caller gets the same
// behavior for free.
//
// Pair each new rootfs.Open() / rootfs.OpenFile() call site with a
// regression test using this helper. One line from the caller side:
//
//	link := rootfstest.WriteSymlinkEscape(t, []byte("payload"))
//	if _, err := myLoader(link); err == nil {
//		t.Fatal("expected symlink-escape rejection, got nil error")
//	}
func WriteSymlinkEscape(t *testing.T, contents []byte) string {
	t.Helper()

	externalDir := t.TempDir()
	external := filepath.Join(externalDir, "target")

	if err := os.WriteFile(external, contents, 0o600); err != nil {
		t.Fatalf("write external target: %v", err)
	}

	rootDir := t.TempDir()
	link := filepath.Join(rootDir, "link")

	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	return link
}
