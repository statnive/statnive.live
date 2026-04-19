# Phase 7c — Security Findings

Generated 2026-04-19. Scope: every Go file under `internal/` + `cmd/`.

Skills run: `golang-security`, `agamm/owasp-security`, `BehiSecc/vibesec`,
`trailofbits/constant-time-analysis`, `trailofbits/insecure-defaults`,
custom `blake3-hmac-identity-review`. SAST (`static-analysis` =
CodeQL+Semgrep): not run — both tools require a separate install pass
(repo-local Semgrep config + CodeQL CLI). Tracked as Phase 7d follow-up.

## Findings

### 1. Dead client-timestamp drift check (FIXED)

**Where:** `internal/ingest/handler.go` (pre-7c lines 96–104).

**Issue:** The handler unconditionally overwrote `raw.TSUTC = now`
*before* invoking `validTimestamp(now, raw.TSUTC)`. Because `RawEvent.TSUTC`
is `json:"-"` (server-authoritative — clients cannot set it) the drift
check was dead code: the comparison was always `validTimestamp(now, now)`
which trivially returns true.

**Fix:** Removed `validTimestamp` + `maxClockDrift` constant + the dead
branch. Added a comment in the handler explaining the server-authoritative
contract: clients cannot supply `TSUTC` / `UserAgent` / `IP` / `CookieID`
because the request itself is the trusted source for each.

**Why this is a security improvement:** The previous code documented a
±1 h drift policy that never executed. Removing it makes the actual
contract (server timestamps every event) auditable. A future PR that
*does* want to honor client TS now has to rebuild the path explicitly
rather than thinking it's already there.

### 2. `truncate(s, max int)` shadowed Go 1.21 builtin (FIXED)

**Where:** `internal/ingest/handler.go:310`.

**Fix:** Renamed parameter to `n`. govet doesn't flag this but it is a
quiet trap for future refactors that try to call `max(a, b)` inside the
function body.

### 3. Audit log PII — clean

`internal/audit/log.go` writes JSONL via stdlib `slog`. Reviewed every
caller of `audit.Logger.Event`:

- `internal/cert/loader.go` — emits cert subject CN + serial + expiry.
  No PII.
- `internal/ingest/fastreject.go` — emits reason + UA truncated to 120 B.
  UA is not PII per CLAUDE.md Privacy Rules; the truncation prevents
  log-bloat abuse.
- `internal/ingest/handler.go` — emits hostname only (already validated
  against the sites registry).
- `internal/ratelimit/ratelimit.go` — emits IP + path + method.
  *Acceptable*: this is a security log, not the analytics events_raw
  table. Privacy Rule 1 ("raw IP never persisted") is scoped to the
  pipeline → ClickHouse path, asserted by the `geoip.go` integration
  test. Audit-log retention of denied-request IPs is standard ops
  practice for forensics.
- `internal/enrich/pipeline.go` — emits first 8 hex chars of visitor_hash
  (32 bits of prefix; not enough to re-identify, enough to deduplicate
  burst events in the log).
- `internal/dashboard/auth.go` + `errors.go` — emits path + endpoint
  string. No PII.

No master_secret / raw user_id / raw cookie value appears in any audit
emission. `master_secret` is loaded by `config.LoadMasterSecret`, copied
defensively into `SaltManager` (`internal/identity/salt.go:78`), and
never exits the package.

### 4. Constant-time crypto — clean

- `internal/dashboard/auth.go:36` — uses `subtle.ConstantTimeCompare`
  for bearer-token comparison. `crypto/subtle` import verified.
- `internal/identity/salt.go` — HMAC-SHA256 derivation; salt is hex-encoded
  output, no comparison branch in the hot path (the cache lookup is on
  the `(siteID, date)` *key*, not on the salt value).
- `internal/identity/hash.go` — BLAKE3-128 keyed mode for VisitorHash;
  SHA-256 for UserIDHash. No equality compare on hashes anywhere in the
  binary.
- No `bytes.Equal` / `==` against any hash value across `internal/`.

### 5. BLAKE3 / HMAC identity invariants — clean

Custom `blake3-hmac-identity-review` skill checklist (encoded in
`.claude/skills/blake3-hmac-identity-review/`):

- ✅ BLAKE3 keyed with per-day salt (`identity.VisitorHash`).
- ✅ Salt = HMAC-SHA256(master_secret, site_id || YYYY-MM-DD IRST).
- ✅ Master secret never logged, never written to events_raw, never
  echoed to the wire.
- ✅ Per-tenant cryptographic separation via `site_id` in HMAC input.
- ✅ Salt cache keyed by (siteID, date); cardinality bounded at
  ~2K entries even at 1K SaaS tenants.

### 6. Insecure defaults sweep — clean

- ✅ TLS 1.3 floor: `cmd/statnive-live/main.go:238`
  (`MinVersion: tls.VersionTLS13`).
- ✅ File modes:
  - `internal/audit/log.go:119` — `0o640` (service-readable, group-readable, world-blind).
  - `internal/enrich/newvisitor.go:89` — `0o640`.
  - `internal/ingest/wal.go:48` — `0o750` (directory, drop world).
  - All test fixtures `0o600`.
- ✅ All app-side `http.Client{...}` construction sets `Timeout`
  (verified `test/perf/perf.go`, `test/perf/disk_full_test.go`,
  `test/integration_test.go`, `test/enrichment_e2e_test.go`,
  `test/multitenant_isolation_test.go`, `test/security_test.go`).
  Vendored `clickhouse-go` does not — but the ClickHouse driver
  has its own connection deadlines + dial timeout, and we always
  use the native protocol on `127.0.0.1`, never HTTP.
- ✅ MaxBytesReader 8 KB on the ingest handler
  (`internal/ingest/handler.go:67`).

### 7. OWASP Top-10 quick pass

| Risk | Status | Evidence |
|---|---|---|
| A01 Broken Access Control | n/a (no auth surface yet) | Phase 2b backlog |
| A02 Cryptographic Failures | clean | findings 4, 5 |
| A03 Injection (SQL) | clean | every CH query uses parameterized binds via `whereTimeAndTenant` |
| A04 Insecure Design | clean (server-authoritative TS) | finding 1 fix |
| A05 Security Misconfiguration | clean | finding 6 |
| A06 Vulnerable Components | govulncheck not installed | Phase 7d task |
| A07 Identification + AuthN | n/a (no auth yet) | Phase 2b backlog |
| A08 Software + Data Integrity | clean | vendored deps + `make vendor-check` |
| A09 Logging + Monitoring | clean | `audit.Logger` JSONL append-only, SIGHUP reopen |
| A10 SSRF | clean | binary makes zero outbound HTTP calls in non-test code |

## Deferred to Phase 7d

- Install + run `govulncheck` (gopls MCP) over the vendored tree; commit
  baseline `audit/govulncheck.json`. Requires `go install
  golang.org/x/vuln/cmd/govulncheck@latest` first.
- Run CodeQL + Semgrep (`static-analysis` skill bundle) over `internal/**`
  with project-tuned rule pack; commit `audit/sast.sarif` baseline.
  Requires authoring `.semgrep/forbid-events-raw-from-dashboard.yaml`,
  `.semgrep/forbid-nullable-columns.yaml`, etc. (~4 rules from custom
  skills).
- STRIDE threat model (`izar/ctm` + `izar/4qpytm` skills). Bigger scope;
  needs Phase 4 (tracker → public API surface) + Phase 2b (auth) before
  the threat surface is stable.
