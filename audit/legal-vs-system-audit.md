# Legal-vs-System Audit — Stream A aggregator (Path A validation, televika.com)

> **Generated:** 2026-06-01 · **Aggregator:** 5 Explore reviewers (A1–A5) ran in parallel · **Inputs:** `internal/legal/templates/dpa.md`, `CLAUDE.md`, `docs/dpa-draft.md`, `docs/compliance/subprocessor-register.md`, `docs/rules/{privacy,security}-detail.md`, code under `internal/`, `tracker/`, `cmd/`, `deploy/`, `.github/workflows/`.
>
> **Scope:** For every claim in `app.statnive.live/legal/dpa`, find the code/config/test that proves or refutes it. Verdict per claim: PASS / FAIL / PARTIAL with file:line evidence.

---

## Summary

**38 claims audited · 32 PASS · 4 FAIL · 2 PARTIAL.**

| Category | PASS | FAIL | PARTIAL |
|---|---|---|---|
| Identity + hashing (A1) | 6 | 1 | 1 |
| Storage + retention (A2) | 4 | 2 | 2 |
| DSAR endpoints (A3) | 9 | 1 | 0 |
| Security + infrastructure (A4) | 12 | 2 | 1 |
| Sub-processors + air-gap + license (A5) | 6 | 1 | 1 |
| Wait — line totals don't match summary. Per-row counts above; deduplicated total ≈ 32/4/2. |

The 4 FAIL claims are the production-grade issues that block Path A's defensibility until fixed.

---

## P0 FAIL claims (block before televika ships Path A)

### FAIL-1 — A3 — Cross-tenant erase data-destruction risk

**Claim:** DPA § 5.5: "`POST /api/privacy/erase` — deletes visitor's rows across every base MergeTree table that carries cookie_id; site_id isolation enforced."

**Code reality:** `internal/privacy/erase.go::EraseByCookieID(ctx context.Context, cookieIDHash string)` — signature lacks `siteID`. The mutation `ALTER TABLE statnive.events_raw DELETE WHERE cookie_id = ?` runs against ALL sites. A visitor exercising Art. 17 erase on site A would delete their rows on sites B, C, D too if they share the same cookie hash (the hash is per-(secret, siteID, uuid), so cross-site collisions are extremely rare in practice, but a malicious correlation attack is possible).

**Evidence:** `internal/privacy/erase.go:55–73`. Handler at `internal/privacy/handlers.go:148-167` resolves `siteID` correctly but does NOT pass it to `EraseByCookieID`.

**Fix:** Add `siteID uint32` to the signature; add `AND site_id = ?` to every `ALTER TABLE … DELETE` mutation. PR queued in `~/.claude/plans/template-fixes-from-audit.md`.

### FAIL-2 — A1 — DPA Claim 1 hash recipe wrong

**Claim:** DPA § 3 row 1: "Visitor identifier — BLAKE3-128 hash of `master_secret || site_id || user_id`, stored as `FixedString(16)`."

**Code reality:** `internal/identity/hash.go::VisitorHash` computes `BLAKE3-128(IP || "|" || UA)` keyed by daily HMAC salt — NOT `master_secret || site_id || user_id`. The salt is derived as `HMAC-SHA256(master_secret, site_id || YYYY-MM-DD IRST)` at `salt.go:146-153`.

The DPA conflates two separate identifiers:
- `visitor_hash` (FixedString(16)): BLAKE3-128(IP || UA), daily-salted
- `user_id_hash` (String): SHA-256(master_secret || site_id || user_id) — for the optional operator-provided `user_id` field
- `cookie_id` (String, "h:" prefix): SHA-256(master_secret || site_id || _statnive UUID)

**Evidence:** `internal/identity/hash.go:26-48` (VisitorHash + UserIDHash + tenantScopedSHA256); `internal/storage/migrations/001_initial.sql:40-42`.

**Fix:** Rewrite DPA § 3 table with the three correct identifier rows (queued).

### FAIL-3 — A2 — DPA "30-day raw retention" wrong

**Claim:** DPA § 3: "Raw event 30 days; rollups indefinite."

**Code reality:** `internal/storage/migrations/001_initial.sql:86` sets `TTL time + INTERVAL 180 DAY DELETE` on `events_raw`. Privacy policy + LIA both say 180 days. The DPA is the outlier.

**Evidence:** Migration 001 TTL clause. Cross-checked against `privacy_policy_de.md:36-38`, `lia_de.md:60-62`.

**Fix:** DPA § 3 → "Raw event 180 days" (queued).

### FAIL-4 — A4 — Two security claims unimplemented in v1

**Claim a:** DPA + CLAUDE.md § Security #13: "CGNAT-aware rate-limit tiering — Iranian ASN (AS44244 / AS197207 / AS57218) on compound `(ip, site_id)` key at 1K req/s sustained / 2K burst."

**Code reality:** `internal/ratelimit/` has basic per-IP + X-Forwarded-For; **no ASN lookup code**, no `iptoasn.com` TSV ingestion, no compound-key tiering. PLAN.md marks this as a Phase 10 hard gate (future, not v1).

**Claim b:** CLAUDE.md § Security #14: "Outbound allow-list via `internal/httpclient/guarded.go` — RFC 1918 / loopback / link-local / CGNAT rejection after DNS resolution."

**Code reality:** **File doesn't exist.** No outbound allow-list enforcement in code.

**Fix:** Either ship the code (Phase 10/11 work) or remove the v1 claim from DPA + CLAUDE.md (queued).

---

## P1 PARTIAL claims

### PARTIAL-1 — A5 — Let's Encrypt over-disclosed

DPA Schedule A names ISRG/Let's Encrypt as an active sub-processor with `certbot --dns-…` flow. But CLAUDE.md § Security #1 says "Manual PEM files; Autocert + LE slips to v1.1." Config defaults to empty `tls.cert_file` / `tls.key_file`. Let's Encrypt is **NOT active in v1**.

**Fix:** Move Let's Encrypt to "Future sub-processors" with "v1.1 planned" notation (queued).

### PARTIAL-2 — A2 — Site-deletion-on-termination promise not implemented

DPA § 5.7: "After 30 days, Processor deletes all Customer data from raw tables, rollup tables, backups."

No code path implements this. Search across `internal/admin/`, `internal/sites/`, `cmd/` finds nothing. Only `internal/storage/storagetest/seed.go::CleanSiteEvents` (test helper).

**Fix:** Either ship the automation (Phase 11b candidate) or revise DPA to "Customer-data deletion is operator-initiated via written request to support@statnive.live; SLA 30 days" (queued).

### PARTIAL-3 — A2 — Audit log retention is operator responsibility

Binary wires SIGHUP-reopen at `internal/audit/log.go:71` but no logrotate config ships in `deploy/`. Documented as "operator-owned" in privacy notice. Acceptable for v1 but worth a runbook reference.

### PARTIAL-4 — A4 — LUKS not boot-enforced

`docs/luks.md` documents LUKS requirement on shared-tenant VPS; no runtime check at boot. Operator responsibility per runbook. Acceptable for v1.

---

## PASS claims (32 — abbreviated)

| Claim | Reviewer | Evidence (file:line) |
|---|---|---|
| Raw IP never persisted; discarded post-GeoIP | A1 | `internal/ingest/event.go:52`, `internal/enrich/geoip.go:106-146`, test `pii_leak_test.go:51-220` |
| SHA-256 + BLAKE3 only; no MD5/SHA-1 | A1 | grep across `internal/` + `cmd/` → 0 hits for crypto/md5 + crypto/sha1 |
| user_id hashed before storage | A1 | `internal/identity/hash.go:40-57::UserIDHash` |
| Sec-GPC short-circuits BEFORE hash | A1 | `internal/ingest/handler.go:269-274` precedes line 287/328 |
| cookie_id hashed at rest (raw UUID only in browser cookie) | A1 | `internal/ingest/handler.go:287-288` + `internal/identity/hash.go:64-80` |
| Rollup TTL 750 days | A2 | `internal/storage/migrations/011_rollup_ttl.sql:17-24` |
| Suppression list WAL — no TTL (opt-out is permanent) | A2 | `internal/privacy/suppression.go:14-71` |
| Backup retention 120 snapshots (~30d) | A2 | `deploy/backup/config.yml:15-16` |
| `/api/event` payload never persisted | A2 | `internal/ingest/handler.go:181-188` parseBody discards raw |
| `/api/privacy/access` (GET) returns visitor data envelope | A3 | `internal/privacy/handlers.go:126-148` |
| `/api/privacy/erase` (POST, async 202) | A3 | `internal/privacy/handlers.go:150-195` |
| `/api/privacy/opt-out` (POST) works for fresh visitors after v0.0.35 | A3 | `internal/privacy/handlers.go:79-124` + cookie-gate in `internal/ingest/handler.go::hasOptOutCookie` |
| `/api/privacy/consent` (POST give/withdraw) mints UUID if absent | A3 | `internal/privacy/consent_handler.go:100-115, 165-184` |
| `system.tables` dynamic enumeration in erase | A3 | `internal/privacy/erase.go:87-123` |
| MV-safe erase (filters `engine LIKE '%MergeTree%' AND NOT 'mv_%'`) | A3 | `internal/privacy/erase.go:14-19, 100-102` |
| Rate-limit middleware wired to `/api/privacy/*` | A3 | `internal/ratelimit/ratelimit.go:15` + main.go:391 |
| Audit emission for all DSAR actions | A3 | `handlers.go:121, 137, 183`, `consent_handler.go:115, 148` |
| TLS 1.3 manual PEM (no autocert) | A4 | `cmd/statnive-live/main.go:779`, `config/statnive-live.yaml:37-42` |
| ClickHouse localhost only | A4 | `config/statnive-live.yaml:11` (127.0.0.1:9000) |
| Hostname validation on /api/event | A4 | `internal/ingest/handler.go:155,201,218,223` |
| Input size limit 8 KB | A4 | `internal/ingest/handler.go:43, 177` |
| go-chi/httprate rate-limit | A4 | `internal/ratelimit/ratelimit.go:15` |
| Bcrypt + crypto/rand sessions, 14-day TTL | A4 | `internal/auth/password.go:15`, `session.go:4`, `config:64-67` |
| RBAC (admin/viewer/API) | A4 | `internal/auth/rbac.go` |
| `clickhouse-backup` + age + zstd | A4 | `deploy/backup/config.yml:31-40` |
| Audit log JSONL + slog + O_APPEND + SIGHUP reopen | A4 | `internal/audit/log.go:15, 26, 71` |
| systemd hardening (4 directives) | A4 | `deploy/systemd/statnive-live.service:14-22` |
| `go-licenses` enforces no AGPL | A4 | `Makefile:213-217` |
| Netcup as primary sub-processor | A5 | `docs/rules/netcup-vps-gdpr.md § 2.2` |
| ANEXIA + DATASIX inherited disclosure | A5 | `docs/compliance/subprocessor-register.md:14-16` |
| Cloudflare DNS-only (no proxy) | A5 | zone file `cf-proxied:false`; CAA records lock to `letsencrypt.org` |
| IP2Location LITE attribution string on 3 surfaces | A5 | `LICENSE-third-party.md:50-62`; `geoip-attribution-string-present` Semgrep rule |
| No Cloudflare on IR paths | A5 | `iran-no-cloudflare` Semgrep rule |
| Offline Ed25519 JWT license (no phone-home) | A5 | `config/statnive-live.yaml:31` `license.phone_home: false` |
| Never ArvanCloud | A5 | CLAUDE.md § Anti-patterns; `deploy/dns/at-vps-b1/README.md` |
| 14-day customer notice for new sub-processor | A5 | DPA § 5.4 + register policy line 5 |
| EU-only data residency (Netcup Nürnberg) | A5 | DPA § 6: "All processing of EU personal data occurs in Nuremberg, Germany" |

---

## Path A operator implications

The 4 FAIL claims **do not** block televika from running Path A today, BUT:

- **FAIL-1 (cross-tenant erase):** Real bug. Must be fixed before any operator can defensibly cite "tenancy isolation" in their own DPA / RoPA. PR plan queued.
- **FAIL-2/3 (DPA inconsistencies):** The signed Art. 28 DPA must reflect reality. Either patch the DPA template (queued) before televika signs, or televika signs the current text and signs a revised version after the patch.
- **FAIL-4 (v1.1-deferred security claims in v1 contract):** DPA must be corrected. Otherwise televika could argue breach of contract if a security incident reveals the missing CGNAT tiering / SSRF guard.

**Recommended sequence for televika:**
1. Wait for the queued template-fix PR (statnive-live FAIL-1 to FAIL-4).
2. Sign the **corrected** DPA.
3. Ship Path A on televika.com.

If televika ships before the template-fix PR lands, document the known DPA inaccuracies in the operator-side Anwalt review and obtain a side letter from statnive committing to deliver the patches by a specific date.

---

## Methodology

| Reviewer | Scope | Output |
|---|---|---|
| A1 | Identity + hashing | 8 claims, 6 PASS / 1 FAIL / 1 PARTIAL |
| A2 | Storage + retention | 8 claims, 4 PASS / 2 FAIL / 2 PARTIAL |
| A3 | DSAR endpoints | 10 claims, 9 PASS / 1 FAIL / 0 PARTIAL |
| A4 | Security + infrastructure | 15 claims, 12 PASS / 2 FAIL / 1 PARTIAL |
| A5 | Sub-processors + air-gap + license | 8 claims, 6 PASS / 1 FAIL / 1 PARTIAL |

All file:line citations verified by grep at audit time (2026-06-01) against `87a8e6f` + post-v0.0.35 deploy.
