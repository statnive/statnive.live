# statnive-live

> **statnive.live** — High-performance, privacy-aware analytics for high-traffic websites.
> Self-hosted or SaaS. First customer: SamplePlatform (10-20M DAU).

## Project Goals

1. **Security first** — data protection is #1 priority above all features.
2. **Minimum cost, maximum performance** — 8c/32GB handles 100–200M events/day (doc 19); 8c/64GB Hetzner AX42 is the SaaS floor with headroom.
3. **Generic platform** — business logic lives in custom events/goals/funnels, never hardcoded.
4. **Multi-tenant from day 1** — `site_id` on all raw + rollup tables, `WHERE site_id = ?` on every query, SaaS-ready.
5. **Self-contained & isolation-capable** — binary runs fully air-gapped on one server with **zero required outbound connections**. All third-party services opt-in. Tested under `iptables -P OUTPUT DROP`.

## Stack

- **Backend:** Go 1.22+, single binary, go-chi router, go-chi/httprate for rate limiting.
- **Database:** ClickHouse (single node, MergeTree + **3 AggregatingMergeTree rollups in v1**: `hourly_visitors`, `daily_pages`, `daily_sources`) using `AggregateFunction(uniqCombined64, FixedString(16))` (HLL, 0.5% error). Add `daily_geo`/`daily_devices`/`daily_users` in v1.1 as panels ship. Rollup `ORDER BY` leads with `site_id`. Time column is **`DateTime('UTC')`** (seconds, per doc 24 §Sec 2). **Reject mutable-row engines** (Collapsing/VersionedCollapsing). Migrations use `{{if .Cluster}}` templates from day 1 (doc 24 §Migration 0029) — single-node → Distributed is a config flip.
- **Frontend:** Preact + @preact/signals + uPlot + Frappe Charts (~50KB minified / ~15KB gzipped), embedded via go:embed.
- **Tracker:** Vanilla JS ~1.2KB minified / ~600B gzipped (doc 20), sendBeacon + fetch keepalive, text/plain.
- **Identity:** user_id (site sends) → cookie → BLAKE3-128 hash; daily salt = `HMAC(master_secret, site_id || YYYY-MM-DD IRST)`. One secret across tenants — site_id in HMAC input provides per-site cryptographic separation without per-site key management.
- **Privacy:** Iran = no GDPR (cookies + user_id allowed). **SaaS (outside Iran) = GDPR applies to EU visitors** — customer DPA, consent banner, access/erasure rights required. IRST = UTC+3:30, no DST since Sept 2022; store UTC in CH, convert at API layer only.

## Architecture Rules (Non-Negotiable)

1. **Raw table is WRITE-ONLY** — dashboard never queries `events_raw` (except funnels via `windowFunnel()`, cached 1h).
2. **All dashboard reads from rollups** — 3 materialized views in v1 (<100 KB/day/site), up to 6 by v1.1.
3. **1-hour delay, NOT real-time** — saves 98% query cost. Never build 5-min real-time.
4. **Client-side batching in Go** — WAL for durability, batch 500ms / 1000 rows. Async inserts are safety valve only.
5. **No Nullable columns** — use `DEFAULT ''` or `DEFAULT 0`. Nullable costs 10–200% on aggregations (doc 20 measured 2× on `Nullable(Int8)`). **Carve-out for test-instrumentation columns** (`test_run_id`, `test_generator_seq`, `generator_node_id`, `send_ts`): use typed `DEFAULT` sentinels (UUID zero / UInt64 0 / UInt16 0 / DateTime64(3) 0) to preserve the sparse-serialization path at >93.75% defaults, which doc 29 §6.1 proves is ~zero-cost in production. Never ship `Nullable(` for analytics columns.
6. **Enrichment order is locked** — per event: identity → bloom → GeoIP → UA → bot → channel (doc 22 §GAP 1, asserted in integration tests). **Pre-pipeline fast-reject gate** (doc 24 §Sec 1 item 6): UA length 16–500, non-ASCII UA, IP-as-UA, UUID-as-UA, `X-Purpose`/`X-Moz` prefetch → `204`. In-pipeline bot layering is cheap-first (prefetch → UA shape → referrer spam → browser-version floor → UA keyword/regex blacklist).
7. **Defer before building** — if a feature isn't required for the 5 Project Goals or SamplePlatform's first 90 days, it ships in v1.1 or v2. Applies to multi-sink alerts, DLQ tooling, subdomain-per-tenant routing, Polar customer portal, and anything else not load-bearing.
8. **Central tenancy choke point** — every dashboard SQL path goes through `internal/storage/queries.go:whereTimeAndTenant()` (doc 24 §Sec 4 pattern 6). `WHERE site_id = ?` is the first clause. `ORDER BY` / `PARTITION BY` lead with `site_id`. Any new query skipping this helper is a CI failure.

## License Rules (Critical)

- **ALL linked dependencies MUST be MIT/Apache/BSD/ISC** — no AGPL in the binary.
- statnive-live is sold as SaaS outside Iran where AGPL Section 13 applies.
- **DO NOT import pirsch-analytics/pirsch** (AGPL) — reference patterns only.
- **DO NOT use knadh/koanf** (AGPL) — use viper (MIT) or env-only config.
- Before adding any dependency, verify its license with `go-licenses`.
- **CC-BY-SA-4.0 carve-out for non-linked data files only** (doc 28 §Gap 1 policy). IP2Location LITE DB23 and similar GeoIP BINs are data, not linked code — the binary surface gate does not apply. CC-BY-SA-4.0 §3(a)(1) "reasonable-manner based on the medium" attribution is satisfied for LITE **only** by delivering the verbatim string — *"This site or product includes IP2Location LITE data available from https://lite.ip2location.com."* — in **all three** of: (a) `LICENSE-third-party.md`, (b) the `/about` JSON endpoint, and (c) the dashboard footer. `--license` CLI flag alone does not satisfy §3(a)(1). All three surfaces are enforced at CI time by the Semgrep rule `geoip-attribution-string-present` shipped in [`geoip-pipeline-review`](.claude/skills/geoip-pipeline-review/README.md); full delivery matrix in [`geoip-pipeline-review/references/attribution.md`](.claude/skills/geoip-pipeline-review/references/attribution.md). Every major free city-level GeoIP DB is CC-BY-SA-4.0, so the previous blanket rejection was unsatisfiable; tier-by-tier posture is in [`docs/tooling.md`](docs/tooling.md) § GeoIP licensing. Paid IP2Location DB23 Site License at Phase 10 waives attribution; until then LITE stays default.

## Privacy Rules (Non-Negotiable)

Iran allows cookies + `user_id`; the EU/SaaS tier does not. Both code paths live in the same binary — these rules keep them consistent. Extended GDPR Art./Recital-26/C-413/23 chain in [`docs/rules/privacy-detail.md`](docs/rules/privacy-detail.md). **SaaS-only ops contract** (Netcup Art. 28(3) DPA, sub-processor register, DNS hygiene, server hardening above Netcup's Annex 1 TOM) lives in [`docs/rules/netcup-vps-gdpr.md`](docs/rules/netcup-vps-gdpr.md) — load before provisioning or re-provisioning the SaaS VPS or when a sub-processor changes; canonical sub-processor list at [`docs/compliance/subprocessor-register.md`](docs/compliance/subprocessor-register.md); customer-facing DPA at [`docs/dpa-draft.md`](docs/dpa-draft.md).

1. **Raw IP never persisted** — IP enters the pipeline only for GeoIP lookup, discarded before the batch writer sees the row (`internal/enrich/geoip.go` contract, asserted by integration test).
2. **Daily rotating salts** — `HMAC(master_secret, site_id || YYYY-MM-DD IRST)`. Derived, never stored.
3. **SHA-256+ and BLAKE3 only** in any privacy/identity path. No MD5, no SHA-1 anywhere in the binary.
4. **User ID hashed before ClickHouse write** — `SHA-256(master_secret || site_id || user_id)`. Raw `user_id` never logged, never on disk, never in audit sinks.
5. **Iran = cookies + user_id allowed; SaaS = GDPR applies to EU visitors** — customer DPA, consent banner, subject access / erasure rights required.
6. **DNT + GPC respected by default** on SaaS; self-hosted operator decides per deployment.
7. **First-party tracker via `go:embed`** — no external CDN, no fingerprinting (no canvas / WebGL / font probing, no `navigator.plugins` enumeration).
8. **Salt rotation DELETES the previous salt file** — not overwrites (recoverability + Recital 26 — see [detail](docs/rules/privacy-detail.md#rule-8--salt-rotation-deletes-the-previous-salt-file)). Enforced by [`blake3-hmac-identity-review`](.claude/skills/blake3-hmac-identity-review/README.md) + [`gdpr-code-review`](.claude/skills/gdpr-code-review/README.md).
9. **`Sec-GPC: 1` and consent-decline short-circuit BEFORE hash computation** — not after (GDPR Art. 4(2)). SaaS DPA legal chain in [detail](docs/rules/privacy-detail.md#rule-9--consent--gpc-short-circuit-before-hash-computation); draft at `docs/dpa-draft.md` (Phase 11).

## Security (14 Features, All v1)

Extended operational detail (fallback CA list, full systemd option list, LUKS I/O reasoning, CGNAT ASN list) in [`docs/rules/security-detail.md`](docs/rules/security-detail.md).

1. TLS 1.3 via **manual PEM files** (`tls.cert_file` / `tls.key_file`) — Hetzner (LE cron), Iranian DC (internal CA / cert-forge rsync), enterprise (customer root CA). One code path, zero outbound. Autocert + LE slips to v1.1.
2. ClickHouse localhost only (bound to 127.0.0.1, never exposed).
3. Hostname validation on `/api/event` (HMAC **skipped entirely** per doc 20 — hostname check is its own defense; Plausible/Umami do the same).
4. Input validation (`http.MaxBytesReader` 8KB, field length limits, timestamp ±1h drift).
5. Rate limiting per IP via `go-chi/httprate` (100 req/s, burst 200, NAT-aware via X-Forwarded-For / X-Real-IP).
6. Dashboard auth (bcrypt + `crypto/rand` sessions, 14-day TTL, `SameSite=Lax` cookies for CSRF).
7. RBAC (admin / viewer / API-only). 2FA deferred to v2.
8. Encrypted backups (`clickhouse-backup` + `age` + `zstd`, cron-scheduled, restore test on every release).
9. Disk encryption (LUKS — **required** on shared-tenant cloud VPS including the Netcup VPS 2000 G12 NUE D1 host per [`docs/rules/netcup-vps-gdpr.md` § 6.1](docs/rules/netcup-vps-gdpr.md#6-vps-server-side-hardening-layered-above-netcups-annex-1-tom); optional on dedicated cage hardware — 40–50% I/O overhead; tier matrix in [`docs/luks.md`](docs/luks.md)).
10. Audit log (JSONL via `slog`, append-only, **file sink only** in v1). Syslog / remote sinks = v1.1.
11. User ID hashed before storage (SHA-256 of `master_secret || site_id || user_id`; never log raw user_id).
12. systemd hardening (`NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`, `CapabilityBoundingSet=CAP_NET_BIND_SERVICE`) + tracker via `go:embed` (first-party, no external CDN, ad-blocker-resistant).
13. **CGNAT-aware rate-limit tiering** — Iranian ASN (AS44244 Irancell / AS197207 MCI / AS57218 RighTel) on compound `(ip, site_id)` key at 1 K req/s sustained / 2 K burst; default 100/s fallback elsewhere; per-`site_id` global cap at 25 K req/s. ASN DB is **`iptoasn.com`** public-domain TSV (MaxMind GeoLite2 + IPLocate are CC-BY-SA — rejected per § License Rules). Enforced by [`ratelimit-tuning-review`](.claude/skills/ratelimit-tuning-review/README.md); **hard gate on Phase 10 SamplePlatform cutover**.
14. **Outbound allow-list for opt-in features** (OWASP A10 SSRF guard). When any opt-in outbound path is enabled (ACME/LE in v1.1, Polar.sh checkout in Phase 11, paid IP2Location DB23 download, license phone-home v2, Telegram, email SMTP), outbound `http.Client` / `net.Dialer` traffic routes through `internal/httpclient/guarded.go` which (a) rejects destinations not on the config-declared FQDN allow-list in `config.outbound.allowlist`, (b) rejects all RFC 1918 / loopback / link-local / CGNAT `100.64.0.0/10` ranges *after* DNS resolution (DNS-rebinding guard), (c) forces `https://` scheme. The air-gap default build keeps `config.outbound.allowlist: []`, so the Isolation invariant (§ Isolation / Air-Gapped) is unchanged — the allow-list only applies to operators who opt in. Enforced by [`air-gap-validator`](.claude/skills/air-gap-validator/README.md) Semgrep rule `airgap-no-raw-httpclient` + unit test in `internal/httpclient/`. Verification in PLAN.md §51.

## Isolation / Air-Gapped Capability (Non-Negotiable)

The final binary MUST run on a fully isolated server with zero required outbound connections. Every network-touching feature must be **optional and config-gated**.

| Capability | Default | Air-gapped mode | Fallback |
|---|---|---|---|
| TLS certificates | Manual PEM files | Manual PEM files (same) | One code path; operator rotates certs |
| License validation (v1) | Offline JWT verify | Offline JWT verify (same) | Always works offline |
| License phone-home (v2) | Opt-in per deployment | **Disabled** | `license.phone_home = false` |
| GeoIP updates | Manual file drop | Manual file drop (same) | SCP/rsync DB23 BIN, SIGHUP reload |
| Bot pattern updates | Embedded in binary | Embedded in binary (same) | Update via new release only |
| Monitoring alerts | File sink (JSONL) | File sink (same) | Syslog / Telegram are v1.1 opt-ins |
| Tracker JS | `go:embed` (first-party) | `go:embed` (same) | No external CDN ever |
| Frontend SPA | `go:embed` | `go:embed` (same) | No external CDN ever |
| Dependencies | Vendored (`go mod vendor`) | Vendored (same) | No `go mod download` at runtime |
| NTP | System NTP | Internal NTP server | IRST salt depends on correct date |

**Verification:** integration test runs binary under `iptables -A OUTPUT -j DROP` (localhost + configured tracker clients only); all endpoints work, events ingest, rollups materialize, dashboard renders.

## Workflow Rule — Always `/simplify` Before Committing

Every code change must pass through `/simplify` before you propose a commit. **Non-negotiable** for any edit under `cmd/**`, `internal/**`, `web/**`, `tracker/**`, `clickhouse/**`, `config/**`, `deploy/**`, or `.claude/**` — Go, TypeScript, Preact, tracker JS, SQL, YAML, tests, and fixtures all count.

**Procedure:** (1) make the change, (2) invoke the `simplify` skill via the Skill tool, (3) re-run the Test Gate below after `/simplify` has modified files, (4) commit only when green. Pre-commit hook at `.githooks/pre-commit` re-runs the gate and rejects regressions — if it rejects, fix and create a **new** commit (never `--amend`).

**Exceptions:** pure-docs (`*.md` outside `.claude/skills/`) skip `/simplify` but run `make lint` if Go docstrings touched. Pure assets (images, fonts, GeoIP `.BIN` drops) skip entirely. Emergency security hot-fixes flagged by the user with "skip simplify" are exempt only when the user explicitly says so.

**Other non-negotiables:** every new dep requires `go-licenses` verification (MIT/Apache/BSD/ISC); every API endpoint requires an integration test; ClickHouse schema changes go through migrations (embedded SQL, run on startup); `goals` / `funnels` config hot-reloads via SIGHUP (no restart).

## Workflow Rule — `LEARN.md` is canonical institutional memory

[`LEARN.md`](LEARN.md) captures hard-won lessons from prior cutovers / outages / bug-discovery sessions. **Read it BEFORE planning any task that touches:**

- `deploy/` (`airgap-bundle.sh`, `airgap-install.sh`, `airgap-update-geoip.sh`, systemd units, iptables rules)
- `cmd/statnive-live/main.go` (especially the `loadConfig` / viper / flag-parsing surface)
- `config/statnive-live.yaml.example` (schema parity with binary defaults)
- Operator-facing scripts (`step-b.sh`, `step-d.sh`, `statnive-deploy.sh`, `courier-iran.sh`)
- Any cutover SOP in [`docs/runbook.md`](docs/runbook.md)

The point is to **avoid re-discovering bugs we already caught**. Each LEARN.md entry encodes a specific bug class; cross-reference before designing a fix or a new operator path.

**Update LEARN.md when:**

- A cutover (Milestone N) completes — capture every surprise as a lesson.
- A bug-discovery class hits ≥3 related bugs in a single operation.
- A production outage / SSH lockout / on-call incident is resolved — root cause + preventive measure goes in.

**Per-lesson format** (enforced):

> **Lesson N — <one-sentence rule>**
>
> 1. **What we did** — the action that broke (factual, no blame).
> 2. **Why it broke** — the underlying mismatch / assumption.
> 3. **The fix we applied** — what unblocked us in the moment.
> 4. **Preventive measure** — the durable change so the next operator doesn't hit it (CI gate / doc / process).

Lessons live forever. Don't delete entries; mark them `[obsolete]` if the underlying cause is gone (e.g. a CI gate now catches the bug class).

## Test Gate

```bash
make test                   # Go unit tests (fast, <5s)
make test-integration       # Go + ClickHouse integration (needs `docker compose up clickhouse`)
make lint                   # golangci-lint + go vet + govulncheck
make bench                  # benchmarks (enrichment pipeline, rollup queries)
npm --prefix web run test   # Vitest (Preact dashboard)
npm --prefix web run lint   # eslint
```

Pre-commit hook runs `make test && make lint` + `npm --prefix web run test` on staged frontend files. Release gate (`make release`) additionally runs `make test-integration` + the air-gap test.

The `make test` / `make test-integration` / `make lint` / `npm run test` suite is the **per-PR CI tier**. The **graduation gate** (Locust + k6 + Vegeta + wrk2 + observability VPS, 72h soak + 6-scenario chaos + breakpoint per doc 29 §4) is a separate **pre-Phase-cutover** process invoked at `make load-gate PHASE=Px` (Phase 7e deliverable — scaffold + skill `load-gate-harness` land together). The graduation gate runs once per phase, not continuously; passing is a Phase 10 hard gate per PLAN.md Verification §41.

## Feature Scope

Full roadmap in [`PLAN.md`](PLAN.md) — 51 v1 + 10 v1.1 + 17 v2 features across 20 weeks (docs 17/18/24). v1 = SamplePlatform first 90 days + 5 Project Goals; polish → v1.1; product expansion → v2.

**Deliberate skips / Never:**
- **ClickHouse cluster at v1** — single-node is the rule; migrations Distributed-ready from day 1 per [`clickhouse-cluster-migration`](.claude/skills/clickhouse-cluster-migration/README.md).
- **Redis session cache** — breaks single-binary/air-gap; WAL + in-memory replaces it.
- **5-minute real-time** — rollup-hourly is the line; breaks cost model.
- **Bounce rate** — vanity metric (docs 09 / 14); expose time-on-page + funnel drop-off.
- **Multi-touch attribution** — last-touch channel grouping is final.
- **No remote agentic installer wizard** (doc 34 §A "Reject as default"). The `v1.1-install-cli` (deterministic framework detector + diff preview + round-trip verify, zero LLM, zero outbound) is the only accepted alternative. PostHog-style cloud-agent code edits corrupt `.env` files and mangle Astro frontmatter in public issues — not worth the ergonomic win on an air-gap product.
- **No cloud-mediated AI memory / `/init` persistent context** (doc 34 §B). Any future AI layer is local-model or pluggable-provider with air-gap default. F10 LLM-triage bookmark in PLAN.md stays conditional, not scheduled.
- **No broad MCP mutation surface** (doc 34 §"Decision matrix"). The roadmapped MCP server (v2) stays **read-only** over stdio or HTTP. Write operations go through the authenticated admin surface only.
- **No default-on autocapture** (doc 34 §E). The v1.1 Safe Autocapture Pack (`pack-safe.js`) is opt-in per tenant via `<script data-pack="safe">` / `statnive.enablePack('safe')`. Base tracker stays manual-first.
- **No session replay / DOM snapshot / canvas-WebGL-font fingerprinting** (doc 32 §A / doc 34 §E). Replaced by "frustration signals" inside the Safe Autocapture Pack — rage clicks, Web Vitals, `$pageleave`.

## Key Paths

- `cmd/statnive-live/main.go` — entry point
- `internal/ingest/` — HTTP handler, pipeline, WAL, consumer
- `internal/enrich/` — GeoIP, UA, channel, bot, bloom filter
- `internal/identity/` — BLAKE3 hash, salt rotation
- `internal/storage/` — ClickHouse client, queries, migrations
- `internal/dashboard/` — API endpoints (8 routes)
- `internal/auth/` — sessions, RBAC, audit
- `web/` — Preact SPA (embedded) · `tracker/` — JS tracker (<2KB)
- `clickhouse/` — schema SQL + migrations · `config/` — YAML + sources.yaml (50+ Iranian referrers) · `deploy/` — systemd, backup, iptables, docker-compose

## Testing

| Level | Tool | Location |
|---|---|---|
| Unit | Go testing | `*_test.go` alongside source |
| Integration (incl. security) | Go testing | `test/integration_test.go` |
| Load smoke | k6 | `test/perf/load.js` (`make load-test`, 7K EPS × 5min) |
| Frontend | Vitest | `web/src/**/*.test.tsx` |
| Graduation gate | Locust (primary) + k6 (cross-check) + Vegeta + wrk2 | `test/perf/gate/` + `test/perf/chaos/` + `test/perf/generator/` (doc 29 §4, Phase 7e) |

**ClickHouse-Oracle Assertion Hierarchy.** Always use the highest applicable tier; lower tiers are diagnostic, not acceptance evidence.

| Tier | Method | Use When |
|---|---|---|
| 1 | **ClickHouse-oracle** — correlation query against rollups | Ingest, rollup correctness, attribution, multi-tenant |
| 2 | **Network** — `httptest.Server` / route interception | Tracker transport, sendBeacon payload |
| 3 | **DOM / locator** — Playwright or Vitest RTL | Dashboard UI state |
| 4 | **Screenshot** — `only-on-failure` | Debug artifact only |

**For load-gate correlation (Phase 7e onward), `generator_seq` is the reference Tier-1 primitive:** every synthesized event carries `(test_run_id, generator_node_id, test_generator_seq, send_ts)`, and one ClickHouse query per run (loss / duplicates / ordering / latency) derives from it. Doc 29 §6.2 lists the canonical queries; projection `proj_oracle` makes them sub-second on 200M-row runs.

**Analytics Invariant Thresholds (release-blocking, CI-asserted on every v1/v1.1 RC):**

- Event loss server ≤ 0.05%, client ≤ 0.5%; Duplicates ≤ 0.1%
- Attribution correctness ≥ 99.5%; Consent / PII leaks = 0; TTFB overhead ≤ +10% / +25 ms

Thresholds apply to both per-PR CI runs and the per-phase graduation gate (doc 29 §4); any breach during a 72h soak or within the 6-scenario chaos matrix halts the gate and blocks the corresponding Phase 10 sub-phase cutover.

## Dev Tooling

Full inventory in [`docs/tooling.md`](docs/tooling.md): 4 original skill collections (cc-skills-golang, ClickHouse Agent Skills, trailofbits, marina-skill — doc 23), doc-25/27/28 additions (anthropics cherry-pick, JetBrains use-modern-go, OWASP, VibeSec, tm_skills, vercel-labs, obra/superpowers subset, knip, constant-time-analysis, grc-gdpr, legal-compliance-check), 14 project-local custom skills (see triggers below), and 4 MCP servers (Altinity ClickHouse, gopls, Hetzner, Grafana). `/jaan-to:*` handle *what* (specs/tests/reviews); community handle *how* (patterns); the 14 custom skills encode the 8 architecture rules + privacy + air-gap + Iranian-DC contract as CI-blocking guardrails.

**Do not install:** `anthropics/skills/web-artifacts-builder` (air-gap violation), `shajith003/awesome-claude-skills`, `sickn33/antigravity-awesome-skills`, `rohitg00/awesome-claude-code-toolkit` (low S/N). Ref: doc 25 §landscape.

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
| `test/perf/gate/**` / `test/perf/chaos/**` / `test/perf/generator/**` / `deploy/observability/**` (scheduled Phase 7e) | `load-gate-harness` (to scaffold in Phase 7e; advisory until Phase 10 P1 cutover — HARD GATE thereafter) |

### Anti-patterns (doc 28 §Anti-patterns) — absolute bans

Enforced by custom-skill Semgrep rules. Human-facing mirror so PR review can reject on sight:

- **No Cloudflare on any IR-resident code path** — OFAC 31 CFR 560.540(b)(3) + no IR POP. Enforced by `iran-no-cloudflare`.
- **No ACME / Let's Encrypt from inside Iran** — issue PEMs on outside-Iran `cert-forge`, rsync inward, SIGHUP swap. Enforced by `iran-no-letsencrypt-in-binary`.
- **No fsnotify for GeoIP reload** — overlayfs/NFS/kqueue lose events silently; SIGHUP only. Enforced by `geoip-no-fsnotify-on-bin`.
- **No `OPTIMIZE TABLE ... FINAL` without `PARTITION`** — serializes merges, OOMs 8c/32GB, non-idempotent on AggregatingMergeTree. Alternative: `OPTIMIZE ... PARTITION '...' FINAL DEDUPLICATE` off-peak or `min_age_to_force_merge_seconds=3600`. Enforced by `ch-ops-no-optimize-final-in-sql`.
- **No phone-home license check "even for telemetry"** — telemetry = "services rendered" under OFAC; `560.540(b)(3)` excludes. Offline Ed25519 JWT, zero `net.Dial`. Enforced by `iran-license-verify-must-be-offline`.
- **No AGPL linked into the binary; CC-BY-SA only via the § License Rules data-file carve-out** — OS daemons (chrony, acme.sh, knot, bind) are operator-installed, outside binary boundary. GeoIP BIN qualifies as non-linked data.
- **`{{if .Cluster}}` is DDL templating only, NOT cluster-upgrade automation** — data migration from MergeTree → ReplicatedMergeTree is manual via hard-link `ATTACH PARTITION`. See [`clickhouse-upgrade-playbook`](.claude/skills/clickhouse-upgrade-playbook/README.md).
- **Never ArvanCloud** — sanctioned + 2022 breach. Asiatech primary, ParsPack / Shatel backup.
- **Never default-enable exception-telemetry as a tracker event type** — doc 30 observed a reference-platform at 1.2B `app_exception` events over 192 days (73/sec sustained; 1-per-1.5-sessions). statnive-live's tracker ships any exception-telemetry as opt-in with 10/session cap + 1-in-10 sampling + server-side tenant quota. Otherwise the design-ceiling event budget gets consumed by crash-log noise rather than user behavior.

## Single Source of Truth

`../statnive-workflow/jaan-to/docs/research/` (docs 14–30) is canonical for every architecture, feature, and threat-model decision. Do **not** restate research conclusions here or in skill prompts — reference by doc number and section. When a decision changes, update the research doc; this file references and never duplicates.

## Enforcement

Integration tests that pin the invariants in this file — full 6-test matrix + assertions in [`docs/rules/enforcement-tests.md`](docs/rules/enforcement-tests.md). Phase 0 / Phase 7 deliverables. `/simplify` and PR review reject regressions against this list on day one.

## Research Documents

Canonical source: `../statnive-workflow/jaan-to/docs/research/` (docs 14–30, 500+ sources).

- **Doc 23** — initial Claude Code tooling (30 skills + 4 MCP servers).
- **Doc 24** — AGPL-safe Pirsch extraction (pre-pipeline fast-reject, cross-day grace, cheap-first bot ordering, reject mutable-row engines, `DateTime` not `DateTime64`, templated DDL, 17-step channel tree, `Filter → Store → queryBuilder`, `whereTimeAndTenant`). Zero Pirsch code ported.
- **Doc 25** — Claude-skills install matrix + custom-skill catalog + explicit blacklist.
- **Doc 27** — three-gap closure: WAL durability (fsyncgate, ack-after-fsync), CGNAT rate limit (Iranian ASN, iptoasn.com), GDPR-on-HLL (Recital 26 + C-413/23).
- **Doc 28** — final-three-gap closure: GeoIP pipeline / Iranian DC deploy / ClickHouse ops + upgrade playbook.
- **Doc 29** — production load-simulation gate on Asiatech Tehran: generator_seq oracle, 6-scenario chaos matrix (BGP cut / mobile curfew / DPI RST / Tehran-IX degrade / Asiatech DC outage / clock skew), 5-phase pre-cutover graduation matrix, Locust (primary) + k6 (CI cross-check) + Vegeta + wrk2 tool stack, Prometheus + Grafana + Pyroscope + Vector.dev + Parca + Falco observability stack, ≤40% cost envelope, SamplePlatform anonymized-replay protocol.
- **Doc 30** — GA4 calibration delta (2026-04-20): 192-day current-state observation of SamplePlatform traffic, used as **load-shape realism overlay** for doc 29's graduation gate. Adds: scenario G (international-egress) chaos matrix extension, long-session memory-leak soak (1000 VUs × 6h × 1080 @ 20s pings), diaspora-cohort load mix + SLO segmentation (62% Iran / 38% non-Iran), bimodal events-per-session (15% iPhone short / 40% Android short / 30% Android binge / 15% mobile-web power), Chrome/Safari/Samsung P0 tracker-compat matrix, `app_exception` default-enable anti-pattern. **Does NOT override doc 29 design ceiling** — 200M events/day / 40K EPS burst / 32c/128GB P5 cluster / 17–37% cost envelope all stand per PLAN.md Context "design ceiling vs observed current-state" callout. Doc 30's proposed P5 downsize to 16c/64GB / 80M events was rejected per user directive "design for maximum."
