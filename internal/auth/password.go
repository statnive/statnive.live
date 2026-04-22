package auth

import (
	"crypto/subtle"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// MinBcryptCost is the minimum cost HashPassword will accept. 12 matches
// CLAUDE.md Security #6; the OWASP 2024 recommendation is 12+ on x86-64
// (roughly 250 ms per verify on a modern core), high enough to defeat
// offline cracking while staying tolerable on the login hot path.
const MinBcryptCost = 12

// dummyHash is a pre-computed bcrypt hash used to make the
// unknown-user case wall-time indistinguishable from the
// wrong-password case. Package-init hashes a sentinel string at
// MinBcryptCost; the login handler calls Verify against it when
// GetUserByEmail returns ErrNotFound. See PLAN.md § Login rate-limit.
var dummyHash []byte

func init() {
	h, err := bcrypt.GenerateFromPassword(
		[]byte("statnive-live-unknown-user-dummy-v1"), MinBcryptCost,
	)
	if err != nil {
		// Panic in init is fine — if bcrypt can't hash at startup the
		// whole binary is broken anyway.
		panic(fmt.Errorf("auth: dummy-hash init: %w", err))
	}

	dummyHash = h
}

// HashPassword returns a bcrypt hash of plaintext at the given cost.
// Cost < MinBcryptCost is rejected — callers must not downgrade.
func HashPassword(plaintext string, cost int) (string, error) {
	if cost < MinBcryptCost {
		return "", fmt.Errorf("%w: bcrypt cost %d below minimum %d",
			ErrInvalidInput, cost, MinBcryptCost)
	}

	if plaintext == "" {
		return "", fmt.Errorf("%w: empty password", ErrInvalidInput)
	}

	h, err := bcrypt.GenerateFromPassword([]byte(plaintext), cost)
	if err != nil {
		return "", fmt.Errorf("bcrypt generate: %w", err)
	}

	return string(h), nil
}

// VerifyPassword returns nil iff plaintext matches hash. Returns
// ErrBadCredentials on any mismatch. Callers upstream of the store
// (login handler) pass dummyHash when the user was not found so the
// wall-time for unknown-user equals wrong-password.
func VerifyPassword(hash, plaintext string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext))
	if err == nil {
		return nil
	}

	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return ErrBadCredentials
	}

	return fmt.Errorf("bcrypt compare: %w", err)
}

// VerifyAgainstDummy runs a constant-cost bcrypt compare against the
// package dummyHash. Used by the login handler on the unknown-user
// branch so an attacker can't distinguish "no such account" from
// "wrong password" via response time. Always returns ErrBadCredentials.
func VerifyAgainstDummy(plaintext string) error {
	// Discard the return — we always report bad credentials on the
	// unknown-user branch regardless of what bcrypt says.
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(plaintext))

	return ErrBadCredentials
}

// constantTimeEq is a thin wrapper around subtle.ConstantTimeCompare
// for byte slices of equal length. Returns false immediately on length
// mismatch (as subtle does). Used by session hash lookup.
func constantTimeEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}

	return subtle.ConstantTimeCompare(a, b) == 1
}
