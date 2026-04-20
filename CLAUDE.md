# statnive-live

> **statnive.live** — High-performance, privacy-aware analytics for high-traffic websites.
> Self-hosted or SaaS. First customer: Filimo (10-20M DAU).

## Project Goals

1. **Security first** — data protection is #1 priority above all features
2. **Minimum cost, maximum performance** — 8c/32GB handles ~100–200M events/day with careful tuning (doc 19); 8c/64GB (Hetzner AX42) is the SaaS floor with headroom
3. **Generic platform** — business logic lives in custom events/goals/funnels, never hardcoded
4. **Multi-tenant from day 1** — `site_id` on all raw + rollup tables, `WHERE site_id = ?` on every query, SaaS-ready
5. **Self-contained & isolation-capable** — the final binary must run fully air-gapped on a single server with **zero required outbound connections**. All third-party services (Let's Encrypt, Telegram, license server, GeoIP updates, analytics CDNs) are **opt-in only**. Tested by running under `iptables -P OUTPUT DROP`.

## Stack

- **Backend:** Go 1.22+, single binary, go-chi router, go-chi/httprate for rate limiting
- **Database:** ClickHouse (single node, MergeTree + **3 AggregatingMergeTree rollups in v1**: `hourly_visitors`, `daily_pages`, `daily_sources`) using `AggregateFunction(uniqCombined64, FixedString(16))` (HyperLogLog, 0.5% error). Add `daily_geo`, `daily_devices`, `daily_users` in v1.1 when the corresponding dashboard panels ship. Rollup `ORDER BY` leads with `site_id`. Time column is **`DateTime('UTC')`** (seconds precision — `DateTime64(3)` rejected per doc 24 §Sec 2: hourly grain makes ms precision a 4 B/row waste). **Reject all mutable-row engines** (Collapsing, VersionedCollapsing) — AggregatingMergeTree rollups sidestep cancel-row ordering bugs entirely. Migrations use `{{if .Cluster}}` Go templates from day 1 (doc 24 §Migration 0029) so single-node → Distributed is a config flip, not a rewrite.
- **Frontend:** Preact + @preact/signals + uPlot + Frappe Charts (~50KB minified / ~15KB gzipped), embedded via go:embed
- **Tracker:** Vanilla JS ~1.2KB minified / ~600B gzipped (doc 20), sendBeacon + fetch keepalive, text/plain
- **Identity:** Three layers — user_id (site sends) → cookie → BLAKE3-128 hash; daily salt derived deterministically from a **single master secret + site_id + IRST date** (`HMAC(master_secret, site_id || YYYY-MM-DD IRST)`). One secret across all tenants — site_id in the HMAC input provides per-site cryptographic separation without per-site key management.
- **Privacy:** Iran = no GDPR; cookies + user_id allowed. **SaaS (hosted outside Iran) = GDPR applies to EU visitors** — customer DPA, consent banner, and user access/erasure rights required on SaaS tier. IRST = UTC+3:30, no DST since Sept 2022; store UTC in ClickHouse, convert at API layer only.

## Architecture Rules (Non-Negotiable)

1. **Raw table is WRITE-ONLY** — dashboard never queries `events_raw` (except funnels via `windowFunnel()`, cached 1h)
2. **All dashboard reads from rollups** — 3 materialized views in v1 (<100 KB/day total per site), up to 6 by v1.1
3. **1-hour delay, NOT real-time** — saves 98% query cost. Never build 5-min real-time.
4. **Client-side batching in Go** — WAL for durability, batch 500ms / 1000 rows, async inserts as safety valve only
5. **No Nullable columns** — use `DEFAULT ''` or `DEFAULT 0`. Nullable costs 10–200% on aggregations depending on type (doc 20 measured 2× on `Nullable(Int8)`).
6. **Enrichment order is locked** — per event: identity (visitor_hash) → bloom filter → GeoIP → UA → bot detection → channel (doc 22 §GAP 1). Order is asserted in integration tests. **Pre-pipeline fast-reject gate** (doc 24 §Sec 1 item 6) runs *before* the pipeline: UA length 16–500, non-ASCII UA, IP-as-UA, UUID-as-UA, `X-Purpose`/`Purpose`/`X-Moz` prefetch headers → `204 No Content`. Bot layering *inside* the pipeline is cheap-first: prefetch → UA length/charset → UA-is-IP/UUID → referrer spam → browser-version floor → UA keyword blacklist → UA regex blacklist.
7. **Defer before building.** If a feature isn't required to hit the 5 Project Goals or to support Filimo's first 90 days, it goes to v1.1 or v2. Applies to multi-sink alerts, DLQ tooling, subdomain-per-tenant routing, Polar customer portal, and anything else not load-bearing for launch.
8. **Central tenancy choke point** — every dashboard SQL path goes through `internal/storage/queries.go:whereTimeAndTenant()` (doc 24 §Sec 4 pattern 6). `WHERE site_id = ?` is the first clause. `ORDER BY` / `PARTITION BY` both lead with `site_id`. Any new query that skips this helper is a CI failure.

## License Rules (Critical)

- **ALL linked dependencies MUST be MIT/Apache/BSD/ISC** — no AGPL in the binary
- statnive-live is sold as SaaS outside Iran where AGPL Section 13 applies
- **DO NOT import pirsch-analytics/pirsch** (AGPL) — reference patterns only
- **DO NOT use knadh/koanf** (AGPL) — use viper (MIT) or env-only config
- Before adding any dependency, verify its license with `go-licenses`
- **CC-BY-SA-4.0 carve-out for non-linked data files only** (doc 28 §Gap 1 policy). IP2Location LITE DB23 and similar GeoIP BINs are data, not linked code — the binary surface gate does not apply. CC-BY-SA-4.0 §3(a)(1) "reasonable-manner based on the medium" attribution is satisfied for LITE **only** by delivering the verbatim string — *"This site or product includes IP2Location LITE data available from https://lite.ip2location.com."* — in **all three** of: (a) `LICENSE-third-party.md`, (b) the `/about` JSON endpoint, and (c) the dashboard footer. `--license` CLI flag alone does not satisfy §3(a)(1). All three surfaces are enforced at CI time by the Semgrep rule `geoip-attribution-string-present` shipped in [`geoip-pipeline-review`](.claude/skills/geoip-pipeline-review/README.md); full delivery matrix in [`geoip-pipeline-review/references/attribution.md`](.claude/skills/geoip-pipeline-review/references/attribution.md). Every major free city-level GeoIP DB is CC-BY-SA-4.0, so the previous blanket rejection was unsatisfiable; tier-by-tier posture is tabled in [`docs/tooling.md`](docs/tooling.md) § GeoIP licensing. Paid IP2Location DB23 Site License at Phase 10 waives attribution; until then LITE stays default.

## Privacy Rules (Non-Negotiable)

Iran allows cookies + `user_id` pass-through; the EU/SaaS tier does not. Both code paths live in the same binary — these rules are what keep them consistent.

1. **Raw IP never persisted** — IP enters the pipeline only for GeoIP lookup, then is discarded before the batch writer sees the row (`internal/enrich/geoip.go` contract, asserted by integration test).
2. **Daily rotating salts** — `HMAC(master_secret, site_id || YYYY-MM-DD IRST)`. Same visitor produces a different hash each day; salt is derived, never stored.
3. **SHA-256+ and BLAKE3 only** in any privacy/identity path. No MD5, no SHA-1 anywhere in the binary.
4. **User ID hashed before ClickHouse write** — `SHA-256(master_secret || site_id || user_id)`. Raw `user_id` is never logged, never written to disk, never shipped to audit sinks.
5. **Iran = cookies + user_id allowed; SaaS (hosted outside Iran) = GDPR applies to EU visitors** — customer DPA, consent banner, and subject access / erasure rights are required on the SaaS tier.
6. **DNT + GPC respected by default** on the SaaS tier; self-hosted operator decides per deployment.
7. **First-party tracker via `go:embed`** — no external CDN, no fingerprinting (no canvas / WebGL / font probing, no `navigator.plugins` enumeration).
8. **Salt rotation DELETES the previous salt file** — not overwrites. Overwriting leaves recoverable on-disk remnants and breaks the Recital 26 HLL-anonymous argument. Enforced by [`blake3-hmac-identity-review`](.claude/skills/blake3-hmac-identity-review/README.md) + [`gdpr-code-review`](.claude/skills/gdpr-code-review/README.md).
9. **`Sec-GPC: 1` and consent-decline short-circuit BEFORE hash computation** — not after. Computing-then-discarding is a processing event under GDPR Art. 4(2). SaaS DPA language (Recital 26 + C-413/23 + weekly rollup rebuild as safety net) lives in `docs/dpa-draft.md` (Phase 11).

## Security (12 Features, All v1)

1. TLS 1.3 via **manual PEM files** (`tls.cert_file` / `tls.key_file`). Works on Hetzner (LE cert renewed by a cron calling `certbot` separately), Iranian DC (internal CA or self-signed), and enterprise (customer root CA). One code path, zero outbound. `autocert` + Let's Encrypt integration slips to v1.1 once operator volume justifies auto-renewal.
2. ClickHouse localhost only (bound to 127.0.0.1, never exposed)
3. Hostname validation on `/api/event` (HMAC **skipped entirely** per doc 20 — hostname check is its own defense. Plausible/Umami do the same.)
4. Input validation (`http.MaxBytesReader` 8KB, field length limits, timestamp ±1h drift)
5. Rate limiting per IP via `go-chi/httprate` (100 req/s, burst 200, NAT-aware via X-Forwarded-For / X-Real-IP)
6. Dashboard auth (bcrypt + `crypto/rand` sessions, 14-day TTL, `SameSite=Lax` cookies for CSRF)
7. RBAC (admin / viewer / API-only). 2FA deferred to v2 (single-admin v1 with SSH-key-only server access).
8. Encrypted backups (`clickhouse-backup` + `age` + `zstd`, cron-scheduled, restore test on every release)
9. Disk encryption (LUKS **optional** — 40–50% I/O overhead; physical DC security + encrypted backups usually suffice. Re-evaluate per deployment.)
10. Audit log (JSONL via `slog`, append-only, **file sink only** in v1). Local syslog / remote sinks = v1.1.
11. User ID hashed before storage (SHA-256 of `master_secret || site_id || user_id`; never log raw user_id)
12. systemd hardening (`NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`, `CapabilityBoundingSet=`) + tracker served via `go:embed` from the analytics host (first-party, no external CDN, no SRI needed, ad-blocker-resistant)
13. **CGNAT-aware rate-limit tiering** — Iranian ASN (AS44244 Irancell / AS197207 MCI / AS57218 RighTel) on compound `(ip, site_id)` key at 1 K req/s sustained / 2 K burst; default 100/s fallback everywhere else; per-`site_id` global cap at 25 K req/s. ASN DB is **`iptoasn.com`** public-domain TSV (MaxMind GeoLite2 + IPLocate are CC-BY-SA — rejected per § License Rules). Enforced by [`ratelimit-tuning-review`](.claude/skills/ratelimit-tuning-review/README.md); **hard gate on Phase 10 Filimo cutover**.

## Isolation / Air-Gapped Capability (Non-Negotiable)

The final binary MUST run on a fully isolated server with zero required outbound connections. Every network-touching feature must be **optional and config-gated**.

| Capability | Default | Air-gapped mode | Fallback |
|---|---|---|---|
| TLS certificates | Manual PEM files | Manual PEM files (same) | One code path; operator rotates certs |
| License validation (v1) | Offline JWT verify | Offline JWT verify (same) | Always works offline |
| License phone-home (v2) | Opt-in per deployment | **Disabled** | Config: `license.phone_home = false` |
| GeoIP updates | Manual file drop | Manual file drop (same) | SCP/rsync the DB23 BIN file, SIGHUP to reload |
| Bot pattern updates | Embedded in binary | Embedded in binary (same) | Update via new release only |
| Monitoring alerts | File sink (JSONL) | File sink (same) | Syslog / Telegram are v1.1 opt-ins |
| Tracker JS | `go:embed` (first-party) | `go:embed` (same) | No external CDN ever |
| Frontend SPA | `go:embed` | `go:embed` (same) | No external CDN ever |
| Dependencies | Vendored (`go mod vendor`) | Vendored (same) | No `go mod download` at runtime |
| NTP | System NTP | Internal NTP server | Config required; IRST salt depends on correct date |

**Verification rule:** integration test runs the binary under `iptables -A OUTPUT -j DROP` (allowing only localhost + the configured tracker clients) and asserts all endpoints work, events ingest, rollups materialize, and dashboard renders.

## Workflow Rule — Always `/simplify` Before Committing

**Every code change must pass through `/simplify` before you propose a commit. This is non-negotiable.**

The rule applies to any edit under `cmd/**`, `internal/**`, `web/**`, `tracker/**`, `clickhouse/**`, `config/**`, `deploy/**`, or `.claude/**` — Go, TypeScript, Preact, tracker JS, SQL migrations, YAML config, tests, and fixtures all count.

### Procedure

1. Make the code change.
2. Invoke the `/simplify` skill via the Skill tool (skill name: `simplify`). It reviews the diff for reuse, quality, and efficiency via three parallel review agents and applies fixes directly.
3. Re-run the Test Gate (below) after `/simplify` has modified files.
4. Only after the gate is green, stage and commit. The pre-commit hook at `.githooks/pre-commit` will re-run the gate and reject the commit if anything regressed. If the hook rejects the commit, fix the issue and create a **new** commit — never `--amend`.

### Exceptions

- Pure-documentation changes (`*.md` outside `.claude/skills/`) may skip `/simplify` but still run `make lint` if any Go docstring was touched.
- Pure-asset changes (images, fonts, GeoIP `.BIN` drops) skip the entire gate.
- Emergency security hot-fixes explicitly flagged by the user with "skip simplify" are exempted — only when the user says so.

### Why

`/simplify` catches duplication, dead code, hot-path bloat, unsafe SQL, and Nullable-column regressions before they enter the tree. The pre-commit hook enforces the test/lint half but cannot invoke Claude Code skills from a shell — this `CLAUDE.md` rule is the only way to guarantee the `/simplify` step runs. Do not skip it.

### Other rules

- Every new dependency requires license verification (`go-licenses` must report MIT/Apache/BSD/ISC).
- Every API endpoint must have an integration test.
- ClickHouse schema changes go through migrations (embedded SQL, run on startup).
- Config changes to goals / funnels hot-reload via SIGHUP (no restart).

## Test Gate

From the repo root:

```bash
make test                   # Go unit tests (fast, <5s)
make test-integration       # Go + ClickHouse integration (needs `docker compose up clickhouse`)
make lint                   # golangci-lint + go vet + govulncheck
make bench                  # benchmarks (enrichment pipeline, rollup queries)
npm --prefix web run test   # Vitest (Preact dashboard)
npm --prefix web run lint   # eslint
```

The pre-commit hook runs `make test && make lint` plus `npm --prefix web run test` on staged frontend files only. The release gate (`make release`) additionally runs `make test-integration` and the air-gap test from the Enforcement section.

## Feature Scope

Full roadmap lives in [`PLAN.md`](PLAN.md) — 51 v1 + 10 v1.1 + 17 v2 features, phased across 20 weeks. Derived from research docs 17, 18, 24. v1 is load-bearing for Filimo's first 90 days + the 5 Project Goals; polish slips to v1.1; product expansion lives in v2.

### Deliberate skips (Pirsch has; statnive-live rejects)

- ClickHouse **cluster mode at v1** (single-node is the Architecture Rule; migrations are Distributed-ready from day 1 per [`.claude/skills/clickhouse-cluster-migration`](.claude/skills/clickhouse-cluster-migration/README.md))
- **Redis session cache** — breaks the single-binary / air-gap promise; WAL + in-memory replaces it
- **Bounce rate** — vanity metric; expose time-on-page + funnel drop-off as the honest answer

### Never

- **5-minute real-time** — rollup-based hourly is the line; breaks cost model
- **Bounce rate** — vanity metric per research docs 09 / 14
- **Multi-touch attribution** — last-touch channel grouping is the final answer

## Key Paths

- `cmd/statnive-live/main.go` — entry point
- `internal/ingest/` — HTTP handler, pipeline, WAL, consumer
- `internal/enrich/` — GeoIP, UA, channel, bot, bloom filter
- `internal/identity/` — BLAKE3 hash, salt rotation
- `internal/storage/` — ClickHouse client, queries, migrations
- `internal/dashboard/` — API endpoints (8 routes)
- `internal/auth/` — sessions, RBAC, audit
- `web/` — Preact SPA (embedded)
- `tracker/` — JS tracker (<2KB)
- `clickhouse/` — schema SQL + migrations
- `config/` — YAML config + sources.yaml (50+ Iranian referrer sources)
- `deploy/` — systemd, backup, iptables, docker-compose

## Testing

| Level | Tool | Location |
|-------|------|----------|
| Unit | Go testing | `*_test.go` alongside source |
| Integration (includes security assertions) | Go testing | `test/integration_test.go` |
| Load (smoke) | k6 | `test/k6/load-test.js` |
| Frontend | Vitest | `web/src/**/*.test.tsx` |

### ClickHouse-Oracle Assertion Hierarchy

Always use the highest applicable tier. Lower tiers are signal amplifiers for failure diagnosis — they are not acceptance evidence on their own.

| Tier | Method | Use When |
|------|--------|----------|
| 1 | **ClickHouse-oracle** — correlation query against rollup tables | Ingest, rollup correctness, attribution, multi-tenant isolation |
| 2 | **Network** — `httptest.Server` / route interception | Tracker transport, sendBeacon payload shape |
| 3 | **DOM / locator** — Playwright or Vitest RTL | Dashboard UI state, routing |
| 4 | **Screenshot** — `only-on-failure` | Debug artifact only |

### Analytics Invariant Thresholds

These are the release-blocking numbers. CI must assert them on every v1/v1.1 RC build.

- Event loss (server) ≤ 0.05%
- Event loss (client) ≤ 0.5%
- Duplicates ≤ 0.1%
- Attribution correctness ≥ 99.5%
- Consent / PII leaks = 0
- TTFB overhead ≤ +10% or +25 ms

## Dev Tooling

Claude Code skills + MCP server setup for this project live in [`docs/tooling.md`](docs/tooling.md) (not in CLAUDE.md — it's developer ergonomics, not product rules). That file covers the **original 4 skill collections** (cc-skills-golang, ClickHouse Agent Skills, trailofbits, marina-skill — doc 23 foundation), the **doc 25 additions** (anthropics/skills cherry-pick, JetBrains use-modern-go, agamm/claude-code-owasp, BehiSecc/VibeSec-Skill, izar/tm_skills, vercel-labs web-design-guidelines, obra/superpowers 5-skill subset, knip, constant-time-analysis), the **doc 27 additions** (`grc-gdpr`, `legal-compliance-check`, custom `wal-durability-review` + `ratelimit-tuning-review` + `gdpr-code-review` + `dsar-completeness-checker`), the **doc 28 additions** (custom `iranian-dc-deploy` + `clickhouse-operations-review` + `clickhouse-upgrade-playbook` + `geoip-pipeline-review`), the **10 + 4 project-local custom skills** (see § Enforcement below), **4 MCP servers** (Altinity ClickHouse, gopls, Hetzner, Grafana), and the phase → tooling mapping. The `/jaan-to:*` skills ship with the parent plugin and handle *what* (specs, scaffolds, tests, reviews); community collections handle *how* (Go/ClickHouse/security/deploy patterns); the 14 custom skills encode the 8 non-negotiable architecture rules + privacy + air-gap + Iranian-DC contract as CI-blocking guardrails.

**Do not install:** `anthropics/skills/web-artifacts-builder` (React+shadcn+CDN-fonts — air-gap violation, blows past bundle budget), `shajith003/awesome-claude-skills` (AI-slop), `sickn33/antigravity-awesome-skills`, `rohitg00/awesome-claude-code-toolkit` (inflated counts, low S/N). Reference: doc 25 §landscape.

### Custom-skill triggers (project-local guardrails — fire automatically)

| Touching | Fires |
|---|---|
| Dashboard SQL / `internal/storage/` | [`tenancy-choke-point-enforcer`](.claude/skills/tenancy-choke-point-enforcer/README.md) |
| New dep / outbound call / CDN URL | [`air-gap-validator`](.claude/skills/air-gap-validator/README.md) |
| `AggregatingMergeTree` DDL / MV | [`clickhouse-rollup-correctness`](.claude/skills/clickhouse-rollup-correctness/README.md) |
| Migration file | [`clickhouse-cluster-migration`](.claude/skills/clickhouse-cluster-migration/README.md) |
| `web/**`, `tracker/**` | [`preact-signals-bundle-budget`](.claude/skills/preact-signals-bundle-budget/README.md) |
| Crypto / identity code | [`blake3-hmac-identity-review`](.claude/skills/blake3-hmac-identity-review/README.md) |
| `internal/ingest/wal.go` / `consumer.go` / `tidwall/wal` | [`wal-durability-review`](.claude/skills/wal-durability-review/README.md) |
| `internal/ratelimit/**` / `httprate` / middleware chain | [`ratelimit-tuning-review`](.claude/skills/ratelimit-tuning-review/README.md) |
| `internal/privacy/**` / `/api/privacy/*` / tracker JS / EnrichedEvent | [`gdpr-code-review`](.claude/skills/gdpr-code-review/README.md) |
| New migration + erase.go + audit sink | [`dsar-completeness-checker`](.claude/skills/dsar-completeness-checker/README.md) |
| `deploy/**` / `ops/**` / DNS / TLS / NTP / license code | [`iranian-dc-deploy`](.claude/skills/iranian-dc-deploy/README.md) |
| `internal/enrich/geoip.go` / ip2location-go / SIGHUP wiring / attribution surfaces | [`geoip-pipeline-review`](.claude/skills/geoip-pipeline-review/README.md) |
| `migrations/*.sql` / `internal/ingest/**` / `internal/query/**` / `prometheus/*.rules.yml` | [`clickhouse-operations-review`](.claude/skills/clickhouse-operations-review/README.md) |
| `Engine=` or `{{if .Cluster}}` in migrations (advisory runbook) | [`clickhouse-upgrade-playbook`](.claude/skills/clickhouse-upgrade-playbook/README.md) |

### Anti-patterns (doc 28 §Anti-patterns) — absolute bans

These are enforced by the custom-skill Semgrep rules. Human-facing mirror here so PR review can reject on sight without needing the CI job to fire:

- **No Cloudflare on any IR-resident code path** — no CF for DNS, TLS, DDoS, or KV. OFAC 31 CFR 560.540(b)(3) + no IR POP. Enforced by `iran-no-cloudflare`.
- **No ACME / Let's Encrypt from inside Iran** — issue PEMs on outside-Iran `cert-forge` box, rsync inward, SIGHUP swap. Enforced by `iran-no-letsencrypt-in-binary`.
- **No fsnotify for GeoIP reload** — overlayfs/NFS/kqueue lose events silently; SIGHUP only. Enforced by `geoip-no-fsnotify-on-bin`.
- **No `OPTIMIZE TABLE ... FINAL` without `PARTITION`** — serializes merges, OOMs 8c/32GB, non-idempotent on AggregatingMergeTree. Sanctioned alternative is `OPTIMIZE ... PARTITION '...' FINAL DEDUPLICATE` off-peak or `min_age_to_force_merge_seconds=3600`. Enforced by `ch-ops-no-optimize-final-in-sql`.
- **No phone-home license check "even for telemetry"** — telemetry = "services rendered" under OFAC interpretation; `560.540(b)(3)` excludes. Offline Ed25519 JWT, zero `net.Dial`. Enforced by `iran-license-verify-must-be-offline`.
- **No AGPL linked into the binary; CC-BY-SA only via the § License Rules data-file carve-out** — OS daemons (chrony, acme.sh, knot, bind) are operator-installed and live outside the binary boundary. GeoIP BIN data qualifies as non-linked data. No other exceptions.
- **`{{if .Cluster}}` is DDL templating only, NOT cluster-upgrade automation** — data migration from MergeTree → ReplicatedMergeTree is manual via hard-link `ATTACH PARTITION`. See the advisory [`clickhouse-upgrade-playbook`](.claude/skills/clickhouse-upgrade-playbook/README.md) runbook.
- **Never ArvanCloud** — sanctioned + 2022 breach. Asiatech primary, ParsPack or Shatel backup.

### Routing for everything else

For community skills (Go / ClickHouse / security / frontend / methodology) and `/jaan-to:*` workflow skills, open [`docs/tooling.md`](docs/tooling.md) — full inventory, phase→tooling map, 4 MCP servers (Altinity ClickHouse, gopls, Hetzner, Grafana). Clamp for `frontend-design` / `vercel-labs/web-design-guidelines`: emit Preact-compatible output with self-hosted fonts only (air-gap rule).

## Single Source of Truth

`../statnive-workflow/jaan-to/docs/research/` (docs 14–28) is the canonical source for every architecture, feature, and threat-model decision in this project. Do **not** restate research conclusions in this `CLAUDE.md` or in skill prompts — reference by doc number and section only. When a decision changes, update the research doc; this file references it and never duplicates. Same rule applies to the feature matrix (doc 17, 18), the cost model (doc 19), the initial skill / MCP list (doc 23), the **AGPL-safe Pirsch pattern extraction (doc 24)** — reference only, never port — the **Claude-skills install matrix + custom-skill catalog (doc 25)**, the **three-gap closure (doc 27 — WAL durability / CGNAT rate limit / GDPR-on-HLL)**, and the **final-three-gap closure (doc 28 — GeoIP pipeline / Iranian DC deploy / ClickHouse ops + upgrade playbook)** — reference only, never restate in skill prompts.

## Enforcement

These integration tests pin the invariants in this file. They are Phase 0 / Phase 7 deliverables — listing them here makes the contract explicit so `/simplify` and PR review can reject regressions on day one.

- `test/integration/enrichment_order_test.go` — asserts pipeline order is `identity → bloom → geo → ua → bot → channel` (Architecture Rule 6).
- `test/integration/airgap_test.go` — runs the binary under `iptables -A OUTPUT -j DROP` and asserts ingest, rollup materialization, and dashboard rendering all work (Isolation rule).
- `test/integration/multitenant_test.go` — asserts every dashboard query includes `WHERE site_id = ?` and no cross-tenant row leaks (Project Goal 4).
- `test/integration/pii_leak_test.go` — asserts raw IP and raw `user_id` never appear in ClickHouse tables or in the JSONL audit log (Privacy Rules 1, 4).
- `test/security/no_agpl_test.go` — `go-licenses` asserts every direct + transitive dep is MIT / Apache / BSD / ISC (License Rules).
- `web/src/__tests__/tenant-isolation.test.tsx` — Vitest guard that Preact signal stores don't leak `site_id` state across dashboard views.

**Project-local SKILL.md guardrails** — fourteen scaffolded skills under `.claude/skills/` encode Architecture Rules 2/5/8 + Isolation + Privacy Rules 2/3/4 + Iranian-DC operational contract (doc 28) + GeoIP privacy (doc 28) + ClickHouse ops (doc 28) as triggerable guardrails. Full specs in each skill's `README.md`; triggers in § Dev Tooling above. Semgrep bodies + fixtures fill in per phase — doc-25 set shipped, doc-27 set mid-implementation, doc-28 set scaffolded for Weeks 17–22.

`/simplify` and PR review must reject any new unguarded query (no `WHERE site_id = ?`), any new dependency without a license check, any new outbound network call not behind a config flag, and any new `Nullable(...)` column.

## Research Documents

All architecture decisions are backed by research at:
`../statnive-workflow/jaan-to/docs/research/` (docs 14–27, 500+ sources). Doc 23 covers the initial Claude Code tooling recommendations (doc-23 foundation: 30 installed skills, 4 MCP servers). **Doc 24** is the AGPL-safe Pirsch pattern extraction (reference-only audit of `github.com/pirsch-analytics/pirsch` v6) — informs ingestion shape (pre-pipeline fast-reject, cross-day fingerprint grace, cheap-first bot ordering), ClickHouse schema (reject mutable-row engines, `DateTime` not `DateTime64`, templated DDL for Distributed upgrade), channel mapping (17-step decision tree, AI channel on day 1), and dashboard query architecture (`Filter → Store → queryBuilder` shape, `WITH FILL` gap-fill, central `whereTimeAndTenant` helper). **Zero Pirsch code ported.** **Doc 25** is the Claude-skills install matrix and custom-skill catalog — 8 community bundles, 6 custom `.claude/skills/` to author, and an explicit blacklist (`web-artifacts-builder`, `shajith003/awesome-claude-skills`, etc.). **Doc 27** closes the three gaps doc 25 couldn't: **WAL durability** (tidwall/wal semantics, fsyncgate 2018, ack-after-fsync group commit), **CGNAT-aware rate limiting** (Iranian ASN compound key, `iptoasn.com` public-domain TSV — MaxMind / IPLocate rejected as CC-BY-SA), and **GDPR on append-only HyperLogLog** (Recital 26 + C-413/23 anonymity argument + weekly rollup rebuild as safety net). Reference-only; never restate the matrix in skill prompts.
