package dashboard

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
)

// SiteLister is the subset of *sites.Registry the dashboard consumes.
// Kept as an interface so tests can wire a stub without spinning up a
// real ClickHouse connection.
type SiteLister interface {
	List(ctx context.Context) ([]sites.Site, error)
}

// Deps groups the runtime collaborators every handler needs. Store is
// usually a *storage.CachedStore so 100 dashboard tabs collapse to one
// ClickHouse roundtrip per cache TTL.
type Deps struct {
	Store  storage.Store
	Sites  SiteLister
	Audit  *audit.Logger
	Logger *slog.Logger
}

// endpoint names — the strings that land in the audit log "endpoint"
// attr and (eventually) in metric labels. Keep these stable; renaming
// breaks operator dashboards that group on the field.
const (
	endpointOverview  = "overview"
	endpointSources   = "sources"
	endpointPages     = "pages"
	endpointSEO       = "seo"
	endpointTrend     = "trend"
	endpointCampaigns = "campaigns"
	endpointGeo       = "geo"
	endpointDevices   = "devices"
	endpointFunnel    = "funnel"
	endpointRealtime  = "realtime"
	endpointSites     = "sites"
)

// overviewHandler answers GET /api/stats/overview.
func overviewHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = endpointOverview

		f, err := filterFromRequest(r)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		result, err := deps.Store.Overview(r.Context(), f)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, result)
	}
}

func sourcesHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = "sources"

		f, err := filterFromRequest(r)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		result, err := deps.Store.Sources(r.Context(), f)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, result)
	}
}

func pagesHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = "pages"

		f, err := filterFromRequest(r)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		result, err := deps.Store.Pages(r.Context(), f)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, result)
	}
}

func seoHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = "seo"

		f, err := filterFromRequest(r)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		result, err := deps.Store.SEO(r.Context(), f)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, result)
	}
}

func trendHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = endpointTrend

		f, err := filterFromRequest(r)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		result, err := deps.Store.Trend(r.Context(), f)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, result)
	}
}

func campaignsHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = "campaigns"

		f, err := filterFromRequest(r)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		result, err := deps.Store.Campaigns(r.Context(), f)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, result)
	}
}

// geoHandler currently passes through to Store.Geo which returns
// ErrNotImplemented. The route is mounted in v1 so the dashboard SPA
// can attach to a 501 response instead of a 404; v1.1 swaps the Store
// implementation when the daily_geo rollup ships.
func geoHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = "geo"

		f, err := filterFromRequest(r)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		result, err := deps.Store.Geo(r.Context(), f)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, result)
	}
}

// devicesHandler — Store.Devices returns ErrNotImplemented in v1; same
// rationale as geoHandler.
func devicesHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = "devices"

		f, err := filterFromRequest(r)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		result, err := deps.Store.Devices(r.Context(), f)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, result)
	}
}

// funnelHandler — Store.Funnel returns ErrNotImplemented (v2). Reads
// ?steps=a,b,c from the query string for the future implementation.
func funnelHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = "funnel"

		f, err := filterFromRequest(r)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		// Second Query() allocation is acceptable here — Funnel is v2
		// (Store returns ErrNotImplemented), so this branch never
		// reaches the parse cost in production until the v2 work
		// arrives, at which point filterFromRequest can return the
		// pre-parsed query alongside the Filter.
		steps := r.URL.Query()["steps"]

		result, err := deps.Store.Funnel(r.Context(), f, steps)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, result)
	}
}
