package dashboard

import (
	"errors"
	"net/http"

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
// tenant. Behind BearerTokenMiddleware (same as every other /api/*
// route), so unauthenticated probes can't enumerate tenants.
//
// Empty-registry case returns {"sites": []} — the SPA handles "no sites
// yet" by rendering a first-run onboarding banner.
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

		if rows == nil {
			rows = []sites.Site{}
		}

		writeOK(w, r, deps, endpoint, sitesResponse{Sites: rows})
	}
}
