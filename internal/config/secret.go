// Package config holds runtime configuration loaders for statnive-live.
// The master-secret loader is the single source of truth for the HMAC key
// used by every salt derivation across every tenant — losing it (or
// rotating it without warning) invalidates every visitor_hash already in
// ClickHouse, so the loader is intentionally strict and fail-closed.
package config

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MinSecretLen is 32 bytes (256 bits) — HMAC-SHA256's natural block size
// and the floor below which the daily salt would carry insufficient entropy
// to satisfy Privacy Rule 2.
const MinSecretLen = 32

// secretGroupOtherMask is the bit pattern for "any group or world bit set".
// AND'd against the file-mode perm bits, a non-zero result means the file
// is more permissive than 0600 and we refuse to read it.
const secretGroupOtherMask os.FileMode = 0o077

// ErrNoMasterSecret signals that neither the environment variable nor the
// configured file produced a usable master secret. Callers MUST fail-closed
// — never boot the binary without a real secret.
var ErrNoMasterSecret = errors.New(
	"master secret not provided: set $STATNIVE_MASTER_SECRET (64 hex chars or >=32 raw bytes) " +
		"or place a chmod 0600 file at config/master.key",
)

// LoadMasterSecret resolves the master HMAC key, env-first.
//
// Order:
//  1. envVar — if set and non-empty:
//     - 64 hex chars → decoded to 32 bytes
//     - otherwise raw bytes, must be >= MinSecretLen
//  2. filePath — if exists, must be a regular file, mode bits <= 0600,
//     contents (after trimming trailing newlines) >= MinSecretLen.
//  3. neither — returns ErrNoMasterSecret.
//
// The returned slice is a fresh allocation; callers may mutate it without
// affecting future loads, and our copy is invisible to the caller.
func LoadMasterSecret(envVar, filePath string) ([]byte, error) {
	if v := os.Getenv(envVar); v != "" {
		return parseEnvSecret(envVar, v)
	}

	return readFileSecret(filePath)
}

func parseEnvSecret(envVar, raw string) ([]byte, error) {
	if len(raw) == 2*MinSecretLen {
		if decoded, err := hex.DecodeString(raw); err == nil {
			return decoded, nil
		}
	}

	if len(raw) < MinSecretLen {
		return nil, fmt.Errorf("$%s: secret too short (got %d bytes, need >=%d)", envVar, len(raw), MinSecretLen)
	}

	out := make([]byte, len(raw))
	copy(out, raw)

	return out, nil
}

func readFileSecret(path string) ([]byte, error) {
	if path == "" {
		return nil, ErrNoMasterSecret
	}

	// os.OpenRoot + root.Open resolves `name` relative to a directory FD
	// via openat2 (Linux) / OBJECT_ATTRIBUTES (Windows), so a symlink
	// pointing outside the root directory fails to open — TOCTOU-safe vs
	// a bare os.Open(path). Go 1.24+.
	dir, name := filepath.Split(path)
	if dir == "" {
		dir = "."
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoMasterSecret
		}

		return nil, fmt.Errorf("open master key dir: %w", err)
	}

	defer func() { _ = root.Close() }()

	f, err := root.Open(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoMasterSecret
		}

		return nil, fmt.Errorf("open master key: %w", err)
	}

	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat master key: %w", err)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", path)
	}

	if perm := info.Mode().Perm(); perm&secretGroupOtherMask != 0 {
		return nil, fmt.Errorf("%s: permissions %#o too permissive (must be 0600)", path, perm)
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read master key: %w", err)
	}

	data = bytes.TrimRight(data, "\r\n")

	if len(data) < MinSecretLen {
		return nil, fmt.Errorf("%s: secret too short (got %d bytes, need >=%d)", path, len(data), MinSecretLen)
	}

	out := make([]byte, len(data))
	copy(out, data)

	return out, nil
}
