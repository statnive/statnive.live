// Package auth implements dashboard authentication and RBAC:
// email/username/password login with bcrypt (cost 12), crypto/rand
// session tokens carried in SameSite=Lax HttpOnly cookies, and a
// three-role RBAC surface (admin / viewer / api). Bearer-token callers
// (CI smoke harness, legacy operator scripts) keep working via
// APITokenMiddleware — a pre-shared token maps to the `api` role.
//
// Non-goals for v1: 2FA, passwordless, SSO, self-serve signup (owned
// by Phase 11), password-reset email flow (owned by v1.1). See PLAN.md.
//
// Invariant (CVE-2024-10924): every function returning (*User, error),
// (*Session, error), or (*SessionInfo, error) in this package — and
// their callers — MUST guard the pointer against nil AFTER err==nil.
// Enforced by the blake3-hmac-identity-review skill's
// auth-return-nil-guard Semgrep rule + the regression test in
// nilguard_test.go.
package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Role is the RBAC ticket carried in the session context.
// Enum8 values in the users/sessions tables match the string form here.
type Role string

// Role values mirror the Enum8('admin'=1,'viewer'=2,'api'=3) column on
// the users and sessions tables. Adding a new role requires a migration.
const (
	RoleAdmin  Role = "admin"
	RoleViewer Role = "viewer"
	RoleAPI    Role = "api"
)

// Valid reports whether r is one of the three known roles.
func (r Role) Valid() bool {
	switch r {
	case RoleAdmin, RoleViewer, RoleAPI:
		return true
	}

	return false
}

// User is the operator-facing principal. password_hash is never exported
// on the wire; it's a DB-only field. Disabled users cannot create new
// sessions but existing sessions are revoked on disable.
type User struct {
	UserID    uuid.UUID
	SiteID    uint32
	Email     string
	Username  string
	Role      Role
	Disabled  bool
	CreatedAt int64 // unix seconds, UTC
	UpdatedAt int64
}

// Session is the server-side record of one logged-in client. The raw
// cookie token is never stored — only SHA-256(raw). Lookups compare hashes
// with hmac.Equal (constant-time).
type Session struct {
	IDHash     [32]byte // SHA-256 of the raw cookie value
	UserID     uuid.UUID
	SiteID     uint32
	Role       Role
	CreatedAt  int64
	LastUsedAt int64
	ExpiresAt  int64
	RevokedAt  int64 // 0 == not revoked
}

// SessionInfo bundles the two pointers a middleware hands to downstream
// handlers. Both fields non-nil on success; both nil on failure.
type SessionInfo struct {
	User    *User
	Session *Session
}

// Context keys for the typed helpers below. Unexported to avoid collision
// with other packages.
type ctxKey int

const (
	ctxKeyUser ctxKey = iota
	ctxKeySession
)

// WithSession attaches the current user + session to the request context.
// Middleware is the only caller; handlers read via UserFrom / SessionFrom.
func WithSession(ctx context.Context, u *User, s *Session) context.Context {
	ctx = context.WithValue(ctx, ctxKeyUser, u)
	ctx = context.WithValue(ctx, ctxKeySession, s)

	return ctx
}

// UserFrom returns the authenticated user for the request, or nil if the
// request is unauthenticated. Handlers after RequireRole can assume non-nil.
func UserFrom(ctx context.Context) *User {
	u, _ := ctx.Value(ctxKeyUser).(*User)

	return u
}

// SessionFrom returns the session record for the request, or nil.
func SessionFrom(ctx context.Context) *Session {
	s, _ := ctx.Value(ctxKeySession).(*Session)

	return s
}

// Sentinel errors. Store implementations return these so handlers can
// distinguish "unknown user" (404) from "auth failure" (401) without
// string-matching the wrapped error.
var (
	ErrNotFound       = errors.New("auth: record not found")
	ErrAlreadyExists  = errors.New("auth: email already registered")
	ErrInvalidInput   = errors.New("auth: invalid input")
	ErrBadCredentials = errors.New("auth: bad credentials")
	ErrDisabled       = errors.New("auth: user disabled")
	ErrExpired        = errors.New("auth: session expired")
	ErrRevoked        = errors.New("auth: session revoked")
	ErrLockedOut      = errors.New("auth: too many failed attempts")
	ErrRateLimited    = errors.New("auth: rate limited")
	ErrForbidden      = errors.New("auth: role not permitted for this route")
)
