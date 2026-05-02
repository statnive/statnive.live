// Package admin owns the HTTP handlers for /api/admin/* — user + goal
// CRUD. Router (cmd/statnive-live/main.go) stacks
// auth.RequireRole(admin) before admin.Mount, so handlers here assume
// auth.UserFrom(ctx) returns a non-nil admin user. Every write handler
// decodes via httpjson.DecodeAllowed (enforced by the Semgrep rule
// admin-no-raw-json-decoder in blake3-hmac-identity-review).
package admin

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/goals"
	"github.com/statnive/statnive.live/internal/sites"
)

// SitesStore is the minimum interface admin sites_handlers needs. The
// production implementation is *sites.Registry; tests inject a
// fakeSitesStore. Kept narrow so callers don't accidentally take a hot-
// path dep (LookupSiteIDByHostname) into the admin surface.
type SitesStore interface {
	CreateSite(ctx context.Context, hostname, slug, tz string) (uint32, error)
	UpdateSiteEnabled(ctx context.Context, siteID uint32, enabled bool) error
	UpdateSitePolicy(ctx context.Context, siteID uint32, policy sites.SitePolicy) error
	LookupSiteByID(ctx context.Context, siteID uint32) (sites.SiteAdmin, error)
	ListAdmin(ctx context.Context) ([]sites.SiteAdmin, error)
}

// Deps bundles the dependencies every admin handler shares. One
// construction point (cmd/statnive-live/main.go), one source of truth.
// Every field is non-nil in production; tests may pass a subset where
// the handler doesn't touch the missing dep.
type Deps struct {
	Auth     auth.Store
	Goals    goals.Store
	Snapshot *goals.Snapshot // for post-write Reload()
	Sites    SitesStore
	Audit    *audit.Logger
	Logger   *slog.Logger
}

// emitDashboardError emits a single audit record for a handler that
// bottomed out in an unexpected error. Shared across users / goals /
// sites handlers — keep the shape identical so log aggregators can
// pivot on {path, reason, err} without per-entity knowledge.
func (d Deps) emitDashboardError(r *http.Request, reason string, err error) {
	if d.Audit == nil {
		return
	}

	d.Audit.Event(r.Context(), audit.EventDashboardError,
		slog.String("path", r.URL.Path),
		slog.String("reason", reason),
		slog.String("err", err.Error()),
	)
}
