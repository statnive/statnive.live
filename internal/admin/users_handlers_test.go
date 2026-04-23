package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/auth"
)

func newTestAdmin() *auth.User {
	return &auth.User{
		UserID: uuid.New(),
		SiteID: 1,
		Email:  "admin@example.com",
		Role:   auth.RoleAdmin,
	}
}

func newTestDeps() (Deps, *fakeAuthStore, *fakeGoalsStore) {
	as := newFakeAuthStore()
	gs := newFakeGoalsStore()

	return Deps{Auth: as, Goals: gs}, as, gs
}

func TestUsers_List(t *testing.T) {
	t.Parallel()

	deps, as, _ := newTestDeps()
	admin := newTestAdmin()
	_ = as.CreateUser(context.Background(), admin, "hash")

	// Another user on a different site — must NOT appear in admin's list.
	otherSite := &auth.User{UserID: uuid.New(), SiteID: 2, Email: "x@y.z", Role: auth.RoleViewer}
	_ = as.CreateUser(context.Background(), otherSite, "hash")

	h := NewUsers(deps)
	w := httptest.NewRecorder()
	h.List(w, adminRequest(t, "GET", "/api/admin/users", "", admin, nil))

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}

	var body struct {
		Users []userResponse `json:"users"`
	}

	_ = json.Unmarshal(w.Body.Bytes(), &body)

	if len(body.Users) != 1 || body.Users[0].Email != "admin@example.com" {
		t.Errorf("unexpected users: %+v", body.Users)
	}
}

func TestUsers_CreateHappy(t *testing.T) {
	t.Parallel()

	deps, _, _ := newTestDeps()
	admin := newTestAdmin()

	h := NewUsers(deps)
	body := `{"email":"viewer@example.com","username":"viewer","password":"strong-pass-123","role":"viewer"}`

	w := httptest.NewRecorder()
	h.Create(w, adminRequest(t, "POST", "/api/admin/users", body, admin, nil))

	if w.Code != 201 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var got userResponse

	_ = json.Unmarshal(w.Body.Bytes(), &got)

	if got.SiteID != admin.SiteID {
		t.Errorf("site_id = %d, want %d (session-derived)", got.SiteID, admin.SiteID)
	}

	if got.Role != "viewer" {
		t.Errorf("role = %q", got.Role)
	}
}

// TestUsers_Create_RejectsMassAssignment — canonical F4 body that
// tries to slip site_id=99, role=admin past the body into the
// response. Expect 400 at decode time (unknown field from the
// attacker's perspective because the struct has no `site_id`
// field).
func TestUsers_Create_RejectsMassAssignment(t *testing.T) {
	t.Parallel()

	deps, _, _ := newTestDeps()
	admin := newTestAdmin()

	h := NewUsers(deps)
	attack := `{"email":"x@y.z","username":"x","password":"pw","role":"viewer","site_id":99,"is_admin":true}`

	w := httptest.NewRecorder()
	h.Create(w, adminRequest(t, "POST", "/api/admin/users", attack, admin, nil))

	if w.Code != 400 {
		t.Fatalf("mass-assignment attack not rejected: status = %d", w.Code)
	}
}

func TestUsers_Update_ChangesRoleSameSite(t *testing.T) {
	t.Parallel()

	deps, as, _ := newTestDeps()
	admin := newTestAdmin()
	_ = as.CreateUser(context.Background(), admin, "hash")

	target := &auth.User{UserID: uuid.New(), SiteID: 1, Email: "v@x.z", Role: auth.RoleViewer}
	_ = as.CreateUser(context.Background(), target, "hash")

	h := NewUsers(deps)
	body := `{"username":"v","role":"admin"}`

	w := httptest.NewRecorder()
	h.Update(w, adminRequest(t, "PATCH", "/api/admin/users/"+target.UserID.String(), body, admin,
		map[string]string{"id": target.UserID.String()}))

	if w.Code != 200 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	u, _ := as.GetUserByID(context.Background(), target.UserID)
	if u.Role != auth.RoleAdmin {
		t.Errorf("role not updated: %s", u.Role)
	}
}

func TestUsers_Update_CrossSiteForbidden(t *testing.T) {
	t.Parallel()

	deps, as, _ := newTestDeps()
	admin := newTestAdmin()
	_ = as.CreateUser(context.Background(), admin, "hash")

	other := &auth.User{UserID: uuid.New(), SiteID: 2, Email: "x@y.z", Role: auth.RoleViewer}
	_ = as.CreateUser(context.Background(), other, "hash")

	h := NewUsers(deps)
	body := `{"username":"x","role":"admin"}`

	w := httptest.NewRecorder()
	h.Update(w, adminRequest(t, "PATCH", "/api/admin/users/"+other.UserID.String(), body, admin,
		map[string]string{"id": other.UserID.String()}))

	if w.Code != 403 {
		t.Errorf("cross-site Update: status = %d, want 403", w.Code)
	}
}

func TestUsers_ResetPassword(t *testing.T) {
	t.Parallel()

	deps, as, _ := newTestDeps()
	admin := newTestAdmin()
	_ = as.CreateUser(context.Background(), admin, "hash")

	h := NewUsers(deps)
	body := `{"password":"new-strong-pass-456"}`

	w := httptest.NewRecorder()
	h.ResetPassword(w, adminRequest(t, "POST",
		"/api/admin/users/"+admin.UserID.String()+"/password",
		body, admin,
		map[string]string{"id": admin.UserID.String()}))

	if w.Code != 204 {
		t.Errorf("status = %d body = %s", w.Code, w.Body.String())
	}
}

func TestUsers_Disable(t *testing.T) {
	t.Parallel()

	deps, as, _ := newTestDeps()
	admin := newTestAdmin()
	_ = as.CreateUser(context.Background(), admin, "hash")

	target := &auth.User{UserID: uuid.New(), SiteID: 1, Email: "v@x.z", Role: auth.RoleViewer}
	_ = as.CreateUser(context.Background(), target, "hash")

	h := NewUsers(deps)

	w := httptest.NewRecorder()
	h.Disable(w, adminRequest(t, "POST",
		"/api/admin/users/"+target.UserID.String()+"/disable",
		"", admin,
		map[string]string{"id": target.UserID.String()}))

	if w.Code != 204 {
		t.Fatalf("status = %d", w.Code)
	}

	u, _ := as.GetUserByID(context.Background(), target.UserID)
	if !u.Disabled {
		t.Error("Disable did not flip Disabled flag")
	}
}

func TestUsers_Enable_IdempotentNoop(t *testing.T) {
	t.Parallel()

	deps, as, _ := newTestDeps()
	admin := newTestAdmin()
	_ = as.CreateUser(context.Background(), admin, "hash")

	target := &auth.User{UserID: uuid.New(), SiteID: 1, Email: "v@x.z", Role: auth.RoleViewer}
	_ = as.CreateUser(context.Background(), target, "hash")

	h := NewUsers(deps)

	w := httptest.NewRecorder()
	h.Enable(w, adminRequest(t, "POST",
		"/api/admin/users/"+target.UserID.String()+"/enable",
		"", admin,
		map[string]string{"id": target.UserID.String()}))

	// v1 Enable is a 204 no-op (DisableUser has no reverse primitive
	// in auth.Store v1; see users_handlers.go:setEnabled comment).
	if w.Code != 204 {
		t.Errorf("Enable: status = %d, want 204", w.Code)
	}
}

func TestUsers_NoSessionReturns401(t *testing.T) {
	t.Parallel()

	deps, _, _ := newTestDeps()

	h := NewUsers(deps)
	r := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)

	w := httptest.NewRecorder()
	h.List(w, r)

	if w.Code != 401 {
		t.Errorf("unauthenticated: status = %d, want 401", w.Code)
	}
}
