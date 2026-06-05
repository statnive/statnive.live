//go:build chatgpt_app

package oauthas

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/statnive/statnive.live/internal/auth"
)

// authRequest is the validated /authorize (and re-validated /consent) request.
// Once built, redirectURI is guaranteed to exactly match a registered URI, so
// errors may be returned to the client via redirectErr.
type authRequest struct {
	client        Client
	redirectURI   string
	state         string
	scope         string
	codeChallenge string
	audience      string
}

// Authorize handles GET /authorize: the OAuth 2.1 authorization-code +
// PKCE-S256 entry point. The mount applies sessionMW (no hard requireAuthed) so
// an unauthenticated user is bounced to the dashboard login rather than 401'd.
//
// Error ordering is security-critical: client_id and redirect_uri are validated
// FIRST and any failure returns a plain 400 (never a redirect) — redirecting to
// an unvalidated URI is an open redirect. Only after redirect_uri is confirmed
// do later errors (bad PKCE, bad scope) redirect back to the client.
func (s *Server) Authorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}

	q := r.URL.Query()

	req, ok := s.validateAuthParams(w, r, q.Get)
	if !ok {
		return
	}

	// Require a dashboard session. Unauthenticated → bounce to login with a
	// return_to so the user lands back on this exact authorize request.
	user := auth.UserFrom(r.Context())
	if user == nil {
		s.redirectToLogin(w, r)

		return
	}

	grants, err := s.sites.LoadUserSites(r.Context(), user.UserID)
	if err != nil {
		s.logger.Warn("oauthas authorize: load user sites", "err", err)
		s.redirectErr(w, r, req.redirectURI, req.state, "server_error", "could not load your sites")

		return
	}

	s.renderConsent(w, req, s.consentableSites(grants))
}

// paramSource abstracts query (GET /authorize) vs form (POST /consent) lookups.
type paramSource func(string) string

// validateAuthParams runs the full request validation shared by /authorize and
// /consent. On any failure it writes the response (400 or redirect) and returns
// ok=false. The open-redirect guard lives here: client + redirect_uri are
// checked before any redirect is possible.
func (s *Server) validateAuthParams(w http.ResponseWriter, r *http.Request, get paramSource) (authRequest, bool) {
	clientID := get("client_id")
	redirectURI := get("redirect_uri")

	// 1. client_id — pre-redirect, plain 400 on failure.
	client, err := s.store.GetClient(r.Context(), clientID)
	if err != nil {
		http.Error(w, "invalid client_id", http.StatusBadRequest)

		return authRequest{}, false
	}

	// 2. redirect_uri — must EXACTLY match a registered URI (no normalization,
	// no wildcards). Pre-redirect, plain 400 on failure.
	if redirectURI == "" || !exactRedirectMatch(client.RedirectURIs, redirectURI) {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)

		return authRequest{}, false
	}

	state := get("state")

	// 3. response_type — from here, failures redirect back to the client.
	if get("response_type") != "code" {
		s.redirectErr(w, r, redirectURI, state, "unsupported_response_type", "only response_type=code is supported")

		return authRequest{}, false
	}

	// 4. PKCE S256 mandatory (downgrade guard): method must be S256 and the
	// challenge must be a well-formed base64url SHA-256.
	challenge := get("code_challenge")
	if !validChallengeMethod(get("code_challenge_method")) || !validChallenge(challenge) {
		s.redirectErr(w, r, redirectURI, state, "invalid_request", "PKCE with code_challenge_method=S256 is required")

		return authRequest{}, false
	}

	// 5. scope — must be a subset of the single granted scope.
	scope, scopeOK := s.resolveScope(get("scope"))
	if !scopeOK {
		s.redirectErr(w, r, redirectURI, state, "invalid_scope", "only "+s.cfg.Scope+" is supported")

		return authRequest{}, false
	}

	// 6. resource (RFC 8707) — if supplied, must equal the canonical audience.
	resource := get("resource")
	if resource != "" && resource != s.cfg.Audience {
		s.redirectErr(w, r, redirectURI, state, "invalid_target", "unknown resource")

		return authRequest{}, false
	}

	return authRequest{
		client:        client,
		redirectURI:   redirectURI,
		state:         state,
		scope:         scope,
		codeChallenge: challenge,
		audience:      s.cfg.Audience,
	}, true
}

// resolveScope returns the effective scope. Empty request scope defaults to the
// configured scope; a request scope must be exactly the configured scope (the
// only one we grant) — anything else is rejected (no scope escalation).
func (s *Server) resolveScope(requested string) (string, bool) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return s.cfg.Scope, true
	}

	for _, tok := range strings.Fields(requested) {
		if tok != s.cfg.Scope {
			return "", false
		}
	}

	return s.cfg.Scope, true
}

// exactRedirectMatch reports whether candidate exactly equals a registered URI.
// Byte-exact: no trailing-slash tolerance, no case folding, no path
// normalization — the canonical defense against redirect smuggling.
func exactRedirectMatch(registered []string, candidate string) bool {
	for _, u := range registered {
		if u == candidate {
			return true
		}
	}

	return false
}

func (s *Server) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	returnTo := r.URL.RequestURI()
	loc := s.cfg.LoginPath + "?return_to=" + url.QueryEscape(returnTo)

	http.Redirect(w, r, loc, http.StatusFound)
}
