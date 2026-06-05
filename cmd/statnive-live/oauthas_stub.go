//go:build !chatgpt_app

package main

import (
	"errors"

	"github.com/go-chi/chi/v5"
)

// mountOAuthAS is a no-op in the default (and air-gap / inside-iran) build. The
// OAuth 2.1 authorization server — and ALL of its signing-key / JWT / consent
// code in internal/oauthas — compiles ONLY with the `chatgpt_app` tag, so
// `make licenses` / air-gap-validator (which inspect the default build) never
// see it. Setting mcp.oauth_as.enabled=true on a default build fails loudly
// here rather than silently not serving the AS.
func mountOAuthAS(_ chi.Router, p oauthASParams) error {
	if p.cfg.MCP.OAuthAS.Enabled {
		return errors.New("mcp.oauth_as.enabled=true but this binary lacks the OAuth AS — rebuild with `-tags chatgpt_app` (SaaS only; never in the air-gap bundle)")
	}

	return nil
}
