//go:build chatgpt_app

package oauthas

import (
	"net/http"
	"strings"
)

// JWKSPath is where the AS publishes its signing keys. It matches the resource-
// server verifier's default JWKS discovery (cmd/statnive-live/mcp_oauth.go:
// issuer + "/.well-known/jwks.json"), so the in-tree RS validates AS-issued
// tokens with no extra config. Exported so the route mount and the metadata
// document agree on one path.
const JWKSPath = "/.well-known/jwks.json"

// Metadata handles GET /.well-known/oauth-authorization-server (RFC 8414). It
// advertises only what we actually support: response_type=code, the
// authorization_code + refresh_token grants, and — critically —
// code_challenge_methods_supported=["S256"] (no "plain", signalling the
// mandatory-PKCE posture).
func (s *Server) Metadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}

	base := strings.TrimRight(s.cfg.Issuer, "/")

	s.writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/authorize",
		"token_endpoint":                        base + "/token",
		"registration_endpoint":                 base + "/register",
		"jwks_uri":                              base + JWKSPath,
		"scopes_supported":                      []string{s.cfg.Scope},
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "client_secret_basic", "none"},
	})
}

// JWKS handles GET /.well-known/jwks.json — the public signing keys (active +
// any retired keys still in the rotation grace window). Cacheable; no secrets.
func (s *Server) JWKS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(s.key.JWKS())
}
