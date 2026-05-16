package dashboard

import (
	"errors"
	"net/http"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/sites"
)

// sitesResponse is the JSON envelope GET /api/sites returns. Wrapped in
// a {"sites": [...]} object (not a bare array) so future versions can
// add sibling keys (pagination, feature flags) without breaking clients.
type sitesResponse struct {
	Sites []sites.Site `json:"sites"`
}

// sitesHandler answers GET /api/sites — the dashboard's site switcher
// reads this once at boot to render the dropdown and pick an active
// tenant. Behind requireAuthed (session OR api-token) so unauthenticated
// probes can't enumerate tenants. Authorised actors only see sites their
// grants cover — preventing cross-tenant enumeration (OWASP A01:2021).
//
// Filtering branches:
//
//   - API-token actor (synthetic UserID == uuid.Nil): exactly one site,
//     whichever the token is bound to. No escalation to the underlying
//     user's other grants.
//   - Per-site-admin path (actor.Sites populated): intersection of the
//     registry with CanAccessSite(viewer-or-above).
//   - Legacy flag-OFF (actor.Sites nil): single match on actor.SiteID.
//
// Empty result is a valid response (`{"sites":[]}`) — the SPA renders the
// onboarding banner. Never 403 here, because the dropdown bootstrap path
// must surface "you have no sites" UX, not a hard error.
func sitesHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = endpointSites

		if deps.Sites == nil {
			writeError(w, r, deps, endpoint, errors.New("sites registry not wired"))

			return
		}

		rows, err := deps.Sites.List(r.Context())
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, sitesResponse{
			Sites: filterSitesForActor(auth.UserFrom(r.Context()), rows),
		})
	}
}

// filterSitesForActor returns only the registry rows the actor is
// allowed to see. Uses auth.User.ActorCanReadSite as the single source
// of truth for the api-token / per-site / legacy branches, so this
// stays in lockstep with the middleware + filter-level guards.
//
// Defense-in-depth: nil actor is unreachable in production (requireAuthed
// precedes), but if a future caller mounts this without auth, return an
// empty list rather than the full registry.
func filterSitesForActor(actor *auth.User, rows []sites.Site) []sites.Site {
	if rows == nil || actor == nil {
		return []sites.Site{}
	}

	out := make([]sites.Site, 0, len(rows))

	for _, s := range rows {
		if actor.ActorCanReadSite(s.ID) {
			out = append(out, s)
		}
	}

	return out
}
