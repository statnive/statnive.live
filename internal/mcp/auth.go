package mcp

import (
	"strconv"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/sites"
)

// stdioScopedActorID is a fixed, non-nil sentinel UUID for a scoped stdio
// operator. It must be non-nil so auth.User.ActorCanReadSite takes the
// per-site grant-map branch instead of the UserID==Nil wildcard branch — a
// scoped operator must NOT be treated as wildcard.
var stdioScopedActorID = uuid.UUID{0xff}

// syntheticOperator builds the *auth.User for a stdio session. stdio is
// fail-closed by default: with allowAll=false and no allowSites, the empty
// grant map denies every site (piping stdio to an LLM makes the LLM an actor
// inside the operator's trust boundary, so an all-tenant default is wrong).
// `--all-sites` opts into the wildcard; `--allow-sites=1,4` scopes.
func syntheticOperator(allowSites []uint32, allowAll bool) *auth.User {
	if allowAll {
		// Wildcard: UserID==Nil + SiteID==0 ⇒ ActorCanReadSite true for all.
		return &auth.User{UserID: uuid.Nil, SiteID: 0, Role: auth.RoleAdmin}
	}

	grants := make(map[uint32]auth.Role, len(allowSites))
	for _, id := range allowSites {
		grants[id] = auth.RoleAdmin
	}

	// Non-nil UserID + non-nil (possibly empty) Sites map ⇒ the grant-map
	// branch of ActorCanReadSite. Empty map ⇒ no access (fail-closed).
	return &auth.User{UserID: stdioScopedActorID, SiteID: 0, Role: auth.RoleAdmin, Sites: grants}
}

// StdioActor builds the session principal for a stdio MCP server from the
// operator's --allow-sites / --all-sites flags. Exported entry point for the
// cmd; see syntheticOperator for the fail-closed semantics.
func StdioActor(allowSites []uint32, allowAll bool) *auth.User {
	return syntheticOperator(allowSites, allowAll)
}

// meetsRoleFloor reports whether the actor's global role meets a tool's
// minimum (mirrors auth.RequireRole). The per-site grant check
// (ActorCanReadSite) is separate.
func meetsRoleFloor(u *auth.User, required auth.Role) bool {
	if u == nil {
		return false
	}

	return roleSatisfies(u.Role, required)
}

// isWildcardActor reports whether the actor reads every site (legacy bearer
// or stdio --all-sites). These get the strict budget tier.
func isWildcardActor(u *auth.User) bool {
	return u != nil && u.UserID == uuid.Nil && u.SiteID == 0
}

// actorKey is the stable per-actor budget key. Real users + scoped stdio key
// by UserID; API tokens key by their bound site (0 = wildcard).
func actorKey(u *auth.User) string {
	if u == nil {
		return "anon"
	}

	if u.UserID != uuid.Nil {
		return "u:" + u.UserID.String()
	}

	return "t:" + strconv.FormatUint(uint64(u.SiteID), 10)
}

// actorLabel is the non-PII audit label. Operator UUIDs are metadata
// surrogates (allowed in audit per the package conventions), never visitor
// identity; tokens are labeled by scope.
func actorLabel(u *auth.User) string {
	if u == nil {
		return "anon"
	}

	if u.UserID != uuid.Nil {
		return "user:" + u.UserID.String()
	}

	if u.SiteID == 0 {
		return "token:wildcard"
	}

	return "token:site=" + strconv.FormatUint(uint64(u.SiteID), 10)
}

// filterSitesForActor returns only the sites the actor may read. Wildcard
// actors get the full list; everyone else is filtered through
// ActorCanReadSite (per-site grants or single-site).
func filterSitesForActor(u *auth.User, all []sites.Site) []sites.Site {
	if isWildcardActor(u) {
		return all
	}

	out := make([]sites.Site, 0, len(all))

	for _, st := range all {
		if u.ActorCanReadSite(st.ID) {
			out = append(out, st)
		}
	}

	return out
}
