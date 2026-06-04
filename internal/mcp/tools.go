package mcp

import (
	"context"
	"encoding/json"

	"github.com/statnive/statnive.live/internal/auth"
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
		devicesTool(),
		funnelTool(),
	}
}

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
