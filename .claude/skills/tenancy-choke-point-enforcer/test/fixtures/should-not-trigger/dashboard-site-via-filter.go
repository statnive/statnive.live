// Package dashboard test fixture: handler reads ?site through
// filterFromRequest (the canonical choke-point) so the rule MUST NOT
// fire.
package dashboard

import "net/http"

// Stub types so the fixture compiles in isolation. Production code uses
// the real signatures from internal/dashboard.
type Filter struct{ SiteID uint32 }

func filterFromRequest(_ *http.Request, _ any) (*Filter, error) {
	return &Filter{SiteID: 1}, nil
}

func goodHandlerViaFilter(w http.ResponseWriter, r *http.Request) {
	// SHOULD-NOT-TRIGGER: routed through filterFromRequest which carries
	// the per-site authorization check.
	_, err := filterFromRequest(r, nil)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	w.WriteHeader(http.StatusOK)
}
