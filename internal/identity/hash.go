// Package identity computes per-event visitor_hash, rotates the daily
// IRST salt, and hashes user_id values before they touch ClickHouse.
//
// Privacy contract (CLAUDE.md):
//   - Only BLAKE3 / SHA-256+ in any privacy path. No MD5, no SHA-1.
//   - The master secret enters this package once at boot and is keyed by
//     site_id at every derivation, so per-tenant cryptographic separation
//     is structural, not a layered convention.
//   - Raw user_id is never logged, never written to disk, never echoed
//     back to the wire — only its SHA-256 lands in events_raw.
package identity

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"

	"lukechampine.com/blake3"
)

// VisitorHash returns BLAKE3-128(IP || "|" || UA) keyed by the provided
// per-day salt. The salt is the hex-encoded HMAC-SHA256 string emitted by
// SaltManager (64 hex chars → 32 bytes); decoded keys feed BLAKE3's keyed
// mode directly. If the salt is malformed, raw bytes are used (truncated /
// zero-padded to 32) so the hash function never panics on a stale input.
func VisitorHash(ip, userAgent, salt string) [16]byte {
	key := normalizeKey(salt)

	h := blake3.New(16, key[:])
	_, _ = h.Write([]byte(ip))
	_, _ = h.Write([]byte{'|'})
	_, _ = h.Write([]byte(userAgent))

	var out [16]byte
	copy(out[:], h.Sum(nil))

	return out
}

// UserIDHash returns the per-tenant hash of the customer-supplied user_id
// (Privacy Rule 4). Drives events_raw.user_id_hash.
func UserIDHash(masterSecret []byte, siteID uint32, userID string) [32]byte {
	return tenantScopedSHA256(masterSecret, siteID, userID)
}

// HexUserIDHash is the hex-encoded form expected by events_raw.user_id_hash
// (defined as String in the schema). Returns "" for empty userID so the
// column stays empty rather than carrying a hash of nothing.
func HexUserIDHash(masterSecret []byte, siteID uint32, userID string) string {
	if userID == "" {
		return ""
	}

	sum := UserIDHash(masterSecret, siteID, userID)

	return hex.EncodeToString(sum[:])
}

// CookieIDHash returns the per-tenant hash of the visitor's _statnive
// cookie. The cookie value the tracker sees stays a raw UUID; only
// events_raw.cookie_id is hashed. Non-rotating — same input always
// yields the same output, preserving cross-day continuity for DSAR /
// erase queries that key off cookie_id.
func CookieIDHash(masterSecret []byte, siteID uint32, cookieID string) [32]byte {
	return tenantScopedSHA256(masterSecret, siteID, cookieID)
}

// HexCookieIDHash is the on-disk form: "h:" + hex(CookieIDHash). The "h:"
// prefix marks already-hashed rows so the backfill migration is
// idempotent (rerunning hex-prefixed rows is a no-op). Returns "" for
// empty cookieID — visitors who refused the cookie carry an empty value.
func HexCookieIDHash(masterSecret []byte, siteID uint32, cookieID string) string {
	if cookieID == "" {
		return ""
	}

	sum := CookieIDHash(masterSecret, siteID, cookieID)

	return "h:" + hex.EncodeToString(sum[:])
}

// tenantScopedSHA256 is the shared primitive both UserIDHash and
// CookieIDHash route through: SHA-256(masterSecret || siteIDBytes(siteID)
// || value). Keeping one body locks the (masterSecret, siteID, value)
// shape so a future encoding change (e.g. siteIDBytes endian) cannot
// drift between the two on-disk hashes.
func tenantScopedSHA256(masterSecret []byte, siteID uint32, value string) [32]byte {
	h := sha256.New()
	_, _ = h.Write(masterSecret)
	_, _ = h.Write(siteIDBytes(siteID))
	_, _ = h.Write([]byte(value))

	var out [32]byte
	copy(out[:], h.Sum(nil))

	return out
}

func siteIDBytes(siteID uint32) []byte {
	return binary.LittleEndian.AppendUint32(nil, siteID)
}

func normalizeKey(salt string) [32]byte {
	var k [32]byte

	if b, err := hex.DecodeString(salt); err == nil {
		copy(k[:], b)

		return k
	}

	copy(k[:], salt)

	return k
}
