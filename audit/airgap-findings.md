# Phase 7c — Isolation, Supply-Chain & Air-Gap Findings

Generated 2026-04-19. Skills run: custom `air-gap-validator`,
`trailofbits/supply-chain-risk-auditor`, custom
`tenancy-choke-point-enforcer` (re-run as gate),
`agentskillexchange/knip-unused-code-dependency-finder` (knip-style
audit; we don't ship a JS/TS frontend yet so the literal tool was
adapted to a Go-mod walk).

## Findings

### 1. Direct dependencies — every one in active use

13 direct deps in `go.mod`. Each verified by greping for an import
across non-test, non-vendor code:

| Dep | Purpose | Used in | License | Verdict |
|---|---|---|---|---|
| `github.com/ClickHouse/clickhouse-go/v2` | CH driver | `internal/storage/*` | Apache-2.0 | ✅ |
| `github.com/bits-and-blooms/bloom/v3` | bloom filter | `internal/enrich/newvisitor.go` | BSD-2-Clause | ✅ |
| `github.com/go-chi/chi/v5` | router | `cmd/statnive-live/main.go` + `internal/dashboard/router.go` | MIT | ✅ |
| `github.com/go-chi/httprate` | per-IP rate limiting | `internal/ratelimit/ratelimit.go` | MIT | ✅ |
| `github.com/google/uuid` | cookie ID gen | `internal/ingest/handler.go` | BSD-3-Clause | ✅ |
| `github.com/hashicorp/golang-lru/v2` | dashboard cache | `internal/cache/lru.go` | **MPL-2.0** | ⚠️ see note |
| `github.com/ip2location/ip2location-go/v9` | GeoIP | `internal/enrich/geoip.go` | MIT | ✅ |
| `github.com/medama-io/go-useragent` | UA parser | `internal/enrich/ua.go` | MIT | ✅ |
| `github.com/spf13/viper` | config loader | `cmd/statnive-live/main.go` | MIT | ✅ |
| `github.com/tidwall/wal` | WAL persistence | `internal/ingest/wal.go` | MIT | ✅ |
| `golang.org/x/sync` | `errgroup` for shutdown coordination | `cmd/statnive-live/main.go` | BSD-3-Clause | ✅ |
| `gopkg.in/yaml.v3` | sources.yaml parsing | `internal/enrich/channel.go` | MIT + Apache-2.0 | ✅ |
| `lukechampine.com/blake3` | BLAKE3-128 visitor hash | `internal/identity/hash.go` | MIT | ✅ |

**MPL-2.0 note:** `hashicorp/golang-lru` is weak copyleft. MPL-2.0
requires source-disclosure only for *modified* MPL files; we use the
library unmodified, so the obligation does not extend to our binary.
This was ratified in Phase 0 (cache slice) and reaffirmed here.

`make licenses` would catch any drift; the target is wired but
`go-licenses` itself isn't installed in this environment. Phase 7d
follow-up: `go install github.com/google/go-licenses@latest` and pipe
output to `audit/licenses.json` baseline.

### 2. No outbound calls in non-test code — clean

`grep` over `internal/**` + `cmd/**` for `http.Get` / `http.Post` /
`net.Dial` / `http.DefaultClient` / `https://`-as-target returns:

- Zero outbound calls in non-test, non-vendor source.
- Vendored matches: `ip2location-go/v9/ip2locationwebservice.go` (the
  webservice path — we never invoke it; we only use the local `.BIN`
  file lookup) and `clickhouse-go/v2/conn.go` (loopback CH only).
- Test matches: literal URL strings in `internal/enrich/channel_test.go`
  + `internal/storage/filter_test.go` etc. — used as referrer fixtures,
  not actually fetched.

The single `http.DefaultClient` reference is in
`test/dashboard_http_test.go` — test-only, talks to an httptest.Server
on loopback.

### 3. iptables OUTPUT DROP runbook step — added

`docs/runbook.md` § "Air-Gap Verification" now documents the manual
gate: build the binary, drop all outbound traffic except loopback
(via `iptables -A OUTPUT -j DROP -m owner ... ! -d 127.0.0.1/8`),
boot, hit `/healthz` + `/api/event`, expect success.

`make airgap-test` is the help-text scaffold (prints the steps); the
gate is not automated because iptables manipulation needs root and CI
containers don't grant it.

### 4. Vendor tree integrity — clean

`make vendor-check` runs `go mod verify` + `go mod vendor` + `git diff
--exit-code vendor/ go.mod go.sum`. Output: `all modules verified`,
no diff. The two CRLF warnings are git informational on the vendored
CHANGELOG/CONTRIBUTING text files — not actual diffs.

### 5. Tenancy choke-point — clean (re-verified)

`make tenancy-grep` passes:
- 0 dashboard queries against `events_raw` (Architecture Rule 1).
- 6 of 7 dashboard `SELECT`s route through `whereTimeAndTenant`
  (Architecture Rule 8); the 7th is `Realtime`, which is a documented
  exception (different time-bound semantics, inline `WHERE site_id = ?
  AND hour >= ?`, also reviewed in Pass C).

### 6. Supply-chain risk — informational

Auditing `go.sum` for known-vulnerable releases requires `govulncheck`
(deferred per `audit/sec-findings.md` § Deferred). The high-risk
substitution to watch: any new CVE in `clickhouse-go/v2` (largest
attack surface — TCP protocol parser) or `viper` (touches the file
system at boot). Both are well-maintained projects with regular
releases.

## Files changed in this pass

- `Makefile` — added `make audit` (one-shot 7c gate) + `make
  airgap-test` (manual procedure scaffold).
- `docs/runbook.md` — added § Phase 7c, § Bench baseline, § Air-Gap
  Verification, § Dependency licenses.

## Deferred

- Install + run `go-licenses` → `audit/licenses.json` baseline.
  Phase 7d.
- Install + run `govulncheck` → `audit/govulncheck.json` baseline.
  Phase 7d.
- Author project-tuned Semgrep rule pack
  (`.semgrep/forbid-events-raw-from-dashboard.yaml`,
  `.semgrep/forbid-nullable-columns.yaml`,
  `.semgrep/forbid-cdn-imports.yaml`, etc.) and run via
  `static-analysis` skill bundle. Phase 7d.
