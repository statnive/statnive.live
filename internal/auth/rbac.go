package auth

import (
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

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
// on. Always loads the actor's fresh per-site grants from user_sites
// (no session-cache window) and rejects requests where the actor has
// no qualifying grant on ANY site. When ?site_id=<n> is present, also
// validates the actor's grant on that specific site and stashes it in
// context for downstream handlers; routes that don't carry ?site_id
// (e.g. /api/admin/sites listing, /api/admin/currencies, per-user
// operations by UUID path-param) pass through without the per-site
// check — defence-in-depth then runs in the handler.
//
// Errors:
//
//   - 400 if site_id is non-numeric or zero (when present).
//   - 401 if the request is unauthenticated.
//   - 403 if the actor has no qualifying admin grant on any site, or
//     no grant on the specific ?site_id when one is supplied.
//
// Latency: one CH FINAL read over a tiny table (≤ users × sites rows).
// ~50µs on the SaaS box.
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

			// API-token users (synthetic, UserID=uuid.Nil) have no
			// user_sites rows by construction. Skip the lookup; floor
			// check below will 403 them on every admin route — which is
			// the documented contract for the `api` role.
			grants := map[uint32]Role{}

			if u.UserID != uuid.Nil {
				loaded, loadErr := sitesStore.LoadUserSites(r.Context(), u.UserID)
				if loadErr != nil {
					http.Error(w, "internal error", http.StatusInternalServerError)

					return
				}

				grants = loaded
			}

			// Floor check: actor must have at least one grant satisfying
			// `required` SOMEWHERE. Blocks users with revoked-everywhere
			// state from touching /api/admin/* regardless of which route.
			if !hasAnyRole(grants, required) {
				emitRBACDenied(r, auditLog,
					slog.String("required_role", string(required)),
					slog.String("actor_user_id", u.UserID.String()),
				)
				http.Error(w, "forbidden", http.StatusForbidden)

				return
			}

			// Shallow-copy the cached User to attach grants without
			// mutating the pointer CachedStore hands out. Safe today
			// because User has no slice fields (types.go).
			scoped := *u
			scoped.Sites = grants

			ctx := WithSession(r.Context(), &scoped, SessionFrom(r.Context()))

			// Optional site_id validation. Missing query param is fine —
			// the route is a global / resource-by-id surface and the
			// handler authorizes the resource itself. When supplied,
			// it must be valid AND the actor must have role on that site.
			if raw := r.URL.Query().Get("site_id"); raw != "" {
				siteID, parseErr := parseSiteID(raw)
				if parseErr != nil {
					http.Error(w, "bad site_id", http.StatusBadRequest)

					return
				}

				if !scoped.CanAccessSite(siteID, required) {
					emitRBACDenied(r, auditLog,
						slog.String("required_role", string(required)),
						slog.Uint64("site_id", uint64(siteID)),
						slog.String("actor_user_id", u.UserID.String()),
					)
					http.Error(w, "forbidden", http.StatusForbidden)

					return
				}

				ctx = WithActiveSiteID(ctx, siteID)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// hasAnyRole reports whether the actor has at least one active grant
// satisfying `required` (lower roleRank = higher privilege; admin=1
// satisfies viewer=2 and api=3).
func hasAnyRole(grants map[uint32]Role, required Role) bool {
	for _, r := range grants {
		if roleRank(r) <= roleRank(required) {
			return true
		}
	}

	return false
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
