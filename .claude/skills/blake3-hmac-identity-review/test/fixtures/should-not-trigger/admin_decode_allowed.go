// should-not-trigger: correctly guarded admin handler using
// httpjson.DecodeAllowed. The admin-no-raw-json-decoder Semgrep rule
// MUST NOT flag this file.
package admin

import (
	"net/http"
)

// allowedFields declares the handler's acceptance surface — anything
// else in the body (role, site_id, is_admin) is rejected at decode
// time, not silently written through.
var allowedFields = []string{"name"}

// GoodCreate uses the shared httpjson.DecodeAllowed helper (not raw
// json.NewDecoder), so the F4 guard is consistent across every admin
// mutation. Pseudo — the real helper lives at
// internal/httpjson/decode.go; fixture stays self-contained so
// Semgrep can parse it.
func GoodCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}

	if err := decodeAllowed(r, &body, allowedFields); err != nil {
		http.Error(w, "bad", http.StatusBadRequest)

		return
	}

	_ = body
}

// decodeAllowed is a local stub so the fixture compiles without
// depending on the real httpjson package.
func decodeAllowed(_ *http.Request, _ any, _ []string) error {
	return nil
}