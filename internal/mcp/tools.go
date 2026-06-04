package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/statnive/statnive.live/internal/about"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/storage"
)

// catalog is the ordered single source of truth for the tool surface. The
// no-gap parity gate, tools/list, and docs/mcp.md all derive from it. PR1
// ships the 3-tool spine; PR2/PR3 append the rest (the parity gate fails
// until every read surface has an entry or a documented exclusion).
func catalog() []toolDef {
	return []toolDef{
		listSitesTool(),
		overviewTool(),
		trendTool(),
		sourcesTool(),
		pagesTool(),
		campaignsTool(),
		seoTool(),
		realtimeTool(),
		geoTool(),
		compareTool(),
		propsListTool(),
		goalsListTool(),
		myAccessTool(),
		eventAuditTool(),
		siteConfigTool(),
		aboutTool(),
		systemHealthTool(),
		devicesTool(),
		funnelTool(),
	}
}

// eventNameCapCeiling is the CNIL consent-free event-name ceiling. event_audit
// reports cap_status="over" when a site's distinct event-name count exceeds it.
const eventNameCapCeiling = 3

// maxEventAuditNames bounds the event-name list event_audit returns (the query
// is already bounded; this is a defensive output cap).
const maxEventAuditNames = 500

// analyticsInputSchema is the shared site+range+filters schema for every
// site-scoped analytics tool. additionalProperties:false makes a typo'd or
// injected key a clean -32602. `offset` is intentionally absent.
var analyticsInputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "site": {"type": "string", "description": "Site slug, numeric site_id, or hostname. The caller must be authorized for it."},
    "range": {"type": "string", "default": "7d", "description": "Time window: 1h|24h|7d|30d|90d, or YYYY-MM-DD..YYYY-MM-DD (end-exclusive), in the site's timezone."},
    "filters": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "path": {"type": "string"},
        "referrer": {"type": "string"},
        "channel": {"type": "string"},
        "utm_source": {"type": "string"},
        "utm_medium": {"type": "string"},
        "utm_campaign": {"type": "string"},
        "utm_content": {"type": "string"},
        "utm_term": {"type": "string"},
        "country": {"type": "string"},
        "browser": {"type": "string"},
        "os": {"type": "string"},
        "device": {"type": "string"},
        "hit_props": {"type": "object", "additionalProperties": {"type": "string"}},
        "session_props": {"type": "object", "additionalProperties": {"type": "string"}},
        "user_props": {"type": "object", "additionalProperties": {"type": "string"}}
      }
    },
    "limit": {"type": "integer", "minimum": 1, "maximum": 500},
    "sort": {"type": "string"},
    "dir": {"type": "string", "enum": ["asc", "desc"]},
    "search": {"type": "string"}
  },
  "required": ["site"],
  "additionalProperties": false
}`)

// emptyInputSchema is for global tools that take no arguments.
var emptyInputSchema = json.RawMessage(`{"type": "object", "properties": {}, "additionalProperties": false}`)

// siteOnlyInputSchema is for site-scoped tools that need no range/filters
// (goals_list reads the in-memory snapshot).
var siteOnlyInputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {"site": {"type": "string", "description": "Site slug, numeric site_id, or hostname."}},
  "required": ["site"],
  "additionalProperties": false
}`)

// compareInputSchema = the analytics base + the required compare extras.
var compareInputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "site": {"type": "string", "description": "Site slug, numeric site_id, or hostname."},
    "range": {"type": "string", "default": "7d", "description": "1h|24h|7d|30d|90d or YYYY-MM-DD..YYYY-MM-DD (end-exclusive), site timezone."},
    "filters": {"type": "object", "additionalProperties": false, "properties": {
      "path": {"type": "string"}, "referrer": {"type": "string"}, "channel": {"type": "string"},
      "utm_source": {"type": "string"}, "utm_medium": {"type": "string"}, "utm_campaign": {"type": "string"},
      "utm_content": {"type": "string"}, "utm_term": {"type": "string"},
      "country": {"type": "string"}, "browser": {"type": "string"}, "os": {"type": "string"}, "device": {"type": "string"},
      "hit_props": {"type": "object", "additionalProperties": {"type": "string"}},
      "session_props": {"type": "object", "additionalProperties": {"type": "string"}},
      "user_props": {"type": "object", "additionalProperties": {"type": "string"}}
    }},
    "dimension": {"type": "string", "description": "The custom dimension to split on, as \"scope:name\" (e.g. \"session:ab_variant\")."},
    "goal": {"type": "string", "description": "The event name to count as a conversion."}
  },
  "required": ["site", "dimension", "goal"],
  "additionalProperties": false
}`)

// propsListInputSchema = site + range + the scope/limit knobs.
var propsListInputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "site": {"type": "string", "description": "Site slug, numeric site_id, or hostname."},
    "range": {"type": "string", "default": "7d", "description": "1h|24h|7d|30d|90d or YYYY-MM-DD..YYYY-MM-DD (end-exclusive), site timezone."},
    "scope": {"type": "string", "enum": ["hit", "session", "user"], "default": "hit", "description": "Custom-property scope to list."},
    "limit": {"type": "integer", "minimum": 1, "maximum": 500}
  },
  "required": ["site"],
  "additionalProperties": false
}`)

var overviewOutputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "pageviews": {"type": "integer"},
    "visitors": {"type": "integer"},
    "goals": {"type": "integer"},
    "revenue": {"type": "integer"},
    "rpv": {"type": "number"}
  }
}`)

var trendOutputSchema = json.RawMessage(`{
  "type": "array",
  "items": {
    "type": "object",
    "properties": {
      "day": {"type": "string", "format": "date-time"},
      "visitors": {"type": "integer"},
      "pageviews": {"type": "integer"},
      "goals": {"type": "integer"},
      "revenue": {"type": "integer"}
    }
  }
}`)

var listSitesOutputSchema = json.RawMessage(`{
  "type": "array",
  "items": {
    "type": "object",
    "properties": {
      "id": {"type": "integer"},
      "hostname": {"type": "string"},
      "enabled": {"type": "boolean"},
      "tz": {"type": "string"},
      "currency": {"type": "string"}
    }
  }
}`)

// rpvRowItems is the shared per-dimension row shape (visitors/views/goals/
// revenue/rpv plus the dimension columns) used by sources/pages/campaigns.
var sourcesOutputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "rows": {"type": "array", "items": {"type": "object", "properties": {
      "referrer_name": {"type": "string"}, "channel": {"type": "string"},
      "views": {"type": "integer"}, "visitors": {"type": "integer"},
      "goals": {"type": "integer"}, "revenue": {"type": "integer"}, "rpv": {"type": "number"}}}},
    "by_channel": {"type": "array", "items": {"type": "object", "properties": {
      "channel": {"type": "string"}, "views": {"type": "integer"}, "visitors": {"type": "integer"},
      "goals": {"type": "integer"}, "revenue": {"type": "integer"}, "rpv": {"type": "number"}}}}
  }
}`)

var pagesOutputSchema = json.RawMessage(`{
  "type": "array",
  "items": {"type": "object", "properties": {
    "pathname": {"type": "string"}, "views": {"type": "integer"}, "visitors": {"type": "integer"},
    "goals": {"type": "integer"}, "revenue": {"type": "integer"}, "rpv": {"type": "number"}}}
}`)

var campaignsOutputSchema = json.RawMessage(`{
  "type": "array",
  "items": {"type": "object", "properties": {
    "utm_campaign": {"type": "string"}, "utm_source": {"type": "string"}, "utm_medium": {"type": "string"},
    "utm_content": {"type": "string"}, "utm_term": {"type": "string"}, "channel": {"type": "string"},
    "views": {"type": "integer"}, "visitors": {"type": "integer"}, "goals": {"type": "integer"},
    "revenue": {"type": "integer"}, "rpv": {"type": "number"}}}
}`)

var seoOutputSchema = json.RawMessage(`{
  "type": "array",
  "items": {"type": "object", "properties": {
    "day": {"type": "string", "format": "date-time"}, "views": {"type": "integer"},
    "visitors": {"type": "integer"}, "goals": {"type": "integer"}, "revenue": {"type": "integer"}}}
}`)

var realtimeOutputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "hour_utc": {"type": "string", "format": "date-time"},
    "active_visitors": {"type": "integer"},
    "pageviews_last_hr": {"type": "integer"}
  }
}`)

var geoOutputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "top": {"type": "array", "items": {"type": "object", "properties": {
      "country_code": {"type": "string"}, "views": {"type": "integer"}, "visitors": {"type": "integer"},
      "goals": {"type": "integer"}, "revenue": {"type": "integer"}, "rpv": {"type": "number"}}}},
    "rows": {"type": "array", "items": {"type": "object", "properties": {
      "country_code": {"type": "string"}, "province": {"type": "string"}, "city": {"type": "string"},
      "views": {"type": "integer"}, "visitors": {"type": "integer"}, "goals": {"type": "integer"},
      "revenue": {"type": "integer"}, "rpv": {"type": "number"}}}}
  }
}`)

var compareOutputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "dimension": {"type": "string"}, "goal": {"type": "string"}, "control": {"type": "string"},
    "variants": {"type": "array", "items": {"type": "object", "properties": {
      "value": {"type": "string"}, "visitors": {"type": "integer"}, "goal_completions": {"type": "integer"},
      "conversion_rate": {"type": "number"}, "delta_pp": {"type": "number"}, "delta_rel": {"type": "number"},
      "p_value": {"type": "number"}, "significant": {"type": "boolean"},
      "ci_low": {"type": "number"}, "ci_high": {"type": "number"}}}}
  }
}`)

var propsListOutputSchema = json.RawMessage(`{
  "type": "array",
  "items": {"type": "object", "properties": {
    "name": {"type": "string"},
    "sample_values": {"type": "array", "items": {"type": "string"}},
    "last_seen": {"type": "string", "format": "date-time"}}}
}`)

var goalsListOutputSchema = json.RawMessage(`{
  "type": "array",
  "items": {"type": "object", "properties": {
    "name": {"type": "string"}, "pattern": {"type": "string"},
    "match_type": {"type": "string"}, "value": {"type": "integer"}}}
}`)

var myAccessOutputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "role": {"type": "string"},
    "wildcard": {"type": "boolean"},
    "sites": {"type": "array", "items": {"type": "object", "properties": {
      "site_id": {"type": "integer"}, "role": {"type": "string"}}}}
  }
}`)

var eventAuditOutputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "events": {"type": "array", "items": {"type": "object", "properties": {
      "name": {"type": "string"}, "count": {"type": "integer"}}}},
    "distinct": {"type": "integer"},
    "cap": {"type": "integer"},
    "cap_status": {"type": "string", "enum": ["ok", "over"]}
  }
}`)

var siteConfigOutputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "site_id": {"type": "integer"}, "hostname": {"type": "string"}, "slug": {"type": "string"},
    "plan": {"type": "string"}, "enabled": {"type": "boolean"}, "tz": {"type": "string"},
    "currency": {"type": "string"}, "jurisdiction": {"type": "string"}, "consent_mode": {"type": "string"},
    "respect_dnt": {"type": "boolean"}, "respect_gpc": {"type": "boolean"}, "track_bots": {"type": "boolean"},
    "event_allowlist": {"type": "array", "items": {"type": "string"}},
    "allowed_origins": {"type": "array", "items": {"type": "string"}}
  }
}`)

var aboutOutputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "version": {"type": "string"}, "git_sha": {"type": "string"}, "go_version": {"type": "string"},
    "attributions": {"type": "array", "items": {"type": "object", "properties": {
      "name": {"type": "string"}, "license": {"type": "string"}, "url": {"type": "string"}, "text": {"type": "string"}}}}
  }
}`)

var systemHealthOutputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "clickhouse": {"type": "string", "enum": ["up", "down", "unknown"]},
    "version": {"type": "string"},
    "checked_at": {"type": "string", "format": "date-time"}
  }
}`)

// listSitesTool is the discovery entry point: it tells the client which
// sites the actor may query. Global (not site-scoped); returns only sites
// the actor can read (wildcard actors get all).
func listSitesTool() toolDef {
	return toolDef{
		Name:         "list_sites",
		Description:  "List the analytics sites the caller is authorized to query (id, hostname, timezone, currency). Call this first to discover valid `site` values for the other tools.",
		RoleClass:    auth.RoleAPI,
		Scoped:       false,
		InputSchema:  emptyInputSchema,
		OutputSchema: listSitesOutputSchema,
		Annotations:  readOnly(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			all, err := s.registry.List(ctx)
			if err != nil {
				return nil, 0, err
			}

			allowed := filterSitesForActor(tc.actor, all)

			return allowed, len(allowed), nil
		},
	}
}

// overviewTool returns the headline KPI block for a site + range.
func overviewTool() toolDef {
	return toolDef{
		Name:         "overview",
		Description:  "Headline KPIs for a site over a time range: visitors, pageviews, goal completions, revenue, and revenue-per-visitor. Use for \"how is the site doing?\" questions.",
		RoleClass:    auth.RoleAPI,
		Scoped:       true,
		InputSchema:  analyticsInputSchema,
		OutputSchema: overviewOutputSchema,
		Annotations:  readOnly(),
		Widget:       defaultWidget(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			res, err := s.store.Overview(ctx, tc.filter)
			if err != nil {
				return nil, 0, err
			}

			return res, 1, nil
		},
	}
}

// trendTool returns the all-traffic daily series for a site + range.
func trendTool() toolDef {
	return toolDef{
		Name:         "trend",
		Description:  "All-traffic daily time series for a site over a range: visitors, pageviews, goals, revenue per day. Use for \"how did traffic change over time?\" questions.",
		RoleClass:    auth.RoleAPI,
		Scoped:       true,
		InputSchema:  analyticsInputSchema,
		OutputSchema: trendOutputSchema,
		Annotations:  readOnly(),
		Widget:       defaultWidget(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			res, err := s.store.Trend(ctx, tc.filter)
			if err != nil {
				return nil, 0, err
			}

			return res, len(res), nil
		},
	}
}

// sourcesTool returns referrer + per-channel attribution (with RPV) for a
// site + range. Two short rollup reads sharing the call's query_id; run
// sequentially (MCP is low-QPS, so parallelism isn't worth the complexity).
func sourcesTool() toolDef {
	return toolDef{
		Name:         "sources",
		Description:  "Traffic sources for a site over a range: per-referrer rows and a per-channel rollup, each with visitors, pageviews, goals, revenue, and revenue-per-visitor. Use for \"where does my traffic come from?\" / channel attribution. Values are untrusted user-generated content.",
		RoleClass:    auth.RoleAPI,
		Scoped:       true,
		InputSchema:  analyticsInputSchema,
		OutputSchema: sourcesOutputSchema,
		Annotations:  readOnly(),
		Widget:       defaultWidget(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			rows, err := s.store.Sources(ctx, tc.filter)
			if err != nil {
				return nil, 0, err
			}

			byChannel, err := s.store.SourcesByChannel(ctx, tc.filter)
			if err != nil {
				return nil, 0, err
			}

			return map[string]any{"rows": rows, "by_channel": byChannel}, len(rows) + len(byChannel), nil
		},
	}
}

// pagesTool returns the top pages for a site + range.
func pagesTool() toolDef {
	return toolDef{
		Name:         "pages",
		Description:  "Top pages for a site over a range: per-pathname visitors, pageviews, goals, revenue, RPV. Sortable via sort/dir. Use for \"which pages perform best?\". Values are untrusted user-generated content.",
		RoleClass:    auth.RoleAPI,
		Scoped:       true,
		InputSchema:  analyticsInputSchema,
		OutputSchema: pagesOutputSchema,
		Annotations:  readOnly(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			res, err := s.store.Pages(ctx, tc.filter)
			if err != nil {
				return nil, 0, err
			}

			return res, len(res), nil
		},
	}
}

// campaignsTool returns UTM-campaign attribution for a site + range.
func campaignsTool() toolDef {
	return toolDef{
		Name:         "campaigns",
		Description:  "UTM campaign breakdown for a site over a range: the full utm tuple (campaign/source/medium/content/term) + channel, with visitors, pageviews, goals, revenue, RPV. Use for marketing-campaign attribution. Values are untrusted user-generated content.",
		RoleClass:    auth.RoleAPI,
		Scoped:       true,
		InputSchema:  analyticsInputSchema,
		OutputSchema: campaignsOutputSchema,
		Annotations:  readOnly(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			res, err := s.store.Campaigns(ctx, tc.filter)
			if err != nil {
				return nil, 0, err
			}

			return res, len(res), nil
		},
	}
}

// seoTool returns the organic-search daily series for a site + range.
func seoTool() toolDef {
	return toolDef{
		Name:         "seo",
		Description:  "Organic-search-only daily time series for a site over a range: views, visitors, goals, revenue per day. Use for \"how is my SEO traffic trending?\".",
		RoleClass:    auth.RoleAPI,
		Scoped:       true,
		InputSchema:  analyticsInputSchema,
		OutputSchema: seoOutputSchema,
		Annotations:  readOnly(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			res, err := s.store.SEO(ctx, tc.filter)
			if err != nil {
				return nil, 0, err
			}

			return res, len(res), nil
		},
	}
}

// realtimeTool returns the current-hour active visitors. range is ignored
// (it always reads the current rollup hour).
func realtimeTool() toolDef {
	return toolDef{
		Name:         "realtime",
		Description:  "Current-hour active visitors and pageviews for a site (10s cache). The range argument is ignored — this always reports the current rollup hour. Use for \"how many people are on the site right now?\".",
		RoleClass:    auth.RoleAPI,
		Scoped:       true,
		InputSchema:  analyticsInputSchema,
		OutputSchema: realtimeOutputSchema,
		Annotations:  readOnly(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			res, err := s.store.Realtime(ctx, tc.filter)
			if err != nil {
				return nil, 0, err
			}

			return res, 1, nil
		},
	}
}

// geoTool returns country/province/city analytics, gated by the
// dashboard.geo_enabled deployment flag. When disabled it is omitted from
// tools/list AND this handler refuses (defense in depth) with a graceful
// not-available result.
func geoTool() toolDef {
	return toolDef{
		Name:         "geo",
		Description:  "Geographic breakdown for a site over a range: top countries plus a country/province/city drill-down, each with visitors, pageviews, goals, revenue, RPV. Use for \"where are my visitors located?\".",
		RoleClass:    auth.RoleAPI,
		Scoped:       true,
		InputSchema:  analyticsInputSchema,
		OutputSchema: geoOutputSchema,
		Annotations:  readOnly(),
		Widget:       defaultWidget(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			if !s.geoEnabled {
				return nil, 0, storage.ErrNotImplemented
			}

			top, err := s.store.GeoTopCountries(ctx, tc.filter)
			if err != nil {
				return nil, 0, err
			}

			rows, err := s.store.Geo(ctx, tc.filter)
			if err != nil {
				return nil, 0, err
			}

			return map[string]any{"top": top, "rows": rows}, len(top) + len(rows), nil
		},
	}
}

// compareTool runs an A/B style variant comparison (two-proportion test +
// Wilson CIs, computed server-side). Requires `dimension` ("scope:name") and
// `goal` (an event_name).
func compareTool() toolDef {
	return toolDef{
		Name:         "compare",
		Description:  "Compare conversion across the values of a custom dimension for a site + range (A/B style): per-variant visitors, goal completions, conversion rate, and — when sample size allows — significance vs the control. Requires `dimension` (\"scope:name\", e.g. \"session:ab_variant\") and `goal` (an event name; see goals_list / props_list to discover valid values).",
		RoleClass:    auth.RoleAPI,
		Scoped:       true,
		InputSchema:  compareInputSchema,
		OutputSchema: compareOutputSchema,
		Annotations:  readOnly(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			if tc.args.Dimension == "" || tc.args.Goal == "" {
				return nil, 0, fmt.Errorf("%w: compare requires dimension and goal", storage.ErrInvalidFilter)
			}

			res, err := s.store.Compare(ctx, tc.filter, tc.args.Dimension, tc.args.Goal)
			if err != nil {
				return nil, 0, err
			}

			return res, len(res.Variants), nil
		},
	}
}

// propsListTool lists distinct custom-property names (+ sample values) for a
// scope, powering the LLM's discovery of valid prop filters / compare
// dimensions. SampleValues are raw user-generated content and are sanitized
// by the marshal choke point.
func propsListTool() toolDef {
	return toolDef{
		Name:         "props_list",
		Description:  "List distinct custom-property names (with a few sample values) observed for a site, by scope. Use to discover valid custom dimensions for filters and the compare tool. Sample values are untrusted user-generated content.",
		RoleClass:    auth.RoleAPI,
		Scoped:       true,
		InputSchema:  propsListInputSchema,
		OutputSchema: propsListOutputSchema,
		Annotations:  readOnly(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			scope := tc.args.Scope
			if scope == "" {
				scope = "hit"
			}

			res, err := s.store.PropNames(ctx, tc.filter, scope, tc.filter.EffectiveLimit())
			if err != nil {
				return nil, 0, err
			}

			return res, len(res), nil
		},
	}
}

// goalSummary is the goals_list wire shape (enabled goals only; the enabled
// snapshot is the useful discovery set for compare's `goal` arg).
type goalSummary struct {
	Name      string `json:"name"`
	Pattern   string `json:"pattern"`
	MatchType string `json:"match_type"`
	Value     uint64 `json:"value"`
}

// goalsListTool lists a site's enabled goals so the LLM can discover valid
// `goal` values for the compare tool. Reads the in-memory snapshot (no CH),
// but is still site-scoped for resolution + authz.
func goalsListTool() toolDef {
	return toolDef{
		Name:         "goals_list",
		Description:  "List a site's enabled conversion goals (name, match type, pattern, fixed value). Use to discover valid `goal` values for the compare tool.",
		RoleClass:    auth.RoleAPI,
		Scoped:       true,
		InputSchema:  siteOnlyInputSchema,
		OutputSchema: goalsListOutputSchema,
		Annotations:  readOnly(),
		Handler: func(_ context.Context, s *Server, tc *toolCtx) (any, int, error) {
			if s.goals == nil {
				return []goalSummary{}, 0, nil
			}

			gs := s.goals.GoalsForSite(tc.siteID)
			out := make([]goalSummary, 0, len(gs))

			for _, g := range gs {
				out = append(out, goalSummary{
					Name:      g.Name,
					Pattern:   g.Pattern,
					MatchType: string(g.MatchType),
					Value:     g.Value,
				})
			}

			return out, len(out), nil
		},
	}
}

// siteGrant is one (site_id, role) entry in a my_access response.
type siteGrant struct {
	SiteID uint32 `json:"site_id"`
	Role   string `json:"role"`
}

// accessInfo is the my_access wire shape — the ACTOR'S OWN access only. Never
// carries email / user_id / other users (Privacy Rule 4).
type accessInfo struct {
	Role     string      `json:"role"`
	Wildcard bool        `json:"wildcard"` // true ⇒ may read every site (legacy bearer / --all-sites)
	Sites    []siteGrant `json:"sites"`
}

// myAccessTool answers "what can I read / who has access to site X?" for the
// CALLING actor only — its global role + per-site grants. Any authenticated
// actor; global (no site arg). Privacy-safe: no email / user_id / other users.
func myAccessTool() toolDef {
	return toolDef{
		Name:         "my_access",
		Description:  "Report the calling actor's own analytics access: global role and the sites they may read (with per-site role). Use to answer \"what can I see?\" / \"do I have access to site X?\". Returns only the caller's own grants — never other users or any PII.",
		RoleClass:    auth.RoleAPI,
		Scoped:       false,
		InputSchema:  emptyInputSchema,
		OutputSchema: myAccessOutputSchema,
		Annotations:  readOnly(),
		Handler: func(_ context.Context, _ *Server, tc *toolCtx) (any, int, error) {
			u := tc.actor
			out := accessInfo{Role: string(u.Role)}

			switch {
			case isWildcardActor(u):
				out.Wildcard = true
			case u.Sites != nil:
				for _, id := range u.SiteIDs() {
					out.Sites = append(out.Sites, siteGrant{SiteID: id, Role: string(u.Sites[id])})
				}
			case u.SiteID != 0:
				out.Sites = []siteGrant{{SiteID: u.SiteID, Role: string(u.Role)}}
			}

			return out, len(out.Sites) + 1, nil
		},
	}
}

// eventAuditTool reports a site's distinct event-name cardinality + CNIL
// cap status (admin-only). Backed by the off-interface
// *ClickHouseStore.EventNameCardinality. Event names are operator/UGC strings
// → sanitized by the marshal choke point.
func eventAuditTool() toolDef {
	return toolDef{
		Name:         "event_audit",
		Description:  "Admin: per-site custom event-name cardinality over a range, plus cap_status against the CNIL consent-free 3-event ceiling. Use to answer \"am I over the consent-free event cap?\". Event names are untrusted user-generated content.",
		RoleClass:    auth.RoleAdmin,
		Scoped:       true,
		InputSchema:  analyticsInputSchema,
		OutputSchema: eventAuditOutputSchema,
		Annotations:  readOnly(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			if s.concrete == nil {
				return nil, 0, storage.ErrNotImplemented
			}

			rows, err := s.concrete.EventNameCardinality(ctx, tc.siteID, tc.filter.From, tc.filter.To)
			if err != nil {
				return nil, 0, err
			}

			distinct := len(rows)

			if len(rows) > maxEventAuditNames {
				rows = rows[:maxEventAuditNames]
			}

			status := "ok"
			if distinct > eventNameCapCeiling {
				status = "over"
			}

			return map[string]any{
				"events":     rows,
				"distinct":   distinct,
				"cap":        eventNameCapCeiling,
				"cap_status": status,
			}, distinct, nil
		},
	}
}

// siteConfigTool returns a site's read-only configuration (admin-only):
// enabled flag, timezone, currency, consent posture, jurisdiction, bot/GPC/DNT
// flags, event allowlist, allowed origins, plan. No PII.
func siteConfigTool() toolDef {
	return toolDef{
		Name:         "site_config",
		Description:  "Admin: a site's read-only configuration — enabled, timezone, currency, plan, jurisdiction, consent_mode, respect_dnt/gpc, track_bots, event_allowlist, allowed_origins. Use to answer \"which sites enforce GPC?\" / \"what's site X's consent mode?\".",
		RoleClass:    auth.RoleAdmin,
		Scoped:       true,
		InputSchema:  siteOnlyInputSchema,
		OutputSchema: siteConfigOutputSchema,
		Annotations:  readOnly(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			sa, err := s.registry.LookupSiteByID(ctx, tc.siteID)
			if err != nil {
				return nil, 0, err
			}

			return map[string]any{
				"site_id":         sa.ID,
				"hostname":        sa.Hostname,
				"slug":            sa.Slug,
				"plan":            sa.Plan,
				"enabled":         sa.Enabled,
				"tz":              sa.Site.TZ, // Site.TZ vs SitePolicy.TZ — must qualify
				"currency":        sa.Currency,
				"jurisdiction":    sa.Jurisdiction,
				"consent_mode":    sa.ConsentMode,
				"respect_dnt":     sa.RespectDNT,
				"respect_gpc":     sa.RespectGPC,
				"track_bots":      sa.TrackBots,
				"event_allowlist": sa.EventAllowlist,
				"allowed_origins": sa.AllowedOrigins,
			}, 1, nil
		},
	}
}

// aboutTool returns the build version + third-party data attributions
// (incl. the CC-BY-SA IP2Location LITE notice the license requires on the
// /about surface). Any authenticated actor; global; no PII.
func aboutTool() toolDef {
	return toolDef{
		Name:         "about",
		Description:  "Build/version info for this statnive-live instance plus required third-party data attributions (e.g. IP2Location LITE). Use to report which build an answer came from.",
		RoleClass:    auth.RoleAPI,
		Scoped:       false,
		InputSchema:  emptyInputSchema,
		OutputSchema: aboutOutputSchema,
		Annotations:  readOnly(),
		Handler: func(_ context.Context, s *Server, _ *toolCtx) (any, int, error) {
			return map[string]any{
				"version":      s.build.Version,
				"git_sha":      s.build.GitSHA,
				"go_version":   s.build.GoVersion,
				"attributions": about.DefaultAttributions(),
			}, 1, nil
		},
	}
}

// systemHealthTool reports what the separate MCP process can know about
// backend health: ClickHouse connectivity (a Ping) + build version. It does
// NOT report the daemon's in-memory WAL fill / cert expiry (that state lives
// in the running daemon, not this process). Admin-only; global.
func systemHealthTool() toolDef {
	return toolDef{
		Name:         "system_health",
		Description:  "Admin: liveness of the analytics backend as seen by the MCP process — ClickHouse connectivity and build version. Does NOT include the daemon's WAL/cert/alert state (those live in the running server).",
		RoleClass:    auth.RoleAdmin,
		Scoped:       false,
		InputSchema:  emptyInputSchema,
		OutputSchema: systemHealthOutputSchema,
		Annotations:  readOnly(),
		Handler: func(ctx context.Context, s *Server, _ *toolCtx) (any, int, error) {
			ch := "unknown"

			if s.health != nil {
				if err := s.health.Ping(ctx); err != nil {
					ch = "down"
				} else {
					ch = "up"
				}
			}

			return map[string]any{
				"clickhouse": ch,
				"version":    s.version,
				"checked_at": s.now().UTC().Format(time.RFC3339),
			}, 1, nil
		},
	}
}

// devicesTool is reserved: the daily_devices rollup ships in a later phase,
// so the Store returns storage.ErrNotImplemented and the dispatcher maps it
// to a graceful "not yet available" result (not -32601).
func devicesTool() toolDef {
	return toolDef{
		Name:        "devices",
		Description: "Device / browser / OS breakdown for a site. Not yet available in this build (waiting on the daily_devices rollup).",
		RoleClass:   auth.RoleAPI,
		Scoped:      true,
		InputSchema: analyticsInputSchema,
		Annotations: readOnly(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			res, err := s.store.Devices(ctx, tc.filter)
			if err != nil {
				return nil, 0, err
			}

			return res, len(res), nil
		},
	}
}

// funnelTool is reserved: windowFunnel ships in a later phase. Passes nil
// steps; the Store returns storage.ErrNotImplemented today.
func funnelTool() toolDef {
	return toolDef{
		Name:        "funnel",
		Description: "Conversion funnel step counts + drop-off for a site. Not yet available in this build (waiting on windowFunnel).",
		RoleClass:   auth.RoleAPI,
		Scoped:      true,
		InputSchema: analyticsInputSchema,
		Annotations: readOnly(),
		Handler: func(ctx context.Context, s *Server, tc *toolCtx) (any, int, error) {
			res, err := s.store.Funnel(ctx, tc.filter, nil)
			if err != nil {
				return nil, 0, err
			}

			return res, len(res.Steps), nil
		},
	}
}
