package dashboard

import "net/http"

// realtimeHandler answers GET /api/realtime/visitors?site=N&channel=…
// Store.Realtime reads the current-hour bucket from hourly_visitors and
// (since migration 015) narrows by channel when the chip is active.
// The CachedStore wrapping in main.go pins the result for TTLRealtime
// (10s) per (site, channel) so dashboards polling once per second
// collapse to one ClickHouse query per 10s per slice.
//
// Authorization: same two-layer rule as filterFromRequest — context
// value set by RequireDashboardSiteAccess is the canonical site_id;
// fallback re-checks actor.ActorCanReadSite if the middleware wasn't
// mounted. Fail-closed contract.
func realtimeHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = "realtime"

		f, err := filterFromRequest(r, deps.Sites)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		result, err := deps.Store.Realtime(r.Context(), f)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, result)
	}
}
