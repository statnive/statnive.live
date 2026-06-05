package auth

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CachedAPITokenStore fronts LookupActive — the only per-request hot path —
// with a TTL-bounded cache, mirroring CachedSitesStore. Without it, a
// token-authed MCP load issues one ClickHouse read per request; with it,
// steady state collapses to one read per (token, TTL window).
//
// Correctness over the cache:
//   - Revoke flushes the cache so a revoked token can never be served from a
//     stale entry — immediate-revoke. Revokes are rare (a dashboard action)
//     and the cache is tiny (bounded by the per-user active-token cap), so a
//     full flush is cheaper than maintaining a reverse index.
//   - Expiry is re-checked on every cache hit against the token's own
//     ExpiresAt, so an expired token is never served from cache even inside
//     the TTL window.
//   - Negative results (unknown hash) are cached too, so a flood of bad
//     bearer tokens can't hammer ClickHouse.
//
// Create/List/Count pass through (not hot-path; dashboard-rate).
type CachedAPITokenStore struct {
	APITokenStore // embed: Create/ListForUser/CountActiveForUser pass through

	mu    sync.RWMutex
	items map[string]cachedToken // hashHex → result

	ttl time.Duration
	now func() time.Time
}

type cachedToken struct {
	token     MintedToken
	found     bool
	expiresAt time.Time // cache-entry TTL, not the token's own expiry
}

// NewCachedAPITokenStore wraps inner with a TTL cache. ttl <= 0 defaults to
// 60s (the same window CachedStore/CachedSitesStore use).
func NewCachedAPITokenStore(inner APITokenStore, ttl time.Duration) *CachedAPITokenStore {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}

	return &CachedAPITokenStore{
		APITokenStore: inner,
		items:         make(map[string]cachedToken),
		ttl:           ttl,
		now:           time.Now,
	}
}

// LookupActive serves from cache when fresh; otherwise falls through and
// primes. A cache hit whose token has expired by its own ExpiresAt is
// treated as a miss (and dropped) so expiry is effectively immediate.
func (c *CachedAPITokenStore) LookupActive(
	ctx context.Context, hashHex string,
) (MintedToken, bool, error) {
	now := c.now()

	c.mu.RLock()
	entry, ok := c.items[hashHex]
	c.mu.RUnlock()

	if ok && now.Before(entry.expiresAt) {
		if !entry.found {
			return MintedToken{}, false, nil
		}

		if entry.token.ExpiresAt == 0 || entry.token.ExpiresAt > now.Unix() {
			return entry.token, true, nil
		}
		// Token expired within the cache window — drop and re-resolve.
		c.drop(hashHex)
	}

	token, found, err := c.APITokenStore.LookupActive(ctx, hashHex)
	if err != nil {
		return MintedToken{}, false, err
	}

	c.mu.Lock()
	c.items[hashHex] = cachedToken{token: token, found: found, expiresAt: now.Add(c.ttl)}
	c.mu.Unlock()

	return token, found, nil
}

// Revoke writes through, then flushes the cache so the revocation is observed
// on the very next LookupActive (immediate-revoke).
func (c *CachedAPITokenStore) Revoke(
	ctx context.Context, tokenID, userID uuid.UUID,
) error {
	if err := c.APITokenStore.Revoke(ctx, tokenID, userID); err != nil {
		return err
	}

	c.mu.Lock()
	c.items = make(map[string]cachedToken)
	c.mu.Unlock()

	return nil
}

func (c *CachedAPITokenStore) drop(hashHex string) {
	c.mu.Lock()
	delete(c.items, hashHex)
	c.mu.Unlock()
}
