package auth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// countingSitesStore wraps an in-memory grants map and tracks how many
// times LoadUserSites is called. Used to assert the cache absorbs
// repeated reads.
type countingSitesStore struct {
	grants map[uuid.UUID]map[uint32]Role
	loads  atomic.Int32
	err    error
}

func (c *countingSitesStore) LoadUserSites(_ context.Context, userID uuid.UUID) (map[uint32]Role, error) {
	c.loads.Add(1)

	if c.err != nil {
		return nil, c.err
	}

	g := c.grants[userID]
	if g == nil {
		return map[uint32]Role{}, nil
	}

	out := make(map[uint32]Role, len(g))
	for k, v := range g {
		out[k] = v
	}

	return out, nil
}

func (c *countingSitesStore) Grant(_ context.Context, userID uuid.UUID, siteID uint32, role Role) error {
	if c.grants == nil {
		c.grants = map[uuid.UUID]map[uint32]Role{}
	}

	if c.grants[userID] == nil {
		c.grants[userID] = map[uint32]Role{}
	}

	c.grants[userID][siteID] = role

	return nil
}

func (c *countingSitesStore) Revoke(_ context.Context, userID uuid.UUID, siteID uint32) error {
	delete(c.grants[userID], siteID)

	return nil
}

func (c *countingSitesStore) ListUsersBySite(_ context.Context, _ uint32) ([]UserSiteGrant, error) {
	return nil, nil
}

func TestCachedSitesStore_HitsCacheOnRepeatedRead(t *testing.T) {
	t.Parallel()

	inner := &countingSitesStore{
		grants: map[uuid.UUID]map[uint32]Role{
			{}: {1: RoleAdmin},
		},
	}
	userID := uuid.New()
	_ = inner.Grant(context.Background(), userID, 4, RoleViewer)

	cache := NewCachedSitesStore(inner, 0)

	for i := 0; i < 10; i++ {
		got, err := cache.LoadUserSites(context.Background(), userID)
		if err != nil {
			t.Fatalf("LoadUserSites: %v", err)
		}

		if got[4] != RoleViewer {
			t.Fatalf("iteration %d: grants[4] = %q, want viewer", i, got[4])
		}
	}

	if loads := inner.loads.Load(); loads != 1 {
		t.Errorf("inner load count = %d, want 1 (cache hits the rest)", loads)
	}
}

func TestCachedSitesStore_TTLExpiry(t *testing.T) {
	t.Parallel()

	inner := &countingSitesStore{}
	userID := uuid.New()
	_ = inner.Grant(context.Background(), userID, 1, RoleAdmin)

	cache := NewCachedSitesStore(inner, 100*time.Millisecond)

	if _, err := cache.LoadUserSites(context.Background(), userID); err != nil {
		t.Fatalf("first load: %v", err)
	}

	if _, err := cache.LoadUserSites(context.Background(), userID); err != nil {
		t.Fatalf("warm load: %v", err)
	}

	if got := inner.loads.Load(); got != 1 {
		t.Fatalf("warm load count = %d, want 1", got)
	}

	// Advance the cache's clock past the TTL using its injectable now.
	cache.now = func() time.Time { return time.Now().Add(200 * time.Millisecond) }

	if _, err := cache.LoadUserSites(context.Background(), userID); err != nil {
		t.Fatalf("post-TTL load: %v", err)
	}

	if got := inner.loads.Load(); got != 2 {
		t.Errorf("post-TTL load count = %d, want 2 (TTL expired → refetch)", got)
	}
}

func TestCachedSitesStore_GrantInvalidates(t *testing.T) {
	t.Parallel()

	inner := &countingSitesStore{}
	userID := uuid.New()
	_ = inner.Grant(context.Background(), userID, 1, RoleAdmin)

	cache := NewCachedSitesStore(inner, time.Hour)

	if _, err := cache.LoadUserSites(context.Background(), userID); err != nil {
		t.Fatalf("first load: %v", err)
	}

	if err := cache.Grant(context.Background(), userID, 2, RoleViewer); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	got, err := cache.LoadUserSites(context.Background(), userID)
	if err != nil {
		t.Fatalf("post-grant load: %v", err)
	}

	if got[2] != RoleViewer {
		t.Errorf("post-grant grants[2] = %q, want viewer (cache stale)", got[2])
	}

	if inner.loads.Load() != 2 {
		t.Errorf("inner loads = %d, want 2 (Grant must invalidate cache)", inner.loads.Load())
	}
}

func TestCachedSitesStore_RevokeInvalidates(t *testing.T) {
	t.Parallel()

	inner := &countingSitesStore{}
	userID := uuid.New()
	_ = inner.Grant(context.Background(), userID, 1, RoleAdmin)
	_ = inner.Grant(context.Background(), userID, 2, RoleViewer)

	cache := NewCachedSitesStore(inner, time.Hour)

	first, err := cache.LoadUserSites(context.Background(), userID)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}

	if _, ok := first[2]; !ok {
		t.Fatalf("expected grant on site 2 in first read")
	}

	if err := cache.Revoke(context.Background(), userID, 2); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	post, err := cache.LoadUserSites(context.Background(), userID)
	if err != nil {
		t.Fatalf("post-revoke load: %v", err)
	}

	if _, ok := post[2]; ok {
		t.Errorf("post-revoke still sees grant on site 2 — cache not invalidated: %+v", post)
	}
}

func TestCachedSitesStore_PropagatesError_AndFlushesStale(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	inner := &countingSitesStore{}
	_ = inner.Grant(context.Background(), userID, 1, RoleAdmin)

	cache := NewCachedSitesStore(inner, time.Hour)
	cache.now = time.Now

	if _, err := cache.LoadUserSites(context.Background(), userID); err != nil {
		t.Fatalf("first load: %v", err)
	}

	// Force the next inner load to fail; verify the stale entry is
	// dropped so callers don't read poisoned cached grants.
	inner.err = errors.New("ch unavailable")

	// Bump clock past TTL so the cache re-fetches and observes the error.
	cache.now = func() time.Time { return time.Now().Add(2 * time.Hour) }

	_, err := cache.LoadUserSites(context.Background(), userID)
	if err == nil {
		t.Fatalf("expected error propagation, got nil")
	}

	cache.mu.RLock()
	_, present := cache.items[userID]
	cache.mu.RUnlock()

	if present {
		t.Errorf("stale cache entry retained after inner error — must be flushed")
	}
}

func TestCachedSitesStore_ReturnedMapIsIsolated(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	inner := &countingSitesStore{}
	_ = inner.Grant(context.Background(), userID, 1, RoleAdmin)

	cache := NewCachedSitesStore(inner, time.Hour)

	first, err := cache.LoadUserSites(context.Background(), userID)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}

	// Mutate the returned map; a second read should still see the
	// original cached state.
	first[99] = RoleAdmin

	second, err := cache.LoadUserSites(context.Background(), userID)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}

	if _, leaked := second[99]; leaked {
		t.Errorf("caller mutation leaked into cache: %+v", second)
	}
}
