// Package sites — origin index (Stage 4).
//
// OriginIndex is the in-memory origin → site_id lookup that
// internal/middleware/cors.go consults on every cross-origin request.
// Backed by atomic.Pointer[map[string]uint32] for lock-free hot-path
// reads — mirrors the GeoIP DB hot-swap pattern at
// internal/enrich/geoip.go (per geoip-pipeline-review skill: explicit
// rejection of sync.RWMutex for read-heavy maps).
//
// Lifecycle: built once at boot from Registry.ListAdmin, then rebuilt
// write-through on every admin write (admin/sites_handlers.go) and on
// SIGHUP as a safety valve. The map is small (≤ MaxAllowedOriginsPerSite
// × site_count, typically <100 entries at v1 SamplePlatform scale) so
// rebuilds are sub-millisecond and pay no allocation amortization
// concern.
//
// Threat model (Stage-4 plan §7): the map is a closed-world allowlist.
// Hot-path lookups are O(1) map reads against a frozen pointer; an
// attacker spamming unknown Origins forces map misses, not rebuilds.
// Rebuilds happen only on admin writes (rate-limited by the existing
// admin auth middleware) and SIGHUP (operator-initiated).
package sites

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
)

// SiteLister is the minimum read contract OriginIndex.Rebuild needs.
// Production: *Registry; tests: an in-memory fake. Kept narrower
// than the full Registry surface so the index doesn't accidentally
// pull in write or hot-path methods.
type SiteLister interface {
	ListAdmin(ctx context.Context) ([]SiteAdmin, error)
}

// OriginIndex carries the origin → site_id resolver consumed by
// the CORS middleware. Zero value is usable but resolves nothing
// until Rebuild runs at least once.
type OriginIndex struct {
	m atomic.Pointer[map[string]uint32]
}

// NewOriginIndex returns an unpopulated index. Callers MUST call
// Rebuild before mounting the middleware. Lookup is nil-safe before
// Rebuild — returns (0, false), which refuses every cross-origin
// request (safe default).
func NewOriginIndex() *OriginIndex {
	return &OriginIndex{}
}

// Lookup returns the site_id registered for the given Origin header
// value, or (0, false) if the Origin is unrecognised.
//
// Fast path: browser-sent Origin headers are already canonical
// (lowercase ASCII, no path, no trailing slash, no port for default
// ports). Direct map probe handles these in ~20 ns with zero alloc.
// Slow path (NormalizeOrigin + retry) covers stray test fixtures,
// uppercased hosts, trailing-slash artifacts; ~200 ns + 3-4 allocs
// on miss only.
//
// www. equivalence fallback: if neither the literal nor the
// canonicalised form resolves, the lookup retries once with the
// host's "www." prefix toggled. Mirrors LookupSitePolicy so a tenant
// who allowlists https://foo.com is also accepted from
// https://www.foo.com (and vice versa) — matches the existing ingest
// hostname-resolution behavior so CORS and ingest agree on www.
// equivalence.
func (i *OriginIndex) Lookup(origin string) (uint32, bool) {
	if origin == "" || origin == "null" {
		return 0, false
	}

	mp := i.m.Load()
	if mp == nil {
		return 0, false
	}

	if siteID, ok := (*mp)[origin]; ok {
		return siteID, true
	}

	canon, err := NormalizeOrigin(origin)
	if err != nil {
		return 0, false
	}

	if siteID, ok := (*mp)[canon]; ok {
		return siteID, true
	}

	alt, ok := wwwBareToggleOrigin(canon)
	if !ok {
		return 0, false
	}

	siteID, ok := (*mp)[alt]

	return siteID, ok
}

// Resolver returns a closure suitable for passing into the CORS
// middleware. Decouples the middleware from *OriginIndex so the
// middleware test can pass a fake without importing the sites
// package.
func (i *OriginIndex) Resolver() func(origin string) (uint32, bool) {
	return i.Lookup
}

// Rebuild atomically swaps in a fresh map built from r.ListAdmin().
// Any entry that fails NormalizeOrigin is silently skipped so a stale
// stored value never blocks the rebuild — Validate already ran at
// write time, so unparseable entries shouldn't be in the DB; if they
// are, dropping them is the safer recovery than crashing the rebuild
// path that other admins depend on.
//
// Returns the rebuilt entry count for observability — main.go's
// SIGHUP handler logs this so an operator can confirm a reload took
// effect.
func (i *OriginIndex) Rebuild(ctx context.Context, r SiteLister) (int, error) {
	if r == nil {
		return 0, errors.New("origin index: nil registry")
	}

	sites, err := r.ListAdmin(ctx)
	if err != nil {
		return 0, fmt.Errorf("origin index rebuild: %w", err)
	}

	return i.RebuildFromSites(sites), nil
}

// RebuildFromSites swaps in a fresh map built from a prefetched site
// list, skipping the ListAdmin round-trip. Admin write paths use this
// to fold the post-write rebuild into the same DB read that did the
// collision check — one round-trip per admin write instead of two.
func (i *OriginIndex) RebuildFromSites(allSites []SiteAdmin) int {
	var capHint int

	for _, s := range allSites {
		if s.Enabled {
			capHint += len(s.AllowedOrigins)
		}
	}

	fresh := make(map[string]uint32, capHint)

	for _, s := range allSites {
		if !s.Enabled {
			continue
		}

		for _, raw := range s.AllowedOrigins {
			canon, err := NormalizeOrigin(raw)
			if err != nil {
				continue
			}

			// If two sites somehow ended up with the same origin
			// (uniqueness check skipped or raced), keep the lower
			// site_id deterministically. Admin write-time
			// uniqueness check is the primary defence; this is the
			// rebuild-time tiebreak.
			if existing, dup := fresh[canon]; dup && existing <= s.ID {
				continue
			}

			fresh[canon] = s.ID
		}
	}

	i.m.Store(&fresh)

	return len(fresh)
}

// HasSelfHostInAnyAllowlist reports the first site whose
// allowed_origins includes selfHost (the SaaS host the binary serves
// under). main.go's boot pre-flight uses this to enforce the Stage-4
// plan's "self-host never in operator allowlist" invariant — if an
// operator allowlisted app.statnive.live itself, the CSRF + CORS
// defence collapses (any attacker page could read the SaaS-host's
// CSRF meta-tag through the permitted ACAO).
//
// Reads the in-memory canonical map rather than re-listing the DB —
// boot pre-flight runs immediately after Rebuild, so the map is
// fresh. Lower allocations, single map iteration, zero DB I/O.
//
// Returns (0, nil) when selfHost is empty (operator unset
// cfg.Server.PublicHost — skip the check).
func (i *OriginIndex) HasSelfHostInAnyAllowlist(selfHost string) uint32 {
	if selfHost == "" {
		return 0
	}

	canon, err := NormalizeOrigin("https://" + selfHost)
	if err != nil {
		// Malformed selfHost — main.go's separate hostname check
		// would have caught a truly invalid value; treat as no-match
		// here rather than blocking boot on a config error.
		return 0
	}

	mp := i.m.Load()
	if mp == nil {
		return 0
	}

	if siteID, ok := (*mp)[canon]; ok {
		return siteID
	}

	return 0
}
