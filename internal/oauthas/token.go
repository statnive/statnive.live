//go:build chatgpt_app

package oauthas

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/metrics"
)

// tokenResponse is the RFC 6749 §5.1 success body. RefreshToken is omitted (via
// omitempty) only if a flow ever issues none — both our flows return one.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
}

// Token handles POST /token (no session — it is a client-to-server call). It
// supports two grants: authorization_code (PKCE-verified) and refresh_token
// (rotating, reuse-detecting). All failures return RFC 6749 §5.2 JSON errors and
// never leak whether a code/token existed beyond invalid_grant.
func (s *Server) Token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		s.oauthError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")

		return
	}

	if err := r.ParseForm(); err != nil {
		s.oauthError(w, http.StatusBadRequest, "invalid_request", "bad form")

		return
	}

	client, ok := s.authenticateClient(w, r)
	if !ok {
		return
	}

	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		s.grantAuthorizationCode(w, r, client)
	case "refresh_token":
		s.grantRefreshToken(w, r, client)
	default:
		s.oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "only authorization_code and refresh_token are supported")
	}
}

// authenticateClient resolves + authenticates the requesting client via
// client_secret_post (client_id + client_secret in the body) or HTTP Basic. A
// confidential client (non-empty SecretHash) MUST present the correct secret,
// compared in constant time. A public client (empty SecretHash) authenticates by
// client_id alone and relies on PKCE.
func (s *Server) authenticateClient(w http.ResponseWriter, r *http.Request) (Client, bool) {
	clientID := r.PostFormValue("client_id")
	secret := r.PostFormValue("client_secret")

	if clientID == "" {
		if id, pw, hasBasic := r.BasicAuth(); hasBasic {
			clientID, secret = id, pw
		}
	}

	client, err := s.store.GetClient(r.Context(), clientID)
	if err != nil {
		// Count unknown-client as a rejection so the rejection-spike alert
		// catches client-id enumeration / stuffing (Gate 2 B2).
		s.metrics.IncOAuthToken(metrics.OAuthRejected)
		s.oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client")

		return Client{}, false
	}

	if client.SecretHash != "" {
		// Constant-time compare of the SHA-256 hex digests (equal length).
		got := HashToken(secret)
		if subtle.ConstantTimeCompare([]byte(got), []byte(client.SecretHash)) != 1 {
			// Count as a rejection so the rejection-spike alert catches
			// client-secret credential stuffing (the likeliest /token attack).
			s.metrics.IncOAuthToken(metrics.OAuthRejected)
			s.oauthError(w, http.StatusUnauthorized, "invalid_client", "bad client credentials")

			return Client{}, false
		}
	}

	return client, true
}

func (s *Server) grantAuthorizationCode(w http.ResponseWriter, r *http.Request, client Client) {
	rawCode := r.PostFormValue("code")
	redirectURI := r.PostFormValue("redirect_uri")
	verifier := r.PostFormValue("code_verifier")

	if rawCode == "" {
		s.metrics.IncOAuthToken(metrics.OAuthRejected)
		s.oauthError(w, http.StatusBadRequest, "invalid_request", "missing code")

		return
	}

	ac, err := s.store.ConsumeAuthCode(r.Context(), HashToken(rawCode), s.now())
	if err != nil {
		// A replayed (already-consumed) code is the notable signal.
		if errors.Is(err, ErrCodeConsumed) {
			s.audit.Event(r.Context(), audit.EventOAuthTokenRejected,
				slog.String("client_id", client.ID),
				slog.String("reason", "auth_code_replay"),
			)
		}

		s.metrics.IncOAuthToken(metrics.OAuthRejected)
		s.oauthError(w, http.StatusBadRequest, "invalid_grant", "invalid or expired code")

		return
	}

	// Bind checks: the code must belong to THIS client, the redirect_uri must
	// match the one used at /authorize, and PKCE must verify (strip guard).
	if ac.ClientID != client.ID ||
		!exactRedirectMatch([]string{ac.RedirectURI}, redirectURI) ||
		!verifyPKCE(ac.CodeChallenge, verifier) {
		s.audit.Event(r.Context(), audit.EventOAuthTokenRejected,
			slog.String("client_id", client.ID),
			slog.String("reason", "code_binding_mismatch"),
		)
		s.metrics.IncOAuthToken(metrics.OAuthRejected)
		s.oauthError(w, http.StatusBadRequest, "invalid_grant", "code binding mismatch")

		return
	}

	s.issueTokens(w, r, ac.grant, audit.EventOAuthTokenIssued)
}

func (s *Server) grantRefreshToken(w http.ResponseWriter, r *http.Request, client Client) {
	rawRefresh := r.PostFormValue("refresh_token")
	if rawRefresh == "" {
		s.metrics.IncOAuthToken(metrics.OAuthRejected)
		s.oauthError(w, http.StatusBadRequest, "invalid_request", "missing refresh_token")

		return
	}

	newRaw, err := newRawToken()
	if err != nil {
		s.oauthError(w, http.StatusInternalServerError, "server_error", "could not mint token")

		return
	}

	g, err := s.store.RotateRefreshToken(r.Context(), HashToken(rawRefresh), HashToken(newRaw), client.ID, s.now())
	if err != nil {
		if errors.Is(err, ErrRefreshReused) {
			s.audit.Event(r.Context(), audit.EventOAuthRefreshReuse,
				slog.String("client_id", client.ID),
			)
			s.metrics.IncOAuthToken(metrics.OAuthRefreshReuse)
		} else {
			s.metrics.IncOAuthToken(metrics.OAuthRejected)
		}

		s.oauthError(w, http.StatusBadRequest, "invalid_grant", "invalid refresh token")

		return
	}

	s.writeTokens(w, r, g.grant, newRaw, audit.EventOAuthTokenRefreshed)
}

// issueTokens mints an access JWT + a brand-new refresh family for a fresh
// authorization-code grant.
func (s *Server) issueTokens(w http.ResponseWriter, r *http.Request, g grant, event audit.EventName) {
	newRaw, err := newRawToken()
	if err != nil {
		s.oauthError(w, http.StatusInternalServerError, "server_error", "could not mint token")

		return
	}

	rt := RefreshToken{grant: g, FamilyID: uuid.New(), ExpiresAt: s.now().Add(s.cfg.RefreshTTL)}
	if err := s.store.SaveRefreshToken(r.Context(), HashToken(newRaw), rt); err != nil {
		s.oauthError(w, http.StatusInternalServerError, "server_error", "could not store token")

		return
	}

	s.writeTokens(w, r, g, newRaw, event)
}

// writeTokens signs the access JWT and writes the token response. refreshRaw is
// the (already-persisted) refresh token to hand back.
func (s *Server) writeTokens(w http.ResponseWriter, r *http.Request, g grant, refreshRaw string, event audit.EventName) {
	access, err := s.key.SignAccessToken(g, s.cfg.Issuer, s.now(), s.cfg.AccessTTL)
	if err != nil {
		s.oauthError(w, http.StatusInternalServerError, "server_error", "could not sign token")

		return
	}

	s.audit.Event(r.Context(), event,
		slog.String("client_id", g.ClientID),
		slog.String("actor_user_id", g.UserID.String()),
	)
	s.metrics.IncOAuthToken(metrics.OAuthIssued)

	s.writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.cfg.AccessTTL / time.Second),
		RefreshToken: refreshRaw,
		Scope:        g.Scope,
	})
}
