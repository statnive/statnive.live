package auth

import (
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"

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
				http.Error(w, "unauthorized", http.StatusUnauthorized)

				return
			}

			if _, ok := allowSet[u.Role]; !ok {
				emitRBACDenied(r, auditLog,
					slog.String("role", string(u.Role)),
					slog.Uint64("site_id", uint64(u.SiteID)),
				)
				http.Error(w, "forbidden", http.StatusForbidden)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// emitRBACDenied is the shared audit emit for RBAC-denied requests.
// Both RequireRole and RequireSiteRole pivot on (path, session hash)
// plus their per-policy attrs; this helper owns the common scaffold.
func emitRBACDenied(r *http.Request, auditLog *audit.Logger, extra ...slog.Attr) {
	if auditLog == nil {
		return
	}

	attrs := make([]slog.Attr, 0, len(extra)+2)
	attrs = append(attrs, slog.String("path", r.URL.Path))
	attrs = append(attrs, extra...)

	if sess := SessionFrom(r.Context()); sess != nil {
		attrs = append(attrs,
			slog.String("session_id_hash", hex.EncodeToString(sess.IDHash[:])),
		)
	}

	auditLog.Event(r.Context(), audit.EventRBACDenied, attrs...)
}

// RequireSiteRole is the per-site authorization middleware. Replaces
// RequireRole on /api/admin/* when the per_site_admin feature flag is
// on. Reads ?site_id=<n> from the request, loads the actor's fresh
// per-site grants from user_sites (no session-cache window), verifies
// the actor has at least `required` role on that site, then stashes
// (a) the active site_id in context and (b) a User-clone with the
// Sites map populated so handlers can call actor.CanAccessSite for
// defence-in-depth on resource loads.
//
// Errors:
//
//   - 400 if site_id is missing, non-numeric, or zero.
//   - 401 if the request is unauthenticated (must compose AFTER
//     RequireAuthenticated).
//   - 403 if the actor has no qualifying grant. Identical response
//     for both "site doesn't exist" and "site exists but no grant"
//     to avoid tenant enumeration (see plan § 6.10).
//
// Stash latency: one CH FINAL read over a tiny table (≤ users ×
// sites rows). ~50µs on the SaaS box.
func RequireSiteRole(
	auditLog *audit.Logger, sitesStore SitesStore, required Role,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFrom(r.Context())
			if u == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)

				return
			}

			siteID, parseErr := parseSiteID(r.URL.Query().Get("site_id"))
			if parseErr != nil {
				http.Error(w, "bad site_id", http.StatusBadRequest)

				return
			}

			grants, loadErr := sitesStore.LoadUserSites(r.Context(), u.UserID)
			if loadErr != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)

				return
			}

			// Shallow-copy the cached User to attach grants without
			// mutating the pointer CachedStore hands out. Safe today
			// because User has no slice fields (types.go) — if that
			// changes this becomes a deep-copy site.
			scoped := *u
			scoped.Sites = grants

			if !scoped.CanAccessSite(siteID, required) {
				emitRBACDenied(r, auditLog,
					slog.String("required_role", string(required)),
					slog.Uint64("site_id", uint64(siteID)),
					slog.String("actor_user_id", u.UserID.String()),
				)
				http.Error(w, "forbidden", http.StatusForbidden)

				return
			}

			ctx := WithSession(r.Context(), &scoped, SessionFrom(r.Context()))
			ctx = WithActiveSiteID(ctx, siteID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// parseSiteID parses ?site_id=<n> as a uint32. Empty / non-numeric /
// zero values all return an error so the middleware can return 400.
func parseSiteID(raw string) (uint32, error) {
	if raw == "" {
		return 0, ErrInvalidInput
	}

	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || n == 0 {
		return 0, ErrInvalidInput
	}

	return uint32(n), nil
}

