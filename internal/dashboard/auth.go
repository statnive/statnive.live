package dashboard

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"

	"github.com/statnive/statnive.live/internal/audit"
)

// BearerTokenMiddleware gates every wrapped route with a single
// pre-shared bearer token. Empty token returns a no-op middleware
// (dev / local convenience); production deployments MUST set a token.
//
// Replaced wholesale by Phase 2b (bcrypt + crypto/rand sessions +
// SameSite=Lax cookies + admin/viewer/api RBAC). Until then this is the
// only thing keeping dashboard endpoints from being world-readable.
//
// Token comparison uses crypto/subtle.ConstantTimeCompare to avoid
// leaking the token via timing. NOTE: ConstantTimeCompare returns 0
// quickly on length mismatch — that leaks the token's length, which
// is acceptable for a pre-shared secret (operator-known, not derived
// from user input). Phase 2b's session-token comparison won't have
// this constraint.
func BearerTokenMiddleware(token string, auditLog *audit.Logger) func(http.Handler) http.Handler {
	if token == "" {
		return noopMiddleware
	}

	tokenBytes := []byte(token)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, ok := bearerTokenFromHeader(r)
			if !ok || subtle.ConstantTimeCompare([]byte(got), tokenBytes) != 1 {
				if auditLog != nil {
					auditLog.Event(r.Context(), audit.EventDashboardUnauthorized,
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

// noopMiddleware is the dev-mode pass-through.
func noopMiddleware(next http.Handler) http.Handler { return next }

// bearerTokenFromHeader extracts the token from `Authorization: Bearer
// <token>`. Case-insensitive on the scheme name per RFC 6750.
func bearerTokenFromHeader(r *http.Request) (string, bool) {
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
