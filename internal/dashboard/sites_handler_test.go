package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/sites"
)

// manySitesLister returns the full registry of sites (mimics a SaaS
// box with multiple tenants seeded). filterSitesForActor is the unit
// under test — the registry itself does no auth.
type manySitesLister struct{ ids []uint32 }

func (m manySitesLister) List(_ context.Context) ([]sites.Site, error) {
	out := make([]sites.Site, 0, len(m.ids))
	for _, id := range m.ids {
		out = append(out, sites.Site{ID: id, Hostname: "site-" + id2str(id), Enabled: true, TZ: "UTC", Currency: "EUR"})
	}

	return out, nil
}

func (m manySitesLister) LookupSiteByID(_ context.Context, id uint32) (sites.SiteAdmin, error) {
	for _, sid := range m.ids {
		if sid == id {
			return sites.SiteAdmin{
				Site: sites.Site{ID: id, Hostname: "site-" + id2str(id), Enabled: true, TZ: "UTC", Currency: "EUR"},
			}, nil
		}
	}

	return sites.SiteAdmin{}, nil
}

func id2str(id uint32) string {
	const digits = "0123456789"

	if id == 0 {
		return "0"
	}

	var buf [10]byte
	i := len(buf)

	for id > 0 {
		i--
		buf[i] = digits[id%10]
		id /= 10
	}

	return string(buf[i:])
}

func newSitesDeps(t *testing.T, lister SiteLister) Deps {
	return Deps{
		Sites:  lister,
		Audit:  newSilentAudit(t),
		Logger: newSilentLogger(),
	}
}

func TestSitesHandler_FiltersToActorGrants(t *testing.T) {
	t.Parallel()

	deps := newSitesDeps(t, manySitesLister{ids: []uint32{1, 4, 5, 7}})

	actor := &auth.User{
		UserID: uuid.New(),
		Sites:  map[uint32]auth.Role{1: auth.RoleAdmin, 4: auth.RoleViewer},
	}

	r := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	r = r.WithContext(auth.WithSession(r.Context(), actor, &auth.Session{}))
	w := httptest.NewRecorder()

	sitesHandler(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var env sitesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(env.Sites) != 2 {
		t.Fatalf("Sites count = %d, want 2 (grants on 1+4); got %+v", len(env.Sites), env.Sites)
	}

	ids := map[uint32]bool{}
	for _, s := range env.Sites {
		ids[s.ID] = true
	}

	if !ids[1] || !ids[4] {
		t.Errorf("Sites = %+v, want {1, 4}", env.Sites)
	}

	if ids[5] || ids[7] {
		t.Errorf("Sites leaked an ungranted site: %+v", env.Sites)
	}
}

func TestSitesHandler_EmptyWhenActorHasNoGrants(t *testing.T) {
	t.Parallel()

	deps := newSitesDeps(t, manySitesLister{ids: []uint32{1, 4, 5}})

	actor := &auth.User{
		UserID: uuid.New(),
		Sites:  map[uint32]auth.Role{}, // empty map, not nil — flag-ON with no grants
	}

	r := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	r = r.WithContext(auth.WithSession(r.Context(), actor, &auth.Session{}))
	w := httptest.NewRecorder()

	sitesHandler(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with empty list (onboarding UX)", w.Code)
	}

	var env sitesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(env.Sites) != 0 {
		t.Errorf("Sites = %+v, want empty", env.Sites)
	}
}

func TestSitesHandler_LegacyMode_FiltersToActorSiteID(t *testing.T) {
	t.Parallel()

	deps := newSitesDeps(t, manySitesLister{ids: []uint32{1, 4, 5}})

	// Legacy flag-OFF: actor.Sites is nil; only actor.SiteID is visible.
	actor := &auth.User{UserID: uuid.New(), SiteID: 4, Role: auth.RoleAdmin}

	r := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	r = r.WithContext(auth.WithSession(r.Context(), actor, &auth.Session{}))
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

func TestSitesHandler_APIToken_FiltersToBoundSite(t *testing.T) {
	t.Parallel()

	deps := newSitesDeps(t, manySitesLister{ids: []uint32{1, 4, 5}})

	actor := &auth.User{UserID: uuid.Nil, SiteID: 4, Role: auth.RoleAPI}

	r := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	r = r.WithContext(auth.WithSession(r.Context(), actor, &auth.Session{}))
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
		t.Errorf("Sites = %+v, want exactly [{id:4}] for api token bound to site 4", env.Sites)
	}
}

func TestSitesHandler_NilActor_FailsClosed(t *testing.T) {
	t.Parallel()

	deps := newSitesDeps(t, manySitesLister{ids: []uint32{1, 4, 5}})

	// No auth.WithSession — actor is nil. Defense in depth: must return
	// an empty list, not the full registry.
	r := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	w := httptest.NewRecorder()

	sitesHandler(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (handler still answers; just empty)", w.Code)
	}

	var env sitesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(env.Sites) != 0 {
		t.Errorf("Sites = %+v, want empty for nil actor", env.Sites)
	}
}
