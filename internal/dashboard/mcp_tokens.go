package dashboard

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/httpjson"
)

// MCPTokenDeps wires the self-serve MCP-token endpoints. Tokens is the
// cached store (the hot-path LookupActive lives in middleware, not here).
type MCPTokenDeps struct {
	Tokens         auth.APITokenStore
	Audit          *audit.Logger
	Logger         *slog.Logger
	MaxPerUser     int    // hard cap on active tokens per user (0 ⇒ default 20)
	DefaultTTLDays int    // applied when the request omits ttl_days (0 ⇒ default 90)
	PublicMCPURL   string // e.g. https://app.statnive.live/mcp — for /connection
	HTTPEnabled    bool   // mcp.http.enabled — surfaced in /connection
}

const (
	defaultMaxTokensPerUser = 20
	defaultTokenTTLDays     = 90
	maxTokenNameLen         = 80
	maxTokenTTLDays         = 365
)

// MountMCPTokens registers the self-serve token routes. The caller MUST
// stack session + api-token auth, RequireAuthenticated, the per-user mint
// rate-limit, and HydrateActorGrants (so the scope-clamp can read the
// actor's site grants) before these routes — see cmd/statnive-live/main.go.
//
// Routes:
//
//	POST   /api/mcp/tokens       — mint (raw shown once); scope-clamped to caller grants
//	GET    /api/mcp/tokens       — list active tokens (metadata only, never raw/hash)
//	DELETE /api/mcp/tokens/{id}  — revoke (own tokens only)
//	GET    /api/mcp/connection   — MCP URL + transport + caller scope + add-command
func MountMCPTokens(r chi.Router, deps MCPTokenDeps) {
	r.Method(http.MethodPost, "/api/mcp/tokens", mcpTokenCreateHandler(deps))
	r.Method(http.MethodGet, "/api/mcp/tokens", mcpTokenListHandler(deps))
	r.Method(http.MethodDelete, "/api/mcp/tokens/{id}", mcpTokenRevokeHandler(deps))
	r.Method(http.MethodGet, "/api/mcp/connection", mcpConnectionHandler(deps))
}

type mcpTokenCreateReq struct {
	Name    string   `json:"name"`
	SiteIDs []uint32 `json:"site_ids"`
	Role    string   `json:"role"`
	TTLDays int      `json:"ttl_days"`
}

// mcpTokenView is the wire shape for a token. Token is populated ONLY on the
// mint response (shown once) and omitted everywhere else via omitempty —
// list/revoke never carry the raw secret or the hash.
type mcpTokenView struct {
	TokenID    string   `json:"token_id"`
	Name       string   `json:"name"`
	SiteIDs    []uint32 `json:"site_ids"`
	Role       string   `json:"role"`
	CreatedAt  int64    `json:"created_at"`
	ExpiresAt  int64    `json:"expires_at"` // 0 = never
	LastUsedAt int64    `json:"last_used_at"`
	Token      string   `json:"token,omitempty"` // raw, mint-only, shown once
}

func mcpTokenCreateHandler(deps MCPTokenDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := mintingActor(w, r)
		if !ok {
			return
		}

		var req mcpTokenCreateReq
		if err := httpjson.DecodeAllowed(r, &req,
			[]string{"name", "site_ids", "role", "ttl_days"}); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed body"})

			return
		}

		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" || len(req.Name) > maxTokenNameLen {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required (1-80 chars)"})

			return
		}

		role, ok := tokenRole(req.Role)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be viewer or api"})

			return
		}

		avail := availableSites(actor)
		if len(avail) == 0 {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "no site access"})

			return
		}

		sites, err := clampSites(actor, avail, req.SiteIDs, role)
		if err != nil {
			mcpEmit(deps, r, audit.EventMCPTokenRejected, actor, uuid.Nil, req.SiteIDs, role)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})

			return
		}

		// Max-active-per-user cap.
		maxPer := deps.MaxPerUser
		if maxPer <= 0 {
			maxPer = defaultMaxTokensPerUser
		}

		if n, cerr := deps.Tokens.CountActiveForUser(r.Context(), actor.UserID); cerr == nil && n >= maxPer {
			mcpEmit(deps, r, audit.EventMCPTokenRejected, actor, uuid.Nil, sites, role)
			writeJSON(w, http.StatusConflict,
				map[string]string{"error": fmt.Sprintf("token limit reached (%d) — revoke one first", maxPer)})

			return
		}

		raw, meta, cerr := deps.Tokens.Create(r.Context(), actor.UserID, req.Name, sites, role, ttlFor(req.TTLDays, deps))
		if cerr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mint failed"})

			return
		}

		mcpEmit(deps, r, audit.EventMCPTokenCreated, actor, meta.TokenID, sites, role)

		resp := viewOf(meta)
		resp.Token = raw // raw, shown once
		writeJSON(w, http.StatusCreated, resp)
	}
}

func mcpTokenListHandler(deps MCPTokenDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := auth.UserFrom(r.Context())
		if actor == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})

			return
		}

		list, err := deps.Tokens.ListForUser(r.Context(), actor.UserID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})

			return
		}

		views := make([]mcpTokenView, 0, len(list))
		for _, m := range list {
			views = append(views, viewOf(m))
		}

		writeJSON(w, http.StatusOK, map[string]any{"tokens": views})
	}
}

func mcpTokenRevokeHandler(deps MCPTokenDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := auth.UserFrom(r.Context())
		if actor == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})

			return
		}

		tokenID, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid token id"})

			return
		}

		err = deps.Tokens.Revoke(r.Context(), tokenID, actor.UserID)

		switch {
		case err == nil:
			mcpEmit(deps, r, audit.EventMCPTokenRevoked, actor, tokenID, nil, "")
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, auth.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "token not found"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "revoke failed"})
		}
	}
}

func mcpConnectionHandler(deps MCPTokenDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := auth.UserFrom(r.Context())
		if actor == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})

			return
		}

		sites := sortedKeys(availableSites(actor))
		cmd := fmt.Sprintf(
			`claude mcp add --transport http %s --header "Authorization: Bearer <TOKEN>"`,
			deps.PublicMCPURL)

		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":              deps.HTTPEnabled,
			"transport":            "http",
			"url":                  deps.PublicMCPURL,
			"role":                 string(actor.Role),
			"sites":                sites,
			"add_command_template": cmd,
		})
	}
}

// --- helpers ---------------------------------------------------------------

// mintingActor returns the session actor allowed to mint, or writes the
// rejection and returns ok=false. A fresh dashboard session is required:
// API-token / minted-token principals (uuid.Nil or the api:/mcp-token:
// username prefixes) cannot mint — a token can never beget another token.
func mintingActor(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})

		return nil, false
	}

	if actor.UserID == uuid.Nil ||
		strings.HasPrefix(actor.Username, "api:") ||
		strings.HasPrefix(actor.Username, "mcp-token:") {
		writeJSON(w, http.StatusForbidden,
			map[string]string{"error": "a dashboard session is required to mint tokens"})

		return nil, false
	}

	return actor, true
}

// availableSites returns the actor's grant map, falling back to the legacy
// single-site invariant when Sites is unhydrated.
func availableSites(actor *auth.User) map[uint32]auth.Role {
	if len(actor.Sites) > 0 {
		return actor.Sites
	}

	if actor.SiteID != 0 {
		return map[uint32]auth.Role{actor.SiteID: actor.Role}
	}

	return nil
}

// clampSites resolves the requested site_ids against the actor's grants:
// empty request ⇒ all available sites; otherwise every requested site must
// be one the actor holds at least `role` on (no escalation). Reuses
// auth.User.CanAccessSite so the rank logic stays single-sourced.
func clampSites(actor *auth.User, avail map[uint32]auth.Role, requested []uint32, role auth.Role) ([]uint32, error) {
	clamp := &auth.User{UserID: actor.UserID, Sites: avail}

	if len(requested) == 0 {
		return sortedKeys(avail), nil
	}

	seen := make(map[uint32]struct{}, len(requested))
	out := make([]uint32, 0, len(requested))

	for _, id := range requested {
		if _, dup := seen[id]; dup {
			continue
		}

		seen[id] = struct{}{}

		if !clamp.CanAccessSite(id, role) {
			return nil, fmt.Errorf("site %d not in your access scope", id)
		}

		out = append(out, id)
	}

	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })

	return out, nil
}

// tokenRole maps the request role to a valid read-only token role. Tokens
// are read-only — admin is never grantable. Default (empty) ⇒ api.
func tokenRole(raw string) (auth.Role, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "api":
		return auth.RoleAPI, true
	case "viewer":
		return auth.RoleViewer, true
	default:
		return "", false
	}
}

func ttlFor(days int, deps MCPTokenDeps) time.Duration {
	if days <= 0 { // unset or negative ⇒ fall back to the configured default
		days = deps.DefaultTTLDays
		if days <= 0 {
			days = defaultTokenTTLDays
		}
	}

	if days > maxTokenTTLDays {
		days = maxTokenTTLDays
	}

	return time.Duration(days) * 24 * time.Hour
}

func viewOf(m auth.MintedToken) mcpTokenView {
	return mcpTokenView{
		TokenID:    m.TokenID.String(),
		Name:       m.Name,
		SiteIDs:    m.SiteIDs,
		Role:       string(m.Role),
		CreatedAt:  m.CreatedAt,
		ExpiresAt:  m.ExpiresAt,
		LastUsedAt: m.LastUsedAt,
	}
}

func sortedKeys(m map[uint32]auth.Role) []uint32 {
	out := make([]uint32, 0, len(m))
	for id := range m {
		out = append(out, id)
	}

	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })

	return out
}

// mcpEmit records a token lifecycle event — token_id + actor + scope only,
// NEVER the raw token (Privacy Rule 4 / audit discipline).
func mcpEmit(deps MCPTokenDeps, r *http.Request, name audit.EventName, actor *auth.User, tokenID uuid.UUID, sites []uint32, role auth.Role) {
	if deps.Audit == nil {
		return
	}

	attrs := []slog.Attr{
		slog.String("actor_user_id", actor.UserID.String()),
		slog.String("token_id", tokenID.String()),
		slog.Any("site_ids", sites),
	}
	if role != "" {
		attrs = append(attrs, slog.String("role", string(role)))
	}

	deps.Audit.Event(r.Context(), name, attrs...)
}
