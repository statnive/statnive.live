# `_statnive` Cookie Posture (Milestone 1 Bug #19 / LEARN.md Lesson 17)

> **Status: Option C SHIPPED (2026-04-26).** Three independently-toggleable server-side flags now gate the cookie + visitor-identity hash:
>
> - `consent.required` (default `true`, env `STATNIVE_CONSENT_REQUIRED`)
> - `consent.respect_gpc` (default `true`, env `STATNIVE_CONSENT_RESPECT_GPC`)
> - `consent.respect_dnt` (default `true`, env `STATNIVE_CONSENT_RESPECT_DNT`)
>
> Default posture = SaaS-safe (all three on). Self-hosted Iran tier flips `consent.required=false` to restore pre-Option-C behavior. Operators in jurisdictions where GPC/DNT have no legal weight may flip respect flags off — but doing so regresses the privacy posture and should be paired with a clear in-product disclosure.
>
> The decision context below stays archived for counsel review pre-Phase-11a (first paying SaaS customer). Counsel review is also needed for: privacy-page wording, customer DPA (`docs/dpa-draft.md`), and the SaaS sub-processor register (`docs/compliance/subprocessor-register.md`).

## What the cookie does

The binary's `/api/event` ingest handler sets the following header on the response when no incoming `Cookie:` header carries it:

```
Set-Cookie: _statnive=<UUIDv4>; Path=/; Max-Age=31536000; HttpOnly; SameSite=Lax
```

Verified live during the Milestone 1 cutover (2026-04-25). Source: [`internal/ingest/handler.go:readOrSetCookieID()`](../../internal/ingest/handler.go).

| Property | Value |
|---|---|
| Name | `_statnive` |
| Domain | first-party (the `app.statnive.live` apex; not cross-site) |
| Value | random UUIDv4 |
| Lifetime | 1 year (`Max-Age=31536000`) |
| Path | `/` |
| HttpOnly | yes |
| SameSite | Lax |
| Secure | not set in code; relies on the deployment serving over HTTPS only |
| Cross-site | no — first-party only |
| PII | none — pure-random UUID, no user info |
| Server-side use | dedup / session-stitching keyed on the UUID; cleared via the daily-rotating salt path before any persisted hash |

## Why it exists

- Closes the visitor-identity-fuzz that the daily-rotating-salt scheme alone can't fully eliminate. Two distinct visits in the same calendar day with the same hashed identity would otherwise be merged; the cookie ID lets the binary keep them separate.
- Cheap server-side enforcement of "is this the same browser session" without fingerprinting (CLAUDE.md Privacy Rule 7) and without storing personal data (Privacy Rule 1, 4).

## Posture options

### Option A — keep the cookie (proposal)

**Legal basis (verbatim citations for counsel review):**

- **ePrivacy Directive 2002/58/EC Art. 5(3)** as amended by **Directive 2009/136/EC**: storage on, or access to information already stored in, a user's terminal equipment requires consent — *except* where it is "strictly necessary in order for the provider of an information society service explicitly requested by the subscriber or user to provide the service."
- **EDPB Guidelines 2/2023 on Article 5(3) of the ePrivacy Directive** (the current authoritative interpretation post-Planet49) — narrows "strictly necessary" to functions the user has *explicitly requested*, not the operator's analytics or product-improvement goals. Analytics cookies typically fall *outside* this exemption.
- **CJEU Case C-673/17 (Planet49)**, judgment 2019-10-01: pre-ticked consent boxes are not valid consent under GDPR; analytics cookies require active opt-in unless they qualify under Art. 5(3)'s strict-necessity carve-out.

**Argument for the strict-necessity carve-out applying here:** Statnive's analytics service requires session continuity for accurate visitor dedup; the UUID is the minimum identifier for that function (no PII, no cross-site reach, no fingerprinting, no third-party share). The user "explicitly requested" the analytics service by using a site whose owner published Statnive analytics in their privacy policy.

**Litigation risk (counsel must weigh):** CNIL (France) and DPC (Ireland) have historically read Art. 5(3) more strictly than the EDPB Guidelines suggest, often treating any analytics cookie as consent-required regardless of first-party scope. Option A is a defensible-but-contested posture, not settled law.

| Surface | Action |
|---|---|
| `/privacy` page on statnive-website (+ `.de` / `.fr` mirrors) | Add a row to the cookie table: name, lifetime, purpose, "no consent required (analytics-essential per Art. 5(3))" with the legal-basis cite |
| Customer DPA template ([docs/dpa-draft.md](../dpa-draft.md)) | Annex A — Categories of Data: list the `_statnive` cookie + its analytics-essential purpose |
| Code | No change — current behavior |

**Counsel sign-off:** _____ / _____ (date)

### Option B — drop the cookie

Code change: remove the `Set-Cookie` line from `readOrSetCookieID()` and route all dedup through the daily-rotating-salt-keyed BLAKE3 hash exclusively. Accept the visitor-identity fuzz this introduces (two same-day visits with cleared cookies merge into one identity if the IP + UA + identity-hash inputs collide).

**Legal posture:** removes the Art. 5(3) question entirely — no terminal-equipment storage, no consent question, no contested interpretation. Strongest defensible posture for the EU SaaS tier; matches how Plausible/Fathom/Simple-Analytics frame their no-cookie pitch.

| Surface | Action |
|---|---|
| `internal/ingest/handler.go:readOrSetCookieID()` | Remove the Set-Cookie path + the matching read; remove the cookie name constant |
| Integration test | Update the assertion that the response carries the cookie header — replace with assertion that NO cookie is set on the response |
| `/privacy` page | Drop the cookie row entirely |
| Customer DPA | Drop the cookie reference |

Risk: ~5–10% identity-resolution accuracy regression (rough estimate; would need a measurement run on the live VPS). Bot detection still works (UA + IP + ASN heuristics). Multi-visit-per-day analytics still works in aggregate but per-visitor session counts get fuzzier.

**Counsel sign-off:** _____ / _____ (date)

### Option C — gate the cookie behind explicit operator consent (`STATNIVE_CONSENT_REQUIRED=1`)

Default `false` for self-hosted Iran (Privacy Rule 5: Iran allows cookies + user_id), default `true` for the SaaS binary. When `true`, the `Set-Cookie` is gated on a consent decision (request header / cookie banner integration); when `false`, current behavior.

| Surface | Action |
|---|---|
| `cmd/statnive-live/main.go` config | Add `STATNIVE_CONSENT_REQUIRED` env var + viper key |
| `internal/ingest/handler.go` | Branch on the consent flag |
| Integration test | Add coverage for both branches |

Risk: doubles the test matrix + adds a config knob that needs operator documentation; rejects "cheap server-side dedup" as a default for SaaS. The complexity buys flexibility but doesn't eliminate the underlying decision — operators still have to choose at deploy time.

**Counsel sign-off:** _____ / _____ (date)

## Recommendation

Go with **Option A** (keep the cookie) for Phase 9 / Milestone 1 dogfood, with the privacy-page + DPA edits landing in Phase 11a (first public SaaS signup). Option C is the right shape for Phase 11b once we have multi-tenant customer-facing SaaS, but adding it now would gold-plate the v1 binary.

The cutover postmortem catalogues this as Bug #19; LEARN.md Lesson 17 preserves the underlying reasoning. Either way, the decision should be documented + counsel-reviewed before any non-dogfood EU traffic reaches the binary.

## Decision log

| Date | Decision | Decided by | Rationale |
|---|---|---|---|
| 2026-04-25 | Defer (cookie ships as-is on dogfood tier) | Cutover operator | Milestone 1 functional gate; not a release blocker |
| _TBD_ | _Option A / B / C_ | _user + counsel_ | _record decision here before Phase 11a_ |

## References

- [LEARN.md § F](../../LEARN.md) — Lesson 17 (cookie GDPR review)
- [PLAN.md § Milestone 1 cutover postmortem](../../PLAN.md#milestone-1-cutover-postmortem-2026-04-25) — Bug #19
- [docs/rules/privacy-detail.md](../rules/privacy-detail.md) — extended GDPR Art. 5(3) / Recital-26 / C-413/23 chain
- [docs/dpa-draft.md](../dpa-draft.md) — customer DPA template (Annex A receives the cookie row when Option A is chosen)
- [docs/rules/netcup-vps-gdpr.md](../rules/netcup-vps-gdpr.md) — SaaS-tier ops contract
- [internal/ingest/handler.go](../../internal/ingest/handler.go) — `readOrSetCookieID()` is where Option B's deletion would land
