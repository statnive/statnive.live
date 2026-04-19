package dashboard

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/storage"
)

// errorEnvelope is the JSON shape returned for non-2xx responses.
type errorEnvelope struct {
	Error string `json:"error"`
}

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
func writeError(w http.ResponseWriter, r *http.Request, deps Deps, endpoint string, err error) {
	status, name := classifyError(err)

	if deps.Audit != nil {
		deps.Audit.Event(r.Context(), name,
			slog.String("endpoint", endpoint),
			slog.Int("status", status),
			slog.String("err", err.Error()),
		)
	}

	writeJSON(w, status, errorEnvelope{Error: err.Error()})
}

// writeOK marshals the typed result as JSON 200 + emits an
// dashboard.ok audit event so operators have a paper trail of who
// queried what (within the bearer-token gate's context).
func writeOK(w http.ResponseWriter, r *http.Request, deps Deps, endpoint string, payload any) {
	if deps.Audit != nil {
		deps.Audit.Event(r.Context(), audit.EventDashboardOK,
			slog.String("endpoint", endpoint),
		)
	}

	writeJSON(w, http.StatusOK, payload)
}

// classifyError maps storage errors to (status, audit-event-name).
// Sentinel errors get specific statuses; anything else is a 500.
func classifyError(err error) (int, audit.EventName) {
	switch {
	case errors.Is(err, storage.ErrInvalidFilter),
		errors.Is(err, errBadInput):
		return http.StatusBadRequest, audit.EventDashboardBadRequest
	case errors.Is(err, storage.ErrNotImplemented):
		return http.StatusNotImplemented, audit.EventDashboardNotImplemented
	default:
		return http.StatusInternalServerError, audit.EventDashboardError
	}
}

