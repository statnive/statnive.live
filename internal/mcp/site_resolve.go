package mcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/statnive/statnive.live/internal/sites"
)

// errUnknownSite is returned when the `site` arg can't be resolved to a real
// site_id. The dispatcher maps it to -32602 (invalid params); a genuine
// infra failure (CH down mid-resolve) maps to -32603 instead.
var errUnknownSite = errors.New("mcp: unknown site")

// resolveSite turns the `site` arg (numeric id | slug | hostname) into a
// site_id plus the site's IANA location (used for range parsing). Resolving
// does NOT grant access — ActorCanReadSite is the gate, run by the
// dispatcher after this returns.
func (s *Server) resolveSite(ctx context.Context, raw string) (uint32, *time.Location, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil, fmt.Errorf("%w: site is required", errUnknownSite)
	}

	id, err := s.resolveSiteID(ctx, raw)
	if err != nil {
		return 0, nil, err
	}

	sa, err := s.registry.LookupSiteByID(ctx, id)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %q", errUnknownSite, raw)
	}

	return id, sites.LocationFor(sa.Site.TZ), nil
}

// resolveSiteID tries numeric → slug → hostname. Numeric ids are accepted
// as-is (existence is confirmed by the LookupSiteByID in resolveSite).
func (s *Server) resolveSiteID(ctx context.Context, raw string) (uint32, error) {
	if n, err := strconv.ParseUint(raw, 10, 32); err == nil && n > 0 {
		return uint32(n), nil
	}

	id, err := s.registry.LookupSiteIDBySlug(ctx, raw)
	if err == nil {
		return id, nil
	}

	if !errors.Is(err, sites.ErrUnknownSlug) {
		return 0, fmt.Errorf("resolve slug: %w", err)
	}

	hid, _, herr := s.registry.LookupSitePolicy(ctx, raw)
	if herr == nil {
		return hid, nil
	}

	if errors.Is(herr, sites.ErrUnknownHostname) {
		return 0, fmt.Errorf("%w: %q", errUnknownSite, raw)
	}

	return 0, fmt.Errorf("resolve hostname: %w", herr)
}
