package dashboard

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// MountSiteListing registers GET /api/sites — the site-switcher
// bootstrap route. The handler internally filters the registry by the
// actor's grants, so this route does NOT need a per-site auth
// middleware. Date params are not applicable here.
//
// Routes:
//
//	GET /api/sites              — typed sitesResponse (site switcher)
//
// Caller wraps this in a chi.Group with session + api-token auth and
// the role floor (admin/viewer/api). See cmd/statnive-live/main.go.
func MountSiteListing(r chi.Router, deps Deps) {
	r.Method(http.MethodGet, "/api/sites", sitesHandler(deps))
}

// MountSiteScoped registers every dashboard route that requires
// ?site=N. The caller MUST stack RequireDashboardSiteAccess before
// these routes — that middleware validates the actor's grant on the
// requested site and stashes the validated site_id in context. Defense
// in depth: filterFromRequest re-checks via actor.CanAccessSite if the
// middleware is somehow missing.
//
// Routes:
//
//	GET /api/stats/overview     — typed OverviewResult
//	GET /api/stats/sources      — []SourceRow
//	GET /api/stats/pages        — []PageRow
//	GET /api/stats/seo          — []SEORow (organic-only daily series)
//	GET /api/stats/trend        — []DailyPoint (all-traffic daily series)
//	GET /api/stats/campaigns    — []CampaignRow
//	GET /api/stats/geo          — 501 in v1 (rollup ships in v1.1)
//	GET /api/stats/devices      — 501 in v1 (rollup ships in v1.1)
//	GET /api/stats/funnel       — 501 in v1 (windowFunnel ships in v2)
//	GET /api/realtime/visitors  — typed RealtimeResult
//
// Date params are YYYY-MM-DD midnights in the site's TZ; default is
// last-7-days.
func MountSiteScoped(r chi.Router, deps Deps) {
	r.Method(http.MethodGet, "/api/stats/overview", overviewHandler(deps))
	r.Method(http.MethodGet, "/api/stats/sources", sourcesHandler(deps))
	r.Method(http.MethodGet, "/api/stats/pages", pagesHandler(deps))
	r.Method(http.MethodGet, "/api/stats/seo", seoHandler(deps))
	r.Method(http.MethodGet, "/api/stats/trend", trendHandler(deps))
	r.Method(http.MethodGet, "/api/stats/campaigns", campaignsHandler(deps))
	r.Method(http.MethodGet, "/api/stats/geo", geoHandler(deps))
	r.Method(http.MethodGet, "/api/stats/devices", devicesHandler(deps))
	r.Method(http.MethodGet, "/api/stats/funnel", funnelHandler(deps))

	r.Method(http.MethodGet, "/api/realtime/visitors", realtimeHandler(deps))
}

// Mount preserves the legacy single-group entry point for callers that
// don't (yet) split site-listing from site-scoped enforcement. Equivalent
// to MountSiteListing + MountSiteScoped on the same router, but lacks
// the per-site authorization middleware — only suitable for tests that
// supply their own context.
//
// Deprecated: production callers MUST use MountSiteListing +
// MountSiteScoped with auth.RequireDashboardSiteAccess on the scoped
// subgroup. This shim exists so existing test fixtures don't have to be
// rewritten in lockstep with the prod wiring.
func Mount(r chi.Router, deps Deps) {
	MountSiteListing(r, deps)
	MountSiteScoped(r, deps)
}
