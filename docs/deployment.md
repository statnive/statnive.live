# Deployment — SaaS + air-gap + server cost

> Referenced from [PLAN.md](../PLAN.md). Three deployment-mode reference sections consolidated here. Agent reading this file: only needed when provisioning infrastructure or triaging a deployment mismatch.

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

- **Pre-paying-customers (dogfood):** Netcup VPS 2000 G12 iv NUE hourly-based (€25.48/mo + €5 one-time setup, 8 vCore AMD EPYC x86_64 / 16 GB DDR5 ECC / 512 GB NVMe / 2.5 Gbit unlimited / Nuremberg, Germany / IPv4 + IPv6 / no contract lock-in — **procured 2026-04-24** per [research doc 36 §4.1](../../jaan-to/docs/research/36-devops-hetzner-saas-vps-selection-2026.md) as the fallback to Hetzner CX43; Hetzner's photo-ID doc-verification blocks signup right now). **Live IPs:** IPv4 `94.16.108.78`, IPv6 `2a03:4000:51:f0c::1` (chosen from the Netcup-assigned `2a03:4000:51:f0c::/64` subnet, bound on the VM via netplan per [`runbook.md` § Bind IPv6 on Netcup VM](runbook.md)). Hosts statnive.com + a handful of free-tier trials. Fixed cost, no per-customer math yet. Tracker origin is public-facing — BOTH IPv4 and IPv6 enabled at provisioning. Hetzner CX43/AX41 remains documented as the future Phase C growth-tier option once doc-verification is resolved.
- **First ~10 paying customers:** AX41 (~€39/mo) — comfortably handles 10–30 sites at 1M PV/mo each. **~€1.30–3.90/mo per customer**; ~90% gross margin at $19/mo pricing.
- **~30–50 customers:** AX42 (€46/mo) safely handles 30–50 sites at 1M PV/mo each. 100 sites × 1M PV/mo = ~13.5K EPS — above the 7K EPS proven load ceiling, so don't over-pack AX42.
- **100+ customers:** AX102 (€104/mo) or horizontal shard. Revisit architecture when we get there.

## Server Costs

| Stage | Server | Monthly | Annual |
|-------|--------|---------|--------|
| **Phase A dogfood (v1)** | Netcup VPS 2000 G12 NUE hourly (8 vCore EPYC / 16 GB DDR5 ECC / 512 GB NVMe / Nuremberg) — procured 2026-04-24 per doc 36 §4.1 fallback (Hetzner doc-verification pending); +€5 one-time setup | **~€25.48** | **~€311** (incl. €5 setup in year 1) |
| Phase C first paying tier (~10 customers) | Hetzner AX41 (6c/64GB/2×512GB) | **~€39** | **~€468** |
| Phase C growth (~50–100 customers) | Hetzner AX42 (8c/64GB/1TB) | **€46** | **€552** |
| Phase C scale (100+ customers) | Hetzner AX102 (16c/128GB/4TB) | **€104** | **€1,248** |
| SamplePlatform (Phase B) | 8c/32GB/1TB NVMe Iranian DC (Asiatech / Shatel / Afranet) | **~€180** | **~€2,160** |

**Notes:**
- **Start small:** Netcup VPS 2000 G12 NUE (€25.48/mo + €5 setup / ~€311 year 1 — procured 2026-04-24 per [research doc 36 §4.1](../../jaan-to/docs/research/36-devops-hetzner-saas-vps-selection-2026.md) with Hetzner-doc-verification fallback) handles statnive.com dogfood traffic (<100K PV/mo) for ~44% of AX42's cost. Upgrade to Hetzner AX42 (if doc-verification resolved) or Netcup Root Server RS 2000/4000 G12 when SaaS load demands it. Saves ~€241/yr in year 1 vs. starting directly on AX42 (€552/yr). Note: the monthly/hourly billing premium vs. Netcup's 12-month prepaid (~€14/mo) is ~€137/yr — the trade-off buys cancellation flexibility during Phase A uncertainty.
- Iranian DCs are quote-based (not public pricing). Upfront CAPEX on custom bare-metal builds; monthly figure is colocation + bandwidth only.
- **Customer Iranian DC sizing is phase-dependent**, not a single number. P1/P2 (StreamCo MIN, web only) runs on an Asiatech G2 standard VPS (~28M Rial/mo). P3 (+iOS) needs bandwidth upgrade or small dedicated. P4/P5 (StreamCo MAX, full fidelity) is a 2–3 node cluster. See the 5-phase table in PLAN.md § Phase 10 and [`../../jaan-to/outputs/capacity-planning-standalone-analytics.md`](../../jaan-to/outputs/capacity-planning-standalone-analytics.md) for monthly bandwidth / disk / EPS per sub-phase.
- **Bandwidth envelope by sub-phase** (StreamCo profile, at 300 B/event optimized): P1 ~22 GB/mo (MIN), P2 ~105 GB/mo, P3 ~420 GB/mo, P4 ~900 GB/mo, P5 ~1.2 TB/mo (MAX). All Asiatech standard VPS tiers cap at 150 GB/mo — upgrade conversation lands at P3, not at initial cutover.
- IP2Location paid DB23 subscription only on D2 (SamplePlatform) in v1. LITE DB23 on D1 (free, attribution required).

## Air-Gapped / Isolated Deployment

The final platform runs as a **single, self-contained binary on one server with zero required outbound connections**. This is a core product requirement, not an edge case — SamplePlatform's Iranian DC is assumed internet-restricted, and enterprise self-hosted customers may deploy behind corporate firewalls.

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
- `IP2LOCATION-LITE-DB23.BIN` (or licensed DB23 BIN for SamplePlatform)
- `clickhouse-backup` + `age` binaries
- `schema.sql` + `migrations/`
- `deploy/` scripts (systemd, iptables, backup, airgap-install, airgap-update-geoip)
- `SHA256SUMS` + detached Ed25519 signature

**Docker tarball (`docker save`) deferred to v1.1** — static binary is one file, runs anywhere; Docker-based installs are a convenience layer that adds bundle size + CI time without unblocking any of the 5 goals. Revisit when an operator actually asks for it.

### Mandatory external services

**NONE.**

### Opt-in external services (all OFF by default in air-gapped mode)

| Service | Purpose | Disable via config |
|---|---|---|
| Let's Encrypt (ACME) | TLS cert issuance | v1 uses manual PEM only — LE never called from the binary. Operator obtains certs separately via `certbot certonly --manual` and drops PEMs. |
| Telegram Bot API | Operator alerts | v1.1 only — v1 ships the file sink at `/var/log/statnive-live/alerts.jsonl` (Phase 8; event taxonomy + grep recipes in [runbook.md § Alerts file format](runbook.md#alerts-file-format-varlogstatnive-livealertsjsonl)) |
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
- Optional: internal CA + root cert distributed to tracker-embedding clients (for SamplePlatform's corporate trust store)
