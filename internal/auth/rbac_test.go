package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestRequireRole_AllowsPermitted(t *testing.T) {
	u := &User{UserID: uuid.New(), SiteID: 1, Role: RoleAdmin}

	h := RequireRole(nil, RoleAdmin)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req = req.WithContext(WithSession(context.Background(), u, &Session{}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("admin denied: %d", w.Code)
	}
}

func TestRequireRole_RejectsOther(t *testing.T) {
	u := &User{UserID: uuid.New(), SiteID: 1, Role: RoleViewer}

	h := RequireRole(nil, RoleAdmin)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req = req.WithContext(WithSession(context.Background(), u, &Session{}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("viewer not blocked: %d", w.Code)
	}
}

func TestRequireRole_NoUserFailsClosed(t *testing.T) {
	h := RequireRole(nil, RoleAdmin)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("missing user did not fail closed: %d", w.Code)
	}
}

func TestRequireRole_MultipleAllowed(t *testing.T) {
	u := &User{UserID: uuid.New(), SiteID: 1, Role: RoleViewer}

	h := RequireRole(nil, RoleAdmin, RoleViewer)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/stats/overview", nil)
	req = req.WithContext(WithSession(context.Background(), u, &Session{}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("viewer blocked from viewer-allowed route: %d", w.Code)
	}
}
