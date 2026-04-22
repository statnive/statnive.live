package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestRequireRole(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		userRole  Role // empty = no session attached
		allowed   []Role
		wantCode  int
		wantLabel string
	}{
		{"admin on admin-only", RoleAdmin, []Role{RoleAdmin}, http.StatusOK, "admin allowed"},
		{"viewer on admin-only", RoleViewer, []Role{RoleAdmin}, http.StatusForbidden, "viewer blocked"},
		{"no session on admin-only", "", []Role{RoleAdmin}, http.StatusUnauthorized, "missing session → 401"},
		{"viewer on admin+viewer", RoleViewer, []Role{RoleAdmin, RoleViewer}, http.StatusOK, "viewer allowed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := RequireRole(nil, tc.allowed...)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/x", nil)

			if tc.userRole != "" {
				u := &User{UserID: uuid.New(), SiteID: 1, Role: tc.userRole}
				req = req.WithContext(WithSession(context.Background(), u, &Session{}))
			}

			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != tc.wantCode {
				t.Errorf("%s: got %d, want %d", tc.wantLabel, w.Code, tc.wantCode)
			}
		})
	}
}
