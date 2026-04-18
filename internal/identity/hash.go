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

// UserIDHash returns SHA-256(masterSecret || site_id || user_id) — the only
// representation of the customer's user_id that ever reaches ClickHouse
// (Privacy Rule 4). The site_id is encoded little-endian so two tenants
// sharing a (masterSecret, user_id) pair land on different hashes.
func UserIDHash(masterSecret []byte, siteID uint32, userID string) [32]byte {
	h := sha256.New()
	_, _ = h.Write(masterSecret)
	_, _ = h.Write(siteIDBytes(siteID))
	_, _ = h.Write([]byte(userID))

	var out [32]byte
	copy(out[:], h.Sum(nil))

	return out
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
