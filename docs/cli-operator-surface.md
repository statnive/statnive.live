# CLI & MCP — operator + agent surfaces

> Referenced from [PLAN.md § Launch Sequence](../PLAN.md). v1.1 CLI + v2 MCP server specs. Full content lives here; PLAN.md points.

The single binary already runs as the analytics daemon (`statnive-live` with no arguments boots the HTTP server). v1.1 + v2 add two more entry points so operators don't have to reach for `clickhouse-client` + raw SQL, and so AI assistants can answer customer questions directly from rollup data.

Both surfaces share the existing `internal/storage` package — no parallel data paths, no separate auth model. Air-gap rules from [PLAN.md § Air-Gapped / Isolated Deployment](deployment.md#air-gapped--isolated-deployment) apply unchanged: stdio MCP transport works on a fully isolated host; HTTP MCP is opt-in and disabled by default.

## v1.1 — Operator CLI (`statnive` subcommands, ~1 week)

Same binary, sub-command dispatcher (`spf13/cobra`, Apache-2.0). Default sub-command stays `serve` so existing systemd units / Dockerfiles don't break — running the binary with no args is identical to `statnive serve`.

Every sub-command writes to stdout in JSON (`--output=json`, default) or human-readable table (`--output=table`); exits non-zero on error. `--config=path/to/statnive-live.yaml` overrides the default config search.

### Subcommands (alphabetical)

| Command | Purpose | Notes |
|---|---|---|
| `statnive backup snapshot` | Wraps `clickhouse-backup` + `age` + `zstd` per Phase 8. | Verifies output via SHA-256; refuses to overwrite. |
| `statnive backup restore <file>` | Restores an encrypted snapshot to a fresh CH. | Idempotent; runs migrations before applying rows. |
| `statnive bloom dump` | Emit current bloom-filter approx-count + size. | Diagnostic only. |
| `statnive bloom reset` | Truncate `${wal_dir}/bloom.dat` (next boot starts cold). | Requires `--yes` confirmation. |
| `statnive doctor` | Health snapshot: CH ping, WAL fill ratio, disk free, cert expiry, DB23 freshness, audit-log tail. | Same data as `/healthz` + extras for shell troubleshooting. |
| `statnive license issue` | Generate a signed Ed25519 license JWT. | Reads private key from age-encrypted file; never logs. |
| `statnive license verify <file>` | Verify a license file against the embedded public key. | Returns parsed claims on success. |
| `statnive migrate status` | Show applied migration versions. | Reads from `statnive.schema_migrations`. |
| `statnive migrate up` | Apply pending migrations. | Same path the daemon uses on boot. |
| `statnive secret generate` | Hex-encoded 32-byte master secret to stdout. | Operator pipes to `chmod 0600 config/master.key`. |
| `statnive serve` | Run the analytics HTTP server. | **Default**; preserves current behavior. |
| `statnive sites add --hostname X --slug Y` | Insert a row into `statnive.sites`. | Validates hostname uniqueness + DNS-resolvable. |
| `statnive sites disable --site N` | Flip `enabled = 0`. | Soft delete; preserves historical rows. |
| `statnive sites list` | Tabular listing of registered sites. | |
| `statnive stats overview --site N --range 7d` | Quick CLI read of the overview panel. | Wraps the dashboard API; useful for cron + ssh-only ops. |
| `statnive users add --email X --role admin\|viewer\|api` | Create dashboard auth user. | Lands once Phase 2 auth ships. |
| `statnive users reset-password --email X` | Generate a random password + bcrypt-hash it. | Prints once; never logs. |

### Why a CLI in v1.1, not v1

- v1's tiny operator surface (Phase 8 deploy → Phase 9 dogfood → Phase 10 SamplePlatform) needs maybe 4–5 of these commands — `secret generate`, `license issue`, `sites add`, `migrate`, `doctor`. Those can ship as discrete shell scripts in `deploy/` for v1.
- A real CLI binds the operator UX to a single tool, which is the right shape for v1.1 once we have ≥1 self-hosted customer running their own ops.
- Cobra adds ~200 KB to the binary. Acceptable; binary is currently <20 MB.

**Skill + dep surface:** `golang-cli` skill (already installed). Deps: `spf13/cobra` (Apache-2.0). No new transitive risk.

## v2 — MCP server (read-only analytics tools, ~2 weeks)

Implements the [Model Context Protocol](https://modelcontextprotocol.io) so Claude / other MCP-aware clients can query analytics data via natural language. Same binary; new sub-command:

```
statnive mcp serve --transport=stdio                    # default; air-gap-safe
statnive mcp serve --transport=http --listen=127.0.0.1:8081 --token=$TOKEN
```

Stdio transport works on a fully isolated host (operator pipes through `claude mcp add statnive -- statnive mcp serve`). HTTP transport is opt-in, requires Bearer token auth (same session token as the dashboard), and is listed in [deployment.md § Opt-in external services](deployment.md#opt-in-external-services-all-off-by-default-in-air-gapped-mode) as `mcp.http.enabled = false` by default.

### v2 read-only tools (mapped 1:1 to dashboard API endpoints)

| MCP tool | Wraps | Returns |
|---|---|---|
| `statnive_overview` | `GET /api/stats/overview` | Visitors / pageviews / goals / revenue for a site + range. |
| `statnive_sources` | `GET /api/stats/sources` | Per-channel breakdown with RPV. |
| `statnive_pages` | `GET /api/stats/pages` | Top pages (sortable). |
| `statnive_geo` | `GET /api/stats/geo` | Iranian provinces + cities (v1.1 rollup). |
| `statnive_devices` | `GET /api/stats/devices` | Device / browser / OS breakdown. |
| `statnive_funnel` | `GET /api/stats/funnel` | Funnel step counts + drop-off %. |
| `statnive_campaigns` | `GET /api/stats/campaigns` | UTM-campaign attribution. |
| `statnive_seo` | `GET /api/stats/seo` | Organic search trend (richer panels = v1.1). |
| `statnive_realtime` | `GET /api/realtime/visitors` | Last-5-min active visitors (10s cache). |

### Tool argument shape (every tool)

```jsonc
{
  "site_slug": "SamplePlatform-com",   // resolved server-side to site_id; auth must permit
  "range": "7d",               // 1h | 1d | 7d | 30d | "2026-04-01..2026-04-18"
  "filters": { ... }           // optional; matches the Filter struct from Phase 3
}
```

### Auth model

Every MCP request carries a Bearer token (HTTP) or is bound to the operator's local file permissions (stdio). Token → `(user_id, role, allowed_site_ids[])`. Tools enforce `WHERE site_id IN (...)` via the same central `whereTimeAndTenant()` helper Phase 3 lands. Cross-tenant attempts return `mcp.invalid_params`, not silent empty results.

### Deferred to post-v2

- **Write tools** — `statnive_create_goal`, `statnive_create_funnel`, `statnive_disable_site`. Require dashboard auth UX to absorb MCP-issued changes first; ship after v2 dashboard CRUD stabilizes.
- **`statnive_query` (sandboxed SELECT)** — operator-grade ad-hoc SQL with `WHERE site_id = ?` enforcement + a per-token query budget. Powerful but needs a query parser to enforce the tenancy clause; defer until a customer asks.
- **Anomaly detection tool** — `statnive_anomaly_detect(site, range)`. Needs a baseline model. Defer until we have ≥3 months of production rollups to seed.

### Dependency + license surface

- MCP Go SDK candidate: [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) (MIT — verify at v2 dep-add time)
- No new transitive AGPL risk
- Both stdio + HTTP transports are in-tree; no daemon-of-a-daemon

### Air-gap invariant (re-stated)

Stdio MCP requires zero outbound. HTTP MCP listens on `127.0.0.1` by default; operator must explicitly bind to a routable address + open the firewall to expose it externally — the binary won't dial out to register itself anywhere.
