package auth

import (
	"encoding/hex"
	"log/slog"
	"net/http"

	"github.com/statnive/statnive.live/internal/audit"
)

// RequireRole gates a handler to a set of allowed Roles. Must be
// composed AFTER SessionMiddleware + APITokenMiddleware +
// RequireAuthenticated so UserFrom is guaranteed non-nil; the internal
// nil-guard is belt-and-braces (PLAN.md §53).
func RequireRole(auditLog *audit.Logger, allowed ...Role) func(http.Handler) http.Handler {
	allowSet := make(map[Role]struct{}, len(allowed))

	for _, r := range allowed {
		allowSet[r] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFrom(r.Context())
			if u == nil {
				// This middleware should never run before
				// RequireAuthenticated; if it does, fail closed.
				http.Error(w, "unauthorized", http.StatusUnauthorized)

				return
			}

			if _, ok := allowSet[u.Role]; !ok {
				sess := SessionFrom(r.Context())

				attrs := []slog.Attr{
					slog.String("path", r.URL.Path),
					slog.String("role", string(u.Role)),
					slog.Uint64("site_id", uint64(u.SiteID)),
				}

				if sess != nil {
					attrs = append(attrs,
						slog.String("session_id_hash",
							hex.EncodeToString(sess.IDHash[:])),
					)
				}

				if auditLog != nil {
					auditLog.Event(r.Context(), audit.EventRBACDenied, attrs...)
				}

				http.Error(w, "forbidden", http.StatusForbidden)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
