package sites

// In-package test so SiteAdmin values can be constructed directly and
// Rebuild can be exercised against an in-memory fake (no *Registry /
// no ClickHouse).

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type fakeLister struct {
	mu    sync.Mutex
	sites []SiteAdmin
	err   error
}

func (f *fakeLister) ListAdmin(_ context.Context) ([]SiteAdmin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return nil, f.err
	}

	out := make([]SiteAdmin, len(f.sites))
	copy(out, f.sites)

	return out, nil
}

func (f *fakeLister) set(sa []SiteAdmin) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.sites = sa
}

func TestOriginIndex_LookupReturnsZeroOnEmpty(t *testing.T) {
	t.Parallel()

	idx := NewOriginIndex()

	if id, ok := idx.Lookup("https://anywhere.com"); ok || id != 0 {
		t.Fatalf("empty index returned (%d, %v); want (0, false)", id, ok)
	}
}

func TestOriginIndex_Rebuild_PopulatesFromEnabledSites(t *testing.T) {
	t.Parallel()

	lister := &fakeLister{sites: []SiteAdmin{
		{
			Site:       Site{ID: 1, Enabled: true},
			SitePolicy: SitePolicy{AllowedOrigins: []string{"https://televika.com", "https://www.televika.com"}},
		},
		{
			Site:       Site{ID: 2, Enabled: true},
			SitePolicy: SitePolicy{AllowedOrigins: []string{"https://other.example"}},
		},
		{
			// Disabled — entries must NOT appear in the index even
			// though stored in the DB. Operator who disables a site
			// expects every CORS path to instantly stop accepting
			// requests from that origin.
			Site:       Site{ID: 3, Enabled: false},
			SitePolicy: SitePolicy{AllowedOrigins: []string{"https://disabled.example"}},
		},
	}}

	idx := NewOriginIndex()

	n, err := idx.Rebuild(context.Background(), lister)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if n != 3 {
		t.Fatalf("Rebuild returned %d entries; want 3", n)
	}

	if id, ok := idx.Lookup("https://televika.com"); !ok || id != 1 {
		t.Errorf("televika lookup = (%d, %v); want (1, true)", id, ok)
	}

	if id, ok := idx.Lookup("https://www.televika.com"); !ok || id != 1 {
		t.Errorf("www.televika lookup = (%d, %v); want (1, true)", id, ok)
	}

	if id, ok := idx.Lookup("https://other.example"); !ok || id != 2 {
		t.Errorf("other.example lookup = (%d, %v); want (2, true)", id, ok)
	}

	if id, ok := idx.Lookup("https://disabled.example"); ok || id != 0 {
		t.Errorf("disabled site entry leaked: (%d, %v); want (0, false)", id, ok)
	}
}

func TestOriginIndex_Rebuild_CanonicalisesLookup(t *testing.T) {
	t.Parallel()

	lister := &fakeLister{sites: []SiteAdmin{
		{
			Site:       Site{ID: 1, Enabled: true},
			SitePolicy: SitePolicy{AllowedOrigins: []string{"https://Televika.COM"}},
		},
	}}

	idx := NewOriginIndex()

	if _, err := idx.Rebuild(context.Background(), lister); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// Lookup against the canonical form succeeds even when the stored
	// entry was uppercased — NormalizeOrigin runs on both sides.
	for _, probe := range []string{
		"https://televika.com",
		"https://Televika.com",
		"https://televika.com/",
	} {
		if id, ok := idx.Lookup(probe); !ok || id != 1 {
			t.Errorf("Lookup(%q) = (%d, %v); want (1, true)", probe, id, ok)
		}
	}
}

func TestOriginIndex_Lookup_RejectsNull(t *testing.T) {
	t.Parallel()

	lister := &fakeLister{sites: []SiteAdmin{
		{
			Site:       Site{ID: 1, Enabled: true},
			SitePolicy: SitePolicy{AllowedOrigins: []string{"https://televika.com"}},
		},
	}}

	idx := NewOriginIndex()
	_, _ = idx.Rebuild(context.Background(), lister)

	if id, ok := idx.Lookup("null"); ok || id != 0 {
		t.Errorf("Lookup(\"null\") = (%d, %v); want (0, false)", id, ok)
	}

	if id, ok := idx.Lookup(""); ok || id != 0 {
		t.Errorf("Lookup(\"\") = (%d, %v); want (0, false)", id, ok)
	}
}

func TestOriginIndex_Rebuild_ErrorBubbles(t *testing.T) {
	t.Parallel()

	lister := &fakeLister{err: errors.New("clickhouse: connection refused")}

	idx := NewOriginIndex()

	if _, err := idx.Rebuild(context.Background(), lister); err == nil {
		t.Fatal("expected error from failing ListAdmin")
	}

	// On failed rebuild the index must not have been swapped — Lookup
	// against the empty initial map should still resolve nothing,
	// rather than panic on a nil pointer.
	if id, ok := idx.Lookup("https://anywhere.com"); ok || id != 0 {
		t.Errorf("Lookup after failed rebuild leaked: (%d, %v)", id, ok)
	}
}

// TestOriginIndex_HotSwapNoTear runs concurrent readers vs writers
// under -race so any regression to a non-atomic swap is caught.
func TestOriginIndex_HotSwapNoTear(t *testing.T) {
	t.Parallel()

	lister := &fakeLister{sites: []SiteAdmin{
		{Site: Site{ID: 1, Enabled: true}, SitePolicy: SitePolicy{AllowedOrigins: []string{"https://a.example"}}},
	}}

	idx := NewOriginIndex()
	_, _ = idx.Rebuild(context.Background(), lister)

	var (
		reads     atomic.Int64
		stop      atomic.Bool
		readersWG sync.WaitGroup
		writersWG sync.WaitGroup
	)

	for range 8 {
		readersWG.Add(1)

		go func() {
			defer readersWG.Done()

			for !stop.Load() {
				idx.Lookup("https://a.example")
				idx.Lookup("https://b.example")
				idx.Lookup("https://c.example")

				reads.Add(1)
			}
		}()
	}

	for w := range 2 {
		writersWG.Add(1)

		go func(seed int) {
			defer writersWG.Done()

			for i := range 200 {
				origins := []string{"https://a.example"}
				if (i+seed)%2 == 0 {
					origins = append(origins, "https://b.example")
				}

				if (i+seed)%3 == 0 {
					origins = append(origins, "https://c.example")
				}

				lister.set([]SiteAdmin{
					{Site: Site{ID: 1, Enabled: true}, SitePolicy: SitePolicy{AllowedOrigins: origins}},
				})

				_, _ = idx.Rebuild(context.Background(), lister)
			}
		}(w)
	}

	writersWG.Wait()
	stop.Store(true)
	readersWG.Wait()

	if reads.Load() == 0 {
		t.Error("readers did zero lookups")
	}
}

// TestOriginIndex_Lookup_WwwToBareFallback — tenant allowlists the bare
// origin; browser sends the www. variant (CF bare→www redirect, common
// CDN topology). Lookup must resolve via the www.→bare retry.
func TestOriginIndex_Lookup_WwwToBareFallback(t *testing.T) {
	t.Parallel()

	lister := &fakeLister{sites: []SiteAdmin{
		{
			Site:       Site{ID: 4, Enabled: true},
			SitePolicy: SitePolicy{AllowedOrigins: []string{"https://televika.com"}},
		},
	}}

	idx := NewOriginIndex()
	if _, err := idx.Rebuild(context.Background(), lister); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if id, ok := idx.Lookup("https://www.televika.com"); !ok || id != 4 {
		t.Errorf("www.televika lookup with bare-only allowlist = (%d, %v); want (4, true)", id, ok)
	}
}

// TestOriginIndex_Lookup_BareToWwwFallback — symmetric direction: tenant
// allowlists the www. origin; browser sends the bare variant (tenant
// redirects www→bare; CDN strips). Lookup must resolve via the bare→www. retry.
func TestOriginIndex_Lookup_BareToWwwFallback(t *testing.T) {
	t.Parallel()

	lister := &fakeLister{sites: []SiteAdmin{
		{
			Site:       Site{ID: 4, Enabled: true},
			SitePolicy: SitePolicy{AllowedOrigins: []string{"https://www.televika.com"}},
		},
	}}

	idx := NewOriginIndex()
	if _, err := idx.Rebuild(context.Background(), lister); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if id, ok := idx.Lookup("https://televika.com"); !ok || id != 4 {
		t.Errorf("bare televika lookup with www-only allowlist = (%d, %v); want (4, true)", id, ok)
	}
}

// TestOriginIndex_Lookup_PortPreservedThroughFallback — explicit non-default
// ports must round-trip through the www. toggle. Otherwise an admin who
// allowlists https://staging.foo.com:8443 would be matched on bare 443 too,
// which is wrong (different origin per RFC 6454).
func TestOriginIndex_Lookup_PortPreservedThroughFallback(t *testing.T) {
	t.Parallel()

	lister := &fakeLister{sites: []SiteAdmin{
		{
			Site:       Site{ID: 7, Enabled: true},
			SitePolicy: SitePolicy{AllowedOrigins: []string{"https://staging.foo.com:8443"}},
		},
	}}

	idx := NewOriginIndex()
	if _, err := idx.Rebuild(context.Background(), lister); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// www.+port should resolve via fallback toggle that preserves :8443.
	if id, ok := idx.Lookup("https://www.staging.foo.com:8443"); !ok || id != 7 {
		t.Errorf("www.+port lookup = (%d, %v); want (7, true)", id, ok)
	}

	// www. without port MUST NOT resolve — different origin per RFC 6454.
	if id, ok := idx.Lookup("https://www.staging.foo.com"); ok {
		t.Errorf("portless lookup must NOT cross :8443 origin boundary; got (%d, true)", id)
	}
}

// TestOriginIndex_Lookup_ExplicitVariantStillWins — when both bare and www.
// are allowlisted, the literal match must win (avoids unnecessary toggle
// work AND keeps the exact-match-wins semantic visible to operators who
// register both intentionally).
func TestOriginIndex_Lookup_ExplicitVariantStillWins(t *testing.T) {
	t.Parallel()

	lister := &fakeLister{sites: []SiteAdmin{
		{
			Site:       Site{ID: 4, Enabled: true},
			SitePolicy: SitePolicy{AllowedOrigins: []string{"https://televika.com", "https://www.televika.com"}},
		},
	}}

	idx := NewOriginIndex()
	if _, err := idx.Rebuild(context.Background(), lister); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if id, ok := idx.Lookup("https://televika.com"); !ok || id != 4 {
		t.Errorf("bare lookup = (%d, %v); want (4, true)", id, ok)
	}

	if id, ok := idx.Lookup("https://www.televika.com"); !ok || id != 4 {
		t.Errorf("www lookup = (%d, %v); want (4, true)", id, ok)
	}
}

// TestOriginIndex_Lookup_WwwFallbackNeverWidensAcrossSites — admin A
// allowlists https://foo.com (site=1); admin B allowlists
// https://www.bar.com (site=2). A lookup for https://www.foo.com must
// resolve to site=1 (via fallback), NOT cross to site=2; lookup for
// https://bar.com must resolve to site=2, NOT cross to site=1. The
// fallback is per-toggle, not a global www-collapse.
func TestOriginIndex_Lookup_WwwFallbackNeverWidensAcrossSites(t *testing.T) {
	t.Parallel()

	lister := &fakeLister{sites: []SiteAdmin{
		{Site: Site{ID: 1, Enabled: true}, SitePolicy: SitePolicy{AllowedOrigins: []string{"https://foo.com"}}},
		{Site: Site{ID: 2, Enabled: true}, SitePolicy: SitePolicy{AllowedOrigins: []string{"https://www.bar.com"}}},
	}}

	idx := NewOriginIndex()
	if _, err := idx.Rebuild(context.Background(), lister); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if id, ok := idx.Lookup("https://www.foo.com"); !ok || id != 1 {
		t.Errorf("www.foo.com via fallback = (%d, %v); want (1, true)", id, ok)
	}

	if id, ok := idx.Lookup("https://bar.com"); !ok || id != 2 {
		t.Errorf("bar.com via fallback = (%d, %v); want (2, true)", id, ok)
	}

	// Unrelated host must still miss — fallback is a www toggle, not a
	// general subdomain collapse.
	if id, ok := idx.Lookup("https://api.foo.com"); ok {
		t.Errorf("api.foo.com must NOT resolve via www toggle; got (%d, true)", id)
	}
}

// TestOriginIndex_Lookup_DisabledSiteNoFallback — when the only matching
// site is disabled, neither the literal nor the www.-toggled lookup
// resolves. Disabling a site must instantly stop accepting BOTH variants.
func TestOriginIndex_Lookup_DisabledSiteNoFallback(t *testing.T) {
	t.Parallel()

	lister := &fakeLister{sites: []SiteAdmin{
		{
			Site:       Site{ID: 9, Enabled: false},
			SitePolicy: SitePolicy{AllowedOrigins: []string{"https://disabled.example"}},
		},
	}}

	idx := NewOriginIndex()
	if _, err := idx.Rebuild(context.Background(), lister); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	for _, probe := range []string{"https://disabled.example", "https://www.disabled.example"} {
		if id, ok := idx.Lookup(probe); ok {
			t.Errorf("disabled-site lookup leaked %s = (%d, true)", probe, id)
		}
	}
}

func TestOriginIndex_HasSelfHostInAnyAllowlist(t *testing.T) {
	t.Parallel()

	lister := &fakeLister{sites: []SiteAdmin{
		{
			Site:       Site{ID: 7, Enabled: true},
			SitePolicy: SitePolicy{AllowedOrigins: []string{"https://televika.com", "https://app.statnive.live"}},
		},
	}}

	idx := NewOriginIndex()
	if _, err := idx.Rebuild(context.Background(), lister); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	if id := idx.HasSelfHostInAnyAllowlist("app.statnive.live"); id != 7 {
		t.Errorf("self-host scan returned site_id %d; want 7", id)
	}

	if id := idx.HasSelfHostInAnyAllowlist("untouched.example"); id != 0 {
		t.Errorf("clean scan returned %d; want 0", id)
	}

	if id := idx.HasSelfHostInAnyAllowlist(""); id != 0 {
		t.Errorf("empty-self scan returned %d; want 0", id)
	}
}
