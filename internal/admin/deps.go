// Package admin owns the HTTP handlers for /api/admin/* — user + goal
// CRUD. Router (cmd/statnive-live/main.go) stacks
// auth.RequireRole(admin) before admin.Mount, so handlers here assume
// auth.UserFrom(ctx) returns a non-nil admin user. Every write handler
// decodes via httpjson.DecodeAllowed (enforced by the Semgrep rule
// admin-no-raw-json-decoder in blake3-hmac-identity-review).
package admin

import (
	"log/slog"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/goals"
)

// Deps bundles the dependencies every admin handler shares. One
// construction point (cmd/statnive-live/main.go), one source of truth.
// Every field is non-nil in production; tests may pass a subset where
// the handler doesn't touch the missing dep.
type Deps struct {
	Auth     auth.Store
	Goals    goals.Store
	Snapshot *goals.Snapshot // for post-write Reload()
	Audit    *audit.Logger
	Logger   *slog.Logger
}
