---
name: gdpr-code-review
description: MUST USE when editing `internal/identity/**`, `internal/audit/**`, `/api/privacy/*` handlers, files importing `blake3`/`hmac`, or `tracker/**`. Validates Sec-GPC + consent-decline short-circuit BEFORE hash (not after); salt file deleted (not overwritten); no canvas/WebGL/font-enum/device-memory fingerprinting; resolution bucketed; no raw IP / raw user_id / master_secret in slog; no MD5/SHA-1 anywhere. Full 12-item body.
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 11
  research: "jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md §Gap 3"
---

# gdpr-code-review

> **Activation gate (Phase 11 public signup — HARD GATE).** This skill's Semgrep rule bodies and CI wiring are scheduled for Phase 11 (first SaaS public signup). Until the corresponding `.github/workflows/gdpr-gate.yml` is green on main, treat this skill as **advisory-only** — surface the checklist to the reviewer, do not block merges, and flag any mismatch as `activation-pending` rather than auto-fixing.

Encodes **CLAUDE.md Privacy Rules 1–7** + Project Goal 1 (security first). Where `blake3-hmac-identity-review` validates the crypto *mechanics*, this skill validates the **privacy-by-design** consumption pattern — that the hash is never computed when the user has opted out, that no fingerprinting vectors creep in, that no PII leaks through slog / error strings / audit logs. Phase 11 (SaaS) is the hard gate.

## When this skill fires

- Any file under `internal/identity/**`, `internal/audit/**`, `internal/privacy/**`.
- Any handler matching `/api/privacy/*` route pattern.
- Any import of `lukechampine.com/blake3`, `crypto/hmac`, `crypto/sha256`, `crypto/md5` (import deny), `crypto/sha1` (import deny).
- Any `slog.*` call or audit-log emit near identity code.
- Any change to `tracker/src/**` or the JS tracker's `sendBeacon` path.
- Any `EnrichedEvent` struct field addition.

## Enforced invariants — the 12-item checklist (doc 27 §Skills §gdpr-code-review)

1. **Raw `r.RemoteAddr` never appears in slog fields.** The slog rule in `internal/audit/log.go` must strip / hash IP before emit.
2. **`user_id` is hashed before any persist or log.** Raw user_id never reaches disk or any audit sink.
3. **Daily salt file has mode 0600 AND is deleted (not overwritten) at rotation.** Overwriting leaves recoverable on-disk remnants — violates Recital 26 anonymity argument for HLL rollups.
4. **`Sec-GPC: 1` check precedes any hash computation on SaaS paths.** Computing-then-discarding is a processing event under GDPR Article 4(2) and is not legally equivalent to never computing.
5. **Consent-decline short-circuits before hash, not after.** In the first 10 lines of the ingest handler — before any `blake3.Sum(...)` call.
6. **Fingerprinting APIs (canvas, WebGL, font enum, `navigator.deviceMemory`, `hardwareConcurrency`, `navigator.plugins`, `AudioContext`) are not exercised client-side.** EFF Panopticlick: UA + screen + plugins = 18.1 bits of entropy (2010); PETS 2016 Laperdrix et al.: 89.4% of browsers uniquely identifiable. The 34-field `EnrichedEvent` is a k-anonymity risk if any of these slip in.
7. **Screen resolution is bucketed.** `1920×1080 → "desktop-fhd"`. Never pixel-precise.
8. **City is population-gated (> 100 K).** Standard k-anon heuristic — cities smaller than 100 K combined with browser+OS uniquely identify.
9. **Audit log fields match the DPA field list** — fail on drift. The list lives in `docs/dpa-draft.md`; the skill greps against it.
10. **No MD5 / SHA-1 anywhere.** Enforced by import deny on `crypto/md5`, `crypto/sha1`.
11. **No PII in error messages returned to clients.** Error strings are generic (`"invalid payload"`), never echoing submitted values.
12. **All 34 EnrichedEvent fields documented in `FIELDS.md`** with a justification column (what purpose, what retention, what legal basis under Art. 6).
13. **Static `slog` / audit-log PII gate (F3 — PLAN.md Phase 7d, complements Phase 7e Vector.dev live wire-scan).** Semgrep rule `slog-no-raw-pii` — block any `slog.*` or `logger.*` call where a keyed arg name (first string in a key/value pair) matches `/(?i)^(user_?id|ip|remote_?addr|email|site_?id|master_?secret|raw_.*)$/` unless the paired value is wrapped in `identity.HexUserIDHash(...)`, `identity.HashIP(...)`, `redact.*(...)`, or a compile-time constant. Closes the merge-time gap that the Phase 7e live PII wire-scan (doc 29 §3.4 / §6.3) only catches post-deploy. Pairs with Privacy Rule #4 (enforcement surface).

## Legal framework (document, don't assume)

- **WP29 Opinion 05/2014** — hashing alone, even with rotating salt, is pseudonymisation, not anonymisation. Three anonymity tests: no singling-out, no linkability, no inference.
- **CJEU C-582/14 *Breyer*** — dynamic IPs are personal data when the operator has legal means to link.
- **CJEU C-413/23 *EDPS v SRB* (4 Sept 2025)** — "relative concept" of personal data: pseudonymous data can be anonymous *from the recipient's perspective* when the recipient lacks re-identification means.
- **GDPR Recital 26** — "reasonably likely" standard.
- **GDPR Article 4(2)** — "processing" includes computing/holding briefly, even if discarded. This is why consent-decline must short-circuit BEFORE hash.
- **GDPR Article 21** — GPC is a valid objection signal (Berlin Regional Court 2023 DNT ruling is closest precedent).

## Should trigger (reject)

```go
// BAD — IP logged raw
slog.Info("request", "ip", r.RemoteAddr)

// BAD — user_id logged pre-hash
slog.Info("event received", "user_id", uid)

// BAD — consent check after hash
hash := computeVisitorHash(cfg, event)
if consent.Declined(r) { return }   // already processed under Art. 4(2)

// BAD — canvas fingerprint
canvas := event.CanvasFingerprint   // EnrichedEvent should not carry this
```

```js
// BAD — in tracker/
const entropy = [
  canvas.toDataURL(),
  webgl.getParameter(webgl.VENDOR),
  Array.from(document.fonts).map(f => f.family),
  navigator.deviceMemory,
  navigator.hardwareConcurrency,
].join('|');
```

## Should NOT trigger (allow)

```go
// OK — consent check FIRST
if consent.Declined(r) || r.Header.Get("Sec-GPC") == "1" {
    w.WriteHeader(204)
    return
}
// only now: hash
hash := computeVisitorHash(cfg, event)

// OK — IP hashed for rate-limit key only; slog gets the hash
ipHash := hashIP(r.RemoteAddr, cfg.MasterSecret)
slog.Info("ratelimit", "ip_hash", ipHash)  // never raw

// OK — bucketed screen / population-gated city
event.ScreenBucket = bucketScreen(event.Screen)  // "desktop-fhd"
event.City = gateCityByPopulation(event.City, 100_000)
```

## Implementation (TODO — Phase 11 hard gate)

- `semgrep/pii-rules.yml` — TODO:
  1. flag `slog.String("ip", r.RemoteAddr)` or any slog call with raw IP.
  2. flag `slog.String("user_id", uid)` without `hash(uid)` wrapper.
  3. flag `http.SetCookie` without a preceding consent check.
  4. flag `md5.` / `sha1.` imports.
- `semgrep/fingerprint-rules.yml` — TODO:
  1. flag any call to `context.Canvas`, `navigator.getBattery`, `screen.availWidth` in tracker JS.
  2. flag `navigator.deviceMemory`, `navigator.hardwareConcurrency`, `navigator.plugins`, `AudioContext`, `document.fonts`.
- `test/fixtures/` — should-trigger / should-not-trigger Go + JS cases.
- `FIELDS.md` — TODO (Phase 11 content): 34 EnrichedEvent fields with justification columns.

Full spec + DPA language template: [README.md](README.md). Paired skill: [`dsar-completeness-checker`](../dsar-completeness-checker/README.md) — same Phase 11 gate, different surface (sink matrix).