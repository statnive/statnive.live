# Never Break — the hard rules

> Single distilled index of the rules that, if violated, cause **lawsuit / breach / OFAC hit / platform failure / WP-scale reputational damage**. Every row points to a canonical source — this file never restates, only aggregates.
>
> Readers: PR reviewers, CI gate authors, onboarding devs, any agent about to touch security / privacy / dependency / deploy / ingestion code.
>
> **If you are about to break one of these**, stop. The fix is in the wrong place. Find the right one.

## How to read a row

```
<Rule> — <canonical source> → <enforcement> → <blast radius if violated>
```

- **Canonical source** is where the rule's normative text lives. If wording drifts, update the source, not this index.
- **Enforcement** is the automated gate (Semgrep rule, Go test, size-limit, skill) that rejects a violation. `—` means human PR review only.
- **Blast radius** is the worst-case consequence. Judge edge cases by it.

---

## 1. Privacy — Art. 28 / Recital 26 / CJEU C-413/23

The SaaS tier serves EU visitors. Every rule below is a **GDPR breach vector** if violated. Legal chain in [`privacy-detail.md`](privacy-detail.md).

| Rule | Source | Enforcement | Blast radius |
|---|---|---|---|
| Raw IP never persisted (used for GeoIP → discarded) | [CLAUDE.md § Privacy Rule 1](../../CLAUDE.md#privacy-rules-non-negotiable) | `test/integration/pii_leak_test.go` greps CH rows + audit log for IP-shaped substrings | GDPR fine + forced schema-wipe |
| `user_id` hashed as `SHA-256(master_secret ‖ site_id ‖ user_id)` before CH write; raw user_id never logged | [CLAUDE.md § Privacy Rule 4](../../CLAUDE.md#privacy-rules-non-negotiable) | `slog-no-raw-pii` Semgrep rule (`make privacy-gate`) + `gdpr-code-review` skill | DSAR breach; forced re-hash migration |
| Daily salt = `HMAC(master_secret, site_id ‖ YYYY-MM-DD IRST)`; derived, never stored | [CLAUDE.md § Privacy Rule 2](../../CLAUDE.md#privacy-rules-non-negotiable) | `blake3-hmac-identity-review` skill | Recital 26 anonymity argument collapses; re-identification becomes feasible |
| Salt rotation **deletes** the previous salt file (atomic rename + `os.Remove`) — never overwrites | [CLAUDE.md § Privacy Rule 8](../../CLAUDE.md#privacy-rules-non-negotiable) + [privacy-detail.md § Rule 8](privacy-detail.md#rule-8--salt-rotation-deletes-the-previous-salt-file) | `blake3-hmac-identity-review` + `gdpr-code-review` | Past-day hashes reversible via fs journal remnants; Recital 26 argument fails |
| `Sec-GPC: 1` / `DNT: 1` / consent-decline short-circuits **before** hash computation — not after | [CLAUDE.md § Privacy Rule 9](../../CLAUDE.md#privacy-rules-non-negotiable) + [privacy-detail.md § Rule 9](privacy-detail.md#rule-9--consent--gpc-short-circuit-before-hash-computation) | `gdpr-code-review` skill | Art. 4(2) "processing" breach even with never-stored hashes |
| SHA-256+ and BLAKE3 only — no MD5, no SHA-1 anywhere in the binary (incl. tests) | [CLAUDE.md § Privacy Rule 3](../../CLAUDE.md#privacy-rules-non-negotiable) | `blake3-hmac-identity-review` skill | Collision-feasible in session/identity paths → impersonation |
| First-party tracker via `go:embed` — no external CDN, no canvas / WebGL / font / audio-context / `navigator.plugins` fingerprinting | [CLAUDE.md § Privacy Rule 7](../../CLAUDE.md#privacy-rules-non-negotiable) + [privacy-detail.md § Rule 7](privacy-detail.md#rule-7--first-party-tracker-via-goembed) | `preact-signals-bundle-budget` + `gdpr-code-review` | Consent-of-nothing becomes fingerprinting; product category shifts from "privacy-first" to "tracker" |
| DNT + GPC respected by default on SaaS (self-hosted operator decides) | [CLAUDE.md § Privacy Rule 6](../../CLAUDE.md#privacy-rules-non-negotiable) | — | Non-compliance with GPC browser signals + ePrivacy |
| Iran = cookies + `user_id` allowed; SaaS = GDPR applies — runtime config gate `tenant.saas_mode`, not per-deploy rebuild | [CLAUDE.md § Privacy Rule 5](../../CLAUDE.md#privacy-rules-non-negotiable) + [privacy-detail.md § Rule 5](privacy-detail.md#rule-5--iran-vs-saas-tier-legal-posture) | — | Wrong-tier deploy → either Iran users get consent gated (product broken) or EU users get fingerprinted (lawsuit) |
| SaaS Art. 28(3) DPA upstream with Netcup **signed** before first EU visitor | [netcup-vps-gdpr.md § 2](netcup-vps-gdpr.md) | — | No controller→processor contract = unlawful processing under Art. 28(1) |
| SaaS sub-processor list published at `statnive.live/privacy`, updated within 7 days of Netcup Annex 2 change | [netcup-vps-gdpr.md § 3](netcup-vps-gdpr.md) | — | Art. 28(2) breach; customer DPAs void |

---

## 2. Licensing — AGPL carve-out, CC-BY-SA, go-licenses

statnive-live ships as SaaS outside Iran where **AGPL §13 applies**. One AGPL dep in the binary = we owe source disclosure to every SaaS visitor.

| Rule | Source | Enforcement | Blast radius |
|---|---|---|---|
| All **linked** deps are MIT/Apache/BSD/ISC — no AGPL in the binary | [CLAUDE.md § License Rules](../../CLAUDE.md#license-rules-critical) | `make licenses` + `go-licenses` in CI | AGPL §13 triggers; SaaS forced to publish full source |
| Do not import `pirsch-analytics/pirsch` (AGPL) — reference patterns only | [CLAUDE.md § License Rules](../../CLAUDE.md#license-rules-critical) | PR review; repo does not vendor it | Binary derivative-work taint |
| Do not import `knadh/koanf` (AGPL) — use viper (MIT) or env-only | [CLAUDE.md § License Rules](../../CLAUDE.md#license-rules-critical) | PR review | Same |
| Every new dep → `go-licenses` verification before merge | [CLAUDE.md § License Rules](../../CLAUDE.md#license-rules-critical) | `make licenses` CI job | Accidental AGPL/CC-BY-SA infusion |
| CC-BY-SA-4.0 carve-out is **data files only** (GeoIP BIN); never linked code | [CLAUDE.md § License Rules](../../CLAUDE.md#license-rules-critical) + [doc 28 §Gap 1](../../../jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md) | `geoip-pipeline-review` skill | Binary boundary violation; ShareAlike contamination |
| IP2Location LITE verbatim attribution string present in **all three** surfaces: `LICENSE-third-party.md`, `/about` JSON, dashboard footer | [CLAUDE.md § License Rules](../../CLAUDE.md#license-rules-critical) | `geoip-attribution-string-present` Semgrep in `geoip-pipeline-review` | CC-BY-SA §3(a)(1) breach; cease-and-desist from IP2Location |

---

## 3. Architecture — rollups, tenancy, enrichment, schema

Eight [Architecture Rules](../../CLAUDE.md#architecture-rules-non-negotiable) in CLAUDE.md. The five below are the most commonly violated and the hardest to reverse once shipped.

| Rule | Source | Enforcement | Blast radius |
|---|---|---|---|
| `events_raw` is WRITE-ONLY for the dashboard (except cached `windowFunnel()`) | [CLAUDE.md § Arch Rule 1](../../CLAUDE.md#architecture-rules-non-negotiable) | `tenancy-choke-point-enforcer` + `clickhouse-operations-review` | Query cost blows up; P99 latency → O(billions) rows scanned |
| Every dashboard SQL path through `internal/storage/queries.go:whereTimeAndTenant()`; `WHERE site_id = ?` is the first clause | [CLAUDE.md § Arch Rule 8](../../CLAUDE.md#architecture-rules-non-negotiable) + [doc 24 §Sec 4 pattern 6](../../../jaan-to/docs/research/24-pirsch-pattern-extraction-reference-only.md) | `tenancy-choke-point-enforcer` Semgrep (CI fail) | Cross-tenant data leak; catastrophic multi-tenancy breach |
| No `Nullable(...)` on analytics columns — `DEFAULT ''` / `DEFAULT 0` + typed sentinels for test-instrumentation | [CLAUDE.md § Arch Rule 5](../../CLAUDE.md#architecture-rules-non-negotiable) | `clickhouse-operations-review` Semgrep (requires `-- NULLABLE-OK:` comment) | 10–200% aggregation regression; breaks the 8c/32GB → 200M events/day ceiling |
| Enrichment order locked: identity → bloom → GeoIP → UA → bot → channel | [CLAUDE.md § Arch Rule 6](../../CLAUDE.md#architecture-rules-non-negotiable) + [doc 22 §GAP 1](../../../jaan-to/docs/research/22-statnive-ingestion-pipeline-10-critical-gaps-drop-in-go-code.md) | `test/integration/enrichment_order_test.go` | Attribution corruption (bot-as-user, geo-before-identity race) |
| Pre-pipeline fast-reject gate (UA 16–500, non-ASCII, IP-as-UA, UUID-as-UA, `X-Purpose`/`X-Moz`) → `204` | [CLAUDE.md § Arch Rule 6](../../CLAUDE.md#architecture-rules-non-negotiable) + [doc 24 §Sec 1 item 6](../../../jaan-to/docs/research/24-pirsch-pattern-extraction-reference-only.md) | Integration test (10-case fast-reject table) | Bot noise in rollups → attribution accuracy drops below 99.5% gate |
| `events_raw` time column is `DateTime('UTC')` (seconds) — never `DateTime64` | [CLAUDE.md § Stack](../../CLAUDE.md#stack) + [doc 24 §Sec 2](../../../jaan-to/docs/research/24-pirsch-pattern-extraction-reference-only.md) | `clickhouse-rollup-correctness` skill | 2× storage; rollup MVs go subtly wrong at DST-less IRST boundaries |
| Reject mutable-row engines (Collapsing / VersionedCollapsing) | [CLAUDE.md § Stack](../../CLAUDE.md#stack) | `clickhouse-rollup-correctness` + `clickhouse-operations-review` | Rollup merge non-determinism; `-State`/`-Merge` correctness breaks |
| Migrations use `{{if .Cluster}}` templates from day 1 | [CLAUDE.md § Stack](../../CLAUDE.md#stack) + [doc 24 §Migration 0029](../../../jaan-to/docs/research/24-pirsch-pattern-extraction-reference-only.md) | `clickhouse-cluster-migration` skill | Single-node → cluster upgrade requires rewriting every DDL; dead-end at Phase C scale |
| Client-side batching in Go via WAL (batch 500ms / 1000 rows / 10MB) — async inserts are safety valve only | [CLAUDE.md § Arch Rule 4](../../CLAUDE.md#architecture-rules-non-negotiable) | `wal-durability-review` skill | Ingest loss > 0.05% server SLO; WAL replay fails to recover |
| WAL: fsync **before** 202 ack; Sync errors are terminal (exit, not retry) | [CLAUDE.md § Arch Rule 4](../../CLAUDE.md#architecture-rules-non-negotiable) + [doc 27 Gap A](../../../jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md) | `wal-durability-review` skill + `make wal-killtest` (nightly 50-iter) | fsyncgate-class silent data loss |
| `TruncateFront` on WAL only **after** ClickHouse commit | [doc 27 Gap A](../../../jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md) | `wal-durability-review` skill | Events acked to client but lost on crash |

---

## 4. Security — TLS, auth, rate-limit, systemd

[14 Security items in CLAUDE.md](../../CLAUDE.md#security-14-features-all-v1). Extended reasoning in [`security-detail.md`](security-detail.md).

| Rule | Source | Enforcement | Blast radius |
|---|---|---|---|
| TLS 1.3 only via manual PEM (`tls.cert_file` / `tls.key_file`); binary never calls ACME | [CLAUDE.md § Security 1](../../CLAUDE.md#security-14-features-all-v1) | `iranian-dc-deploy` skill (`iran-no-letsencrypt-in-binary`) + `air-gap-validator` | Outbound-dependency baked into the air-gap-contracted path |
| ClickHouse bound to 127.0.0.1 only | [CLAUDE.md § Security 2](../../CLAUDE.md#security-14-features-all-v1) | Integration test `security_test.go` | Unauthenticated CH exposed to internet |
| `http.MaxBytesReader` 8KB, field-length caps, ±1h timestamp drift | [CLAUDE.md § Security 4](../../CLAUDE.md#security-14-features-all-v1) | Integration test | Memory-DoS via oversized body; replay attacks |
| Dashboard auth: bcrypt + `crypto/rand` 32-byte session tokens, 14-day TTL, `SameSite=Lax` HttpOnly cookies | [CLAUDE.md § Security 6](../../CLAUDE.md#security-14-features-all-v1) + [PLAN.md Phase 2b](../../PLAN.md) | `internal/auth/nilguard_test.go` (hard gate) | Session fixation; CVE-2024-10924-class bypass |
| `internal/httpjson.DecodeAllowed` is the single F4 mass-assignment guard on all admin routes | [PLAN.md Phase 3c](../../PLAN.md) | Integration test `TestDecodeAllowed_RejectsMassAssignmentAttack` | Privilege escalation via JSON-body field injection |
| CGNAT-aware rate-limit tiering — Iranian ASN list (AS44244 Irancell / AS197207 MCI / AS57218 RighTel / AS31549 Shatel / AS43754 Asiatech) on compound `(ip, site_id)` at 1K/s sustained / 2K/s burst; default 100/s elsewhere; per-`site_id` cap 25K/s | [CLAUDE.md § Security 13](../../CLAUDE.md#security-14-features-all-v1) + [doc 27 Gap B](../../../jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md) | `ratelimit-tuning-review` skill — **HARD GATE on Phase 10 SamplePlatform cutover** | Iranian mobile CGNAT blanket-throttled → SamplePlatform cutover fails |
| ASN DB is **`iptoasn.com`** public-domain TSV only; MaxMind GeoLite2 + IPLocate rejected (CC-BY-SA) | [CLAUDE.md § Security 13](../../CLAUDE.md#security-14-features-all-v1) | `ratelimit-tuning-review` skill | License rule (§2) violation by proxy |
| Outbound allow-list for opt-in features (ACME, Polar, DB23 download, Telegram, SMTP) routes through `internal/httpclient/guarded.go`: FQDN allow-list + RFC-1918 / CGNAT-100.64 reject after DNS + `https://` forced | [CLAUDE.md § Security 14](../../CLAUDE.md#security-14-features-all-v1) | `airgap-no-raw-httpclient` Semgrep in `air-gap-validator` + unit test | OWASP A10 SSRF; DNS rebinding; air-gap guarantee broken |
| Audit log: `slog` JSONL, file sink only in v1, `O_APPEND`, `Reopen()` for logrotate, `chattr +a` in prod | [CLAUDE.md § Security 10](../../CLAUDE.md#security-14-features-all-v1) | PR review | Audit trail tampering |
| systemd hardening: `NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`, `CapabilityBoundingSet=CAP_NET_BIND_SERVICE`, `SystemCallFilter=@system-service`, `RestrictAddressFamilies` | [CLAUDE.md § Security 12](../../CLAUDE.md#security-14-features-all-v1) | `deploy/systemd/harden-verify.sh` via `make systemd-verify` | Post-RCE escalation; container-escape analog on VPS |
| No raw PII in `slog` output | [PLAN.md Phase 7d F3](../../PLAN.md) | `slog-no-raw-pii` Semgrep via `make privacy-gate` + `semgrep-identity-privacy` CI job | DSAR grep leak; GDPR breach |
| TOCTOU-safe file loading via `os.OpenRoot` for master-secret and bloom files | [PLAN.md Phase 7d F7](../../PLAN.md) | Symlink-escape regression tests in `internal/config` + `internal/enrich` | Symlink swap during race → key exfiltration |
| `govulncheck` + CodeQL + Semgrep run on every PR via `.github/workflows/security-gate.yml` | [PLAN.md Phase 7d](../../PLAN.md) | CI job | Supply-chain / logic / known-CVE regressions |

---

## 5. Isolation / air-gap — zero outbound by default

The binary must run with `iptables -P OUTPUT DROP` and **every endpoint still works**. Every network feature is opt-in.

| Rule | Source | Enforcement | Blast radius |
|---|---|---|---|
| Zero required outbound connections in default build | [CLAUDE.md § Isolation](../../CLAUDE.md#isolation--air-gapped-capability-non-negotiable) | Integration test under `iptables -A OUTPUT -j DROP` | Iranian DC / enterprise on-prem deploys fail closed |
| Tracker JS + Preact SPA + all deps via `go:embed` + `go mod vendor` — no `go mod download` / no CDN at runtime | [CLAUDE.md § Isolation](../../CLAUDE.md#isolation--air-gapped-capability-non-negotiable) | `preact-signals-bundle-budget` + `air-gap-validator` Semgrep | Runtime fetch kills air-gap |
| License verify is offline Ed25519 JWT only — no phone-home, not even for telemetry | [CLAUDE.md § Anti-patterns](../../CLAUDE.md#anti-patterns-doc-28-anti-patterns--absolute-bans) | `iran-license-verify-must-be-offline` Semgrep in `iranian-dc-deploy` | OFAC 31 CFR 560.540(b)(3) breach ("services rendered" exclusion) |
| GeoIP updates via manual file drop + SIGHUP reload; **no `fsnotify`** | [CLAUDE.md § Anti-patterns](../../CLAUDE.md#anti-patterns-doc-28-anti-patterns--absolute-bans) + [doc 28 §Anti-patterns](../../../jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md) | `geoip-no-fsnotify-on-bin` Semgrep in `geoip-pipeline-review` | overlayfs/NFS/kqueue lose events silently → stale GeoIP indefinitely |
| Tracker: no canvas, no WebGL, no font-availability, no audio-context, no `navigator.plugins` enumeration | [privacy-detail.md § Rule 7](privacy-detail.md#rule-7--first-party-tracker-via-goembed) | `preact-signals-bundle-budget` + `gdpr-code-review` | Fingerprinting surface → GDPR + product-category breach |

---

## 6. Iran / OFAC — absolute bans for IR-resident code paths

Enforced by [doc 28 §Anti-patterns](../../../jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md). OFAC 31 CFR 560.540(b)(3) excludes "services rendered" — silent phone-home is a sanctions violation.

| Rule | Source | Enforcement | Blast radius |
|---|---|---|---|
| **No Cloudflare** on any IR-resident code path (no IR POP + OFAC) | [CLAUDE.md § Anti-patterns](../../CLAUDE.md#anti-patterns-doc-28-anti-patterns--absolute-bans) | `iran-no-cloudflare` Semgrep in `iranian-dc-deploy` | OFAC breach; IR tracker requests never arrive |
| **No ACME / Let's Encrypt from inside Iran** — cert issued outside IR, rsync'd inward, SIGHUP swap | [CLAUDE.md § Anti-patterns](../../CLAUDE.md#anti-patterns-doc-28-anti-patterns--absolute-bans) | `iran-no-letsencrypt-in-binary` Semgrep | Same |
| **Never ArvanCloud** (sanctioned + 2022 breach). Asiatech primary, ParsPack / Shatel backup | [CLAUDE.md § Anti-patterns](../../CLAUDE.md#anti-patterns-doc-28-anti-patterns--absolute-bans) | PR review | OFAC + documented breach exposure |
| **No phone-home license check, not even telemetry** — offline Ed25519 JWT, zero `net.Dial` | [CLAUDE.md § Anti-patterns](../../CLAUDE.md#anti-patterns-doc-28-anti-patterns--absolute-bans) | `iran-license-verify-must-be-offline` Semgrep | Same |
| **No `OPTIMIZE TABLE ... FINAL` without `PARTITION`** — serializes merges, OOMs 8c/32GB, non-idempotent on AggregatingMergeTree | [CLAUDE.md § Anti-patterns](../../CLAUDE.md#anti-patterns-doc-28-anti-patterns--absolute-bans) | `ch-ops-no-optimize-final-in-sql` Semgrep in `clickhouse-operations-review` | SamplePlatform cluster OOM during a match-day spike |
| **Never default-enable exception-telemetry as a tracker event type** — opt-in with 10/session cap + 1-in-10 sampling + per-tenant quota | [CLAUDE.md § Anti-patterns](../../CLAUDE.md#anti-patterns-doc-28-anti-patterns--absolute-bans) + [doc 30 §app_exception](../../../jaan-to/docs/research/30-ga4-calibration-delta.md) | PR review | Event budget consumed by crash-log noise; tenant rollups become noise-dominated |

---

## 7. Performance / cost — release-blocking thresholds

[CI-asserted on every v1/v1.1 RC](../../CLAUDE.md#testing). Any breach halts the graduation gate.

| Threshold | Source | Enforcement | Blast radius |
|---|---|---|---|
| Event loss server ≤ 0.05% | [CLAUDE.md § Testing](../../CLAUDE.md#testing) | k6 + graduation gate | Under-counting → customer churn |
| Event loss client ≤ 0.5% | [CLAUDE.md § Testing](../../CLAUDE.md#testing) | Tracker Vitest + Playwright | Same |
| Duplicates ≤ 0.1% | [CLAUDE.md § Testing](../../CLAUDE.md#testing) | generator_seq oracle (doc 29 §6.2) | Over-counting → revenue attribution wrong |
| Attribution correctness ≥ 99.5% | [CLAUDE.md § Testing](../../CLAUDE.md#testing) | Integration test vs. reference `EnrichedEvent` | RPV metric becomes unusable |
| Consent / PII leaks = 0 | [CLAUDE.md § Testing](../../CLAUDE.md#testing) | `slog-no-raw-pii` + `pii_leak_test.go` | GDPR breach |
| TTFB overhead ≤ +10% or +25 ms | [CLAUDE.md § Testing](../../CLAUDE.md#testing) | Tracker perf test | Customer site speed regression = forced uninstall |
| Design-ceiling EPS — P5 = 40K burst (200M events/day) | [PLAN.md Context](../../PLAN.md) | Graduation gate `make load-gate PHASE=P5` | SamplePlatform Friday-derby outage |
| Frontend initial JS ≤ 16 KB gz; uPlot chunk ≤ 25 KB gz; lazy panels ≤ 10 KB gz; main CSS ≤ 5 KB gz; per-panel CSS ≤ 3 KB gz | [PLAN.md Phase 5e](../../PLAN.md) | `web/.size-limit.json` in `npm run test` | Dashboard cold-start over budget → PR blocked |
| Tracker bundle ~1.2 KB min / ~600 B gz | [CLAUDE.md § Stack](../../CLAUDE.md#stack) | `preact-signals-bundle-budget` size-limit | Tracker cold-load regression |
| 72h soak + 6-scenario chaos matrix + breakpoint — HARD GATE on Phase 10 cutover | [CLAUDE.md § Testing](../../CLAUDE.md#testing) + [doc 29 §4](../../../jaan-to/docs/research/29-Production-load-simulation-gate-statnive-live-asiatech-tehran.md) | `make load-gate` | Production incident during SamplePlatform launch |

---

## 8. Release / CI / workflow gates

| Rule | Source | Enforcement | Blast radius |
|---|---|---|---|
| `/simplify` before every commit under `cmd/**`, `internal/**`, `web/**`, `tracker/**`, `clickhouse/**`, `config/**`, `deploy/**`, `.claude/**` | [CLAUDE.md § Workflow Rule](../../CLAUDE.md#workflow-rule--always-simplify-before-committing) | `.githooks/pre-commit` | Duplicated / dead / hacky code enters the tree |
| Every new dep → `go-licenses` pass + `/simplify` review of the dep-added diff | [CLAUDE.md § Workflow Rule](../../CLAUDE.md#workflow-rule--always-simplify-before-committing) | `make licenses` CI | AGPL / CC-BY-SA infusion |
| Every API endpoint → integration test | [CLAUDE.md § Workflow Rule](../../CLAUDE.md#workflow-rule--always-simplify-before-committing) | `make test-integration` | Untested surface → regression + SSRF / auth-bypass latent |
| ClickHouse schema change → numbered migration file + `{{if .Cluster}}` templating | [CLAUDE.md § Workflow Rule](../../CLAUDE.md#workflow-rule--always-simplify-before-committing) + [doc 24 §Migration 0029](../../../jaan-to/docs/research/24-pirsch-pattern-extraction-reference-only.md) | `clickhouse-cluster-migration` skill | Out-of-band DDL; upgrade path broken |
| `goals` / `funnels` config hot-reloads via SIGHUP — no binary restart | [CLAUDE.md § Workflow Rule](../../CLAUDE.md#workflow-rule--always-simplify-before-committing) | PR review + `internal/goals/` atomic.Pointer test | Operator availability loss on every config change |
| Pre-commit hook re-runs `make test && make lint` + frontend Vitest; rejects regressions | [CLAUDE.md § Test Gate](../../CLAUDE.md#test-gate) | `.githooks/pre-commit` | Broken tests ride into main |
| **Shipped code must be executed in CI** (not compile-checked) — new tests ride an existing CI job or add one in the same PR | [PLAN.md Phase 7b2-completion](../../PLAN.md) | CI workflow count | "Shipped" code silently unrun for weeks |
| `statnive-workflow` parent repo: commit to `main`; `statnive-live` + `statnive` submodules: feature branch + PR | user memory | — | Submodule branch-rule violation = merge conflicts on pointer bumps |

---

## 9. Product definition — deliberate skips (never ship these)

These are **product-defining rejections**. Shipping any of them changes what statnive-live *is*.

| Skip | Source | Reason | Substitute |
|---|---|---|---|
| 5-minute real-time | [CLAUDE.md § Deliberate skips](../../CLAUDE.md#feature-scope) | 98% query cost breaks the model | 1-hour rollup delay |
| Bounce rate | [CLAUDE.md § Deliberate skips](../../CLAUDE.md#feature-scope) | Vanity metric (doc 09 / 14) | Time-on-page + funnel drop-off |
| Multi-touch attribution | [CLAUDE.md § Deliberate skips](../../CLAUDE.md#feature-scope) | Complexity without decision value | Last-touch channel grouping |
| Session replay / DOM snapshot | [CLAUDE.md § Deliberate skips](../../CLAUDE.md#feature-scope) + [doc 32 §A](../../../jaan-to/docs/research/32-posthog-statnive-deep-research.md) | Privacy surface + storage cost | "Frustration signals": rage clicks, Web Vitals, `$pageleave` (v1.1 Safe Autocapture Pack, **opt-in**) |
| Default-on autocapture | [CLAUDE.md § Deliberate skips](../../CLAUDE.md#feature-scope) + [doc 34 §E](../../../jaan-to/docs/research/34-posthog-mechanics-and-portability-to-statnive.md) | Consent-invalidating | `<script data-pack="safe">` opt-in per tenant |
| Remote agentic installer wizard | [CLAUDE.md § Deliberate skips](../../CLAUDE.md#feature-scope) + [doc 34 §A](../../../jaan-to/docs/research/34-posthog-mechanics-and-portability-to-statnive.md) | Air-gap violation + PostHog issues w/ Astro frontmatter / `.env` | `v1.1-install-cli` (deterministic, zero-LLM, zero-outbound) |
| Cloud-mediated AI memory / `/init` persistent context | [CLAUDE.md § Deliberate skips](../../CLAUDE.md#feature-scope) + [doc 34 §B](../../../jaan-to/docs/research/34-posthog-mechanics-and-portability-to-statnive.md) | Air-gap violation | Local-model or pluggable-provider, air-gap default |
| Broad MCP mutation surface | [CLAUDE.md § Deliberate skips](../../CLAUDE.md#feature-scope) + [doc 34 § Decision matrix](../../../jaan-to/docs/research/34-posthog-mechanics-and-portability-to-statnive.md) | Write from agent = auth escalation vector | v2 MCP stays **read-only**; writes via authenticated admin API only |
| Redis session cache | [CLAUDE.md § Deliberate skips](../../CLAUDE.md#feature-scope) | Breaks single-binary / air-gap | WAL + in-memory |
| ClickHouse cluster at v1 | [CLAUDE.md § Deliberate skips](../../CLAUDE.md#feature-scope) | Single-node suffices to 200M events/day (doc 19) | Migrations `{{if .Cluster}}`-templated from day 1 |

---

## 10. How to extend this index

- **Add a row** only when a rule is enforced (CI, Semgrep, size-limit, integration test, `make` target, skill) **or** has a legal / sanctions / compliance blast radius that makes PR review the enforcement.
- **Every row links to a canonical source.** If you cannot link one, the rule is not ready to be "never break" — upgrade the source first.
- **When CLAUDE.md / PLAN.md / rule-file wording changes**, re-check the `#fragment` anchor in the link column only. Do not restate the rule here.
- **Conflicts:** if two sources disagree, flag in PR and fix the source. Do not paper over in this file.

---

## See also

- [CLAUDE.md](../../CLAUDE.md) — project conventions
- [PLAN.md](../../PLAN.md) — phase-by-phase roadmap
- [privacy-detail.md](privacy-detail.md) — GDPR legal chain
- [security-detail.md](security-detail.md) — 14-item security detail
- [enforcement-tests.md](enforcement-tests.md) — 6-test integration matrix
- [netcup-vps-gdpr.md](netcup-vps-gdpr.md) — Netcup Art. 28 DPA + VPS hardening
- [research/](../../../jaan-to/docs/research/) — docs 14–36 (canonical architecture / threat-model SoT)
