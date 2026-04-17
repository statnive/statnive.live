# statnive-live — Self-Hosted & SaaS Analytics Platform

## Context

9 research documents (docs 14–22), 400+ sources, and 2,000+ lines of drop-in Go code are complete. All architecture, features, schema, and security decisions are finalized.

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
2. **Minimum cost, maximum performance** — 8c/32GB handles ~100–200M events/day with careful tuning (doc 19); 8c/64GB (Hetzner AX42) is the SaaS floor with headroom
3. **Generic platform** — business logic lives in custom events/goals/funnels, never hardcoded
4. **Multi-tenant from day 1** — `site_id` on all raw + rollup tables, `WHERE site_id = ?` on every query, SaaS-ready
5. **Self-contained & isolation-capable** — the final binary must run fully air-gapped on a single server with **zero required outbound connections**. All third-party services (Let's Encrypt, Telegram, license server, GeoIP updates, analytics CDNs) are **opt-in only**. Tested by running under `iptables -P OUTPUT DROP`.

## Stack

- **Backend:** Go 1.22+, single binary, go-chi router, go-chi/httprate for rate limiting
- **Database:** ClickHouse (single node, MergeTree + 6 AggregatingMergeTree rollups: `hourly_visitors`, `daily_pages`, `daily_sources`, `daily_geo`, `daily_devices`, `daily_users`) using `AggregateFunction(uniqCombined64, FixedString(16))` (HyperLogLog, 0.5% error, lower memory than `uniqExact`)
- **Frontend:** Preact + @preact/signals + uPlot + Frappe Charts (~50KB minified / ~15KB gzipped), embedded via go:embed
- **Tracker:** Vanilla JS ~1.2KB minified / ~600B gzipped (doc 20), sendBeacon + fetch keepalive, text/plain
- **Identity:** Three layers — user_id (site sends) → cookie → BLAKE3-128 hash; daily salt derived deterministically from master key + IRST date (`HMAC(master_key, site_id || YYYY-MM-DD IRST)`)
- **Privacy:** Iran = no GDPR; cookies + user_id allowed. **SaaS (hosted outside Iran) = GDPR applies to EU visitors** — customer DPA, consent banner, and user access/erasure rights required on SaaS tier. IRST = UTC+3:30, no DST since Sept 2022; store UTC in ClickHouse, convert at API layer only.

## Architecture Rules (Non-Negotiable)

1. **Raw table is WRITE-ONLY** — dashboard never queries `events_raw` (except funnels via `windowFunnel()`, cached 1h)
2. **All dashboard reads from rollups** — 6 materialized views, <200 KB/day total per site
3. **1-hour delay, NOT real-time** — saves 98% query cost. Never build 5-min real-time.
4. **Client-side batching in Go** — WAL for durability, batch 500ms / 1000 rows, async inserts as safety valve only
5. **No Nullable columns** — use `DEFAULT ''` or `DEFAULT 0`. Nullable costs 10–200% on aggregations depending on type (doc 20 measured 2× on `Nullable(Int8)`).
6. **Enrichment order is locked** — per event: identity (visitor_hash) → bloom filter → GeoIP → UA → bot detection → channel (doc 22 §GAP 1). Order is asserted in integration tests.

## License Rules (Critical)

- **ALL dependencies MUST be MIT/Apache/BSD/ISC** — no AGPL in the binary
- statnive-live is sold as SaaS outside Iran where AGPL Section 13 applies
- **DO NOT import pirsch-analytics/pirsch** (AGPL) — reference patterns only
- **DO NOT use knadh/koanf** (AGPL) — use viper (MIT) or env-only config
- Before adding any dependency, verify its license with `go-licenses`

## Security (13 Features, All v1)

1. TLS 1.3 on all endpoints. **Three modes, configurable:** (a) `autocert` + Let's Encrypt (default, internet-facing); (b) manual PEM files (`tls.cert_file`/`tls.key_file` — works air-gapped with internal CA); (c) internal-CA mode with custom root CA bundle for enterprise. Binary must start and serve traffic in modes (b) and (c) with zero outbound traffic.
2. ClickHouse localhost only (bound to 127.0.0.1, never exposed)
3. Hostname validation on `/api/event` (HMAC **skipped entirely** per doc 20 — hostname check is its own defense, not an HMAC replacement. Plausible/Umami do the same.)
4. Input validation (`http.MaxBytesReader` 8KB, field length limits, timestamp ±1h drift)
5. Rate limiting per IP via `go-chi/httprate` (100 req/s, burst 200, NAT-aware via X-Forwarded-For / X-Real-IP)
6. Dashboard auth (bcrypt + `crypto/rand` sessions, 14-day TTL, `SameSite=Lax` cookies for CSRF)
7. RBAC (admin / viewer / API-only). 2FA intentionally deferred to v2 (single-admin v1 with SSH-key-only server access).
8. Encrypted backups (`clickhouse-backup` + `age` + `zstd`, cron-scheduled, restore test on every release)
9. Disk encryption (LUKS **optional** — 40–50% I/O overhead is significant; physical DC security + encrypted backups may suffice per doc 20. Re-evaluate per deployment.)
10. Audit log (JSONL via `slog`, append-only). Sinks are pluggable: **file** (default, works air-gapped), **local syslog** (RFC 5424), or **remote syslog/webhook** (opt-in, SaaS only). No external shipping required in self-hosted mode.
11. User ID hashed before storage (SHA-256 + per-site secret, never log raw user_id)
12. systemd hardening (`NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`, `CapabilityBoundingSet=`)
13. Tracker served via `go:embed` from the analytics host — no external CDN, no SRI needed, first-party only (also bypasses ad-blockers)

## Isolation / Air-Gapped Capability (Non-Negotiable)

The final binary MUST run on a fully isolated server with zero required outbound connections. Every network-touching feature must be **optional and config-gated**.

| Capability | Default | Air-gapped mode | Fallback |
|---|---|---|---|
| TLS certificates | `autocert` (LE) | Manual PEM files / internal CA | Config: `tls.mode = "manual"` |
| License validation (v1) | Offline JWT verify | Offline JWT verify (same) | Always works offline |
| License phone-home (v2) | Opt-in per deployment | **Disabled** | Config: `license.phone_home = false` |
| GeoIP updates | Manual file drop | Manual file drop (same) | SCP/rsync the DB23 BIN file, SIGHUP to reload |
| Bot pattern updates | Embedded in binary | Embedded in binary (same) | Update via new release only |
| Monitoring alerts | Multi-sink | File + local syslog only | Telegram/webhook disabled |
| Tracker JS | `go:embed` (first-party) | `go:embed` (same) | No external CDN ever |
| Frontend SPA | `go:embed` | `go:embed` (same) | No external CDN ever |
| Dependencies | Vendored (`go mod vendor`) | Vendored (same) | No `go mod download` at runtime |
| Docker image | Pre-built multi-stage | `docker save` tarball | Transfer tarball via SCP |
| NTP | System NTP | Internal NTP server | Config required; IRST salt depends on correct date |

**Verification rule:** integration test runs the binary under `iptables -A OUTPUT -j DROP` (allowing only localhost + the configured tracker clients) and asserts all endpoints work, events ingest, rollups materialize, and dashboard renders.

## Development Rules

- Run `make test` before every commit
- Run `make lint` (golangci-lint) before every PR
- Every new dependency requires license verification
- Every API endpoint must have an integration test
- ClickHouse schema changes go through migrations (embedded SQL, run on startup)
- Config changes to goals/funnels hot-reload via SIGHUP (no restart)

## Feature Scope (complete enumeration — 55 v1 + 10 v2 + 1 Future)

Full list derived from research doc 18 (feature-decisions-summary) and doc 17 (feature-cost-decision-matrix). Every v1 row must exist in the shipped binary.

### v1 — 55 features

**Security (13):**
1. TLS 1.3 on all endpoints (three-mode: autocert / manual PEM / internal CA)
2. ClickHouse localhost-only (bound 127.0.0.1)
3. Hostname validation on `/api/event` (HMAC skipped per doc 20)
4. Input validation (`MaxBytesReader` 8KB, field limits, timestamp ±1h)
5. Rate limiting via `go-chi/httprate` (100 req/s, burst 200, NAT-aware)
6. Dashboard auth (bcrypt + `crypto/rand` sessions, 14-day TTL, `SameSite=Lax`)
7. RBAC (admin / viewer / api-only)
8. Encrypted backups (`clickhouse-backup` + `age` + `zstd`, cron + monthly restore test)
9. Disk encryption LUKS (optional; 40–50% I/O overhead trade-off)
10. Audit log (JSONL multi-sink: file / local syslog / opt-in remote)
11. User ID hashed before storage (SHA-256 + per-site secret)
12. systemd hardening (NoNewPrivileges, ProtectSystem=strict, PrivateTmp, empty CapabilityBoundingSet)
13. Tracker served via `go:embed` (first-party, ad-blocker-resistant, no SRI needed)

**Identity (3):**
14. user_id pass-through (site sends; hashed server-side)
15. Cookie fallback (httpOnly, SameSite=Lax, 1y max-age)
16. BLAKE3-128 hash fallback with daily IRST-derived salt (`HMAC(master_key, site_id || YYYY-MM-DD IRST)`)

**Events & Goals (4):**
17. Custom event API: `statnive.track(name, props, value)`
18. Goal YAML config (event → goal mapping, hot reload via SIGHUP)
19. Goal value column (UInt64 rials, `DEFAULT 0`, no Nullable)
20. Goal rate per channel / per page (aggregated in rollups)

**Funnels (2):**
21. Funnel YAML definition (ordered event steps)
22. Funnel report: count + drop-off % per step, 1h cache

**Revenue & CRO (7):**
23. Revenue sum per channel
24. Revenue sum per page
25. Revenue trend (daily / weekly)
26. Conversion rate per source
27. Conversion rate trend
28. Average value per conversion per channel
29. Revenue Per Visitor (RPV) per channel — **primary CRO metric**

**Attribution (5):**
30. UTM tracking (5 params: source, medium, campaign, content, term)
31. Auto source detection (referrer → named source via `sources.yaml`)
32. Channel grouping (Organic / Social / Direct / Paid / Email / Referral priority)
33. 50+ Iranian source database (Divar, Torob, Filimo, etc.)
34. Campaign report (breakdown by `utm_campaign`)

**SEO (5):**
35. Organic search traffic trend
36. Top landing pages from organic search
37. Organic conversion rate + revenue
38. Organic vs paid split
39. High-traffic / low-conversion pages

**Content & Trends (4):**
40. Top pages (by visitors, views, goals, revenue)
41. Visitors trend (hourly / daily)
42. New vs returning visitors (18MB bloom filter, 10M visitors, 0.1% FPR)
43. Comparison periods (this period vs previous, % change)

**Audience (4):**
44. Iranian provinces / cities (IP2Location DB23, ~84% city accuracy)
45. Device / browser / OS (`medama-io/go-useragent`, ~287 ns/op)
46. ISP / carrier (MCI, Irancell, Rightel, etc. via DB23)
47. User segments (custom properties sent with user_id)

**Infrastructure (6):**
48. Pageview tracking (`navigator.sendBeacon` + fetch keepalive)
49. SPA route tracking (pushState/replaceState patching + popstate)
50. Bot filtering (server: `omrilotan/isbot` + `crawler-user-agents.json`; client: `navigator.webdriver`, `evt.isTrusted`)
51. GeoIP at ingest (IP2Location `.BIN`, raw IP discarded after lookup)
52. UA parsing (Medama fast-path)
53. Hourly active-visitors widget (NOT 5-min real-time — rollup-based)

**Nice-to-have (2):**
54. Jalali calendar display (`jalaali-js` 3KB, client-side)
55. Outbound link tracking (click delegation + sendBeacon on external links)

### v2 — 10 features (post-launch, +8–12 weeks)

1. Sequential funnel (`windowFunnel`, 24h window)
2. Cohort / retention (first_seen cohort, weeks-later window)
3. Filtering / drill-down (extra `WHERE` on rollups, hash-keyed cache)
4. Google Search Console integration (OAuth2, keywords, position, CTR — 2–3d delay)
5. Session tracking (duration, pages/session, window functions)
6. Entry / exit pages (`first_value` / `last_value` per session)
7. Engagement time (page-gap between consecutive events per visitor)
8. Email + Telegram weekly reports (`robfig/cron`, Monday 9 AM IRST, Persian numerals)
9. CSV data export (`http.Flusher` chunked transfer, 1 export/hour rate limit)
10. Public REST API (Bearer token auth, rate limited, OpenAPI docs)

### Future (post-v2)

- **Microsoft Clarity integration** — free heatmaps + session recordings on Clarity's infra. Complementary (doc 21), not a replacement. Effort ~1 day.

### Never

- 5-minute real-time (rollup-based hourly is the line; breaks cost model)
- Bounce rate (vanity metric per research doc 09 / 14)
- Multi-touch attribution (last-touch channel grouping is the final answer)

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

## Claude Code Tooling (Skills + MCP)

Tooling strategy follows research doc 23. **Curate 5–8 skills max** — Claude Code's 30-skill discovery limit causes activation failures when overloaded. All listed skills verified MIT/Apache-2.0 (no AGPL in toolchain).

### Required skills (install at repo bootstrap)

| Skill | Purpose | Phases |
|-------|---------|--------|
| [`samber/cc-skills-golang`](https://github.com/samber/cc-skills-golang) (MIT, 1.3k★) | 25+ atomic Go skills: concurrency, database, context, error-handling, testing, benchmark, slog, testify | 0, 1, 3, 6, 7 |
| [`ClickHouse/agent-skills`](https://github.com/ClickHouse/agent-skills) (Apache-2.0) | 28 optimization rules + Architecture Advisor: primary keys, AggregatingMergeTree, MV patterns, batch inserts | 0, 1, 3, 6 |
| [`trailofbits/skills`](https://github.com/trailofbits/skills) | static-analysis, semgrep-rule-creator, differential-review, variant-analysis — security gate for Phase 2 | 2, 7 |
| [`darrenoakey/claude-skill-golang`](https://github.com/darrenoakey/claude-skill-golang) | Zero-fabrication testing, golangci-lint CI/CD gate enforcement | 0, 7 |
| [`The-Focus-AI/marina-skill`](https://github.com/The-Focus-AI/marina-skill) | Hetzner + Docker + Caddy + Cloudflare DNS deploy automation — `/marina-deploy` | 8 |

### MCP servers (runtime, not install-time)

| Server | Role | Install |
|--------|------|---------|
| [Altinity ClickHouse MCP](https://github.com/Altinity/altinity-mcp) | Production ClickHouse with OAuth/TLS + dynamic tools from views | Docker: `ghcr.io/altinity/altinity-mcp:latest` |
| [gopls MCP](https://go.dev/gopls/features/mcp) | Go linting, testing, coverage, **govulncheck** | Built into gopls |
| [Hetzner MCP](https://github.com/dkruyt/mcp-hetzner) | 60+ tools: provisioning, volumes, firewalls, DNS, snapshots | Source build |
| [Grafana MCP](https://github.com/grafana/mcp-grafana) | Dashboards, alerts, Prometheus/Loki for Phase 8 monitoring | Source build |

**Write-safety:** ClickHouse MCP is read-only by default. Writes require explicit `CLICKHOUSE_ALLOW_WRITE_ACCESS=true` — never set in production; route migrations through `storage/migrate.go` instead.

### Phase → skill/MCP mapping

| Phase | Primary Skills | MCP |
|-------|---------------|-----|
| 0. Setup | `cc-skills-golang`, `claude-skill-golang` | — |
| 1. Ingestion | `cc-skills-golang` (concurrency/context/database) + ClickHouse Agent Skills (batch insert, async) | Altinity MCP (schema validation) |
| 2. Security | `trailofbits/skills` (static-analysis, differential-review) | gopls MCP (govulncheck) |
| 3. Dashboard API | `cc-skills-golang` + ClickHouse Agent Skills (query optimization) | Altinity MCP (`run_query`) |
| 4. Tracker JS | **No skill coverage — build from scratch** (IIFE + sendBeacon + fetch keepalive + text/plain) | — |
| 5. Frontend | Preact + Vite + uPlot + Frappe + Jalali — **no dedicated skills; generate from Context7 cache at `docs/tech-docs/`** | — |
| 6. Config | `cc-skills-golang` (cli) + ClickHouse Agent Skills (schema) | Altinity MCP (schema discovery) |
| 7. Testing | `cc-skills-golang` (testing, benchmark, testify) + `claude-skill-golang` (CI gates) | gopls MCP |
| 8. Deploy | Marina Skill + Hetzner MCP | Grafana MCP |

### Gaps requiring custom work (no community skill exists)

Use libraries directly per the license table. Author a custom skill in `.claude/skills/` only if the gap recurs across phases.

| Gap | Approach |
|-----|----------|
| Vanilla JS <2KB tracker | Build by hand — no skill can replace domain-specific architecture |
| uPlot / Frappe Charts | Generate on demand from Context7 cache at `docs/tech-docs/` |
| Jalali calendar | Use `jalaali-js` (3KB, MIT) directly |
| WAL durability | Use `tidwall/wal` (MIT) directly; `cc-skills-golang` covers the concurrency patterns |
| BLAKE3 identity | Use `lukechampine.com/blake3` (MIT) directly |
| Iranian DC deploy | Manual runbook (IP geolocation, ISP BGP, payment restrictions) |

### Skill authoring rules (when filling a gap)

- SKILL.md ≤ 500 lines, split references into separate files
- Required frontmatter: `name`, `description`, `allowed-tools`, `effort`, `paths`
- Place at `.claude/skills/<name>/SKILL.md` in this repo
- Verify license before adding any new skill — **zero AGPL in the binary or toolchain**

## Research Documents

All architecture decisions are backed by research at:
`../statnive-workflow/jaan-to/docs/research/` (docs 14–23, 500+ sources). Doc 23 maps Claude Code skills + MCP servers to this 8-phase build.
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
│   │   ├── event.go                # RawEvent + EnrichedEvent structs (34 fields incl. site_id)
│   │   ├── handler.go              # POST /api/event (JSON array parser)
│   │   ├── pipeline.go             # 6-worker enrichment pipeline (order: identity→bloom→geo→ua→bot→channel)
│   │   ├── consumer.go             # Batch writer (500ms / 1000 rows) + DLQ on retry exhaustion
│   │   └── wal.go                  # WAL (tidwall/wal, 100ms fsync, 10GB size cap)
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
│   │   ├── clickhouse.go           # Batch insert (34 cols incl. site_id) + retry + DLQ
│   │   ├── queries.go              # Dashboard SQL (8 endpoints, all WHERE site_id=?)
│   │   └── migrate.go              # Numbered schema migrations, applied versions tracked in CH
│   ├── dashboard/
│   │   ├── router.go               # chi routes + auth middleware + httprate
│   │   ├── tenant.go               # Subdomain <slug>.statnive.live -> site_id middleware (Phase C)
│   │   ├── overview.go             # GET /api/stats/overview
│   │   ├── sources.go              # GET /api/stats/sources
│   │   ├── pages.go                # GET /api/stats/pages
│   │   ├── geo.go                  # GET /api/stats/geo
│   │   ├── devices.go              # GET /api/stats/devices
│   │   ├── funnel.go               # GET /api/stats/funnel
│   │   ├── campaigns.go            # GET /api/stats/campaigns
│   │   ├── seo.go                  # GET /api/stats/seo
│   │   ├── admin.go                # POST/PUT/DELETE /api/admin/users, /api/admin/goals (funnels via YAML+SIGHUP)
│   │   ├── signup.go               # POST /api/signup (Phase C self-serve)
│   │   ├── onboarding.go           # GET /api/stats/ping?site_id=X (Phase C onboarding polling)
│   │   └── billing.go              # POST /api/admin/billing (Stripe webhook, Phase C)
│   ├── sites/                       # Multi-tenant site registry (shared by ingest + dashboard)
│   │   ├── sites.go                # Sites table DAO: hostname <-> site_id resolution
│   │   └── provisioning.go         # Create / disable site, slug generation for subdomain routing
│   ├── auth/
│   │   ├── session.go              # bcrypt + session store (in-memory)
│   │   ├── middleware.go           # Auth + RBAC (admin/viewer/api)
│   │   └── audit.go                # JSONL audit logger
│   ├── cache/
│   │   └── lru.go                  # LRU — realtime=10s, today=60s, yesterday=1h, historical=forever (doc 20)
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
│   ├── iptables.sh                 # Firewall rules (default: OUTPUT DROP except tracker clients)
│   ├── airgap-install.sh           # One-shot offline installer from bundle
│   ├── airgap-update-geoip.sh      # Offline GeoIP DB rotation
│   └── docker-compose.yml          # Alternative deployment
├── vendor/                         # Vendored Go deps (go mod vendor) — checked in for offline builds
├── offline-bundle/                 # Release artifact: binary + vendor + docker tarballs + migrations + default configs + tracker + IP2Location DB23 + SHA256SUMS
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
- [ ] Set up Makefile (build, test, lint, release, **airgap-bundle** targets)
- [ ] Create ClickHouse schema SQL (events_raw + 6 rollups from doc 20)
- [ ] Copy all Go files from doc 22 into project structure
- [ ] Set up CI (GitHub Actions: build + lint + test + **`go mod vendor` check**)
- [ ] **Vendor all Go deps** (`go mod vendor`, commit to repo) — enables fully offline builds
- [ ] Create config/sources.yaml (50+ Iranian sources from doc 22)
- [ ] Create config/statnive-live.yaml (default config from doc 20)

### Phase 1: Ingestion Pipeline (Weeks 2–4)

- [ ] Wire main.go (from doc 22 bonus code)
- [ ] Add `SiteID` field to EnrichedEvent + populate in pipeline.processEvent() — required for multi-tenant from v1
- [ ] Implement ingest/handler.go (JSON array parsing; site_id resolved from hostname)
- [ ] Implement ingest/pipeline.go (6-worker enrichment; order **locked**: identity → bloom → geo → ua → bot → channel)
- [ ] Implement ingest/consumer.go (batch writer 500ms/1000 rows + retry + DLQ sink to disk)
- [ ] Implement ingest/wal.go (WAL + 100ms fsync + 10GB size cap; reject with 503 when >80% full)
- [ ] Implement storage/clickhouse.go (**34-column** batch insert incl. site_id)
- [ ] Implement storage/migrate.go (numbered migrations, applied versions tracked in CH system table)
- [ ] Implement enrich/ (GeoIP with IP2Location DB23, medama-io UA, channel mapper, isbot + crawler-user-agents.json, bloom 18MB/10M visitors/0.1% FPR)
- [ ] Implement identity/ (BLAKE3-128 hash, deterministic daily salt from master_key + IRST date)
- [ ] k6 load test: prove 7K EPS (Filimo baseline at 10–20M DAU per doc 16) with zero event loss
- [ ] Crash recovery test: kill -9 → WAL replay → verify zero loss
- [ ] Integration test: emit bot event → verify visitor_hash populated AND is_bot=1 (enrichment order assertion)

### Phase 2: Security Layer (Weeks 5–6)

- [ ] TLS: three-mode loader — (a) autocert + LE (default), (b) manual PEM files, (c) internal CA. Air-gapped deployments use (b) or (c) with zero outbound.
- [ ] DNS-01 fallback playbook for internet-connected Iranian DC deployments
- [ ] Dashboard auth (bcrypt + `crypto/rand` sessions + SameSite=Lax cookies + RBAC)
- [ ] Rate limiting via `go-chi/httprate.LimitByRealIP` (100 req/s, burst 200, NAT-aware)
- [ ] Input validation (`http.MaxBytesReader` 8KB, field limits, timestamp ±1h)
- [ ] Hostname validation on `/api/event` (HMAC skipped entirely per doc 20)
- [ ] Audit log (JSONL via slog, append-only)
- [ ] systemd hardening (NoNewPrivileges, ProtectSystem=strict, PrivateTmp, empty CapabilityBoundingSet)
- [ ] iptables rules (80/443/22 only; CH port 9000 never exposed)
- [ ] LUKS setup procedure (documented, **optional** — evaluate 40–50% I/O overhead vs physical security)
- [ ] Backup script (clickhouse-backup + age + zstd + cron + monthly restore test)
- [ ] Security test: verify all 13 features work end-to-end

### Phase 3: Dashboard API (Weeks 7–9)

- [ ] GET /api/stats/overview (visitors, goals, revenue, conv%, RPV + comparison)
- [ ] GET /api/stats/sources (table: source, channel, visitors, goals, revenue, conv%)
- [ ] GET /api/stats/pages (table: pathname, visitors, views, goals, revenue)
- [ ] GET /api/stats/geo (provinces, cities, ISP)
- [ ] GET /api/stats/devices (device_type, browser, OS)
- [ ] GET /api/stats/funnel?id=X (windowFunnel step counts + by channel, cached 1h)
- [ ] GET /api/stats/campaigns (utm_campaign breakdown)
- [ ] GET /api/stats/seo (organic trend, pages, conv rate, organic vs paid)
- [ ] POST/PUT/DELETE /api/admin/users (user + RBAC CRUD, admin-only)
- [ ] POST/PUT/DELETE /api/admin/goals (goal CRUD, writes YAML + triggers SIGHUP hot reload)
- [ ] GET /api/realtime/visitors (10s cache, last-5-min active visitors — NOT full real-time)
- [ ] Date range handling (Asia/Tehran UTC+3:30, no DST; store UTC, convert at API layer)
- [ ] LRU cache (realtime=10s, today=60s, yesterday=1h, historical=forever; invalidate on goal/config change)
- [ ] Dashboard query benchmark under 7K EPS load, all endpoints scoped by `WHERE site_id = ?`

### Phase 4: Tracker JS (Week 10)

- [ ] Build tracker from doc 20 source (~1.2KB minified / ~600B gzipped)
- [ ] Rollup + Terser build config
- [ ] Pageview + SPA (history API) + outbound links + custom events + user_id + batching
- [ ] Client-side bot hints: `navigator.webdriver`, `_phantom`, `evt.isTrusted` flag (Clarity pattern, doc 21)
- [ ] Server-side bot filtering: isbot + crawler-user-agents.json (primary; client is supplementary)
- [ ] Clarity-inspired patterns (doc 21): root-domain cookie walking, throttle-with-last-event, base36 date encoding, envelope + payload separation
- [ ] Engagement ping (10s heartbeat, visibility-aware) — feeds v2 session/engagement metrics
- [ ] Served via `go:embed` from the analytics host — first-party, no external CDN, no SRI needed
- [ ] Integration test: tracker → Go server → ClickHouse → verify rollups

### Phase 5: Dashboard Frontend (Weeks 11–14, assumes 1 dedicated FE; add 1 week buffer if shared with BE per doc 15)

- [ ] Preact SPA scaffold (Vite + TypeScript + @preact/signals for reactive state)
- [ ] Overview panel (summary cards + comparison %)
- [ ] Visitors trend chart (uPlot, hourly/daily)
- [ ] Sources table (sortable, with revenue + conv%)
- [ ] Pages table (with goals + revenue)
- [ ] Funnel visualization (Frappe Charts bar)
- [ ] Geo panel (provinces table or SVG map)
- [ ] Devices panel (device/browser/OS breakdown)
- [ ] SEO panel (organic trend, pages, organic vs paid)
- [ ] Campaigns panel (utm_campaign table)
- [ ] Date picker (Jalali via `jalaali-js` 3KB, period shortcuts)
- [ ] Comparison toggle (this period vs previous)
- [ ] Real-time active-visitors widget (10s refresh)
- [ ] Admin pages: users + goals + funnels (calls /api/admin/*)
- [ ] CSV export on all tables
- [ ] Keyboard shortcuts (j/k navigation, ? help, / search) + command palette
- [ ] WCAG 2.2 AA compliance (contrast, focus rings, aria labels, keyboard reachability)
- [ ] Embed via go:embed, verify binary size <20MB

### Phase 6: Configuration & First-Run (Week 15)

- [ ] YAML config loader (with hot reload for goals/funnels)
- [ ] First-run setup: create admin user, init ClickHouse schema
- [ ] Goal CRUD (YAML-based, add/remove without restart)
- [ ] Funnel CRUD (YAML-based)
- [ ] Schema migration runner (embedded SQL, run on startup)
- [ ] Health check endpoint (/healthz)

### Phase 7: Testing & Hardening (Weeks 16–17)

- [ ] k6 full load test (7K EPS ramp, Persian URLs, Iranian UAs) — 7K EPS = ~600M events/day, Filimo baseline at 10–20M DAU per doc 16
- [ ] Go benchmark suite (every pipeline stage)
- [ ] Integration test (100K events, multi-tenant → all rollups → all API endpoints, each scoped by site_id)
- [ ] Security validation (auth, rate limit, TLS, ClickHouse isolation, hostname validation, input limits)
- [ ] Crash recovery test (kill -9 Go → WAL replay zero-loss; kill ClickHouse for 10 min → events buffer then drain)
- [ ] Disk-full policy test (fill WAL to 10GB cap → verify 503 with clear error, existing events preserved)
- [ ] Backup restore test (restore encrypted backup to fresh CH → row counts match)
- [ ] TLS renewal test (ACME cert rotation 14+ days before expiry; HTTP-01 and DNS-01 paths)
- [ ] Documentation: README, deployment guide, API docs, runbook

### Phase 8: Deployment & Launch (Weeks 18–19)

- [ ] Deploy to Hetzner AX42 (€46/mo) for staging
- [ ] OR deploy to Iranian DC for Filimo (production)
- [ ] Build **offline install bundle** (`make airgap-bundle`): statically-linked binary + `vendor/` + docker image tarball (`docker save`) + migration SQL + default YAML + tracker bundle + IP2Location DB23 BIN + SHA256SUMS + signed manifest
- [ ] Complete deployment runbook (bare metal, Docker, **air-gapped bundle install**)
- [ ] Backup cron verified + monthly restore drill scheduled
- [ ] Monitoring: health endpoint + **multi-sink alerts** — file + local syslog (always-on, air-gapped-safe) + Telegram/webhook (opt-in, internet-facing only). Alerts: WAL >80%, CH down, DLQ non-empty, CH mutation queue backlog, disk >85%, cert expiry <14d
- [ ] DLQ inspection tooling (CLI to re-queue or discard)
- [ ] Document offline GeoIP DB update procedure (SCP new BIN + SIGHUP)
- [ ] Document internal NTP requirement (IRST salt correctness depends on correct clock)
- [ ] Filimo tracker integration
- [ ] **Air-gapped acceptance test**: deploy bundle on a host with `iptables -P OUTPUT DROP` (loopback + tracker IPs only), run full integration suite
- [ ] v1 launch

### Phase 9: Dogfood on statnive.com (Weeks 20–21, Phase A of Launch Sequence)

- [ ] Provision Hetzner AX42 as **Deployment D1** (€46/mo, Germany)
- [ ] DNS: A + AAAA records for `statnive.live` and `demo.statnive.live`
- [ ] ACME DNS-01 wildcard cert for `*.statnive.live` + apex `statnive.live`
- [ ] Seed `sites` table: `site_id=1, hostname='statnive.com'`
- [ ] Create shared viewer account `demo / demo-statnive` and internal admin account
- [ ] Login page exposes demo credentials inline + "Sign up for your own analytics" CTA
- [ ] Paste tracker snippet into `statnive-website/` Astro base layout: `<script src="https://statnive.live/tracker.js" defer></script>`
- [ ] Acceptance: 24h after tracker install, `demo.statnive.live` dashboard shows non-zero visitors; viewer cannot call `/api/admin/*`; all 8 `/api/stats/*` endpoints return data

### Phase 10: Filimo dedicated Iranian VPS (Weeks 22–25, Phase B of Launch Sequence)

- [ ] Negotiate Iranian DC quote: Asiatech / Shatel / Afranet — 8c/32GB/1TB NVMe, 1 Gbps uplink, co-hosted ClickHouse, ~€180/mo target
- [ ] Provision **Deployment D2** on Iranian DC bare metal
- [ ] DNS: CNAME `filimo.statnive.live` → Iranian DC IP (Cloudflare proxy **OFF** for this record — traffic must reach Iranian DC directly)
- [ ] Build offline install bundle via `make airgap-bundle`
- [ ] SCP bundle → Iranian DC, verify SHA256 + Ed25519 signature
- [ ] Run `deploy/airgap-install.sh`
- [ ] TLS: manual PEM files (plan A) or DNS-01 via Cloudflare API (plan B) or customer-provided internal CA (plan C)
- [ ] Generate Ed25519 license JWT: `site_id=1, Customer="Filimo", MaxEventsDay=0, Features=["*"], ExpiresAt=+1y`; drop at `config/license.key`
- [ ] Config overrides: `tls.mode = "manual"`, `alerts.sinks = ["file","syslog"]`, `license.phone_home = false`, `audit.remote = ""`
- [ ] Seed `sites` table with Filimo hostnames: `filimo.com`, `www.filimo.com`, + any CDN / video-delivery subdomains
- [ ] Create Filimo admin user; deliver password via secure channel (Signal / in-person / PGP)
- [ ] Filimo pastes `<script src="https://filimo.statnive.live/tracker.js" defer></script>` in their site template
- [ ] Root-domain cookie walking (Clarity pattern, doc 21) to cover CDN subdomains
- [ ] Acceptance: k6 7K EPS ramp (Persian URLs, Iranian UAs) passes p99 <500ms; full `iptables OUTPUT DROP` air-gapped acceptance from Phase 8 passes; Filimo smoke test confirms live traffic in dashboard within 1h; backup + restore drill succeeds

### Phase 11: International SaaS self-serve (Weeks 26–30, Phase C of Launch Sequence)

- [ ] Implement `POST /api/signup` (email + password + hostname → creates site + admin user)
- [ ] Implement `GET /api/stats/ping?site_id=X` (onboarding polling for first-event detection)
- [ ] Implement `POST /api/admin/billing` (Stripe webhook for upgrades)
- [ ] Subdomain routing middleware `dashboard/tenant.go` — extract `<slug>` from host, resolve to `site_id`, scope all `/api/stats/*` calls
- [ ] `internal/sites/provisioning.go` — slug generation (`example.com` → `example-com`), uniqueness check, hostname blocklist (spam/phishing lists)
- [ ] Signup guardrails: hostname DNS-resolvable, not on blocklist, unique in `sites` table, rate limit 5 signups/hour per IP
- [ ] Free tier quota: 10K PV/mo tracked via `daily_users` rollup; soft throttle on ingest above limit (still accept, tag events `quota_exceeded=1`), upsell banner in dashboard
- [ ] Stripe integration (tiers per existing pricing table: Starter $9, Growth $19, Business $69, Scale $199)
- [ ] Paid tiers unlock higher quota + goals/funnels CRUD
- [ ] Onboarding page at `<slug>.statnive.live/onboarding` with copy-paste snippet + live polling
- [ ] Email transactional flow (signup confirm, payment receipt, quota warnings) — opt-in per deployment, can disable for air-gapped
- [ ] Acceptance: fresh signup → tracker embed → first event visible in tenant dashboard in <5 min; cross-site isolation test (site A admin cannot query site B data via URL manipulation); Stripe webhook correctly updates `sites.plan`; signup rate limiter rejects 6th signup/hour from same IP

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

**Key operations:**
- Private key stored on offline HSM or hardware token (never on a networked machine)
- Rotation SOP: generate new keypair yearly; embed both old + new public keys in binary during overlap window; revoke old after all tokens expire
- Compromise recovery: rotate keypair, re-issue tokens, ship binary with only the new key

---

## v2 Roadmap (Post-Launch, +8–12 weeks) — 10 features per doc 18

| Feature | Effort | Priority |
|---------|--------|----------|
| Sequential funnel (windowFunnel) | 2 weeks | High |
| Cohort / retention | 2 weeks | High |
| Filtering / drill-down | 2 weeks | High |
| Google Search Console integration | 2 weeks | High |
| Session tracking | 1 week | Medium |
| Entry / exit pages | 1 week | Medium |
| Engagement time / page gap (doc 17 #62) | 1 week | Medium |
| Telegram weekly reports | 1 week | Medium |
| Data export / CSV (promote to v1 if frontend time permits) | 1 week | Medium |
| Public REST API | 1 week | Low |
| Microsoft Clarity integration (complementary, not replacement) | 1 day | Future |

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

### Infrastructure Cost per Customer (revised vs doc 19 — 7K EPS sustained is the real ceiling)

- Hetzner AX42 (€46/mo) safely handles **~30–50 sites at 1M PV/mo** each; 100 sites × 1M PV/mo would sustain ~13.5K EPS — above the 7K EPS proven load line. Upgrade to AX52 (€64/mo) for the 100-site tier.
- Cost per customer at 1M PV: **~€0.92–1.53/mo** on AX42 at 30–50 sites, or ~€0.64/mo on AX52 at 100 sites
- Gross margin at $19/mo pricing: **~90–95%** depending on tier density

---

## Server Costs

| Phase | Server | Monthly | Annual |
|-------|--------|---------|--------|
| v1 Launch (SaaS) | Hetzner AX42 (8c/64GB/1TB) | **€46** | **€552** |
| v2 Growth (SaaS) | Hetzner AX52 (8c/64GB/2TB) | **€64** | **€768** |
| Scale (SaaS) | Hetzner AX102 (16c/128GB/4TB) | **€104** | **€1,248** |
| Iranian DC (Filimo) | 8c/32GB/1TB SSD (Asiatech / Shatel / Afranet) | **~€180** | **~€2,160** |

**Notes:**
- Iranian DCs are quote-based (not public pricing). Assume upfront CAPEX on custom bare-metal builds; monthly figure is colocation + bandwidth only.
- Hetzner AX42 at 8c/64GB contradicts doc 19's 8c/32GB floor — the extra 32GB is SaaS multi-tenant headroom, not Filimo's single-tenant requirement. Filimo's Iranian DC can safely run 8c/32GB per doc 19.
- Bandwidth for 10–20M DAU @ ~1KB/event ≈ 10–20 GB/day raw → ~50–100 GB/day with responses; factored into Iranian DC quote.

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

- `statnive-live` binary (`CGO_ENABLED=0` where possible)
- `vendor/` tarball (for buildable-from-source audits)
- `statnive-live.docker.tar` (pre-built image via `docker save`, for Docker-based installs)
- `IP2LOCATION-LITE-DB23.BIN` (or licensed DB23 BIN)
- `clickhouse-backup` + `age` binaries
- `schema.sql` + `migrations/`
- `deploy/` scripts (systemd, iptables, backup, airgap-install, airgap-update-geoip)
- `SHA256SUMS` + detached Ed25519 signature

### Mandatory external services: **NONE**

### Opt-in external services (all OFF by default in air-gapped mode)

| Service | Purpose | Disable via config |
|---|---|---|
| Let's Encrypt (ACME) | TLS cert issuance | `tls.mode = "manual"` |
| Telegram Bot API | Operator alerts | `alerts.telegram.enabled = false` |
| `license.statnive.live` | SaaS license phone-home | `license.phone_home = false` |
| ip2location.com | Monthly GeoIP DB refresh | Never auto-fetched — always manual file drop |
| Remote syslog | Audit log shipping | `audit.remote = ""` |
| Google Search Console (v2) | Organic SEO data | Feature flag off |
| Microsoft Clarity (future) | Heatmaps | Feature flag off |
| Stripe (SaaS Phase C only) | Billing webhooks + payment | `billing.stripe.enabled = false` (D2 always off) |
| Transactional email (SaaS Phase C only) | Signup confirm, receipts, quota alerts | `email.enabled = false` (D2 always off) |

### Install procedure (air-gapped host)

1. Transfer `statnive-live-<version>-airgap.tar.gz` via USB/SCP from a trusted bastion
2. Verify SHA256 + Ed25519 signature against public key on a separate channel
3. Run `deploy/airgap-install.sh` — provisions user, systemd unit, iptables (`OUTPUT DROP` except tracker clients + loopback; CH localhost-bound)
4. Place license JWT at `config/license.key`
5. Start service; first-run creates admin user, applies migrations
6. GeoIP updates: SCP new `IP2LOCATION-…BIN` monthly, run `deploy/airgap-update-geoip.sh` (atomic rename + `SIGHUP`)

### What stops working in air-gapped mode (acceptable)

- Automatic TLS renewal — operator rotates manual certs quarterly (alert at `<14d`)
- Telegram alerts — file + local syslog replace them
- v2 license phone-home — pure offline JWT, grace treated as forever
- GSC / Clarity / auto-dep-updates — never required

### Prerequisites on the air-gapped host

- Linux kernel 5.x+, systemd, ClickHouse 24+ (also shipped in the bundle)
- **Internal NTP source** — IRST salt correctness depends on accurate clock
- Sufficient disk (plan ≥100 GB for WAL + CH data at 7K EPS for 90 days)
- Optional: internal CA + root cert distributed to tracker-embedding clients (for mode (c))

---

## Launch Sequence

statnive-live ships in **three public-facing phases across two deployments**. Same binary, same schema; differences are config + DNS + hosting.

| Deployment | Host | Tenancy | Purpose | Phases |
|---|---|---|---|---|
| **D1 — `statnive.live` (SaaS)** | Hetzner AX42, Germany | Multi-tenant, pooled ClickHouse | Dogfood + public SaaS | A, C |
| **D2 — `filimo.statnive.live` (Dedicated)** | Iranian DC (Asiatech / Shatel / Afranet) | Single-tenant (`site_id=1` only), air-gapped | Filimo production | B |

### Routing strategy (both deployments)

- **Single tracker URL:** `https://<host>/tracker.js` — site-agnostic, `site_id` resolved server-side from `Origin` / `Referer` hostname against the `sites` table
- **Dashboard subdomain per site** (D1): `<slug>.statnive.live` where `<slug>` is the sanitized hostname (`example.com` → `example-com`); wildcard TLS cert covers all
- **Fixed dashboard hostnames:** `demo.statnive.live` (Phase A), `filimo.statnive.live` (Phase B)
- **Central signup + login:** `statnive.live/signup`, `statnive.live/app`

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

- **Deployment:** D1 (Hetzner AX42)
- **DNS:** A + AAAA → D1 IP for both `statnive.live` and `demo.statnive.live`
- **TLS:** `autocert` via ACME DNS-01 (Cloudflare API token) — wildcard `*.statnive.live` + apex
- **Config diff from default:** `tls.mode = "autocert"`, license NOT required
- **Seed SQL:** `INSERT INTO sites (site_id, hostname) VALUES (1, 'statnive.com');`
- **Seed users:** shared viewer `demo / demo-statnive`; internal admin for us
- **Login page:** displays demo credentials inline + "Sign up for your own analytics" CTA → Phase C signup
- **Tracker install:** `<script src="https://statnive.live/tracker.js" defer></script>` in `statnive-website/` Astro base layout
- **Rate limiting:** login attempts capped at 10/min per IP to prevent brute force on the shared demo password
- **Banner in dashboard:** "Public demo — statnive.com traffic — viewer role, no writes"
- **Acceptance:** within 24h of tracker install, dashboard shows non-zero visitors; viewer login gets 403 on any `/api/admin/*`; all 8 `/api/stats/*` endpoints return data scoped to `site_id=1`

### Phase B — Filimo dedicated Iranian VPS (Weeks 22–25)

**Goal:** `filimo.statnive.live` runs on an Iranian DC, Filimo team logs in with admin credentials, tracker is `filimo.statnive.live/tracker.js`. Fully secure, max performance, air-gapped-capable.

- **Deployment:** D2 (Iranian DC bare metal, 8c/32GB/1TB NVMe, 1 Gbps uplink, ~€180/mo negotiated)
- **Hardware:** negotiate quotes with Asiatech / Shatel / Afranet — colocation + bandwidth; we provide the hardware spec per doc 16 §12.2
- **Install:** offline bundle from Phase 8 (`make airgap-bundle`) — SCP tarball via bastion, verify `SHA256SUMS` + Ed25519 signature, run `deploy/airgap-install.sh`
- **DNS:** `CNAME filimo.statnive.live → <Iranian-DC-IP>`; Cloudflare proxy **OFF** (traffic must terminate inside Iran)
- **TLS (three fallbacks):**
  - **Plan A — manual PEM:** customer-provided or self-signed via internal CA, rotated quarterly
  - **Plan B — ACME DNS-01 via Cloudflare API:** only if outbound HTTPS to Let's Encrypt is allowed
  - **Plan C — internal CA:** customer's corporate root CA, distributed to tracker-embedding clients
- **License:** generate JWT with our offline Ed25519 HSM key — `site_id=1, Customer="Filimo", MaxEventsDay=0, Features=["*"], ExpiresAt=+1y` — drop at `config/license.key`
- **Config overrides:**
  - `tls.mode = "manual"` (or `"autocert-dns01"` if Plan B)
  - `alerts.sinks = ["file", "syslog"]` (no Telegram)
  - `license.phone_home = false`
  - `audit.remote = ""` (local JSONL only)
  - Single-tenant: only `site_id=1` provisioned in `sites` table
- **Seed:** `INSERT INTO sites VALUES (1, 'filimo.com'), (1, 'www.filimo.com'), (1, 'cdn.filimo.com'), …` — all Filimo-owned hostnames that might embed the tracker
- **Admin user:** password generated at first-run, delivered to Filimo via secure channel (Signal / in-person / PGP)
- **Tracker install (on Filimo side):** `<script src="https://filimo.statnive.live/tracker.js" defer></script>` in their site template; root-domain cookie walking (Clarity pattern, doc 21) automatically covers all Filimo subdomains + CDN hosts
- **Firewall:** `iptables -P OUTPUT DROP` with explicit allows for: loopback, ClickHouse port (localhost only), tracker client IP ranges (if geofenced), DNS resolver, NTP
- **Acceptance:** k6 7K EPS ramp (Persian URLs, Iranian UA strings) sustains p99 <500ms; full air-gapped acceptance test from Phase 8 verification passes end-to-end; Filimo smoke test confirms live traffic in dashboard within 1h; monthly backup + restore drill succeeds

### Phase C — International SaaS self-serve (Weeks 26–30)

**Goal:** anyone registers at `statnive.live`, gets their dashboard at `<slug>.statnive.live`, pastes a one-liner tracker snippet.

- **Deployment:** D1 (same instance as Phase A, multi-tenant continues)
- **New endpoints (on top of v1):**
  - `POST /api/signup` — `{email, password, hostname}` → creates `site_id`, admin user, returns redirect to `<slug>.statnive.live/onboarding`
  - `GET /api/stats/ping?site_id=X` — onboarding page polls until first event arrives (returns `{seen: bool}`)
  - `POST /api/admin/billing` — Stripe webhook (plan upgrades / cancellations)
- **Subdomain routing middleware** (`internal/dashboard/tenant.go`):
  - Parse host → extract `<slug>` → resolve to `site_id` via `internal/sites/sites.go`
  - Inject `site_id` into request context; all `/api/stats/*` handlers read from context
  - Missing slug → redirect to `statnive.live/app` (root login)
- **Signup guardrails:**
  - Hostname must DNS-resolve (simple A/AAAA lookup)
  - Hostname not on blocklist (spam/phishing lists, known typosquats)
  - Unique in `sites` table (first-come-first-served for hostname)
  - Rate limit 5 signups/hour per IP
  - Email verification link before tracker is activated (24h grace)
- **Free tier quota:** 10K PV/mo tracked via `daily_users` rollup; over-quota = soft throttle (still accept events, tag with `quota_exceeded=1`, show upsell banner)
- **Stripe tiers** (per existing pricing at PLAN.md line 546):
  - Free (self-hosted only, no SaaS)
  - Starter $9/mo → 100K PV + 5 goals
  - Growth $19/mo → 1M PV + unlimited goals + funnels CRUD
  - Business $69/mo → 10M PV + API access
  - Scale $199/mo → 100M PV + priority support
- **Onboarding UX:** post-signup page shows tracker snippet + live polling indicator; flips to real dashboard as soon as first event lands
- **Email transactional:** signup confirm, payment receipt, quota warnings — opt-in per deployment (can disable for future self-hosted SaaS)
- **Acceptance:** fresh signup → tracker embed → first event visible in <5 min; cross-tenant isolation (site A admin sees only site A data even when URL-manipulating); Stripe webhook correctly updates `sites.plan` and quota flips accordingly; signup rate limiter rejects 6th signup/hour per IP

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
13. TLS renewal: ACME cert rotation 14+ days before expiry on both HTTP-01 and DNS-01 paths
14. Tracker: install on test page → events appear in dashboard within 1 hour
15. GDPR (SaaS only): consent decline drops cookies + user_id; `/api/privacy/erase` removes visitor across raw + all 6 rollups
16. License: demo-mode binary caps at 10K events/day; valid JWT unlocks; expired JWT falls back to demo-mode with warning
17. **Air-gapped acceptance**: deploy offline bundle on host with `iptables -P OUTPUT DROP` (loopback + tracker IPs only). Binary starts, migrations apply, events ingest end-to-end, rollups materialize, dashboard renders, backup + restore succeed — all with zero outbound traffic
18. **Offline build**: `go build -mod=vendor ./...` succeeds with `GOFLAGS=-mod=vendor` and no network access
19. Manual TLS: binary serves traffic with `tls.mode = "manual"` and `tls.cert_file` / `tls.key_file` pointing at internal-CA-issued PEMs (no autocert call)
20. Air-gapped GeoIP update: replace DB23 BIN + `SIGHUP` → new IPs resolve correctly without restart
21. **Phase A (dogfood):** statnive.com fires a pageview → visible in `demo.statnive.live` dashboard within 5 minutes; shared viewer login (`demo / demo-statnive`) gets 403 on every `/api/admin/*` route; login brute-force capped at 10 attempts/min per IP
22. **Phase B (Filimo):** Filimo tracker at `filimo.statnive.live/tracker.js` fires → visible in `filimo.statnive.live` dashboard within 5 minutes; `iptables -P OUTPUT DROP` test passes end-to-end on Iranian DC box; backup + restore drill succeeds on the dedicated instance
23. **Phase C (SaaS):** fresh signup (`POST /api/signup`) → tracker embed → first event appears in `<slug>.statnive.live` within 5 minutes; cross-tenant isolation — site A admin cannot query site B data even by URL manipulation; Stripe webhook updates `sites.plan` and quota enforcement flips correctly; 6th signup/hour from same IP is rejected
