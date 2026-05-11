package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/httpjson"
	"github.com/statnive/statnive.live/internal/sites"
)

// Sites is the handler group for /api/admin/sites/*. Phase 6-polish
// (first-run UX) — lets an admin create + disable + list sites via the
// dashboard instead of raw `INSERT INTO statnive.sites` in ClickHouse.
type Sites struct {
	deps Deps
}

// NewSites constructs the handler group.
func NewSites(deps Deps) *Sites {
	return &Sites{deps: deps}
}

type siteAdminResponse struct {
	SiteID     uint32 `json:"site_id"`
	Hostname   string `json:"hostname"`
	Slug       string `json:"slug"`
	Plan       string `json:"plan"`
	Enabled    bool   `json:"enabled"`
	TZ         string `json:"tz"`
	Currency   string `json:"currency"`
	CreatedAt  int64  `json:"created_at"`
	RespectDNT bool   `json:"respect_dnt"`
	RespectGPC bool   `json:"respect_gpc"`
	TrackBots  bool   `json:"track_bots"`
}

func toSiteResponse(s sites.SiteAdmin) siteAdminResponse {
	return siteAdminResponse{
		SiteID:     s.ID,
		Hostname:   s.Hostname,
		Slug:       s.Slug,
		Plan:       s.Plan,
		Enabled:    s.Enabled,
		TZ:         s.TZ,
		Currency:   s.Currency,
		CreatedAt:  s.CreatedAt,
		RespectDNT: s.RespectDNT,
		RespectGPC: s.RespectGPC,
		TrackBots:  s.TrackBots,
	}
}

// List handles GET /api/admin/sites. When per_site_admin flag is ON,
// returns only sites the actor holds any grant on; otherwise returns
// every registered site (legacy global view for single-operator deploys).
func (h *Sites) List(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	list, err := h.deps.Sites.ListAdmin(r.Context())
	if err != nil {
		h.deps.emitDashboardError(r, "list_sites", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	out := make([]siteAdminResponse, 0, len(list))

	// Per-site mode: filter to only the actor's accessible sites.
	// Legacy mode (actor.Sites nil): all sites are returned as before.
	for _, s := range list {
		if actor.Sites != nil && !actor.CanAccessSite(s.ID, auth.RoleViewer) {
			continue
		}

		out = append(out, toSiteResponse(s))
	}

	writeJSON(w, http.StatusOK, map[string]any{"sites": out})
}

// createSiteRequest — tight allow-list; enabled/plan/created_at are
// server-set, never accepted from the body. Currency + TZ default to
// EUR + Europe/Berlin when omitted (sites.DefaultCurrency /
// sites.DefaultTimezone) — operator PATCHes per-site after creation.
type createSiteRequest struct {
	Hostname string `json:"hostname"`
	Slug     string `json:"slug"`
	TZ       string `json:"tz"`
	Currency string `json:"currency"`
}

// Create handles POST /api/admin/sites. Returns 201 with the full site
// projection on success, 409 on hostname-taken or slug-taken, 400 on
// invalid hostname or unknown field.
//
//nolint:gocyclo // switch over 7 distinct store error types; each must map to a specific HTTP status
func (h *Sites) Create(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	var req createSiteRequest
	if err := httpjson.DecodeAllowed(r, &req, []string{
		"hostname", "slug", "tz", "currency",
	}); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	hostname := strings.ToLower(strings.TrimSpace(req.Hostname))
	siteID, err := h.deps.Sites.CreateSite(r.Context(), hostname, req.Slug, req.TZ, req.Currency)

	switch {
	case errors.Is(err, sites.ErrInvalidHostname):
		h.emitSiteRejected(r, actor, hostname, err)
		http.Error(w, "invalid hostname", http.StatusBadRequest)

		return
	case errors.Is(err, sites.ErrInvalidCurrency):
		h.emitSiteRejected(r, actor, hostname, err)
		http.Error(w, "invalid currency", http.StatusBadRequest)

		return
	case errors.Is(err, sites.ErrInvalidTimezone):
		h.emitSiteRejected(r, actor, hostname, err)
		http.Error(w, "invalid timezone", http.StatusBadRequest)

		return
	case errors.Is(err, sites.ErrHostnameTaken):
		h.emitSiteRejected(r, actor, hostname, err)
		http.Error(w, "hostname taken", http.StatusConflict)

		return
	case errors.Is(err, sites.ErrSlugTaken):
		h.emitSiteRejected(r, actor, hostname, err)
		http.Error(w, "slug taken", http.StatusConflict)

		return
	case err != nil:
		h.deps.emitDashboardError(r, "create_site", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	// Re-read via ListAdmin so the response carries server-computed fields
	// (slug when auto-generated, plan, created_at). Cheap — sites is a
	// low-cardinality table.
	list, err := h.deps.Sites.ListAdmin(r.Context())
	if err != nil {
		h.deps.emitDashboardError(r, "list_after_create", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	var created *sites.SiteAdmin

	for i := range list {
		if list[i].ID == siteID {
			created = &list[i]

			break
		}
	}

	if created == nil {
		h.deps.emitDashboardError(r, "create_site_lookup", errors.New("site missing after insert"))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	// Auto-grant the creating actor admin on the new site so they can
	// immediately manage it. No-op when UserSites is nil (legacy mode).
	if h.deps.UserSites != nil {
		_ = h.deps.UserSites.Grant(r.Context(), actor.UserID, siteID, auth.RoleAdmin)
	}

	h.emitSiteEvent(r, audit.EventAdminSiteCreated, actor, *created)
	writeJSON(w, http.StatusCreated, toSiteResponse(*created))
}

// updateSiteRequest — Phase 6 supports enable/disable. PR D2 adds the
// per-site privacy + bot policy (CLAUDE.md Privacy Rule 6 + migration
// 006). Per-site-currency PR adds Currency + TZ. Pointer fields so the
// handler can distinguish "field omitted" (no change) from "field set
// to zero value" (apply it). Slug + plan mutations still land when
// operator demand justifies them.
type updateSiteRequest struct {
	Enabled    *bool   `json:"enabled,omitempty"`
	RespectDNT *bool   `json:"respect_dnt,omitempty"`
	RespectGPC *bool   `json:"respect_gpc,omitempty"`
	TrackBots  *bool   `json:"track_bots,omitempty"`
	Currency   *string `json:"currency,omitempty"`
	TZ         *string `json:"tz,omitempty"`
}

// Update handles PATCH /api/admin/sites/{id}. Payload may include any
// combination of {enabled, respect_dnt, respect_gpc, track_bots,
// currency, tz}; anything else fails the F4 unknown-field guard. Empty
// payloads short-circuit with the existing site projection (idempotent
// no-op).
func (h *Sites) Update(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	siteID, ok := parseSiteIDParam(r)
	if !ok {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	var req updateSiteRequest
	if err := httpjson.DecodeAllowed(r, &req, []string{
		"enabled", "respect_dnt", "respect_gpc", "track_bots", "currency", "tz",
	}); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	if status := h.applyPolicyPatch(r, siteID, req); status != 0 {
		http.Error(w, http.StatusText(status), status)

		return
	}

	if status := h.applyAttributesPatch(r, siteID, req); status != 0 {
		http.Error(w, http.StatusText(status), status)

		return
	}

	if req.Enabled != nil {
		if status := h.applyEnabledPatch(r, siteID, *req.Enabled); status != 0 {
			http.Error(w, http.StatusText(status), status)

			return
		}
	}

	// Re-read for response parity with Create.
	list, err := h.deps.Sites.ListAdmin(r.Context())
	if err != nil {
		h.deps.emitDashboardError(r, "list_after_update", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	for _, s := range list {
		if s.ID == siteID {
			h.emitSiteEvent(r, audit.EventAdminSiteUpdated, actor, s)
			writeJSON(w, http.StatusOK, toSiteResponse(s))

			return
		}
	}

	http.Error(w, "not found", http.StatusNotFound)
}

// applyPolicyPatch read-modify-writes the per-site DNT/GPC/track_bots
// policy when at least one of those fields is present in req. Returns
// 0 on success / no-op (no policy fields), or an HTTP status code on
// failure (404 for unknown site, 500 otherwise). Splits the
// ListAdmin-free read + UpdateSitePolicy write out of Update so the
// caller stays under the gocyclo threshold.
func (h *Sites) applyPolicyPatch(r *http.Request, siteID uint32, req updateSiteRequest) int {
	if req.RespectDNT == nil && req.RespectGPC == nil && req.TrackBots == nil {
		return 0
	}

	current, lookupErr := h.deps.Sites.LookupSiteByID(r.Context(), siteID)
	if lookupErr != nil {
		if errors.Is(lookupErr, sites.ErrUnknownHostname) {
			return http.StatusNotFound
		}

		h.deps.emitDashboardError(r, "lookup_for_update", lookupErr)

		return http.StatusInternalServerError
	}

	policy := current.SitePolicy
	if req.RespectDNT != nil {
		policy.RespectDNT = *req.RespectDNT
	}

	if req.RespectGPC != nil {
		policy.RespectGPC = *req.RespectGPC
	}

	if req.TrackBots != nil {
		policy.TrackBots = *req.TrackBots
	}

	if err := h.deps.Sites.UpdateSitePolicy(r.Context(), siteID, policy); err != nil {
		h.deps.emitDashboardError(r, "update_site_policy", err)

		return http.StatusInternalServerError
	}

	return 0
}

// applyAttributesPatch read-modify-writes the per-site display
// attributes (currency + tz) when at least one of those fields is
// present in req. Returns 0 on success / no-op (no attribute fields),
// or an HTTP status code on failure (404 for unknown site, 400 for
// invalid currency/tz, 500 otherwise). Mirror of applyPolicyPatch.
func (h *Sites) applyAttributesPatch(r *http.Request, siteID uint32, req updateSiteRequest) int {
	if req.Currency == nil && req.TZ == nil {
		return 0
	}

	err := h.deps.Sites.UpdateSiteAttributes(r.Context(), siteID, req.Currency, req.TZ)

	switch {
	case errors.Is(err, sites.ErrUnknownHostname):
		return http.StatusNotFound
	case errors.Is(err, sites.ErrInvalidCurrency), errors.Is(err, sites.ErrInvalidTimezone):
		return http.StatusBadRequest
	case err != nil:
		h.deps.emitDashboardError(r, "update_site_attributes", err)

		return http.StatusInternalServerError
	}

	return 0
}

// applyEnabledPatch flips the enabled flag and maps the registry error
// to an HTTP status code. Mirror of applyPolicyPatch's shape so
// Update() composes them linearly.
func (h *Sites) applyEnabledPatch(r *http.Request, siteID uint32, enabled bool) int {
	if err := h.deps.Sites.UpdateSiteEnabled(r.Context(), siteID, enabled); err != nil {
		if errors.Is(err, sites.ErrUnknownHostname) {
			return http.StatusNotFound
		}

		h.deps.emitDashboardError(r, "update_site", err)

		return http.StatusInternalServerError
	}

	return 0
}

func (h *Sites) emitSiteEvent(
	r *http.Request, evt audit.EventName, actor *auth.User, s sites.SiteAdmin,
) {
	if h.deps.Audit == nil {
		return
	}

	h.deps.Audit.Event(r.Context(), evt,
		slog.String("actor_user_id", actor.UserID.String()),
		slog.Uint64("site_id", uint64(s.ID)),
		slog.String("slug", s.Slug),
		slog.Bool("enabled", s.Enabled),
	)
}

func (h *Sites) emitSiteRejected(
	r *http.Request, actor *auth.User, hostname string, err error,
) {
	if h.deps.Audit == nil {
		return
	}

	h.deps.Audit.Event(r.Context(), audit.EventAdminSiteRejected,
		slog.String("actor_user_id", actor.UserID.String()),
		slog.String("hostname", hostname),
		slog.String("reason", err.Error()),
	)
}

func parseSiteIDParam(r *http.Request) (uint32, bool) {
	raw := chi.URLParam(r, "id")

	id, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || id == 0 {
		return 0, false
	}

	return uint32(id), true
}
