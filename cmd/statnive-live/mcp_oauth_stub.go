//go:build !chatgpt_app

package main

import (
	"errors"
	"log/slog"
	"net/http"
)

// oauthMiddleware is a no-op stub in the default (and air-gap / inside-iran)
// build. The ChatGPT-app OAuth 2.1 resource-server verifier — and ALL of its
// JWKS / IdP / outbound HTTP code — is compiled in ONLY with the
// `chatgpt_app` build tag (see mcp_oauth.go). Selecting
// `mcp.http.profile=chatgpt-app` on a default build therefore fails loudly
// here rather than silently serving unauthenticated. This is the air-gap
// carve-out: `make licenses` / `air-gap-validator` only ever see the default
// build, which contains zero IdP code.
func oauthMiddleware(_ mcpOAuthConfig, _ *slog.Logger) (func(http.Handler) http.Handler, error) {
	return nil, errors.New("chatgpt-app OAuth verifier not built in — rebuild with `-tags chatgpt_app` (SaaS only; never in the air-gap bundle)")
}
