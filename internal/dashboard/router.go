package dashboard

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Mount registers every dashboard route on r. The caller (main.go)
// decides which middleware stack the routes live under (rate limit,
// optional bearer-token auth) by mounting Mount inside a chi.Group.
//
// Routes:
//
//	GET /api/stats/overview     — typed OverviewResult
//	GET /api/stats/sources      — []SourceRow
//	GET /api/stats/pages        — []PageRow
//	GET /api/stats/seo          — []SEORow (daily series with WITH FILL)
//	GET /api/stats/campaigns    — []CampaignRow
//	GET /api/stats/geo          — 501 in v1 (rollup ships in v1.1)
//	GET /api/stats/devices      — 501 in v1 (rollup ships in v1.1)
//	GET /api/stats/funnel       — 501 in v1 (windowFunnel ships in v2)
//	GET /api/realtime/visitors  — typed RealtimeResult
//
// Every route requires `?site=N`. Date params are YYYY-MM-DD IRST
// midnights; defaults to last-7-days.
func Mount(r chi.Router, deps Deps) {
	r.Method(http.MethodGet, "/api/stats/overview", overviewHandler(deps))
	r.Method(http.MethodGet, "/api/stats/sources", sourcesHandler(deps))
	r.Method(http.MethodGet, "/api/stats/pages", pagesHandler(deps))
	r.Method(http.MethodGet, "/api/stats/seo", seoHandler(deps))
	r.Method(http.MethodGet, "/api/stats/campaigns", campaignsHandler(deps))
	r.Method(http.MethodGet, "/api/stats/geo", geoHandler(deps))
	r.Method(http.MethodGet, "/api/stats/devices", devicesHandler(deps))
	r.Method(http.MethodGet, "/api/stats/funnel", funnelHandler(deps))

	r.Method(http.MethodGet, "/api/realtime/visitors", realtimeHandler(deps))
}
