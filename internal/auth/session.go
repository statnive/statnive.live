package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

// tokenRawBytes is the number of random bytes in a freshly-minted
// session token. 32 bytes = 256 bits of entropy; hex-encoded to 64 chars.
// Never change this without invalidating all existing sessions — the
// sessions table stores SHA-256 of the hex string, not raw bytes.
const tokenRawBytes = 32

// TokenPair is what NewToken returns: the raw string sent to the client
// as a cookie, and the SHA-256 hash stored in ClickHouse.
type TokenPair struct {
	Raw  string   // hex-encoded, 64 chars; sent in the Set-Cookie header
	Hash [32]byte // SHA-256(Raw); stored in sessions.session_id_hash
}

// NewToken reads 32 cryptographically random bytes from crypto/rand,
// hex-encodes them for cookie safety, and returns both the raw value
// and its SHA-256 hash. The raw value MUST NOT be persisted — only the
// hash.
func NewToken() (TokenPair, error) {
	buf := make([]byte, tokenRawBytes)
	if _, err := rand.Read(buf); err != nil {
		return TokenPair{}, fmt.Errorf("crypto/rand: %w", err)
	}

	raw := hex.EncodeToString(buf)

	return TokenPair{Raw: raw, Hash: sha256.Sum256([]byte(raw))}, nil
}

// HashRawToken is the lookup helper: given a raw token read from a
// cookie header, return the SHA-256 hash to compare against
// sessions.session_id_hash. Never exported publicly as the sole
// consumer is the session middleware.
func HashRawToken(raw string) [32]byte {
	return sha256.Sum256([]byte(raw))
}

// SessionCookieConfig captures the subset of cookie attributes that
// main.go needs to decide (per environment). `Secure: false` must only
// appear when STATNIVE_DEV=1 is set (see CookieFromToken's docstring).
type SessionCookieConfig struct {
	Name     string
	TTL      time.Duration
	Secure   bool
	SameSite http.SameSite
}

// CookieFromToken builds a Set-Cookie header for a freshly-minted
// session. Mirrors the tracker cookie shape at
// internal/ingest/handler.go:113-120 (HttpOnly + SameSite=Lax + Path=/).
// Caller is expected to validate cfg.Secure=false is only allowed under
// STATNIVE_DEV=1 — the config loader enforces that constraint.
func CookieFromToken(cfg SessionCookieConfig, raw string, now time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     cfg.Name,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: cfg.SameSite,
		Expires:  now.Add(cfg.TTL),
		MaxAge:   int(cfg.TTL.Seconds()),
	}
}

// ClearCookie is the logout companion of CookieFromToken. Emits a
// cookie with the same Name/Path/Secure/SameSite but a zero MaxAge and
// empty value so the browser evicts it immediately.
func ClearCookie(cfg SessionCookieConfig) *http.Cookie {
	return &http.Cookie{
		Name:     cfg.Name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: cfg.SameSite,
		MaxAge:   -1,
	}
}
