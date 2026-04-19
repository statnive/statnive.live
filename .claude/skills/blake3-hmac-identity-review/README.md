# blake3-hmac-identity-review — full spec

## Architecture rule

Encodes **CLAUDE.md Privacy Rules 1-4** (lines 46-49) and the **Identity** block (line 20):

> Three layers — user_id (site sends) → cookie → BLAKE3-128 hash; daily salt derived deterministically from a single master secret + site_id + IRST date (`HMAC(master_secret, site_id || YYYY-MM-DD IRST)`). One secret across all tenants — site_id in the HMAC input provides per-site cryptographic separation without per-site key management.

Cross-references **Security #11** (user_id pre-hashed with SHA-256) and **Privacy Rule 2** (daily rotating salts).

## Research anchors

- [jaan-to/docs/research/25-ai-claude-skills-filimo-grade-analytics-platform.md](../../../../jaan-to/docs/research/25-ai-claude-skills-filimo-grade-analytics-platform.md) §gap-analysis #6.
- [jaan-to/docs/research/22-statnive-ingestion-pipeline-10-critical-gaps-drop-in-go-code.md](../../../../jaan-to/docs/research/22-statnive-ingestion-pipeline-10-critical-gaps-drop-in-go-code.md) §GAP 6 (salt rotation) + §GAP 9 (BLAKE3-128 visitor hash).
- [jaan-to/docs/research/24-pirsch-pattern-extraction-reference-only.md](../../../../jaan-to/docs/research/24-pirsch-pattern-extraction-reference-only.md) §Sec 1.1 (cross-day fingerprint grace — exactly one day back).

## Implementation phase

**Phase 1 — Ingestion Pipeline** (shipped through PR #2). Skill is the regression guard for every change to `internal/identity/`, `internal/enrich/newvisitor.go`, and `internal/auth/`.

## Files

- `semgrep/rule.yml` — TODO: BLAKE3-256 / `==` on HMAC / `time.Now().UTC()` in identity paths.
- `semgrep/logging.yml` — TODO: secret leaks in logs.
- `test/fixtures/should-trigger/` — TODO: cases that must be rejected.
- `test/fixtures/should-not-trigger/` — TODO: the existing Phase 1 identity / salt rotation / cross-day grace code.

## Pairs with

- `trailofbits/skills/constant-time-analysis` (to be installed from doc 25) — catches compiler-induced timing side-channels that source-level review can miss.
- `trailofbits/skills/semgrep-rule-creator` (already installed) — scaffolds the Semgrep rules for this skill.
- `golang-security` (already installed) — general crypto hygiene.
- `agamm/claude-code-owasp` (to be installed) — OWASP crypto checklist.

## CI integration (TODO)

```makefile
identity-semgrep:
    semgrep --config=.claude/skills/blake3-hmac-identity-review/semgrep internal/

identity-test:
    go test ./internal/identity/... ./internal/enrich/...

identity-gate: identity-semgrep identity-test
```

## IRST specifics

IRST (`Asia/Tehran`) is UTC+3:30 with **no daylight saving time** since 2022-09-22. Iran officially abolished DST that year, so `time.LoadLocation("Asia/Tehran")` returns a fixed +03:30 offset year-round.

The salt rotation boundary is therefore exactly every 86,400 seconds from 00:00 IRST = 20:30 UTC of the previous day. The cross-day fingerprint grace window (PR #2) uses yesterday's IRST date, not yesterday's UTC date — the one-hour offset matters for visitors crossing the boundary near midnight.

## What this skill does NOT cover

- TLS / PEM validation (owned by the planned `air-gap-validator` + `golang-security`).
- bcrypt session hygiene beyond cost ≥ 12 (owned by the Phase 2b auth review).
- JWT license parsing (owned by a future `license-jwt-review` skill at v2).
- Master-secret **provenance** (read from env/file vs hardcoded) — this skill only checks leakage; the provenance gate lives in `go-licenses` + `make secret-audit`.

## Scope

- `internal/identity/**`.
- `internal/auth/**`.
- `internal/enrich/newvisitor.go` (cross-day grace lookup).
- `internal/crypto/**` (if added later).
- Any Go file importing `crypto/hmac`, `crypto/subtle`, `lukechampine.com/blake3`.

Does **not** apply to:
- `docs/**`, `tests/perf/**`, `test/integration_*.go` unless they compare HMACs.