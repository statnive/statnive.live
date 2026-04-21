---
name: blake3-hmac-identity-review
description: MUST USE when editing `internal/identity/**`, `internal/auth/**`, or any crypto / hashing / HMAC / bcrypt path. Validates BLAKE3-128 (not 256) truncation; exact `HMAC(master_secret, site_id || YYYY-MM-DD IRST)`; IRST (UTC+3:30, no DST) rotation boundary; `hmac.Equal` constant-time compare (never `==`); master secret env/file only (never logged).
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 1
  research: "jaan-to/docs/research/25-ai-claude-skills-SamplePlatform-grade-analytics-platform.md §gap-analysis #6; CLAUDE.md §Privacy + §Identity"
---

# blake3-hmac-identity-review

> **Activation gate (Phase 1).** This skill's Semgrep rule bodies and CI wiring are scheduled for Phase 1 (`internal/identity/` first ships). Until the corresponding `.github/workflows/identity-gate.yml` is green on main, treat this skill as **advisory-only** — surface the checklist to the reviewer, do not block merges, and flag any mismatch as `activation-pending` rather than auto-fixing.

Encodes **CLAUDE.md Privacy Rules 2, 3, 4** (lines 47-49) and the **Identity** block (line 20). Pair with the community-installed `trailofbits/skills/constant-time-analysis` skill for compiler-induced timing side-channels — this skill covers the usage correctness that timing analysis cannot detect.

## When this skill fires

- Any file under `internal/identity/`, `internal/auth/`, `internal/crypto/`.
- Any import of `crypto/hmac`, `crypto/subtle`, `lukechampine.com/blake3`, `crypto/sha256`, `golang.org/x/crypto/bcrypt`.
- Any string literal or comment mentioning `master_secret`, `MasterSecret`, `master.key`, `salt`, `blake3`, `hmac`.
- Any change to the YAML config schema keys in the `security:` or `identity:` block.
- Any log line near identity code (`slog.Info`, `slog.Debug`, audit-log emit).

## Enforced invariants

### Hashing

1. **BLAKE3-128 (16 bytes), not BLAKE3-256.** Correct form: `h := blake3.New(16, saltKey); h.Write(input); out := h.Sum(nil)`. Wrong: `blake3.Sum256(input)`.
2. **Identity hash column type is `FixedString(16)`** (cross-checked by `clickhouse-rollup-correctness`).
3. No MD5, no SHA-1 anywhere. Reject on import of `crypto/md5`, `crypto/sha1`, `hash/crc32`.
4. SHA-256 is allowed for:
   - `user_id` pre-hash before CH write (`SHA-256(master_secret || site_id || user_id)`), exactly this form.
   - `go-licenses` integrity, bundle SHA256SUMS (operational, not identity).
5. bcrypt is allowed only via `golang.org/x/crypto/bcrypt` with cost ≥ 12 (CLAUDE.md §Security #6).

### HMAC salt derivation

1. **Exactly** `HMAC-SHA256(master_secret, site_id || YYYY-MM-DD IRST)`. No variant. `site_id` is serialized as fixed-width 4-byte big-endian (matching the ClickHouse `UInt32`).
2. Date string is IRST (`Asia/Tehran`) — UTC+3:30, no DST since Sept 2022. Use `time.LoadLocation("Asia/Tehran")`. Reject bare `time.Now().UTC().Format("2006-01-02")` in identity code.
3. Salt is **derived, never stored**. No disk or CH row persists the raw salt bytes.
4. Salt rotates at IRST midnight — the grace window for cross-day fingerprint lookup (PR #2, `internal/enrich/newvisitor.go`) is **exactly one day back**, never two.
5. Master secret is a file `config/master.key` (chmod 0600) or env `STATNIVE_MASTER_SECRET`. Never a string literal, never committed.

### Constant-time comparison

1. Every hash / HMAC comparison uses `hmac.Equal` or `subtle.ConstantTimeCompare`. Reject `==` or `bytes.Equal` on HMAC outputs / session tokens.
2. Session token lookup walks the store, then constant-time-compares on match — never leaks-on-miss timing.

### Logging and leakage

1. Master secret never appears in logs (`slog` structured or `fmt.Printf`), panics, audit-log JSONL, health-check output, or error strings.
2. Raw `user_id` is never logged (CLAUDE.md Privacy Rule 4). Only the post-hash value.
3. Raw IP never persisted (handled by `internal/enrich/geoip.go` contract, but referenced here so identity code cannot accidentally write it to ClickHouse).

## Should trigger (reject)

```go
// BAD — BLAKE3-256 and ==
sum := blake3.Sum256(append([]byte(siteID), visitor...))
if bytes.Equal(sum[:16], stored) { ... }
```

```go
// BAD — logs master secret, uses UTC not IRST
slog.Info("rotating salt", "secret", cfg.MasterSecret, "date", time.Now().UTC().Format("2006-01-02"))
```

## Should NOT trigger (allow)

```go
loc, _ := time.LoadLocation("Asia/Tehran")
dateIRST := time.Now().In(loc).Format("2006-01-02")

mac := hmac.New(sha256.New, []byte(cfg.MasterSecret))
binary.Write(mac, binary.BigEndian, siteID)
mac.Write([]byte(dateIRST))
saltKey := mac.Sum(nil)

h := blake3.New(16, saltKey)
h.Write(visitorInput)
hash := h.Sum(nil)  // 16 bytes

// compare constant-time
if hmac.Equal(hash, expected) { ... }
```

## Implementation (TODO — Phase 1)

- `semgrep/rule.yml` — TODO: flag `==` / `bytes.Equal` on HMAC output types; flag `blake3.Sum256` in identity files; flag `time.Now().UTC()` in identity files (should be `time.Now().In(loc)`).
- `semgrep/logging.yml` — TODO: flag any log attribute or format string using `master_secret`, `MasterSecret`, `Salt`, `raw_user_id`.
- `test/fixtures/` — TODO: should-trigger / should-not-trigger cases, including the existing Phase 1 identity_test.go as a regression baseline.

Full spec: [README.md](README.md).