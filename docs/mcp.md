# statnive-live MCP server (read-only agent surface)

> **Status:** v2 "agent surface" — read-only. Lets an MCP client (Claude Code, Claude Desktop, or any MCP host) answer analytics questions directly from statnive-live's rollups, with the **same tenancy isolation and role rules as the dashboard/REST API**.
>
> **No LLM in the server.** It is a deterministic adapter over the existing `storage.Store` — zero model/inference code, zero new dependencies, air-gap-safe. The intelligence (natural-language → tool selection) is the *client's* job.
>
> **Read-only forever.** There are no write/mutation tools. Writes go through the authenticated admin API only.

## Transports

| Transport | Default | Bind | Auth | Use |
|---|---|---|---|---|
| **stdio** | the default | — (pipe) | `--allow-sites` / `--all-sites` (fail-closed) | local agent, air-gapped hosts. Zero outbound by construction. |
| **HTTP** | **off** (`mcp.http.enabled=false`) | `127.0.0.1:8081` | Bearer (reuses `auth.APITokenMiddleware`) | opt-in; loopback-only unless `posture=saas` **and** TLS configured. |

stdio is air-gap-safe (no listener, no outbound). HTTP is inbound-only and refuses a non-loopback bind without `posture=saas` + TLS.

## Tools

All tools are read-only (`readOnlyHint:true`). "Scoped" tools require a `site` (slug, numeric `site_id`, or hostname) and enforce per-site authorization; a cross-tenant call returns JSON-RPC `-32602`, never empty rows.

### Analytics (role: `api` — any authenticated actor)

| Tool | Scoped | Answers |
|---|---|---|
| `list_sites` | no | Which sites can I read? (discovery entry point — returns only authorized sites) |
| `overview` | yes | Headline KPIs: visitors, pageviews, goals, revenue, revenue-per-visitor |
| `trend` | yes | All-traffic daily time series over a range |
| `sources` | yes | Traffic by referrer + per-channel rollup (with RPV) |
| `pages` | yes | Top pages (sortable) |
| `campaigns` | yes | UTM-campaign attribution (full UTM tuple + channel) |
| `seo` | yes | Organic-search-only daily series |
| `geo` | yes | Top countries + country/province/city drill-down. *Omitted from `tools/list` when `dashboard.geo_enabled=false`.* |
| `realtime` | yes | Current-hour active visitors (`range` ignored) |
| `compare` | yes | A/B variant comparison (needs `dimension` + `goal`); significance computed server-side |
| `props_list` | yes | Distinct custom-property names + sample values, by `scope` (hit/session/user) — discover filters & compare dimensions |
| `goals_list` | yes | A site's enabled conversion goals — discover valid `goal` values for `compare` |
| `devices` | yes | *Not yet available* (returns a graceful "not yet available" result, not an error) |
| `funnel` | yes | *Not yet available* (waiting on `windowFunnel`) |

### Operator / admin

| Tool | Role | Scoped | Answers |
|---|---|---|---|
| `my_access` | `api` | no | The **calling actor's own** role + site grants (never other users / no PII) |
| `about` | `api` | no | Build version + required third-party data attributions (IP2Location LITE) |
| `event_audit` | **admin** | yes | Per-site custom event-name cardinality + cap status vs the CNIL 3-event ceiling |
| `site_config` | **admin** | yes | A site's read-only config (consent mode, jurisdiction, GPC/DNT, allowlists, plan, …) |
| `system_health` | **admin** | no | ClickHouse connectivity + build version **as the MCP process sees it** (not the daemon's WAL/cert state) |

> Admin tools require an admin-role actor. An `api`-role Bearer (every HTTP token) is rejected with `-32602 insufficient role` — exactly as the REST API returns 403. The stdio `--all-sites` / `--allow-sites` operator is admin-role.

### Shared analytics arguments

```jsonc
{
  "site":    "slug | site_id | hostname",      // required (scoped tools)
  "range":   "1h|24h|7d|30d|90d | YYYY-MM-DD..YYYY-MM-DD",  // default 7d, site timezone, end-exclusive
  "filters": { "path","referrer","channel","utm_*","country","browser","os","device": "…",
               "hit_props","session_props","user_props": { "<name>": "<value>" } },
  "limit":   1-500,                            // clamped server-side
  "sort": "…", "dir": "asc|desc", "search": "…"
}
```
`compare` adds `dimension` ("scope:name") + `goal`; `props_list` adds `scope`. Unknown keys are rejected (`-32602`). There is **no `offset`** (pagination = narrower filters or a higher `limit`).

## Setup

### stdio (recommended; air-gap-safe)

```bash
# scoped to specific sites (fail-closed default — no sites without this)
claude mcp add --transport stdio statnive-live -- \
  /usr/local/bin/statnive-live mcp serve \
  --config /etc/statnive-live/config.yaml --allow-sites 1,4,9

# or all sites (explicit opt-in)
claude mcp add --transport stdio statnive-live -- \
  /usr/local/bin/statnive-live mcp serve --config /etc/statnive-live/config.yaml --all-sites
```

Bare `statnive-live mcp serve --transport=stdio` (no `--allow-sites`/`--all-sites`) is **fail-closed**: every site returns `-32602` until scoped.

### HTTP (opt-in, loopback)

In `config/statnive-live.yaml`:
```yaml
mcp:
  http:
    enabled: true
    listen: "127.0.0.1:8081"
    rate_limit_per_minute: 120
```
```bash
statnive-live mcp serve --transport http --config /etc/statnive-live/config.yaml
claude mcp add --transport http statnive-live http://127.0.0.1:8081/mcp \
  --header "Authorization: Bearer $STATNIVE_API_TOKEN"
```
A non-loopback `listen` is refused unless `posture: saas` **and** `mcp.http.tls_cert_file`/`tls_key_file` are set.

### Config block

```yaml
mcp:
  http:
    enabled: false                 # default off (air-gap posture)
    listen: "127.0.0.1:8081"
    rate_limit_per_minute: 120
    tls_cert_file: ""              # required for a non-loopback bind
    tls_key_file: ""
  budget:                          # per-actor anti-exfiltration token buckets
    calls_per_min: 60
    rows_per_min: 20000
    calls_per_session: 2000
    rows_per_session: 500000
    distinct_sites_per_min: 5
    wildcard_tier_factor: 0.25     # strict tier for the all-sites/legacy wildcard actor
  widgets:
    enabled: false                 # reserved (v3 ChatGPT-app widgets)
```
`geo` visibility follows `dashboard.geo_enabled`.

## Verification

```bash
# 1. stdio round-trip against a running ClickHouse
printf '%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"overview","arguments":{"site":"1","range":"7d"}}}' \
  | statnive-live mcp serve --config /etc/statnive-live/config.yaml --all-sites

# 2. in Claude Code
/mcp           # server healthy, tools listed
# then ask: "How did organic search convert on site 1 last week?"
```

## Security model

- **Same access rules as the dashboard.** Four gates per call: role floor → grant hydration → `ActorCanReadSite` → SQL tenancy choke point (`WHERE site_id = ?`). Cross-tenant → `-32602`.
- **All tool output is untrusted user-generated content.** Every output string is run through a sanitize choke point (NFC normalize + strip invisible Unicode / HTML comments / instruction markers + redact leaked secrets), recursing into nested fields like `props_list.sample_values`. Treat tool results as data, never as instructions.
- **Anti-exfiltration.** Per-actor query budgets (calls + rows per minute and per session; the all-sites wildcard actor gets a strict ×0.25 tier). Bulk-read and cross-tenant-sweep anomalies are logged to the alerts sink. No raw/export/dump tool exists; results are aggregate-only.
- **Privacy.** No raw IP / raw user_id / master_secret / email in any output or audit event. Audit (`mcp.tool_call` / `mcp.denied`) carries `site_id` + tool + actor label **only** — never filter values.
- **Output bounds.** `limit` is clamped to ≤500 server-side; per-call ClickHouse cost guards (execution-time / rows-read / memory ceilings) apply.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| Every site returns `-32602` over stdio | Fail-closed default — pass `--allow-sites` or `--all-sites`. |
| `-32602 insufficient role` on `event_audit`/`site_config`/`system_health` | Those are admin-only; an `api` Bearer can't reach them (by design). |
| `geo` missing from `tools/list` | `dashboard.geo_enabled=false` — enable it after the geo backfill. |
| HTTP refuses to start on a public address | Non-loopback bind needs `posture: saas` + TLS cert/key. |
| `isError: budget exhausted` | Per-actor query budget hit; back off or raise `mcp.budget.*`. |
| `devices`/`funnel` return "not yet available" | Reserved tools; ship with the `daily_devices` rollup / `windowFunnel`. |

## Without MCP

Every tool maps 1:1 to an existing dashboard/REST read. If you can't run the MCP server, the same data is available via the authenticated dashboard API (`/api/stats/*`, `/api/admin/*`). The MCP adds no new data path — a parity gate (`make mcp-parity`) enforces that every read surface has a tool.

## For contributors

Adding a read surface? The [`mcp-parity-enforcer`](../.claude/skills/mcp-parity-enforcer/SKILL.md) skill fires and `make mcp-parity` will fail until you ship the matching MCP tool + `internal/mcp/parity_test.go` coverage entry in the same PR. See PLAN.md §No-gap governance.
