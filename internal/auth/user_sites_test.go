package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestUser_CanAccessSite(t *testing.T) {
	t.Parallel()

	u := &User{
		UserID: uuid.New(),
		Sites: map[uint32]Role{
			1: RoleAdmin,
			2: RoleViewer,
			3: RoleAPI,
		},
	}

	cases := []struct {
		name     string
		siteID   uint32
		required Role
		want     bool
	}{
		{"admin satisfies admin", 1, RoleAdmin, true},
		{"admin satisfies viewer", 1, RoleViewer, true},
		{"admin satisfies api", 1, RoleAPI, true},
		{"viewer satisfies viewer", 2, RoleViewer, true},
		{"viewer satisfies api", 2, RoleAPI, true},
		{"viewer does NOT satisfy admin", 2, RoleAdmin, false},
		{"api does NOT satisfy admin", 3, RoleAdmin, false},
		{"api does NOT satisfy viewer", 3, RoleViewer, false},
		{"unknown site denies all", 99, RoleAdmin, false},
		{"unknown site denies viewer", 99, RoleViewer, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := u.CanAccessSite(tc.siteID, tc.required); got != tc.want {
				t.Fatalf("CanAccessSite(%d, %s) = %v, want %v", tc.siteID, tc.required, got, tc.want)
			}
		})
	}
}

func TestUser_CanAccessSite_NilUser_FailsClosed(t *testing.T) {
	t.Parallel()

	var u *User

	if u.CanAccessSite(1, RoleAdmin) {
		t.Fatal("nil User must fail closed")
	}
}

func TestUser_CanAccessSite_NilSitesMap_FailsClosed(t *testing.T) {
	t.Parallel()

	u := &User{UserID: uuid.New()} // no Sites map populated

	if u.CanAccessSite(1, RoleAdmin) {
		t.Fatal("nil Sites map must fail closed")
	}
}

func TestUser_SiteIDs(t *testing.T) {
	t.Parallel()

	u := &User{
		Sites: map[uint32]Role{7: RoleViewer, 1: RoleAdmin, 3: RoleViewer},
	}

	got := u.SiteIDs()
	want := []uint32{1, 3, 7}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SiteIDs() = %v, want %v (sorted)", got, want)
	}
}

func TestUser_SiteIDs_Empty(t *testing.T) {
	t.Parallel()

	if got := (*User)(nil).SiteIDs(); len(got) != 0 {
		t.Fatalf("nil User SiteIDs() = %v, want empty", got)
	}

	if got := (&User{}).SiteIDs(); len(got) != 0 {
		t.Fatalf("empty Sites SiteIDs() = %v, want empty", got)
	}
}

func TestParseSiteID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw     string
		want    uint32
		wantErr bool
	}{
		{"1", 1, false},
		{"42", 42, false},
		{"4294967295", 4294967295, false},
		{"", 0, true},
		{"0", 0, true},
		{"-1", 0, true},
		{"abc", 0, true},
		{"1 OR 1=1", 0, true},
		{"4294967296", 0, true}, // overflow uint32
		{"1.5", 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()

			got, err := parseSiteID(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseSiteID(%q) err=nil, want error", tc.raw)
				}

				return
			}

			if err != nil {
				t.Fatalf("parseSiteID(%q) unexpected err: %v", tc.raw, err)
			}

			if got != tc.want {
				t.Fatalf("parseSiteID(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

// fakeSitesStore is the in-memory mock used by RequireSiteRole tests.
// Mirrors fakeStore's shape from store_test.go — same package, no API
// to export.
type fakeSitesStore struct {
	mu     sync.Mutex
	grants map[uuid.UUID]map[uint32]Role
	loads  int
}

func newFakeSitesStore() *fakeSitesStore {
	return &fakeSitesStore{grants: map[uuid.UUID]map[uint32]Role{}}
}

func (f *fakeSitesStore) LoadUserSites(_ context.Context, userID uuid.UUID) (map[uint32]Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.loads++

	out := make(map[uint32]Role, len(f.grants[userID]))
	for k, v := range f.grants[userID] {
		out[k] = v
	}

	return out, nil
}

func (f *fakeSitesStore) Grant(_ context.Context, userID uuid.UUID, siteID uint32, role Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.grants[userID] == nil {
		f.grants[userID] = map[uint32]Role{}
	}

	f.grants[userID][siteID] = role

	return nil
}

func (f *fakeSitesStore) Revoke(_ context.Context, userID uuid.UUID, siteID uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.grants[userID], siteID)

	return nil
}

func (f *fakeSitesStore) ListUsersBySite(_ context.Context, siteID uint32) ([]UserSiteGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]UserSiteGrant, 0, 4)

	for userID, sites := range f.grants {
		if r, ok := sites[siteID]; ok {
			out = append(out, UserSiteGrant{UserID: userID, Role: r})
		}
	}

	return out, nil
}

// errorSitesStore returns an error on every LoadUserSites call. Used to
// pin the "internal error" path in RequireSiteRole.
type errorSitesStore struct{}

func (errorSitesStore) LoadUserSites(context.Context, uuid.UUID) (map[uint32]Role, error) {
	return nil, errors.New("ch is down")
}

func (errorSitesStore) Grant(context.Context, uuid.UUID, uint32, Role) error { return nil }

func (errorSitesStore) Revoke(context.Context, uuid.UUID, uint32) error { return nil }

func (errorSitesStore) ListUsersBySite(context.Context, uint32) ([]UserSiteGrant, error) {
	return nil, nil
}

// nextOK is the downstream handler used by every RequireSiteRole test:
// 200 OK with the active site_id echoed in the body so we can assert
// the middleware stashed it correctly.
func nextOK(w http.ResponseWriter, r *http.Request) {
	siteID, _ := ActiveSiteIDFromContext(r.Context())

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("active=" + strconv.FormatUint(uint64(siteID), 10)))
}

func TestRequireSiteRole_Authorized_200(t *testing.T) {
	t.Parallel()

	store := newFakeSitesStore()
	uid := uuid.New()

	_ = store.Grant(context.Background(), uid, 5, RoleAdmin)

	mw := RequireSiteRole(nil, store, RoleAdmin)
	handler := mw(http.HandlerFunc(nextOK))

	u := &User{UserID: uid, SiteID: 5, Role: RoleAdmin}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/goals?site_id=5", nil)
	req = req.WithContext(WithSession(req.Context(), u, &Session{}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	if got := rec.Body.String(); got != "active=5" {
		t.Fatalf("active site echo = %q, want active=5", got)
	}
}

func TestRequireSiteRole_NoGrant_403(t *testing.T) {
	t.Parallel()

	store := newFakeSitesStore()
	uid := uuid.New()

	_ = store.Grant(context.Background(), uid, 5, RoleAdmin)

	mw := RequireSiteRole(nil, store, RoleAdmin)
	handler := mw(http.HandlerFunc(nextOK))

	u := &User{UserID: uid}

	// Actor has admin on 5 but requests site 6 — IDOR attempt.
	req := httptest.NewRequest(http.MethodGet, "/api/admin/goals?site_id=6", nil)
	req = req.WithContext(WithSession(req.Context(), u, &Session{}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRequireSiteRole_ViewerOnAdminEndpoint_403(t *testing.T) {
	t.Parallel()

	store := newFakeSitesStore()
	uid := uuid.New()

	_ = store.Grant(context.Background(), uid, 5, RoleViewer)

	mw := RequireSiteRole(nil, store, RoleAdmin) // admin required
	handler := mw(http.HandlerFunc(nextOK))

	u := &User{UserID: uid}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/goals?site_id=5", nil)
	req = req.WithContext(WithSession(req.Context(), u, &Session{}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer hitting admin endpoint: status = %d, want 403", rec.Code)
	}
}

func TestRequireSiteRole_BadSiteID_400(t *testing.T) {
	t.Parallel()

	store := newFakeSitesStore()
	mw := RequireSiteRole(nil, store, RoleAdmin)
	handler := mw(http.HandlerFunc(nextOK))

	cases := []string{
		"/api/admin/goals",            // missing param
		"/api/admin/goals?site_id=",   // empty
		"/api/admin/goals?site_id=0",  // zero
		"/api/admin/goals?site_id=ab", // non-numeric
	}

	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			u := &User{UserID: uuid.New()}
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req = req.WithContext(WithSession(req.Context(), u, &Session{}))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s status = %d, want 400", path, rec.Code)
			}
		})
	}
}

func TestRequireSiteRole_Unauthenticated_401(t *testing.T) {
	t.Parallel()

	store := newFakeSitesStore()
	mw := RequireSiteRole(nil, store, RoleAdmin)
	handler := mw(http.HandlerFunc(nextOK))

	req := httptest.NewRequest(http.MethodGet, "/api/admin/goals?site_id=1", nil)
	// No WithSession — UserFrom returns nil.

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestRequireSiteRole_LoadError_500(t *testing.T) {
	t.Parallel()

	mw := RequireSiteRole(nil, errorSitesStore{}, RoleAdmin)
	handler := mw(http.HandlerFunc(nextOK))

	u := &User{UserID: uuid.New()}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/goals?site_id=1", nil)
	req = req.WithContext(WithSession(req.Context(), u, &Session{}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestRequireSiteRole_PerRequestRevoke(t *testing.T) {
	t.Parallel()

	// Regression-pins the revoke-race fix: granting then revoking
	// between two requests must take effect immediately on the second
	// request. RequireSiteRole reads user_sites per-request (no cache).
	store := newFakeSitesStore()
	uid := uuid.New()

	_ = store.Grant(context.Background(), uid, 5, RoleAdmin)

	mw := RequireSiteRole(nil, store, RoleAdmin)
	handler := mw(http.HandlerFunc(nextOK))

	u := &User{UserID: uid}

	// First request: 200.
	{
		req := httptest.NewRequest(http.MethodGet, "/api/admin/goals?site_id=5", nil)
		req = req.WithContext(WithSession(req.Context(), u, &Session{}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("first request status = %d, want 200", rec.Code)
		}
	}

	// Revoke.
	_ = store.Revoke(context.Background(), uid, 5)

	// Second request: 403 (no cached state).
	{
		req := httptest.NewRequest(http.MethodGet, "/api/admin/goals?site_id=5", nil)
		req = req.WithContext(WithSession(req.Context(), u, &Session{}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("second request status = %d, want 403 (revoke must take effect immediately)", rec.Code)
		}
	}

	if store.loads != 2 {
		t.Fatalf("LoadUserSites called %d times, want 2 (per-request, no cache)", store.loads)
	}
}

func TestActiveSiteIDFromContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	if _, ok := ActiveSiteIDFromContext(ctx); ok {
		t.Fatal("empty ctx must not yield active site_id")
	}

	ctx = WithActiveSiteID(ctx, 42)

	got, ok := ActiveSiteIDFromContext(ctx)
	if !ok || got != 42 {
		t.Fatalf("ActiveSiteIDFromContext = (%d, %v), want (42, true)", got, ok)
	}
}
