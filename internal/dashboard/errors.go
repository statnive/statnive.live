package dashboard

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/storage"
)

// errorEnvelope is the JSON shape returned for non-2xx responses.
type errorEnvelope struct {
	Error string `json:"error"`
}

// errForbiddenSite is the sentinel returned by the belt-and-braces
// guard in filterFromRequest when an actor's grants don't include the
// requested site_id (OWASP A01:2021 IDOR class). classifyError maps it
// to HTTP 403 + EventDashboardForbidden. The middleware
// RequireDashboardSiteAccess catches the same condition one layer
// earlier; this sentinel only fires if a handler is reached without the
// middleware mounted (which the Semgrep rule
// dashboard-site-query-needs-authz blocks at CI time).
var errForbiddenSite = errors.New("dashboard: forbidden site")

// writeJSON marshals payload as JSON with the given status. Failure to
// marshal collapses to a 500 with a generic message — handlers should
// never see this branch since the result types are well-formed.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, `{"error":"json marshal failed"}`, http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeError maps a storage error to the right HTTP status, writes a
// JSON envelope, and emits one audit event so operators see the
// distribution of bad requests vs not-implemented vs internal errors.
//
// The audit record carries `site_id` from auth.ActiveSiteIDFromContext
// when the middleware has populated it so a cross-tenant attempt is
// forensically visible after the fact.
func writeError(w http.ResponseWriter, r *http.Request, deps Deps, endpoint string, err error) {
	status, name := classifyError(err)

	if deps.Audit != nil {
		deps.Audit.Event(r.Context(), name, dashboardEventAttrs(r,
			slog.String("endpoint", endpoint),
			slog.Int("status", status),
			slog.String("err", err.Error()),
		)...)
	}

	// 403 responses use the uniform shape so the client can't distinguish
	// "unauthorized site" from a deliberate handler-level 403 by body
	// content. The audit record still carries the precise event name.
	body := errorEnvelope{Error: err.Error()}
	if status == http.StatusForbidden {
		body.Error = "forbidden"
	}

	writeJSON(w, status, body)
}

// writeOK marshals the typed result as JSON 200 + emits a
// dashboard.ok audit event so operators have a paper trail of who
// queried what — including the site_id from context.
func writeOK(w http.ResponseWriter, r *http.Request, deps Deps, endpoint string, payload any) {
	if deps.Audit != nil {
		deps.Audit.Event(r.Context(), audit.EventDashboardOK, dashboardEventAttrs(r,
			slog.String("endpoint", endpoint),
		)...)
	}

	writeJSON(w, http.StatusOK, payload)
}

// dashboardEventAttrs appends the per-request site_id + actor_user_id
// to the supplied base attrs when the auth middleware has populated
// context. /api/sites doesn't carry ?site (and therefore no
// ActiveSiteIDFromContext) — the field is omitted rather than logged as
// 0 to keep the JSONL shape honest. Variadic input is owned by writeOK
// / writeError (fresh slice per call), so we append in place — at full
// dashboard EPS this saves the per-event slice-header allocation.
func dashboardEventAttrs(r *http.Request, base ...slog.Attr) []slog.Attr {
	if siteID, ok := auth.ActiveSiteIDFromContext(r.Context()); ok {
		base = append(base, slog.Uint64("site_id", uint64(siteID)))
	}

	if u := auth.UserFrom(r.Context()); u != nil {
		base = append(base, slog.String("actor_user_id", u.UserID.String()))
	}

	return base
}

// classifyError maps storage errors to (status, audit-event-name).
// Sentinel errors get specific statuses; anything else is a 500.
func classifyError(err error) (int, audit.EventName) {
	switch {
	case errors.Is(err, errForbiddenSite):
		return http.StatusForbidden, audit.EventDashboardForbidden
	case errors.Is(err, storage.ErrInvalidFilter),
		errors.Is(err, errBadInput):
		return http.StatusBadRequest, audit.EventDashboardBadRequest
	case errors.Is(err, storage.ErrNotImplemented):
		return http.StatusNotImplemented, audit.EventDashboardNotImplemented
	default:
		return http.StatusInternalServerError, audit.EventDashboardError
	}
}
