# gdpr-code-review — full spec

## Architecture rule

Encodes **CLAUDE.md Privacy Rules 1–7** and **Project Goal 1**. The **hard gate** on Phase 11 (SaaS — first public signup). Paired with [`dsar-completeness-checker`](../dsar-completeness-checker/README.md) which handles the erasure sink matrix.

## Research anchors

- [jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md](../../../../jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md) §Gap 3 (first half) — primary source.
- [WP29 Opinion 05/2014 on anonymisation](https://ec.europa.eu/justice/article-29/documentation/opinion-recommendation/files/2014/wp216_en.pdf).
- CJEU *Breyer* C-582/14, *EDPS v SRB* C-413/23 (Sept 2025).
- [EFF Panopticlick](https://panopticlick.eff.org/), Laperdrix et al. PETS 2016.

## Implementation phase

**Phase 11 — International SaaS self-serve.** Hard gate: no public signup until this skill's 12-item checklist is green on every ingest + privacy-API code path.

## Companion: draft DPA §X.Y (doc 27 §line 77-79)

This skill enforces *code-level* preconditions for the following DPA paragraph (to be copied verbatim into `docs/dpa-draft.md` in Phase 11):

> *statnive-live processes visitor data through a two-tier architecture. The events_raw table stores pseudonymous records keyed by a BLAKE3-128 hash of (visitor_attributes, daily_salt), where the daily salt is rotated at 00:00 Iran Standard Time and the previous salt is cryptographically erased within 60 seconds of rotation. Upon a verified Article 17 erasure request, statnive-live deletes all rows from events_raw matching the requester's visitor_hash within 30 days (typically within 7 days, scheduled in the weekly deletion batch). The AggregatingMergeTree rollup tables store only HyperLogLog sketches (uniqCombined64 state) of daily, weekly, and monthly aggregates; these sketches are mathematically irreversible and, following the midnight salt deletion, cannot be linked to any individual data subject by any means reasonably likely to be used, in the sense of GDPR Recital 26. statnive-live therefore classifies rollup tables as anonymous aggregate statistics under WP29 Opinion 05/2014 and CJEU C-413/23 EDPS v SRB (4 September 2025), and these tables are not within the scope of individual Article 17 requests. Rollups are fully rebuilt from events_raw on a rolling weekly schedule, providing a bounded-time remediation path if required by supervisory authority guidance.*

The skill's role: verify every code invariant the paragraph *claims* is actually enforced. If code drifts from the DPA, this skill fails the build.

## Consent / GPC code path (mandatory short-circuit)

Per doc 27 §line 83: **GPC is legally binding in California (CCPA, Sephora $1.2 M 2022, Tractor Supply $1.35 M Oct 2025, AB 566 mandating browser-level GPC by Jan 2027), Colorado, Connecticut, New Jersey as of April 2026**. statnive-live SaaS must honor GPC by default — no additional banner interaction required.

End-to-end test the skill requires:
```bash
curl -H 'Sec-GPC: 1' -X POST http://localhost:8080/api/event -d '{...}'
# MUST: 204 No Content, no Set-Cookie, zero rows in events_raw
```

## Files

- `semgrep/pii-rules.yml` — TODO: slog PII, r.RemoteAddr, SetCookie-before-consent, md5/sha1 deny.
- `semgrep/fingerprint-rules.yml` — TODO: canvas/WebGL/font-enum/deviceMemory/hardwareConcurrency/AudioContext.
- `test/fixtures/` — TODO: should-trigger / should-not-trigger Go + JS cases.
- `FIELDS.md` — Phase 11 deliverable; 34 EnrichedEvent fields × {purpose, retention, Article-6 basis}.

## Pairs with

- [`blake3-hmac-identity-review`](../blake3-hmac-identity-review/README.md) — validates crypto mechanics; this skill validates consumption pattern.
- [`dsar-completeness-checker`](../dsar-completeness-checker/README.md) — validates erasure sink matrix; this skill validates up-stream privacy.
- `sanitize` (BehiSecc, community, installed by this PR) — last-mile grep for PII in output files.
- `grc` (Sushegaad, community, installed by this PR) — outer GDPR checklist mapping findings to Articles.
- `legal-compliance-check` (anthropics, community, installed by this PR) — Article 28 DPA template.

## CI integration (TODO)

```makefile
gdpr-semgrep:
    semgrep --config=.claude/skills/gdpr-code-review/semgrep internal/ tracker/

gpc-integration-test:
    go test ./test/integration -run TestSaaS_GPCShortCircuit
    go test ./test/integration -run TestSaaS_ConsentDeclineShortCircuit

release-gate-phase-11: lint test test-integration gdpr-semgrep gpc-integration-test dsar-test
```

## Fingerprinting denylist (JS tracker)

The skill rejects any reference to these APIs in `tracker/src/**`:

- `canvas.toDataURL`, `canvas.getContext('2d')` for fingerprinting (allowed for feature detection only if no pixel read).
- `WebGLRenderingContext.getParameter(VENDOR)` / `UNMASKED_VENDOR_WEBGL` / `UNMASKED_RENDERER_WEBGL`.
- `document.fonts.values()`, `document.fonts.check()`.
- `navigator.deviceMemory`, `navigator.hardwareConcurrency`.
- `navigator.plugins.*`, `navigator.mimeTypes.*`.
- `new AudioContext()`, `OfflineAudioContext`.
- `navigator.getBattery()`.
- `window.performance.memory`.
- `screen.availWidth` / `availHeight` at pixel precision (bucket allowed).

## Scope

- `internal/identity/**`, `internal/audit/**`, `internal/privacy/**`, `internal/enrich/**` (EnrichedEvent additions).
- `tracker/src/**`.
- `/api/event`, `/api/privacy/*` handlers.
- Does **not** apply to operator CLI (`statnive-live doctor`, `stats overview` subcommands) or Phase 1 internal debugging paths — those are operator-only surfaces.