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

- **ALL dependencies MUST be MIT/Apache/BSD/ISC** — no AGPL in the binary
- statnive-live is sold as SaaS outside Iran where AGPL Section 13 applies
- **DO NOT import pirsch-analytics/pirsch** (AGPL) — reference patterns only
- **DO NOT use knadh/koanf** (AGPL) — use viper (MIT) or env-only config
- Before adding any dependency, verify its license with `go-licenses`

## Privacy Rules (Non-Negotiable)

Iran allows cookies + `user_id` pass-through; the EU/SaaS tier does not. Both code paths live in the same binary — these rules are what keep them consistent.

1. **Raw IP never persisted** — IP enters the pipeline only for GeoIP lookup, then is discarded before the batch writer sees the row (`internal/enrich/geoip.go` contract, asserted by integration test).
2. **Daily rotating salts** — `HMAC(master_secret, site_id || YYYY-MM-DD IRST)`. Same visitor produces a different hash each day; salt is derived, never stored.
3. **SHA-256+ and BLAKE3 only** in any privacy/identity path. No MD5, no SHA-1 anywhere in the binary.
4. **User ID hashed before ClickHouse write** — `SHA-256(master_secret || site_id || user_id)`. Raw `user_id` is never logged, never written to disk, never shipped to audit sinks.
5. **Iran = cookies + user_id allowed; SaaS (hosted outside Iran) = GDPR applies to EU visitors** — customer DPA, consent banner, and subject access / erasure rights are required on the SaaS tier.
6. **DNT + GPC respected by default** on the SaaS tier; self-hosted operator decides per deployment.
7. **First-party tracker via `go:embed`** — no external CDN, no fingerprinting (no canvas / WebGL / font probing, no `navigator.plugins` enumeration).

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

## Feature Scope (51 v1 + 10 v1.1 fast-follow + 17 v2 + 1 Future)

Derived from research doc 18 + doc 17, revised with doc 24 (Pirsch pattern extraction). v1 is scoped to what's load-bearing for Filimo's first 90 days and the 5 Project Goals. Polish features (organic SEO depth, comparison widget, Jalali, outbound links) slip to **v1.1** — a 4–6 week fast-follow after v1 cut. v2 is the post-launch 8–12 week product push.

### v1 — 51 features (MVP)

**Security (12):**
1. TLS 1.3 via manual PEM files (one code path; works everywhere)
2. ClickHouse localhost-only (bound 127.0.0.1)
3. Hostname validation on `/api/event` (HMAC skipped per doc 20)
4. Input validation (`MaxBytesReader` 8KB, field limits, timestamp ±1h)
5. Rate limiting via `go-chi/httprate` (100 req/s, burst 200, NAT-aware)
6. Dashboard auth (bcrypt + `crypto/rand` sessions, 14-day TTL, `SameSite=Lax`)
7. RBAC (admin / viewer / api-only)
8. Encrypted backups (`clickhouse-backup` + `age` + `zstd`, cron + monthly restore test)
9. Disk encryption LUKS (optional; 40–50% I/O overhead trade-off)
10. Audit log (JSONL, file sink only)
11. User ID hashed before storage (SHA-256 of `master_secret || site_id || user_id`)
12. systemd hardening + tracker via `go:embed` (first-party, ad-blocker-resistant)

**Identity (3):**
13. user_id pass-through (site sends; hashed server-side)
14. Cookie fallback (httpOnly, SameSite=Lax, 1y max-age)
15. BLAKE3-128 hash fallback with daily salt `HMAC(master_secret, site_id || YYYY-MM-DD IRST)`

**Events & Goals (4):**
16. Custom event API: `statnive.track(name, props, value)`
17. Goal YAML config (event → goal mapping, SIGHUP hot reload)
18. Goal value column (UInt64 rials, `DEFAULT 0`, no Nullable)
19. Goal rate per channel / per page (aggregated in rollups)

**Funnels (2):**
20. Funnel YAML definition (ordered event steps)
21. Funnel report: count + drop-off % per step, 1h cache

**Revenue & CRO (7):**
22. Revenue sum per channel
23. Revenue sum per page
24. Revenue trend (daily / weekly)
25. Conversion rate per source
26. Conversion rate trend
27. Average value per conversion per channel
28. **Revenue Per Visitor (RPV) per channel** — primary CRO metric (Project Goal philosophy)

**Attribution (6):**
29. UTM tracking (5 params: source, medium, campaign, content, term)
30. Auto source detection (referrer → named source via `sources.yaml`)
31. Channel grouping (Organic / Social / Direct / Paid / Email / Referral priority) — **17-step decision tree** per doc 24 §Sec 3 (paid-first ordering: click-IDs → `utm_medium` tokens → organic fallback). Iranian referrer seed lives in `config/sources.yaml` (doc 24 §Sec 3b, ~55 hostnames original research).
32. 50+ Iranian source database (Divar, Torob, Filimo, etc.)
33. Campaign report (breakdown by `utm_campaign`)
34. **AI traffic channel** (ChatGPT, Claude, Gemini, Copilot, Perplexity) — new bucket in the 17-step tree [doc 24 §Sec 3.3]. Free wedge, < 1d effort; AI referrer share is non-trivial on Iranian Filimo blog / news landing traffic in 2025–2026.

**SEO (1):**
35. Organic search traffic trend

**Content & Trends (3):**
36. Top pages (by visitors, views, goals, revenue)
37. Visitors trend (hourly / daily)
38. New vs returning visitors (18MB bloom filter, 10M visitors, 0.1% FPR)

**Audience (5):**
39. Iranian provinces / cities (IP2Location LITE DB23 in v1; paid DB23 for Filimo specifically)
40. Device / browser / OS (`medama-io/go-useragent`, ~287 ns/op)
41. ISP / carrier (MCI, Irancell, Rightel via DB23)
42. User segments (custom properties sent with user_id)
43. **Language dimension** (`LowCardinality(String)`, ISO-639 normalized — `en-us` → `en`) [doc 24 §Sec 5 Table 2 row 12]. One extra rollup column; Filimo has an English landing alongside Persian. < 1d effort.

**Infrastructure (6):**
44. Pageview tracking (`navigator.sendBeacon` + fetch keepalive)
45. SPA route tracking (pushState/replaceState patching + popstate)
46. Bot filtering — server (`omrilotan/isbot` + `crawler-user-agents.json`, layered cheap-first per doc 24 §Sec 1.3) + client (`navigator.webdriver`, `evt.isTrusted`, `_phantom`) + **max-pageviews-per-visitor** burst guard (doc 24 §Sec 5 Table 2 row 15 — one counter per visitor_hash in the WAL window, prevents Iranian scraper-network bot inflation)
47. GeoIP at ingest (IP2Location `.BIN`, raw IP discarded after lookup). Proxy IP parsing honors `X-Forwarded-For` (rightmost), `X-Real-IP`, `True-Client-IP`, and `CF-Connecting-IP` (doc 24 §Sec 5 Table 2 row 20 — relevant for Iranian sites behind ArvanCloud / Cloudflare).
48. UA parsing (Medama fast-path)
49. Hourly active-visitors widget (NOT 5-min real-time — rollup-based)

**Multi-tenant (1):**
50. `site_id` on every raw + rollup row; hostname → site_id resolution at ingest; `WHERE site_id = ?` on every query (via central `whereTimeAndTenant()` helper — Architecture Rule 8)

**Ingestion Hardening (1):**
51. **Pre-pipeline fast-reject gate** in `internal/ingest/handler.go` — checks `X-Purpose`/`Purpose`/`X-Moz` prefetch headers + UA length (16–500) + UA-is-IP/UUID + non-ASCII UA *before* the event enters the enrichment pipeline. Returns `204 No Content`. Zero-cost on real traffic; skips all 6 pipeline stages on bots/prefetch. [doc 24 §Sec 1 item 6]

### v1.1 — 10 fast-follow features (ship 4–6 weeks after v1 cut)

These are polish / depth items, not load-bearing for the 5 goals:

1. Top landing pages from organic search
2. Organic conversion rate + revenue
3. Organic vs paid split
4. High-traffic / low-conversion pages
5. Comparison periods (this period vs previous, % change UI) — **day-of-week aligned** (Tuesday-vs-Tuesday, not Tuesday-vs-Monday) per doc 24 §Sec 5 Table 2 row 19. Query change only; < 1d effort once comparison UI ships.
6. Jalali calendar display (`jalaali-js` 3KB, client-side)
7. Outbound link tracking (click delegation + sendBeacon on external links)
8. **Weekday × hour heatmap** (when are my visitors active?) — drops out of `hourly_visitors` with one extra `toDayOfWeek(hour)` projection [doc 24 §Sec 5 Table 2 row 13]. High-signal for content-planning panels; < 1d effort.
9. **Non-interactive events** — boolean column on `events_raw`, excluded from visitor counts [doc 24 §Sec 5 Table 2 row 18]. Needed for Filimo video-play telemetry, scroll-depth, and autoplay instrumentation without visitor inflation.
10. **Bot-reason logging** — `LowCardinality(String)` column on `events_raw` recording *why* the row was flagged (`ua_regex` / `ua_length` / `referrer_spam` / `version_floor` / `isbot` / etc.) [doc 24 §Sec 5 Table 2 row 16]. Debugging gift for support; ship after Phase 8 bot filter stabilizes.

Plus the **3 additional rollups** (`daily_geo`, `daily_devices`, `daily_users`) that power the v1.1 depth panels.

### v2 — 17 features (post-launch, +8–12 weeks)

1. Sequential funnel (`windowFunnel`, 24h window) — keep `windowFunnel()`; **do not adopt** Pirsch's N-CTE JOIN pattern (doc 24 §Sec 4 pattern 4 — too expensive at 10–20M DAU)
2. Cohort / retention (first_seen cohort, weeks-later window)
3. Filtering / drill-down (extra `WHERE` on rollups, hash-keyed cache)
4. Google Search Console integration (OAuth2, keywords, position, CTR — 2–3d delay)
5. Session tracking (duration, pages/session, window functions)
6. Entry / exit pages (`first_value` / `last_value` per session) — use `argMaxState` in rollups, not mutable session rows (doc 24 §Sec 2 Migration 0008)
7. Engagement time (page-gap between consecutive events per visitor)
8. Email + Telegram weekly reports (`robfig/cron`, Monday 9 AM IRST, Persian numerals)
9. CSV data export (`http.Flusher` chunked transfer, 1 export/hour rate limit)
10. Public REST API (Bearer token auth, rate limited, OpenAPI docs)
11. **Filter options API** — dynamic autocomplete for every dimension (hostnames, pages, referrer, utm_*, browser, OS) [doc 24 §Sec 5 Table 2 row 9]. Required companion to v2 #3 drill-down; without autocomplete, filter UX is broken. ~3 days using existing rollups.
12. **Time on page** — session-local page duration (distinct from #7 engagement time which is visitor-level page-gap) [doc 24 §Sec 5 Table 2 row 7]. Feeds SEO panels (v1.1 #1–4). ~1 week alongside session tracking.
13. **Session sampling** — `SAMPLE N` on rollups, feature-flagged per query [doc 24 §Sec 2 Migration 0020 + §Sec 5 Table 2 row 3]. Free once AggregatingMergeTree rollups are in place; keeps dashboard p95 flat at Filimo's 10–20M DAU. < 3 days.
14. **Data import (CSV-in)** — minimal daily-rollup CSV ingestion endpoint for GA4 / Matomo / WP-Statistics historical migration [doc 24 §Sec 5 Table 2 row 4]. ~1 week. **Skip** Pirsch's 13-table `imported_*` shape — overkill for our need.
15. **Anonymous click-ID tracking** — capture `gclid`, `msclkid`, Iranian equivalents (Yektanet / Tap30) without PII [doc 24 §Sec 5 Table 2 row 17]. Feeds paid-search attribution; plumbing is generic, click-ID → source mapping lives in `sources.yaml`. ~2 days.
16. **Individual session drill-down** — separate narrow `session_detail` rollup (raw-table query forbidden by Architecture Rule 1) [doc 24 §Sec 5 Table 2 row 14]. Useful for Filimo support debugging. Ships alongside #5 session tracking.
17. **Max-pageviews-per-visitor config** — configurable threshold (defaults: 500/request, 75ms–500ms min delay between PVs) [doc 24 §Sec 5 Table 2 row 15 — extended from v1 burst guard]. Adds per-site override to the v1 default.

**Deliberate skips** (Pirsch has, statnive-live rejects per doc 24 §Sec 5 Table 2 dispositions):

- ClickHouse cluster mode (single-node is the Architecture Rule)
- Redis session cache (breaks the single-binary / air-gapped promise — WAL + in-memory replaces it)
- Bounce rate (vanity metric per Never list; expose time-on-page + funnel drop-off as the honest answer if customers ask)

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

Claude Code skills + MCP server setup for this project live in [`docs/tooling.md`](docs/tooling.md) (not in CLAUDE.md — it's developer ergonomics, not product rules). That file covers the **original 4 skill collections** (cc-skills-golang, ClickHouse Agent Skills, trailofbits, marina-skill — doc 23 foundation), the **doc 25 additions** (anthropics/skills cherry-pick, JetBrains use-modern-go, agamm/claude-code-owasp, BehiSecc/VibeSec-Skill, izar/tm_skills, vercel-labs web-design-guidelines, obra/superpowers 5-skill subset, knip, constant-time-analysis), the **6 project-local custom skills** (see § Enforcement below), **4 MCP servers** (Altinity ClickHouse, gopls, Hetzner, Grafana), and the phase → tooling mapping. The `/jaan-to:*` skills ship with the parent plugin and handle *what* (specs, scaffolds, tests, reviews); community collections handle *how* (Go/ClickHouse/security/deploy patterns); the 6 custom skills encode the 8 non-negotiable architecture rules as CI-blocking guardrails.

**Do not install:** `anthropics/skills/web-artifacts-builder` (React+shadcn+CDN-fonts — air-gap violation, blows past bundle budget), `shajith003/awesome-claude-skills` (AI-slop), `sickn33/antigravity-awesome-skills`, `rohitg00/awesome-claude-code-toolkit` (inflated counts, low S/N). Reference: doc 25 §landscape.

### Skills Decision Tree

Quick route before diving into `docs/tooling.md`:

```
Task arrives
  ├─ PRD / story / roadmap?                       → /jaan-to:pm-prd-write, pm-story-write, pm-roadmap-add
  ├─ ClickHouse schema design?                    → /jaan-to:backend-data-model
                                                    then clickhouse-architecture-advisor + clickhouse MCP
  ├─ API contract / OpenAPI?                      → /jaan-to:backend-api-contract
  ├─ Scaffold Go service from spec?               → /jaan-to:backend-scaffold then golang-project-layout
  ├─ Go concurrency / context / errors?           → golang-concurrency / golang-context / golang-error-handling
  ├─ DB query tuning / rollups?                   → clickhouse-best-practices + clickhouse MCP
  ├─ Security review / static analysis?           → static-analysis + golang-security +
                                                    gopls MCP (govulncheck)
  ├─ Remediate security findings?                 → /jaan-to:sec-audit-remediate
  ├─ Engineering audit / scoring?                 → /jaan-to:detect-dev
  ├─ Backend PR review?                           → /jaan-to:backend-pr-review +
                                                    differential-review + second-opinion
  ├─ BDD / Gherkin test cases?                    → /jaan-to:qa-test-cases
  ├─ Runnable tests from cases?                   → /jaan-to:qa-test-generate
  ├─ Run / diagnose / auto-fix tests?             → /jaan-to:qa-test-run + golang-linter
  ├─ CI/CD / Docker scaffolds?                    → /jaan-to:devops-infra-scaffold
  ├─ Deploy (Hetzner)?                            → server-management + server-bootstrap +
                                                    hetzner MCP + /jaan-to:devops-deploy-activate
  ├─ Verify running build?                        → /jaan-to:dev-verify
  ├─ Fetch library docs?                          → /jaan-to:dev-docs-fetch (Context7 MCP)
                                                    fallback: docs/tech-docs/ (16 cached refs)
  ├─ Preact SPA from handoff?                     → /jaan-to:frontend-scaffold / frontend-design
  │                                                 (clamp: emit Preact-compatible output,
  │                                                  self-hosted fonts only — same clamp applies
  │                                                  to vercel-labs/web-design-guidelines)
  ├─ User flow diagrams?                          → /jaan-to:ux-flowchart-generate
  ├─ Microcopy / i18n (Persian/English)?          → /jaan-to:ux-microcopy-write
  ├─ Skill / SKILL.md authoring?                  → anthropics/skills → skill-creator + template
  ├─ Air-gap compliance / outbound-call review?   → .claude/skills/air-gap-validator (custom)
  ├─ New ClickHouse rollup / MV?                  → .claude/skills/clickhouse-rollup-correctness +
                                                    clickhouse-best-practices
  ├─ New migration (single-node → Distributed)?   → .claude/skills/clickhouse-cluster-migration
  ├─ Dashboard query review?                      → .claude/skills/tenancy-choke-point-enforcer
  ├─ Preact / signals / bundle-budget review?     → .claude/skills/preact-signals-bundle-budget +
                                                    vercel-labs/web-design-guidelines
  ├─ BLAKE3 / HMAC / identity review?             → .claude/skills/blake3-hmac-identity-review +
                                                    trailofbits/constant-time-analysis
  ├─ Threat model / STRIDE?                       → izar/tm_skills (ctm / 4qpytm)
  ├─ OWASP checklist?                             → agamm/claude-code-owasp
  ├─ IDOR / horizontal authZ review?              → BehiSecc/VibeSec-Skill
  ├─ Modern Go idioms (b.Loop, wg.Go)?            → JetBrains/use-modern-go
  ├─ Planning / methodology?                      → obra/superpowers 5-skill subset
                                                    (brainstorming, writing-plans,
                                                     subagent-driven-development,
                                                     verification-before-completion,
                                                     systematic-debugging)
  ├─ Frontend dead-code / unused-dep scan?        → agentskillexchange/knip-unused-code-dependency-finder
  ├─ Tracker (<2 KB IIFE)?                        → .claude/skills/preact-signals-bundle-budget
                                                    (covers the ~1.2 KB-min / ~600 B-gz tracker budget;
                                                     builds are still hand-authored)
  └─ Unknown?                                      → open docs/tooling.md, don't guess
```

## Single Source of Truth

`../statnive-workflow/jaan-to/docs/research/` (docs 14–25) is the canonical source for every architecture, feature, and threat-model decision in this project. Do **not** restate research conclusions in this `CLAUDE.md` or in skill prompts — reference by doc number and section only. When a decision changes, update the research doc; this file references it and never duplicates. Same rule applies to the feature matrix (doc 17, 18), the cost model (doc 19), the initial skill / MCP list (doc 23), the **AGPL-safe Pirsch pattern extraction (doc 24)** — reference only, never port — and the **Claude-skills install matrix + custom-skill catalog (doc 25)** — reference only, never restate the matrix in skill prompts.

## Enforcement

These integration tests pin the invariants in this file. They are Phase 0 / Phase 7 deliverables — listing them here makes the contract explicit so `/simplify` and PR review can reject regressions on day one.

- `test/integration/enrichment_order_test.go` — asserts pipeline order is `identity → bloom → geo → ua → bot → channel` (Architecture Rule 6).
- `test/integration/airgap_test.go` — runs the binary under `iptables -A OUTPUT -j DROP` and asserts ingest, rollup materialization, and dashboard rendering all work (Isolation rule).
- `test/integration/multitenant_test.go` — asserts every dashboard query includes `WHERE site_id = ?` and no cross-tenant row leaks (Project Goal 4).
- `test/integration/pii_leak_test.go` — asserts raw IP and raw `user_id` never appear in ClickHouse tables or in the JSONL audit log (Privacy Rules 1, 4).
- `test/security/no_agpl_test.go` — `go-licenses` asserts every direct + transitive dep is MIT / Apache / BSD / ISC (License Rules).
- `web/src/__tests__/tenant-isolation.test.tsx` — Vitest guard that Preact signal stores don't leak `site_id` state across dashboard views.

### Project-local SKILL.md guardrails (doc 25 §custom-skills)

These six skills live under `.claude/skills/` and encode the 8 non-negotiable architecture rules as triggerable Claude guardrails. Each has a `SKILL.md` (frontmatter + trigger) and a `README.md` (full spec + CI wiring). Bodies (Semgrep rules, test fixtures) fill in per phase — the slots exist from day one so code cannot merge pretending they don't exist.

- [`.claude/skills/tenancy-choke-point-enforcer/`](.claude/skills/tenancy-choke-point-enforcer/README.md) — encodes Architecture Rule 8. Rejects dashboard SQL that bypasses `whereTimeAndTenant()` or places `WHERE site_id = ?` anywhere but first.
- [`.claude/skills/air-gap-validator/`](.claude/skills/air-gap-validator/README.md) — encodes the Isolation rule. Rejects new deps that do DNS/outbound at runtime, CDN imports in `web/`, or telemetry calls anywhere.
- [`.claude/skills/clickhouse-rollup-correctness/`](.claude/skills/clickhouse-rollup-correctness/README.md) — encodes Architecture Rule 2 + doc 20 MV discipline. Validates `-State`/`-Merge`/`-MergeState` combinator discipline for `uniqCombined64` and rejects `Nullable` anywhere.
- [`.claude/skills/clickhouse-cluster-migration/`](.claude/skills/clickhouse-cluster-migration/README.md) — encodes the `{{if .Cluster}}` templating rule from doc 24 §Migration 0029. Every migration must be single-node ↔ Distributed flip-ready.
- [`.claude/skills/preact-signals-bundle-budget/`](.claude/skills/preact-signals-bundle-budget/README.md) — encodes the 50KB/15KB-gz dashboard + 1.2KB/600B-gz tracker budgets. Rejects barrel imports, >5KB deps, and CDN URLs in `web/` or `tracker/`.
- [`.claude/skills/blake3-hmac-identity-review/`](.claude/skills/blake3-hmac-identity-review/README.md) — encodes Privacy Rules 2, 3, 4. Rejects MD5/SHA-1, non-`hmac.Equal` comparisons, and any code path that logs the master secret.

`/simplify` and PR review must reject any new unguarded query (no `WHERE site_id = ?`), any new dependency without a license check, any new outbound network call not behind a config flag, and any new `Nullable(...)` column.

## Research Documents

All architecture decisions are backed by research at:
`../statnive-workflow/jaan-to/docs/research/` (docs 14–25, 500+ sources). Doc 23 covers the initial Claude Code tooling recommendations (doc-23 foundation: 30 installed skills, 4 MCP servers). **Doc 24** is the AGPL-safe Pirsch pattern extraction (reference-only audit of `github.com/pirsch-analytics/pirsch` v6) — informs ingestion shape (pre-pipeline fast-reject, cross-day fingerprint grace, cheap-first bot ordering), ClickHouse schema (reject mutable-row engines, `DateTime` not `DateTime64`, templated DDL for Distributed upgrade), channel mapping (17-step decision tree, AI channel on day 1), and dashboard query architecture (`Filter → Store → queryBuilder` shape, `WITH FILL` gap-fill, central `whereTimeAndTenant` helper). **Zero Pirsch code ported.** **Doc 25** is the Claude-skills install matrix and custom-skill catalog — 8 community bundles to install, 6 custom `.claude/skills/` to author, and an explicit blacklist (`web-artifacts-builder`, `shajith003/awesome-claude-skills`, etc.). Reference-only; never restate the matrix in skill prompts.
