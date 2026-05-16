package auth

import (
	"context"
	"fmt"
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

// The two 403 tests below intentionally pin two distinct failure
// branches of the same middleware: site-not-in-grants vs grant-rank-
// below-floor. Their setup happens to look near-identical but
// different inputs exercise different code paths in scopeUserToSite.
//
//nolint:dupl // intentional structural twin — pins two distinct rejection branches
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

//nolint:dupl // intentional structural twin — pins two distinct rejection branches
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

// TestRequireDashboardSite_APIToken_Branches pins three distinct
// api-token branches in a single table:
//
//   - SiteID==requestedSite: own-site read (200).
//   - SiteID==0: legacy admin-equivalent bearer wildcard (200) — the
//     auto-promoted "bearer-legacy" entry in
//     cmd/statnive-live/main.go::buildAPITokens.
//   - SiteID > 0 && SiteID != requestedSite: cross-site escalation
//     regression — must 403 even if the underlying user has other
//     grants.
func TestRequireDashboardSite_APIToken_Branches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		tokenSiteID   uint32
		requestedSite uint32
		want          int
	}{
		{"own_site", 4, 4, http.StatusOK},
		{"legacy_wildcard_zero", 0, 42, http.StatusOK},
		{"cross_site_403", 4, 5, http.StatusForbidden},
	}

	mw := RequireDashboardSiteAccess(newSilentAudit(t), newFakeSitesStore(), RoleAPI)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			u := &User{UserID: uuid.Nil, SiteID: tc.tokenSiteID, Role: RoleAPI}
			r := httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("/api/stats/overview?site=%d", tc.requestedSite), nil)
			r = r.WithContext(WithSession(r.Context(), u, &Session{}))
			w := httptest.NewRecorder()

			mw(okHandler()).ServeHTTP(w, r)

			if w.Code != tc.want {
				t.Errorf("token{SiteID=%d} → site=%d: status = %d, want %d",
					tc.tokenSiteID, tc.requestedSite, w.Code, tc.want)
			}
		})
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

// TestHydrateActorGrants_PopulatesSitesForSessionUser pins the contract
// that the /api/sites listing route receives a User whose Sites map has
// been hydrated from user_sites grants — without which the listing
// filter falls through to the legacy single-site path and returns the
// wrong list.
func TestHydrateActorGrants_PopulatesSitesForSessionUser(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	store := seedGrants(map[uuid.UUID]map[uint32]Role{
		userID: {4: RoleViewer, 5: RoleAdmin},
	})

	mw := HydrateActorGrants(store)

	var seen map[uint32]Role

	probe := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if u := UserFrom(r.Context()); u != nil {
			seen = u.Sites
		}
	})

	u := &User{UserID: userID, Role: RoleAdmin}
	r := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	r = r.WithContext(WithSession(r.Context(), u, &Session{}))
	w := httptest.NewRecorder()

	mw(probe).ServeHTTP(w, r)

	if len(seen) != 2 || seen[4] != RoleViewer || seen[5] != RoleAdmin {
		t.Errorf("hydrated Sites = %+v, want {4: viewer, 5: admin}", seen)
	}
}

// TestHydrateActorGrants_PassesThroughAPIToken pins that synthetic
// api-token actors (UserID == uuid.Nil) are passed through unchanged —
// their (SiteID, Role) is the source of truth, no user_sites lookup.
func TestHydrateActorGrants_PassesThroughAPIToken(t *testing.T) {
	t.Parallel()

	mw := HydrateActorGrants(seedGrants(map[uuid.UUID]map[uint32]Role{}))

	var seenUserID uuid.UUID

	probe := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if u := UserFrom(r.Context()); u != nil {
			seenUserID = u.UserID
		}
	})

	u := &User{UserID: uuid.Nil, SiteID: 0, Role: RoleAPI}
	r := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	r = r.WithContext(WithSession(r.Context(), u, &Session{}))
	w := httptest.NewRecorder()

	mw(probe).ServeHTTP(w, r)

	if seenUserID != uuid.Nil {
		t.Errorf("api-token actor was mutated: %v", seenUserID)
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

// silenceSlog ensures slog.Default() doesn't write test output to
// stderr. Called from package-init so all tests benefit.
func silenceSlog() {
	slog.SetDefault(slog.New(slog.DiscardHandler))
}

func init() { silenceSlog() }
