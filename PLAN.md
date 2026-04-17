# statnive-live — Self-Hosted & SaaS Analytics Platform

## Context

8 research documents (docs 14–22), 400+ sources, and 2,000+ lines of drop-in Go code are complete. All architecture, features, schema, and security decisions are finalized.

**statnive-live** is the standalone analytics platform (separate from the WordPress plugin "statnive"). It targets Iranian high-traffic sites (10–20M DAU), with Filimo as first customer.

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

## CLAUDE.md (Create in repo root)

```markdown
# statnive-live

> **statnive.live** — High-performance, privacy-aware analytics for high-traffic websites.
> Self-hosted or SaaS. First customer: Filimo (10-20M DAU).

## Project Goals

1. **Security first** — data protection is #1 priority above all features
2. **Minimum cost, maximum performance** — 8 vCPU / 32 GB handles 200M events/day
3. **Generic platform** — business logic lives in custom events/goals/funnels, never hardcoded
4. **Multi-tenant from day 1** — site_id on all tables, SaaS-ready

## Stack

- **Backend:** Go 1.22+, single binary, go-chi router
- **Database:** ClickHouse (single node, MergeTree + 6 AggregatingMergeTree rollups)
- **Frontend:** Preact + uPlot + Frappe Charts (~50KB gzipped), embedded via go:embed
- **Tracker:** Vanilla JS <2KB, sendBeacon + fetch keepalive, text/plain
- **Identity:** Three layers — user_id (site sends) → cookie → BLAKE3 hash
- **Privacy:** No GDPR requirement for Iran; cookies + user_id allowed. Salt rotation at IRST midnight.

## Architecture Rules (Non-Negotiable)

1. **Raw table is WRITE-ONLY** — dashboard never queries events_raw (except funnels, cached 1h)
2. **All dashboard reads from rollups** — 6 materialized views, <200 KB/day total
3. **1-hour delay, NOT real-time** — saves 98% query cost. Never build 5-min real-time.
4. **Client-side batching in Go** — WAL for durability, batch 500ms/1000 rows, async inserts as safety valve only
5. **No Nullable columns** — use DEFAULT '' or DEFAULT 0. Nullable costs 10-30% on aggregations.

## License Rules (Critical)

- **ALL dependencies MUST be MIT/Apache/BSD/ISC** — no AGPL in the binary
- statnive-live is sold as SaaS outside Iran where AGPL Section 13 applies
- **DO NOT import pirsch-analytics/pirsch** (AGPL) — reference patterns only
- **DO NOT use knadh/koanf** (AGPL) — use viper (MIT) or env-only config
- Before adding any dependency, verify its license with `go-licenses`

## Security (12 Features, All v1)

1. TLS 1.3 on all endpoints (autocert + Let's Encrypt)
2. ClickHouse localhost only
3. Hostname validation on tracker endpoint (no HMAC — Plausible/Umami skip it too)
4. Input validation (MaxBytesReader 8KB, field length limits, timestamp ±1h)
5. Rate limiting per IP (100 req/s, burst 200, NAT-aware)
6. Dashboard auth (bcrypt + crypto/rand sessions, 14-day TTL)
7. RBAC (admin / viewer / API-only)
8. Encrypted backups (clickhouse-backup + age + zstd)
9. Disk encryption (LUKS, accept 40-50% I/O overhead)
10. Audit log (JSONL via slog, append-only)
11. User ID hashed before storage (SHA256 + site_secret)
12. systemd hardening (NoNewPrivileges, ProtectSystem=strict)

## Development Rules

- Run `make test` before every commit
- Run `make lint` (golangci-lint) before every PR
- Every new dependency requires license verification
- Every API endpoint must have an integration test
- ClickHouse schema changes go through migrations (embedded SQL, run on startup)
- Config changes to goals/funnels hot-reload via SIGHUP (no restart)

## Feature Scope

- **v1: 55 features** — security, identity, events/goals, funnels, revenue, attribution, SEO, content, audience
- **v2: 10 features** — sequential funnels, cohort, filtering, GSC, sessions, Telegram reports
- **Never: 3 features** — 5-min real-time, bounce rate, multi-touch attribution
- **Future: Microsoft Clarity** — free heatmaps + session recordings (their infra)
- See `docs/research/18-feature-decisions-summary.md` for complete list

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
| Integration | Go testing | `test/integration_test.go` |
| Load | k6 | `test/k6/load-test.js` |
| Security | Go testing | `test/security_test.go` |
| Frontend | Vitest | `web/src/**/*.test.tsx` |

## Research Documents

All architecture decisions are backed by research at:
`../statnive-workflow/jaan-to/docs/research/` (docs 14-22, 400+ sources)
```

## Repository Structure

```
statnive-live/                          # https://github.com/statnive/statnive.live.git
├── CLAUDE.md                           # Project rules (content above)
├── cmd/
│   └── statnive-live/
│       └── main.go                 # Entry point (wiring, shutdown — doc 22)
├── internal/
│   ├── config/                     # YAML config + hot reload
│   ├── ingest/
│   │   ├── event.go                # RawEvent + EnrichedEvent structs
│   │   ├── handler.go              # POST /api/event (JSON array parser)
│   │   ├── pipeline.go             # 6-worker enrichment pipeline
│   │   ├── consumer.go             # Batch writer (500ms / 1000 rows)
│   │   └── wal.go                  # WAL (tidwall/wal, 100ms fsync)
│   ├── enrich/
│   │   ├── channel.go              # Referrer → source/channel mapper
│   │   ├── geoip.go                # IP2Location DB23 wrapper
│   │   ├── ua.go                   # medama-io/go-useragent wrapper
│   │   ├── bot.go                  # Bot detection (isbot + crawler DB)
│   │   ├── newvisitor.go           # Bloom filter (18MB, 10M visitors)
│   │   └── crawler-user-agents.json # Embedded bot patterns
│   ├── identity/
│   │   ├── hash.go                 # BLAKE3-128 visitor hash
│   │   └── salt.go                 # IRST midnight salt rotation
│   ├── storage/
│   │   ├── clickhouse.go           # Batch insert (33 cols) + retry + DLQ
│   │   ├── queries.go              # Dashboard SQL (8 endpoints)
│   │   └── migrate.go              # Schema migrations (embed SQL)
│   ├── dashboard/
│   │   ├── router.go               # chi routes + auth middleware
│   │   ├── overview.go             # GET /api/stats/overview
│   │   ├── sources.go              # GET /api/stats/sources
│   │   ├── pages.go                # GET /api/stats/pages
│   │   ├── geo.go                  # GET /api/stats/geo
│   │   ├── devices.go              # GET /api/stats/devices
│   │   ├── funnel.go               # GET /api/stats/funnel
│   │   ├── campaigns.go            # GET /api/stats/campaigns
│   │   └── seo.go                  # GET /api/stats/seo
│   ├── auth/
│   │   ├── session.go              # bcrypt + session store (in-memory)
│   │   ├── middleware.go           # Auth + RBAC (admin/viewer/api)
│   │   └── audit.go                # JSONL audit logger
│   ├── cache/
│   │   └── lru.go                  # LRU with TTL (60s today, forever past)
│   └── health/
│       └── check.go                # /healthz (CH + WAL + disk + EPS)
├── web/
│   ├── src/                        # Preact SPA
│   │   ├── app.tsx
│   │   ├── pages/
│   │   │   ├── Overview.tsx
│   │   │   ├── Sources.tsx
│   │   │   ├── Pages.tsx
│   │   │   ├── Funnel.tsx
│   │   │   ├── Geo.tsx
│   │   │   ├── Devices.tsx
│   │   │   ├── SEO.tsx
│   │   │   └── Campaigns.tsx
│   │   └── components/
│   │       ├── Chart.tsx            # uPlot wrapper
│   │       ├── Table.tsx
│   │       ├── FunnelBar.tsx        # Frappe Charts
│   │       ├── DatePicker.tsx       # Jalali support
│   │       └── CompareToggle.tsx
│   ├── package.json
│   └── vite.config.ts
├── tracker/
│   ├── src/tracker.js              # <2KB tracker source
│   ├── rollup.config.js
│   └── package.json
├── clickhouse/
│   ├── schema.sql                  # events_raw + 6 rollups + MVs
│   └── migrations/
│       ├── 001_initial.sql
│       └── 002_add_revenue.sql
├── config/
│   ├── statnive-live.yaml          # Default config
│   └── sources.yaml                # 50+ Iranian referrer sources
├── deploy/
│   ├── statnive-live.service       # systemd unit (hardened)
│   ├── clickhouse-override.conf    # ClickHouse systemd override
│   ├── backup.sh                   # age + zstd + rotation
│   ├── iptables.sh                 # Firewall rules
│   └── docker-compose.yml          # Alternative deployment
├── test/
│   ├── k6/
│   │   └── load-test.js            # 7K EPS ramp test
│   ├── integration_test.go         # 100K events → verify rollups
│   └── security_test.go            # HMAC, rate limit, auth checks
├── Makefile                        # build, test, lint, release
├── Dockerfile                      # Multi-stage build
├── go.mod
├── go.sum
└── README.md
```

---

## Development Phases

### Phase 0: Project Setup (Week 1)

- [ ] Create `github.com/statnive/statnive-live` repository
- [ ] Initialize Go module, copy go.mod from doc 22
- [ ] Set up Makefile (build, test, lint, release targets)
- [ ] Create ClickHouse schema SQL (events_raw + 6 rollups from doc 20)
- [ ] Copy all Go files from doc 22 into project structure
- [ ] Set up CI (GitHub Actions: build + lint + test)
- [ ] Create config/sources.yaml (50+ Iranian sources from doc 22)
- [ ] Create config/statnive-live.yaml (default config from doc 20)

### Phase 1: Ingestion Pipeline (Weeks 2–4)

- [ ] Wire main.go (from doc 22 bonus code)
- [ ] Implement ingest/handler.go (JSON array parsing)
- [ ] Implement ingest/pipeline.go (6-worker enrichment)
- [ ] Implement ingest/consumer.go (batch writer)
- [ ] Implement ingest/wal.go (WAL + fsync + size cap)
- [ ] Implement storage/clickhouse.go (33-column batch insert)
- [ ] Implement enrich/ (GeoIP, UA, channel, bot, bloom)
- [ ] Implement identity/ (BLAKE3 hash, salt rotation)
- [ ] k6 load test: prove 7K EPS with zero event loss
- [ ] Crash recovery test: kill -9 → WAL replay → verify

### Phase 2: Security Layer (Weeks 5–6)

- [ ] TLS via autocert (Let's Encrypt)
- [ ] Dashboard auth (bcrypt + sessions + RBAC)
- [ ] Rate limiting (per-IP, 100 req/s, NAT-aware)
- [ ] Input validation (MaxBytesReader, field limits)
- [ ] Hostname validation (replaces HMAC)
- [ ] Audit log (JSONL, slog)
- [ ] systemd hardening (complete unit file)
- [ ] iptables rules (80/443/22 only)
- [ ] LUKS setup procedure (documented, optional)
- [ ] Backup script (clickhouse-backup + age + cron)
- [ ] Security test: verify all 12 features work

### Phase 3: Dashboard API (Weeks 7–9)

- [ ] GET /api/stats/overview (visitors, goals, revenue, conv%, RPV + comparison)
- [ ] GET /api/stats/sources (table: source, channel, visitors, goals, revenue, conv%)
- [ ] GET /api/stats/pages (table: pathname, visitors, views, goals, revenue)
- [ ] GET /api/stats/geo (provinces, cities, ISP)
- [ ] GET /api/stats/devices (device_type, browser, OS)
- [ ] GET /api/stats/funnel?id=X (count-per-step + by channel, cached 1h)
- [ ] GET /api/stats/campaigns (utm_campaign breakdown)
- [ ] GET /api/stats/seo (organic trend, pages, conv rate, organic vs paid)
- [ ] Date range handling (Asia/Tehran, comparison periods)
- [ ] LRU cache (60s today, forever past days)
- [ ] Dashboard query benchmark under 7K EPS load

### Phase 4: Tracker JS (Week 10)

- [ ] Build tracker from doc 20 source (~1.2KB minified)
- [ ] Rollup + Terser build config
- [ ] Pageview + SPA + outbound + custom events + user_id + batching
- [ ] Bot detection (navigator.webdriver)
- [ ] Integration test: tracker → Go server → ClickHouse → verify rollups

### Phase 5: Dashboard Frontend (Weeks 11–14)

- [ ] Preact SPA scaffold (Vite + TypeScript)
- [ ] Overview panel (summary cards + comparison %)
- [ ] Visitors trend chart (uPlot, hourly/daily)
- [ ] Sources table (sortable, with revenue + conv%)
- [ ] Pages table (with goals + revenue)
- [ ] Funnel visualization (Frappe Charts bar)
- [ ] Geo panel (provinces table or SVG map)
- [ ] Devices panel (device/browser/OS breakdown)
- [ ] SEO panel (organic trend, pages, organic vs paid)
- [ ] Campaigns panel (utm_campaign table)
- [ ] Date picker (Jalali via jalaali-js, period shortcuts)
- [ ] Comparison toggle (this period vs previous)
- [ ] Embed via go:embed, verify binary size <20MB

### Phase 6: Configuration & First-Run (Week 15)

- [ ] YAML config loader (with hot reload for goals/funnels)
- [ ] First-run setup: create admin user, init ClickHouse schema
- [ ] Goal CRUD (YAML-based, add/remove without restart)
- [ ] Funnel CRUD (YAML-based)
- [ ] Schema migration runner (embedded SQL, run on startup)
- [ ] Health check endpoint (/healthz)

### Phase 7: Testing & Hardening (Weeks 16–17)

- [ ] k6 full load test (7K EPS, Persian URLs, Iranian UAs)
- [ ] Go benchmark suite (every pipeline stage)
- [ ] Integration test (100K events → all rollups → all API endpoints)
- [ ] Security validation (auth, rate limit, TLS, ClickHouse isolation)
- [ ] Crash recovery test (kill Go, kill ClickHouse, verify recovery)
- [ ] Documentation: README, deployment guide, API docs

### Phase 8: Deployment & Launch (Weeks 18–19)

- [ ] Deploy to Hetzner AX42 (€46/mo) for staging
- [ ] OR deploy to Iranian DC for Filimo (production)
- [ ] Complete deployment runbook (bare metal or Docker)
- [ ] Backup cron verified
- [ ] Monitoring: health endpoint + Telegram alerts
- [ ] Filimo tracker integration
- [ ] v1 launch

---

## License Management (Self-Hosted)

statnive-live is **not open-source**. Self-hosted customers need a license.

### v1 License System (Manual)
- License key = signed JWT containing: `{site_id, customer, expires, max_events_per_day, features[]}`
- Go binary checks license on startup: decode JWT, verify signature, check expiry
- License stored in `config/license.key` file
- **Manual activation**: admin generates license key via CLI tool, sends to customer
- No payment system integration yet — handle offline
- Unlicensed binary runs in "demo mode" (30-day trial, 10K events/day cap, watermark on dashboard)

### v2 License System (Automated)
- License server at `license.statnive.live`
- Periodic license validation (daily phone-home, grace period 7 days offline)
- Stripe integration for self-serve purchase
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

---

## v2 Roadmap (Post-Launch, +8–12 weeks)

| Feature | Effort | Priority |
|---------|--------|----------|
| Sequential funnel (windowFunnel) | 2 weeks | High |
| Cohort / retention | 2 weeks | High |
| Filtering / drill-down | 2 weeks | High |
| Google Search Console integration | 2 weeks | High |
| Session tracking | 1 week | Medium |
| Entry / exit pages | 1 week | Medium |
| Telegram weekly reports | 1 week | Medium |
| Data export / CSV | 1 week | Medium |
| Public REST API | 1 week | Low |
| Microsoft Clarity integration | 1 day | Future |

---

## SaaS Model (statnive-live Cloud)

If offering as SaaS alongside self-hosted:

### Multi-Tenant Architecture
- Same binary, add `site_id` to all queries
- Row-level isolation via `WHERE site_id = ?` on all rollups
- Per-site rate limiting + metering
- Shared ClickHouse (pool model) for <1000 tenants

### Pricing (Pageview-Based, Plausible-Compatible)

| Tier | Pageviews/mo | Price |
|------|-------------|-------|
| Free | 10K | $0 (self-hosted only) |
| Starter | 100K | $9/mo |
| Growth | 1M | $19/mo |
| Business | 10M | $69/mo |
| Scale | 100M | $199/mo |
| Enterprise | 1B+ | Custom |

### Infrastructure Cost per Customer
- Hetzner AX42 (€46/mo) handles ~100 sites at 1M PV/mo each
- Cost per customer: ~€0.46/mo at 1M PV
- Gross margin: ~95% at $19/mo pricing

---

## Server Costs

| Phase | Server | Monthly | Annual |
|-------|--------|---------|--------|
| v1 Launch | Hetzner AX42 (8c/64GB/1TB) | **€46** | **€552** |
| v2 Growth | Hetzner AX52 (8c/64GB/2TB) | **€64** | **€768** |
| Scale | Hetzner AX102 (16c/128GB/4TB) | **€104** | **€1,248** |
| Iranian DC | 8c/32GB/1TB SSD | **~€180** | **~€2,160** |

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
- tidwall/wal — **MIT** ✓
- ip2location-go/v9 — **MIT** ✓
- medama-io/go-useragent — **MIT** ✓
- bits-and-blooms/bloom — **BSD-2** ✓
- lukechampine.com/blake3 — **MIT** ✓
- google/uuid — **BSD-3** ✓
- gopkg.in/yaml.v3 — **MIT** ✓
- hashicorp/golang-lru — **MPL-2.0** ✓ (weak copyleft, OK for SaaS)
- golang.org/x/* — **BSD-3** ✓
- ⚠️ knadh/koanf — **AGPL-3.0** ❌ DO NOT USE. Use viper (MIT) or env-only config.
- ⚠️ pirsch-analytics/pirsch — **AGPL-3.0** ❌ DO NOT IMPORT. Reference only.

| pipeline.go | 6-worker enrichment | Doc 22 GAP 1 |
| handler.go | HTTP handler + JSON array | Doc 22 GAP 1+3 |
| consumer.go | Batch writer + WAL ack | Doc 22 GAP 5 |
| wal.go | WAL + fsync + size cap | Doc 22 GAP 5 |
| clickhouse.go | 33-col batch insert + DLQ | Doc 22 GAP 4 |
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

## Technology Docs Fetched (Context7, 2026-04-17)

Docs resolved and reviewed for all key dependencies:

| Library | Context7 ID | Key Findings |
|---------|-------------|-------------|
| **clickhouse-go/v2** | `/clickhouse/clickhouse-go` | `PrepareBatch → Append → Send` confirmed. LZ4 compression via `Compression.Method`. `MaxOpenConns`, `ConnMaxLifetime`, `BlockBufferSize` all configurable. Supports `FixedString`, `Array(String)`, `DateTime64`, `Map` natively. |
| **go-chi/chi v5** | `/go-chi/docs` | `r.Group()` for public vs protected routes. `middleware.RealIP` for proxy IP extraction. JWT auth via `go-chi/jwtauth`. Mount sub-routers with `r.Mount()`. |
| **go-chi/httprate** | `/go-chi/httprate` | **Use this instead of raw x/time/rate.** `httprate.LimitByRealIP(100, time.Minute)` handles X-Forwarded-For/X-Real-IP automatically. Returns `429` with `X-RateLimit-*` headers. Sliding window counter pattern. |
| **ClickHouse server** | `/clickhouse/clickhouse-docs` | `AggregatingMergeTree` with `AggregateFunction(uniqExact, UInt64)` confirmed for rollups. `PARTITION BY toYYYYMM()`, `ORDER BY (bucket, dimension)`. Materialized views with `uniqState()`/`sumState()`. |
| **Preact** | `/preactjs/preact-www` | 3KB alternative to React. `@preact/signals` for reactive state (no useState needed). `useSignal()`, `useComputed()`, `useSignalEffect()`. Direct signal usage in JSX. `useState`/`useEffect` hooks also supported. |
| **k6** | `/grafana/k6-docs` | `ramping-arrival-rate` executor confirmed. `preAllocatedVUs` + `maxVUs` for burst handling. Scenario-specific thresholds via tags: `http_req_duration{scenario:X}`. `discardResponseBodies: true` for perf. |

### Plan Updates from Docs

1. **Rate limiting**: Switch from `golang.org/x/time/rate` (manual) to `go-chi/httprate` (MIT, chi-native). Use `httprate.LimitByRealIP(100, time.Minute)` on `/api/event`. Handles NAT/proxy correctly.
2. **Preact signals**: Use `@preact/signals` instead of useState for dashboard state. Signals auto-update JSX without re-renders — better for real-time metric displays.
3. **ClickHouse rollups**: Confirmed `AggregateFunction(uniqExact, UInt64)` pattern. Our schema uses `uniqCombined64` (HyperLogLog approximation, lighter) which is also supported.

---

## Verification

1. `go build ./cmd/statnive-live` compiles without errors
2. `make test` passes (unit + integration)
3. k6 load test sustains 7K EPS with p99 <500ms
4. All 8 dashboard endpoints return correct data
5. Security test: auth required, rate limiting works, ClickHouse not accessible externally
6. Crash recovery: kill -9 → restart → zero event loss (WAL replay)
7. Tracker: install on test page → events appear in dashboard within 1 hour
8. Backup: restore from encrypted backup → data intact
