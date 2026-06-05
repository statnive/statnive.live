package mcp

import (
	"context"
	"encoding/json"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/storage"
)

// toolHandler executes one tool. For site-scoped tools the dispatcher
// resolves + authorizes the site and builds a validated *storage.Filter
// before the handler runs (tc.filter non-nil); global tools (list_sites)
// receive a nil filter and read from tc.actor. The returned rows count is
// charged to the per-actor query budget (1 for scalar results, len for
// slices) so a clone-the-dataset loop trips the budget.
type toolHandler func(ctx context.Context, s *Server, tc *toolCtx) (result any, rows int, err error)

// toolDef is one entry in the catalog — the single source of truth the
// no-gap parity gate, tools/list, and docs/mcp.md all derive from. Adding a
// read tool is one entry here.
type toolDef struct {
	Name           string
	Description    string
	RoleClass      auth.Role // minimum global role floor: RoleAPI = any authed actor; RoleAdmin = admin-only
	Scoped         bool      // true ⇒ dispatcher resolves site + range + builds/authorizes Filter
	InputSchema    json.RawMessage
	OutputSchema   json.RawMessage
	Annotations    toolAnnotations
	MaxCallsPerMin int         // 0 ⇒ global budget; >0 ⇒ tighter per-tool cap (admin tools)
	Widget         *widgetMeta // nil for every v2 tool — dormant ChatGPT Apps-SDK seam (PLAN.md)
	// Reserved ⇒ the tool stays in the catalog (so the mcp-parity gate keeps
	// tracking its Store method) but is OMITTED from the published tools/list,
	// so the model never selects a capability the build can't answer yet. The
	// handler still returns storage.ErrNotImplemented if called directly. Drop
	// the flag when the backing rollup/query ships (devices → daily_devices,
	// funnel → windowFunnel). ChatGPT discovery-precision gate: never advertise
	// a tool whose golden prompt dead-ends.
	Reserved bool
	Handler  toolHandler
}

// toolAnnotations are the MCP-spec tool hints, also mandatory + the #1
// rejection cause for the ChatGPT app store. Every tool in this server is
// read-only and touches only the operator's own analytics.
type toolAnnotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
}

// readOnly is the shared annotation value for every tool.
func readOnly() toolAnnotations {
	return toolAnnotations{ReadOnlyHint: true, DestructiveHint: false, OpenWorldHint: false}
}

// widgetMeta is the dormant ChatGPT-app widget descriptor (nil in v2). When
// populated in v3, tools/list emits _meta.ui.resourceUri + the openai/*
// invocation hints. Reserved now so adding widgets is non-breaking.
type widgetMeta struct {
	TemplateURI string
	Invoking    string
	Invoked     string
	Accessible  bool
}

// toolCtx carries per-call state from the dispatcher into a handler.
type toolCtx struct {
	actor  *auth.User
	args   toolArgs
	siteID uint32          // resolved site (0 for global tools)
	filter *storage.Filter // built + validated (nil for global tools)
}

// roleSatisfies reports whether an actor's global role meets a tool's floor.
// admin(1) satisfies viewer(2) and api(3); api(3) satisfies only api. The
// per-site grant check (ActorCanReadSite) runs separately after the site is
// resolved. Note: per-site admins (admin on a site via the grant map but a
// weaker global role) are handled at the tool level for admin tools in a
// later phase; v2 spine tools all use the RoleAPI floor, which any
// authenticated actor meets.
func roleSatisfies(have, required auth.Role) bool {
	return roleRank(have) <= roleRank(required)
}

// roleRank mirrors the unexported auth.roleRank (admin=1 < viewer=2 < api=3;
// unknown = very weak). Duplicated here rather than exported from auth to
// keep the auth surface unchanged.
func roleRank(r auth.Role) int {
	switch r {
	case auth.RoleAdmin:
		return 1
	case auth.RoleViewer:
		return 2
	case auth.RoleAPI:
		return 3
	default:
		return 99
	}
}
