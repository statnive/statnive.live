package dashboard

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/storage"
)

// countingStore records how many times each method was invoked so the
// per-handler tests can assert "Store was never reached when the request
// was rejected at the choke point" — proves the 403 fired BEFORE any
// ClickHouse query.
type countingStore struct {
	overview  atomic.Int32
	sources   atomic.Int32
	pages     atomic.Int32
	seo       atomic.Int32
	trend     atomic.Int32
	campaigns atomic.Int32
	realtime  atomic.Int32
}

func (c *countingStore) Overview(_ context.Context, _ *storage.Filter) (*storage.OverviewResult, error) {
	c.overview.Add(1)

	return &storage.OverviewResult{}, nil
}

func (c *countingStore) Sources(_ context.Context, _ *storage.Filter) ([]storage.SourceRow, error) {
	c.sources.Add(1)

	return nil, nil
}

func (c *countingStore) Pages(_ context.Context, _ *storage.Filter) ([]storage.PageRow, error) {
	c.pages.Add(1)

	return nil, nil
}

func (c *countingStore) SEO(_ context.Context, _ *storage.Filter) ([]storage.SEORow, error) {
	c.seo.Add(1)

	return nil, nil
}

func (c *countingStore) Trend(_ context.Context, _ *storage.Filter) ([]storage.DailyPoint, error) {
	c.trend.Add(1)

	return nil, nil
}

func (c *countingStore) Campaigns(_ context.Context, _ *storage.Filter) ([]storage.CampaignRow, error) {
	c.campaigns.Add(1)

	return nil, nil
}

func (c *countingStore) Geo(_ context.Context, _ *storage.Filter) ([]storage.GeoRow, error) {
	return nil, storage.ErrNotImplemented
}

func (c *countingStore) Devices(_ context.Context, _ *storage.Filter) ([]storage.DeviceRow, error) {
	return nil, storage.ErrNotImplemented
}

func (c *countingStore) Funnel(_ context.Context, _ *storage.Filter, _ []string) (*storage.FunnelResult, error) {
	return nil, storage.ErrNotImplemented
}

func (c *countingStore) Realtime(_ context.Context, _ uint32) (*storage.RealtimeResult, error) {
	c.realtime.Add(1)

	return &storage.RealtimeResult{}, nil
}

func newSilentAudit(t *testing.T) *audit.Logger {
	t.Helper()

	lg, err := audit.New(t.TempDir() + "/audit.jsonl")
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}

	t.Cleanup(func() { _ = lg.Close() })

	return lg
}

func newSilentLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func newDeps(t *testing.T, store storage.Store) Deps {
	t.Helper()

	return Deps{
		Store:  store,
		Sites:  stubLister{tz: "UTC"},
		Audit:  newSilentAudit(t),
		Logger: newSilentLogger(),
	}
}

// authzWith returns a request with the given site_id stashed in
// ActiveSiteIDFromContext (the same shape the production middleware
// installs) and an actor whose Sites grants match.
func authzWith(method, target string, actor *auth.User, activeSite uint32) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	ctx := auth.WithSession(r.Context(), actor, &auth.Session{})

	if activeSite > 0 {
		ctx = auth.WithActiveSiteID(ctx, activeSite)
	}

	return r.WithContext(ctx)
}

// actorOnSites builds a User with the given role granted on each siteID.
func actorOnSites(role auth.Role, siteIDs ...uint32) *auth.User {
	u := &auth.User{UserID: uuid.New(), Role: role}
	u.Sites = make(map[uint32]auth.Role, len(siteIDs))

	for _, id := range siteIDs {
		u.Sites[id] = role
	}

	return u
}

// TestDashboardHandlers_AllowGrantedSite — happy path for every
// site-scoped handler; proves the canonical context flow works.
func TestDashboardHandlers_AllowGrantedSite(t *testing.T) {
	t.Parallel()

	store := &countingStore{}
	deps := newDeps(t, store)

	cases := []struct {
		name    string
		path    string
		handler http.HandlerFunc
	}{
		{"overview", "/api/stats/overview?site=4", overviewHandler(deps)},
		{"sources", "/api/stats/sources?site=4", sourcesHandler(deps)},
		{"pages", "/api/stats/pages?site=4", pagesHandler(deps)},
		{"seo", "/api/stats/seo?site=4", seoHandler(deps)},
		{"trend", "/api/stats/trend?site=4", trendHandler(deps)},
		{"campaigns", "/api/stats/campaigns?site=4", campaignsHandler(deps)},
		{"realtime", "/api/realtime/visitors?site=4", realtimeHandler(deps)},
	}

	actor := actorOnSites(auth.RoleAdmin, 4)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := authzWith(http.MethodGet, tc.path, actor, 4)
			w := httptest.NewRecorder()
			tc.handler(w, r)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200; body = %q", w.Code, w.Body.String())
			}
		})
	}
}

// TestDashboardHandlers_403_OnUnauthorizedSite is the regression matrix.
// Actor with admin on site 4 hits ?site=5 directly via filterFromRequest
// (no middleware mounted). The belt-and-braces guard MUST return 403 +
// EventDashboardForbidden, and the Store MUST NOT be reached.
func TestDashboardHandlers_403_OnUnauthorizedSite(t *testing.T) {
	t.Parallel()

	store := &countingStore{}
	deps := newDeps(t, store)

	cases := []struct {
		name    string
		path    string
		handler http.HandlerFunc
		check   func(t *testing.T)
	}{
		{
			"overview", "/api/stats/overview?site=5", overviewHandler(deps),
			func(t *testing.T) {
				t.Helper()

				if store.overview.Load() != 0 {
					t.Errorf("Store.Overview called despite 403")
				}
			},
		},
		{
			"sources", "/api/stats/sources?site=5", sourcesHandler(deps),
			func(t *testing.T) {
				t.Helper()

				if store.sources.Load() != 0 {
					t.Errorf("Store.Sources called despite 403")
				}
			},
		},
		{
			"pages", "/api/stats/pages?site=5", pagesHandler(deps),
			func(t *testing.T) {
				t.Helper()

				if store.pages.Load() != 0 {
					t.Errorf("Store.Pages called despite 403")
				}
			},
		},
		{
			"seo", "/api/stats/seo?site=5", seoHandler(deps),
			func(t *testing.T) {
				t.Helper()

				if store.seo.Load() != 0 {
					t.Errorf("Store.SEO called despite 403")
				}
			},
		},
		{
			"trend", "/api/stats/trend?site=5", trendHandler(deps),
			func(t *testing.T) {
				t.Helper()

				if store.trend.Load() != 0 {
					t.Errorf("Store.Trend called despite 403")
				}
			},
		},
		{
			"campaigns", "/api/stats/campaigns?site=5", campaignsHandler(deps),
			func(t *testing.T) {
				t.Helper()

				if store.campaigns.Load() != 0 {
					t.Errorf("Store.Campaigns called despite 403")
				}
			},
		},
		{
			"realtime", "/api/realtime/visitors?site=5", realtimeHandler(deps),
			func(t *testing.T) {
				t.Helper()

				if store.realtime.Load() != 0 {
					t.Errorf("Store.Realtime called despite 403")
				}
			},
		},
	}

	actor := actorOnSites(auth.RoleAdmin, 4)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// authzWith with activeSite=0 means we DO NOT inject the
			// post-middleware context — handler must fall through to the
			// belt-and-braces check using actor.CanAccessSite.
			r := authzWith(http.MethodGet, tc.path, actor, 0)
			w := httptest.NewRecorder()
			tc.handler(w, r)

			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403; body = %q", w.Code, w.Body.String())
			}

			body := strings.TrimSpace(w.Body.String())
			if !strings.Contains(body, "forbidden") {
				t.Errorf("body = %q, want 'forbidden' uniform shape", body)
			}

			tc.check(t)
		})
	}
}

// TestDashboardHandlers_403_OnViewerCrossSite — viewer role on site 4
// must 403 on site 5 same as admin (role doesn't matter for cross-tenant).
func TestDashboardHandlers_403_OnViewerCrossSite(t *testing.T) {
	t.Parallel()

	store := &countingStore{}
	deps := newDeps(t, store)

	actor := actorOnSites(auth.RoleViewer, 4)
	r := authzWith(http.MethodGet, "/api/stats/overview?site=5", actor, 0)
	w := httptest.NewRecorder()

	overviewHandler(deps)(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("viewer cross-site status = %d, want 403", w.Code)
	}

	if store.overview.Load() != 0 {
		t.Errorf("Store.Overview called for viewer cross-site request")
	}
}

// TestDashboardHandlers_403_OnAPITokenCrossSite — api token bound to
// site 4 must 403 on site 5; token capability never escalates.
func TestDashboardHandlers_403_OnAPITokenCrossSite(t *testing.T) {
	t.Parallel()

	store := &countingStore{}
	deps := newDeps(t, store)

	// API token actor: UserID = uuid.Nil, SiteID bound to scope.
	actor := &auth.User{UserID: uuid.Nil, SiteID: 4, Role: auth.RoleAPI}
	r := authzWith(http.MethodGet, "/api/stats/overview?site=5", actor, 0)
	w := httptest.NewRecorder()

	overviewHandler(deps)(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("api-token cross-site status = %d, want 403", w.Code)
	}

	if store.overview.Load() != 0 {
		t.Errorf("Store.Overview called for api-token cross-site request")
	}
}

// TestDashboardHandlers_403_OnAPITokenCrossSite_SitesEndpoint — proves
// the /api/sites enumeration leak is closed: an api token bound to one
// site sees only that site.
func TestDashboardHandlers_APITokenSeesOnlyBoundSite(t *testing.T) {
	t.Parallel()

	deps := newDeps(t, &countingStore{})
	deps.Sites = manySitesLister{ids: []uint32{1, 4, 5, 7}}

	actor := &auth.User{UserID: uuid.Nil, SiteID: 4, Role: auth.RoleAPI}
	r := authzWith(http.MethodGet, "/api/sites", actor, 0)
	w := httptest.NewRecorder()

	sitesHandler(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var env sitesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(env.Sites) != 1 || env.Sites[0].ID != 4 {
		t.Errorf("Sites = %+v, want exactly [{id:4}]", env.Sites)
	}
}
