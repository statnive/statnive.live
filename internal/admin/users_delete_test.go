package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/privacy"
)

// fakeEraser records EraseByUserID calls so tests can assert the hard-delete
// handler purges the account's data (MCP tokens + OAuth grants).
type fakeEraser struct {
	erased []uuid.UUID
	err    error
}

func (f *fakeEraser) EraseByUserID(_ context.Context, id uuid.UUID) ([]privacy.EraseResult, error) {
	f.erased = append(f.erased, id)

	return nil, f.err
}

// deleteReq builds an authenticated DELETE /api/admin/users/{id} request.
func deleteReq(t *testing.T, actor *auth.User, targetID uuid.UUID) *http.Request {
	t.Helper()

	return adminRequest(t, http.MethodDelete, "/api/admin/users/"+targetID.String(), "", actor,
		map[string]string{"id": targetID.String()})
}

func TestUsers_Delete_Success_RevokesErasesRemoves(t *testing.T) {
	t.Parallel()

	as := newFakeAuthStore()
	er := &fakeEraser{}
	deps := Deps{Auth: as, Eraser: er}
	ctx := context.Background()

	admin := newTestAdmin()
	_ = as.CreateUser(ctx, admin, "hash")
	// A second admin so the target isn't the last admin (it's a viewer anyway).
	_ = as.CreateUser(ctx, &auth.User{UserID: uuid.New(), SiteID: admin.SiteID, Email: "admin2@x.z", Role: auth.RoleAdmin}, "hash")

	target := &auth.User{UserID: uuid.New(), SiteID: admin.SiteID, Email: "v@x.z", Role: auth.RoleViewer}
	_ = as.CreateUser(ctx, target, "hash")
	// Seed an active session for the target to prove the cascade revoke.
	var sh [32]byte
	copy(sh[:], target.UserID[:])
	_ = as.CreateSession(ctx, &auth.Session{IDHash: sh, UserID: target.UserID, SiteID: target.SiteID, Role: target.Role}, [16]byte{}, "ua")

	h := NewUsers(deps)
	w := httptest.NewRecorder()
	h.Delete(w, deleteReq(t, admin, target.UserID))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	if _, err := as.GetUserByID(ctx, target.UserID); err == nil {
		t.Error("user row still present after Delete")
	}

	if len(er.erased) != 1 || er.erased[0] != target.UserID {
		t.Errorf("EraseByUserID not called for target: %v", er.erased)
	}

	if !as.revoked[sh] {
		t.Error("target session was not revoked")
	}
}

func TestUsers_Delete_RejectsSelf(t *testing.T) {
	t.Parallel()

	as := newFakeAuthStore()
	er := &fakeEraser{}
	deps := Deps{Auth: as, Eraser: er}

	admin := newTestAdmin()
	_ = as.CreateUser(context.Background(), admin, "hash")

	h := NewUsers(deps)
	w := httptest.NewRecorder()
	h.Delete(w, deleteReq(t, admin, admin.UserID))

	if w.Code != http.StatusForbidden {
		t.Fatalf("self-delete status = %d, want 403", w.Code)
	}

	if _, err := as.GetUserByID(context.Background(), admin.UserID); err != nil {
		t.Error("self-delete must not remove the actor")
	}

	if len(er.erased) != 0 {
		t.Error("self-delete must not erase data")
	}
}

func TestUsers_Delete_RejectsLastAdmin(t *testing.T) {
	t.Parallel()

	as := newFakeAuthStore()
	er := &fakeEraser{}
	deps := Deps{Auth: as, Eraser: er}
	ctx := context.Background()

	// Two admins on the same site: actor + the sole OTHER admin (the target).
	// Deleting the target would leave only the actor — allowed. To exercise the
	// guard we instead make the target the only admin besides a *disabled* one.
	actor := newTestAdmin()
	_ = as.CreateUser(ctx, actor, "hash")

	target := &auth.User{UserID: uuid.New(), SiteID: actor.SiteID, Email: "a2@x.z", Role: auth.RoleAdmin}
	_ = as.CreateUser(ctx, target, "hash")
	// Disable the actor so target is the LAST enabled admin on the site.
	_ = as.DisableUser(ctx, actor.UserID)

	h := NewUsers(deps)
	w := httptest.NewRecorder()
	h.Delete(w, deleteReq(t, actor, target.UserID))

	if w.Code != http.StatusConflict {
		t.Fatalf("last-admin delete status = %d, want 409 body=%s", w.Code, w.Body.String())
	}

	if _, err := as.GetUserByID(ctx, target.UserID); err != nil {
		t.Error("last admin must not be removed")
	}
}

func TestUsers_Delete_NotFound(t *testing.T) {
	t.Parallel()

	as := newFakeAuthStore()
	deps := Deps{Auth: as, Eraser: &fakeEraser{}}

	admin := newTestAdmin()
	_ = as.CreateUser(context.Background(), admin, "hash")

	h := NewUsers(deps)
	w := httptest.NewRecorder()
	h.Delete(w, deleteReq(t, admin, uuid.New()))

	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown-user delete status = %d, want 404", w.Code)
	}
}
