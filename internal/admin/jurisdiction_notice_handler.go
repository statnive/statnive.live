package admin

import (
	"net/http"

	"github.com/statnive/statnive.live/internal/auth"
)

// JurisdictionNotice serves GET + POST /api/admin/jurisdiction-notice.
// GET returns the current dismissal state; POST flips it to dismissed.
// Both require an admin session (the route is mounted under
// RequireRole(RoleAdmin) in main.go).
type JurisdictionNotice struct {
	deps Deps
}

// NewJurisdictionNotice constructs the handler group.
func NewJurisdictionNotice(deps Deps) *JurisdictionNotice {
	return &JurisdictionNotice{deps: deps}
}

type jurisdictionNoticeResponse struct {
	Dismissed bool `json:"dismissed"`
}

// Get returns the per-user dismissed state. Used by the SPA on every
// dashboard load to decide whether to render the Compliance prompt.
func (h *JurisdictionNotice) Get(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	if h.deps.JurisdictionNotice == nil {
		// No backing store → treat as not-yet-dismissed so the SPA's
		// notice still works on a binary that hasn't applied migration
		// 014 yet (graceful in mixed-version rollouts).
		writeJSON(w, http.StatusOK, jurisdictionNoticeResponse{Dismissed: false})

		return
	}

	dismissed, err := h.deps.JurisdictionNotice.IsJurisdictionNoticeDismissed(r.Context(), actor.UserID)
	if err != nil {
		h.deps.emitDashboardError(r, "jurisdiction_notice_get", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	writeJSON(w, http.StatusOK, jurisdictionNoticeResponse{Dismissed: dismissed})
}

// Dismiss flips the per-user flag to 1 so the prompt never reappears
// for that user. Idempotent — re-issuing the POST is a no-op.
func (h *JurisdictionNotice) Dismiss(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	if h.deps.JurisdictionNotice == nil {
		http.Error(w, "not configured", http.StatusServiceUnavailable)

		return
	}

	if err := h.deps.JurisdictionNotice.DismissJurisdictionNotice(r.Context(), actor.UserID); err != nil {
		h.deps.emitDashboardError(r, "jurisdiction_notice_dismiss", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
