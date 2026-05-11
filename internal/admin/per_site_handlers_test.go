package admin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/goals"
)

// Per-site handler tests — exercising the ActiveSiteIDFromContext path
// that becomes active when features.per_site_admin is ON (flag flip in
// v0.0.10). Each test uses adminRequestWithSite to inject the active
// site_id the middleware would set in production.

// -------------- Goals.List with per-site path --------------

func TestGoals_List_UsesActiveSiteIDFromContext(t *testing.T) {
	t.Parallel()

	deps, gs := newGoalsDeps()
	deps.Sites = newFakeSitesStore()
	admin := newTestAdminWithSites(1, 2)

	ctx := context.Background()

	_ = gs.Create(ctx, &goals.Goal{
		SiteID: 1, Name: "Site1 Goal", MatchType: goals.MatchTypeEventNameEquals,
		Pattern: "buy", Enabled: true,
	})
	_ = gs.Create(ctx, &goals.Goal{
		SiteID: 2, Name: "Site2 Goal", MatchType: goals.MatchTypeEventNameEquals,
		Pattern: "subscribe", Enabled: true,
	})

	h := NewGoals(deps)

	// Request for site 2 via active site_id context.
	w := httptest.NewRecorder()
	h.List(w, adminRequestWithSite(t, "GET", "/api/admin/goals?site_id=2", "", admin, 2, nil))

	if w.Code != 200 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	var body struct {
		Goals []goalResponse `json:"goals"`
	}

	_ = json.Unmarshal(w.Body.Bytes(), &body)

	if len(body.Goals) != 1 || body.Goals[0].Name != "Site2 Goal" {
		t.Errorf("expected only Site2 Goal, got: %+v", body.Goals)
	}
}

func TestGoals_List_ReturnsHostname(t *testing.T) {
	t.Parallel()

	deps, gs := newGoalsDeps()
	ss := newFakeSitesStore()
	_, _ = ss.CreateSite(context.Background(), "example.com", "example", "", "")
	deps.Sites = ss

	admin := newTestAdminWithSites(1)
	ctx := context.Background()

	_ = gs.Create(ctx, &goals.Goal{
		SiteID: 1, Name: "Purchase", MatchType: goals.MatchTypeEventNameEquals,
		Pattern: "purchase", Enabled: true,
	})

	h := NewGoals(deps)
	w := httptest.NewRecorder()
	h.List(w, adminRequestWithSite(t, "GET", "/api/admin/goals?site_id=1", "", admin, 1, nil))

	if w.Code != 200 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	var body struct {
		Goals []goalResponse `json:"goals"`
	}

	_ = json.Unmarshal(w.Body.Bytes(), &body)

	if len(body.Goals) == 0 {
		t.Fatal("expected at least one goal")
	}

	if body.Goals[0].Hostname != "example.com" {
		t.Errorf("hostname = %q, want example.com", body.Goals[0].Hostname)
	}
}

func TestGoals_Create_UsesSiteFromContext(t *testing.T) {
	t.Parallel()

	deps, gs := newGoalsDeps()
	deps.Sites = newFakeSitesStore()
	admin := newTestAdminWithSites(5)

	h := NewGoals(deps)
	body := `{"name":"Signup","match_type":"event_name_equals","pattern":"signup","value":10,"enabled":true}`

	w := httptest.NewRecorder()
	h.Create(w, adminRequestWithSite(t, "POST", "/api/admin/goals?site_id=5", body, admin, 5, nil))

	if w.Code != 201 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	// Goal should be stored with siteID=5, not actor.SiteID=1.
	list, _ := gs.List(context.Background(), 5)
	if len(list) != 1 || list[0].SiteID != 5 {
		t.Errorf("expected goal at site 5, got: %+v", list)
	}

	listSite1, _ := gs.List(context.Background(), 1)
	if len(listSite1) != 0 {
		t.Errorf("no goal should be at site 1 (actor's SiteID); got: %+v", listSite1)
	}
}

func TestGoals_Disable_DefenceInDepth(t *testing.T) {
	t.Parallel()

	// Actor has admin on site 1 only. Tries to disable a goal that
	// belongs to site 2. Should 404 (store's WHERE site_id = ? AND goal_id
	// = ? finds nothing), not silently succeed.
	deps, gs := newGoalsDeps()
	deps.Sites = newFakeSitesStore()
	admin := newTestAdminWithSites(1)

	goalOnSite2 := &goals.Goal{
		GoalID: uuid.New(), SiteID: 2, Name: "Other", MatchType: goals.MatchTypeEventNameEquals,
		Pattern: "x", Enabled: true,
	}
	// Insert directly into the fake store bypassing siteID validation.
	gs.byID[goalOnSite2.GoalID] = goalOnSite2

	h := NewGoals(deps)
	w := httptest.NewRecorder()
	h.Disable(w, adminRequestWithSite(
		t, "POST", "/api/admin/goals/"+goalOnSite2.GoalID.String()+"/disable?site_id=1", "",
		admin, 1, map[string]string{"id": goalOnSite2.GoalID.String()},
	))

	// Store looks up by (site_id=1, goal_id=?) — not found.
	if w.Code != 404 {
		t.Fatalf("expected 404 (goal on site 2 not visible from site 1), got %d", w.Code)
	}
}

// -------------- Users.List per-site path --------------

func TestUsers_List_PerSitePath_ReturnsSitesArray(t *testing.T) {
	t.Parallel()

	as := newFakeAuthStore()
	us := newFakeUserSitesStore()
	ss := newFakeSitesStore()
	deps := Deps{Auth: as, Sites: ss, UserSites: us}
	ctx := context.Background()

	// Seed target user.
	targetUser := &auth.User{UserID: uuid.New(), SiteID: 7, Email: "alice@example.com", Role: auth.RoleViewer}
	_ = as.CreateUser(ctx, targetUser, "hash")
	_ = us.Grant(ctx, targetUser.UserID, 7, auth.RoleViewer)

	// Actor with admin on site 7 and Sites map set.
	actor := newTestAdminWithSites(7)

	h := NewUsers(deps)
	w := httptest.NewRecorder()
	req := adminRequestWithSite(t, "GET", "/api/admin/users?site_id=7", "", actor, 7, nil)
	h.List(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	var body struct {
		Users []userResponse `json:"users"`
	}

	_ = json.Unmarshal(w.Body.Bytes(), &body)

	if len(body.Users) != 1 {
		t.Fatalf("expected 1 user, got %d: %+v", len(body.Users), body.Users)
	}

	if len(body.Users[0].Sites) == 0 {
		t.Errorf("user.sites should be populated, got empty slice")
	}
}

func TestUsers_Create_ValidatesSiteGrants(t *testing.T) {
	t.Parallel()

	as := newFakeAuthStore()
	us := newFakeUserSitesStore()
	ss := newFakeSitesStore()
	deps := Deps{Auth: as, Sites: ss, UserSites: us}

	// Actor has admin on site 1 only.
	actor := newTestAdminWithSites(1)

	h := NewUsers(deps)
	// Try to create a user on site 2 — actor doesn't have admin on 2.
	body := `{"email":"new@example.com","username":"new","password":"secret",` +
		`"sites":[{"site_id":2,"role":"admin"}]}`

	w := httptest.NewRecorder()
	h.Create(w, adminRequestWithSite(t, "POST", "/api/admin/users?site_id=1", body, actor, 1, nil))

	if w.Code != 403 {
		t.Fatalf("expected 403 (actor not admin on site 2), got %d", w.Code)
	}
}

func TestUsers_UpdateSites_DiffGrantRevoke(t *testing.T) {
	t.Parallel()

	as := newFakeAuthStore()
	us := newFakeUserSitesStore()
	ss := newFakeSitesStore()
	deps := Deps{Auth: as, Sites: ss, UserSites: us}
	ctx := context.Background()

	// Seed target user with grant on sites 1+2.
	targetUser := &auth.User{UserID: uuid.New(), SiteID: 1, Email: "target@example.com", Role: auth.RoleViewer}
	_ = as.CreateUser(ctx, targetUser, "hash")
	_ = us.Grant(ctx, targetUser.UserID, 1, auth.RoleViewer)
	_ = us.Grant(ctx, targetUser.UserID, 2, auth.RoleViewer)

	// Actor has admin on sites 1+2.
	actor := newTestAdminWithSites(1, 2)

	h := NewUsers(deps)

	// PATCH /sites — keep site 1, promote to admin; remove site 2; add site 3 ... but actor
	// isn't on site 3, so let's just test keep-1-remove-2.
	body := `{"sites":[{"site_id":1,"role":"admin"}]}`

	w := httptest.NewRecorder()
	h.UpdateSites(w, adminRequestWithSite(
		t, "PATCH", "/api/admin/users/"+targetUser.UserID.String()+"/sites", body,
		actor, 1, map[string]string{"id": targetUser.UserID.String()},
	))

	if w.Code != 204 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	// Site 1 should be upgraded to admin.
	grants, _ := us.LoadUserSites(ctx, targetUser.UserID)
	if r, ok := grants[1]; !ok || r != auth.RoleAdmin {
		t.Errorf("site 1 should be admin, got %v (ok=%v)", r, ok)
	}

	// Site 2 should be revoked.
	if _, has := grants[2]; has {
		t.Errorf("site 2 should have been revoked")
	}
}

// -------------- Sites.List per-site filtering --------------

func TestSites_List_FilteredToActorSites(t *testing.T) {
	t.Parallel()

	deps, ss := newSitesDeps()
	ctx := context.Background()

	// Create three sites.
	_, _ = ss.CreateSite(ctx, "alpha.com", "alpha", "", "")
	_, _ = ss.CreateSite(ctx, "beta.com", "beta", "", "")
	_, _ = ss.CreateSite(ctx, "gamma.com", "gamma", "", "")

	// Actor has access to sites 1+2 only.
	actor := newTestAdminWithSites(1, 2)

	h := NewSites(deps)
	w := httptest.NewRecorder()
	h.List(w, adminRequestWithSite(t, "GET", "/api/admin/sites?site_id=1", "", actor, 1, nil))

	if w.Code != 200 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	var body struct {
		Sites []siteAdminResponse `json:"sites"`
	}

	_ = json.Unmarshal(w.Body.Bytes(), &body)

	if len(body.Sites) != 2 {
		t.Errorf("expected 2 sites (1+2), got %d: %+v", len(body.Sites), body.Sites)
	}
}

func TestSites_Create_AutoGrantsActor(t *testing.T) {
	t.Parallel()

	as := newFakeAuthStore()
	us := newFakeUserSitesStore()
	ss := newFakeSitesStore()
	deps := Deps{Auth: as, Sites: ss, UserSites: us}

	actor := newTestAdminWithSites(1)
	ctx := context.Background()
	_ = as.CreateUser(ctx, actor, "hash")

	h := NewSites(deps)
	body := `{"hostname":"newsite.example.com"}`

	w := httptest.NewRecorder()
	h.Create(w, adminRequestWithSite(t, "POST", "/api/admin/sites?site_id=1", body, actor, 1, nil))

	if w.Code != 201 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	var resp siteAdminResponse

	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	// Actor should now have a user_sites grant on the new site.
	grants, _ := us.LoadUserSites(ctx, actor.UserID)
	if _, ok := grants[resp.SiteID]; !ok {
		t.Errorf("actor not auto-granted on new site %d; grants: %v", resp.SiteID, grants)
	}
}
