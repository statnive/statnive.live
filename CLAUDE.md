# statnive-live

> **statnive.live** — High-performance, privacy-aware analytics for high-traffic websites.
> Self-hosted or SaaS. First customer: Filimo (10-20M DAU).

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
5. **No Nullable columns** — use `DEFAULT ''` or `DEFAULT 0`. Nullable costs 10–200% on aggregations (doc 20 measured 2× on `Nullable(Int8)`).
6. **Enrichment order is locked** — per event: identity → bloom → GeoIP → UA → bot → channel (doc 22 §GAP 1, asserted in integration tests). **Pre-pipeline fast-reject gate** (doc 24 §Sec 1 item 6): UA length 16–500, non-ASCII UA, IP-as-UA, UUID-as-UA, `X-Purpose`/`X-Moz` prefetch → `204`. In-pipeline bot layering is cheap-first (prefetch → UA shape → referrer spam → browser-version floor → UA keyword/regex blacklist).
7. **Defer before building** — if a feature isn't required for the 5 Project Goals or Filimo's first 90 days, it ships in v1.1 or v2. Applies to multi-sink alerts, DLQ tooling, subdomain-per-tenant routing, Polar customer portal, and anything else not load-bearing.
8. **Central tenancy choke point** — every dashboard SQL path goes through `internal/storage/queries.go:whereTimeAndTenant()` (doc 24 §Sec 4 pattern 6). `WHERE site_id = ?` is the first clause. `ORDER BY` / `PARTITION BY` lead with `site_id`. Any new query skipping this helper is a CI failure.

## License Rules (Critical)

- **ALL linked dependencies MUST be MIT/Apache/BSD/ISC** — no AGPL in the binary.
- statnive-live is sold as SaaS outside Iran where AGPL Section 13 applies.
- **DO NOT import pirsch-analytics/pirsch** (AGPL) — reference patterns only.
- **DO NOT use knadh/koanf** (AGPL) — use viper (MIT) or env-only config.
- Before adding any dependency, verify its license with `go-licenses`.
- **CC-BY-SA-4.0 carve-out for non-linked data files only** (doc 28 §Gap 1 policy). IP2Location LITE DB23 and similar GeoIP BINs are data, not linked code — the binary surface gate does not apply. CC-BY-SA-4.0 §3(a)(1) "reasonable-manner based on the medium" attribution is satisfied for LITE **only** by delivering the verbatim string — *"This site or product includes IP2Location LITE data available from https://lite.ip2location.com."* — in **all three** of: (a) `LICENSE-third-party.md`, (b) the `/about` JSON endpoint, and (c) the dashboard footer. `--license` CLI flag alone does not satisfy §3(a)(1). All three surfaces are enforced at CI time by the Semgrep rule `geoip-attribution-string-present` shipped in [`geoip-pipeline-review`](.claude/skills/geoip-pipeline-review/README.md); full delivery matrix in [`geoip-pipeline-review/references/attribution.md`](.claude/skills/geoip-pipeline-review/references/attribution.md). Every major free city-level GeoIP DB is CC-BY-SA-4.0, so the previous blanket rejection was unsatisfiable; tier-by-tier posture is in [`docs/tooling.md`](docs/tooling.md) § GeoIP licensing. Paid IP2Location DB23 Site License at Phase 10 waives attribution; until then LITE stays default.

## Privacy Rules (Non-Negotiable)

Iran allows cookies + `user_id`; the EU/SaaS tier does not. Both code paths live in the same binary — these rules keep them consistent. Extended GDPR Art./Recital-26/C-413/23 chain in [`docs/rules/privacy-detail.md`](docs/rules/privacy-detail.md).

1. **Raw IP never persisted** — IP enters the pipeline only for GeoIP lookup, discarded before the batch writer sees the row (`internal/enrich/geoip.go` contract, asserted by integration test).
2. **Daily rotating salts** — `HMAC(master_secret, site_id || YYYY-MM-DD IRST)`. Derived, never stored.
3. **SHA-256+ and BLAKE3 only** in any privacy/identity path. No MD5, no SHA-1 anywhere in the binary.
4. **User ID hashed before ClickHouse write** — `SHA-256(master_secret || site_id || user_id)`. Raw `user_id` never logged, never on disk, never in audit sinks.
5. **Iran = cookies + user_id allowed; SaaS = GDPR applies to EU visitors** — customer DPA, consent banner, subject access / erasure rights required.
6. **DNT + GPC respected by default** on SaaS; self-hosted operator decides per deployment.
7. **First-party tracker via `go:embed`** — no external CDN, no fingerprinting (no canvas / WebGL / font probing, no `navigator.plugins` enumeration).
8. **Salt rotation DELETES the previous salt file** — not overwrites (recoverability + Recital 26 — see [detail](docs/rules/privacy-detail.md#rule-8--salt-rotation-deletes-the-previous-salt-file)). Enforced by [`blake3-hmac-identity-review`](.claude/skills/blake3-hmac-identity-review/README.md) + [`gdpr-code-review`](.claude/skills/gdpr-code-review/README.md).
9. **`Sec-GPC: 1` and consent-decline short-circuit BEFORE hash computation** — not after (GDPR Art. 4(2)). SaaS DPA legal chain in [detail](docs/rules/privacy-detail.md#rule-9--consent--gpc-short-circuit-before-hash-computation); draft at `docs/dpa-draft.md` (Phase 11).

## Security (13 Features, All v1)

Extended operational detail (fallback CA list, full systemd option list, LUKS I/O reasoning, CGNAT ASN list) in [`docs/rules/security-detail.md`](docs/rules/security-detail.md).

1. TLS 1.3 via **manual PEM files** (`tls.cert_file` / `tls.key_file`) — Hetzner (LE cron), Iranian DC (internal CA / cert-forge rsync), enterprise (customer root CA). One code path, zero outbound. Autocert + LE slips to v1.1.
2. ClickHouse localhost only (bound to 127.0.0.1, never exposed).
3. Hostname validation on `/api/event` (HMAC **skipped entirely** per doc 20 — hostname check is its own defense; Plausible/Umami do the same).
4. Input validation (`http.MaxBytesReader` 8KB, field length limits, timestamp ±1h drift).
5. Rate limiting per IP via `go-chi/httprate` (100 req/s, burst 200, NAT-aware via X-Forwarded-For / X-Real-IP).
6. Dashboard auth (bcrypt + `crypto/rand` sessions, 14-day TTL, `SameSite=Lax` cookies for CSRF).
7. RBAC (admin / viewer / API-only). 2FA deferred to v2.
8. Encrypted backups (`clickhouse-backup` + `age` + `zstd`, cron-scheduled, restore test on every release).
9. Disk encryption (LUKS **optional** — 40–50% I/O overhead; physical DC security + encrypted backups usually suffice).
10. Audit log (JSONL via `slog`, append-only, **file sink only** in v1). Syslog / remote sinks = v1.1.
11. User ID hashed before storage (SHA-256 of `master_secret || site_id || user_id`; never log raw user_id).
12. systemd hardening (`NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`, `CapabilityBoundingSet=CAP_NET_BIND_SERVICE`) + tracker via `go:embed` (first-party, no external CDN, ad-blocker-resistant).
13. **CGNAT-aware rate-limit tiering** — Iranian ASN (AS44244 Irancell / AS197207 MCI / AS57218 RighTel) on compound `(ip, site_id)` key at 1 K req/s sustained / 2 K burst; default 100/s fallback elsewhere; per-`site_id` global cap at 25 K req/s. ASN DB is **`iptoasn.com`** public-domain TSV (MaxMind GeoLite2 + IPLocate are CC-BY-SA — rejected per § License Rules). Enforced by [`ratelimit-tuning-review`](.claude/skills/ratelimit-tuning-review/README.md); **hard gate on Phase 10 Filimo cutover**.

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

## Feature Scope

Full roadmap in [`PLAN.md`](PLAN.md) — 51 v1 + 10 v1.1 + 17 v2 features across 20 weeks (docs 17/18/24). v1 = Filimo first 90 days + 5 Project Goals; polish → v1.1; product expansion → v2.

**Deliberate skips / Never:**
- **ClickHouse cluster at v1** — single-node is the rule; migrations Distributed-ready from day 1 per [`clickhouse-cluster-migration`](.claude/skills/clickhouse-cluster-migration/README.md).
- **Redis session cache** — breaks single-binary/air-gap; WAL + in-memory replaces it.
- **5-minute real-time** — rollup-hourly is the line; breaks cost model.
- **Bounce rate** — vanity metric (docs 09 / 14); expose time-on-page + funnel drop-off.
- **Multi-touch attribution** — last-touch channel grouping is final.

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
| Load smoke | k6 | `test/k6/load-test.js` |
| Frontend | Vitest | `web/src/**/*.test.tsx` |

**ClickHouse-Oracle Assertion Hierarchy.** Always use the highest applicable tier; lower tiers are diagnostic, not acceptance evidence.

| Tier | Method | Use When |
|---|---|---|
| 1 | **ClickHouse-oracle** — correlation query against rollups | Ingest, rollup correctness, attribution, multi-tenant |
| 2 | **Network** — `httptest.Server` / route interception | Tracker transport, sendBeacon payload |
| 3 | **DOM / locator** — Playwright or Vitest RTL | Dashboard UI state |
| 4 | **Screenshot** — `only-on-failure` | Debug artifact only |

**Analytics Invariant Thresholds (release-blocking, CI-asserted on every v1/v1.1 RC):**

- Event loss server ≤ 0.05%, client ≤ 0.5%; Duplicates ≤ 0.1%
- Attribution correctness ≥ 99.5%; Consent / PII leaks = 0; TTFB overhead ≤ +10% / +25 ms

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

## Single Source of Truth

`../statnive-workflow/jaan-to/docs/research/` (docs 14–28) is canonical for every architecture, feature, and threat-model decision. Do **not** restate research conclusions here or in skill prompts — reference by doc number and section. When a decision changes, update the research doc; this file references and never duplicates.

## Enforcement

Integration tests that pin the invariants in this file — full 6-test matrix + assertions in [`docs/rules/enforcement-tests.md`](docs/rules/enforcement-tests.md). Phase 0 / Phase 7 deliverables. `/simplify` and PR review reject regressions against this list on day one.

## Research Documents

Canonical source: `../statnive-workflow/jaan-to/docs/research/` (docs 14–28, 500+ sources).

- **Doc 23** — initial Claude Code tooling (30 skills + 4 MCP servers).
- **Doc 24** — AGPL-safe Pirsch extraction (pre-pipeline fast-reject, cross-day grace, cheap-first bot ordering, reject mutable-row engines, `DateTime` not `DateTime64`, templated DDL, 17-step channel tree, `Filter → Store → queryBuilder`, `whereTimeAndTenant`). Zero Pirsch code ported.
- **Doc 25** — Claude-skills install matrix + custom-skill catalog + explicit blacklist.
- **Doc 27** — three-gap closure: WAL durability (fsyncgate, ack-after-fsync), CGNAT rate limit (Iranian ASN, iptoasn.com), GDPR-on-HLL (Recital 26 + C-413/23).
- **Doc 28** — final-three-gap closure: GeoIP pipeline / Iranian DC deploy / ClickHouse ops + upgrade playbook.
