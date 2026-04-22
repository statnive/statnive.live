package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/statnive/statnive.live/internal/audit"
)

// APIToken is one pre-shared bearer token allowed on the dashboard
// routes. TokenHashHex is SHA-256(raw) encoded as lowercase hex —
// operators paste hashes into config, never raw tokens.
type APIToken struct {
	TokenHashHex string
	SiteID       uint32
	Label        string // opaque; shows up in audit logs
	Role         Role   // defaults to RoleAPI when empty
}

// MiddlewareDeps bundles the shared dependencies middleware and
// handlers pull from main.go wiring.
type MiddlewareDeps struct {
	Store        Store
	Audit        *audit.Logger
	CookieCfg    SessionCookieConfig
	APITokens    []APIToken
	ClientIPFunc func(*http.Request) string // reuse internal/ingest.ClientIP
}

// SessionMiddleware reads the session cookie, looks it up via Store
// (cached), attaches *User + *Session to r.Context(), and hands off.
// Unauthenticated requests are NOT rejected here — the composite auth
// middleware below does that so API-token callers can still pass.
func SessionMiddleware(deps MiddlewareDeps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(deps.CookieCfg.Name)
			if err != nil || cookie.Value == "" {
				next.ServeHTTP(w, r)

				return
			}

			hash := HashRawToken(cookie.Value)

			info, err := deps.Store.LookupSession(r.Context(), hash)
			if err != nil {
				emitSessionLookupFailure(deps.Audit, r, hash, err)
				next.ServeHTTP(w, r)

				return
			}

			if info == nil || info.User == nil || info.Session == nil {
				// PLAN.md §53 nil-guard — this branch should be unreachable
				// because Store returns (nil, err) on failure, but defense
				// in depth.
				next.ServeHTTP(w, r)

				return
			}

			ctx := WithSession(r.Context(), info.User, info.Session)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// APITokenMiddleware resolves Authorization: Bearer <token> headers
// against the configured APIToken list. On match, a synthetic *User
// with Role=api (or token.Role) is attached to the context — no
// database row needed, so the Phase 3b smoke harness path stays
// zero-CH-call.
//
// Unlike SessionMiddleware, unknown bearer tokens are NOT silently
// ignored — they proceed without auth context (so Require* middleware
// rejects them cleanly). The wrong-token case is audited.
func APITokenMiddleware(deps MiddlewareDeps) func(http.Handler) http.Handler {
	// Pre-decode the hex hashes once so hot path doesn't re-decode.
	type resolved struct {
		hashBytes [32]byte
		siteID    uint32
		label     string
		role      Role
	}

	tokens := make([]resolved, 0, len(deps.APITokens))

	for _, t := range deps.APITokens {
		raw, err := hex.DecodeString(strings.ToLower(t.TokenHashHex))
		if err != nil || len(raw) != 32 {
			continue
		}

		var h [32]byte

		copy(h[:], raw)

		role := t.Role
		if role == "" {
			role = RoleAPI
		}

		tokens = append(tokens, resolved{h, t.SiteID, t.Label, role})
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if UserFrom(r.Context()) != nil {
				// Session middleware already attached a user — don't
				// double-bind.
				next.ServeHTTP(w, r)

				return
			}

			raw, ok := bearerToken(r)
			if !ok {
				next.ServeHTTP(w, r)

				return
			}

			got := sha256.Sum256([]byte(raw))

			for _, t := range tokens {
				if !constantTimeEq(t.hashBytes[:], got[:]) {
					continue
				}

				// Synthetic user — no DB row. Role comes from config.
				u := &User{
					SiteID:   t.siteID,
					Role:     t.role,
					Username: "api:" + t.label,
				}
				s := &Session{
					IDHash: got,
					SiteID: t.siteID,
					Role:   t.role,
				}

				ctx := WithSession(r.Context(), u, s)
				next.ServeHTTP(w, r.WithContext(ctx))

				return
			}

			// Wrong bearer token → pass through without auth so
			// Require* rejects with the correct 401 shape + audit.
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAuthenticated is the 401 guard after Session + API-token
// middleware. Emits EventDashboardUnauthorized on failure, matching
// the existing bearer-middleware audit shape so /healthz dashboards
// don't flip.
func RequireAuthenticated(auditLog *audit.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if UserFrom(r.Context()) == nil {
				if auditLog != nil {
					auditLog.Event(r.Context(),
						audit.EventDashboardUnauthorized,
						slog.String("path", r.URL.Path),
					)
				}

				w.Header().Set("WWW-Authenticate", `Bearer realm="statnive-live"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// bearerToken extracts the Authorization: Bearer value.
// Case-insensitive scheme per RFC 6750.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}

	const prefix = "bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}

	return strings.TrimSpace(h[len(prefix):]), true
}

func emitSessionLookupFailure(
	auditLog *audit.Logger, r *http.Request, hash [32]byte, err error,
) {
	if auditLog == nil {
		return
	}

	var name audit.EventName

	switch {
	case errors.Is(err, ErrExpired):
		name = audit.EventSessionExpired
	case errors.Is(err, ErrRevoked), errors.Is(err, ErrDisabled):
		name = audit.EventSessionRevoked
	default:
		name = audit.EventDashboardUnauthorized
	}

	auditLog.Event(r.Context(), name,
		slog.String("path", r.URL.Path),
		slog.String("session_id_hash", hex.EncodeToString(hash[:])),
		slog.String("reason", err.Error()),
	)
}

// SiteIDFrom is a convenience for dashboard handlers building Filter:
// returns the authenticated user's site_id, or 0 if unauthenticated.
func SiteIDFrom(ctx context.Context) uint32 {
	if u := UserFrom(ctx); u != nil {
		return u.SiteID
	}

	return 0
}
