package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/statnive/statnive.live/internal/cache"
)

// CachedStore decorates a Store with the tiered-TTL LRU cache from
// internal/cache. The handler layer in Phase 3b consumes this, not the
// bare clickhouseStore — except where the test wants to count
// ClickHouse roundtrips.
//
// One LRU instance holds entries for every endpoint; keys are
// "<endpoint>:<filter-hash>" so collisions across endpoints are
// impossible. The TTL per entry derives from cache.ResolveTTL(now,
// filter.To) so today/yesterday/historical buckets get the right
// staleness budget.
type CachedStore struct {
	inner Store
	cache *cache.Cache
	now   func() time.Time
}

// NewCachedStore wraps inner with an LRU of the given capacity.
// Capacity sizing is set by the caller — see the dashboardCacheCapacity
// constant in cmd/statnive-live/main.go for the production default.
func NewCachedStore(inner Store, capacity int) *CachedStore {
	return &CachedStore{
		inner: inner,
		cache: cache.New(capacity),
		now:   time.Now,
	}
}

// SetClock replaces the wall-clock source. Test-only — callers in
// production must not invoke this. Concurrency-safe only when called
// before any read.
func (c *CachedStore) SetClock(now func() time.Time) { c.now = now }

// Purge clears the cache. Used by /api/admin/cache/purge in v2 + by
// tests that want a clean slate between cases.
func (c *CachedStore) Purge() { c.cache.Purge() }

// Len returns the cache's current entry count. Diagnostic only.
func (c *CachedStore) Len() int { return c.cache.Len() }

func (c *CachedStore) Overview(ctx context.Context, f *Filter) (*OverviewResult, error) {
	v, err := c.cache.Wrap(
		"overview:"+f.Hash(),
		cache.ResolveTTL(c.now(), f.To),
		func() (any, error) { return c.inner.Overview(ctx, f) },
	)
	if err != nil {
		return nil, err
	}

	return v.(*OverviewResult), nil
}

func (c *CachedStore) Sources(ctx context.Context, f *Filter) ([]SourceRow, error) {
	v, err := c.cache.Wrap(
		"sources:"+f.Hash(),
		cache.ResolveTTL(c.now(), f.To),
		func() (any, error) { return c.inner.Sources(ctx, f) },
	)
	if err != nil {
		return nil, err
	}

	return v.([]SourceRow), nil
}

func (c *CachedStore) Pages(ctx context.Context, f *Filter) ([]PageRow, error) {
	v, err := c.cache.Wrap(
		"pages:"+f.Hash(),
		cache.ResolveTTL(c.now(), f.To),
		func() (any, error) { return c.inner.Pages(ctx, f) },
	)
	if err != nil {
		return nil, err
	}

	return v.([]PageRow), nil
}

func (c *CachedStore) SEO(ctx context.Context, f *Filter) ([]SEORow, error) {
	v, err := c.cache.Wrap(
		"seo:"+f.Hash(),
		cache.ResolveTTL(c.now(), f.To),
		func() (any, error) { return c.inner.SEO(ctx, f) },
	)
	if err != nil {
		return nil, err
	}

	return v.([]SEORow), nil
}

func (c *CachedStore) Campaigns(ctx context.Context, f *Filter) ([]CampaignRow, error) {
	v, err := c.cache.Wrap(
		"campaigns:"+f.Hash(),
		cache.ResolveTTL(c.now(), f.To),
		func() (any, error) { return c.inner.Campaigns(ctx, f) },
	)
	if err != nil {
		return nil, err
	}

	return v.([]CampaignRow), nil
}

// Realtime is always cached at TTLRealtime (10s) regardless of clock —
// the underlying query reads the current hour bucket which doesn't
// have a Filter.To to inspect.
func (c *CachedStore) Realtime(ctx context.Context, siteID uint32) (*RealtimeResult, error) {
	key := fmt.Sprintf("realtime:%d", siteID)

	v, err := c.cache.Wrap(
		key,
		cache.TTLRealtime,
		func() (any, error) { return c.inner.Realtime(ctx, siteID) },
	)
	if err != nil {
		return nil, err
	}

	return v.(*RealtimeResult), nil
}

// Geo / Devices / Funnel pass through to the inner Store, which
// returns ErrNotImplemented in v1. Caching the not-implemented error
// would be wasteful; the inner call is cheap.
func (c *CachedStore) Geo(ctx context.Context, f *Filter) ([]GeoRow, error) {
	return c.inner.Geo(ctx, f)
}

func (c *CachedStore) Devices(ctx context.Context, f *Filter) ([]DeviceRow, error) {
	return c.inner.Devices(ctx, f)
}

func (c *CachedStore) Funnel(ctx context.Context, f *Filter, steps []string) (*FunnelResult, error) {
	return c.inner.Funnel(ctx, f, steps)
}
