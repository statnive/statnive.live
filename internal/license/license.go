// Package license verifies offline Ed25519-signed license tokens at
// startup. Air-gap safe: zero network calls.
//
// Crypto path is stdlib `crypto/ed25519`; file load goes through
// `internal/rootfs` for the same symlink-escape guard the master
// secret loader uses (`internal/config/secret.go:79`).
//
// Token format is RFC 8037 EdDSA JWT — three base64url segments:
//
//	base64url(header) . base64url(claims) . base64url(signature)
//
// Header is fixed at {"alg":"EdDSA","typ":"JWT"}.
// Signature is Ed25519 over the literal bytes of `header.b64 + "." + claims.b64`.
//
// `golang.org/x/crypto/ssh` would also work via ssh-keygen -Y, but the
// JWT path keeps everything in stdlib and lets the v1.1 signing CLI be
// a 30-line `go run` script.
//
// Verification is opt-in: when path is empty, Verify returns ErrNoLicense
// and main.go treats it as a no-op so deploys without a license file
// (statnive.com / statnive.de / fr.statnive.com on Netcup) boot
// byte-identically to the pre-WP1 binary.
package license

import (
	"bytes"
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/statnive/statnive.live/internal/rootfs"
)

//go:embed signing.pub
var embeddedPubKey []byte

// maxLicenseFileBytes caps the file read so a malicious symlink to a
// large file can't OOM the binary at startup. Tokens are ~500 bytes;
// 8 KiB is the same defensive ceiling CLAUDE.md Security §4 picks for
// the ingest MaxBytesReader. Also reused by cmd/statnive-license for
// the same defense on the signer-side priv-PEM read.
const maxLicenseFileBytes = 8 << 10

// JWTHeader is the canonical RFC 8037 EdDSA JWT header. Both the
// verifier (this package) and the signer (cmd/statnive-license) read
// from this constant so the byte-for-byte format cannot drift.
const JWTHeader = `{"alg":"EdDSA","typ":"JWT"}`

// Claims is the verified license body.
type Claims struct {
	Customer string `json:"sub"`
	// SiteID pins the license to one statnive.sites tenant. 0 = wildcard.
	SiteID       uint32   `json:"sid"`
	MaxEventsDay uint64   `json:"max"`
	Features     []string `json:"feat"`
	IssuedAt     int64    `json:"iat"`
	// ExpiresAt is unix seconds. exp <= 0 means no expiry.
	ExpiresAt int64 `json:"exp"`
}

// ErrNoLicense signals "license verification not configured" rather
// than failure. main.go logs this at INFO and continues — preserves
// the pre-WP1 boot path for deploys that never had a license file.
var ErrNoLicense = errors.New("license: no path configured")

// ErrPlaceholderPubKey fires when the binary still ships the dev
// placeholder Ed25519 public key (32 zero bytes) but the operator
// configured a license file. Refusing to verify stops a release-
// engineering mistake from being papered over by a crafted token.
// Rebuild with the real deploy/keys/license-signing.pub before
// shipping a customer license.
var ErrPlaceholderPubKey = errors.New("license: binary built with placeholder public key")

// ErrExpired is returned when exp is in the past.
var ErrExpired = errors.New("license: expired")

// ErrInvalidSignature is returned when Ed25519 verification fails.
var ErrInvalidSignature = errors.New("license: invalid signature")

// ErrMalformed is returned when the token shape is wrong.
var ErrMalformed = errors.New("license: malformed token")

// Verify reads the license file at path, validates the Ed25519
// signature against the binary-embedded public key, and asserts the
// exp claim is in the future. Returns parsed Claims on success.
//
// Empty path returns ErrNoLicense and is the opt-in escape hatch for
// deploys that have never had a license file.
func Verify(path string) (*Claims, error) {
	if strings.TrimSpace(path) == "" {
		return nil, ErrNoLicense
	}

	if isPlaceholderKey(embeddedPubKey) {
		return nil, ErrPlaceholderPubKey
	}

	if len(embeddedPubKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("license: embedded pubkey is %d bytes, expected %d",
			len(embeddedPubKey), ed25519.PublicKeySize)
	}

	return verifyFile(path, ed25519.PublicKey(embeddedPubKey), time.Now())
}

// verifyFile reads a license token from path through the rootfs
// symlink guard and runs verifyToken against the supplied pubKey.
// Exported only via Verify in production; tests reach it directly to
// exercise the file-IO branch with a real (non-placeholder) key.
func verifyFile(path string, pubKey ed25519.PublicKey, now time.Time) (*Claims, error) {
	f, err := rootfs.Open(path)
	if err != nil {
		return nil, fmt.Errorf("license: open %s: %w", path, err)
	}

	defer func() { _ = f.Close() }()

	raw, err := io.ReadAll(io.LimitReader(f, maxLicenseFileBytes))
	if err != nil {
		return nil, fmt.Errorf("license: read %s: %w", path, err)
	}

	return VerifyToken(strings.TrimSpace(string(raw)), pubKey, now)
}

// Sign produces an RFC 8037 EdDSA JWT bearing the given claims, signed
// by priv. Used by cmd/statnive-license (operator-side issuance) and
// by the package's own tests. Production verify path is Verify ->
// verifyFile -> VerifyToken; this function is the symmetric inverse.
func Sign(priv ed25519.PrivateKey, c Claims) (string, error) {
	if l := len(priv); l != ed25519.PrivateKeySize {
		return "", fmt.Errorf("license: priv key is %d bytes, expected %d", l, ed25519.PrivateKeySize)
	}

	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(JWTHeader))

	claimsBytes, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("license: marshal claims: %w", err)
	}

	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsBytes)
	signingInput := headerB64 + "." + claimsB64
	sig := ed25519.Sign(priv, []byte(signingInput))
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return signingInput + "." + sigB64, nil
}

// VerifyToken parses + validates a token string against pubKey at the
// supplied clock. Exported so the offline signer CLI's roundtrip test
// can verify its own output without reimplementing the crypto path.
// Tests in this package use VerifyToken to inject pubKey + clock
// directly without touching the filesystem.
func VerifyToken(token string, pubKey ed25519.PublicKey, now time.Time) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrMalformed
	}

	if len(pubKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("license: pubkey is %d bytes, expected %d",
			len(pubKey), ed25519.PublicKeySize)
	}

	signingInput := parts[0] + "." + parts[1]

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: signature segment: %w", ErrMalformed, err)
	}

	if !ed25519.Verify(pubKey, []byte(signingInput), sig) {
		return nil, ErrInvalidSignature
	}

	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: claims segment: %w", ErrMalformed, err)
	}

	var c Claims
	if err := json.Unmarshal(claimsBytes, &c); err != nil {
		return nil, fmt.Errorf("%w: claims unmarshal: %w", ErrMalformed, err)
	}

	if c.ExpiresAt > 0 && now.Unix() >= c.ExpiresAt {
		return nil, ErrExpired
	}

	return &c, nil
}

// isPlaceholderKey returns true when the embedded pubkey is all zero
// bytes — the development sentinel that ships in main until a real
// release pubkey is committed.
func isPlaceholderKey(b []byte) bool {
	if len(b) != ed25519.PublicKeySize {
		return false
	}

	var zero [ed25519.PublicKeySize]byte

	return bytes.Equal(b, zero[:])
}
