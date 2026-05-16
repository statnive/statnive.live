package storage

import (
	"context"
	"errors"
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

// wrapFiltered caches a rollup-backed endpoint under ResolveTTL rules.
// Every dashboard read (Overview, Sources, Pages, SEO, Campaigns, Trend)
// routes through here so the cache keying + TTL policy + type-assertion
// error path live in one place.
func wrapFiltered[T any](c *CachedStore, endpoint string, f *Filter, load func() (T, error)) (T, error) {
	var zero T

	v, err := c.cache.Wrap(
		endpoint+":"+f.Hash(),
		cache.ResolveTTL(c.now(), f.To),
		func() (any, error) { return load() },
	)
	if err != nil {
		return zero, err
	}

	out, ok := v.(T)
	if !ok {
		return zero, fmt.Errorf("cached_store: %s cache value has unexpected type %T", endpoint, v)
	}

	return out, nil
}

// Overview caches the headline KPI block under ResolveTTL rules.
func (c *CachedStore) Overview(ctx context.Context, f *Filter) (*OverviewResult, error) {
	return wrapFiltered(c, "overview", f, func() (*OverviewResult, error) { return c.inner.Overview(ctx, f) })
}

// Sources caches the channel-attribution rollup under ResolveTTL rules.
func (c *CachedStore) Sources(ctx context.Context, f *Filter) ([]SourceRow, error) {
	return wrapFiltered(c, "sources", f, func() ([]SourceRow, error) { return c.inner.Sources(ctx, f) })
}

// Pages caches the top-pages rollup under ResolveTTL rules.
func (c *CachedStore) Pages(ctx context.Context, f *Filter) ([]PageRow, error) {
	return wrapFiltered(c, "pages", f, func() ([]PageRow, error) { return c.inner.Pages(ctx, f) })
}

// SEO caches the SEO-landing rollup under ResolveTTL rules.
func (c *CachedStore) SEO(ctx context.Context, f *Filter) ([]SEORow, error) {
	return wrapFiltered(c, "seo", f, func() ([]SEORow, error) { return c.inner.SEO(ctx, f) })
}

// Campaigns caches the UTM-campaign rollup under ResolveTTL rules.
func (c *CachedStore) Campaigns(ctx context.Context, f *Filter) ([]CampaignRow, error) {
	return wrapFiltered(c, "campaigns", f, func() ([]CampaignRow, error) { return c.inner.Campaigns(ctx, f) })
}

// Trend caches the daily visitor series (Overview headline chart +
// SEO panel) under the same ResolveTTL rules as the other rollup-backed
// endpoints — current-hour buckets refresh every 10 s, today every
// 60 s, yesterday every hour, historical ~indefinitely.
func (c *CachedStore) Trend(ctx context.Context, f *Filter) ([]DailyPoint, error) {
	return wrapFiltered(c, "trend", f, func() ([]DailyPoint, error) { return c.inner.Trend(ctx, f) })
}

// Realtime is always cached at TTLRealtime (10s) regardless of clock —
// the underlying query reads the current hour bucket which doesn't
// have a Filter.To to inspect. The key partitions on (site_id, channel)
// because the dashboard chip toggles produce distinct results without
// changing the TTL semantics.
func (c *CachedStore) Realtime(ctx context.Context, f *Filter) (*RealtimeResult, error) {
	if f == nil {
		return nil, errors.New("cached_store: realtime requires non-nil filter")
	}

	key := fmt.Sprintf("realtime:%d:%s", f.SiteID, f.Channel)

	v, err := c.cache.Wrap(key, cache.TTLRealtime, func() (any, error) { return c.inner.Realtime(ctx, f) })
	if err != nil {
		return nil, err
	}

	out, ok := v.(*RealtimeResult)
	if !ok {
		return nil, fmt.Errorf("cached_store: realtime cache value has unexpected type %T", v)
	}

	return out, nil
}

// Geo / Devices / Funnel pass through to the inner Store, which
// returns ErrNotImplemented in v1. Caching the not-implemented error
// would be wasteful; the inner call is cheap.
func (c *CachedStore) Geo(ctx context.Context, f *Filter) ([]GeoRow, error) {
	return c.inner.Geo(ctx, f)
}

// Devices passes through to the inner Store (v1 stub).
func (c *CachedStore) Devices(ctx context.Context, f *Filter) ([]DeviceRow, error) {
	return c.inner.Devices(ctx, f)
}

// Funnel passes through to the inner Store (v1 stub).
func (c *CachedStore) Funnel(ctx context.Context, f *Filter, steps []string) (*FunnelResult, error) {
	return c.inner.Funnel(ctx, f, steps)
}
