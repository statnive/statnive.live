// Package dashboard test fixture: handler that reads ?site directly
// from the URL query WITHOUT calling filterFromRequest or
// actor.CanAccessSite. Semgrep MUST flag this as the IDOR bypass
// caught in Lesson 35.
package dashboard

import (
	"net/http"
	"strconv"
)

func badHandler(w http.ResponseWriter, r *http.Request) {
	// SHOULD-TRIGGER: ?site read with no per-site grant check, no
	// filterFromRequest invocation, no ActiveSiteIDFromContext lookup.
	raw := r.URL.Query().Get("site")

	siteID, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		http.Error(w, "bad site", http.StatusBadRequest)

		return
	}

	_ = siteID

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("leaked"))
}
