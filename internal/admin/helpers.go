package admin

import (
	"context"

	"github.com/statnive/statnive.live/internal/auth"
)

// activeSiteOr returns the per-site site_id stashed by RequireSiteRole
// when the per_site_admin feature flag is ON, falling back to the
// legacy single-site value when the middleware didn't set the key
// (flag OFF path).
func activeSiteOr(ctx context.Context, fallback uint32) uint32 {
	if id, ok := auth.ActiveSiteIDFromContext(ctx); ok {
		return id
	}

	return fallback
}

// hostnameFor resolves the hostname for a site_id using the admin
// SitesStore. Returns empty string if the site is not found — handlers
// render "" gracefully rather than failing the request over a display
// field.
func hostnameFor(ctx context.Context, sites SitesStore, siteID uint32) string {
	if sites == nil {
		return ""
	}

	s, err := sites.LookupSiteByID(ctx, siteID)
	if err != nil {
		return ""
	}

	return s.Hostname
}
