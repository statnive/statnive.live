package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/goals"
	"github.com/statnive/statnive.live/internal/httpjson"
)

// Goals is the handler group for /api/admin/goals/*. Router wraps it
// with auth.RequireRole(admin); handlers assume UserFrom(ctx) is a
// non-nil admin.
type Goals struct {
	deps Deps
}

// NewGoals constructs the handler group.
func NewGoals(deps Deps) *Goals {
	return &Goals{deps: deps}
}

type goalResponse struct {
	GoalID    string `json:"goal_id"`
	SiteID    uint32 `json:"site_id"`
	Hostname  string `json:"hostname"`
	Name      string `json:"name"`
	MatchType string `json:"match_type"`
	Pattern   string `json:"pattern"`
	Value     uint64 `json:"value"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

func toGoalResponse(g *goals.Goal, hostname string) goalResponse {
	return goalResponse{
		GoalID:    g.GoalID.String(),
		SiteID:    g.SiteID,
		Hostname:  hostname,
		Name:      g.Name,
		MatchType: string(g.MatchType),
		Pattern:   g.Pattern,
		Value:     g.Value,
		Enabled:   g.Enabled,
		CreatedAt: g.CreatedAt,
		UpdatedAt: g.UpdatedAt,
	}
}

// List handles GET /api/admin/goals — all goals for the active site.
// When RequireSiteRole middleware ran (per_site_admin flag ON), the
// site_id comes from ActiveSiteIDFromContext; otherwise falls back to
// actor.SiteID (legacy single-site path).
//
//nolint:dupl // symmetric with Users.List but over a different entity.
func (h *Goals) List(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	siteID := activeSiteOr(r.Context(), actor.SiteID)

	list, err := h.deps.Goals.List(r.Context(), siteID)
	if err != nil {
		h.deps.emitDashboardError(r, "list_goals", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	// One hostname lookup per List — all goals share the same site_id.
	hostname := hostnameFor(r.Context(), h.deps.Sites, siteID)

	out := make([]goalResponse, 0, len(list))

	for _, g := range list {
		if g == nil {
			continue
		}

		out = append(out, toGoalResponse(g, hostname))
	}

	writeJSON(w, http.StatusOK, map[string]any{"goals": out})
}

// createGoalRequest — tight whitelist. site_id comes from session.
type createGoalRequest struct {
	Name      string `json:"name"`
	MatchType string `json:"match_type"`
	Pattern   string `json:"pattern"`
	Value     uint64 `json:"value"`
	Enabled   bool   `json:"enabled"`
}

// Create handles POST /api/admin/goals. Write-time validation rejects
// patterns > MaxPatternLen (128 chars); on success, reloads the
// in-memory Snapshot so the ingest pipeline picks up the new goal
// within milliseconds.
func (h *Goals) Create(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	var req createGoalRequest
	if err := httpjson.DecodeAllowed(r, &req, []string{
		"name", "match_type", "pattern", "value", "enabled",
	}); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	g := &goals.Goal{
		SiteID:    activeSiteOr(r.Context(), actor.SiteID), // middleware or session
		Name:      strings.TrimSpace(req.Name),
		MatchType: goals.MatchType(req.MatchType),
		Pattern:   strings.TrimSpace(req.Pattern),
		Value:     req.Value,
		Enabled:   req.Enabled,
	}

	if err := h.deps.Goals.Create(r.Context(), g); err != nil {
		if errors.Is(err, goals.ErrInvalidInput) {
			h.emitGoalRejected(r, actor, g, err)
			http.Error(w, "bad request", http.StatusBadRequest)

			return
		}

		h.deps.emitDashboardError(r, "create_goal", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	h.reloadSnapshot(r.Context())
	h.emitGoalEvent(r, audit.EventAdminGoalCreated, actor, g)
	hostname := hostnameFor(r.Context(), h.deps.Sites, g.SiteID)
	writeJSON(w, http.StatusCreated, toGoalResponse(g, hostname))
}

// updateGoalRequest — editable fields. site_id / goal_id come from
// path + session.
type updateGoalRequest struct {
	Name      string `json:"name"`
	MatchType string `json:"match_type"`
	Pattern   string `json:"pattern"`
	Value     uint64 `json:"value"`
	Enabled   bool   `json:"enabled"`
}

// Update handles PATCH /api/admin/goals/{id}.
func (h *Goals) Update(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	goalID, ok := parseUUIDParam(r)
	if !ok {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	var req updateGoalRequest
	if err := httpjson.DecodeAllowed(r, &req, []string{
		"name", "match_type", "pattern", "value", "enabled",
	}); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	g := &goals.Goal{
		GoalID:    goalID,
		SiteID:    activeSiteOr(r.Context(), actor.SiteID), // middleware or session
		Name:      strings.TrimSpace(req.Name),
		MatchType: goals.MatchType(req.MatchType),
		Pattern:   strings.TrimSpace(req.Pattern),
		Value:     req.Value,
		Enabled:   req.Enabled,
	}

	if err := h.deps.Goals.Update(r.Context(), g); err != nil {
		switch {
		case errors.Is(err, goals.ErrNotFound):
			http.Error(w, "not found", http.StatusNotFound)
		case errors.Is(err, goals.ErrInvalidInput):
			h.emitGoalRejected(r, actor, g, err)
			http.Error(w, "bad request", http.StatusBadRequest)
		default:
			h.deps.emitDashboardError(r, "update_goal", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}

		return
	}

	h.reloadSnapshot(r.Context())
	h.emitGoalEvent(r, audit.EventAdminGoalUpdated, actor, g)
	hostname := hostnameFor(r.Context(), h.deps.Sites, g.SiteID)
	writeJSON(w, http.StatusOK, toGoalResponse(g, hostname))
}

// Disable handles POST /api/admin/goals/{id}/disable. Soft delete —
// goal stays in CH for audit trail, ingest matcher skips it via
// Snapshot's enabled-only ListActive feed.
func (h *Goals) Disable(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	goalID, ok := parseUUIDParam(r)
	if !ok {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	siteID := activeSiteOr(r.Context(), actor.SiteID)
	if err := h.deps.Goals.Disable(r.Context(), siteID, goalID); err != nil {
		if errors.Is(err, goals.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)

			return
		}

		h.deps.emitDashboardError(r, "disable_goal", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	h.reloadSnapshot(r.Context())
	h.emitGoalEvent(r, audit.EventAdminGoalDisabled, actor,
		&goals.Goal{GoalID: goalID, SiteID: siteID})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Goals) reloadSnapshot(ctx context.Context) {
	if h.deps.Snapshot == nil {
		return
	}

	if err := h.deps.Snapshot.Reload(ctx); err != nil && h.deps.Audit != nil {
		h.deps.Audit.Event(ctx, audit.EventGoalsReloadFailed,
			slog.String("err", err.Error()),
		)

		return
	}

	if h.deps.Audit != nil {
		h.deps.Audit.Event(ctx, audit.EventGoalsReloadOK)
	}
}

func (h *Goals) emitGoalEvent(
	r *http.Request, evt audit.EventName, actor *auth.User, g *goals.Goal,
) {
	if h.deps.Audit == nil {
		return
	}

	patternHash := sha256.Sum256([]byte(g.Pattern))
	h.deps.Audit.Event(r.Context(), evt,
		slog.String("actor_user_id", actor.UserID.String()),
		slog.String("target_goal_id", g.GoalID.String()),
		slog.Uint64("site_id", uint64(g.SiteID)),
		slog.String("goal_pattern_hash", hex.EncodeToString(patternHash[:])),
		slog.String("match_type", string(g.MatchType)),
	)
}

func (h *Goals) emitGoalRejected(
	r *http.Request, actor *auth.User, _ *goals.Goal, err error,
) {
	if h.deps.Audit == nil {
		return
	}

	h.deps.Audit.Event(r.Context(), audit.EventAdminGoalRejected,
		slog.String("actor_user_id", actor.UserID.String()),
		slog.Uint64("site_id", uint64(actor.SiteID)),
		slog.String("reason", err.Error()),
	)
}
