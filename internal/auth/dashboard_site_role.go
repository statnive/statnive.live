package auth

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/audit"
)

// RequireDashboardSiteAccess is the per-site authorization middleware for
// dashboard read routes (/api/stats/* and /api/realtime/visitors). Sibling
// of RequireSiteRole but with three deliberate differences:
//
//  1. Reads ?site=N (dashboard convention) — NOT ?site_id=N (admin
//     convention). Renaming the param across the operator scripts /
//     tracker URLs / SPA is out of scope; the two middlewares accept the
//     names the existing world uses.
//
//  2. Fails CLOSED when ?site is missing. RequireSiteRole passes through
//     for global admin routes like /api/admin/sites listing; dashboard
//     has no global-site equivalent — every /api/stats/* call carries a
//     site_id, so absence is malformed input.
//
//  3. API-token actors (synthetic User with UserID == uuid.Nil) have no
//     user_sites rows by construction. The middleware treats their Sites
//     map as {token.SiteID: token.Role} — the token capability is exactly
//     its issued scope; the underlying user's other grants do NOT escalate
//     a bearer token.
//
// Legacy flag-OFF deploys pass sitesStore=nil. In that mode, actor.Sites
// stays nil and the middleware enforces the single-site invariant
// actor.SiteID == requestedSite. Same fail-closed posture.
//
// Errors (all uniform body `{"error":"forbidden"}` for 403 to avoid
// distinguishing "no grant" from "no such site" by response shape):
//
//   - 400 if ?site is missing / non-numeric / zero.
//   - 401 if the request is unauthenticated.
//   - 403 if the actor has no qualifying grant on the requested site.
//
// On success: attaches the scoped User (with grants map hydrated) and
// stashes the validated site_id via WithActiveSiteID so downstream
// handlers read it from context instead of re-parsing ?site (defense in
// depth: filterFromRequest also re-checks if ActiveSiteIDFromContext is
// empty, which would indicate this middleware wasn't mounted — fail
// closed there too).
//
// Latency: at most one CH FINAL read over a tiny table on cache miss.
// Production wires sitesStore as a *CachedSitesStore with a 60s TTL —
// at full dashboard EPS the cache hit ratio stabilises above 0.95 within
// seconds, so amortised cost is <1µs/request. Mutation paths
// (Grant/Revoke) invalidate immediately so a revoke takes effect on the
// next request, not after the TTL window.
func RequireDashboardSiteAccess(
	auditLog *audit.Logger, sitesStore SitesStore, required Role,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFrom(r.Context())
			if u == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)

				return
			}

			siteID, parseErr := parseSiteID(r.URL.Query().Get("site"))
			if parseErr != nil {
				http.Error(w, "bad site", http.StatusBadRequest)

				return
			}

			scoped, ok := scopeUserToSite(r, u, siteID, required, sitesStore)
			if !ok {
				emitRBACDenied(r, auditLog,
					slog.String("required_role", string(required)),
					slog.Uint64("site_id", uint64(siteID)),
					slog.String("actor_user_id", u.UserID.String()),
				)

				if auditLog != nil {
					auditLog.Event(r.Context(), audit.EventDashboardForbidden,
						slog.String("path", r.URL.Path),
						slog.Uint64("site_id", uint64(siteID)),
						slog.String("actor_user_id", u.UserID.String()),
					)
				}

				http.Error(w, "forbidden", http.StatusForbidden)

				return
			}

			ctx := WithSession(r.Context(), scoped, SessionFrom(r.Context()))
			ctx = WithActiveSiteID(ctx, siteID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// scopeUserToSite returns a shallow-copied User with the Sites map
// hydrated for the request, or (nil, false) when the actor has no
// qualifying grant on siteID. Three branches keep the legacy +
// per-site-admin + api-token paths in one helper so the call sites
// don't ad-hoc-reimplement them.
func scopeUserToSite(
	r *http.Request, u *User, siteID uint32, required Role, sitesStore SitesStore,
) (*User, bool) {
	scoped := *u // shallow copy; User has no slice fields

	switch {
	case u.UserID == uuid.Nil:
		// API-token actor — synthetic user, no user_sites rows. The
		// token's bound (site_id, role) IS the grant; tokens never
		// escalate to the underlying user's other sites.
		if u.SiteID != siteID {
			return nil, false
		}

		if roleRank(u.Role) > roleRank(required) {
			return nil, false
		}

		scoped.Sites = map[uint32]Role{siteID: u.Role}

	case sitesStore == nil:
		// Legacy flag-OFF deploy — no user_sites table queried, only the
		// session-bound primary site applies. Same fail-closed semantics
		// but the source of truth is users.site_id instead of user_sites.
		if u.SiteID != siteID {
			return nil, false
		}

		if roleRank(u.Role) > roleRank(required) {
			return nil, false
		}

		scoped.Sites = nil // explicit: legacy mode signals "no grants map"

	default:
		// Per-site-admin path — load fresh grants on every request
		// (CachedStore handles the TTL window for hot paths).
		grants, loadErr := sitesStore.LoadUserSites(r.Context(), u.UserID)
		if loadErr != nil {
			return nil, false
		}

		role, hasGrant := grants[siteID]
		if !hasGrant || roleRank(role) > roleRank(required) {
			return nil, false
		}

		scoped.Sites = grants
	}

	return &scoped, true
}
