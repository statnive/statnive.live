# statnive-live — Self-Hosted & SaaS Analytics Platform

## Context

9 research documents (docs 14–22), 400+ sources, and 2,000+ lines of drop-in Go code are complete. All architecture, features, schema, and security decisions are finalized.

**statnive-live** is the standalone analytics platform (separate from the WordPress plugin "statnive"). It targets Iranian high-traffic sites, with Filimo as first customer.

**Reference streaming workload (StreamCo, confirmed 2026-04-19).** Two endpoints frame the capacity envelope — we ship the minimum first and ramp to the maximum app-by-app.

| Envelope | Scope | Peak day events | Monthly events | Bandwidth/mo | Disk/year | EPS peak |
|---|---|---|---|---|---|---|
| **MINIMUM — P1 cutover** | Web app, required events (views) only | 3M | 75M | ~22 GB | ~36 GB | ~140 |
| **MAXIMUM — P5 full build** | All apps (web + iOS + Android + TV), all optional events | 200M | 4B | ~1.2 TB | ~1.9 TB | ~9,000 (spike ~18K) |

Minimum fits the cheapest Asiatech VPS (G1/G2 standard plan, ~15–28M Rial/mo). Maximum requires a 2–3 node Iranian-DC cluster with HA and unmetered bandwidth (~800M–1.5B Rial/mo). 5-phase app-by-app roadmap between them (P1 web views → P2 web+interactions → P3 +iOS → P4 +Android → P5 +TV/HA) in [`../jaan-to/outputs/capacity-planning-standalone-analytics.md`](../jaan-to/outputs/capacity-planning-standalone-analytics.md).

- **Repo:** https://github.com/statnive/statnive.live.git
- **Folder:** `statnive-live/`
- **Domain:** statnive.live

---

## Product Definition

**statnive-live** = Go single binary + ClickHouse analytics platform

**Decisions locked:**
- **Greenfield build** — 100% original code. Do NOT copy Pirsch source (AGPL). Use doc 22's 2,000 LOC as starting point. Study Pirsch fork at `~/Projects/pirsch/` for patterns and architecture reference only.
- **License: ALL dependencies must be MIT/Apache/BSD** — no AGPL in the binary. statnive-live will be sold as SaaS outside Iran where AGPL Section 13 applies.
- **Multi-tenant from v1** — `site_id` in schema from day 1. Filimo = site_id=1. SaaS-ready.
- **Dual hosting** — Hetzner (€46/mo) for dev/staging, Iranian DC (~€180/mo) for Filimo production.
- **Pirsch as reference only** — study `~/Projects/pirsch/` for ClickHouse schema patterns, session logic, channel mapping approach. Never import or copy code.

Two distribution models from day 1:

| Model | Description | Revenue |
|-------|-------------|---------|
| **Self-hosted** | Customer runs statnive-live on their own server | License fee (paid, not open-source). Manual activation for now — no payment system yet. Need license management system. |
| **SaaS (managed)** | We host on Hetzner (outside Iran only) | Monthly subscription by pageviews |

Both models use the **exact same Go binary**. Multi-tenant via `site_id` column on all tables + `WHERE site_id = ?` on all queries. SaaS adds billing metering on top.

---

## Repository Structure

Items marked **[shipped]** exist in the working tree as of the most recent
merged PR. Items marked **[planned]** are scheduled for a later phase.

```
statnive-live/                          # https://github.com/statnive/statnive.live.git
├── CLAUDE.md                           # Project rules                                                  [shipped]
├── cmd/
│   └── statnive-live/
│       └── main.go                     # Entry point: wiring, SIGHUP fan-out, graceful shutdown         [shipped]
├── internal/
│   ├── config/
│   │   ├── secret.go                   # Master-secret loader (env → file → fail-closed)                [shipped]
│   │   └── secret_test.go                                                                                [shipped]
│   ├── audit/                          # JSONL append-only file sink (Phase 2a)                          [shipped]
│   │   ├── events.go                   # Typed EventName constants (TLS / ratelimit / ingest events)
│   │   ├── log.go                      # Logger with O_APPEND + SIGHUP-aware Reopen()
│   │   ├── log_test.go
│   │   └── audittest/
│   │       └── audittest.go            # Test-only ReadEventNames helper (substring scan)
│   ├── cert/                           # TLS lifecycle (Phase 2a)                                        [shipped]
│   │   ├── loader.go                   # atomic.Pointer hot-reload, fail-closed, keep-old-on-fail
│   │   ├── expiry.go                   # 6h ticker, <30d warn / <7d critical, crossing-dedup
│   │   ├── loader_test.go
│   │   └── expiry_test.go
│   ├── ratelimit/                      # NAT-aware go-chi/httprate wrapper (Phase 2a)                    [shipped]
│   │   ├── ratelimit.go                # Keys via ingest.ClientIP; 429 emits audit event
│   │   └── ratelimit_test.go
│   ├── ingest/
│   │   ├── event.go                    # RawEvent + EnrichedEvent (34 fields incl. site_id)             [shipped]
│   │   ├── fastreject.go               # POST-only + prefetch/UA-shape gate as chi middleware           [shipped]
│   │   ├── handler.go                  # POST /api/event; ClientIP exported; Audit nil-safe field       [shipped]
│   │   ├── handler_test.go             # 10-case fast-reject table                                       [shipped]
│   │   ├── wal.go                      # tidwall/wal, 100ms fsync, 10GB cap                              [shipped]
│   │   └── consumer.go                 # Dual-trigger batch writer (1000 rows / 500ms / 10MB)            [shipped]
│   ├── enrich/
│   │   ├── pipeline.go                 # 6-worker pipeline (identity→bloom→geo→ua→bot→channel)          [shipped]
│   │   ├── channel.go                  # 17-step decision tree; reload via main.go's runSIGHUP          [shipped]
│   │   ├── geoip.go                    # IP2Location wrapper (no-op fallback when no BIN configured)    [shipped]
│   │   ├── ua.go                       # medama-io/go-useragent singleton                                [shipped]
│   │   ├── bot.go                      # Cheap-first matcher + embedded crawler-user-agents.json        [shipped]
│   │   ├── newvisitor.go               # Bloom filter (18MB / 10M / 0.1% FPR) + cross-day grace         [shipped]
│   │   ├── crawler-user-agents.json    # Embedded bot patterns (refresh via make refresh-bot-patterns)  [shipped]
│   │   └── *_test.go                   # Per-component unit tests                                        [shipped]
│   ├── identity/
│   │   ├── hash.go                     # BLAKE3-128 visitor hash + SHA-256 user_id hash                 [shipped]
│   │   ├── salt.go                     # IRST daily salt (HMAC-SHA256), 5-min overlap, in-mem cache    [shipped]
│   │   └── identity_test.go                                                                              [shipped]
│   ├── storage/
│   │   ├── clickhouse.go               # 34-col batch insert (incl. site_id) + 1 retry                  [shipped]
│   │   ├── migrate.go                  # Templated migrations, schema_migrations bookkeeping            [shipped]
│   │   ├── migrations/                 # SQL embedded via go:embed (lives here, not under clickhouse/)
│   │   │   ├── 001_initial.sql         # events_raw + sites + schema_migrations                          [shipped]
│   │   │   └── 002_rollups.sql         # hourly_visitors + daily_pages + daily_sources + MVs            [shipped]
│   │   ├── store.go                    # Typed Store interface + ErrNotImplemented                     [shipped]
│   │   ├── queries.go                  # whereTimeAndTenant + 6 v1 query implementations               [shipped]
│   │   ├── filter.go                   # Filter struct + Validate + BLAKE3 Hash                         [shipped]
│   │   ├── result.go                   # Typed per-endpoint result structs                              [shipped]
│   │   ├── cached_store.go             # LRU decorator with per-endpoint TTL                            [shipped]
│   │   └── storagetest/                # SeedEvents + CleanSiteEvents helpers (test-only)               [shipped]
│   ├── sites/
│   │   └── sites.go                    # Hostname → site_id lookup; slug/create/disable in Phase 11    [shipped]
│   ├── health/
│   │   └── check.go                    # /healthz (CH ping + WAL fill + uptime)                         [shipped]
│   ├── cache/                          # LRU (realtime=10s / today=60s / historical=∞) + ResolveTTL    [shipped]
│   │   ├── lru.go                      # Thread-safe cache with per-entry expiresAt TTL
│   │   └── policy.go                   # TTL tier constants + ResolveTTL pure function
│   ├── dashboard/                      # 8 GET /api/stats/* + /api/realtime/visitors + bearer-token mw [shipped]
│   │   ├── filter.go                   # ?site / ?from / ?to (IRST→UTC) → storage.Filter
│   │   ├── stats.go                    # 8 handlers (3 return 501 until v1.1/v2)
│   │   ├── realtime.go                 # GET /api/realtime/visitors (10s cache via CachedStore)
│   │   ├── errors.go                   # writeError + classifyError (400 / 501 / 500 + audit)
│   │   ├── auth.go                     # BearerTokenMiddleware (stub — Phase 2b replaces wholesale)
│   │   └── router.go                   # Mount(chi.Router, Deps) — caller decides middleware stack
│   │   # admin/* + signup/* + billing/* routes wait on                                   [planned: Phase 3c + 11]
│   └── auth/                           # bcrypt sessions + RBAC (admin / viewer / api)                 [planned: Phase 2b]
├── web/                                # Preact SPA (Vite + TypeScript + @preact/signals)              [planned: Phase 5]
├── tracker/                            # <2KB IIFE tracker (sendBeacon + history API)                  [planned: Phase 4]
├── clickhouse/
│   └── schema.sql                      # Reference DDL pointer to internal/storage/migrations/         [shipped]
├── config/
│   ├── statnive-live.yaml              # Defaults: server, clickhouse, ingest, enrich, tls, audit,     [shipped]
│   │                                   # ratelimit, license. master.key path.
│   └── sources.yaml                    # 60+ Iranian + AI referrer entries                              [shipped]
├── deploy/
│   ├── docker-compose.dev.yml          # Local dev ClickHouse (named volumes, 127.0.0.1 only)          [shipped]
│   ├── statnive-live.service           # systemd unit (NoNewPrivileges, ProtectSystem)                 [planned: Phase 2c]
│   ├── iptables.sh                     # Firewall rules (80/443/22; CH never exposed)                  [planned: Phase 2c]
│   ├── backup.sh                       # clickhouse-backup + age + zstd                                 [planned: Phase 2c]
│   ├── airgap-install.sh               # One-shot offline installer                                    [planned: Phase 8]
│   └── airgap-update-geoip.sh          # Offline GeoIP DB rotation                                     [planned: Phase 8]
├── vendor/                             # Vendored Go deps — checked in for offline builds              [shipped]
├── offline-bundle/                     # Release artifact (binary + DB23 + SHA256SUMS + signature)     [planned: Phase 8]
├── docs/
│   ├── tech-docs/                      # Context7-cached library refs (16 libs)                         [shipped]
│   └── tooling.md                      # Claude Code skills + MCP setup                                 [shipped]
├── test/
│   ├── integration_test.go             # 100-event smoke (handler → WAL → CH)                          [shipped]
│   ├── enrichment_e2e_test.go          # All 6 stages produce expected events_raw columns              [shipped]
│   ├── multitenant_isolation_test.go   # Privacy Rule 2: per-tenant visitor_hash separation            [shipped]
│   ├── security_test.go                # Rate limit short-circuits before events reach ClickHouse      [shipped]
│   ├── dashboard_isolation_test.go     # Architecture Rule 8: every Store query scoped by site_id     [shipped]
│   ├── tls_keys/                       # Self-signed cert+key (make tls-test-keys)                     [shipped]
│   └── k6/load-test.js                 # 7K EPS smoke                                                  [planned: Phase 7]
├── Makefile                            # build, test, test-integration, lint, fmt, vendor-check,       [shipped]
│                                       # licenses, tenancy-grep, dev-secret, tls-test-keys,
│                                       # refresh-bot-patterns. airgap-bundle / release stubs.
├── go.mod
├── go.sum
└── README.md                           # Operator quick-start                                          [planned]
```

---

## Development Phases

### Status — 2026-04-19

| Phase | Status | Notes |
|---|---|---|
| **0 — Project setup** | ✅ Complete | PR #1 merged. Repo, Makefile, CI, schema, vendoring all live. |
| **1 — Ingestion pipeline** | ✅ Complete | PR #2 merged. Real 6-stage enrichment, BLAKE3 + IRST salt, 18 MB bloom + cross-day grace, 17-step channel tree, 503 back-pressure. Burst guard shipped in PR #14. Deferred: advisory locks. |
| **2a — Surface hardening** | ✅ Complete | PR #6 merged. TLS 1.3 with manual PEM + SIGHUP reload + expiry watcher; httprate rate limit (NAT-aware, audit-instrumented); JSONL audit log file sink with logrotate-aware reopen; FastRejectMiddleware so prefetches don't burn rate-limit budget. |
| **2b — Auth + RBAC** | ⏳ Pending | bcrypt + crypto/rand sessions + SameSite=Lax cookies + admin/viewer/api roles. Can land in parallel with Phase 3b. |
| **2c — Operational hardening** | ⏳ Pending | systemd unit (NoNewPrivileges + ProtectSystem), iptables, LUKS docs, backup script (clickhouse-backup + age + zstd). Pairs with Phase 8 deploy. |
| **3a — Dashboard query foundation** | ✅ Complete | PR #9 merged. Filter + Store interface + 6 v1 queries (Overview/Sources/Pages/SEO/Campaigns/Realtime) + LRU with real per-entry TTL + tenancy-grep CI gate. Geo/Devices (v1.1 rollups) + Funnel (v2 windowFunnel) return ErrNotImplemented. |
| **3b — Dashboard HTTP layer** | ✅ Complete | PR #12 merged. 8 stat handlers + realtime endpoint + IRST-aware Filter parsing + bearer-token middleware (stub until Phase 2b) + WITH FILL on SEO daily series + .gitignore fix that exposed cmd/statnive-live/main.go for the first time. |
| **3c — Admin CRUD** | ⏳ Pending | `/api/admin/users`, `/api/admin/goals` (writes YAML + SIGHUP). Needs Phase 2b auth to gate the mutations. Sequenced after backend gate per user mandate. |
| **4 — Tracker JS** | 🔜 Next | <2 KB IIFE, sendBeacon, SPA route tracking. Closes the loop — real browsers feed the backend that Phase 7a proved survives stress. |
| **5 — Dashboard frontend** | ⛔ Blocked | Preact SPA + uPlot + Frappe Charts. **User-mandated gate: Phase 7b (real-traffic verification) must be green first.** |
| **6 — Config & first-run** | 🟡 Partial | YAML loader, migrations, /healthz, env-var override (.→_) all done. Admin-user first-run + Goal/Funnel CRUD wait on Phase 2/3. |
| **7a — Backend solidity gate** | ✅ Complete | PR #14 merged. Burst guard (~50 ns/op) + Go bench suite + crash / CH-outage / disk-full stress tests + k6 7K EPS load script + WAL replay wired at boot + viper env-var fix. Surfaced 3 Phase 1 contract gaps for Phase 7b backlog. |
| **7b — Backend cleanup + real-traffic verification** | ⏳ Pending | Fix the 3 gaps from 7a (WAL replay decode after SIGKILL; consumer drops events when CH unreachable; post-restart drain). Then real-tracker correctness (queries match expected aggregations from Phase 4 tracker payloads). Backup restore drill + manual TLS rotation. |
| **8 — Deployment & launch** | ⏳ Pending | Hetzner CX32 staging, airgap-bundle, monitoring, runbook, v1 launch. |
| **9 — Phase A dogfood** | ⏳ Pending | statnive.com → demo.statnive.live. |
| **10 — Phase B Filimo** | ⏳ Pending | Iranian DC bare metal, paid DB23 GeoIP. |
| **11 — Phase C SaaS** | ⏳ Pending | Polar.sh checkout + webhooks, signup, path-based tenancy. |
| **CLI** (operator surface) | 🔮 v1.1 | `statnive` subcommands: serve, migrate, license, sites, users, backup, doctor, secret, stats. ~1 week. |
| **MCP server** (agent surface) | 🔮 v2 | Read-only analytics tools over stdio (air-gap-safe) or HTTP. Maps 1:1 to dashboard endpoints. ~2 weeks. |
| **Brand & design tokens** | 📐 Reference ready | Wordmark + summit logo, cream/ink/Persian-Teal palette, Fraunces + IBM Plex type ramp. Applies to statnive.live website + demo + tenant dashboards. See [§ Brand & Design](#brand--design--statnivelive-visual-identity). |

### Phase 0: Project Setup (Week 1)

- [x] Create `github.com/statnive/statnive-live` repository
- [x] Initialize Go module, copy go.mod from doc 22
- [x] Set up Makefile (build, test, lint, release, **airgap-bundle** targets) — `airgap-bundle` is a placeholder; lands in Phase 8
- [x] Create ClickHouse schema SQL (events_raw + **3 v1 rollups**: `hourly_visitors`, `daily_pages`, `daily_sources`; additional 3 deferred to v1.1)
- [x] Copy all Go files from doc 22 into project structure
- [x] Set up CI (GitHub Actions: build + lint + test + **`go mod vendor` check**) — actions SHA-pinned per security rule
- [x] **Vendor all Go deps** (`go mod vendor`, commit to repo) — enables fully offline builds
- [x] Create config/sources.yaml (50+ Iranian sources from doc 22) — 60 entries incl. AI bucket
- [x] Create config/statnive-live.yaml (default config from doc 20)

### Phase 1: Ingestion Pipeline (Weeks 2–4)

- [x] Wire main.go (from doc 22 bonus code)
- [x] Add `SiteID` field to EnrichedEvent + populate in pipeline.processEvent() — required for multi-tenant from v1
- [x] Implement ingest/handler.go (JSON array parsing; site_id resolved from hostname) — **pre-pipeline fast-reject gate** (doc 24 §Sec 1.6): reject `X-Purpose`/`Purpose`/`X-Moz` prefetch headers, UA length < 16 or > 500, UA that parses as IP or UUID, non-ASCII UA → `204 No Content` before the event enters the pipeline channel. Parse `True-Client-IP` + `CF-Connecting-IP` alongside `X-Forwarded-For` (rightmost) for Iranian sites behind ArvanCloud / Cloudflare.
- [x] Implement ingest/pipeline.go (6-worker enrichment; order **locked**: identity → burst → bloom → geo → ua → bot → channel). Bot detection is cheap-first *inside* the pipeline (doc 24 §Sec 1.3): UA literal substring → UA regex → optional datacenter CIDR. **Max-pageviews-per-visitor burst guard** shipped in PR #14 (`internal/ingest/burstguard.go`, ~50 ns/op). v1.1 still owes referrer-spam + browser-version-floor + datacenter-CIDR list.
- [x] Implement ingest/consumer.go (dual-trigger batch writer — size OR time OR ctx.Done per doc 24 §Sec 1.5: 1000 rows OR 500ms OR 10 MB payload. **No `log.Panicf` on retry exhaustion** — WAL + graceful failure.) (Single 250 ms retry instead of exponential backoff; DLQ deferred to first prod failure pattern.)
- [x] Implement ingest/wal.go (WAL + 100ms fsync + 10GB size cap). (>80% threshold surfaced via `/healthz` `wal_fill_ratio` rather than per-request 503; size-cap enforcer drops oldest segments when over.)
- [x] Implement storage/clickhouse.go (**34-column** batch insert incl. site_id; `DateTime('UTC')` time column — not `DateTime64(3)` per doc 24 §Sec 2 Migration 0012)
- [x] Implement storage/migrate.go — numbered migrations, applied versions tracked in a `schema_migrations(version, dirty, sequence)` table. Migrations authored with `{{if .Cluster}}` Go-template placeholders from day 1 (doc 24 §Sec 2 Migration 0029) so single-node → Distributed upgrade at SaaS scale is a config flip, not a migration rewrite. (Deferred: advisory locks for concurrent-start safety — single-binary deploys don't race on startup.)
- [x] Implement enrich/ (GeoIP with IP2Location **LITE DB23** in v1, medama-io UA, channel mapper, isbot + crawler-user-agents.json, bloom 18MB/10M visitors/0.1% FPR). Channel mapper implements the **17-step decision tree** per doc 24 §Sec 3.1 (paid-first ordering). Hostname lookups use `map[string]struct{}` not `slices.Contains` (~100× hot-path savings at 10–20M DAU per doc 24 §Sec 3.5). (GeoIP is a no-op enricher when no BIN file is configured — operator drops in DB23 separately; never blocks boot.)
- [x] Implement identity/ (BLAKE3-128 hash, deterministic daily salt `HMAC(master_secret, site_id || YYYY-MM-DD IRST)` — single master secret, site_id baked into HMAC input). **Cross-day fingerprint grace lookup** (doc 24 §Sec 1.1) — when the bloom filter misses, retry with yesterday's salt before declaring a new visitor. Closes the `user-enters-site-at-23:59` ghost-session bug.
- [ ] k6 load test: prove peak EPS targets with zero event loss — Phase 7. StreamCo P1/P2 envelope (web only) targets ~1,300 peak EPS; P3+ ramps to ~9K peak EPS / ~18K spike at the MAXIMUM envelope (see capacity-planning doc). 7K EPS is a mid-phase (P3–P4) checkpoint.
- [ ] Crash recovery test: kill -9 → WAL replay → verify zero loss — Phase 7
- [x] Integration test: emit bot event → verify visitor_hash populated AND is_bot=1 (enrichment order assertion) — `test/enrichment_e2e_test.go` covers Googlebot UA → is_bot=1 + non-zero visitor_hash
- [x] Integration test: prefetch header + oversized UA + UUID-as-UA + IP-as-UA → handler returns `204` with zero pipeline work (pre-pipeline fast-reject assertion) — `internal/ingest/handler_test.go` 10-case table
- [x] Integration test: visitor seen at 23:58 IRST returns at 00:02 IRST → identified as returning (cross-day fingerprint grace assertion) — `internal/identity/identity_test.go` salt rotation + `internal/enrich/newvisitor_test.go` cross-day grace

### Phase 2: Security Layer (Weeks 5–6)

- [x] TLS: **manual PEM loader only** — read `tls.cert_file` + `tls.key_file` from config, reload on SIGHUP for quarterly rotations. Autocert/LE slips to v1.1. (`internal/cert/loader.go` + `expiry.go` — atomic.Pointer hot-reload, fail-closed at boot, keep-old-on-reload-failure, expiry watcher with crossing-dedup at <30d/<7d.)
- [ ] Dashboard auth (bcrypt + `crypto/rand` sessions + SameSite=Lax cookies + RBAC) — Phase 2b (deferred until Phase 3 dashboard contract lands)
- [x] Rate limiting via `go-chi/httprate.LimitByRealIP` (100 req/s, NAT-aware) — `internal/ratelimit/ratelimit.go`. Keyed by the same `ingest.ClientIP` ladder the handler uses; emits `audit.ratelimit.exceeded` on 429. (Burst-as-separate-knob deferred — httprate uses a fixed window and the per-minute cap behaves as the burst.)
- [x] Input validation (`http.MaxBytesReader` 8KB, field limits, timestamp ±1h) — already shipped in Phase 1; verified preserved.
- [x] Hostname validation on `/api/event` (HMAC skipped entirely per doc 20) — already shipped in Phase 1; now also emits `audit.ingest.hostname_unknown` on miss.
- [x] Audit log (JSONL via slog, append-only, **file sink only**) — `internal/audit/log.go`. `O_APPEND` for atomic concurrent writes; `Reopen()` for logrotate; typed `EventName` constants.
- [ ] systemd hardening (NoNewPrivileges, ProtectSystem=strict, PrivateTmp, empty CapabilityBoundingSet) — Phase 2c (pairs with Phase 8 deploy)
- [ ] iptables rules (80/443/22 only; CH port 9000 never exposed) — Phase 2c
- [ ] LUKS setup procedure (documented, **optional** — evaluate 40–50% I/O overhead vs physical security) — Phase 2c
- [ ] Backup script (clickhouse-backup + age + zstd + cron + monthly restore test) — Phase 2c
- [x] Security assertions folded into `test/integration_test.go` (not a separate harness): httprate returns 429, hostname validation rejects, CH not reachable externally — `test/security_test.go` proves rate limit short-circuits before events reach ClickHouse. (Auth=403 deferred to Phase 2b.)

### Phase 3: Dashboard API (Weeks 7–9)

All 8 stats endpoints live in one file (`internal/dashboard/stats.go`) — they share date-parse, site_id scoping, cache key, and JSON shaping. Admin + billing are separate files since they mutate state. Query building lives in a **flat** `internal/storage/queries.go` (one Go function per endpoint) — we do NOT mirror Pirsch's 10 sub-analyzer split because our 8 endpoints don't warrant it (doc 24 §Sec 4 pattern 1 recommendation).

- [x] `internal/storage/store.go` — typed `Store` interface (doc 24 §Sec 4 pattern 3). One method per endpoint: `Overview(ctx, *Filter)`, `Sources(ctx, *Filter)`, etc. Enables Phase 7 integration-test mocking without a live ClickHouse.
- [x] `internal/storage/queries.go` — central `whereTimeAndTenant(*Filter, col) (string, []any)` helper that emits `WHERE site_id = ? AND <col> >= ? AND <col> < ?` as the first clause of every query (Architecture Rule 8 + doc 24 §Sec 4 pattern 6). Every endpoint SQL routes through this helper; a CI `tenancy-grep` gate rejects any `SELECT` in `internal/storage/queries.go` that doesn't call it (Makefile target + CI step).
- [x] `internal/storage/filter.go` — `Filter` struct with `SiteID uint32`, `From`/`To time.Time`, `Path`, `Referrer`, `UTM*`, `Country`, `Browser`, `OS`, `Device`, `Sort`, `Search`. Field names aligned with Pirsch; `ClientID → SiteID` is the only rename. Deterministic `Hash()` (BLAKE3-128, UTC-normalized) doubles as the cache key.
- [x] `stats.go` with 8 handlers (`GET /api/stats/...`): overview, sources, pages, geo, devices, funnel, campaigns, seo — `internal/dashboard/stats.go`. Geo/Devices/Funnel mounted but their Store methods return `ErrNotImplemented` → 501 (v1.1 / v2).
- [x] Time-series endpoints use **`WITH FILL … STEP INTERVAL`** for zero-result gap fill — SEO daily series in `internal/storage/queries.go`. Visitors-trend endpoint deferred until Phase 5 frontend asks for it (would need a new `Store.VisitorsTrend` method).
- [ ] POST/PUT/DELETE /api/admin/users (user + RBAC CRUD, admin-only) — Phase 3c (needs Phase 2b auth)
- [ ] POST/PUT/DELETE /api/admin/goals (goal CRUD, writes YAML + triggers SIGHUP hot reload) — Phase 3c
- [x] GET /api/realtime/visitors (10s cache, last-5-min active visitors — NOT full real-time) — `internal/dashboard/realtime.go`; cache TTL = `cache.TTLRealtime` (10s) via the CachedStore wrapping.
- [x] Date range handling — half-open intervals `[from, to)` at day granularity (doc 24 §Sec 4 pattern 7) enforced in `Filter.Validate()`. Asia/Tehran conversion stays at the API layer (Phase 3b).
- [x] LRU cache (realtime=10s, today=60s, yesterday=1h, historical=forever) — `internal/cache/{lru,policy}.go`. Per-entry TTL via `expiresAt` since the underlying `hashicorp/golang-lru/v2/expirable` only honors constructor-time TTL. `CachedStore` decorator in `internal/storage/cached_store.go`.
- [ ] Funnel endpoint uses **`windowFunnel()`** + 1h cache — v2 (Store.Funnel returns ErrNotImplemented in 3a)
- [ ] Dashboard query benchmark under 7K EPS load — Phase 7 hardening
- [x] Integration test: call every endpoint with `site_id=A` and `site_id=B`, assert zero row leakage across sites — `test/dashboard_isolation_test.go` seeds two sites with disjoint paths + referrers, verifies Overview/Sources/Pages/Campaigns for each, asserts Geo/Devices/Funnel return ErrNotImplemented.

### Phase 4: Tracker JS (Week 10)

- [ ] Build tracker from doc 20 source (~1.2KB minified / ~600B gzipped)
- [ ] Rollup + Terser build config
- [ ] Pageview + SPA (history API) + custom events + user_id + batching (outbound link tracking deferred to v1.1)
- [ ] Client-side bot hints: `navigator.webdriver`, `_phantom`, `evt.isTrusted` (Clarity pattern, doc 21)
- [ ] Server-side bot filtering: isbot + crawler-user-agents.json (primary; client is supplementary)
- [ ] Root-domain cookie walking (Clarity pattern, doc 21) — required for Filimo CDN subdomains
- [ ] Served via `go:embed` from the analytics host — first-party, no external CDN, no SRI needed
- [ ] Integration test: tracker → Go server → ClickHouse → verify rollups

**Deferred to v1.1:** engagement ping (10s heartbeat), throttle-with-last-event, base36 date encoding, envelope+payload separation. These power v2 session/engagement features — safe to defer until we build them.

### Phase 5: Dashboard Frontend (Weeks 11–13, reduced scope for v1 cut)

> **Brand reference:** all components use the tokens + type ramp from
> [§ Brand & Design](#brand--design--statnivelive-visual-identity). The Preact
> SPA imports `web/src/tokens.css` at the entry point so every panel /
> chart / table reads from `var(--green)`, `var(--ink)`, `var(--paper)`
> etc. Hand-rolled hex values in components are a PR-review reject.

- [ ] Preact SPA scaffold (Vite + TypeScript + @preact/signals for reactive state)
- [ ] Overview panel (summary cards)
- [ ] Visitors trend chart (uPlot, hourly/daily)
- [ ] Sources table (sortable, with revenue + conv%)
- [ ] Pages table (with goals + revenue)
- [ ] Funnel visualization (Frappe Charts bar)
- [ ] Geo panel (provinces table)
- [ ] Devices panel (device/browser/OS breakdown)
- [ ] SEO panel (organic trend line only — richer panels deferred to v1.1)
- [ ] Campaigns panel (utm_campaign table)
- [ ] Gregorian date picker with period shortcuts (Jalali = v1.1)
- [ ] Real-time active-visitors widget (10s refresh)
- [ ] Admin pages: users + goals + funnels (calls /api/admin/*)
- [ ] WCAG 2.2 AA compliance (contrast, focus rings, aria labels, keyboard reachability)
- [ ] Embed via go:embed, verify binary size <20MB

**Deferred to v1.1:** Jalali calendar, comparison period toggle (% change UI), CSV export on tables, keyboard shortcuts / command palette. Polish, not load-bearing for Filimo launch.

### Phase 6: Configuration & First-Run (Week 15)

- [x] YAML config loader (with hot reload for goals/funnels) — viper-based; goals/funnels CRUD ships in Phase 3
- [ ] First-run setup: create admin user, init ClickHouse schema — schema init done; admin user awaits Phase 2 auth
- [ ] Goal CRUD (YAML-based, add/remove without restart)
- [ ] Funnel CRUD (YAML-based)
- [x] Schema migration runner (embedded SQL, run on startup) — `internal/storage/migrate.go`
- [x] Health check endpoint (/healthz) — `internal/health/check.go`

### Phase 7: Testing & Hardening (Week 16 — tightened from 2 weeks)

- [x] k6 smoke load test — `test/perf/load.js` shipped in PR #14. 7K EPS ramp with Persian paths + Iranian UAs. Run via `make load-test`. (Per-phase EPS targets in [`../jaan-to/outputs/capacity-planning-standalone-analytics.md`](../jaan-to/outputs/capacity-planning-standalone-analytics.md): ~1,300 peak EPS for P1/P2; ~9K peak / ~18K spike for P3+.)
- [x] Go benchmark suite (every pipeline stage) — `internal/{ingest,enrich}/bench_test.go` shipped in PR #14. Baselines: BurstGuard ~50 ns/op, Channel ~280 ns, Bloom ~340 ns, UA ~300 ns, Bot ~500 ns, Handler full path ~4 µs.
- [ ] Integration test (100K events, multi-tenant → all v1 rollups → all API endpoints, each scoped by site_id; **security assertions folded in** — auth, rate limit, hostname validation, CH isolation, input limits) — Phase 7b, after auth lands.
- [x] Crash recovery test (kill -9 Go → WAL replay) — `test/perf/crash_recovery_test.go` shipped in PR #14. Currently logs ~80% loss because WAL Replay decodes only 1 of N entries after SIGKILL (tidwall NoSync window). **Phase 7b will fix the WAL contract; this test will then assert <0.05% loss.**
- [x] Disk-full policy test — `test/perf/disk_full_test.go` shipped in PR #14. Surfaced that the consumer drops events when CH is unreachable (fill_ratio stays 0.0). **Phase 7b will fix the buffer-on-outage contract; this test will then assert fill_ratio > 0.5 under pressure.**
- [ ] Backup restore test (restore encrypted backup to fresh CH → row counts match) — Phase 7b (needs Phase 2c backup script first).
- [ ] Manual TLS rotation test (replace PEMs + SIGHUP → new cert served without restart) — Phase 7b.
- [ ] Documentation: README, deployment guide, API docs, runbook — partial: `docs/runbook.md` (Phase 7a operator gates) shipped in PR #14. Full deployment guide + API docs land with Phase 7b + Phase 8.
- [x] CH outage buffer-and-drain test — `test/perf/ch_outage_test.go` shipped in PR #14 (10s in-test variant; 10-min variant documented in runbook for manual verification).

### Phase 8: Deployment & Launch (Weeks 17–18)

- [ ] Deploy to Hetzner CX32 (~€13/mo) for Phase A dogfood staging
- [ ] OR deploy to Iranian DC for Filimo (production)
- [ ] Build **offline install bundle** (`make airgap-bundle`): statically-linked binary + `vendor/` + migration SQL + default YAML + tracker bundle + IP2Location LITE DB23 BIN + SHA256SUMS + signed manifest. **Docker tarball deferred to v1.1.**
- [ ] Complete deployment runbook (bare metal, air-gapped bundle install)
- [ ] Backup cron verified + monthly restore drill scheduled
- [ ] Monitoring: health endpoint + **file-sink alerts** (JSONL in `/var/log/statnive/alerts.jsonl`). Alerts: WAL >80%, CH down, disk >85%, cert expiry <30d. Syslog/Telegram sinks = v1.1.
- [ ] Document offline GeoIP DB update procedure (SCP new BIN + SIGHUP)
- [ ] Document internal NTP requirement (IRST salt correctness depends on correct clock)
- [ ] Filimo tracker integration
- [ ] **Air-gapped acceptance test**: deploy bundle on a host with `iptables -P OUTPUT DROP` (loopback + tracker IPs only), run full integration suite
- [ ] v1 launch

### Phase 9: Dogfood on statnive.com (Weeks 19–20, Phase A of Launch Sequence)

- [ ] Provision **Hetzner CX32 cloud (~€13/mo)** as initial **Deployment D1**. statnive.com traffic is <100K PV/mo — AX42 is 400× over-provisioned for dogfood. Upgrade to AX42 when first ~10 SaaS customers sign up (Phase C ramp).
- [ ] DNS: A + AAAA records for `statnive.live` and `demo.statnive.live`
- [ ] TLS: manual PEM via `certbot certonly` on a separate host (or a throwaway LE cert) — cron `certbot renew` calls a script that copies PEMs to D1 + SIGHUPs the binary. No autocert integration in v1.
- [ ] IP2Location **LITE DB23** (free, attribution required) — good enough for Phase A dogfood. Upgrade to paid DB23 only for Filimo in Phase B.
- [ ] Seed `sites` table: `site_id=1, hostname='statnive.com'`
- [ ] Create shared viewer account `demo / demo-statnive` and internal admin account
- [ ] Login page exposes demo credentials inline + "Sign up for your own analytics" CTA
- [ ] Paste tracker snippet into `statnive-website/` Astro base layout: `<script src="https://statnive.live/tracker.js" defer></script>`
- [ ] Acceptance: 24h after tracker install, `demo.statnive.live` dashboard shows non-zero visitors; viewer cannot call `/api/admin/*`; all 8 `/api/stats/*` endpoints return data

### Phase 10: Filimo dedicated Iranian VPS (Weeks 21–24, Phase B of Launch Sequence)

**Onboarding sequence — app-by-app, not all-at-once.** Full StreamCo-class traffic (MAXIMUM envelope: 5M DAU / 200M events/day) requires a cluster. We enter the customer deployment with **web only** (MINIMUM envelope: ~200K DAU / 3M views/day — 30× smaller), then onboard iOS, Android, and TV apps across months 1–12 post-deployment. Hardware sizing evolves with the onboarding — see [`../jaan-to/outputs/capacity-planning-standalone-analytics.md`](../jaan-to/outputs/capacity-planning-standalone-analytics.md) § 0 for the canonical 5-phase plan.

**DNS & shutdown-resilience architecture (non-negotiable for Iranian deployment):** Single brand domain `statnive.live` with authoritative DNS split across an outside-Iran primary (Bunny DNS or ClouDNS) and an Iran-hosted secondary NSD on a cheap Asiatech VPS (AT-VPS-B1) fed by AXFR + NOTIFY. Parent-zone delegation lists both NS sets. Iranian resolvers reach the Iranian NS over NIN during international-internet shutdowns (currently day 48+ of the ongoing 2026-01-08 blackout as of this doc revision) and continue to resolve `statnive.live` → Iranian DC origin IP. Plus defensive `statnive.ir` registration via Pars.ir (EUR/USDT payment) parked at 301 → `statnive.live` as a break-glass for the rare case where `.live` is DPI-blocked by exact hostname. Replaces Cloudflare as the DNS management surface; `dns-management` skill no longer applies to Iranian domains. Canonical spec + operational playbook in [`../jaan-to/docs/research/26-iran-shutdown-dns-strategy.md`](../jaan-to/docs/research/26-iran-shutdown-dns-strategy.md). Recurring cost: ~$14/mo + $15/yr. Ships before P1 cutover — the Iranian NS must exist before the first byte of Iranian customer traffic flows.

Per-phase Iranian DC sizing target (Asiatech primary pick):

| Sub-phase | Scope | Max DAU | Max MAU | Max daily events | Max monthly events | Asiatech server | Price/mo |
|---|---|---|---|---|---|---|---|
| **P1** (cutover) [MIN] | Web views only | 200K | 1.4M | 3M | 75M | `AT-VPS-G2` | 27.9M Rial |
| **P2** (+1mo) | Web + curated interactions | 200K | 1.4M | 15M | 350M | `AT-VPS-G2` (carry over) | 27.9M Rial |
| **P3** (+3mo) | +iOS onboarding | ~1.45M | ~5.65M | 70M | 1.4B | `AT-VPS-A1` + BW upgrade ≥500 GB/mo | 63.5M Rial + BW |
| **P4** (+6mo) | +Android | ~3.45M | ~12.45M | 140M | 3B | Dedicated 16–32c/64–128GB/2TB NVMe + ≥1 TB/mo BW (outside VPS catalog) | quote |
| **P5** (+10mo) [MAX] | +TV apps + HA | 5M | 17M | 200M | 4B | Dedicated cluster 2–3× (32c/128GB/4TB NVMe) + unmetered BW | quote |

- [ ] **P1 cutover hardware:** start with **Asiatech G1 or G2 standard VPS** (~15–28M Rial/mo, 150 GB/mo bandwidth fits web-views-only). Postpone the "~8c/32GB/1TB NVMe dedicated" conversation to P3 when iOS joins and bandwidth upgrade becomes load-bearing.
- [ ] Negotiate P3+ quotes in parallel: Asiatech bandwidth upgrade (500 GB/mo, 1 TB/mo, unmetered tiers), plus dedicated server quotes from Asiatech / Shatel / Afranet / ParsPack — 8c/32GB/1TB NVMe through 32c/128GB/4TB NVMe
- [ ] Provision **Deployment D2** on Iranian DC (VPS for P1/P2, graduates to dedicated at P3)
- [ ] DNS: CNAME `filimo.statnive.live` → Iranian DC IP (Cloudflare proxy **OFF** — traffic must reach Iranian DC directly)
- [ ] Build offline install bundle via `make airgap-bundle`
- [ ] SCP bundle → Iranian DC, verify SHA256 + Ed25519 signature
- [ ] Run `deploy/airgap-install.sh`
- [ ] TLS: manual PEM files (issued from a throwaway LE cert or Filimo's internal CA), rotated quarterly by operator
- [ ] Upgrade to **paid IP2Location DB23** on D2 only (city accuracy matters for Filimo)
- [ ] Generate Ed25519 license JWT: `site_id=1, Customer="Filimo", MaxEventsDay=0, Features=["*"], ExpiresAt=+1y`; drop at `config/license.key`. Key stored in an age-encrypted file on an offline laptop (no HSM in v1).
- [ ] Config overrides: `audit.sink = "file"`, `license.phone_home = false`. Single-tenant (only site_id=1).
- [ ] Seed `sites` table with Filimo hostnames: `filimo.com`, `www.filimo.com`, + any CDN / video-delivery subdomains
- [ ] Create Filimo admin user; deliver password via secure channel (Signal / in-person / PGP)
- [ ] Filimo pastes `<script src="https://filimo.statnive.live/tracker.js" defer></script>` in their site template
- [ ] Root-domain cookie walking (Clarity pattern, doc 21) to cover CDN subdomains
- [ ] Acceptance: k6 7K EPS ramp (Persian URLs, Iranian UAs) passes p99 <500ms; full `iptables OUTPUT DROP` air-gapped acceptance from Phase 8 passes; Filimo smoke test confirms live traffic in dashboard within 1h; backup + restore drill succeeds

### Phase 11: International SaaS self-serve (Weeks 26–30, Phase C of Launch Sequence)

- [ ] Implement `POST /api/signup` (email + password + hostname → creates site + admin user)
- [ ] Implement `POST /api/admin/billing` (Polar.sh webhook — verify `X-Polar-Signature` HMAC-SHA256, handle `subscription.created` / `subscription.updated` / `subscription.canceled` only; plan→features mapping lives in Go code, not Polar Benefits; idempotent by event.id)
- [ ] **Path-based tenant routing** in `dashboard/router.go` — URL shape `app.statnive.live/s/<slug>/...`; middleware extracts `<slug>`, resolves to `site_id` via `internal/sites/sites.go`, scopes all `/api/stats/*` calls. No subdomain-per-tenant, no wildcard TLS beyond `*.statnive.live` apex+app, no cookie isolation gymnastics.
- [ ] `internal/sites/sites.go` — slug generation (`example.com` → `example-com`), uniqueness check, hostname blocklist, create/disable
- [ ] Signup guardrails: hostname DNS-resolvable, not on blocklist, unique in `sites` table, rate limit 5 signups/hour per IP
- [ ] Free tier quota: 10K PV/mo tracked via `daily_users` rollup (available once v1.1 adds that rollup); v1 falls back to a periodic `count(DISTINCT visitor_hash)` query over `hourly_visitors` for the current month; soft throttle on ingest above limit (still accept, tag events `quota_exceeded=1`), upsell banner in dashboard
- [ ] Polar.sh integration (Merchant of Record — Polar handles VAT / global tax). Create 4 Products (Starter, Growth, Business, Scale) × monthly+yearly Prices in Polar dashboard; `POST /api/billing/checkout` server endpoint creates a Polar checkout session via `POST https://api.polar.sh/v1/checkouts/` with `external_customer_id = site_id`, redirects user to `{url}` from response. **Customer Portal link + Polar Benefits model = v2** — v1 cancellations go through us (email support).
- [ ] Paid tiers unlock higher quota + goals/funnels CRUD (feature gate in Go code keyed by `sites.plan`)
- [ ] Onboarding page at `app.statnive.live/s/<slug>/onboarding` with copy-paste snippet + "I've installed the tracker — check now" button (no polling endpoint; user-triggered refresh)
- [ ] Email transactional flow (signup confirm, payment receipt, quota warnings) — opt-in per deployment via `email.enabled`
- [ ] Acceptance: fresh signup → tracker embed → first event visible in tenant dashboard in <5 min; cross-site isolation test (site A admin cannot query site B data via URL manipulation); Polar sandbox `subscription.created` webhook correctly updates `sites.plan`; webhook is idempotent (replay same event.id → no double-apply); signup rate limiter rejects 6th signup/hour from same IP

---

## License Management (Self-Hosted)

statnive-live is **not open-source**. Self-hosted customers need a license.

### v1 License System (Manual)
- License key = signed JWT containing: `{site_id, customer, expires, max_events_per_day, features[]}`
- Go binary checks license on startup: decode JWT, verify Ed25519 signature, check expiry
- License stored in `config/license.key` file
- **Manual activation**: admin generates license key via CLI tool, sends to customer
- No payment system integration yet — handle offline
- Unlicensed binary runs in "demo mode" (30-day trial, 10K events/day cap, watermark on dashboard). *Numbers are a starting point — revisit against Plausible/Fathom/PostHog trial policies (doc 14, doc 17) before public launch.*

### v2 License System (Automated)
- License server at `license.statnive.live`
- Periodic license validation — daily phone-home, **grace period 30 days offline** (Iran connectivity is fragile per doc 19; 7 days too aggressive)
- Phone-home payload is strictly `{site_id, events_day_count, version}` — no user, URL, event, IP, or referrer data transmitted (privacy + GDPR)
- Polar.sh integration for self-serve purchase (Merchant of Record — Polar absorbs global tax compliance, no per-country registration)
- Usage reporting (anonymous: events/day count only)

### License Key Structure
```go
type License struct {
    SiteID       string    `json:"site_id"`
    Customer     string    `json:"customer"`
    Plan         string    `json:"plan"`       // starter, growth, business
    MaxEventsDay int64     `json:"max_events"` // 0 = unlimited
    Features     []string  `json:"features"`   // ["funnels", "revenue", "seo"]
    IssuedAt     time.Time `json:"issued_at"`
    ExpiresAt    time.Time `json:"expires_at"`
}
```

Signed with Ed25519 (public key embedded in binary, private key kept by us).

**Key operations (v1 — simple):**
- Private key stored in an **age-encrypted file** on an offline laptop (no HSM, no hardware token — defer until we have 20+ licensed self-hosted customers)
- Single keypair for all of v1; if compromised, rotate and ship a new binary
- Compromise recovery: rotate keypair, re-issue tokens, ship binary with only the new key

HSM + yearly rotation SOP + public-key overlap window = v2 ceremony when license volume justifies the overhead.

---

## v2 Roadmap (Post-Launch, +8–12 weeks)

| Feature | Effort | Priority | Lands in |
|---------|--------|----------|----------|
| Sequential funnel (windowFunnel) | 2 weeks | High | v2 |
| Cohort / retention | 2 weeks | High | v2 |
| Filtering / drill-down | 2 weeks | High | v2 |
| Google Search Console integration | 2 weeks | High | v2 |
| Session tracking | 1 week | Medium | v2 |
| Entry / exit pages | 1 week | Medium | v2 |
| Engagement time / page gap (doc 17 #62) | 1 week | Medium | v2 |
| Telegram weekly reports | 1 week | Medium | v2 |
| Data export / CSV (promote to v1 if frontend time permits) | 1 week | Medium | v2 |
| Public REST API | 1 week | Low | v2 |
| **Operator CLI** (`statnive` subcommands — see CLI section) | 1 week | Medium | **v1.1** |
| **MCP server** (read-only analytics tools — see MCP section) | 2 weeks | High | **v2** |
| Microsoft Clarity integration (complementary, not replacement) | 1 day | Future | post-v2 |

---

## CLI & MCP — Operator + Agent Surfaces

The single binary already runs as the analytics daemon (`statnive-live` with no
arguments boots the HTTP server). v1.1 + v2 add two more entry points so
operators don't have to reach for `clickhouse-client` + raw SQL, and so AI
assistants can answer customer questions directly from rollup data.

Both surfaces share the existing `internal/storage` package — no parallel
data paths, no separate auth model. Air-gap rules from
[§ Air-Gapped / Isolated Deployment](#air-gapped--isolated-deployment) apply
unchanged: stdio MCP transport works on a fully isolated host; HTTP MCP is
opt-in and disabled by default.

### v1.1 — Operator CLI (`statnive` subcommands, ~1 week)

Same binary, sub-command dispatcher (`spf13/cobra`, Apache-2.0). Default
sub-command stays `serve` so existing systemd units / Dockerfiles don't
break — running the binary with no args is identical to `statnive serve`.

Every sub-command writes to stdout in JSON (`--output=json`, default) or
human-readable table (`--output=table`); exits non-zero on error.
`--config=path/to/statnive-live.yaml` overrides the default config search.

**Subcommands (alphabetical):**

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

**Why a CLI in v1.1, not v1:**

- v1's tiny operator surface (Phase 8 deploy → Phase 9 dogfood → Phase 10 Filimo)
  needs maybe 4–5 of these commands — `secret generate`, `license issue`,
  `sites add`, `migrate`, `doctor`. Those can ship as discrete shell scripts
  in `deploy/` for v1.
- A real CLI binds the operator UX to a single tool, which is the right shape
  for v1.1 once we have ≥1 self-hosted customer running their own ops.
- Cobra adds ~200 KB to the binary. Acceptable; binary is currently <20 MB.

**Skill + dep surface:** `golang-cli` skill (already installed). Deps:
`spf13/cobra` (Apache-2.0). No new transitive risk.

### v2 — MCP server (read-only analytics tools, ~2 weeks)

Implements the [Model Context Protocol](https://modelcontextprotocol.io) so
Claude / other MCP-aware clients can query analytics data via natural
language. Same binary; new sub-command:

```
statnive mcp serve --transport=stdio                    # default; air-gap-safe
statnive mcp serve --transport=http --listen=127.0.0.1:8081 --token=$TOKEN
```

Stdio transport works on a fully isolated host (operator pipes through
`claude mcp add statnive -- statnive mcp serve`). HTTP transport is opt-in,
requires Bearer token auth (same session token as the dashboard), and is
listed in [§ Opt-in external services](#opt-in-external-services-all-off-by-default-in-air-gapped-mode)
as `mcp.http.enabled = false` by default.

**v2 read-only tools (mapped 1:1 to dashboard API endpoints):**

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

**Tool argument shape (every tool):**

```jsonc
{
  "site_slug": "filimo-com",   // resolved server-side to site_id; auth must permit
  "range": "7d",               // 1h | 1d | 7d | 30d | "2026-04-01..2026-04-18"
  "filters": { ... }           // optional; matches the Filter struct from Phase 3
}
```

**Auth model:** every MCP request carries a Bearer token (HTTP) or is bound
to the operator's local file permissions (stdio). Token → `(user_id, role,
allowed_site_ids[])`. Tools enforce `WHERE site_id IN (...)` via the same
central `whereTimeAndTenant()` helper Phase 3 lands. Cross-tenant attempts
return `mcp.invalid_params`, not silent empty results.

**Deferred to post-v2:**

- **Write tools** — `statnive_create_goal`, `statnive_create_funnel`,
  `statnive_disable_site`. Require dashboard auth UX to absorb MCP-issued
  changes first; ship after v2 dashboard CRUD stabilizes.
- **`statnive_query` (sandboxed SELECT)** — operator-grade ad-hoc SQL with
  `WHERE site_id = ?` enforcement + a per-token query budget. Powerful but
  needs a query parser to enforce the tenancy clause; defer until a
  customer asks.
- **Anomaly detection tool** — `statnive_anomaly_detect(site, range)`. Needs
  a baseline model. Defer until we have ≥3 months of production rollups
  to seed.

**Dependency + license surface:**

- MCP Go SDK candidate: [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) (MIT — verify at v2 dep-add time)
- No new transitive AGPL risk
- Both stdio + HTTP transports are in-tree; no daemon-of-a-daemon

**Air-gap invariant (re-stated):** stdio MCP requires zero outbound. HTTP
MCP listens on `127.0.0.1` by default; operator must explicitly bind to
a routable address + open the firewall to expose it externally — the
binary won't dial out to register itself anywhere.

---

## Brand & Design — statnive.live Visual Identity

The canonical brand reference is the standalone HTML preview at
[`../jaan-to/outputs/detect/design/statnive-brand-guideline/statnive-live-brand-guidelines.html`](../jaan-to/outputs/detect/design/statnive-brand-guideline/statnive-live-brand-guidelines.html)
(open it in a browser to see the wordmark, summit logo, color swatches,
type ramp, component samples, and pattern library). Companion files in
the same directory: `Statnive Logo.html` (mark variants), `icons.jsx`
(SVG icon set), `design-canvas.jsx` (source for the preview).

**Where the brand applies.** Every public surface that carries the
statnive.live name:

- **statnive.live root** — marketing landing page (Phase 9 + Phase 11). Hero
  uses the summit mark + Persian Teal accent on cream; CTAs in serif.
- **demo.statnive.live** — public demo dashboard (Phase 9). Same surface as
  tenant dashboards but with the "Public demo" banner from Phase A.
- **app.statnive.live/s/&lt;slug&gt;** — tenant dashboards (Phase 11). The Preact
  SPA from Phase 5 owns the implementation; brand tokens shipped as CSS
  custom properties so per-tenant white-label (a v2 upsell) is a token
  swap, not a refactor.
- **filimo.statnive.live** — Filimo dedicated dashboard (Phase 10). Same
  brand by default; Filimo can request white-label co-branding under v2.
- **README + docs site** — when we eventually publish operator docs, they
  use the same palette + Fraunces wordmark.

**Tokens to ship as CSS custom properties** (copy verbatim into
`web/src/tokens.css` when Phase 5 starts):

```css
:root {
  /* Surface */
  --paper:     #F4EFE6;   /* primary background — cream */
  --ink:       #1A1916;   /* primary text + rules */
  --rule-soft: #C9C0AB;   /* dividers, table borders */

  /* Accent — Persian Teal (the .live mark color) */
  --green:     #00756A;
  --green-dk:  #004F48;   /* hover / pressed */
  --green-lt:  #9FCDC5;   /* tinted backgrounds, info pills */

  /* Secondary palette — chart series, status badges */
  --navy:      #1E3551;
  --ochre:     #B87B1A;
  --plum:      #5F3B6E;
  --rust:      #A84628;

  /* Type */
  --serif: 'Fraunces', 'Charter', Georgia, serif;
  --sans:  'IBM Plex Sans', system-ui, sans-serif;
  --mono:  'IBM Plex Mono', ui-monospace, monospace;
}
```

**Typography rules** (from the guideline's "Type Ramp" panel):

- **Fraunces (serif)** — wordmark, marketing headlines, dashboard panel
  titles. Italic for the `.live` suffix in the wordmark.
- **IBM Plex Sans** — UI body, table cells, form controls.
- **IBM Plex Mono** — numeric telemetry (visitor counts, RPV figures,
  latency p99), code samples, IDs, hashes. Mono is load-bearing for the
  "fast, smart" product positioning — numbers must align in tables.

**Logo + voice rules** (from the guideline's "Three tones do ninety percent
of the work" thesis):

- The summit mark (the angular peak with the Persian Teal apex dot) is
  **secondary** to the wordmark. Wordmark first; summit mark only as a
  favicon, app-tile icon, or condensed-space mark.
- Cream + ink + rule do most of the work. Persian Teal is the **only**
  accent; secondary palette colors (navy / ochre / plum / rust) are reserved
  for chart series, status badges, and category differentiation — never for
  primary UI chrome.
- Voice from the guideline blurb: terse, technical, confident. "Cream, ink,
  rule." not "Our brand uses three primary colors."

**Compliance hooks** (where this plan enforces brand consistency):

- **Phase 5 (Dashboard Frontend)** — `web/src/tokens.css` MUST originate from
  the swatch table above. PR review rejects hand-rolled hex values in
  Preact components; they must reference `var(--green)` etc.
- **Phase 9 (Phase A dogfood)** — when the tracker snippet is pasted into
  `statnive-website/`, the surrounding marketing copy + hero use the
  brand palette + Fraunces wordmark.
- **Phase 11 (Phase C SaaS)** — the per-tenant slug page reuses the same
  token file; per-tenant white-label is a v2 feature that swaps token
  values, never the structure.

**Source of truth rule:** if the brand guideline HTML and this section
disagree, the HTML wins. Update the HTML first, then port the relevant
token deltas back into PLAN.md.

---

## SaaS Model (statnive-live Cloud)

If offering as SaaS alongside self-hosted:

### Multi-Tenant Architecture
- Same binary, `site_id` on every raw + rollup row
- Row-level isolation via `WHERE site_id = ?` on all queries (no view-per-tenant)
- Per-site rate limiting + metering
- Shared ClickHouse (pool model) for <1000 tenants

### GDPR Compliance (Required for SaaS — hosted outside Iran)

AGPL Section 13 is not the only reason hosting outside Iran matters: **GDPR applies to any EU visitor on a SaaS-hosted customer site.** v1 SaaS must ship with:

- **Data Processing Agreement (DPA)** template signed with every paying customer
- **Consent banner** with Reject / Accept / Custom (ePrivacy compliant); when consent is declined we drop user_id + cookies and fall back to BLAKE3 hash of (ip+ua+site_secret+daily_salt)
- **User rights endpoint**: `GET /api/privacy/export?user_id=X`, `DELETE /api/privacy/erase?user_id=X` (WordPress-style privacy API, CASCADE through rollups)
- **Retention**: raw 90d default, rollups 2y default (configurable per site)
- **Sub-processor list**: Hetzner (Germany, IAAS), Let's Encrypt (certs) — published at statnive.live/privacy
- **Audit trail**: every admin access logged to append-only JSONL + shipped to external syslog

Iranian self-hosted deployments are exempt (no EU visitors / data stays on customer server).

### Pricing (Pageview-Based, Plausible-Compatible)

| Tier | Pageviews/mo | Price |
|------|-------------|-------|
| Free | 10K | $0 (self-hosted only) |
| Starter | 100K | $9/mo |
| Growth | 1M | $19/mo |
| Business | 10M | $69/mo |
| Scale | 100M | $199/mo |
| Enterprise | 1B+ | Custom |

### Infrastructure Cost per Customer (growth path)

- **Pre-paying-customers (dogfood):** Hetzner CX32 (~€13/mo) hosts statnive.com + a handful of free-tier trials. Fixed cost, no per-customer math yet.
- **First ~10 paying customers:** AX41 (~€39/mo) — comfortably handles 10–30 sites at 1M PV/mo each. **~€1.30–3.90/mo per customer**; ~90% gross margin at $19/mo pricing.
- **~30–50 customers:** AX42 (€46/mo) safely handles 30–50 sites at 1M PV/mo each. 100 sites × 1M PV/mo = ~13.5K EPS — above the 7K EPS proven load ceiling, so don't over-pack AX42.
- **100+ customers:** AX102 (€104/mo) or horizontal shard. Revisit architecture when we get there.

---

## Server Costs

| Stage | Server | Monthly | Annual |
|-------|--------|---------|--------|
| **Phase A dogfood (v1)** | Hetzner CX32 cloud (4c/8GB/80GB) | **~€13** | **~€156** |
| Phase C first paying tier (~10 customers) | Hetzner AX41 (6c/64GB/2×512GB) | **~€39** | **~€468** |
| Phase C growth (~50–100 customers) | Hetzner AX42 (8c/64GB/1TB) | **€46** | **€552** |
| Phase C scale (100+ customers) | Hetzner AX102 (16c/128GB/4TB) | **€104** | **€1,248** |
| Filimo (Phase B) | 8c/32GB/1TB NVMe Iranian DC (Asiatech / Shatel / Afranet) | **~€180** | **~€2,160** |

**Notes:**
- **Start small:** CX32 (~€13/mo) handles statnive.com dogfood traffic (<100K PV/mo) for ~400× less cost than AX42. Upgrade to AX42 when SaaS load demands it. Saves ~€430/yr in year 1.
- Iranian DCs are quote-based (not public pricing). Upfront CAPEX on custom bare-metal builds; monthly figure is colocation + bandwidth only.
- **Customer Iranian DC sizing is phase-dependent**, not a single number. P1/P2 (StreamCo MIN, web only) runs on an Asiatech G2 standard VPS (~28M Rial/mo). P3 (+iOS) needs bandwidth upgrade or small dedicated. P4/P5 (StreamCo MAX, full fidelity) is a 2–3 node cluster. See the 5-phase table in Phase 10 above and [`../jaan-to/outputs/capacity-planning-standalone-analytics.md`](../jaan-to/outputs/capacity-planning-standalone-analytics.md) for monthly bandwidth / disk / EPS per sub-phase.
- **Bandwidth envelope by sub-phase** (StreamCo profile, at 300 B/event optimized): P1 ~22 GB/mo (MIN), P2 ~105 GB/mo, P3 ~420 GB/mo, P4 ~900 GB/mo, P5 ~1.2 TB/mo (MAX). All Asiatech standard VPS tiers cap at 150 GB/mo — upgrade conversation lands at P3, not at initial cutover.
- IP2Location paid DB23 subscription only on D2 (Filimo) in v1. LITE DB23 on D1 (free, attribution required).

---

## Air-Gapped / Isolated Deployment

The final platform runs as a **single, self-contained binary on one server with zero required outbound connections**. This is a core product requirement, not an edge case — Filimo's Iranian DC is assumed internet-restricted, and enterprise self-hosted customers may deploy behind corporate firewalls.

### What ships inside the binary (via `go:embed`)

- Go server + all vendored dependencies (single statically-linked executable)
- ClickHouse schema + numbered migrations (embedded SQL)
- Preact SPA (compiled Vite bundle)
- Tracker JS (served first-party at `/t.js`)
- `crawler-user-agents.json` bot patterns
- Default `statnive-live.yaml` + `sources.yaml` (overridable at runtime)

### What ships next to the binary (offline install bundle, `make airgap-bundle`)

- `statnive-live` binary (`CGO_ENABLED=0` where possible — one file, no runtime deps)
- `vendor/` tarball (for buildable-from-source audits only; not required to run)
- `IP2LOCATION-LITE-DB23.BIN` (or licensed DB23 BIN for Filimo)
- `clickhouse-backup` + `age` binaries
- `schema.sql` + `migrations/`
- `deploy/` scripts (systemd, iptables, backup, airgap-install, airgap-update-geoip)
- `SHA256SUMS` + detached Ed25519 signature

**Docker tarball (`docker save`) deferred to v1.1** — static binary is one file, runs anywhere; Docker-based installs are a convenience layer that adds bundle size + CI time without unblocking any of the 5 goals. Revisit when an operator actually asks for it.

### Mandatory external services: **NONE**

### Opt-in external services (all OFF by default in air-gapped mode)

| Service | Purpose | Disable via config |
|---|---|---|
| Let's Encrypt (ACME) | TLS cert issuance | v1 uses manual PEM only — LE never called from the binary. Operator obtains certs separately via `certbot certonly --manual` and drops PEMs. |
| Telegram Bot API | Operator alerts | v1.1 only — v1 uses file sink (`/var/log/statnive/alerts.jsonl`) |
| `license.statnive.live` | SaaS license phone-home | `license.phone_home = false` (v1 default) |
| ip2location.com | Monthly GeoIP DB refresh | Never auto-fetched — always manual file drop |
| Remote syslog | Audit log shipping | v1.1 only — v1 uses file sink |
| Google Search Console (v2) | Organic SEO data | Feature flag off |
| Microsoft Clarity (future) | Heatmaps | Feature flag off |
| Polar.sh (SaaS Phase C only) | Billing (Merchant of Record), checkout sessions, webhooks at `api.polar.sh` | `billing.polar.enabled = false` (D2 always off) |
| Transactional email (SaaS Phase C only) | Signup confirm, receipts, quota alerts | `email.enabled = false` (D2 always off) |

### Install procedure (air-gapped host)

1. Transfer `statnive-live-<version>-airgap.tar.gz` via USB/SCP from a trusted bastion
2. Verify SHA256 + Ed25519 signature against public key on a separate channel
3. Run `deploy/airgap-install.sh` — provisions user, systemd unit, iptables (`OUTPUT DROP` except tracker clients + loopback; CH localhost-bound)
4. Place license JWT at `config/license.key`
5. Start service; first-run creates admin user, applies migrations
6. GeoIP updates: SCP new `IP2LOCATION-…BIN` monthly, run `deploy/airgap-update-geoip.sh` (atomic rename + `SIGHUP`)

### What stops working in air-gapped mode (acceptable)

- Automatic TLS renewal — operator rotates manual certs quarterly (file-sink alert when `<30d` remaining)
- Remote alerting (Telegram / syslog forwarding) — file sink only in v1; v1.1 adds optional remote sinks
- v2 license phone-home — pure offline JWT, grace treated as forever
- GSC / Clarity / auto-dep-updates — never required

### Prerequisites on the air-gapped host

- Linux kernel 5.x+, systemd, ClickHouse 24+ (also shipped in the bundle)
- **Internal NTP source** — IRST salt correctness depends on accurate clock
- Sufficient disk (plan ≥100 GB for WAL + CH data at 7K EPS for 90 days)
- Optional: internal CA + root cert distributed to tracker-embedding clients (for Filimo's corporate trust store)

---

## Launch Sequence

statnive-live ships in **three public-facing phases across two deployments**. Same binary, same schema; differences are config + DNS + hosting.

| Deployment | Host | Tenancy | Purpose | Phases |
|---|---|---|---|---|
| **D1 — `statnive.live` (SaaS)** | Hetzner CX32 (v1, ~€13/mo) → AX41/AX42 as paying customers arrive | Multi-tenant, pooled ClickHouse | Dogfood + public SaaS | A, C |
| **D2 — `filimo.statnive.live` (Dedicated)** | Iranian DC (Asiatech / Shatel / Afranet) | Single-tenant (`site_id=1` only), air-gapped | Filimo production | B |

### Routing strategy

- **Single tracker URL per deployment:** `https://statnive.live/tracker.js` (D1) and `https://filimo.statnive.live/tracker.js` (D2). Site-agnostic; `site_id` resolved server-side from `Origin` / `Referer` hostname against the `sites` table.
- **Path-based tenant routing in Phase C:** `app.statnive.live/s/<slug>/…` (e.g. `app.statnive.live/s/example-com/overview`). One TLS cert for `statnive.live` + `demo.statnive.live` + `app.statnive.live` + `filimo.statnive.live`; **no wildcard cert needed in v1**. Subdomain-per-tenant branding = v2 upsell.
- **Fixed dashboard hostnames:** `demo.statnive.live` (Phase A public demo), `filimo.statnive.live` (Phase B dedicated), `app.statnive.live` (Phase C tenant app).
- **Central signup + login:** `statnive.live/signup`, `statnive.live/app` → post-login redirect to `app.statnive.live/s/<slug>/overview`.

### Auth model per phase

| Phase | Who logs in | Role | Credentials source |
|---|---|---|---|
| A (demo) | Anyone | **viewer** (read stats only; no `/api/admin/*`, no CSV export, no audit log) | Shared `demo / demo-statnive`, displayed on login page |
| B (Filimo) | Filimo team | admin + viewer | Set at first-run, handed to Filimo via secure channel; rotatable via `/api/admin/users` |
| C (SaaS) | Registered site owner | admin of their own `site_id` only | Email + password, bcrypt + 14-day session (v1 security #6) |

### License strategy per phase

- **D1 (Phases A + C):** no JWT required — it's our own instance. Access gated by admin-user records, not license keys. Demo mode unused.
- **D2 (Phase B):** signed Ed25519 JWT at `config/license.key`: `{site_id:1, Customer:"Filimo", MaxEventsDay:0, Features:["*"], ExpiresAt:+1y}`. Offline — never phones home.

---

### Phase A — Dogfood on statnive.com (Weeks 20–21)

**Goal:** `statnive.com` → `statnive.live/tracker.js`; live dashboard at `demo.statnive.live` with shared viewer credentials so anyone can watch the live numbers.

- **Deployment:** D1 on Hetzner CX32 (~€13/mo) — statnive.com traffic fits comfortably; upgrade to AX41/AX42 when paying customers arrive in Phase C
- **DNS:** A + AAAA → D1 IP for `statnive.live`, `demo.statnive.live`, `app.statnive.live`
- **TLS:** manual PEM. Obtain LE cert via `certbot certonly --manual --preferred-challenges dns` on our laptop (one-time), drop PEMs on D1, renew quarterly via a cron that calls certbot and SIGHUPs the binary. No autocert in v1.
- **GeoIP:** IP2Location **LITE DB23** (free, attribution required)
- **Config diff from default:** `tls.cert_file` + `tls.key_file` set to PEMs; `license.required = false`
- **Seed SQL:** `INSERT INTO sites (site_id, hostname) VALUES (1, 'statnive.com');`
- **Seed users:** shared viewer `demo / demo-statnive`; internal admin for us
- **Login page:** displays demo credentials inline + "Sign up for your own analytics" CTA → Phase C signup
- **Tracker install:** `<script src="https://statnive.live/tracker.js" defer></script>` in `statnive-website/` Astro base layout
- **Rate limiting:** login attempts capped at 10/min per IP to prevent brute force on the shared demo password
- **Banner in dashboard:** "Public demo — statnive.com traffic — viewer role, no writes"
- **Acceptance:** within 24h of tracker install, dashboard shows non-zero visitors; viewer login gets 403 on any `/api/admin/*`; all 8 `/api/stats/*` endpoints return data scoped to `site_id=1`

### Phase B — Filimo dedicated Iranian VPS (Weeks 22–25)

**Goal:** `filimo.statnive.live` runs on an Iranian DC, Filimo team logs in with admin credentials, tracker is `filimo.statnive.live/tracker.js`. Fully secure, max performance, air-gapped-capable. **Cutover scope = Filimo web traffic only** (P1 of the app-by-app onboarding plan — ~200K DAU / 3M views/day).

- **Deployment:** D2 — Iranian DC. **Initial cutover on an Asiatech G1/G2 standard VPS** (~15–28M Rial/mo, 150 GB/mo bandwidth fits web-only traffic). Graduates to dedicated bare-metal at P3 (+iOS, ~3 months post-cutover) when bandwidth need crosses 150 GB/mo. See Phase 10 sub-phase table above for hardware progression.
- **Hardware progression:** VPS for P1/P2, dedicated bare-metal for P3/P4, cluster for P5. Quotes negotiated in parallel: Asiatech (all tiers + bandwidth upgrades), Shatel, Afranet, ParsPack.
- **Install:** offline bundle from Phase 8 (`make airgap-bundle`) — SCP tarball via bastion, verify `SHA256SUMS` + Ed25519 signature, run `deploy/airgap-install.sh`
- **DNS:** `CNAME filimo.statnive.live → <Iranian-DC-IP>`; Cloudflare proxy **OFF** (traffic must terminate inside Iran)
- **TLS:** manual PEM files only. Either Filimo's internal CA (preferred — cert already trusted by Filimo's client base) or a self-signed cert we generate with our root distributed once. Rotated quarterly by operator via config reload.
- **GeoIP:** upgrade to **paid IP2Location DB23** on D2 only — city accuracy matters for Filimo
- **License:** generate JWT with an Ed25519 private key stored in an age-encrypted file on our offline laptop (no HSM in v1) — `site_id=1, Customer="Filimo", MaxEventsDay=0, Features=["*"], ExpiresAt=+1y` — drop at `config/license.key`
- **Config overrides:**
  - `tls.cert_file` / `tls.key_file` set to manual PEMs
  - `audit.sink = "file"` (no remote log shipping)
  - `license.phone_home = false`
  - Single-tenant: only `site_id=1` provisioned in `sites` table
- **Seed:** `INSERT INTO sites VALUES (1, 'filimo.com'), (1, 'www.filimo.com'), (1, 'cdn.filimo.com'), …` — all Filimo-owned hostnames that might embed the tracker
- **Admin user:** password generated at first-run, delivered to Filimo via secure channel (Signal / in-person / PGP)
- **Tracker install (on Filimo side):** `<script src="https://filimo.statnive.live/tracker.js" defer></script>` in their site template; root-domain cookie walking (Clarity pattern, doc 21) automatically covers all Filimo subdomains + CDN hosts
- **Firewall:** `iptables -P OUTPUT DROP` with explicit allows for: loopback, ClickHouse port (localhost only), tracker client IP ranges (if geofenced), DNS resolver, NTP
- **Acceptance (P1 cutover, StreamCo MIN envelope):** k6 ~1,300 peak EPS ramp (Persian URLs, Iranian UA strings) sustains p99 <500ms; full air-gapped acceptance test from Phase 8 verification passes end-to-end; customer web smoke test confirms live traffic in dashboard within 1h; monthly backup + restore drill succeeds. Higher-EPS gates (7K / 18K — StreamCo MAX envelope) deferred to P3 / P5 graduation reviews.

### Phase C — International SaaS self-serve (Weeks 25–29)

**Goal:** anyone registers at `statnive.live`, gets their dashboard at `app.statnive.live/s/<slug>`, pastes a one-liner tracker snippet.

- **Deployment:** D1 (continues Phase A instance; upgrade CX32 → AX41 when ~10 paying customers sign up)
- **New endpoints (on top of v1):**
  - `POST /api/signup` — `{email, password, hostname}` → creates `site_id`, admin user, returns redirect to `app.statnive.live/s/<slug>/onboarding`
  - `POST /api/billing/checkout` — creates a Polar checkout session, returns `{url}` for redirect
  - `POST /api/admin/billing` — Polar.sh webhook (v1 handles `subscription.created` / `subscription.updated` / `subscription.canceled` only); verify `X-Polar-Signature` HMAC-SHA256; idempotent by `event.id`; reconcile via `customer.external_id = site_id`
- **Path-based tenant routing** (chi middleware in `router.go`):
  - Parse `/s/<slug>/` prefix → resolve to `site_id` via `internal/sites/sites.go`
  - Inject `site_id` into request context; all `/api/stats/*` handlers read from context
  - Missing or unknown slug → 404 / redirect to `statnive.live/app` (root login)
- **Signup guardrails:**
  - Hostname must DNS-resolve (simple A/AAAA lookup)
  - Hostname not on blocklist (spam/phishing lists, known typosquats)
  - Unique in `sites` table (first-come-first-served for hostname)
  - Rate limit 5 signups/hour per IP
  - Email verification link before tracker is activated (24h grace)
- **Free tier quota:** 10K PV/mo. v1 tracks via monthly `count(DISTINCT visitor_hash)` on `hourly_visitors`; v1.1 switches to a dedicated `daily_users` rollup. Over-quota = soft throttle (still accept events, tag `quota_exceeded=1`, show upsell banner).
- **Polar.sh products** (one Polar Product per tier; two Prices each — monthly + yearly):
  - Free (self-hosted only, no SaaS — no Polar Product)
  - Starter $9/mo → 100K PV + 5 goals
  - Growth $19/mo → 1M PV + unlimited goals + funnels CRUD
  - Business $69/mo → 10M PV + API access
  - Scale $199/mo → 100M PV + priority support
- **Why Polar (not Stripe):** Polar is a **Merchant of Record** — handles VAT/GST/sales tax globally so we don't register in every jurisdiction. Open-source, ~4% fee including tax. Cached docs at [`jaan-to/outputs/dev/docs/context7/polar-sh.md`](../jaan-to/outputs/dev/docs/context7/polar-sh.md).
- **v1 Polar scope = checkout + webhook only.** Customer Portal magic-link + `benefit.granted`/`benefit.revoked` feature-flag plumbing = v2. v1 hardcodes plan → features in Go; plan changes ship as releases. Cancellations go through email support in v1.
- **No Go SDK** — call Polar REST directly via `net/http` (or `go-resty` if we want a thin wrapper). Sandbox: `sandbox-api.polar.sh` for CI integration tests.
- **Onboarding UX:** post-signup page shows tracker snippet + "I've installed the tracker — check now" button (user-triggered refresh, no polling endpoint)
- **Email transactional:** signup confirm, payment receipt, quota warnings — opt-in per deployment via `email.enabled`
- **Acceptance:** fresh signup → tracker embed → first event visible in tenant dashboard in <5 min; cross-tenant isolation (site A admin cannot query site B data even when URL-manipulating `/s/<other-slug>/...`); Polar sandbox `subscription.created` updates `sites.plan`, `subscription.canceled` reverts at period end; webhook is idempotent; signup rate limiter rejects 6th signup/hour per IP

---

## Key Files (Already Written)

All Go code from doc 22 is ready to copy:

| File | Content | Source |
|------|---------|--------|
| main.go | Complete wiring + shutdown | Doc 22 bonus |

### License Compliance (Critical for SaaS outside Iran)

All dependencies must be permissive (MIT/Apache/BSD/ISC). Verified list:
- clickhouse-go/v2 — **Apache-2.0** ✓
- go-chi/chi — **MIT** ✓
- go-chi/httprate — **MIT** ✓
- tidwall/wal — **MIT** ✓
- ip2location-go/v9 — **MIT** ✓
- medama-io/go-useragent — **MIT** ✓
- omrilotan/isbot (server bot detection) — **MIT** ✓
- bits-and-blooms/bloom — **BSD-2** ✓
- lukechampine.com/blake3 — **MIT** ✓
- google/uuid — **BSD-3** ✓
- gopkg.in/yaml.v3 — **MIT** ✓
- filippo.io/age (backup encryption) — **BSD-3** ✓
- klauspost/compress (zstd) — **BSD-3** ✓
- golang.org/x/crypto/bcrypt — **BSD-3** ✓
- golang.org/x/crypto/acme/autocert — **BSD-3** ✓
- spf13/viper (optional config loader) — **MIT** ✓
- golang.org/x/* — **BSD-3** ✓
- ⚠️ hashicorp/golang-lru — **MPL-2.0**. Weak copyleft — OK to **use** in SaaS without disclosure, but if we modify golang-lru's own source we must publish those changes. **Decision:** use unmodified; if a feature is missing, fork to a separate repo, not inline. Consider switching to `github.com/hashicorp/golang-lru/v2` or an MIT-licensed alternative (`dgraph-io/ristretto`, MIT) if SaaS legal prefers zero ambiguity.
- ⚠️ knadh/koanf — **AGPL-3.0** ❌ DO NOT USE. Use `spf13/viper` (MIT) or env-only config.
- ⚠️ pirsch-analytics/pirsch — **AGPL-3.0** ❌ DO NOT IMPORT. Reference patterns only.
- **License verification is mandatory**: `go-licenses check ./...` must pass in CI on every PR.

| pipeline.go | 6-worker enrichment | Doc 22 GAP 1 |
| handler.go | HTTP handler + JSON array | Doc 22 GAP 1+3 |
| consumer.go | Batch writer + WAL ack | Doc 22 GAP 5 |
| wal.go | WAL + fsync + size cap | Doc 22 GAP 5 |
| clickhouse.go | 34-col batch insert (incl. site_id) + DLQ | Doc 22 GAP 4 |
| channel.go | Referrer mapper + SIGHUP | Doc 22 GAP 2 |
| sources.yaml | 50+ Iranian sources | Doc 22 GAP 2 |
| geoip.go | IP2Location DB23 | Doc 22 GAP 8 |
| ua.go | Medama UA parser | Doc 22 GAP 9 |
| bot.go | Bot detection | Doc 22 GAP 7 |
| newvisitor.go | Bloom filter | Doc 22 GAP 9 |
| salt.go | IRST salt rotation | Doc 22 GAP 6 |
| hash.go | BLAKE3-128 | Doc 22 GAP 9 |
| check.go | Health endpoint | Doc 22 GAP 10 |

---

## Technology Docs Cache (Context7, 2026-04-17)

Full cache of per-library docs lives at [`docs/tech-docs/`](docs/tech-docs/). Each file carries YAML frontmatter and distilled API snippets aligned to statnive-live's usage in PLAN.md.

### Plan decisions that originated from this cache

1. **Rate limiting**: Switch from `golang.org/x/time/rate` (manual) to `go-chi/httprate` (MIT, chi-native). Use `httprate.LimitByRealIP(100, time.Minute)` on `/api/event`. Handles NAT/proxy correctly.
2. **Preact signals**: Use `@preact/signals` instead of useState for dashboard state. Signals auto-update JSX without re-renders — better for real-time metric displays. Pass `{signal}` directly in JSX (not `{signal.value}`) to bind to DOM text nodes with zero re-renders.
3. **ClickHouse rollups**: Schema uses `AggregateFunction(uniqCombined64, FixedString(16))` — HyperLogLog approximation with ~0.5% error, ~2–3× lower memory than `uniqExact`. All rollup `ORDER BY` clauses lead with `site_id` for multi-tenant index pruning.
4. **Config loader**: `spf13/viper` fsnotify-based `WatchConfig` + `OnConfigChange` replaces the SIGHUP hot-reload mechanism noted elsewhere in this plan — SIGHUP is kept only as a manual fallback.
5. **LRU cache**: `hashicorp/golang-lru/v2` with `v2/expirable` for TTL semantics; generics-ready and MPL-2.0 (weak copyleft, use unmodified).

### Libraries cached (14)

| Library | Context7 ID | Cache file | Delta vs prior snapshot |
|---------|-------------|------------|-------------------------|
| clickhouse-go/v2 | `/clickhouse/clickhouse-go` | [clickhouse-go.md](docs/tech-docs/clickhouse-go.md) | None — `PrepareBatch → Append → Send` stable |
| go-chi/chi v5 | `/go-chi/docs` | [go-chi.md](docs/tech-docs/go-chi.md) | None. Security warning on `middleware.RealIP`: only register behind a trusted reverse proxy |
| go-chi/httprate | `/go-chi/httprate` | [httprate.md](docs/tech-docs/httprate.md) | None. `LimitByRealIP` vs `LimitByIP` choice depends on deployment topology |
| ClickHouse server | `/clickhouse/clickhouse-docs` | [clickhouse-server.md](docs/tech-docs/clickhouse-server.md) | None. AggregatingMergeTree + MV + `PARTITION BY toYYYYMM()` pattern confirmed |
| @preact/signals | `/preactjs/signals` | [preact-signals.md](docs/tech-docs/preact-signals.md) | None. Re-emphasised `{signal}` vs `{signal.value}` for zero-rerender DOM updates |
| uPlot | `/leeoniya/uplot` | [uplot.md](docs/tech-docs/uplot.md) | None. `uPlot.sync()` cursor sync confirmed for cross-panel hover |
| k6 | `/grafana/k6-docs` | [k6.md](docs/tech-docs/k6.md) | None. `maxVUs` anti-pattern warning — use `preAllocatedVUs` generously |
| spf13/viper (v1.20.1) | `/spf13/viper` | [viper.md](docs/tech-docs/viper.md) | None. fsnotify-based hot reload (`WatchConfig` + `OnConfigChange`) supersedes SIGHUP approach |
| ip2location-go/v9 | `/ip2location/ip2location-go` | [ip2location-go.md](docs/tech-docs/ip2location-go.md) | None. Use `Get_city` / `Get_country` over `Get_all` in hot path |
| medama-io/go-useragent | `/medama-io/go-useragent` | [go-useragent.md](docs/tech-docs/go-useragent.md) | None. Singleton `NewParser()` pattern mandatory |
| bits-and-blooms/bloom (v3) | `/bits-and-blooms/bloom` | [bloom.md](docs/tech-docs/bloom.md) | None. `NewWithEstimates(10M, 0.001) ≈ 18MB` matches PLAN budget |
| hashicorp/golang-lru (v2) | `/hashicorp/golang-lru` | [golang-lru.md](docs/tech-docs/golang-lru.md) | v2 import path: `github.com/hashicorp/golang-lru/v2` — generics-ready. Note MPL-2.0 weak-copyleft caveat in license section above |
| Vite | `/websites/vite_dev` | [vite.md](docs/tech-docs/vite.md) | **🔴 API DELTA** — see below |
| Vitest (v4) | `/vitest-dev/vitest` | [vitest.md](docs/tech-docs/vitest.md) | None. v4 GA confirmed; `vi.useFakeTimers` / `vi.setSystemTime` stable |
| Preact | `/preactjs/preact-www` | [preact.md](docs/tech-docs/preact.md) | None. `preact/hooks` + `@preact/signals` integration stable |

### Libraries NOT indexed in Context7

Documented in [_unindexed.md](docs/tech-docs/_unindexed.md) with direct pkg.go.dev / GitHub references.

- **tidwall/wal** — Context7 surfaced only tidwall's BuntDB / Rtree / Tile38 / Pogocache, not `wal`. The `Open / Write / Read / TruncateFront / Sync` API is stable; consult pkg.go.dev.
- **lukechampine.com/blake3** — only Rust / reference / .NET BLAKE3 ports are indexed. Go port API stable: `blake3.Sum256(data)` or `blake3.New(16, key)` for BLAKE3-128 keyed hashing.

### 🔴 API deltas since 2026-04-17 snapshot

Only **Vite** has notable deprecations. All other libraries verify clean against the snapshot at the previous section.

1. **Vite — `build.rollupOptions` → `build.rolldownOptions`.** Vite now bundles with **Rolldown** (Rust-based, rollup-compatible). `rollupOptions` still works as a deprecated alias. **Action:** author `web/vite.config.ts` with `rolldownOptions` from day 1. Reference: https://rolldown.rs/reference/
2. **Vite — JSX config moved from `esbuild.*` to `oxc.jsx.*`.** Preact importSource is now configured via `oxc: { jsx: { importSource: 'preact' } }`. The older `esbuild.jsxImportSource` is no longer the canonical path.
3. **hashicorp/golang-lru — v2 is the current line.** Import as `github.com/hashicorp/golang-lru/v2` and `…/v2/expirable`. v1 is legacy.
4. **Vitest — v4.0.7 is current** (v3.2.4 also indexed). No breaking changes for our planned usage.

All existing architectural decisions in the plan (schema, identity, transport, pipeline, license strategy) remain valid. The only concrete pre-Phase-0 code touch-up is the `vite.config.ts` option names.

---

## Verification

1. `go build ./cmd/statnive-live` compiles without errors
2. `make test` passes (unit + integration)
3. `go-licenses check ./...` passes — zero AGPL / strong-copyleft deps
4. k6 load test sustains 7K EPS with p99 <500ms
5. All dashboard endpoints (8 stats + 2 admin + 1 realtime) return correct data, all scoped by `WHERE site_id = ?`
6. Multi-tenant isolation: events for site_id=A are invisible in site_id=B queries
7. Enrichment order asserted: emit bot event → visitor_hash populated AND is_bot=1
8. Security: auth required, httprate returns 429, TLS 1.3 only, CH bound to 127.0.0.1, hostname validation rejects foreign Origin
9. Crash recovery: kill -9 Go → restart → zero event loss (WAL replay)
10. ClickHouse outage: stop CH 10 min → events buffer to WAL → resume → zero loss
11. Disk-full policy: fill WAL to 10GB cap → 503 with clear error, existing events preserved
12. Backup: restore encrypted backup to fresh CH → row counts match exactly
13. TLS rotation: replace PEM files + SIGHUP → new certificate served without binary restart; cert-expiry alert fires at <30 days
14. Tracker: install on test page → events appear in dashboard within 1 hour
15. GDPR (SaaS only): consent decline drops cookies + user_id; `/api/privacy/erase` removes visitor across raw + all v1 rollups (3 now, 6 after v1.1)
16. License: demo-mode binary caps at 10K events/day; valid JWT unlocks; expired JWT falls back to demo-mode with warning
17. **Air-gapped acceptance**: deploy offline bundle on host with `iptables -P OUTPUT DROP` (loopback + tracker IPs only). Binary starts, migrations apply, events ingest end-to-end, rollups materialize, dashboard renders, backup + restore succeed — all with zero outbound traffic
18. **Offline build**: `go build -mod=vendor ./...` succeeds with `GOFLAGS=-mod=vendor` and no network access
19. Manual TLS: binary serves traffic with `tls.cert_file` / `tls.key_file` pointing at internal-CA-issued PEMs; no autocert code path exercised (v1)
20. Air-gapped GeoIP update: replace DB23 BIN + `SIGHUP` → new IPs resolve correctly without restart
21. **Pre-pipeline fast-reject** (doc 24 §Sec 1.6): handler returns `204` on `X-Purpose: prefetch`, UA length < 16 or > 500, UA-as-IP, UA-as-UUID, non-ASCII UA — asserted with zero pipeline work (no bloom, no GeoIP, no batch write)
22. **Cross-day fingerprint grace** (doc 24 §Sec 1.1): visitor hashed at 23:58 IRST with salt S₁ returns at 00:02 IRST — identified as *returning* via yesterday-salt lookup, not as a new visitor
23. **Bot detection ordering** (doc 24 §Sec 1.3): integration test emits malformed UA, prefetch header, spam referrer, outdated Chrome, and regex-match bot — each short-circuits at the expected layer; `bot_reason` column (v1.1) records which layer fired
24. **Central tenancy helper** (Architecture Rule 8): CI lint asserts every `SELECT` in `internal/storage/` calls `whereTimeAndTenant()`; test fails if any new file bypasses the helper
25. **Schema time column**: ClickHouse asserts `time` is `DateTime('UTC')` (not `DateTime64`) on `events_raw` and all rollups
26. **Templated migration DDL** (doc 24 §Sec 2 Migration 0029): every `CREATE TABLE` migration is authored with `{{if .Cluster}}` placeholders; template renders correctly for both single-node (current) and `ReplicatedMergeTree` + `Distributed` (SaaS future) modes
27. **No Nullable columns** (Architecture Rule 5): CI lint asserts no `Nullable(` appears anywhere in `clickhouse/` or `internal/storage/migrate.go`
28. **Hostname-list lookup shape** (doc 24 §Sec 3.5): channel mapper benchmark confirms `map[string]struct{}` lookup, not `slices.Contains` — hot-path p99 stays below 50 ns/call
29. **AI channel present on day 1** (doc 24 §Sec 3.3): referrer from `chat.openai.com` / `claude.ai` / `gemini.google.com` / `copilot.microsoft.com` / `perplexity.ai` → `channel = "AI"`
30. **Day-of-week growth comparison** (v1.1, doc 24 §Sec 5 T2 #19): this-Tuesday-vs-last-Tuesday returns correct percentages — not this-Tuesday-vs-last-Monday
31. **Phase A (dogfood):** statnive.com fires a pageview → visible in `demo.statnive.live` dashboard within 5 minutes; shared viewer login (`demo / demo-statnive`) gets 403 on every `/api/admin/*` route; login brute-force capped at 10 attempts/min per IP
32. **Phase B (Filimo):** Filimo tracker at `filimo.statnive.live/tracker.js` fires → visible in `filimo.statnive.live` dashboard within 5 minutes; `iptables -P OUTPUT DROP` test passes end-to-end on Iranian DC box; backup + restore drill succeeds on the dedicated instance
33. **Phase C (SaaS):** fresh signup (`POST /api/signup`) → tracker embed → first event appears in `app.statnive.live/s/<slug>` within 5 minutes; cross-tenant isolation — site A admin cannot query site B data even by URL manipulation of `/s/<other-slug>/…`; Polar.sh sandbox webhook (`subscription.created` signed with `X-Polar-Signature`) updates `sites.plan` and quota enforcement flips correctly; webhook handler is idempotent (replaying the same event.id does not double-apply); 6th signup/hour from same IP is rejected
