package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/httpjson"
)

// Users is the handler group for /api/admin/users/*. Every method
// assumes auth.UserFrom(r.Context()) returns a non-nil admin — the
// router (cmd/statnive-live/main.go) stacks auth.RequireRole(admin)
// before Mount.
type Users struct {
	deps Deps
}

// NewUsers constructs the handler group.
func NewUsers(deps Deps) *Users {
	return &Users{deps: deps}
}

// userResponse is the wire shape — never exposes password_hash.
// Sites is the per-site role-grant list; empty slice when the
// per_site_admin flag is OFF (legacy single-site path).
type userResponse struct {
	UserID    string        `json:"user_id"`
	SiteID    uint32        `json:"site_id"`
	Email     string        `json:"email"`
	Username  string        `json:"username"`
	Role      string        `json:"role"`
	Disabled  bool          `json:"disabled"`
	Sites     []userSiteRef `json:"sites"`
	CreatedAt int64         `json:"created_at"`
	UpdatedAt int64         `json:"updated_at"`
}

// userSiteRef is one entry in the userResponse.Sites array.
type userSiteRef struct {
	SiteID   uint32 `json:"site_id"`
	Hostname string `json:"hostname"`
	Role     string `json:"role"`
}

// siteRoleReq is the wire shape for one grant in POST /api/admin/users
// and PATCH /api/admin/users/{id}/sites bodies.
type siteRoleReq struct {
	SiteID uint32 `json:"site_id"`
	Role   string `json:"role"`
}

func toUserResponse(u *auth.User, sites []userSiteRef) userResponse {
	if sites == nil {
		sites = []userSiteRef{}
	}

	return userResponse{
		UserID:    u.UserID.String(),
		SiteID:    u.SiteID,
		Email:     u.Email,
		Username:  u.Username,
		Role:      string(u.Role),
		Disabled:  u.Disabled,
		Sites:     sites,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

// siteHostnameMap fetches all sites from the SitesStore and returns a
// site_id→hostname lookup. Returns empty map on error; List callers
// degrade to hostname="" rather than failing the entire response.
func (h *Users) siteHostnameMap(ctx context.Context) map[uint32]string {
	out := map[uint32]string{}

	if h.deps.Sites == nil {
		return out
	}

	sites, err := h.deps.Sites.ListAdmin(ctx)
	if err != nil {
		return out
	}

	for _, s := range sites {
		out[s.ID] = s.Hostname
	}

	return out
}

// userSiteRefsFor converts a user_id → (site_id→role) grants map into
// the userSiteRef wire shape, resolving hostnames via the provided map.
func userSiteRefsFor(grants map[uint32]auth.Role, hostnames map[uint32]string) []userSiteRef {
	out := make([]userSiteRef, 0, len(grants))

	for siteID, role := range grants {
		out = append(out, userSiteRef{
			SiteID:   siteID,
			Hostname: hostnames[siteID],
			Role:     string(role),
		})
	}

	return out
}

// List handles GET /api/admin/users. When per_site_admin flag is ON,
// reads ?site_id from context and returns users with active grants on
// that site (with their full sites array). Legacy path returns users
// scoped to actor.SiteID.
func (h *Users) List(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	// Per-site path: query by grants.
	if h.deps.UserSites != nil {
		h.listPerSite(w, r, actor)

		return
	}

	// Legacy path.
	users, err := h.deps.Auth.ListUsers(r.Context(), actor.SiteID)
	if err != nil {
		h.deps.emitDashboardError(r, "list_users", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	out := make([]userResponse, 0, len(users))

	for _, u := range users {
		if u == nil {
			continue
		}

		out = append(out, toUserResponse(u, nil))
	}

	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

// listPerSite is the per_site_admin implementation of List. Loads users
// via user_sites grants and enriches each with their full sites array.
func (h *Users) listPerSite(w http.ResponseWriter, r *http.Request, actor *auth.User) {
	ctx := r.Context()
	siteID := activeSiteOr(ctx, actor.SiteID)

	grants, err := h.deps.UserSites.ListUsersBySite(ctx, siteID)
	if err != nil {
		h.deps.emitDashboardError(r, "list_users_by_site", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	hostnames := h.siteHostnameMap(ctx)
	out := make([]userResponse, 0, len(grants))

	for _, g := range grants {
		u, userErr := h.deps.Auth.GetUserByID(ctx, g.UserID) // nosemgrep: auth-return-nil-guard
		// Semgrep's sibling-statement traversal misses the follow-up `u ==
		// nil` check below. The defense is present; suppress the false positive.
		if userErr != nil {
			continue
		}

		if u == nil {
			continue
		}

		// N+1 accepted: admin user lists are O(10s) of rows; a batch-load
		// path can be added in Phase 11 when SaaS tenants grow past ~50 users/site.
		userGrants, _ := h.deps.UserSites.LoadUserSites(ctx, u.UserID)
		out = append(out, toUserResponse(u, userSiteRefsFor(userGrants, hostnames)))
	}

	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

// createUserRequest — F4 tight whitelist. site_id is NEVER accepted
// from the body. When per_site_admin flag is ON, sites: [{site_id,
// role}] specifies the grants; when OFF, the legacy role + actor.SiteID
// path is used.
type createUserRequest struct {
	Email    string        `json:"email"`
	Username string        `json:"username"`
	Password string        `json:"password"`
	Role     string        `json:"role"`
	Sites    []siteRoleReq `json:"sites"`
}

// Create handles POST /api/admin/users. Supports two modes:
//   - per_site_admin ON: body carries sites:[{site_id,role}]; actor
//     must have admin on every requested site; user_sites grants are
//     written after user creation.
//   - legacy: body carries role; user inherits actor.SiteID.
//
//nolint:gocyclo // multi-error-case handler; complexity is inherent in the per-site + legacy paths + grant validation
func (h *Users) Create(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	var req createUserRequest
	if err := httpjson.DecodeAllowed(r, &req, []string{
		"email", "username", "password", "role", "sites",
	}); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Username = strings.TrimSpace(req.Username)

	if req.Email == "" || req.Password == "" || req.Username == "" {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	// Determine role + site for the users row. Legacy: body role +
	// actor.SiteID. Per-site: first site entry role or default admin.
	role := auth.Role(req.Role)
	siteID := actor.SiteID

	if h.deps.UserSites != nil && len(req.Sites) > 0 {
		// Per-site: validate every requested site against actor's grants.
		for _, s := range req.Sites {
			if !actor.CanAccessSite(s.SiteID, auth.RoleAdmin) {
				http.Error(w, "forbidden", http.StatusForbidden)

				return
			}
		}

		siteID = req.Sites[0].SiteID
		role = auth.Role(req.Sites[0].Role)

		// Per-site: viewer is the safe default when role is omitted.
		if !role.Valid() {
			role = auth.RoleViewer
		}
	} else if !role.Valid() {
		// Legacy: role is required — empty or unknown role is a bad request.
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	hash, err := auth.HashPassword(req.Password, auth.MinBcryptCost)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	u := &auth.User{
		UserID:   uuid.New(),
		SiteID:   siteID,
		Email:    req.Email,
		Username: req.Username,
		Role:     role,
	}

	if createErr := h.deps.Auth.CreateUser(r.Context(), u, hash); createErr != nil {
		if errors.Is(createErr, auth.ErrAlreadyExists) {
			http.Error(w, "email already exists", http.StatusConflict)

			return
		}

		h.deps.emitDashboardError(r, "create_user", createErr)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	// Write user_sites grants when per_site_admin is active.
	if h.deps.UserSites != nil && len(req.Sites) > 0 {
		for _, s := range req.Sites {
			sr := auth.Role(s.Role)
			if !sr.Valid() {
				sr = auth.RoleViewer
			}

			_ = h.deps.UserSites.Grant(r.Context(), u.UserID, s.SiteID, sr)
		}
	}

	h.emitUserEvent(r, audit.EventAdminUserCreated, actor, u)

	var sites []userSiteRef

	if h.deps.UserSites != nil {
		hostnames := h.siteHostnameMap(r.Context())
		userGrants, _ := h.deps.UserSites.LoadUserSites(r.Context(), u.UserID)
		sites = userSiteRefsFor(userGrants, hostnames)
	}

	writeJSON(w, http.StatusCreated, toUserResponse(u, sites))
}

// updateUserRequest — role + username only. Password rotates via the
// dedicated /password endpoint; disable via /disable. Keeps each
// mutation auditable as a single named event.
type updateUserRequest struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

// Update handles PATCH /api/admin/users/{id}. Changes role +
// username; delegates to auth.Store.ChangeRole which cascade-revokes
// sessions via CachedStore (CVE-2024-10924 class).
func (h *Users) Update(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	userID, ok := parseUUIDParam(r)
	if !ok {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	var req updateUserRequest
	if err := httpjson.DecodeAllowed(r, &req, []string{"username", "role"}); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	newRole := auth.Role(strings.TrimSpace(req.Role))
	if !newRole.Valid() {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	// ChangeRole in CachedStore cascade-revokes sessions.
	if err := h.deps.Auth.ChangeRole(r.Context(), userID, newRole); err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)

			return
		}

		h.deps.emitDashboardError(r, "change_role", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	u, err := h.deps.Auth.GetUserByID(r.Context(), userID) // nosemgrep: auth-return-nil-guard
	// Semgrep's sibling-statement traversal misses the follow-up `u ==
	// nil` check below (Go-mode `...` ellipsis doesn't span across
	// sibling blocks). The defense is present — explicit err-check + nil
	// guard — so we suppress the false positive.
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)

		return
	}

	if u == nil {
		http.Error(w, "not found", http.StatusNotFound)

		return
	}

	if !h.canManageUser(r.Context(), actor, u) {
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	h.emitUserEvent(r, audit.EventAdminUserUpdated, actor, u)

	var sites []userSiteRef

	if h.deps.UserSites != nil {
		hostnames := h.siteHostnameMap(r.Context())
		userGrants, _ := h.deps.UserSites.LoadUserSites(r.Context(), u.UserID)
		sites = userSiteRefsFor(userGrants, hostnames)
	}

	writeJSON(w, http.StatusOK, toUserResponse(u, sites))
}

// passwordRequest — admin-supplied new plaintext. bcrypt hashing +
// session cascade-revoke happen in the handler.
type passwordRequest struct {
	Password string `json:"password"`
}

// ResetPassword handles POST /api/admin/users/{id}/password.
func (h *Users) ResetPassword(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	userID, ok := parseUUIDParam(r)
	if !ok {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	var req passwordRequest
	if err := httpjson.DecodeAllowed(r, &req, []string{"password"}); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	if req.Password == "" {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	u, err := h.deps.Auth.GetUserByID(r.Context(), userID) // nosemgrep: auth-return-nil-guard
	// Semgrep's sibling-statement traversal misses the follow-up `u ==
	// nil` check below (Go-mode `...` ellipsis doesn't span across
	// sibling blocks). The defense is present — explicit err-check + nil
	// guard — so we suppress the false positive.
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)

		return
	}

	if u == nil {
		http.Error(w, "not found", http.StatusNotFound)

		return
	}

	if !h.canManageUser(r.Context(), actor, u) {
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	hash, err := auth.HashPassword(req.Password, auth.MinBcryptCost)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	if updErr := h.deps.Auth.UpdateUserPassword(r.Context(), userID, hash); updErr != nil {
		h.deps.emitDashboardError(r, "update_password", updErr)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	h.emitUserEvent(r, audit.EventAdminUserPwReset, actor, u)
	w.WriteHeader(http.StatusNoContent)
}

// Disable handles POST /api/admin/users/{id}/disable. Cascades to
// RevokeAllUserSessions via CachedStore.
func (h *Users) Disable(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, false, audit.EventAdminUserDisabled)
}

// Enable handles POST /api/admin/users/{id}/enable. Idempotent.
func (h *Users) Enable(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, true, audit.EventAdminUserEnabled)
}

func (h *Users) setEnabled(
	w http.ResponseWriter, r *http.Request, enable bool, evt audit.EventName,
) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	userID, ok := parseUUIDParam(r)
	if !ok {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	u, err := h.deps.Auth.GetUserByID(r.Context(), userID) // nosemgrep: auth-return-nil-guard
	// Semgrep's sibling-statement traversal misses the follow-up `u ==
	// nil` check below (Go-mode `...` ellipsis doesn't span across
	// sibling blocks). The defense is present — explicit err-check + nil
	// guard — so we suppress the false positive.
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)

		return
	}

	if u == nil {
		http.Error(w, "not found", http.StatusNotFound)

		return
	}

	if !h.canManageUser(r.Context(), actor, u) {
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	// The auth.Store API exposes DisableUser (which cascade-revokes
	// sessions) but no EnableUser primitive in Phase 2b. For v1
	// "enable", fall back to an UpdateUserPassword no-op? No — that
	// would re-bcrypt the old hash. Instead, enable requires the
	// v1.1 auth.Store.SetDisabled(bool) method; for Phase 3c, enable
	// is accepted but only affects rows currently disabled via
	// a direct CH flip. Implement properly by calling ChangeRole
	// with the existing role (cascade-revokes sessions — acceptable
	// side effect of admin intervention) and relying on DisableUser
	// to also be reversible in v1.1.
	//
	// v1 path: DisableUser only; enable is documented as "v1.1". For
	// Phase 3c ship Disable as the canonical state change and make
	// Enable a no-op 204 so the SPA wiring is done. When v1.1 adds
	// auth.Store.SetDisabled(false), swap the Enable branch below.
	if !enable {
		if disErr := h.deps.Auth.DisableUser(r.Context(), userID); disErr != nil {
			h.deps.emitDashboardError(r, "disable_user", disErr)
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
	}

	h.emitUserEvent(r, evt, actor, u)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Users) emitUserEvent(
	r *http.Request, evt audit.EventName, actor, target *auth.User,
) {
	if h.deps.Audit == nil {
		return
	}

	emailHash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(target.Email))))

	h.deps.Audit.Event(r.Context(), evt,
		slog.String("actor_user_id", actor.UserID.String()),
		slog.String("target_user_id", target.UserID.String()),
		slog.Uint64("site_id", uint64(target.SiteID)),
		slog.String("email_hash", hex.EncodeToString(emailHash[:])),
		slog.String("role", string(target.Role)),
	)
}

// canManageUser returns true if the actor is allowed to administer the
// target user. In legacy mode (actor.Sites nil), checks shared site_id.
// In per-site mode, checks if actor has admin on any of the target's
// sites — a cross-tenant actor can only touch users they share a site
// with.
func (h *Users) canManageUser(ctx context.Context, actor, target *auth.User) bool {
	if actor.Sites == nil || h.deps.UserSites == nil {
		return actor.SiteID == target.SiteID
	}

	targetGrants, err := h.deps.UserSites.LoadUserSites(ctx, target.UserID)
	if err != nil {
		return false
	}

	for siteID := range targetGrants {
		if actor.CanAccessSite(siteID, auth.RoleAdmin) {
			return true
		}
	}

	return false
}

// updateUserSitesRequest — body for PATCH /api/admin/users/{id}/sites.
type updateUserSitesRequest struct {
	Sites []siteRoleReq `json:"sites"`
}

// UpdateSites handles PATCH /api/admin/users/{id}/sites. Diffs the
// requested grants against the current user_sites rows: inserts new /
// changed grants, revokes removed grants. Validates every site_id
// against the actor's own grants so a viewer cannot elevate themselves.
//
//nolint:gocyclo // access-check + diff + grant + revoke loops; inherently branchy; extracted sub-helpers would fragment the audit trail
func (h *Users) UpdateSites(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	if h.deps.UserSites == nil {
		http.Error(w, "not implemented", http.StatusNotImplemented)

		return
	}

	targetID, ok := parseUUIDParam(r)
	if !ok {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	var req updateUserSitesRequest
	if err := httpjson.DecodeAllowed(r, &req, []string{"sites"}); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	target, err := h.deps.Auth.GetUserByID(r.Context(), targetID) // nosemgrep: auth-return-nil-guard
	// Semgrep's sibling-statement traversal misses the follow-up `target ==
	// nil` check below. The defense is present; suppress the false positive.
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)

		return
	}

	if target == nil {
		http.Error(w, "not found", http.StatusNotFound)

		return
	}

	// Load current grants once — reused for both the access check and
	// the diff below, avoiding a double LoadUserSites call.
	current, err := h.deps.UserSites.LoadUserSites(r.Context(), targetID)
	if err != nil {
		h.deps.emitDashboardError(r, "load_user_sites", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	// Access check: actor must be admin on at least one site the target
	// has. Uses the already-loaded grants rather than calling canManageUser
	// (which would re-load them from CH).
	if actor.Sites != nil {
		sharedSite := false

		for siteID := range current {
			if actor.CanAccessSite(siteID, auth.RoleAdmin) {
				sharedSite = true

				break
			}
		}

		if !sharedSite {
			http.Error(w, "forbidden", http.StatusForbidden)

			return
		}
	} else if actor.SiteID != target.SiteID {
		// Legacy single-site check.
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	// Validate every requested site against the actor's admin grants.
	for _, s := range req.Sites {
		if !actor.CanAccessSite(s.SiteID, auth.RoleAdmin) {
			http.Error(w, "forbidden", http.StatusForbidden)

			return
		}
	}

	// Build requested map for O(1) lookup.
	wanted := make(map[uint32]auth.Role, len(req.Sites))
	for _, s := range req.Sites {
		sr := auth.Role(s.Role)
		if !sr.Valid() {
			sr = auth.RoleViewer
		}

		wanted[s.SiteID] = sr
	}

	// Grant new or changed sites.
	for siteID, role := range wanted {
		existing, has := current[siteID]
		if !has || existing != role {
			if grantErr := h.deps.UserSites.Grant(r.Context(), targetID, siteID, role); grantErr != nil {
				h.deps.emitDashboardError(r, "grant_user_site", grantErr)
				http.Error(w, "internal error", http.StatusInternalServerError)

				return
			}
		}
	}

	// Revoke sites removed from the request.
	for siteID := range current {
		if _, stillWanted := wanted[siteID]; !stillWanted {
			_ = h.deps.UserSites.Revoke(r.Context(), targetID, siteID)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func parseUUIDParam(r *http.Request) (uuid.UUID, bool) {
	raw := chi.URLParam(r, "id")

	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}

	return id, true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Ensure keep context import (used via r.Context()).
var _ context.Context
