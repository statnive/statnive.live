# Audit — statnive-live vs research 53 (consent-free analytics under GDPR + ePrivacy)
Generated: 2026-05-11  ·  Reviewers: A1–A8  ·  Aggregator: A9
Plan: ~/.claude/plans/now-based-on-this-glistening-clarke.md
Research: jaan-to/docs/research/53-consent-free.md

## Executive Summary

`statnive-live` is structurally close to the strictest viable consent-free architecture described in `53-consent-free.md`: site-scoped daily-rotating BLAKE3-keyed identity, no raw IP / no raw UA at rest, no client-side storage in the tracker, EU/DPF-only sub-processor register, zero telemetry exfiltration, and a clean multitenant choke point. The architecture is one engineering sprint and one publication sprint away from defensibly claiming the CNIL audience-measurement exemption in France, Spain, Italy, and the Netherlands — and from defensibly operating in Germany under Section 25 TDDDG.

It is **not there today**. The audit surfaces **four P0 findings** that block any "consent-free" marketing claim in any binding EU regime, and **fourteen P1 findings** that block the claim for at least one of CNIL / AEPD / Garante / AP / TDDDG / ICO. Two of the P0 findings are launch blockers for a public GDPR Article 13/14 surface (no `/privacy` route; no jurisdiction-aware defaults). The remaining two are mismatches between the per-site policy defaults and the binding rule in Germany (GPC/DNT default-off; persistent server-side cookie set without consent under default config). All four are addressable without rewriting the data pipeline.

### Verdict per dimension

| Dimension | Reviewer | Verdict | Headline |
|-----------|----------|---------|----------|
| Identity / hash / salt | A1 | conditional | site_id encoding mismatch (decimal-string vs binary.BigEndian) |
| IP / UA / Referrer pipeline | A2 | conditional | full referrer URL persisted in `events_raw` for 180 days |
| Tracker / device storage | A3 | conditional | `_statnive` cookie jurisdiction-conditional (P0 in DE under default) |
| Event schema vs CNIL 3-cap | A4 | conditional | free-form `event_name` pass-through; cap unenforced |
| Retention / TTL / aggregation | A5 | conditional | rollups have no TTL; no aggregation-to-nearest-10 helper |
| Tenant isolation | A6 | pass | clean; zero findings |
| Opt-out / DSAR / GPC-DNT | A7 | fail | GPC/DNT default-off; no `/api/privacy/*` |
| Jurisdiction / residency / disclosure | A8 | fail | no `/privacy` route; no jurisdiction-aware defaults |

### Top P0 findings (one line each)
- **F-P0-1** — No public `/privacy` route. Launch blocker for GDPR Art. 13/14 and CNIL Sheet 16 opt-out anchor (A5, A7, A8).
- **F-P0-2** — No jurisdiction-aware per-site policy defaults; Germany sites default to `ConsentRequired=false` → Section 25 TDDDG hard-rule violation by default (A3, A7, A8).
- **F-P0-3** — `RespectGPC` / `RespectDNT` default-off across all jurisdictions; EU-default violates §06 do-list and is silently non-compliant in Germany (A3, A7, A8).
- **F-P0-4** — `_statnive` UUIDv4 cookie set on `ConsentRequired=false` sites with no jurisdiction guard — unconditional storage on terminal equipment in Germany under default config (A3).

### Top P1 findings (one line each)
- **F-P1-1** — `events_raw.referrer` persists full URL 180 days; CNIL self-assessment requires host-only (A2).
- **F-P1-2** — Rollups (`hourly_visitors`, `daily_pages`, `daily_sources`) have no TTL — indefinite retention of HLL state vs CNIL 25-month ceiling (A5).
- **F-P1-3** — No aggregation-to-nearest-10 in any visitor-count API response; CNIL self-assessment recommends or requires an anonymisation Opinion analysis (A5).
- **F-P1-4** — `event_name` is free-form ≤128 bytes; CNIL §02 three-event cap is semantic and the server cannot enforce it today (A4).
- **F-P1-5** — Salt derivation uses `fmt.Fprintf(mac, "%d||%s", siteID, date)` decimal-string encoding instead of `binary.BigEndian` (A1).
- **F-P1-6** — Endianness inconsistency between `UserIDHash` (LittleEndian) and salt derivation (string) — regression hazard (A1).
- **F-P1-7** — No `/api/privacy/{opt-out,access,erase}` endpoints; Article 21 GDPR right not exercisable through the product (A3, A5, A7).
- **F-P1-8** — Public LIA template not published (EDPB Guidelines 1/2024 requires documented three-step assessment) (A7, A8).
- **F-P1-9** — No `/dpa` download route; `docs/dpa-draft.md` exists but unmounted (A8).
- **F-P1-10** — Privacy-policy templates from research §08 (EN + DE) not served publicly (A7).
- **F-P1-11** — No `audit.EventOptOut*` / `EventDSARErase*` constants in `internal/audit/events.go` — accountability gap once /api/privacy/* lands (A7).
- **F-P1-12** — Marketing language must downgrade from "qualifies under CNIL exemption" to "configurable to qualify" until cap is enforceable (A4).
- **F-P1-13** — No operator-visible event-name audit endpoint; cannot demonstrate compliance with cap (A4).
- **F-P1-14** — Tracker JS does not introspect `navigator.globalPrivacyControl` defensively; GPC honoured server-side only and only when policy opts in (A3).

`53-consent-free.md` is the single source of truth for all severity reconciliation below.

## Findings

| ID | Severity | Title | file:line | Maps-to research § | Reviewer(s) | Suggested remediation |
|----|----------|-------|-----------|--------------------|-------------|----------------------|
| F-P0-1 | P0 | No public `/privacy` route mounted | `cmd/statnive-live/main.go:370-510` (route table; zero `/privacy` matches) | §08 privacy-policy template; §02 CNIL Sheet 16 opt-out anchor | A5, A7, A8 | Mount `/privacy` (EN + DE) using §08 templates; serve from `internal/landing/**` with locale negotiation; include persistent Article 21 opt-out link. |
| F-P0-2 | P0 | No jurisdiction-aware per-site policy defaults | `internal/sites/sites.go:62-69`; migration `006:21-22`; `cmd/statnive-live/main.go:311-319` bootstrap | §03 Germany TDDDG; §04 country-by-country | A7, A8 | Add `SitePolicy.Jurisdiction` enum (`DE`/`FR`/`IT`/`ES`/`NL`/`BE`/`IE`/`UK`/`OTHER`); auto-set `ConsentRequired=true`, `RespectGPC=true`, `RespectDNT=true` for `DE`; surface in admin site-creation UI. |
| F-P0-3 | P0 | `RespectGPC` / `RespectDNT` default-off across all jurisdictions | `internal/sites/sites.go:62-69`; `internal/ingest/handler.go:379, 383`; migration `006:21-22` | §06 DO list (DNT + GPC enabled by default); §10 Omnibus Art. 88b machine-readable signal | A3 (P1), A5 (P2), A7 (P0), A8 (P0) | Flip defaults to `true` for EU jurisdictions; force-on for `DE`; expose admin toggle but log audit event when an operator disables them. Jurisdiction note: **P0 in DE / EU-default; P1 elsewhere with mitigation**. |
| F-P0-4 | P0 | `_statnive` UUIDv4 cookie set unconditionally on `ConsentRequired=false` sites | `internal/ingest/handler.go:402-421` (cookie write); `handler.go:173-178` (consent gate) | §03 Germany Section 25 TDDDG; §05 terminal-equipment storage | A3 | Hash cookieID with daily salt server-side before any ClickHouse write (close the open question in §Open Questions); refuse to set the cookie when `Jurisdiction=DE`; for non-DE EU jurisdictions document the CNIL-style first-party analytics cookie justification + add periodic 13-month rotation. Jurisdiction note: **P0 in DE; P1 in FR/ES/IT/NL with documented mitigation**. |
| F-P1-1 | P1 | `events_raw.referrer` persists full URL 180 days | `clickhouse/migrations/001_initial.sql:50`; `internal/ingest/pipeline.go:169` | §02 CNIL self-assessment ("Le référent … se limite au domaine (« host »)"); §06 risk table | A2 | Call `extractHostLower()` from `internal/enrich/channel.go:484-509` before assignment in `pipeline.go:169`; add Semgrep rule `referrer-host-only`; one-shot UPDATE to backfill the existing 180-day window or rely on natural TTL. |
| F-P1-2 | P1 | Rollups have no TTL — indefinite HLL-state retention | `internal/storage/migrations/002_rollups.sql:10-79` (`hourly_visitors`, `daily_pages`, `daily_sources`) | §02 CNIL 25-month ceiling; §05 anonymisation analysis | A5 | Add `TTL day + INTERVAL 750 DAY DELETE` (≈25 months) on all three rollups, OR publish an EDPB anonymisation Opinion analysis justifying indefinite retention of `AggregateFunction(uniqCombined64, FixedString(16))` state. |
| F-P1-3 | P1 | No aggregation-to-nearest-10 on visitor counts | `internal/storage/queries.go:130, 162, 209` (exact `uniqCombined64Merge`) | §02 CNIL "Agrégation et la présentation à la dizaine la plus proche" | A5 | Add `roundToNearest10(n int64) int64` helper at the API response layer for visitor cardinality fields (not for pageviews, not for revenue); document in the privacy policy template. |
| F-P1-4 | P1 | Free-form `event_name` pass-through; CNIL 3-event cap unenforceable | `tracker/src/tracker.js:103` (`.track(name, props, value)`); `internal/ingest/event.go` (no enum) | §02 CNIL "trois type d'évènements" verbatim | A4 | Either (a) constrain `event_name` to an enum mapped to the three CNIL categories (page presence / feature interaction / timing), OR (b) downgrade the marketing claim to "configurable to qualify" and require a per-site event-taxonomy attestation. |
| F-P1-5 | P1 | Salt derivation uses decimal-string `siteID` encoding | `internal/identity/salt.go:142-147` (`fmt.Fprintf(mac, "%d||%s", siteID, date)`) | §05 site-scoped salt | A1 | Replace with `binary.Write(mac, binary.BigEndian, siteID); mac.Write([]byte(date))` per `blake3-hmac-identity-review` skill. Anchor with a comment; add a regression test pinning the byte layout. |
| F-P1-6 | P1 | Endianness inconsistency between `UserIDHash` and salt derivation | `internal/identity/hash.go:69-71` (`binary.LittleEndian.AppendUint32`) vs `salt.go:142` (string) | §05 site-scoped salt | A1 | Pick one endianness (recommend BigEndian network order across both paths); anchor with a comment in both files; add a cross-file regression test. |
| F-P1-7 | P1 | No `/api/privacy/{opt-out,access,erase}` endpoints | route inspection `cmd/statnive-live/main.go:377-510` (zero matches) | §06 DO list; §07 LIA template; §08 right-to-object | A3, A5, A7 | Add three routes: opt-out (writes suppression flag), access (DSAR JSON export), erase (calls `internal/privacy/erase.go` enumerating `system.tables` per `dsar-completeness-checker`). Bound by IP rate-limit; CSRF-protected. |
| F-P1-8 | P1 | Public LIA template not published | `internal/landing/**` (no LIA route) | §07 EDPB Guidelines 1/2024 documented three-step assessment | A7, A8 | Publish the §07 LIA template at `/legal/lia` (EN + DE); cite EDPB Guidelines 1/2024 of 8 Oct 2024 verbatim; review annually or on material change. |
| F-P1-9 | P1 | No `/dpa` download route | `docs/dpa-draft.md` exists; no route mount | §08 DPA implications table; GDPR Article 28 | A8 | Mount `/legal/dpa` serving the existing draft as PDF + Markdown; gate by a one-click acceptance form; emit `audit.EventDPADownloaded`. |
| F-P1-10 | P1 | Privacy-policy templates from research §08 not served | no `/docs/privacy-policy-template-*.md` route | §08 EN + DE clauses | A7 | Publish both templates as static pages under `/legal/privacy-policy/{en,de}.md`; cross-link from the marketing site. |
| F-P1-11 | P1 | No audit constants for opt-out / DSAR-erase / DPA-download | `internal/audit/events.go` lacks `EventOptOut*` / `EventDSARErase*` / `EventDPADownloaded` | §07 accountability under Article 28(3)(h); §08 right-to-object | A7 | Add the constants now; wire them into the F-P1-7 / F-P1-9 endpoints when those land. |
| F-P1-12 | P1 | Marketing language must downgrade until 3-cap enforceable | `statnive-website/` claim surfaces (this audit is read-only; flagged for follow-up) | §02 CNIL "Most large audience measurement offerings do not fall within the scope of the exemption" verbatim | A4 | Replace "qualifies under CNIL exemption" with "configurable to qualify under the CNIL exemption when deployed per the LIA + self-assessment"; cite CNIL Sheet 16 verbatim. |
| F-P1-13 | P1 | No operator-visible event-name audit endpoint | no `/api/admin/event-audit` route | §02 operator must demonstrate compliance with cap | A4 | Add a read-only admin endpoint returning unique `event_name` cardinality per `site_id` over the rollup window; surface in the admin UI as "Event taxonomy". |
| F-P1-14 | P1 | Tracker JS does not introspect `navigator.globalPrivacyControl` defensively | `tracker/src/tracker.js:1-50` | §06 DO list "DNT + GPC enabled by default"; §10 Omnibus Art. 88b | A3 | Add a 5-line probe in the tracker that short-circuits `sendBeacon` if `navigator.globalPrivacyControl === true` AND server policy honours GPC; document in tracker README. |
| F-P2-1 | P2 | GeoIP uses `db.Get_all` instead of `Get_city` | `internal/enrich/geoip.go:127` | §05 hot-path minimisation | A2 | Swap to `Get_city`; the `geoip-pipeline-review` skill bans `Get_all` on the hot path; functionally safe but allocates extra strings. Phase 8 deferred. |
| F-P2-2 | P2 | Omnibus Article 88a(4) readiness | (not yet implemented) | §10 single-click refusal + 6-month no-re-prompt | A7 | Pre-wire the single-click refusal UX + 6-month suppression TTL behind a feature flag; ship when Omnibus enters force (2027-2028). |
| F-P2-3 | P2 | Cloudflare DPF certification verification | `docs/compliance/subprocessor-register.md` | §05 Schrems II EU-only; §08 sub-processor disclosure | A8 | Add DPF certification date, expiry, and supplementary-measures column to the sub-processor register; refresh annually. |
| F-P2-4 | P2 | `go.opentelemetry.io/otel v1.41.0 // indirect` transitive import | `go.mod` | §05 Schrems II EU-only; §08 sub-processor disclosure | A8 | Add a Semgrep guard banning explicit `otel.Tracer(...)` / exporter instantiation so a future contributor cannot wire an exporter without review. |

## Confirmed Strengths

- **S1 — Identity & salt.** Site-scoped daily-rotating BLAKE3-keyed identity. IRST (UTC+3:30, no DST) rotation boundary with `time.FixedZone` fallback at `internal/identity/salt.go:24-36`. Cache-only destructive rotation at `salt.go:56-68` and `salt.go:72-83`. `PreviousSalt()` at `salt.go:100-102` enables midnight grace. BLAKE3-128 keyed mode at `internal/identity/hash.go:29` (`blake3.New(16, key[:])`). Cross-tenant isolation pinned by `test/multitenant_isolation_test.go:31-141`. Constant-time compares in `internal/auth/store.go:458` (`constantTimeEq`) and `internal/auth/middleware.go:132`. Zero MD5/SHA-1. **Consent gate fires BEFORE hash** at `internal/ingest/handler.go:173-195` — Privacy Rule 9 satisfied.
- **S2 — `/metrics` is PII-clean.** Counters only; no IPs, no salts, no visitor hashes. Bearer-token gated (`internal/metrics/metrics.go:170-176`); empty token → 404 (`metrics.go:464`).
- **S3 — No telemetry sub-processors.** `go.mod` has zero Sentry / Datadog / Bugsnag / OTLP-exporter direct imports (otel is `indirect` only). Sub-processor register (`docs/compliance/subprocessor-register.md`) is EU/DPF-only: Netcup (DE) + ANEXIA (DE/AT) + DATASIX (DE) + ANX (AT); Let's Encrypt (US-DPF, public CA only); Cloudflare DNS (US-DPF, grey-cloud, no proxy); MailerLite (IE/LT).
- **S4 — Tracker zero device storage.** `tracker/src/tracker.js:1-50` declares zero cookies/localStorage/sessionStorage/IndexedDB; grep confirms. Transport via `sendBeacon` + `keepalive` `fetch`. No fingerprinting probes — only `navigator.webdriver`, `_phantom`, `callPhantom`, `navigator.sendBeacon` for automation/transport gates. Bundle 1,478 bytes min / 747 bytes gz, IIFE, `go:embed` first-party delivery, no CDN, no SRI dependency.
- **S5 — IP / UA pipeline minimisation.** Raw IP never persisted — `EnrichedEvent` has no IP field; `internal/ingest/pipeline.go:114` discards after GeoIP. UA reduced to parsed fields only (`internal/enrich/ua.go:28-56`). XFF parsed right-to-left in `ClientIP()` (`handler.go:350-370`). IP2Location CC-BY-SA attribution verbatim at `about/handler.go:58-79` and on three surfaces (LICENSE-third-party + `/api/about` + dashboard footer). Recent migration `009_rollup_simple_aggregate.sql` (2026-05-11) fixed an `AggregatingMergeTree` cardinality bug.
- **S6 — Multitenant choke point.** Every dashboard SELECT routes through `whereTimeAndTenant()`. `WHERE site_id = ?` is first predicate. Rollup `ORDER BY` leads with `site_id` (`002_rollups.sql:20, 47, 79`). Site-scoped HMAC mixes `siteID` (`salt.go:142-144`). Multitenant isolation test wired into CI via `.github/workflows/ci.yml:199` → `make test-integration`. Hostname canonicalisation (`NormalizeHostname()` at `internal/sites/sites.go:172-200`) lowercases at the end, strips port/path/scheme/userinfo.
- **S7 — Retention floor.** `events_raw` TTL `time + INTERVAL 180 DAY DELETE` at `internal/storage/migrations/001_initial.sql:86` — well within CNIL 25-month ceiling. Auth sessions TTL `expires_at + INTERVAL 7 DAY DELETE` (`004_auth_schema.sql:50`).
- **S8 — PII-leak regression test.** `test/pii_leak_test.go:169, 199` fires a TEST-NET-3 probe (`203.0.113.42`) and asserts WAL + audit + ClickHouse all clean.
- **S9 — Audit sink hygiene.** Append-only JSONL with SIGHUP reopen — sound for accountability under GDPR Article 28(3)(h).
- **S10 — Goal taxonomy doesn't expand event surface.** Goals match on `event_name` not `event_type` (`internal/goals/types.go` — `MatchTypeEventNameEquals`).

## Open Questions

1. **(A3, raised; carried to A9.)** Is the raw `cookieID` UUIDv4 hashed before ClickHouse write, or is it persisted in `events_raw.cookie_id` as raw bytes? If the latter, the cookie becomes a persistent cross-day identifier that survives daily salt rotation, defeats S1, and pushes F-P0-4 to **definitively P0 in every CNIL-exemption jurisdiction** (FR/ES/IT/NL) — not just Germany. Resolution path: read `internal/ingest/pipeline.go` around the cookie-derivation block + the `events_raw.cookie_id` column definition (if any). **Treat F-P0-4 as P0 globally until this is confirmed.**
2. **(A4 → product / legal.)** Should the marketing claim default to "CNIL-configurable" or "CNIL-exempt"? The CNIL self-assessment requires the *operator* (Statnive's customer) to attest. Statnive can only attest that the tool is configurable to qualify. This is a positioning question, not a code question — but A4's P1 downgrade is contingent on it.
3. **(A5 → product / legal.)** Should rollup TTL be set to ≤750 days (mechanical CNIL ceiling) or should Statnive publish an anonymisation Opinion analysis arguing `AggregateFunction(uniqCombined64, FixedString(16))` is irreversible-enough to escape the 25-month rule? The former is faster; the latter is durable. Recommend the former unless legal counsel disagrees.
4. **(A7, A8 → product.)** Per-site `Jurisdiction` enum design: should it auto-detect from a customer-supplied site host TLD (`.de` → `DE`), from a billing-address inference, or be operator-declared at site creation? Auto-detect carries false-positive risk (`.com` German site); operator-declared carries forget-to-set risk. Recommend operator-declared with an admin-UI warning when the host TLD disagrees.
5. **(A8 → legal.)** Cloudflare DPF certification status verification: register lists "DPF certified" without dates. Need to confirm continuous certification + add the date column.
6. **(A3 / A4 → product.)** Should there be a "strict CNIL mode" toggle per site that locks `event_name` to the three CNIL categories, disables the cookie entirely, and forces aggregation-to-10? This would let one product serve both the strict-exemption customer and the consented-mode customer cleanly.

## Methodology

Eight parallel Explore-typed reviewers (A1–A8) each scoped to a slice of `statnive-live/` and paired with the relevant project skills, plus one aggregator (A9, this report) using `general-purpose` for Write access. All reviewers were strictly read-only — no code, schema, migration, or doc was modified. The reviewer roster is the one defined in [~/.claude/plans/now-based-on-this-glistening-clarke.md](file:///Users/parhumm/.claude/plans/now-based-on-this-glistening-clarke.md), §"Reviewer Agent Roster".

| ID | Scope | Skills invoked |
|----|-------|---------------|
| A1 | `internal/identity/**`, `internal/auth/**`, `internal/session/**` | `blake3-hmac-identity-review`, `gdpr-code-review`, `golang-security` |
| A2 | `internal/enrich/**`, `internal/ingest/event.go`, `clickhouse/migrations/001_initial.sql` | `geoip-pipeline-review`, `gdpr-code-review`, `grc-gdpr` |
| A3 | `tracker/src/**`, `tracker/test/**`, `internal/ingest/handler.go` (cookie write) | `gdpr-code-review`, `air-gap-validator`, `preact-signals-bundle-budget`, `vibesec` |
| A4 | `internal/ingest/event.go`, `internal/ingest/handler.go`, `tracker/src/tracker.js` | `legal-compliance-check`, `grc-gdpr`, `gdpr-code-review` |
| A5 | `internal/storage/migrations/**`, `clickhouse/schema.sql`, `internal/storage/queries.go` | `clickhouse-best-practices`, `clickhouse-operations-review`, `clickhouse-rollup-correctness`, `dsar-completeness-checker`, `grc-gdpr` |
| A6 | `internal/storage/queries.go`, `internal/sites/**`, `internal/identity/salt.go` | `tenancy-choke-point-enforcer`, `blake3-hmac-identity-review`, `clickhouse-best-practices` |
| A7 | `internal/admin/**`, `internal/sites/**`, `internal/audit/**`, route table | `dsar-completeness-checker`, `gdpr-code-review`, `grc-gdpr` |
| A8 | `LICENSE-third-party.md`, `docs/compliance/subprocessor-register.md`, `go.mod`, `internal/landing/**` | `legal-compliance-check`, `grc-gdpr`, `iranian-dc-deploy`, `air-gap-validator` |

Severity reconciliation followed the plan's rubric verbatim:
- **P0** — fails a hard rule in a binding regime (Germany Section 25 TDDDG unconditional cookie; raw IP persistence; static salt; missing DSAR path for an existing table; jurisdiction-blind default that violates §03). Blocks any "consent-free" marketing claim.
- **P1** — fails a CNIL exemption condition (referrer host-only; 25-month TTL; 3-event cap; aggregation-to-10) or a documented best-practice (LIA absent; GPC default-off in EU). Acceptable short-term with documented mitigation; blocks the marketing claim for that jurisdiction.
- **P2** — improvement opportunity (Omnibus Article 88b machine-readable signal support; downloadable DPA route; CC-BY-SA attribution surface refresh; `Get_all` → `Get_city`; Semgrep guards).

Cross-reviewer overlaps were merged into single rows with all reviewers cited: GPC/DNT default-off → A3+A5+A7+A8 → reconciled to **P0** with jurisdiction note; missing `/api/privacy/*` → A3+A5+A7 → reconciled to **P1**; missing `/privacy` route → A5+A7+A8 → reconciled to **P0**; missing LIA → A7+A8 → reconciled to **P1**; cookie-classification → A3 owns with explicit Germany caveat; jurisdiction-aware defaults → A7+A8 merged into a single P0 with both framings cited.

This audit produces evidence only. Remediation is a separate engineering follow-up; the §"Open Questions" items require product or legal input before remediation begins. The report references `53-consent-free.md` as the single source of truth and does not restate research conclusions verbatim except where the CNIL self-assessment language is load-bearing for a finding's severity (F-P1-1, F-P1-3, F-P1-4, F-P1-12).
