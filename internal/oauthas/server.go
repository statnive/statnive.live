//go:build chatgpt_app

package oauthas

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/metrics"
)

// Config is the authorization-server configuration. Issuer + Audience are baked
// into every issued token; AllowedSiteIDs is the deployment ceiling (the same
// value the resource server enforces) so consent can never grant a site the RS
// would reject. All zero-value lifespans fall back to the defaults below.
type Config struct {
	Issuer         string   // OAuth issuer, e.g. https://app.statnive.live
	Audience       string   // RFC 8707 resource / aud, e.g. https://app.statnive.live/mcp
	Scope          string   // the single granted scope, e.g. analytics:read
	AllowedSiteIDs []uint32 // deployment ceiling; consent ∩ this ∩ user grants
	// AccessTTL is the access-token lifespan (default 30m). Access tokens are
	// stateless JWTs the resource server validates by signature alone — it does
	// NOT consult the store per request — so a revoked client / refresh family
	// keeps a live access token working until it expires (revocation lag ≤
	// AccessTTL). That is the standard stateless-JWT tradeoff (Gate 2 A1); the
	// real revocation point is refresh rotation (which re-checks the client +
	// family on every /token refresh). Keep AccessTTL short so the lag stays
	// small; the 30m default + 30d rotating refresh is the conventional balance.
	AccessTTL  time.Duration
	RefreshTTL time.Duration // refresh-token lifespan (default 30d)
	CodeTTL    time.Duration // authorization-code lifespan (default 60s)
	LoginPath  string        // where to send an unauthenticated /authorize (default "/")
}

const (
	defaultAccessTTL  = 30 * time.Minute
	defaultRefreshTTL = 30 * 24 * time.Hour
	defaultCodeTTL    = 60 * time.Second
	defaultScope      = "analytics:read"
)

func (c Config) withDefaults() Config {
	if c.AccessTTL <= 0 {
		c.AccessTTL = defaultAccessTTL
	}

	if c.RefreshTTL <= 0 {
		c.RefreshTTL = defaultRefreshTTL
	}

	if c.CodeTTL <= 0 {
		c.CodeTTL = defaultCodeTTL
	}

	if c.Scope == "" {
		c.Scope = defaultScope
	}

	if c.LoginPath == "" {
		c.LoginPath = "/"
	}

	return c
}

// Server is the hand-rolled OAuth 2.1 authorization server. Handlers are wired
// by cmd/statnive-live/oauthas.go with the right middleware (session for
// /authorize + /consent, admin for /register, none for /token + /jwks +
// metadata).
type Server struct {
	cfg     Config
	store   *Store
	key     *SigningKey
	sites   auth.SitesStore // LoadUserSites for the consent intersection
	audit   *audit.Logger
	metrics *metrics.Registry // nil-safe; every Inc* checks the nil receiver
	logger  *slog.Logger
	now     func() time.Time
}

// NewServer constructs the AS. sitesStore loads a user's per-site grants for the
// consent screen + scope-clamp; metricsReg may be nil (its Inc* methods are
// nil-safe); nowFn defaults to time.Now.
func NewServer(cfg Config, store *Store, key *SigningKey, sitesStore auth.SitesStore, auditLog *audit.Logger, metricsReg *metrics.Registry, logger *slog.Logger, nowFn func() time.Time) *Server {
	if nowFn == nil {
		nowFn = time.Now
	}

	return &Server{
		cfg:     cfg.withDefaults(),
		store:   store,
		key:     key,
		sites:   sitesStore,
		audit:   auditLog,
		metrics: metricsReg,
		logger:  logger,
		now:     nowFn,
	}
}

// inCeiling reports whether a site is within the deployment ceiling.
func (s *Server) inCeiling(siteID uint32) bool {
	for _, id := range s.cfg.AllowedSiteIDs {
		if id == siteID {
			return true
		}
	}

	return false
}

// consentableSites returns the sites a user may grant: their active grants
// intersected with the deployment ceiling (M2). The assistant can never read a
// site the user lacks, nor one outside the deployment's allow-list.
func (s *Server) consentableSites(grants map[uint32]auth.Role) []uint32 {
	out := make([]uint32, 0, len(grants))

	for siteID := range grants {
		if s.inCeiling(siteID) {
			out = append(out, siteID)
		}
	}

	return out
}

// --- shared helpers --------------------------------------------------------

// newRawToken returns 32 bytes of crypto/rand as base64url (256-bit,
// unguessable). Used for authorization codes + refresh tokens; the raw value is
// returned to the client and only its SHA-256 hex is stored.
func newRawToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Warn("oauthas: write json", "err", err)
	}
}

// oauthError is the RFC 6749 §5.2 token/registration error body.
func (s *Server) oauthError(w http.ResponseWriter, status int, code, desc string) {
	s.writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// redirectErr sends the user-agent back to a VALIDATED redirect_uri with an
// OAuth error (RFC 6749 §4.1.2.1). It must only be called AFTER redirectURI has
// been confirmed to exactly match a registered URI — otherwise it is an open
// redirect. Pre-validation failures use http.Error instead.
func (s *Server) redirectErr(w http.ResponseWriter, r *http.Request, redirectURI, state, code, desc string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)

		return
	}

	qy := u.Query()
	qy.Set("error", code)

	if desc != "" {
		qy.Set("error_description", desc)
	}

	if state != "" {
		qy.Set("state", state)
	}

	u.RawQuery = qy.Encode()

	http.Redirect(w, r, u.String(), http.StatusFound)
}
