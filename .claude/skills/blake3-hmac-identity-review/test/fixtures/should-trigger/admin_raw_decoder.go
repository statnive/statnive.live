// should-trigger: raw json.NewDecoder inside internal/admin/ bypasses
// the F4 guard. The admin-no-raw-json-decoder Semgrep rule MUST flag
// this file.
//
// Compilable on its own so the fixture doubles as a documentation
// example — a reviewer reading this knows exactly what the rule
// rejects.
package admin

import (
	"encoding/json"
	"net/http"
)

// BrokenCreate is the anti-pattern: decoding a request body with the
// raw stdlib decoder instead of httpjson.DecodeAllowed. Any attacker
// body containing extra fields (role, site_id, is_admin) will land
// silently in any Go struct whose json tags match.
func BrokenCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}

	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&body); err != nil {
		http.Error(w, "bad", http.StatusBadRequest)

		return
	}

	_ = body
}