package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/statnive/statnive.live/internal/config"
)

const testEnvVar = "STATNIVE_TEST_MASTER_SECRET"

func TestLoadMasterSecret_EnvHex(t *testing.T) {
	t.Setenv(testEnvVar, strings.Repeat("ab", 32))

	got, err := config.LoadMasterSecret(testEnvVar, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 32 {
		t.Fatalf("len = %d, want 32 (hex-decoded)", len(got))
	}

	if got[0] != 0xab {
		t.Errorf("first byte = %x, want 0xab", got[0])
	}
}

func TestLoadMasterSecret_EnvRaw(t *testing.T) {
	raw := strings.Repeat("z", 40)
	t.Setenv(testEnvVar, raw)

	got, err := config.LoadMasterSecret(testEnvVar, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(got) != raw {
		t.Errorf("got = %q, want %q", got, raw)
	}
}

func TestLoadMasterSecret_EnvTooShort(t *testing.T) {
	t.Setenv(testEnvVar, "short")

	_, err := config.LoadMasterSecret(testEnvVar, "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "too short") {
		t.Errorf("error = %v, want to contain 'too short'", err)
	}
}

func TestLoadMasterSecret_EnvEmptyFallsThrough(t *testing.T) {
	t.Setenv(testEnvVar, "")

	_, err := config.LoadMasterSecret(testEnvVar, "")
	if !errors.Is(err, config.ErrNoMasterSecret) {
		t.Fatalf("err = %v, want ErrNoMasterSecret", err)
	}
}

func TestLoadMasterSecret_FileMode0600(t *testing.T) {
	t.Parallel()

	path := writeSecretFile(t, strings.Repeat("k", 32), 0o600)

	got, err := config.LoadMasterSecret(testEnvVar, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 32 {
		t.Errorf("len = %d, want 32", len(got))
	}
}

func TestLoadMasterSecret_FileTooPermissive(t *testing.T) {
	t.Parallel()

	path := writeSecretFile(t, strings.Repeat("k", 32), 0o644)

	_, err := config.LoadMasterSecret(testEnvVar, path)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "too permissive") {
		t.Errorf("error = %v, want to contain 'too permissive'", err)
	}
}

func TestLoadMasterSecret_FileTooShort(t *testing.T) {
	t.Parallel()

	path := writeSecretFile(t, "short", 0o600)

	_, err := config.LoadMasterSecret(testEnvVar, path)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "too short") {
		t.Errorf("error = %v, want to contain 'too short'", err)
	}
}

func TestLoadMasterSecret_FileTrailingNewlineStripped(t *testing.T) {
	t.Parallel()

	// `echo "..." > master.key` adds a trailing newline; the loader must
	// strip it so a 32-byte file with one newline (33 bytes on disk) loads.
	path := writeSecretFile(t, strings.Repeat("k", 32)+"\n", 0o600)

	got, err := config.LoadMasterSecret(testEnvVar, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 32 {
		t.Errorf("len = %d, want 32 (newline stripped)", len(got))
	}
}

func TestLoadMasterSecret_FileMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, err := config.LoadMasterSecret(testEnvVar, filepath.Join(dir, "nope.key"))
	if !errors.Is(err, config.ErrNoMasterSecret) {
		t.Fatalf("err = %v, want ErrNoMasterSecret", err)
	}
}

func TestLoadMasterSecret_DefensiveCopy(t *testing.T) {
	t.Setenv(testEnvVar, strings.Repeat("z", 40))

	a, err := config.LoadMasterSecret(testEnvVar, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a[0] = 0xff

	b, err := config.LoadMasterSecret(testEnvVar, "")
	if err != nil {
		t.Fatalf("second load: %v", err)
	}

	if b[0] == 0xff {
		t.Errorf("mutation of returned slice leaked into second call: b[0] = %x", b[0])
	}
}

// TestLoadMasterSecret_SymlinkEscapeRejected is the TOCTOU regression
// for the os.Root migration. A symlink inside the key directory that
// points outside the root must fail to open — a bare os.Open would
// silently follow it and leak a secret from an unexpected location.
func TestLoadMasterSecret_SymlinkEscapeRejected(t *testing.T) {
	t.Parallel()

	// Create the real target outside the root.
	realDir := t.TempDir()
	realKey := filepath.Join(realDir, "evil.key")

	if err := os.WriteFile(realKey, []byte(strings.Repeat("z", 32)), 0o600); err != nil {
		t.Fatalf("write real: %v", err)
	}

	// Create a separate root with a symlink pointing at the real target.
	rootDir := t.TempDir()
	link := filepath.Join(rootDir, "master.key")

	if err := os.Symlink(realKey, link); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	if _, err := config.LoadMasterSecret(testEnvVar, link); err == nil {
		t.Fatal("expected symlink-escape rejection, got nil error")
	}
}

func writeSecretFile(t *testing.T, contents string, mode os.FileMode) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")

	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	return path
}
