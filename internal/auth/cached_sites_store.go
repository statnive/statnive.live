package auth

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CachedSitesStore wraps a SitesStore with a TTL-bounded in-memory cache
// over LoadUserSites — the hot path called by RequireDashboardSiteAccess
// on every dashboard request and by RequireSiteRole on every admin
// request. Without this wrapper, a 7K-EPS dashboard load issues 7K
// ClickHouse FINAL reads against statnive.user_sites every second; with
// it, steady-state load collapses to one CH read per (user, TTL window).
//
// Cache invalidation:
//
//   - Grant(userID, ...)  → invalidate userID (next read sees fresh state).
//   - Revoke(userID, ...) → invalidate userID (per-request-revoke contract
//     from internal/auth/user_sites_test.go:TestRequireSiteRole_PerRequestRevoke).
//   - TTL expiry          → next read re-fetches.
//
// Bound: ~steady_state_active_users entries since 60s TTL expires inactive
// entries quickly. No LRU eviction is needed at SaaS-tier traffic; if the
// box ever reaches >100k concurrent active users this gets revisited.
type CachedSitesStore struct {
	SitesStore // embed: ListUsersBySite passes through unchanged

	mu    sync.RWMutex
	items map[uuid.UUID]cachedGrants

	ttl time.Duration
	now func() time.Time
}

type cachedGrants struct {
	grants    map[uint32]Role
	expiresAt time.Time
}

// NewCachedSitesStore wraps an inner SitesStore with a TTL cache. Pass
// ttl <= 0 to default to 60s — the same TTL CachedStore uses for
// sessions. Mutations (Grant/Revoke) ALWAYS invalidate the affected
// user's entry; reads enjoy TTL-bounded staleness only.
func NewCachedSitesStore(inner SitesStore, ttl time.Duration) *CachedSitesStore {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}

	return &CachedSitesStore{
		SitesStore: inner,
		items:      make(map[uuid.UUID]cachedGrants),
		ttl:        ttl,
		now:        time.Now,
	}
}

// LoadUserSites short-circuits on cache hit; otherwise falls through to
// the inner store and primes the cache.
func (c *CachedSitesStore) LoadUserSites(
	ctx context.Context, userID uuid.UUID,
) (map[uint32]Role, error) {
	now := c.now()

	c.mu.RLock()
	cached, ok := c.items[userID]
	c.mu.RUnlock()

	if ok && now.Before(cached.expiresAt) {
		return cloneGrants(cached.grants), nil
	}

	grants, err := c.SitesStore.LoadUserSites(ctx, userID)
	if err != nil {
		c.mu.Lock()
		delete(c.items, userID)
		c.mu.Unlock()

		return nil, err
	}

	c.mu.Lock()
	c.items[userID] = cachedGrants{
		grants:    cloneGrants(grants),
		expiresAt: now.Add(c.ttl),
	}
	c.mu.Unlock()

	return grants, nil
}

// Grant writes through and invalidates the user's cached grants so the
// next LoadUserSites observes the new row immediately.
func (c *CachedSitesStore) Grant(
	ctx context.Context, userID uuid.UUID, siteID uint32, role Role,
) error {
	if err := c.SitesStore.Grant(ctx, userID, siteID, role); err != nil {
		return err
	}

	c.invalidate(userID)

	return nil
}

// Revoke writes through and invalidates. Combined with the cascade in
// CachedStore.RevokeSession, an admin-side revoke takes effect within
// one round-trip even if other dashboard tabs are holding cached
// sessions.
func (c *CachedSitesStore) Revoke(
	ctx context.Context, userID uuid.UUID, siteID uint32,
) error {
	if err := c.SitesStore.Revoke(ctx, userID, siteID); err != nil {
		return err
	}

	c.invalidate(userID)

	return nil
}

func (c *CachedSitesStore) invalidate(userID uuid.UUID) {
	c.mu.Lock()
	delete(c.items, userID)
	c.mu.Unlock()
}

// cloneGrants is the per-read defensive copy. Callers mutate the
// returned map (e.g. RequireDashboardSiteAccess attaches it to a scoped
// User); without this, two concurrent requests reading the same cached
// entry would share the same map and race on writes.
func cloneGrants(in map[uint32]Role) map[uint32]Role {
	out := make(map[uint32]Role, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}
