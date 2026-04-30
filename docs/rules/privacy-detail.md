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

## Rule 6 — DNT + GPC respected by default on SaaS

SaaS tier: `Sec-GPC: 1` OR `DNT: 1` OR `Sec-GPC: 1` → short-circuit before hash computation (see Rule 9). Self-hosted: operator decides via `privacy.respect_dnt_gpc` config key; default `true` for safety.

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

## Cross-references

- [`CLAUDE.md § Privacy Rules`](../../CLAUDE.md#privacy-rules-non-negotiable) — rule statements
- [`.claude/skills/blake3-hmac-identity-review`](../../.claude/skills/blake3-hmac-identity-review/README.md)
- [`.claude/skills/gdpr-code-review`](../../.claude/skills/gdpr-code-review/README.md)
- [`.claude/skills/dsar-completeness-checker`](../../.claude/skills/dsar-completeness-checker/README.md)
- `docs/dpa-draft.md` — Phase 11
