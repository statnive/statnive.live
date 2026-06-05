//go:build chatgpt_app

package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/oauthas"
	"github.com/statnive/statnive.live/internal/ratelimit"
)

// asEndpointRatePerMin caps per-IP requests to the AS endpoints — defence
// against credential-stuffing /token and authorize-flooding. Generous enough
// for a real consent flow (a handful of redirects) but well below abuse rates.
const asEndpointRatePerMin = 60

// mountOAuthAS wires statnive's OAuth 2.1 authorization server onto the main
// daemon router (same origin as the dashboard, so /authorize reuses the session
// cookie). Compiled ONLY with `-tags chatgpt_app`; the default build ships the
// no-op stub. Fail-closed: it refuses to mount unless posture=saas and the
// issuer/audience/signing-key/ceiling are all configured (H6).
func mountOAuthAS(r chi.Router, p oauthASParams) error {
	if !p.cfg.MCP.OAuthAS.Enabled {
		return nil
	}

	o := p.cfg.MCP.HTTP.OAuth // reuse issuer / audience / ceiling / scope
	as := p.cfg.MCP.OAuthAS

	switch {
	case p.cfg.Posture != "saas":
		return errors.New("mcp.oauth_as requires posture=saas (never on an air-gap/inside-iran build)")
	case o.Issuer == "" || o.Audience == "":
		return errors.New("mcp.oauth_as requires mcp.http.oauth.issuer and .audience")
	case as.SigningKeyFile == "":
		return errors.New("mcp.oauth_as requires mcp.oauth_as.signing_key_file")
	case len(o.AllowedSiteIDs) == 0:
		return errors.New("mcp.oauth_as requires mcp.http.oauth.allowed_site_ids (the deployment ceiling)")
	}

	dev := os.Getenv("STATNIVE_DEV") == "1"

	key, err := oauthas.LoadSigningKey(as.SigningKeyFile, as.RetiredKeyFiles, dev)
	if err != nil {
		return fmt.Errorf("oauth_as signing key: %w", err)
	}

	scope := o.RequiredScope
	if scope == "" {
		scope = "analytics:read"
	}

	store := oauthas.NewStore(p.conn, p.cfg.ClickHouse.Database)
	sites := auth.NewCachedSitesStore(auth.NewClickHouseSitesStore(p.conn, p.cfg.ClickHouse.Database), 0)

	srv := oauthas.NewServer(oauthas.Config{
		Issuer:         o.Issuer,
		Audience:       o.Audience,
		Scope:          scope,
		AllowedSiteIDs: o.AllowedSiteIDs,
		AccessTTL:      time.Duration(as.AccessTTLSeconds) * time.Second,
		RefreshTTL:     time.Duration(as.RefreshTTLSeconds) * time.Second,
		CodeTTL:        time.Duration(as.CodeTTLSeconds) * time.Second,
		LoginPath:      as.LoginPath,
	}, store, key, sites, p.audit, p.metrics, p.logger, time.Now)

	adminOnly := auth.RequireRole(p.audit, auth.RoleAdmin)

	// Proxy-aware limiter: keys on the forwarded client IP (ingest.ClientIP via
	// internal/ratelimit), not RemoteAddr — behind the SaaS reverse proxy
	// RemoteAddr is the proxy's loopback, which would collapse every client into
	// one global bucket (Gate 2 B1). Also emits audit.EventRateLimited + the
	// rate_limited metric on each 429 (Gate 2 B4).
	limit, err := ratelimit.Middleware(asEndpointRatePerMin, time.Minute, ratelimit.Config{Audit: p.audit, Metrics: p.metrics})
	if err != nil {
		return fmt.Errorf("oauth_as rate limiter: %w", err)
	}

	r.Group(func(gr chi.Router) {
		gr.Use(limit)

		// Public discovery + token exchange (client-to-server; no session).
		gr.Method(http.MethodGet, "/.well-known/oauth-authorization-server", http.HandlerFunc(srv.Metadata))
		gr.Method(http.MethodGet, oauthas.JWKSPath, http.HandlerFunc(srv.JWKS))
		gr.Method(http.MethodPost, "/token", http.HandlerFunc(srv.Token))

		// Browser flow — needs the dashboard session. /authorize bounces an
		// unauthenticated user to login itself, so it gets sessionMW but NOT
		// requireAuthed; /consent + /register require an authenticated user.
		gr.With(p.sessionMW).Method(http.MethodGet, "/authorize", http.HandlerFunc(srv.Authorize))
		gr.With(p.sessionMW, p.authedMW).Method(http.MethodPost, "/consent", http.HandlerFunc(srv.Consent))
		gr.With(p.sessionMW, p.authedMW, adminOnly).Method(http.MethodPost, "/register", http.HandlerFunc(srv.Register))
	})

	p.logger.Info("oauth authorization server mounted", "issuer", o.Issuer, "audience", o.Audience)

	return nil
}
