package main

import (
	"log/slog"
	"net/http"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/metrics"
)

// oauthASParams is the dependency bundle for mountOAuthAS. It is defined here
// (untagged) so both the real mount (oauthas.go, //go:build chatgpt_app) and the
// no-op stub (oauthas_stub.go, //go:build !chatgpt_app) share one signature.
// Only types available in every build appear here — never the internal/oauthas
// package, which must stay out of the default + air-gap binaries.
type oauthASParams struct {
	cfg       appConfig
	conn      driver.Conn
	audit     *audit.Logger
	metrics   *metrics.Registry // /metrics counters (shared daemon registry)
	logger    *slog.Logger
	sessionMW func(http.Handler) http.Handler // populates the dashboard session
	authedMW  func(http.Handler) http.Handler // 401s if no session (requireAuthed)
}
