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
	"slices"

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
//
// SiteID + Role are the single-site authorization fields. Sites is the
// per-site role map populated by RequireSiteRole on admin requests via
// LoadUserSites; nil on the dashboard hot path (no extra CH read).
type User struct {
	UserID    uuid.UUID
	SiteID    uint32
	Email     string
	Username  string
	Role      Role
	Disabled  bool
	CreatedAt int64 // unix seconds, UTC
	UpdatedAt int64
	Sites     map[uint32]Role
}

// CanAccessSite reports whether the user holds at least `required` role
// on siteID. Lower Role enum value = higher privilege (admin=1 < viewer=2
// < api=3); admin satisfies viewer and api. Fail-closed: nil receiver or
// unhydrated Sites map returns false.
func (u *User) CanAccessSite(siteID uint32, required Role) bool {
	if u == nil || u.Sites == nil {
		return false
	}

	got, ok := u.Sites[siteID]
	if !ok {
		return false
	}

	return roleRank(got) <= roleRank(required)
}

// SiteIDs returns the deterministic-sorted list of site_ids the user
// holds any non-revoked grant on. Used by /api/sites + /api/admin/sites
// to scope listing responses without leaking sites the user can't see.
func (u *User) SiteIDs() []uint32 {
	if u == nil || len(u.Sites) == 0 {
		return nil
	}

	out := make([]uint32, 0, len(u.Sites))
	for id := range u.Sites {
		out = append(out, id)
	}

	slices.Sort(out)

	return out
}

// roleRank maps a Role to its Enum8 numeric rank. Unknown roles fall
// back to a very-weak rank so they never satisfy any privilege check.
func roleRank(r Role) int {
	switch r {
	case RoleAdmin:
		return 1
	case RoleViewer:
		return 2
	case RoleAPI:
		return 3
	default:
		return 99
	}
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
	ctxKeyActiveSiteID
)

// WithActiveSiteID stashes the request's active site_id in context so
// admin handlers downstream of RequireSiteRole can read it back via
// ActiveSiteIDFromContext. The middleware already authorized the actor
// for this site_id; handlers MUST NOT re-derive site from session state.
func WithActiveSiteID(ctx context.Context, siteID uint32) context.Context {
	return context.WithValue(ctx, ctxKeyActiveSiteID, siteID)
}

// ActiveSiteIDFromContext returns the active site_id that
// RequireSiteRole stashed. Returns 0, false when not present — handlers
// that depend on a positive site_id treat that as 400 bad-request.
func ActiveSiteIDFromContext(ctx context.Context) (uint32, bool) {
	v, ok := ctx.Value(ctxKeyActiveSiteID).(uint32)
	return v, ok && v > 0
}

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
