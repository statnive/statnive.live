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
