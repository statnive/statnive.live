// Package cache wraps hashicorp/golang-lru/v2/expirable with the
// dashboard-specific TTL policy. Per CLAUDE.md license rules,
// hashicorp/golang-lru is MPL-2.0 (weak copyleft) — used unmodified;
// if a feature is missing we fork to a separate repo, never inline.
package cache

import (
	"hash/fnv"
	"sync"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
)

// Cache is a thread-safe LRU with per-entry TTL. Keyed by string for
// dashboard query cache keys (endpoint + Filter.Hash); value is `any`
// so one cache can hold every endpoint's typed result without a
// per-endpoint wrapper.
//
// The underlying expirable.LRU only honors a single constructor-time
// TTL, so per-entry TTL is enforced in Get() against an expiresAt
// timestamp stored alongside the value. The LRU's own TTL acts as a
// hard ceiling (TTLHistorical = 1 year) and capacity is the eviction
// pressure for hot tenants.
type Cache struct {
	inner *expirable.LRU[string, entry]
	now   func() time.Time // overridable for tests
}

type entry struct {
	value     any
	expiresAt time.Time // zero == never expires
}

// New constructs a Cache with the given capacity.
func New(capacity int) *Cache {
	if capacity <= 0 {
		capacity = 256
	}

	return &Cache{
		inner: expirable.NewLRU[string, entry](capacity, nil, TTLHistorical),
		now:   time.Now,
	}
}

// Get returns the cached value for k. Returns ok=false on miss OR on
// expired entry — the latter delegates to Wrap to re-run the loader.
func (c *Cache) Get(k string) (any, bool) {
	e, ok := c.inner.Get(k)
	if !ok {
		return nil, false
	}

	if !e.expiresAt.IsZero() && c.now().After(e.expiresAt) {
		return nil, false
	}

	return e.value, true
}

// Set stores v under k with a per-entry TTL. ttl <= 0 means "never
// expire" (still subject to LRU eviction on capacity).
func (c *Cache) Set(k string, v any, ttl time.Duration) {
	var exp time.Time
	if ttl > 0 {
		exp = c.now().Add(ttl)
	}

	c.inner.Add(k, entry{value: v, expiresAt: exp})
}

// Wrap is the cache-aside primitive: return the cached value if any,
// otherwise call loader, store, return. ttl bounds the staleness of
// the new entry — we re-cache on every miss, so the effective TTL is
// the time since the last miss.
//
// loader is invoked at most once per concurrent miss for the same key
// thanks to the per-key mutex; downstream ClickHouse never sees a
// thundering herd from N tabs all refreshing the same panel.
func (c *Cache) Wrap(k string, ttl time.Duration, loader func() (any, error)) (any, error) {
	if v, ok := c.Get(k); ok {
		return v, nil
	}

	mu := keyLock(k)

	mu.Lock()
	defer mu.Unlock()

	// Re-check after acquiring the lock: a concurrent caller may have
	// loaded the value while we waited.
	if v, ok := c.Get(k); ok {
		return v, nil
	}

	v, err := loader()
	if err != nil {
		return nil, err
	}

	c.Set(k, v, ttl)

	return v, nil
}

// Len returns the current number of cached entries. Useful for
// /healthz dashboards + tests.
func (c *Cache) Len() int { return c.inner.Len() }

// Purge removes every entry. Used by /api/admin/cache/purge in v2 + by
// tests that want a clean slate between cases.
func (c *Cache) Purge() { c.inner.Purge() }

// SetClock replaces the wall-clock source. Test-only.
func (c *Cache) SetClock(now func() time.Time) { c.now = now }

// keyLock returns a sync.Mutex per cache key. We use a sharded map so
// concurrent Wraps on different keys don't serialize. 256 shards keeps
// the contention probability under 1% for a few thousand active keys.
func keyLock(k string) *sync.Mutex {
	shard := keyShards[fnv32(k)%uint32(len(keyShards))]
	shard.mu.Lock()

	if shard.locks == nil {
		shard.locks = make(map[string]*sync.Mutex)
	}

	mu, ok := shard.locks[k]
	if !ok {
		mu = &sync.Mutex{}
		shard.locks[k] = mu
	}

	shard.mu.Unlock()

	return mu
}

type keyShard struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

var keyShards = [256]*keyShard{}

func init() {
	for i := range keyShards {
		keyShards[i] = &keyShard{}
	}
}

// fnv32 returns FNV-1a of s for shard selection. Uniform distribution
// across 256 shards; not crypto-grade.
func fnv32(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))

	return h.Sum32()
}
