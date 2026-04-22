# statnive-live — Self-Hosted & SaaS Analytics Platform

## Context

Sixteen research documents (docs 14–29), 500+ sources, 2,000+ lines of drop-in Go code. Architecture, features, schema, security, and Iranian-DC operational decisions are finalized. Docs 24 (AGPL-safe Pirsch extraction), 25 (skill install matrix), 27 (three-gap closure — WAL / CGNAT / GDPR-on-HLL), 28 (final-three-gap closure — GeoIP pipeline / Iranian DC deploy / ClickHouse ops), 29 (production load-simulation gate — 5-phase graduation matrix + generator_seq oracle + 6-scenario chaos) drive the Week 17+ schedule.

**statnive-live** is the standalone analytics platform (separate from the WordPress plugin "statnive"). Targets Iranian high-traffic sites; SamplePlatform is first customer.

**Reference streaming workload (StreamCo, confirmed 2026-04-19).** Two endpoints frame capacity; we ship the minimum first and ramp app-by-app.

| Envelope | Scope | Peak day events | Monthly events | Bandwidth/mo | Disk/year | EPS peak |
|---|---|---|---|---|---|---|
| **MINIMUM — P1 cutover** | Web, required events only | 3M | 75M | ~22 GB | ~36 GB | ~140 |
| **MAXIMUM — P5 full build** | All apps (web + iOS + Android + TV) | 200M | 4B | ~1.2 TB | ~1.9 TB | ~9,000 (spike ~18K) |

Minimum fits the cheapest Asiatech VPS (~15–28M Rial/mo). Maximum requires a 2–3 node Iranian-DC cluster (~800M–1.5B Rial/mo). 5-phase roadmap P1→P5 in [`../jaan-to/outputs/capacity-planning-standalone-analytics.md`](../jaan-to/outputs/capacity-planning-standalone-analytics.md).

> **Design ceiling vs. observed current-state (doc 30 reconciliation, 2026-04-21).** The MIN/MAX envelope above is the **design target** — statnive-live's load-gate acceptance (Phase 7e graduation gate per doc 29 §4) runs against MAX, not current. Doc 30 (GA4 calibration, 2026-04-20) measured SamplePlatform's observed current-state at ~80M events/day peak / ~8K EPS burst / 62% Iran + 38% diaspora over a 192-day window. We **retain doc 29's 200M / 40K / 32c-128GB P5 numbers** because (a) SamplePlatform's organic growth (Dec 2025 new-user +60% spike, doc 30 §5) will exceed 80M within 12–18 months at streaming-industry norms; (b) 192-day daily aggregates smooth away match-day + Ramadan iftar sub-daily spikes that doc 29's `match_spike()` (2.5–4×) and `ramadan_diurnal()` (1.8–2.2×) formulas correctly model; (c) ~200M Rial/mo P5 steady-state savings from right-sizing are small against the catastrophic cost of an EPS overrun during a Tehran-derby Friday evening. Doc 30's value is **load-shape realism** (bimodal sessions, 62/38 geographic split, `user_engagement` + `ui_interact` event mix, 7th chaos scenario, long-session soak, `app_exception` anti-pattern), **not capacity re-planning**. The proposed P5 downsize to 16c/64GB was rejected per user directive "design for maximum."

- **Repo:** https://github.com/statnive/statnive.live.git
- **Folder:** `statnive-live/`
- **Domain:** statnive.live

## Product Definition

**statnive-live** = Go single binary + ClickHouse analytics platform.

**Decisions locked:**
- **Greenfield build** — 100% original code. Study Pirsch fork at `~/Projects/pirsch/` for patterns; never copy AGPL source.
- **License: ALL deps MIT/Apache/BSD** — no AGPL. Sold as SaaS outside Iran where AGPL §13 applies.
- **Multi-tenant from v1** — `site_id` in schema from day 1. SamplePlatform = site_id=1.
- **Dual hosting** — Hetzner (€46/mo) dev/staging, Iranian DC (~€180/mo) SamplePlatform production.

Two distribution models from day 1 — same binary, multi-tenant via `site_id` + `WHERE site_id = ?`:

| Model | Description | Revenue |
|-------|-------------|---------|
| **Self-hosted** | Customer runs statnive-live on their own server | License fee; manual activation |
| **SaaS (managed)** | We host on Hetzner (outside Iran only) | Monthly subscription by pageviews |

## Repository Structure

Top-level tree:

```
statnive-live/
├── CLAUDE.md           # Project rules
├── .claude/skills/     # 14 custom + 49 community skills
├── cmd/statnive-live/  # Entry point
├── internal/           # config, audit, cert, ratelimit, ingest, enrich, identity, storage, sites, health, cache, dashboard, auth
├── web/                # Preact SPA (Phase 5)
├── tracker/            # <2KB IIFE tracker (Phase 4)
├── config/             # YAML defaults + sources.yaml (60+ Iranian referrers)
├── deploy/             # systemd, iptables, backup, airgap scripts
├── vendor/             # Vendored Go deps (offline builds)
├── docs/               # rules/, history/, tech-docs/, tooling.md, brand.md, cli-operator-surface.md, deployment.md, tech-docs-index.md, repo-structure.md
├── test/               # integration, enrichment, multitenant, security, dashboard tests
└── Makefile            # build, test, lint, audit, airgap-bundle
```

Full tree with `[shipped]`/`[planned]`/`[scaffolded]` per-file markers in [`docs/repo-structure.md`](docs/repo-structure.md).

## Development Phases

### Status — 2026-04-19

| Phase | Status | Notes |
|---|---|---|
| **0 — Project setup** | ✅ Complete | PR #1. Repo, Makefile, CI, schema, vendoring live. |
| **1 — Ingestion pipeline** | ✅ Complete | PR #2. Real 6-stage enrichment, BLAKE3 + IRST salt, 18 MB bloom + cross-day grace, 17-step channel tree, 503 back-pressure. Burst guard PR #14. |
| **2a — Surface hardening** | ✅ Complete | PR #6. TLS 1.3 manual PEM + SIGHUP reload + expiry watcher; httprate rate limit (NAT-aware, audit-instrumented); JSONL audit file sink; FastRejectMiddleware. |
| **2b — Auth + RBAC** | ⏳ Pending | bcrypt + crypto/rand sessions + SameSite=Lax + admin/viewer/api. Auth-return nil-guard lint (F5, Verification §53) ships as build prerequisite — any `(*User, error)` / `(*Session, error)` call site requires `user != nil` check after `err == nil`. Parallel with Phase 3b. |
| **2c — Operational hardening** | ⏳ Pending | systemd unit, iptables, LUKS docs, backup script. Pairs with Phase 8. |
| **3a — Dashboard query foundation** | ✅ Complete | PR #9. Filter + Store + 6 v1 queries + LRU + tenancy-grep gate. Geo/Devices/Funnel return ErrNotImplemented. |
| **3b — Dashboard HTTP layer** | ✅ Complete | PR #12. 8 stat handlers + realtime + IRST Filter + bearer-token stub + WITH FILL. |
| **3c — Admin CRUD** | ⏳ Pending | `/api/admin/users`, `/api/admin/goals`. Every write handler uses `dec.DisallowUnknownFields()` + per-endpoint `AllowedFields` whitelist; sensitive fields (`site_id`, `role`, `is_admin`) sourced from session context, never request body (F4, Verification §52 — Go-adapted mass-assignment guard). Needs Phase 2b. |
| **4 — Tracker JS** | ✅ Complete | PR #21. 1394 B min / 687 B gz + Go embed at `/tracker.js`; `statnive.track()` + `statnive.identify(uid)` end-to-end (raw uid cleared). Sec-GPC + DNT + webdriver + _phantom short-circuits BEFORE send. 15 Vitest + 6 Go handler tests; size gate in `make audit`. |
| **5 — Dashboard frontend** | 🟡 Partial | Phase 5a ✅ (PR #29): Preact + signals + TypeScript scaffold, brand tokens CSS, bundle gate ≤10 KB JS gz / ≤3 KB CSS gz (current 9 KB / 784 B), CSP / nosniff / Referrer-Policy on `/app/*`, `make build → make web-build` dep so `//go:embed all:dist/*` always has fresh assets, Overview panel with 2-tier KPIs (Visitors / Conversion% / Revenue / RPV primary; Pageviews / Goals demoted per "Reject vanity metrics"), bearer token injected via `<meta>` at Handler() construction, `cfg.Dashboard.SPAEnabled` defaults false (production gate-off until Phase 2b), dashboard-vitest CI job + `brand-grep` + `web-airgap-grep` Makefile gates. Phase 5b (Sources / Pages / SEO / Campaigns / Realtime panels + uPlot + filter + date picker) next. |
| **5a-smoke — End-to-end boot harness** | ✅ Complete | `test/smoke/harness.sh` + `make smoke` + `smoke-test` CI job drive the real `cmd/statnive-live/main.go` binary against docker-compose ClickHouse and probe every prod surface: `/healthz`, `/tracker.js`, `/app/` (shell + CSP + nosniff + Referrer-Policy + bearer injection), `/app/assets/*.js` (hashed bundle + long-cache), `POST /api/event` (×10 with CH count-back), `GET /api/stats/overview` (401 without bearer; 200 + 5 KPI keys with). Exercises `rateLimitMW` + `BackpressureMiddleware` + `dashboardAuthMW` from the prod router graph on every PR. Canonical pre-cutover verification for Phase 10 (see `docs/runbook.md` § Pre-cutover verification). |
| **6 — Config & first-run** | 🟡 Partial | YAML loader, migrations, `/healthz`, env override done. Admin-user + Goal/Funnel CRUD wait on Phase 2/3. |
| **7a — Backend solidity gate** | ✅ Complete | PR #14. Burst guard (~50 ns/op) + bench suite + crash/CH-outage/disk-full tests + k6 7K EPS + WAL replay + viper env fix. |
| **7c — Optimization & hardening** | ✅ Complete | PR #18. Channel hot path -13% (1 alloc/op); modern Go (`wg.Go`, range-over-int, `b.Loop()`); dead drift-check removed; CI fixes (vendor-check CRLF, license-check GOFLAGS, golangci-lint v2.5 `--new-from-rev`). Audit evidence at `audit/{sec,ch,airgap}-findings.md`; `bench.out` baseline. |
| **7b1a — WAL group-commit foundation** | ✅ Complete | PR #23. `internal/ingest/walgroup.go` GroupSyncer: ack-after-fsync, 256-event batch / 100ms timer, Sync errors terminate via injectable `exitFn` (fsyncgate). |
| **7b1b — WAL integration + perf-test** | ✅ Complete | PR #25. Pipeline synchronous; handler calls `Pipeline.Enrich` + `GroupSyncer.AppendAndWait` before 202. Consumer acks only after CH commit, 100/500/2000ms backoff, 30s drain. `BackpressureMiddleware` → 503 + `Retry-After: 5` at `wal_fill_ratio ≥ 0.80`. WAL replay emits `wal_replay` / `wal_replay_done` + `EventWALCorruptSkipped`. Same-filesystem boot check; LastIndex monotonic guard. `wal-durability-review` body: 4 Semgrep rules + 8 fixtures + 50-iter kill-9 harness. 0.05% loss SLO tightened. Closes items #1,#3,#4,#8,#9,#10 (#2,#5 in PR #23). |
| **7b2 — Real-traffic verification + drills** | ✅ Complete | PR #27. Shipped 5 of 6 deliverables fully + 1 partial: ✅ tracker payload-golden contract (`tracker/test/payload-golden.test.mjs` + `test/tracker_correctness_test.go`); ✅ integration-level PII grep (`test/pii_leak_test.go`); ✅ manual TLS rotation drill (`internal/cert/rotation_e2e_test.go`); ✅ `wal_fsync_p99_ms` on `/healthz` (closes wal-durability-review item #7 — all 10/10 now green); ✅ `make wal-killtest` 5-iter smoke + nightly 50-iter (`wal-killtest-smoke` job + `wal-killtest-nightly.yml`); 🟡 backup-restore drill — manual SOP only (`docs/runbook.md` § Backup & restore); CI automation defers to Phase 2c (needs clickhouse-backup binary + S3 sink). |
| **7b2-completion — execute the 7b2 integration tests in CI** | ✅ Complete | PR #28. New CI jobs: `integration-tests` (docker-compose CH + `make test-integration`) + `tracker-vitest` (Node 20 + `npm --prefix tracker test`). All 5 CI jobs green: build-test-lint, licenses, wal-killtest-smoke, integration-tests, tracker-vitest. Also fixed 2 latent Phase 7b2 bugs the wiring exposed: schema column `user_id` → `user_id_hash` in `pii_leak_test.go` + `tracker_correctness_test.go`; missing `HandlerConfig.MasterSecret` in `newTestPipelineServer`. Establishes the "shipped must be executed in CI" rule going forward. |
| **7d — Lint baseline cleanup** | ⏳ Pending | ~40 pre-existing findings on main (errcheck, gosec G302/G304, gofmt, intrange/goconst, gocyclo). Install + baselines: govulncheck, CodeQL+Semgrep, go-licenses. Adds four new static gates: (a) `slog-no-raw-pii` Semgrep rule (F3 — complements Phase 7e Vector.dev live wire-scan at merge time); (b) skill-content sanitizer (F6 — Unicode Tag Block / zero-width / bidi / HTML-comment scan across all `.claude/skills/**/*.md`); (c) Go 1.24 floor commit in `go.mod` + migrate config/license/PEM/GeoIP-BIN loaders to `os.Root.Open` (F7 — TOCTOU-safe file I/O per Go 1.24 `openat2` wrapper); (d) SARIF fingerprint baselines + grace periods for existing findings (post-v1 refinement, deferred). |
| **7e — Load-simulation gate scaffolding** | ⏳ Pending | `test/perf/gate/` Locust harness + k6 CI cross-check + Vegeta/wrk2 breakpoint, 7-scenario chaos matrix (doc 29 §5 + doc 30 §3 international-egress), observability VPS, generator_seq schema migration 003, long-session memory-leak soak (doc 30 §6). Doc 29 §8 W1–W5. HARD GATE on Phase 10 P1 cutover. |
| **8 — Deployment & launch** | ⏳ Pending | Hetzner CX32 staging, airgap-bundle, monitoring, runbook, v1 launch. |
| **9 — Phase A dogfood** | ⏳ Pending | statnive.com → demo.statnive.live. |
| **10 — Phase B SamplePlatform** | ⏳ Pending | Iranian DC bare metal, paid DB23 GeoIP. |
| **11 — Phase C SaaS** | ⏳ Pending | Polar.sh checkout + webhooks, signup, path-based tenancy. |
| **CLI** (operator surface) | 🔮 v1.1 | Subcommands: serve, migrate, license, sites, users, backup, doctor, secret, stats. Details in [`docs/cli-operator-surface.md`](docs/cli-operator-surface.md). |
| **MCP server** (agent surface) | 🔮 v2 | Read-only analytics tools over stdio (air-gap-safe) or HTTP. See [`docs/cli-operator-surface.md § MCP`](docs/cli-operator-surface.md). |
| **Brand & design tokens** | 📐 Reference ready | Wordmark + summit logo, cream/ink/Persian-Teal palette, Fraunces + IBM Plex ramp. Full spec in [`docs/brand.md`](docs/brand.md). |

### Phase 0: Project Setup (Week 1)

**Guardrail:** [`air-gap-validator`](.claude/skills/air-gap-validator/README.md).

- [x] Repo, Go module, Makefile (build/test/lint/release/`airgap-bundle`), CI (SHA-pinned actions)
- [x] ClickHouse schema SQL (`events_raw` + 3 v1 rollups)
- [x] Copy Go files from doc 22; vendor all deps
- [x] `config/sources.yaml` (60 entries); `config/statnive-live.yaml`

### Phase 1: Ingestion Pipeline (Weeks 2–4)

**Guardrails:** [`tenancy-choke-point-enforcer`](.claude/skills/tenancy-choke-point-enforcer/README.md), [`blake3-hmac-identity-review`](.claude/skills/blake3-hmac-identity-review/README.md), [`clickhouse-rollup-correctness`](.claude/skills/clickhouse-rollup-correctness/README.md), [`clickhouse-cluster-migration`](.claude/skills/clickhouse-cluster-migration/README.md), [`wal-durability-review`](.claude/skills/wal-durability-review/README.md).

- [x] Wire main.go; SiteID on EnrichedEvent
- [x] `ingest/handler.go` — JSON array parsing; pre-pipeline fast-reject (prefetch headers, UA shape, IP-as-UA, UUID-as-UA, non-ASCII → 204). Parse `True-Client-IP` + `CF-Connecting-IP` alongside XFF rightmost.
- [x] `ingest/pipeline.go` — 6-worker enrichment; order locked: identity → burst → bloom → geo → ua → bot → channel. Cheap-first bot detection. Burst guard in PR #14. v1.1 owes referrer-spam + browser-version-floor + datacenter-CIDR.
- [x] `ingest/consumer.go` — dual-trigger batch writer (1000 rows / 500ms / 10MB). Single 250ms retry; DLQ deferred.
- [x] `ingest/wal.go` — 100ms fsync + 10GB cap; `/healthz` `wal_fill_ratio`.
- [x] `storage/clickhouse.go` — 34-column batch insert + site_id; `DateTime('UTC')` not `DateTime64(3)`.
- [x] `storage/migrate.go` — numbered migrations, `schema_migrations(version, dirty, sequence)`, `{{if .Cluster}}` templates from day 1. Advisory locks deferred.
- [x] `enrich/` — GeoIP (LITE DB23 no-op fallback), medama-io UA, 17-step channel tree, isbot + `crawler-user-agents.json`, 18MB bloom. Hostnames via `map[string]struct{}` (~100× speedup).
- [x] `identity/` — BLAKE3-128 + `HMAC(master_secret, site_id || YYYY-MM-DD IRST)`. Cross-day fingerprint grace lookup.
- [ ] k6 load test (Phase 7): P1 ~300 / P2 ~1,000 / P3 ~4,000 / P4 ~9,000 (18K match) / P5 ~40,000 peak EPS per doc 29 §4 — the design-target graduation-gate sign-off numbers. Doc 30 §4 observed current-state is ~4× lower (P5 ~8K sustained, 12K burst) and is retained as a **realism overlay for load-shape curve fitting only**, not a target override (see Context "design ceiling vs. observed"). Smoke only in CI; per-phase graduation gate runs in Phase 7e against the design target.
- [ ] Crash recovery test (Phase 7): kill-9 → WAL replay → zero loss.
- [x] Integration tests: bot UA → is_bot=1 + visitor_hash (`test/enrichment_e2e_test.go`); 10-case fast-reject table; cross-day grace.

### Phase 2: Security Layer (Weeks 5–6)

**Guardrail:** [`ratelimit-tuning-review`](.claude/skills/ratelimit-tuning-review/README.md) — regression guard for Phase 2a rate-limiter. ASN tiering + `iptoasn.com` is Phase 10 HARD GATE.

- [x] TLS manual PEM loader + SIGHUP reload (`internal/cert/`, atomic.Pointer hot-reload, fail-closed, keep-old-on-fail, expiry watcher <30d/<7d).
- [ ] Dashboard auth — Phase 2b.
- [x] Rate limiting (`httprate.LimitByRealIP`, 100 req/s, NAT-aware via `ingest.ClientIP` ladder; emits `audit.ratelimit.exceeded`).
- [x] Input validation (`MaxBytesReader` 8KB, ±1h drift) — shipped in Phase 1.
- [x] Hostname validation (HMAC skipped per doc 20); emits `audit.ingest.hostname_unknown`.
- [x] Audit log (`internal/audit/`, `O_APPEND`, `Reopen()` for logrotate, typed `EventName`).
- [ ] systemd hardening, iptables, LUKS docs, backup script — Phase 2c.
- [x] Security assertions in `test/integration_test.go` + `test/security_test.go`.

### Phase 3: Dashboard API (Weeks 7–9)

**Guardrail:** [`tenancy-choke-point-enforcer`](.claude/skills/tenancy-choke-point-enforcer/README.md) — pairs with existing `make lint` `tenancy-grep`.

Flat `internal/storage/queries.go` (one Go function per endpoint) — we do NOT mirror Pirsch's 10 sub-analyzer split (doc 24 §Sec 4 pattern 1).

- [x] `storage/store.go` — typed Store interface; one method per endpoint; mockable for Phase 7.
- [x] `storage/queries.go` — central `whereTimeAndTenant(*Filter, col)` helper; CI `tenancy-grep` rejects any `SELECT` skipping it.
- [x] `storage/filter.go` — Filter struct (SiteID, From/To, Path, Referrer, UTM, Country, Browser, OS, Device, Sort, Search). Deterministic BLAKE3 `Hash()` as cache key.
- [x] 8 `GET /api/stats/*` handlers (overview/sources/pages/geo/devices/funnel/campaigns/seo). Geo/Devices/Funnel return 501 until v1.1/v2.
- [x] `WITH FILL … STEP INTERVAL` for SEO daily series. Visitors-trend deferred.
- [ ] Admin CRUD — Phase 3c (needs Phase 2b).
- [x] `GET /api/realtime/visitors` (10s cache).
- [x] Half-open `[from, to)` intervals at day granularity; Asia/Tehran conversion at API layer.
- [x] LRU cache (realtime=10s, today=60s, yesterday=1h, historical=∞) — `internal/cache/{lru,policy}.go`; per-entry TTL via `expiresAt`.
- [ ] Funnel via `windowFunnel()` + 1h cache — v2.
- [ ] Dashboard query benchmark under 7K EPS — Phase 7.
- [x] Multi-tenant integration test (`test/dashboard_isolation_test.go`).

### Phase 4: Tracker JS (Week 10)

**Guardrails:** [`preact-signals-bundle-budget`](.claude/skills/preact-signals-bundle-budget/README.md) (1.2KB min / 600B gz tracker budget + CDN ban); [`air-gap-validator`](.claude/skills/air-gap-validator/README.md).

- [x] Build from doc 20 — 1394 B min / 687 B gz; Rollup + Terser passes=3 + mangle.toplevel; output to `internal/tracker/dist/`.
- [x] Pageview + SPA (history API) + custom events (`statnive.track(name, props, value)`) + `statnive.identify(uid)` (raw cleared in handler, hashed via `identity.HexUserIDHash`).
- [x] Client bot hints: `navigator.webdriver`, `_phantom`, `callPhantom` short-circuit before send.
- [x] **Sec-GPC + DNT short-circuit BEFORE the request fires** (doc 27 Privacy Rule #9).
- [x] Served via `go:embed` (`internal/tracker/tracker.go`).
- [x] Cross-day returning visitors validated via `_statnive` cookie round-trip.
- [x] Real-tracker integration test → rollup verification — Phase 7b2 shipped the payload-golden contract (Vitest captures sendBeacon body in `tracker/test/payload-golden.test.mjs`; Go integration test replays each payload through the full pipeline → ClickHouse in `test/tracker_correctness_test.go`). Phase 7b2-completion (PR #28) wires both into CI — `tracker-vitest` job runs the Vitest; `integration-tests` job runs the Go replay against docker-compose ClickHouse.

**Deferred to v1.1:** engagement ping (10s heartbeat), throttle-with-last-event, base36 date, envelope+payload separation.

### Phase 5: Dashboard Frontend (Weeks 11–13)

**Guardrails:** [`preact-signals-bundle-budget`](.claude/skills/preact-signals-bundle-budget/README.md) (50KB min / 15KB gz); plus `vercel-labs/web-design-guidelines` + `knip-unused-code-dependency-finder`.

Brand tokens from [`docs/brand.md`](docs/brand.md) — `web/src/tokens.css` imports at SPA entry; hand-rolled hex in components is a PR-review reject.

- [x] Preact SPA scaffold (Vite + TypeScript + @preact/signals) — Phase 5a / PR #29.
- [partial] Overview panel done (Phase 5a); Sources / Pages / SEO / Campaigns / Realtime panels + uPlot Visitors trend pending Phase 5b; Funnel (Frappe) / Geo / Devices pending v1.1 or v2 per rollup availability.
- [ ] Gregorian date picker (Jalali = v1.1); real-time widget (10s refresh); Admin pages (users/goals/funnels) — Phase 5b (non-admin) + Phase 2b+3c (admin pages).
- [ ] WCAG 2.2 AA (contrast, focus rings, aria, keyboard) — Phase 5c.
- [x] Embed via go:embed — Phase 5a. Binary-size verification deferred until Phase 5b panels land.

**Deferred to v1.1:** Jalali, comparison-period toggle, CSV export, command palette.

### Phase 6: Configuration & First-Run (Week 15)

- [x] YAML config loader (viper, hot-reload goals/funnels via SIGHUP; goals/funnels CRUD in Phase 3)
- [ ] First-run: admin user creation (awaits Phase 2 auth); Goal/Funnel CRUD (YAML)
- [x] Schema migration runner; `/healthz`

### Phase 7: Testing & Hardening (Week 16)

**Guardrails:** [`air-gap-validator`](.claude/skills/air-gap-validator/README.md) (release-gate under `iptables -P OUTPUT DROP`); [`wal-durability-review`](.claude/skills/wal-durability-review/README.md) — **hard gate on 7b close** (kill-9 CI 50 runs/PR).

- [x] k6 smoke (`test/perf/load.js`, PR #14, Persian paths + Iranian UAs, `make load-test`)
- [x] Go bench suite (`internal/{ingest,enrich}/bench_test.go`, PR #14)
- [ ] 100K-event integration (Phase 7b, after auth)
- [x] Crash recovery test (logs ~80% loss before 7b WAL fix — now asserts <0.05%)
- [x] Disk-full policy test; Phase 7c optimization (PR #18 — Channel -13%, modern Go, CI fixes, `make audit`)
- [partial] Backup restore — Phase 7b2 ships the manual SOP at [`docs/runbook.md`](docs/runbook.md) § Backup & restore; CI automation (`deploy/backup/drill.sh` + dedicated job) lands in Phase 2c.
- [x] Manual TLS rotation test — Phase 7b2 ([`internal/cert/rotation_e2e_test.go`](internal/cert/rotation_e2e_test.go) — atomic.Pointer hot-swap regression + fail-closed on corrupt PEM).
- [x] CH outage buffer-and-drain test (10s in-test; 10min in runbook)
- [x] Integration-level PII grep — Phase 7b2 shipped [`test/pii_leak_test.go`](test/pii_leak_test.go) (byte-scans WAL segments + audit.jsonl + `events_raw` for raw user_id/IP probes; pins Privacy Rules 1 + 4); Phase 7b2-completion (PR #28) executes it per PR via the new `integration-tests` job. Latent `user_id` vs `user_id_hash` column drift fixed in the same PR.
- [x] WAL fsync p99 surfaced via `/healthz` — Phase 7b2 closes [`wal-durability-review`](.claude/skills/wal-durability-review/README.md) item 7 (last open of 10).
- [x] Kill-9 WAL CI gate — Phase 7b1b shipped harness; Phase 7b2 wires `make wal-killtest` 5-iter smoke into per-PR CI + nightly 50-iter on main.
- [ ] **Generator_seq oracle schema** (doc 29 §6.1) — migration `003_load_gate_columns.sql` adds `test_run_id` (UUID), `test_generator_seq` (UInt64), `generator_node_id` (UInt16), `send_ts` (DateTime64(3)) to `events_raw` with typed `DEFAULT` sentinels (not Nullable — Rule 5 carve-out in CLAUDE.md); projection `proj_oracle` for sub-second per-run aggregations. Phase 7e prerequisite — scaffolded alongside Locust harness.
- [ ] **PII wire-scan migration to Vector.dev + VRL** (doc 29 §3.4, §6.3) — supersedes one-shot [`test/pii_leak_test.go`](test/pii_leak_test.go) with live <1s detection at 15K+ EPS via VRL regex (ipv4/ipv6/email/user_id). Halts graduation gate on `rate() > 0`. Phase 7e deliverable.

### Phase 7e: Load-simulation gate scaffolding (Weeks 17–20, overlaps Phase 8)

**Guardrail (scheduled):** `load-gate-harness` skill — triggers on `test/perf/gate/**`, `test/perf/chaos/**`, `test/perf/generator/**`, `deploy/observability/**`. Advisory during scaffolding; HARD GATE on Phase 10 P1 cutover.

Canonical spec: [`../jaan-to/docs/research/29-Production-load-simulation-gate-statnive-live-asiatech-tehran.md`](../jaan-to/docs/research/29-Production-load-simulation-gate-statnive-live-asiatech-tehran.md). Schedule maps to doc 29 §8 W1–W5.

- [ ] **W1–W2** — ClickHouse migration `003_load_gate_columns.sql` applied on staging; verify sparse-column storage overhead ≈ zero on 100M-row synthetic; projection `proj_oracle` MATERIALIZEd.
- [ ] **W3** — Stand up Locust master + 3 FastHttpUser workers on Asiatech (`test/perf/gate/locust-master.py`, `locustfile.py`, worker manifests). Replicate existing k6 scenarios (`test/perf/load.js`) into Locust Python; cross-check p99 within 5% of k6.
- [ ] **W4** — Observability VPS on separated AT-VPS (rack/AZ distinct from generators + target): Prometheus + Grafana + Grafana Pyroscope (AGPL-3.0 server, Apache-2.0 SDK) + Loki + Vector.dev + Parca + Falco. All container images mirrored to internal registry. `strace -f -e trace=connect` burn-in under `iptables -A OUTPUT -j DROP` except observability VLAN confirms no outbound.
- [ ] **W5** — Seven chaos scripts (`test/perf/chaos/A_bgp_cut.sh` … `F_clock_skew.sh` + `G_intl_egress.sh`) per doc 29 §5.1–§5.6 + doc 30 §3 — Ansible playbooks or bash. Scenario G: 3h `tc netem` injecting 80–120ms jitter + 2% loss on outbound Tehran-IX / Asiatech → Frankfurt peering while NIN domestic paths stay clean; pins the 38% non-Iran diaspora cohort (19% Germany / 9% US / 7.5% Finland-VPN / 2.8% UK / 2.7% FR / 2.5% CA) per doc 30 §3. Dry-run each scenario on isolated 2-node test bed; capture oracle-SQL output for every scenario.
- [ ] **Makefile targets** — `make load-gate PHASE=Px` (runs 72h soak + 6-chaos + breakpoint), `make soak-72h`, `make chaos-matrix`, `make breakpoint`, `make oracle-scan`.
- [ ] **Synthesizer** (`test/perf/generator/main.go`) — Go program emitting generator_seq quadruple per event; supports replay from SamplePlatform anonymized NDJSON export + synthetic fill. Kernel tuning applied per doc 29 §3.2 sysctl table on every generator node.
- [ ] **Replay-attestation template** — `docs/replay-attestation-template.md`; SamplePlatform analytics owner signs a per-phase export statement (regex-scrub spec + salt rotation + auto-delete kill-switch).
- [ ] **Acceptance:** P1 dry-run on 2-node test bed passes all 6 chaos scenarios + 0→450 EPS breakpoint + oracle SQL returns zero loss/duplicates before Phase 10 Week 21 begins.

### Phase 8: Deployment & Launch (Weeks 17–18)

**Guardrails:** [`air-gap-validator`](.claude/skills/air-gap-validator/README.md), [`clickhouse-rollup-correctness`](.claude/skills/clickhouse-rollup-correctness/README.md), [`clickhouse-cluster-migration`](.claude/skills/clickhouse-cluster-migration/README.md). Plus `AgriciDaniel/claude-cybersecurity` one-shot audit.

- [ ] Hetzner CX32 (Phase A) OR Iranian DC (SamplePlatform)
- [ ] `make airgap-bundle` — binary + `vendor/` + migrations + default YAML + tracker + DB23 BIN + SHA256SUMS + Ed25519 signature. Docker tarball → v1.1.
- [ ] Deployment runbook (bare-metal, air-gapped bundle install)
- [ ] Backup cron + monthly restore drill
- [ ] File-sink alerts (`/var/log/statnive/alerts.jsonl`): WAL >80%, CH down, disk >85%, cert <30d. Syslog/Telegram = v1.1.
- [ ] Offline GeoIP update procedure (SCP BIN + SIGHUP); internal NTP requirement docs
- [ ] SamplePlatform tracker integration; air-gap acceptance test; v1 launch

### Phase 9: Dogfood on statnive.com (Weeks 19–20)

- [ ] Hetzner CX32 (~€13/mo) as D1 initial. Upgrade to AX42 at ~10 SaaS customers (Phase C).
- [ ] DNS A/AAAA for `statnive.live` + `demo.statnive.live`; manual PEM via certbot + cron+SIGHUP
- [ ] IP2Location LITE DB23 (free, attribution)
- [ ] Seed: `site_id=1, hostname='statnive.com'`; `demo/demo-statnive` viewer + internal admin
- [ ] Login page exposes demo creds + "Sign up for your own analytics" CTA
- [ ] Tracker snippet in `statnive-website/` Astro base layout
- [ ] Acceptance: 24h → non-zero visitors; viewer blocked from `/api/admin/*`; all 8 `/api/stats/*` return data

### Phase 10: SamplePlatform dedicated Iranian VPS (Weeks 21–24)

**HARD GATE on cutover:** [`ratelimit-tuning-review`](.claude/skills/ratelimit-tuning-review/README.md) — Iranian-ASN compound-key tiering before the first byte. AS44244 / AS197207 / AS57218 on `(ip, site_id)` at 1K req/s sustained / 2K burst; 100/200 fallback elsewhere; 25K req/s per-site global cap. ASN via `iptoasn.com` public-domain TSV (MaxMind / IPLocate CC-BY-SA rejected). k6 scenarios `normal`/`burst`/`ddos`/`cgnat` must pass.

**Onboarding app-by-app.** Full StreamCo-class (MAX: 5M DAU / 200M events/day) requires a cluster. Enter with **web only** (MIN: ~200K DAU / 3M views/day — 30× smaller), onboard iOS/Android/TV across months 1–12.

**DNS & shutdown-resilience (non-negotiable):** `statnive.live` authoritative DNS split outside-Iran primary (Bunny/ClouDNS) + Iran-hosted NSD on Asiatech VPS (AT-VPS-B1) via AXFR + NOTIFY. Parent zone lists both NS sets. Iranian resolvers reach Iranian NS over NIN during int'l shutdowns. Plus defensive `statnive.ir` at Pars.ir parked 301 → `statnive.live`. Replaces Cloudflare. Spec: [`../jaan-to/docs/research/26-iran-shutdown-dns-strategy.md`](../jaan-to/docs/research/26-iran-shutdown-dns-strategy.md). ~$14/mo + $15/yr.

Per-phase Iranian DC sizing:

| Sub-phase | Scope | Max DAU | Max MAU | Max daily events | Max monthly events | Asiatech server | Price/mo |
|---|---|---|---|---|---|---|---|
| **P1** [MIN] | Web views | 200K | 1.4M | 3M | 75M | `AT-VPS-G2` | 27.9M Rial |
| **P2** (+1mo) | +curated interactions | 200K | 1.4M | 15M | 350M | `AT-VPS-G2` | 27.9M Rial |
| **P3** (+3mo) | +iOS | ~1.45M | ~5.65M | 70M | 1.4B | `AT-VPS-A1` + BW ≥500 GB/mo | 63.5M + BW |
| **P4** (+6mo) | +Android | ~3.45M | ~12.45M | 140M | 3B | Dedicated 16–32c/64–128GB/2TB NVMe + ≥1 TB/mo | quote |
| **P5** (+10mo) [MAX] | +TV + HA | 5M | 17M | 200M | 4B | Cluster 2–3× (32c/128GB/4TB NVMe) + unmetered | quote |

- [ ] P1 cutover: Asiatech G1/G2 standard VPS (~15–28M Rial/mo, 150 GB/mo fits web)
- [ ] Negotiate P3+ quotes: Asiatech BW upgrades + dedicated from Asiatech/Shatel/Afranet/ParsPack
- [ ] D2 provisioning (VPS P1/P2 → dedicated P3)
- [ ] DNS: `CNAME SamplePlatform.statnive.live → <Iranian-DC-IP>` (Cloudflare proxy **OFF**)
- [ ] `make airgap-bundle` → SCP → verify SHA256 + Ed25519 sig → `deploy/airgap-install.sh` → `make smoke` (Phase 5a-smoke harness) for the pre-cutover readiness check against the freshly-installed binary
- [ ] Manual PEM (LE throwaway or SamplePlatform internal CA), quarterly rotation
- [ ] **Paid IP2Location DB23** on D2 only (city accuracy matters)
- [ ] Ed25519 license JWT (`site_id=1, Customer="SamplePlatform", MaxEventsDay=0, Features=["*"], ExpiresAt=+1y`). Private key age-encrypted on offline laptop.
- [ ] Config overrides: `audit.sink=file`, `license.phone_home=false`. Only `site_id=1`.
- [ ] Seed `sites` with SamplePlatform hostnames (`SamplePlatform.com`, `www.SamplePlatform.com`, CDN subdomains)
- [ ] Admin user → secure channel (Signal / in-person / PGP)
- [ ] SamplePlatform pastes `<script src="https://SamplePlatform.statnive.live/tracker.js" defer></script>` + root-domain cookie walking (Clarity pattern, doc 21)
- [ ] Acceptance per sub-phase (doc 29 §4): P1 72h soak @ 240 EPS + 7-scenario chaos (+ G international-egress per doc 30 §3) + 0→450 EPS breakpoint → binary SLO sign-off before SamplePlatform web cutover; P2/P3/P4/P5 repeat the gate at their respective envelopes (1K / 4K / 9K-18K match / 40K peak) before onboarding each app. PII wire-scan `rate()` = 0 throughout. Air-gap end-to-end + backup+restore remain prerequisite (§17 / §37).

### Phase 11: International SaaS self-serve (Weeks 26–30)

**HARD GATE on first signup:** [`gdpr-code-review`](.claude/skills/gdpr-code-review/README.md) + [`dsar-completeness-checker`](.claude/skills/dsar-completeness-checker/README.md) paired. 12-item privacy-by-design + sink-matrix integration test (`system.tables` enumerated dynamically). DPA draft at `docs/dpa-draft.md` with doc 27 §line 77-79 HLL-anonymous language. Weekly rollup rebuild cron (`robfig/cron`, Sunday 03:00 IRST) as bounded-time safety net.

- [ ] `POST /api/signup` (email + password + hostname → site + admin user)
- [ ] `POST /api/admin/billing` (Polar.sh webhook — verify `X-Polar-Signature` HMAC-SHA256; `subscription.{created,updated,canceled}` only; idempotent by event.id)
- [ ] **Mass-assignment guard on every write endpoint (F4, Verification §52).** `POST /api/signup`, `POST /api/admin/billing`, `POST /api/admin/users`, `POST /api/admin/goals`, and any future mutating handler uses (a) `json.NewDecoder(r.Body).DisallowUnknownFields()` to reject unknown keys, (b) per-endpoint `AllowedFields []string` whitelist enforced pre-unmarshal, (c) `site_id` / `role` / `is_admin` / `plan` sourced from session context (or the verified Polar webhook payload, for billing only) — never from request body. Laravel-style mass-assignment (`site_id=2, role=admin`) is how cross-tenant privilege escalation sneaks in. Go-adapted from jaan-to-plugin research doc 04 + doc 67 patterns.
- [ ] **Path-based tenant routing** in `dashboard/router.go` — `app.statnive.live/s/<slug>/...`; middleware extracts slug → `site_id` via `internal/sites/sites.go`
- [ ] `internal/sites/sites.go` — slug generation, uniqueness, blocklist, create/disable
- [ ] Signup guardrails: DNS-resolvable, not on blocklist, unique in `sites`, 5/hr/IP
- [ ] Free tier 10K PV/mo via `daily_users` (v1.1) or `count(DISTINCT visitor_hash)` over `hourly_visitors`; soft throttle + `quota_exceeded=1` tag + upsell banner
- [ ] Polar.sh (Merchant of Record): 4 Products × monthly+yearly; `POST /api/billing/checkout` → `POST api.polar.sh/v1/checkouts/` with `external_customer_id=site_id`. Customer Portal + Benefits = v2; v1 = email support
- [ ] Paid tiers unlock quota + goals/funnels CRUD (gate keyed by `sites.plan`)
- [ ] Onboarding at `/s/<slug>/onboarding` (copy-paste + user-triggered refresh)
- [ ] Email transactional (signup/receipt/quota) — opt-in via `email.enabled`
- [ ] Acceptance: fresh signup → tracker → first event <5min; cross-site isolation; sandbox `subscription.created` updates `sites.plan`; webhook idempotent; 6th signup/hr rejected

## License Management (Self-Hosted)

Not open-source. Self-hosted customers need a license.

**v1 (Manual):** JWT `{site_id, customer, expires, max_events_per_day, features[]}` signed Ed25519. Startup: decode → verify signature → check expiry. File `config/license.key`. No payment system yet. Unlicensed = demo mode (30-day trial, 10K events/day cap, dashboard watermark).

**v2 (Automated):** license server at `license.statnive.live`. Daily phone-home with 30-day offline grace (Iran connectivity fragile per doc 19). Payload strictly `{site_id, events_day_count, version}` — no PII. Polar.sh Merchant of Record.

**Key management (v1):** private key in age-encrypted file on offline laptop. Single keypair for all of v1; compromise = rotate + ship new binary. HSM + yearly rotation = v2 when volume justifies.

## v2 Roadmap (Post-Launch, +8–12 weeks)

| Feature | Effort | Priority | Lands |
|---------|--------|----------|-------|
| Sequential funnel (windowFunnel) | 2wk | High | v2 |
| Cohort / retention | 2wk | High | v2 |
| Filtering / drill-down | 2wk | High | v2 |
| Google Search Console | 2wk | High | v2 |
| Session tracking | 1wk | Med | v2 |
| Entry / exit pages | 1wk | Med | v2 |
| Engagement time / page gap | 1wk | Med | v2 |
| Telegram weekly reports | 1wk | Med | v2 |
| CSV export | 1wk | Med | v2 |
| Public REST API | 1wk | Low | v2 |
| **Operator CLI** | 1wk | Med | **v1.1** |
| **MCP server** | 2wk | High | **v2** |
| Microsoft Clarity integration | 1d | Future | post-v2 |
| **LLM-triage prompt-injection defense (F10 bookmark)** | — | Conditional | **If-then** |

> **F10 bookmark — if-then, not scheduled.** If a v1.1/v2 feature ever ships (a) an LLM-triaged error / crash-telemetry endpoint, (b) a GitHub-issue-bot that reads user-submitted issues, or (c) AI-assisted NL → ClickHouse query generation, adopt jaan-to-plugin research doc 77 §6–9: untrusted-input envelope (`<untrusted_input>` delimiter tags + explicit system-prompt override rules), CTQRS scoring (Completeness / Technical / Quality / Reproducibility / Severity), layered code-evidence validation, pre-integration sanitization (strip code blocks, credential patterns, shell metacharacters). Doc 30 anti-pattern already bans default-on `app_exception` telemetry, so (a) is unlikely in v1; (b) and (c) are post-v1. No skill, no test, no CI gate until a triggering feature enters scope. Revisit at v2 scoping.

## Skills & Tooling Surface

Authoritative inventory in [`docs/tooling.md`](docs/tooling.md) + the 14 `.claude/skills/*/README.md` specs. Research anchors: doc 23 (tooling landscape), doc 25 (install matrix + custom-skill catalog), doc 27 (three-gap closure: WAL / CGNAT / GDPR-on-HLL), doc 28 (GeoIP / Iranian-DC / CH-ops + upgrade playbook). Blacklist: `anthropics/skills/web-artifacts-builder` (air-gap violation), `shajith003/awesome-claude-skills`, `sickn33/antigravity-awesome-skills`, `rohitg00/awesome-claude-code-toolkit`.

## Brand & Design

Full spec — wordmark + summit logo, cream/ink/Persian-Teal palette, Fraunces + IBM Plex type ramp, token CSS, typography rules, voice rules, compliance hooks — in [`docs/brand.md`](docs/brand.md). Applies to statnive.live marketing + demo + tenant dashboards + SamplePlatform dashboard + README/docs.

## SaaS Model, Server Costs, Air-Gapped Deployment

**SaaS model (pricing tiers, multi-tenant architecture, GDPR requirements):** details in [`docs/deployment.md § SaaS Model`](docs/deployment.md#saas-model-statnive-live-cloud). Tiers: Free 10K PV ($0 self-hosted only), Starter 100K ($9/mo), Growth 1M ($19/mo), Business 10M ($69/mo), Scale 100M ($199/mo), Enterprise 1B+ (custom).

**Server costs:** Hetzner CX32 (~€13/mo) Phase A dogfood → AX41 (~€39/mo) first ~10 paying → AX42 (€46/mo) ~30–50 customers → AX102 (€104/mo) 100+. SamplePlatform Iranian DC ~€180/mo (quote-based; phase-dependent per [`deployment.md § Server Costs`](docs/deployment.md#server-costs)).

**Air-gapped / isolated deployment:** zero required outbound connections, single binary, all deps `go:embed` or vendored; opt-in external services (LE ACME, Telegram, license phone-home, GSC, Clarity, Polar, email) disabled by default. Full procedure (bundle contents, `make airgap-bundle`, install steps, prerequisites) in [`docs/deployment.md § Air-Gapped / Isolated Deployment`](docs/deployment.md#air-gapped--isolated-deployment).

## Launch Sequence

statnive-live ships in **three public-facing phases across two deployments**. Same binary, schema, config differences only.

| Deployment | Host | Tenancy | Purpose | Phases |
|---|---|---|---|---|
| **D1 — `statnive.live` (SaaS)** | Hetzner CX32 (v1) → AX41/AX42 | Multi-tenant, pooled CH | Dogfood + public SaaS | A, C |
| **D2 — `SamplePlatform.statnive.live` (Dedicated)** | Iranian DC (Asiatech / Shatel / Afranet) | Single-tenant (`site_id=1`), air-gapped | SamplePlatform production | B |

**Routing:** single tracker URL per deployment (`statnive.live/tracker.js`, `SamplePlatform.statnive.live/tracker.js`); `site_id` resolved server-side from `Origin`/`Referer`. Path-based tenant routing in Phase C: `app.statnive.live/s/<slug>/…`. One TLS cert for `statnive.live` + `demo.statnive.live` + `app.statnive.live` + `SamplePlatform.statnive.live`; no wildcard in v1. Subdomain-per-tenant branding = v2 upsell.

**Auth per phase:** A demo = shared `demo/demo-statnive` viewer (displayed on login); B SamplePlatform = admin+viewer, rotatable via `/api/admin/users`; C SaaS = email+password, bcrypt + 14-day session.

**License per phase:** D1 (A + C) = no JWT (our instance; admin-user gating). D2 (B) = Ed25519 JWT at `config/license.key`, `MaxEventsDay=0`, `Features=["*"]`, `ExpiresAt=+1y`, offline.

### Phase A — Dogfood on statnive.com (Weeks 20–21)

D1 Hetzner CX32 (~€13/mo). DNS A/AAAA for `statnive.live`, `demo.statnive.live`, `app.statnive.live`. Manual PEM via `certbot certonly --manual --preferred-challenges dns` on laptop, drop on D1, quarterly cron+SIGHUP. LITE DB23. Config diff: `tls.{cert_file,key_file}` set; `license.required=false`. Seed: `INSERT INTO sites VALUES (1, 'statnive.com')`; shared viewer + internal admin. Tracker in `statnive-website/` Astro base layout. Login-attempt cap 10/min/IP. Banner: "Public demo — statnive.com traffic — viewer role, no writes".

**Acceptance:** 24h → non-zero visitors; viewer 403 on `/api/admin/*`; all 8 `/api/stats/*` return `site_id=1` data.

### Phase B — SamplePlatform dedicated Iranian VPS (Weeks 22–25)

Cutover scope = **SamplePlatform web only** (P1 onboarding, ~200K DAU / 3M views/day). D2 initial = Asiatech G1/G2 VPS (~15–28M Rial/mo). Graduates to dedicated bare-metal at P3 (~3mo post-cutover, +iOS). Hardware per sub-phase table in Phase 10. Install = offline bundle (`make airgap-bundle`) → SCP via bastion → verify SHA256+Ed25519 → `deploy/airgap-install.sh`. DNS `CNAME SamplePlatform.statnive.live → <Iranian-DC-IP>` (Cloudflare proxy **OFF**). Manual PEM (SamplePlatform internal CA preferred, or self-signed w/ distributed root), quarterly. Upgrade to paid IP2Location DB23. License JWT + age-encrypted key. Config: `audit.sink=file`, `license.phone_home=false`. Seed SamplePlatform hostnames. Admin via secure channel. Tracker `<script src="https://SamplePlatform.statnive.live/tracker.js" defer></script>` + root-domain cookie walking. Firewall: `iptables -P OUTPUT DROP` except loopback, CH localhost, tracker client IPs, DNS, NTP.

**Acceptance (P1 StreamCo MIN) — doc 29 §4.1 graduation gate:** 72h soak @ 240 EPS with diurnal curve, 7-scenario chaos matrix (BGP cut / mobile curfew / DPI RST / Tehran-IX degrade / Asiatech DC outage / clock skew / international-egress per doc 30 §3), 0→450 EPS breakpoint. Every SLO green (server loss ≤0.05%, client loss ≤0.5%, duplicates ≤0.1%, attribution ≥99.5% independently across 62% Iran + 38% diaspora cohorts per doc 30 §3, PII wire-scan rate()=0, p99<500ms, TTFB overhead ≤+25ms). Air-gap end-to-end + monthly backup+restore remain prerequisite. P2/P3/P4/P5 repeat the gate at 1K/4K/9K-18K/40K peak EPS before onboarding each successive app; P4 + P5 additionally require the long-session memory-leak soak (doc 30 §6, verification §48).

### Phase C — International SaaS self-serve (Weeks 25–29)

D1 continues Phase A; CX32 → AX41 at ~10 paying. New: `POST /api/signup`, `POST /api/billing/checkout`, `POST /api/admin/billing` (Polar webhook). Path-based tenant routing via chi middleware: `/s/<slug>/` → `site_id` context → scoped `/api/stats/*`. Missing slug → 404 / redirect to root login. Guardrails: DNS-resolvable, not blocklisted, unique, 5/hr/IP, email verification (24h grace). Free 10K PV/mo (v1 `count(DISTINCT)` → v1.1 `daily_users`); over-quota = soft throttle + `quota_exceeded=1` + upsell. Polar Products × monthly+yearly (Free self-hosted only, Starter $9, Growth $19, Business $69, Scale $199). **Merchant of Record** = no per-country tax registration. v1 Polar scope = checkout + webhook only; Portal + Benefits = v2. No Go SDK — call REST directly. Sandbox: `sandbox-api.polar.sh`. Onboarding at `/s/<slug>/onboarding` (user-triggered refresh). Email transactional opt-in via `email.enabled`.

**Acceptance:** signup → tracker → first event <5min; cross-tenant isolation (URL-manipulation blocked); Polar sandbox `subscription.created` → `sites.plan`, `subscription.canceled` reverts at period end; idempotent webhook; 6th signup/hr rejected.

## Key Files (Already Written)

All Go code from doc 22 is in the working tree. Per-package inventory in [`docs/repo-structure.md`](docs/repo-structure.md).

**License-verified deps (MIT/Apache/BSD/ISC):** clickhouse-go/v2 (Apache-2.0), go-chi/chi (MIT), go-chi/httprate (MIT), tidwall/wal (MIT), ip2location-go/v9 (MIT), medama-io/go-useragent (MIT), omrilotan/isbot (MIT), bits-and-blooms/bloom (BSD-2), lukechampine.com/blake3 (MIT), google/uuid (BSD-3), gopkg.in/yaml.v3 (MIT), filippo.io/age (BSD-3), klauspost/compress (BSD-3), bcrypt/acme/autocert (BSD-3), spf13/viper (MIT). ⚠️ hashicorp/golang-lru (MPL-2.0 weak copyleft — use unmodified). ❌ knadh/koanf (AGPL-3.0 — never use). ❌ pirsch-analytics/pirsch (AGPL-3.0 — never import). `go-licenses check ./...` must pass in CI.

## Technology Docs Cache

Context7-cached per-library API references (14 libs, 2026-04-17 snapshot). Full index + plan decisions that originated from the cache + API deltas (Vite Rolldown, JSX config, golang-lru v2, Vitest v4) in [`docs/tech-docs-index.md`](docs/tech-docs-index.md). Per-library files in [`docs/tech-docs/`](docs/tech-docs/).

## Verification

1. `go build ./cmd/statnive-live` compiles
2. `make test` passes (unit + integration)
3. `go-licenses check ./...` — zero AGPL / strong-copyleft
4. k6 load test sustains 7K EPS with p99 <500ms
5. All dashboard endpoints (8 stats + 2 admin + 1 realtime) scoped by `WHERE site_id = ?`
6. Multi-tenant isolation: site_id=A invisible in site_id=B queries
7. Enrichment order asserted: bot event → visitor_hash populated AND is_bot=1
8. Security: auth required, httprate 429, TLS 1.3 only, CH 127.0.0.1, hostname validation rejects foreign Origin
9. Crash recovery: kill -9 → restart → zero event loss (WAL replay)
10. CH outage: stop 10min → WAL buffer → resume → zero loss
11. Disk-full: 10GB cap → 503 with clear error, existing events preserved
12. Backup restore: row counts match exactly
13. TLS rotation: replace PEMs + SIGHUP → new cert without restart; expiry alert <30d
14. Tracker: install → events in dashboard <1h
15. GDPR (SaaS): consent decline drops cookies + user_id; `/api/privacy/erase` removes visitor across raw + all v1 rollups
16. License: demo-mode caps 10K/day; valid JWT unlocks; expired → demo-mode with warning
17. **Air-gapped acceptance**: offline bundle on `iptables -P OUTPUT DROP` host (loopback + tracker IPs). Binary starts, migrations apply, events ingest, rollups materialize, dashboard renders, backup+restore — zero outbound (skill: [`air-gap-validator`](.claude/skills/air-gap-validator/README.md))
18. **Offline build**: `go build -mod=vendor ./...` succeeds with `GOFLAGS=-mod=vendor`, no network
19. Manual TLS: binary serves with `tls.{cert_file,key_file}` internal-CA PEMs; no autocert code path (v1)
20. Air-gapped GeoIP update: replace BIN + SIGHUP → new IPs resolve without restart
21. **Pre-pipeline fast-reject** (doc 24 §Sec 1.6): handler 204 on `X-Purpose: prefetch`, UA <16 or >500, UA-as-IP, UA-as-UUID, non-ASCII — zero pipeline work
22. **Cross-day fingerprint grace** (doc 24 §Sec 1.1): visitor hashed 23:58 IRST salt S₁ returns 00:02 IRST → identified as returning via yesterday-salt lookup (skill: [`blake3-hmac-identity-review`](.claude/skills/blake3-hmac-identity-review/README.md))
23. **Bot ordering** (doc 24 §Sec 1.3): malformed UA / prefetch / spam referrer / outdated Chrome / regex-bot short-circuit at expected layer
24. **Central tenancy helper** (Rule 8): CI lint asserts every `SELECT` in `internal/storage/` calls `whereTimeAndTenant()` (skill: [`tenancy-choke-point-enforcer`](.claude/skills/tenancy-choke-point-enforcer/README.md))
25. **Schema time column**: `time` is `DateTime('UTC')` on `events_raw` + rollups (skill: [`clickhouse-rollup-correctness`](.claude/skills/clickhouse-rollup-correctness/README.md))
26. **Templated migration DDL** (doc 24 §Sec 2 Migration 0029): `{{if .Cluster}}` placeholders render for single-node + `ReplicatedMergeTree` + `Distributed` (skill: [`clickhouse-cluster-migration`](.claude/skills/clickhouse-cluster-migration/README.md))
27. **No Nullable columns** (Rule 5): CI lint — no `Nullable(` in `clickhouse/` or `internal/storage/migrate.go` (skill: [`clickhouse-rollup-correctness`](.claude/skills/clickhouse-rollup-correctness/README.md))
28. **Hostname lookup shape** (doc 24 §Sec 3.5): `map[string]struct{}` not `slices.Contains` — p99 <50 ns/call
29. **AI channel day 1** (doc 24 §Sec 3.3): `chat.openai.com` / `claude.ai` / `gemini.google.com` / `copilot.microsoft.com` / `perplexity.ai` → `channel="AI"`
30. **Day-of-week growth comparison** (v1.1, doc 24 §Sec 5 T2 #19): this-Tuesday-vs-last-Tuesday correct
31. **Phase A (dogfood)**: statnive.com pageview → `demo.statnive.live` <5min; shared viewer 403 on `/api/admin/*`; login cap 10/min/IP
32. **Phase B (SamplePlatform)**: `SamplePlatform.statnive.live/tracker.js` → dashboard <5min; `iptables -P OUTPUT DROP` end-to-end on Iranian DC; backup+restore on dedicated instance
33. **Phase C (SaaS)**: signup → tracker → first event in `app.statnive.live/s/<slug>` <5min; cross-tenant isolation (URL-manipulation blocked); Polar sandbox webhook updates `sites.plan`; idempotent; 6th signup/hr rejected
34. **Kill-9 WAL gate** (doc 27 §Gap 1): CI 10K events → kill -9 random 100ms–2s → restart → `count() FROM events_raw == client 2xx` (within 0.05% SLO). 50 runs/PR. 7b close gate. (skill: [`wal-durability-review`](.claude/skills/wal-durability-review/README.md))
35. **CGNAT rate-limit tiering** (doc 27 §Gap 2): k6 `cgnat` = 7K EPS from 100 IPs simulating AS44244 — MUST NOT 429. `ddos` = 30K EPS from 50 IPs — MUST 429 (>50% fail). `normal` = 7K EPS from 10K IPs — <1% fail p99 <500ms. Phase 10 cutover gate. (skill: [`ratelimit-tuning-review`](.claude/skills/ratelimit-tuning-review/README.md))
36. **DSAR completeness** (doc 27 §Gap 3): synthetic `visitor_hash` → 100 events → `/api/privacy/erase` → WAL drain → `system.tables` enumerated dynamically → zero rows matching hash in every non-rollup table. New table without erase.go entry fails this test by construction. Phase 11 gate. (skill: [`dsar-completeness-checker`](.claude/skills/dsar-completeness-checker/README.md))
37. **Blackout-sim green** (doc 28 §Gap 2): vendored `-tags airgap` binary under `iptables -P OUTPUT DROP` (loopback + Docker bridge only). `/health/ready` within 30s, dashboard loads, 50 `POST /t` succeed, `/api/stats?range=1h` ≥50 pageviews, S3 backup degrades (`s3.*(unreachable|timeout).*continuing|degraded mode`), file-sink only (no `slack|pagerduty|opsgenie`). **HARD GATE** on every PR after Week 18. (skill: [`iranian-dc-deploy`](.claude/skills/iranian-dc-deploy/README.md))
38. **GeoIP hot-reload under load** (doc 28 §Gap 1): 100 concurrent lookup goroutines with `-race`; `SIGHUP` 100 times in 1s. p99 <500ms, zero FD leak across 1,000 swaps, last swap wins, zero lookup errors. 7K EPS k6 log grep for IPv4/IPv6 regex — zero matches. Gates Phase 10 paid-DB23 cutover. (skill: [`geoip-pipeline-review`](.claude/skills/geoip-pipeline-review/README.md))
39. **CH parts-ceiling + restore drill** (doc 28 §Gap 3): k6 5min 7K EPS — active parts <100, zero `RejectedInserts`, `DelayedInserts` <50, p99 `http_req_duration{kind:ingest}` <500ms. Nightly + labeled-PR `clickhouse-backup` create+upload+restore → row-count parity + `uniqCombined64Merge(uniq_state) FROM rollup_daily FINAL FORMAT Null` clean. Required before Week 23 load-rehearsal. (skill: [`clickhouse-operations-review`](.claude/skills/clickhouse-operations-review/README.md))
40. **Generator_seq oracle** (doc 29 §6.1): every synthesized event carries `(test_run_id, generator_node_id, test_generator_seq, send_ts)`; one ClickHouse query per run (loss / duplicates / ordering / latency) runs in <60s. Projection `proj_oracle` ORDER BY `(test_run_id, generator_node_id, test_generator_seq)` MATERIALIZEd on staging CH. (skill scheduled: `load-gate-harness`, Phase 7e)
41. **Per-phase graduation gate** (doc 29 §4): 72h soak + 7-scenario chaos + breakpoint 150% passes every SLO before tracker points at that phase. Binary sign-off ceremony with SamplePlatform analytics owner per sub-phase. No hand-wavy partial pass; any single SLO breach halts the gate. Target = doc 29 MAX envelope (200M events/day, 40K EPS burst at P5), not doc 30 observed — per Context "design ceiling vs observed" callout.
42. **7-scenario chaos matrix** (doc 29 §5 + doc 30 §3): A BGP cut (iptables NIN-only, 6h), B mobile curfew (tc netem 80% loss on mobile-AS srcs, 8h), C DPI RST (xt_tls on flagged SNI, 4h), D Tehran-IX degrade (iptables drop 185.1.77.0/24 + 60ms netem, 3h), E Asiatech DC partial outage (iptables drop DC subnet, 1h), F clock skew (block UDP 123 + date-drift, 4h), **G international-egress degraded (tc netem 80–120ms + 2% loss on outbound Tehran-IX / Asiatech → Frankfurt peering, 3h — pins the 38% diaspora cohort per doc 30 §3; oracle correlates `country != 'IR'` event loss independently of the 62% Iran cohort so attribution-correctness ≥99.5% holds for both cohorts, not just the weighted average)**. Each scenario ships with its tc/netem/iptables/xt_tls script AND its oracle SQL.
43. **Breakpoint ramp** (doc 29 §4): 0 → 150% of phase peak EPS over 30 min locates the failure knee above SLO ceiling. Not required to pass SLO above 100%; required to identify the knee for capacity planning and confirm graceful degradation.
44. **Production-replay protocol** (doc 29 §9 open item 4 + doc 30 §8): one anonymized NDJSON export per phase from SamplePlatform; cleanroom attestation signed by SamplePlatform analytics owner (regex-scrub spec + salt rotation per phase + auto-delete kill-switch post-sign-off); chain-of-custody from SamplePlatform bucket to statnive staging documented. **Seed distribution**: at least 40% of events target returning `visitor_hash` values per doc 30 §8 (SamplePlatform Week-1 retention 42.8%, Week-4 32.6%) — uniform-random under-exercises the bloom filter + cross-day fingerprint grace (doc 24 §Sec 1.1) path.
45. **PII wire-scan via Vector.dev + VRL** (doc 29 §3.4, §6.3): live <1s detection at 15K+ EPS (ipv4 / ipv6 / email / user_id regex redact); Loki stream `pii_leak=true`; Alertmanager fires on `rate() > 0` → gate halts. Supersedes one-shot [`test/pii_leak_test.go`](test/pii_leak_test.go) byte-scan.
46. **Staging cost envelope** (doc 29 §7): ≤40% of production monthly cost, billing-average — warm AT-VPS-B1 between gates; prod-parity during 72h hot window; ≤150% burst during breakpoint. All five phases satisfy the envelope at 17–37% per §7 table. Teardown scripted, not manual.
47. **Observability VPS separation** (doc 29 §3.2): Prometheus + Grafana + Pyroscope + Loki + Vector.dev on a dedicated AT-VPS on isolated rack/AZ from generators + target. Verified by `strace -f -e trace=connect` burn-in under `iptables -A OUTPUT -j DROP` except observability VLAN. Prevents softirq contention at peak from manifesting as phantom server-side regressions.
48. **Long-session memory-leak soak** (doc 30 §6): 1000 virtual users × 6h sessions × 1,080 progress-pings @ 20s cadence. Assert zero tracker JS memory leak across 6h, `visitor_hash` stable across cross-day salt rotation, zero duplicate `session_id` emission, all beacons correctly attributed. Exercises the long-Android-binge + mobile-web-power-user cohort (30% + 15% of sessions by count, majority of event volume) that doc 29's 4–15 events/session estimate missed — the heaviest mobile-web cohort averages 8h 26m engagement per doc 30 §6. Runs as part of P4 + P5 graduation gates.
49. **Diaspora-cohort load mix + SLO segmentation** (doc 30 §3): load generator emits 62% Iran-origin + 38% diaspora-origin sessions (19% Germany / 9% US / 7.5% Finland-VPN / 2.8% UK / 2.7% FR / 2.5% CA). Finland cohort tagged as Iranian-over-VPN with high-latency + high-jitter + VPN-egress-reliability profile (Lantern/Psiphon signature). Oracle SQL segments loss / duplicate / attribution SLOs per `geographic_cohort_id` in the generator_seq quadruple; all SLOs must hold independently on both cohorts, not just the weighted average. Pairs with chaos scenario G for the international-egress failure-mode coverage.
50. **Multi-tenant JOIN safety (F1, Phase 1+).** Every `JOIN` / subquery / CTE / `IN (SELECT …)` in `internal/storage/queries.go` carries `WHERE <joined>.site_id = ?` against the joined table, not just the outer predicate. Test fixture: synthetic two-tenant dataset (site_id=1 with 100 events, site_id=2 with 100 events), every `/api/stats/*` response queried as site_id=1 returns zero rows from site_id=2 even when a join against `sites` / `goals` / `funnels` / `daily_*` could cross-contaminate. Enforced by [`clickhouse-rollup-correctness`](.claude/skills/clickhouse-rollup-correctness/README.md) Semgrep rule `rollup-join-tenancy`. Complements [`tenancy-choke-point-enforcer`](.claude/skills/tenancy-choke-point-enforcer/README.md) (outer `WHERE`) — this covers the inner tables. (Pattern from jaan-to-plugin research doc 67; adapted for Go+CH from WordPress/PHP.)
51. **Outbound SSRF allow-list (F2, CLAUDE.md §Security #14).** When any opt-in outbound path is enabled, all outbound `http.Client` / `net.Dialer` traffic routes through `internal/httpclient/guarded.go`. Unit test asserts: (a) FQDN not on `config.outbound.allowlist` → dial error; (b) RFC 1918 (`10/8`, `172.16/12`, `192.168/16`), loopback (`127/8`, `::1`), link-local (`169.254/16`, `fe80::/10`), and CGNAT (`100.64.0.0/10`) rejected post-DNS resolution (DNS-rebinding probe with a domain that resolves to `127.0.0.1` must be blocked); (c) `http://` scheme rejected, only `https://` accepted. Default `config.outbound.allowlist: []` preserves the air-gap Isolation invariant — the guard only applies when an operator opts in. Enforced by [`air-gap-validator`](.claude/skills/air-gap-validator/README.md) Semgrep rule `airgap-no-raw-httpclient` flagging any `&http.Client{}` / `http.DefaultClient` / `http.Get` / `http.Post` outside `internal/httpclient/`. (OWASP A10 mapping per jaan-to-plugin research doc 72 §11.)
52. **Mass-assignment guard on write endpoints (F4, Phase 3c + Phase 11).** Every mutating handler (`/api/signup`, `/api/admin/users`, `/api/admin/goals`, `/api/admin/billing`, future admin CRUD) decodes request JSON with `json.NewDecoder(r.Body).DisallowUnknownFields()` and enforces a per-endpoint `AllowedFields []string` whitelist pre-unmarshal. Sensitive fields (`site_id`, `role`, `is_admin`, `plan`) sourced exclusively from session context (or the verified Polar webhook payload for billing) — never from request body. Integration test: cross-tenant request body `{"site_id": 2, "role": "admin"}` submitted by a session-bound site_id=1 admin → handler writes site_id=1 (session value) and rejects the unknown/privileged fields; site_id=2 remains untouched. (Laravel mass-assignment pattern — jaan-to-plugin research doc 04 + CVE class in doc 67 — adapted to Go JSON unmarshaling.)
53. **Auth-return nil-guard (F5, Phase 2b).** Every caller of `internal/auth/**` functions with signature `(T *<Ptr>, err error)` guards `<ptr> != nil` after `err == nil`, not just `err != nil`. Regression test: a fault-injected `Authenticate()` implementation returning `(nil, nil)` is caught at every call site — not one handler allows request processing with a nil `*User` / `*Session` / `*License`. Enforced by [`blake3-hmac-identity-review`](.claude/skills/blake3-hmac-identity-review/README.md) Semgrep rule `auth-return-nil-guard`. (CVE-2024-10924 "Really Simple Security" pattern — jaan-to-plugin research doc 67 — applies to any Go `(*T, error)` return shape.)
54. **End-to-end boot smoke on every PR (Phase 5a-smoke).** `make smoke` via the `smoke-test` CI job drives the real `cmd/statnive-live/main.go` binary — not an `httptest.Server`, not a manually-wired `chi.Router` — against docker-compose ClickHouse (same container as `integration-tests` and `wal-killtest-smoke`) and probes every production surface: `/healthz` (JSON shape + WAL fsync p99 key), `/tracker.js` (IIFE + nosniff + `application/javascript`), `/app/` (CSP + nosniff + Referrer-Policy + `<div id="statnive-app">` + bearer placeholder rewritten), `/app/assets/*.js` (long-cache + body ≥ 5 KB), `POST /api/event` × 10 with `count() FROM statnive.events_raw WHERE site_id = <smoke>` count-back within 10 s, `/api/stats/overview` with bearer enforcement (401 without the header, 200 + 5 KPI keys with). Exercises `rateLimitMW` + `BackpressureMiddleware` + `dashboardAuthMW` from the prod router graph on every PR. Harness at `test/smoke/harness.sh`; pre-cutover canonical verification for Phase 10 per `docs/runbook.md` § Pre-cutover verification.
