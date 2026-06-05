# statnive-live MCP server (read-only agent surface)

> **New to this?** Start with the plain-language guide: **[mcp-guide.md](mcp-guide.md)** (what it is, how to use it, creative examples). This page is the precise operator reference.

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

Three ways to connect, by audience:

| You are… | Path | How |
|---|---|---|
| A **dashboard customer** (no shell/binary/config) | **HTTP-Bearer token** | Mint a token in the dashboard → paste the `claude mcp add` command. See *Connect from the dashboard* below. The universal path — works in Claude Code, Claude Desktop, and any MCP host. |
| An **operator / CLI** on the box | **stdio** | Run the binary directly. Air-gap-safe; needs the binary + config + `--allow-sites`. |
| A **ChatGPT** user | **OAuth app** | Install the published ChatGPT app and sign in (statnive is the OAuth authorization server — see `docs/mcp-chatgpt.md`, shipping with the OAuth-AS work). |

### Connect from the dashboard (no server access)

The self-serve path for end-users — no shell, binary, or config needed. Requires the operator to have enabled it (`mcp.tokens.enabled: true` + `mcp.http.enabled: true` behind TLS) and to publish the MCP URL (`mcp.public_url`).

1. In the dashboard, open **Connect** (the "Connect your AI assistant" screen).
2. Click **Create token**, give it a name, optionally narrow the sites, and copy the token — **it is shown only once**.
3. Paste the ready-made command (also shown on that screen):
   ```bash
   claude mcp add --transport http https://app.statnive.live/mcp \
     --header "Authorization: Bearer <YOUR_TOKEN>"
   ```
4. Ask your assistant a question, e.g. *"How did organic search convert on site 1 last week?"*

The token is **read-only** and scoped to exactly the sites you can already see. Revoke it any time from the same screen (takes effect immediately). Tokens are SHA-256-hashed at rest; the raw value is never stored or logged.

### stdio (operator / CLI — air-gap-safe)

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
  tokens:                          # self-serve dashboard tokens (end-user path)
    enabled: false                 # turn on to expose the "Connect" screen + /api/mcp/tokens
    max_per_user: 20
    ttl_default_days: 90
  public_url: ""                   # customer-facing /mcp base for the dashboard "Connect" command, e.g. https://app.statnive.live/mcp
```
`geo` visibility follows `dashboard.geo_enabled`. To offer the **dashboard self-serve token** path, the operator enables both `mcp.http.enabled` (the transport the tokens authenticate against) and `mcp.tokens.enabled` (the mint UI/endpoints), and sets `mcp.public_url`.

### Deploying the public `/mcp` (SaaS operators)

For a managed SaaS where end-users connect over the internet:

1. Run the MCP HTTP transport on the prod host (loopback) and `mcp.tokens.enabled: true`:
   ```bash
   statnive-live mcp serve --transport http --config /etc/statnive-live/config.yaml
   ```
   Dashboard-minted tokens authenticate here automatically — the `mcp serve` HTTP auth chain consults the same ClickHouse token store the dashboard mints into (no extra wiring).
2. Reverse-proxy the existing public TLS edge (the one already terminating `app.statnive.live`) to route `POST /mcp` → `127.0.0.1:8081/mcp`. Keep the binary loopback-bound; the proxy owns TLS. (A direct non-loopback bind is refused unless `posture: saas` **and** `mcp.http.tls_cert_file`/`tls_key_file` are set.)
3. Set `mcp.public_url: https://app.statnive.live/mcp` so the dashboard "Connect" screen emits the correct command.
4. Post-deploy smoke (gated `STATNIVE_PROBE_MCP_ENABLED=true`): mint a token for the probe `site_id=9999`, `curl` `/mcp` with it, assert an oracle match, revoke, assert `401`. (Wired into `scripts/prod-probe.sh`.)

Read-only forever: the token path adds no MCP write surface, and the air-gap/inside-iran default leaves `mcp.tokens.enabled` + `mcp.http.enabled` off.

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

## ChatGPT-app profile (v2.5, SaaS-only)

A ChatGPT app is an MCP server reachable over public HTTPS with OAuth. The v2.5 `chatgpt-app` profile turns the same read-only tool surface into a **text-only ChatGPT app** — no widgets (those are v3) — without touching the air-gap build.

**It is gated, fail-closed, and built separately:**

- The OAuth 2.1 / JWKS verifier is compiled **only with `-tags chatgpt_app`**. The default and air-gap binaries ship a stub that refuses `profile=chatgpt-app` — so no IdP/outbound/JWKS code ever enters those builds (`make licenses` / `air-gap-validator` stay clean). Build the SaaS binary with `go build -tags chatgpt_app ./cmd/statnive-live`.
- Activation requires **all** of: `posture: saas`, `mcp.http.profile: chatgpt-app`, `mcp.http.oauth.enabled: true`, a non-empty `mcp.http.oauth.allowed_site_ids`, and TLS on a public bind. Any missing piece → the server refuses to start.

```yaml
posture: saas
mcp:
  http:
    enabled: true
    profile: "chatgpt-app"
    listen: ":443"
    tls_cert_file: "/etc/.../fullchain.pem"
    tls_key_file:  "/etc/.../privkey.pem"
    oauth:
      enabled: true
      issuer:   "https://your-idp.example.com"
      audience: "https://mcp.statnive.live"
      required_scope: "analytics:read"          # optional
      resource_metadata_url: "https://mcp.statnive.live/.well-known/oauth-protected-resource"
      allowed_site_ids: [1, 4]                   # REQUIRED: the sites a verified token may read
```

**How it behaves:**
- Every request must carry a valid Bearer **access token** (RS256/ES256 JWT). The verifier checks signature (against the issuer's JWKS), `iss`, `aud`, `exp`/`nbf`, and the required scope; `alg=none`/HS* are rejected (alg-confusion guard). Invalid/absent → `401` with a `WWW-Authenticate: Bearer resource_metadata=…` discovery hint.
- `GET /.well-known/oauth-protected-resource` serves RFC 9728 metadata so ChatGPT/the IdP can discover the authorization server.
- `tools/list` advertises per-tool `_meta.securitySchemes` (`noauth` + `oauth2`) so ChatGPT knows to run the auth-code + PKCE flow.
- Responses are returned as SSE when the client sends `Accept: text/event-stream` (stateless, one event per response), or plain JSON otherwise.
- The authenticated principal is an `api`-role actor scoped to the token's consented `site_ids` **intersected with** `allowed_site_ids` (the deployment ceiling) — never a wildcard. A token issued by the statnive AS (below) carries a `site_ids` consent claim, so the actor reads only the sites the user consented to (PR-E M1); a token with no claim (a legacy/external-IdP token) falls back to the full ceiling. The same four authz gates (role floor → grant hydration → `ActorCanReadSite` → SQL choke point) + budgets + sanitizer apply exactly as on every other transport.

## Authorization server — statnive issues its own tokens (PR-E, SaaS-only)

The v2.5 profile above is the **resource server** (it *verifies* tokens). For a turnkey ChatGPT app, statnive is also the **authorization server** (it *issues* them) — so an end-user connects with their existing **dashboard account**, no third-party IdP. Auth-code + PKCE S256 + a consent screen; refresh-token rotation with reuse-detection. Same build-tag + posture gating as the verifier (zero AS code, zero new deps in the default/air-gap binary — the AS is hand-rolled stdlib, not a library: `make licenses`/`air-gap-validator` stay clean).

**Where it runs:** the AS mounts on the **main daemon** (not `mcp serve`) so `/authorize` shares the dashboard origin + session. The reverse proxy routes `/authorize`, `/token`, `/register`, `/.well-known/oauth-authorization-server`, `/.well-known/jwks.json`, `/consent` to the daemon; `/mcp` + `/.well-known/oauth-protected-resource` to `mcp serve`. Build the daemon with `-tags chatgpt_app`.

**Endpoints:** `GET /.well-known/oauth-authorization-server` (RFC 8414; advertises `code_challenge_methods_supported:["S256"]` only) · `GET /.well-known/jwks.json` (the signing key the verifier fetches) · `POST /register` (RFC 7591 DCR, **admin-authed**) · `GET /authorize` (dashboard-session login → consent) · `POST /consent` · `POST /token` (auth-code + refresh grants).

```yaml
posture: saas
mcp:
  http:
    oauth:
      issuer:   "https://app.statnive.live"        # the AS issuer
      audience: "https://app.statnive.live/mcp"     # RFC 8707 resource / aud
      allowed_site_ids: [1, 4]                       # the ceiling (shared with the verifier)
  oauth_as:
    enabled: true
    signing_key_file: "/etc/statnive-live/oauth_signing_key.pem"   # RSA, chmod 0600
    # access_ttl_seconds: 1800   refresh_ttl_seconds: 2592000   code_ttl_seconds: 60
```

**Provision the signing key** (operator laptop or the box; chmod 0600, never in git):

```bash
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out oauth_signing_key.pem
chmod 0600 oauth_signing_key.pem      # the loader REFUSES a group/world-readable key
```

Rotation: generate a new key, point `signing_key_file` at it, and list the **old public** key in `retired_key_files` for a grace window (default 24h) so in-flight tokens keep verifying; remove it after the window.

**Register the ChatGPT client** (after deploy, before store submission) — an admin POSTs DCR; the `client_secret` is shown **once**:

```bash
curl -sX POST https://app.statnive.live/register \
  -H "Cookie: statnive_session=<admin session>" -H 'Content-Type: application/json' \
  -d '{"client_name":"ChatGPT","redirect_uris":["https://chatgpt.com/connector_platform_oauth_redirect"]}'
# → {"client_id":"…","client_secret":"… (copy now)","redirect_uris":[…]}
```

`redirect_uris` are **exact-match** (no wildcards, no fragments, https-only except loopback). Pre-flight check before submitting: `SELECT count() FROM statnive.oauth_clients WHERE revoked=0` must be ≥ 1.

**End-user flow:** install the app → "Connect" → statnive `/authorize` (already logged into the dashboard) → consent screen lists the sites they may grant (their grants ∩ the ceiling) → approve → ChatGPT exchanges the code at `/token` → done. The issued JWT carries the consented `site_ids`, which the verifier enforces (M1).

**Security posture** (pinned by the J red-team in `test/oauth_as_integration_test.go`): PKCE S256 mandatory (plain/none rejected); exact redirect-URI match (open-redirect/smuggle rejected); single-use short-TTL codes (replay rejected); code bound to client + verifier (cross-client/PKCE-strip rejected); refresh rotation + family-revoke on reuse; consent server-clamped to the user's real grants (escalation rejected); codes/secrets/refresh-tokens SHA-256-hashed at rest, raw never logged.

## ChatGPT-app widgets (v3)

v3 adds the **UI layer**: an interactive widget so a tool's result renders as cards/tables (and, as an increment, charts) inside ChatGPT instead of plain text.

- Turn it on with `mcp.widgets.enabled: true`. The server then advertises the MCP **`resources`** capability, serves the widget at `ui://widget/statnive.html` (`resources/list` + `resources/read`), and `tools/list` emits per-tool `_meta.ui.resourceUri` for the top tools (`overview`, `trend`, `sources`, `geo`).
- **Air-gap-safe by construction:** the widget is a single, dependency-free, **zero-outbound** HTML file `go:embed`-ded into the binary (no CDN, no fonts, no analytics). Default off. Because it's static embedded content (no JS bundle deps), it needs no build tag — unlike the OAuth verifier.
- The widget is a **generic renderer**: it reads the calling tool's `structuredContent` via the portable MCP Apps bridge (`window.openai`) and renders KPI cards (object) or a table (array). Per-tool ECharts visualisations are an increment on the same contract — the data shape is each tool's `outputSchema`, unchanged. (ECharts adds a ~200 KB chunk; a future widget bundle gets its own size-limit profile.)
- All tool values are escaped in the widget (defence-in-depth on top of the server-side sanitizer).

### Submitting to the ChatGPT app store

When publishing the v2.5 + v3 app, the top rejection causes are pre-handled:

- **Tool annotations** — every tool ships `readOnlyHint:true, destructiveHint:false, openWorldHint:false` (the #1 rejection cause).
- **No undisclosed data** — outputs are aggregate-only; audit/output discipline already forbids session/trace IDs.
- **Privacy statement** — disclose that `props_list.sample_values` are customer-supplied UGC (may contain PII if mis-instrumented), and the data-retention / DSAR posture (`docs/rules/privacy-detail.md`).
- **Descriptions** — no comparative/"best"/"official" language.
- **Least-privilege OAuth** — a single `analytics:read` scope; the principal is site-scoped, never wildcard.
- **WCAG-AA** — the widget uses system fonts/colours + honours `prefers-color-scheme`.
- **Portable bridge** — the widget targets the MCP Apps surface (`window.openai`), not a legacy vendor global, so it also runs in Claude and other MCP hosts.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| Every site returns `-32602` over stdio | Fail-closed default — pass `--allow-sites` or `--all-sites`. |
| `-32602 insufficient role` on `event_audit`/`site_config`/`system_health` | Those are admin-only; an `api` Bearer can't reach them (by design). |
| `geo` missing from `tools/list` | `dashboard.geo_enabled=false` — enable it after the geo backfill. |
| HTTP refuses to start on a public address | Non-loopback bind needs `posture: saas` + TLS cert/key. |
| `isError: budget exhausted` | Per-actor query budget hit; back off or raise `mcp.budget.*`. |
| `devices`/`funnel` return "not yet available" | Reserved tools; ship with the `daily_devices` rollup / `windowFunnel`. |
| Daemon refuses to start with "mcp.oauth_as requires …" | The AS is fail-closed: needs `posture: saas`, `oauth.issuer`/`audience`, a `signing_key_file`, and a non-empty `allowed_site_ids`. |
| "signing key … is group/world-readable" at boot | `chmod 0600` the `signing_key_file` (a leaked key forges any token). `STATNIVE_DEV=1` bypasses the check for local dev only. |
| `/token` returns `invalid_grant` after a refresh | Refresh tokens rotate + are single-use; reusing a rotated one revokes the whole family (theft defence). Re-run the connect flow. |
| `mcp.oauth_as.enabled=true` but AS endpoints 404 / daemon errors about the stub | The binary was built without `-tags chatgpt_app`. Rebuild the SaaS binary with the tag (never ship it inside-iran/air-gap). |

## Without MCP

Every tool maps 1:1 to an existing dashboard/REST read. If you can't run the MCP server, the same data is available via the authenticated dashboard API (`/api/stats/*`, `/api/admin/*`). The MCP adds no new data path — a parity gate (`make mcp-parity`) enforces that every read surface has a tool.

## For contributors

Adding a read surface? The [`mcp-parity-enforcer`](../.claude/skills/mcp-parity-enforcer/SKILL.md) skill fires and `make mcp-parity` will fail until you ship the matching MCP tool + `internal/mcp/parity_test.go` coverage entry in the same PR. See PLAN.md §No-gap governance.
