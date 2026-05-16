package dashboard

import "net/http"

// realtimeHandler answers GET /api/realtime/visitors?site=N. The query
// is fixed-shape (no Filter, just siteID) — Store.Realtime queries the
// current hour bucket from hourly_visitors. The CachedStore wrapping
// in main.go pins the result for TTLRealtime (10s) so dashboards
// polling once per second collapse to one ClickHouse query per 10s
// per site.
//
// Authorization: same two-layer rule as filterFromRequest — context
// value set by RequireDashboardSiteAccess is the canonical site_id;
// fallback re-checks actor.ActorCanReadSite if the middleware wasn't
// mounted. Fail-closed contract.
func realtimeHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = "realtime"

		siteID, err := resolveAuthorizedSiteID(r, r.URL.Query().Get("site"))
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		result, err := deps.Store.Realtime(r.Context(), siteID)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, result)
	}
}
