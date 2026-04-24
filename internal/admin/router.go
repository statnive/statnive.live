package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Mount attaches every /api/admin/* route to r. Caller MUST stack
// auth.RequireAuthenticated + auth.RequireRole(auth.RoleAdmin) on r
// BEFORE calling Mount — handlers in this package assume
// auth.UserFrom(r.Context()) is a non-nil admin.
//
// Route shape:
//
//	GET    /api/admin/users
//	POST   /api/admin/users
//	PATCH  /api/admin/users/{id}
//	POST   /api/admin/users/{id}/password
//	POST   /api/admin/users/{id}/disable
//	POST   /api/admin/users/{id}/enable
//	GET    /api/admin/goals
//	POST   /api/admin/goals
//	PATCH  /api/admin/goals/{id}
//	POST   /api/admin/goals/{id}/disable
//	GET    /api/admin/sites
//	POST   /api/admin/sites
//	PATCH  /api/admin/sites/{id}
func Mount(r chi.Router, deps Deps) {
	users := NewUsers(deps)
	goalsH := NewGoals(deps)
	sitesH := NewSites(deps)

	r.Method(http.MethodGet, "/api/admin/users", http.HandlerFunc(users.List))
	r.Method(http.MethodPost, "/api/admin/users", http.HandlerFunc(users.Create))
	r.Method(http.MethodPatch, "/api/admin/users/{id}", http.HandlerFunc(users.Update))
	r.Method(http.MethodPost, "/api/admin/users/{id}/password", http.HandlerFunc(users.ResetPassword))
	r.Method(http.MethodPost, "/api/admin/users/{id}/disable", http.HandlerFunc(users.Disable))
	r.Method(http.MethodPost, "/api/admin/users/{id}/enable", http.HandlerFunc(users.Enable))

	r.Method(http.MethodGet, "/api/admin/goals", http.HandlerFunc(goalsH.List))
	r.Method(http.MethodPost, "/api/admin/goals", http.HandlerFunc(goalsH.Create))
	r.Method(http.MethodPatch, "/api/admin/goals/{id}", http.HandlerFunc(goalsH.Update))
	r.Method(http.MethodPost, "/api/admin/goals/{id}/disable", http.HandlerFunc(goalsH.Disable))

	r.Method(http.MethodGet, "/api/admin/sites", http.HandlerFunc(sitesH.List))
	r.Method(http.MethodPost, "/api/admin/sites", http.HandlerFunc(sitesH.Create))
	r.Method(http.MethodPatch, "/api/admin/sites/{id}", http.HandlerFunc(sitesH.Update))
}
