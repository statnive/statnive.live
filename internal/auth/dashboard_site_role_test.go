package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/audit"
)

// seedGrants builds a fakeSitesStore (defined in user_sites_test.go)
// pre-populated with the given (userID → site → role) grants. Saves the
// test caller from a Grant-loop boilerplate.
func seedGrants(grants map[uuid.UUID]map[uint32]Role) *fakeSitesStore {
	s := newFakeSitesStore()

	for userID, sites := range grants {
		for siteID, role := range sites {
			_ = s.Grant(context.Background(), userID, siteID, role)
		}
	}

	return s
}

func newSilentAudit(t *testing.T) *audit.Logger {
	t.Helper()

	// audit.Logger requires a path; use a t.TempDir() file so the test is
	// hermetic. Behavioural assertions on emit happen in errors_test.go
	// where we read the JSONL back; here we only care the middleware
	// doesn't panic on emit.
	lg, err := audit.New(t.TempDir() + "/audit.jsonl")
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}

	t.Cleanup(func() { _ = lg.Close() })

	return lg
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestRequireDashboardSite_PassesWithGrantedSite(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	store := seedGrants(map[uuid.UUID]map[uint32]Role{
		userID: {4: RoleAdmin},
	})

	mw := RequireDashboardSiteAccess(newSilentAudit(t), store, RoleViewer)

	u := &User{UserID: userID, Role: RoleAdmin}
	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview?site=4", nil)
	r = r.WithContext(WithSession(r.Context(), u, &Session{}))
	w := httptest.NewRecorder()

	mw(okHandler()).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", w.Code, w.Body.String())
	}
}

func TestRequireDashboardSite_403_WhenSiteNotInGrants(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	store := seedGrants(map[uuid.UUID]map[uint32]Role{
		userID: {4: RoleAdmin},
	})

	mw := RequireDashboardSiteAccess(newSilentAudit(t), store, RoleViewer)

	u := &User{UserID: userID, Role: RoleAdmin}
	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview?site=5", nil)
	r = r.WithContext(WithSession(r.Context(), u, &Session{}))
	w := httptest.NewRecorder()

	mw(okHandler()).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestRequireDashboardSite_403_WhenRoleBelowFloor(t *testing.T) {
	t.Parallel()

	// API role doesn't satisfy viewer-or-above (api=3 > viewer=2 in roleRank).
	userID := uuid.New()
	store := seedGrants(map[uuid.UUID]map[uint32]Role{
		userID: {4: RoleAPI},
	})

	mw := RequireDashboardSiteAccess(newSilentAudit(t), store, RoleViewer)

	u := &User{UserID: userID, Role: RoleAPI}
	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview?site=4", nil)
	r = r.WithContext(WithSession(r.Context(), u, &Session{}))
	w := httptest.NewRecorder()

	mw(okHandler()).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestRequireDashboardSite_400_OnBadSiteParam(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"missing":     "/api/stats/overview",
		"empty":       "/api/stats/overview?site=",
		"zero":        "/api/stats/overview?site=0",
		"non-numeric": "/api/stats/overview?site=foo",
	}

	mw := RequireDashboardSiteAccess(newSilentAudit(t), newFakeSitesStore(), RoleViewer)
	u := &User{UserID: uuid.New(), Role: RoleAdmin}

	for name, url := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequest(http.MethodGet, url, nil)
			r = r.WithContext(WithSession(r.Context(), u, &Session{}))
			w := httptest.NewRecorder()

			mw(okHandler()).ServeHTTP(w, r)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
		})
	}
}

func TestRequireDashboardSite_401_OnUnauthenticated(t *testing.T) {
	t.Parallel()

	mw := RequireDashboardSiteAccess(newSilentAudit(t), newFakeSitesStore(), RoleViewer)

	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview?site=4", nil)
	w := httptest.NewRecorder()

	mw(okHandler()).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// TestRequireDashboardSite_APIToken_OwnSiteAllowed asserts that a bearer
// token bound to site 4 can read /api/stats/overview?site=4. The api-token
// branch never queries user_sites. Uses RoleAPI as the floor — matches
// production main.go where dashboard reads accept admin/viewer/api.
func TestRequireDashboardSite_APIToken_OwnSiteAllowed(t *testing.T) {
	t.Parallel()

	mw := RequireDashboardSiteAccess(newSilentAudit(t), newFakeSitesStore(), RoleAPI)

	u := &User{UserID: uuid.Nil, SiteID: 4, Role: RoleAPI}
	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview?site=4", nil)
	r = r.WithContext(WithSession(r.Context(), u, &Session{}))
	w := httptest.NewRecorder()

	mw(okHandler()).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", w.Code, w.Body.String())
	}
}

// TestRequireDashboardSite_APIToken_403_OnCrossSite is the canonical
// regression for token-theft → cross-site escalation. A token bound to
// site 4 must 403 on every other site, regardless of the underlying
// user's grants.
func TestRequireDashboardSite_APIToken_403_OnCrossSite(t *testing.T) {
	t.Parallel()

	mw := RequireDashboardSiteAccess(newSilentAudit(t), newFakeSitesStore(), RoleAPI)

	u := &User{UserID: uuid.Nil, SiteID: 4, Role: RoleAPI}
	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview?site=5", nil)
	r = r.WithContext(WithSession(r.Context(), u, &Session{}))
	w := httptest.NewRecorder()

	mw(okHandler()).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

// TestRequireDashboardSite_Legacy_AllowsActorSiteIDMatch — flag-OFF
// path where sitesStore is nil and grants come from the user.SiteID
// invariant. The seeded operator on site 1 can read site 1.
func TestRequireDashboardSite_Legacy_AllowsActorSiteIDMatch(t *testing.T) {
	t.Parallel()

	mw := RequireDashboardSiteAccess(newSilentAudit(t), nil, RoleViewer)

	u := &User{UserID: uuid.New(), SiteID: 1, Role: RoleAdmin}
	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview?site=1", nil)
	r = r.WithContext(WithSession(r.Context(), u, &Session{}))
	w := httptest.NewRecorder()

	mw(okHandler()).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", w.Code, w.Body.String())
	}
}

func TestRequireDashboardSite_Legacy_403_OnActorSiteIDMismatch(t *testing.T) {
	t.Parallel()

	mw := RequireDashboardSiteAccess(newSilentAudit(t), nil, RoleViewer)

	u := &User{UserID: uuid.New(), SiteID: 1, Role: RoleAdmin}
	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview?site=2", nil)
	r = r.WithContext(WithSession(r.Context(), u, &Session{}))
	w := httptest.NewRecorder()

	mw(okHandler()).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestRequireDashboardSite_StashesValidatedSiteIDInContext(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	store := seedGrants(map[uuid.UUID]map[uint32]Role{
		userID: {4: RoleAdmin},
	})

	mw := RequireDashboardSiteAccess(newSilentAudit(t), store, RoleViewer)

	var seenSite uint32

	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := ActiveSiteIDFromContext(r.Context()); ok {
			seenSite = id
		}

		w.WriteHeader(http.StatusOK)
	})

	u := &User{UserID: userID, Role: RoleAdmin}
	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview?site=4", nil)
	r = r.WithContext(WithSession(r.Context(), u, &Session{}))
	w := httptest.NewRecorder()

	mw(probe).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	if seenSite != 4 {
		t.Errorf("ActiveSiteIDFromContext = %d, want 4", seenSite)
	}
}

// silenceSlog ensures slog.Default() doesn't drop test output to
// stderr. Called from package-init via TestMain so all tests benefit.
func silenceSlog() {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func init() { silenceSlog() }
