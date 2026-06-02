package dashboard

import (
	"net/http"
	"strconv"

	"github.com/statnive/statnive.live/internal/storage"
)

const (
	endpointPropsList = "props_list"
	endpointCompare   = "compare"
)

// propsListHandler answers GET /api/props/list?scope=hit|session|user&limit=100.
// Returns distinct prop names + sample values from the last 7 days of
// events_raw, scoped to the actor's authorised site. Phase 3 of
// segments. Backed by CachedStore.PropNames with TTLHistorical so the
// chip autocomplete doesn't churn within a session.
func propsListHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = endpointPropsList

		f, err := filterFromRequest(r, deps.Sites)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		scope := r.URL.Query().Get("scope")
		if scope == "" {
			scope = "hit"
		}

		limit := 100

		if v := r.URL.Query().Get("limit"); v != "" {
			n, parseErr := strconv.Atoi(v)
			if parseErr == nil && n > 0 && n <= 200 {
				limit = n
			}
		}

		out, err := deps.Store.PropNames(r.Context(), f, scope, limit)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		// Always return a slice — never nil — so the SPA's empty-state
		// renderer can branch on length without nil-checking the JSON.
		if out == nil {
			out = []storage.PropNameRow{}
		}

		writeOK(w, r, deps, endpoint, out)
	}
}

// compareHandler answers GET /api/stats/compare?dimension=<scope>:<name>&goal=<event_name>.
// Returns a variant-comparison table with conversion math (pooled-
// variance z-test + Wilson CI + sample-size guard). Phase 4 of
// segments. Backed by CachedStore.Compare.
func compareHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const endpoint = endpointCompare

		f, err := filterFromRequest(r, deps.Sites)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		dimension := r.URL.Query().Get("dimension")
		goal := r.URL.Query().Get("goal")

		result, err := deps.Store.Compare(r.Context(), f, dimension, goal)
		if err != nil {
			writeError(w, r, deps, endpoint, err)

			return
		}

		writeOK(w, r, deps, endpoint, result)
	}
}
