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
	GoalID     string `json:"goal_id"`
	SiteID     uint32 `json:"site_id"`
	Name       string `json:"name"`
	MatchType  string `json:"match_type"`
	Pattern    string `json:"pattern"`
	ValueRials uint64 `json:"value_rials"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

func toGoalResponse(g *goals.Goal) goalResponse {
	return goalResponse{
		GoalID:     g.GoalID.String(),
		SiteID:     g.SiteID,
		Name:       g.Name,
		MatchType:  string(g.MatchType),
		Pattern:    g.Pattern,
		ValueRials: g.ValueRials,
		Enabled:    g.Enabled,
		CreatedAt:  g.CreatedAt,
		UpdatedAt:  g.UpdatedAt,
	}
}

// List handles GET /api/admin/goals — all goals for the actor's site.
//
//nolint:dupl // symmetric with Users.List but over a different entity.
func (h *Goals) List(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	list, err := h.deps.Goals.List(r.Context(), actor.SiteID)
	if err != nil {
		h.emitError(r, "list_goals", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	out := make([]goalResponse, 0, len(list))

	for _, g := range list {
		if g == nil {
			continue
		}

		out = append(out, toGoalResponse(g))
	}

	writeJSON(w, http.StatusOK, map[string]any{"goals": out})
}

// createGoalRequest — tight whitelist. site_id comes from session.
type createGoalRequest struct {
	Name       string `json:"name"`
	MatchType  string `json:"match_type"`
	Pattern    string `json:"pattern"`
	ValueRials uint64 `json:"value_rials"`
	Enabled    bool   `json:"enabled"`
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
		"name", "match_type", "pattern", "value_rials", "enabled",
	}); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	g := &goals.Goal{
		SiteID:     actor.SiteID, // session context — NEVER body
		Name:       strings.TrimSpace(req.Name),
		MatchType:  goals.MatchType(req.MatchType),
		Pattern:    strings.TrimSpace(req.Pattern),
		ValueRials: req.ValueRials,
		Enabled:    req.Enabled,
	}

	if err := h.deps.Goals.Create(r.Context(), g); err != nil {
		if errors.Is(err, goals.ErrInvalidInput) {
			h.emitGoalRejected(r, actor, g, err)
			http.Error(w, "bad request", http.StatusBadRequest)

			return
		}

		h.emitError(r, "create_goal", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	h.reloadSnapshot(r.Context())
	h.emitGoalEvent(r, audit.EventAdminGoalCreated, actor, g)
	writeJSON(w, http.StatusCreated, toGoalResponse(g))
}

// updateGoalRequest — editable fields. site_id / goal_id come from
// path + session.
type updateGoalRequest struct {
	Name       string `json:"name"`
	MatchType  string `json:"match_type"`
	Pattern    string `json:"pattern"`
	ValueRials uint64 `json:"value_rials"`
	Enabled    bool   `json:"enabled"`
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
		"name", "match_type", "pattern", "value_rials", "enabled",
	}); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	g := &goals.Goal{
		GoalID:     goalID,
		SiteID:     actor.SiteID, // session-scoped — cross-tenant update impossible
		Name:       strings.TrimSpace(req.Name),
		MatchType:  goals.MatchType(req.MatchType),
		Pattern:    strings.TrimSpace(req.Pattern),
		ValueRials: req.ValueRials,
		Enabled:    req.Enabled,
	}

	if err := h.deps.Goals.Update(r.Context(), g); err != nil {
		switch {
		case errors.Is(err, goals.ErrNotFound):
			http.Error(w, "not found", http.StatusNotFound)
		case errors.Is(err, goals.ErrInvalidInput):
			h.emitGoalRejected(r, actor, g, err)
			http.Error(w, "bad request", http.StatusBadRequest)
		default:
			h.emitError(r, "update_goal", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}

		return
	}

	h.reloadSnapshot(r.Context())
	h.emitGoalEvent(r, audit.EventAdminGoalUpdated, actor, g)
	writeJSON(w, http.StatusOK, toGoalResponse(g))
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

	if err := h.deps.Goals.Disable(r.Context(), actor.SiteID, goalID); err != nil {
		if errors.Is(err, goals.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)

			return
		}

		h.emitError(r, "disable_goal", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	h.reloadSnapshot(r.Context())
	h.emitGoalEvent(r, audit.EventAdminGoalDisabled, actor,
		&goals.Goal{GoalID: goalID, SiteID: actor.SiteID})
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

func (h *Goals) emitError(r *http.Request, reason string, err error) {
	if h.deps.Audit == nil {
		return
	}

	h.deps.Audit.Event(r.Context(), audit.EventDashboardError,
		slog.String("path", r.URL.Path),
		slog.String("reason", reason),
		slog.String("err", err.Error()),
	)
}
