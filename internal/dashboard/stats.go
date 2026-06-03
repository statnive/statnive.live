package dashboard

import (
	"context"
	"log/slog"
	"net/http"

	"golang.org/x/sync/errgroup"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
)

// sourcesResponse is the envelope returned by /api/stats/sources. The
// dashboard's Sources panel renders the per-channel grouped-bar chart
// from ByChannel and the per-referrer table from Rows; both honor the
// same filter. Two store calls run in parallel via errgroup to halve
// cold-cache wall-clock latency.
type sourcesResponse struct {
	Rows      []storage.SourceRow        `json:"rows"`
	ByChannel []storage.SourceChannelRow `json:"by_channel"`
}

// SiteLister is the subset of *sites.Registry the dashboard consumes.
// Kept as an interface so tests can wire a stub without spinning up a
// real ClickHouse connection. LookupSiteByID is needed by
// filterFromRequest so date-range parsing uses the per-site TZ
// (Currency is along for the ride for downstream handlers that surface
// it in responses).
type SiteLister interface {
	List(ctx context.Context) ([]sites.Site, error)
	LookupSiteByID(ctx context.Context, siteID uint32) (sites.SiteAdmin, error)
}

// Deps groups the runtime collaborators every handler needs. Store is
// usually a *storage.CachedStore so 100 dashboard tabs collapse to one
// ClickHouse roundtrip per cache TTL.
type Deps struct {
	Store  storage.Store
	Sites  SiteLister
	Audit  *audit.Logger
	Logger *slog.Logger
	// Goals is the in-memory enabled-goals snapshot. Optional — when nil
	// the Compare panel's goal autocomplete returns an empty list and
	// operators fall back to typing the event_name manually.
	Goals GoalLister
	// GeoEnabled is the v1.1-geo feature flag mirror. When false the
	// /api/stats/geo handler returns 501 (storage.ErrNotImplemented)
	// before touching the store — same behavior as v1, so the SPA
	// renders the Nav tab as "SOON". Flipped on by operator via
	// dashboard.geo_enabled in config/statnive-live.yaml after the
	// historical backfill in cmd/geo-backfill is verified.
	GeoEnabled bool
}

// GoalLister is the dashboard's view onto goals.Snapshot — kept narrow
// so tests can wire a stub without spinning up the full snapshot. The
// production wiring threads *goals.Snapshot in; nil disables the
// goal-autocomplete endpoint without breaking anything else.
type GoalLister interface {
	GoalsForSite(siteID uint32) []GoalSummary
}

// GoalSummary is the on-wire shape for /api/goals/list. Carries only
// the fields the autocomplete needs (label + the string that gets
// passed to /api/stats/compare as ?goal=). Operator value targets +
// match types + raw UUIDs stay on the admin endpoints.
type GoalSummary struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
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

		f, err := filterFromRequest(r, deps.Sites)
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

		f, err := filterFromRequest(r, deps.Sites)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		g, gctx := errgroup.WithContext(r.Context())

		var (
			rows []storage.SourceRow
			byCh []storage.SourceChannelRow
		)

		g.Go(func() error {
			out, gerr := deps.Store.Sources(gctx, f)
			rows = out

			return gerr
		})
		g.Go(func() error {
			out, gerr := deps.Store.SourcesByChannel(gctx, f)
			byCh = out

			return gerr
		})

		if err := g.Wait(); err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, sourcesResponse{Rows: rows, ByChannel: byCh})
	}
}

func pagesHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = "pages"

		f, err := filterFromRequest(r, deps.Sites)
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

		f, err := filterFromRequest(r, deps.Sites)
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

		f, err := filterFromRequest(r, deps.Sites)
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

		f, err := filterFromRequest(r, deps.Sites)
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

// geoResponse is the envelope returned by /api/stats/geo. Top drives
// the panel's "Top 10 by Visitors / Top 10 by Revenue" headline plus
// the share-of-visitors donut; Rows drives the country → province →
// city drill-down table. Two store calls run in parallel via errgroup
// to halve cold-cache wall-clock latency (same pattern as Sources).
type geoResponse struct {
	Top  []storage.GeoTopRow `json:"top"`
	Rows []storage.GeoRow    `json:"rows"`
}

// geoHandler answers GET /api/stats/geo (v1.1-geo). Gated by
// deps.GeoEnabled — when false the handler returns 501 via the same
// storage.ErrNotImplemented path the pre-v1.1 binary used, so flipping
// the flag is a true rollback rather than a behavior change.
func geoHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = endpointGeo

		if !deps.GeoEnabled {
			writeError(w, r, deps, endpoint, errFeatureDisabled)

			return
		}

		f, err := filterFromRequest(r, deps.Sites)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		g, gctx := errgroup.WithContext(r.Context())

		var (
			rows []storage.GeoRow
			top  []storage.GeoTopRow
		)

		g.Go(func() error {
			out, gerr := deps.Store.Geo(gctx, f)
			rows = out

			return gerr
		})
		g.Go(func() error {
			out, gerr := deps.Store.GeoTopCountries(gctx, f)
			top = out

			return gerr
		})

		if err := g.Wait(); err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, geoResponse{Top: top, Rows: rows})
	}
}

// devicesHandler — Store.Devices returns ErrNotImplemented in v1; same
// rationale as geoHandler.
func devicesHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = "devices"

		f, err := filterFromRequest(r, deps.Sites)
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

		f, err := filterFromRequest(r, deps.Sites)
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
