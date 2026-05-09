package admin

import (
	"net/http"
	"time"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/sites"
)

// Options is the handler group for the read-only admin option-list
// endpoints — currencies + timezones. The SPA's Add/Edit Site forms
// fetch these once on mount to populate the two <select> dropdowns,
// keeping the server as the single source of truth for the allow-lists
// gating PATCH /api/admin/sites/{id}.
type Options struct {
	deps Deps
}

// NewOptions constructs the handler group.
func NewOptions(deps Deps) *Options { return &Options{deps: deps} }

// Currencies handles GET /api/admin/currencies — every entry rendered
// as `code — symbol name` in the dropdown. The list is the same one
// IsValidCurrency gates against, so the SPA cannot accidentally offer
// an option the server will reject.
func (h *Options) Currencies(w http.ResponseWriter, r *http.Request) {
	if auth.UserFrom(r.Context()) == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"currencies": sites.Currencies})
}

// Timezones handles GET /api/admin/timezones — every entry rendered as
// `Label (Offset)` in the dropdown. Offset is computed at request time
// from time.Now() so the displayed UTC offset reflects DST without a
// daily refresh.
func (h *Options) Timezones(w http.ResponseWriter, r *http.Request) {
	if auth.UserFrom(r.Context()) == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"timezones": sites.TimezonesWithOffset(time.Now())})
}
