# Privacy rule detail (reference)

> Extended rationale for [CLAUDE.md § Privacy Rules](../../CLAUDE.md#privacy-rules-non-negotiable). The rule statements themselves stay inline in CLAUDE.md; this file holds the legal-chain reasoning that agents need only when drafting DPA language, answering a compliance review, or interpreting an ambiguous edge case.

## Rule 1 — Raw IP never persisted

The raw IP enters `internal/enrich/geoip.go`, is used for the country/city/region lookup, and is **discarded in the same function call** — it never enters the `EnrichedEvent` struct and is never visible to `internal/storage/clickhouse.go` or to any audit sink. Asserted by `test/integration/pii_leak_test.go` which greps the ClickHouse row contents and the JSONL audit log for IP-shaped substrings after a seeded 1 K-event ingest.

## Rule 2 — Daily rotating salts

Salt formula: `HMAC-SHA256(master_secret, site_id || YYYY-MM-DD IRST)`. Deterministically derivable — never stored on disk. Salt cache in-memory only, 5-minute overlap window at IRST midnight so in-flight events at the boundary don't silently mis-hash.

## Rule 3 — SHA-256+ and BLAKE3 only

Enforced by the Semgrep rule `no-md5-no-sha1` (future) and the `blake3-hmac-identity-review` skill's checklist. MD5 and SHA-1 are absolute bans in any identity, privacy, or auth path. This includes test fixtures — a test that uses MD5 signals a pattern future developers will copy.

## Rule 4 — User ID hashed before ClickHouse write

Formula: `SHA-256(master_secret || site_id || user_id)`. Raw `user_id` is never logged via `slog`, never written to a file, never shipped to a remote audit sink. The audit log records only the hashed form with an explicit `user_id_hash` field label (not `user_id`).

## Rule 5 — Iran vs SaaS tier legal posture

Iran: cookies + `user_id` pass-through permitted; no GDPR.

SaaS (hosted outside Iran, serving EU visitors): GDPR applies. Customer DPA required (Phase 11 deliverable at `docs/dpa-draft.md`); consent banner required; Art. 15 access and Art. 17 erasure endpoints required; Art. 30 records-of-processing required.

Both code paths ship in the same binary. The distinction is a runtime config flag (`tenant.saas_mode: true|false`) that toggles the consent gate and the DSAR endpoints.

## Rule 6 — DNT + GPC default off — operator opt-in per deployment

**Posture (April 2026 update).** The previous default-on (`consent.respect_gpc: true`, `consent.respect_dnt: true`) shipped paired with a tracker.js client-side short-circuit on `navigator.doNotTrack === '1'` / `navigator.globalPrivacyControl === true`. Production diagnosis on `wp-slimstat.com` (88% under-count vs WP Analytics over 3 days) showed the client-side check was silently dropping 70-85% of legitimate Brave / Firefox-strict / iOS Safari / Chrome-with-extension visitors *before* the POST to `/api/event` ever fired. Operators saw their dashboards flatlined for the very visitor segments most committed to privacy.

**New posture.** The tracker no longer consults `navigator.doNotTrack` or `navigator.globalPrivacyControl`. The browser still attaches `DNT: 1` / `Sec-GPC: 1` request headers automatically; the binary honors them only when the operator has flipped `consent.respect_gpc` / `consent.respect_dnt` to true in their YAML config. Defaults are now **false** — every visit is counted.

**EU compliance.** Operators with EU visitors **must** flip both flags to `true` to remain GDPR-compliant. The default flip is an operator-policy decision; the legal liability sits with the site operator, not statnive-live. SaaS DPA template (`docs/dpa-draft.md`) calls this out explicitly.

**Iran self-hosted.** Zero GDPR exposure (data stays on customer's box, no EU data-subject in scope per Privacy Rule 5). Defaults stay false.

**v1.1 follow-up.** Per-site server toggles (`statnive.sites.respect_gpc UInt8 DEFAULT 0` + admin UI) replace the global flags so multi-tenant operators can serve EU + non-EU customers from the same binary without re-editing config. Tracked as deferred PR D2.

**The Privacy Rule 9 guarantee still holds.** When the operator has opted in and the visitor's browser sends `Sec-GPC: 1` / `DNT: 1`, the consent-decline short-circuit runs **before** hash computation (`internal/ingest/handler.go:147`). Identity (cookie + `user_id_hash`) is suppressed; the event still ingests anonymously so the operator can count the visit.

## Rule 7 — First-party tracker via `go:embed`

Tracker JS is compiled into the Go binary. Served from the analytics host at `/statnive.js`. Absolute bans:
- No external CDN reference in the JS source or the injection snippet.
- No `navigator.plugins` or `navigator.mimeTypes` enumeration.
- No canvas, WebGL, or font-availability probing.
- No audio-context fingerprinting.

Enforced by `preact-signals-bundle-budget` + `gdpr-code-review` skills.

## Rule 8 — Salt rotation DELETES the previous salt file

**Why delete, not overwrite.** An overwrite leaves recoverable on-disk remnants — filesystem journal entries on ext4, deleted-but-unreferenced inode data until the block is reused, and (on copy-on-write filesystems like btrfs/zfs) shadow copies that survive until the next scrub. For the Recital 26 anonymity argument to hold, the daily salts that generated today's hashes must be provably unrecoverable after rotation. Atomic rename + unlink (via `os.Remove(path)`) is load-bearing.

Enforced by `blake3-hmac-identity-review` (checks the rotation path for `os.Remove`) and by `gdpr-code-review` (checks that the rotation interval matches the IRST midnight boundary, not UTC).

## Rule 9 — Consent / GPC short-circuit BEFORE hash computation

**GDPR Art. 4(2)** defines "processing" broadly — computing-then-discarding a hash is a processing event. Short-circuiting *after* the hash has been computed is non-compliant even if the hash is never written to storage.

**GDPR Recital 26** establishes that anonymous data falls outside GDPR scope. The test is whether re-identification is "reasonably likely, taking into account all the means reasonably likely to be used" (not merely theoretical).

**CJEU Case C-413/23 (Commissioner for Data Protection v. EDPS, 2025)** confirms the Recital 26 test applies to HLL-style aggregates — re-identification must be *reasonably* achievable for the data to fall back inside GDPR scope.

**The HLL-anonymous argument.** HyperLogLog is probabilistic-lossy by construction; the state does not retain the pre-image. Combined with daily-salt rotation (where the salt is deleted after rotation per Rule 8), re-identification requires both HLL pre-image inversion (computationally infeasible) AND knowing the deleted salt (unrecoverable). Under C-413/23, this is outside GDPR scope.

**Weekly rollup rebuild safety net.** Even if the anonymity argument is challenged in a future ruling, the rollup tables are rebuilt from raw events weekly. The window of affected data is bounded to ≤ 7 days. This is a defense-in-depth measure documented in the DPA draft.

**Where to find the DPA draft.** `docs/dpa-draft.md` — Phase 11 deliverable. Includes Recital 26 citation, C-413/23 citation, weekly-rebuild commitment, and Art. 28 processor language.

## Salt rotation path sketch

```go
// internal/identity/salt.go (canonical)
func (r *Rotator) rotate(now time.Time) error {
    newSalt := deriveSalt(r.masterSecret, r.siteID, now) // HMAC-SHA256
    tmp := r.path + ".tmp"
    if err := os.WriteFile(tmp, newSalt, 0600); err != nil { return err }
    if err := os.Rename(tmp, r.path); err != nil { return err } // atomic
    if r.previousPath != "" {
        _ = os.Remove(r.previousPath) // DELETE, not overwrite — Rule 8
    }
    r.previousPath = r.path
    return nil
}
```

Unit test: `TestRotate_DeletesPreviousFile` asserts the previous file path does not exist after rotation.

## Referrer column — host-only at write time

`internal/enrich/pipeline.go` writes `events_raw.referrer` as the lowercase host of the inbound `Referer:` header — query strings, paths, fragments, userinfo, and port are stripped via the same `extractHostLower` helper that the channel mapper has used since v0.0.1 (`internal/enrich/channel.go:484`). Query strings can carry session tokens (`?session=…`), search terms, or reset-password tokens; the host alone is what a privacy-first analytics product has a legal-basis claim to.

**Mixed-format compatibility window.** Rows written before the cutover (≤ 2026-05-12) may still contain full URLs in `events_raw.referrer`. The natural 180-day TTL clears them by 2026-11-12. Any query surfacing the raw column (debug exports, custom SQL) must tolerate both shapes until then; the dashboard surface is unaffected because channel attribution already lazy-extracts the host on read.

**Backfill — none.** Existing rows are not rewritten. An `ALTER TABLE … UPDATE` to strip historical query strings would be a multi-hour mutation against a 180-day-deep table; the TTL gives the same outcome at zero risk.

## Cross-references

- [`CLAUDE.md § Privacy Rules`](../../CLAUDE.md#privacy-rules-non-negotiable) — rule statements
- [`.claude/skills/blake3-hmac-identity-review`](../../.claude/skills/blake3-hmac-identity-review/README.md)
- [`.claude/skills/gdpr-code-review`](../../.claude/skills/gdpr-code-review/README.md)
- [`.claude/skills/dsar-completeness-checker`](../../.claude/skills/dsar-completeness-checker/README.md)
- `docs/dpa-draft.md` — Phase 11
