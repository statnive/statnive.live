//go:build chatgpt_app

package oauthas

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

// PKCE S256 only. The "plain" method and a missing challenge are rejected at
// /authorize (validChallengeMethod); this file just verifies the S256 binding
// at /token. RFC 7636 §4.1: a verifier is 43–128 chars of the unreserved set.

const (
	minVerifierLen = 43
	maxVerifierLen = 128
)

// validChallengeMethod enforces S256 only — the PKCE-downgrade guard. An empty
// method or "plain" is rejected (never silently accepted).
func validChallengeMethod(method string) bool { return method == "S256" }

// isUnreserved reports whether c is in the RFC 7636 verifier unreserved set
// (ALPHA / DIGIT / "-" / "." / "_" / "~").
func isUnreserved(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	case c == '-', c == '.', c == '_', c == '~':
		return true
	default:
		return false
	}
}

// validVerifier checks the RFC 7636 syntax so a malformed verifier is rejected
// before the constant-time compare.
func validVerifier(v string) bool {
	if len(v) < minVerifierLen || len(v) > maxVerifierLen {
		return false
	}

	for i := range len(v) {
		if !isUnreserved(v[i]) {
			return false
		}
	}

	return true
}

// verifyPKCE recomputes BASE64URL(SHA256(verifier)) and compares it to the
// stored challenge in constant time. False on any malformed input.
func verifyPKCE(challenge, verifier string) bool {
	if challenge == "" || !validVerifier(verifier) {
		return false
	}

	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])

	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// validChallenge checks a challenge looks like a base64url-encoded SHA-256
// (43 chars, unreserved-ish) so /authorize rejects junk before persisting.
func validChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}

	for i := range len(c) {
		ch := c[i]

		base64url := (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') ||
			(ch >= '0' && ch <= '9') || ch == '-' || ch == '_'
		if !base64url {
			return false
		}
	}

	return true
}
