//go:build chatgpt_app

package oauthas

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
)

// registerRequest is the RFC 7591 Dynamic Client Registration body subset we
// accept. We deliberately ignore most optional metadata — the only client is
// the ChatGPT connector, registered once by an operator.
type registerRequest struct {
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

// registerResponse is the RFC 7591 §3.2.1 success body. client_secret is shown
// EXACTLY ONCE — only its SHA-256 hex is stored.
type registerResponse struct {
	ClientID              string   `json:"client_id"`
	ClientSecret          string   `json:"client_secret"`
	ClientIDIssuedAt      int64    `json:"client_id_issued_at"`
	ClientSecretExpiresAt int64    `json:"client_secret_expires_at"`
	ClientName            string   `json:"client_name"`
	RedirectURIs          []string `json:"redirect_uris"`
	GrantTypes            []string `json:"grant_types"`
	ResponseTypes         []string `json:"response_types"`
	Scope                 string   `json:"scope"`
	TokenEndpointAuth     string   `json:"token_endpoint_auth_method"`
}

const maxRedirectURIs = 8

// Register handles POST /register (RFC 7591 DCR). Per the security review (H2)
// this is NOT public: the mount gates it behind sessionMW + requireAuthed +
// RequireRole(admin), so only a logged-in operator pre-registers the ChatGPT
// client. The client is confidential — a crypto/rand secret is generated,
// SHA-256-hashed at rest, and returned once.
func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		s.oauthError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")

		return
	}

	var req registerRequest

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&req); err != nil {
		s.oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "bad JSON")

		return
	}

	name := strings.TrimSpace(req.ClientName)
	if name == "" || len(name) > 120 {
		s.oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "client_name required (<=120 chars)")

		return
	}

	if err := validateRedirectURIs(req.RedirectURIs); err != nil {
		s.oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())

		return
	}

	secret, err := newRawToken()
	if err != nil {
		s.oauthError(w, http.StatusInternalServerError, "server_error", "could not mint secret")

		return
	}

	client := Client{
		ID:           uuid.NewString(),
		SecretHash:   HashToken(secret),
		Name:         name,
		RedirectURIs: req.RedirectURIs,
		Scopes:       []string{s.cfg.Scope},
	}

	if err := s.store.CreateClient(r.Context(), client); err != nil {
		s.logger.Warn("oauthas: create client", "err", err)
		s.oauthError(w, http.StatusInternalServerError, "server_error", "could not store client")

		return
	}

	actor := "unknown"
	if u := auth.UserFrom(r.Context()); u != nil {
		actor = u.UserID.String()
	}

	s.audit.Event(r.Context(), audit.EventOAuthClientRegistered,
		slog.String("client_id", client.ID),
		slog.String("client_name", name),
		slog.String("actor_user_id", actor),
		slog.Int("redirect_uri_count", len(client.RedirectURIs)),
	)

	s.writeJSON(w, http.StatusCreated, registerResponse{
		ClientID:              client.ID,
		ClientSecret:          secret,
		ClientIDIssuedAt:      s.now().Unix(),
		ClientSecretExpiresAt: 0, // non-expiring
		ClientName:            client.Name,
		RedirectURIs:          client.RedirectURIs,
		GrantTypes:            []string{"authorization_code", "refresh_token"},
		ResponseTypes:         []string{"code"},
		Scope:                 s.cfg.Scope,
		TokenEndpointAuth:     "client_secret_post",
	})
}

// validateRedirectURIs enforces the exact-match-friendly invariants: 1..N
// absolute https URIs, no fragment, no wildcard, non-empty host. http is
// allowed ONLY for loopback (local ChatGPT dev tooling).
func validateRedirectURIs(uris []string) error {
	if len(uris) == 0 {
		return errors.New("at least one redirect_uri is required")
	}

	if len(uris) > maxRedirectURIs {
		return errors.New("too many redirect_uris")
	}

	for _, raw := range uris {
		u, err := url.Parse(raw)
		if err != nil {
			return errors.New("unparseable redirect_uri")
		}

		if u.Fragment != "" || strings.Contains(raw, "#") {
			return errors.New("redirect_uri must not contain a fragment")
		}

		if strings.Contains(raw, "*") {
			return errors.New("wildcard redirect_uri is not allowed")
		}

		if u.Host == "" {
			return errors.New("redirect_uri must be absolute")
		}

		if u.Scheme == "https" {
			continue
		}

		if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
			continue
		}

		return errors.New("redirect_uri must use https (http allowed only for loopback)")
	}

	return nil
}

func isLoopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}
