---
name: geoip-pipeline-review
description: MUST USE when editing `internal/enrich/geoip.go`, ip2location-go usage, SIGHUP wiring in `cmd/**/main.go`, or attribution surfaces (`LICENSE-third-party.md`, `/about`, dashboard footer). Enforces `atomic.Pointer[DBHandle]` hot-swap (not `sync.RWMutex`), SIGHUP-only reload with pre-swap validation, `Get_city` only (`Get_all` banned), netip.Addr in / Record out (no IP downstream), fsnotify forbidden, CC-BY-SA attribution in all three surfaces. Full 14-item body.
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 8
  research: "jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md §Gap 1"
  policy_dependency: "CLAUDE.md § License Rules — CC-BY-SA carve-out for non-linked data files"
---

# geoip-pipeline-review

> **Activation gate (Phase 8 Weeks 19–20; blocks Phase 10 paid-DB23 cutover).** This skill's Semgrep rule bodies and CI wiring are scheduled for Phase 8 Weeks 19–20, after `iranian-dc-deploy` ships (`airgap-update-geoip.sh` dependency) and CC-BY-SA policy resolves (Week 19 Day 1 legal call). Until the corresponding `.github/workflows/geoip-gate.yml` is green on main, treat this skill as **advisory-only** — surface the checklist to the reviewer, do not block merges, and flag any mismatch as `activation-pending` rather than auto-fixing.

Encodes **CLAUDE.md § Privacy Rule 1** ("Raw IP never persisted — IP enters the pipeline only for GeoIP lookup, then is discarded before the batch writer sees the row") and the CC-BY-SA-4.0 attribution obligation for **IP2Location LITE DB23** (the only major free city-level GeoIP DB compatible with our air-gap + license posture).

## When this skill fires

- `internal/enrich/geoip.go` and `internal/enrich/geoip_test.go` — hot-swap wrapper logic.
- Any file matching `**/*ip2location*` or `**/*geoip*` — new integrations.
- `cmd/**/main.go` — SIGHUP handler wiring.
- `internal/about/**` — attribution surface.
- `internal/http/middleware/*.go` — request-level IP extraction (must go to GeoIP then drop).
- `LICENSE-third-party.md` — attribution block present.
- `.github/workflows/geoip*.yml` — CI gate wiring.

## 14-item blocking checklist (doc 28 §Gap 1)

**Hot-swap shape**
1. DB handle is `atomic.Pointer[*dbHandle]`; `sync.RWMutex` around `*ip2location.DB` is **rejected** (benchmark: ~1–3ns atomic load vs ~20–100ns RLock at 9K EPS).
2. Reload validates before swap: size ≥ 50MB floor; `OpenDB` succeeds; `8.8.8.8 → "US"` probe passes; `185.143.232.1 → "IR"` probe passes; `DatabaseVersion()` ≥ current.
3. Old handle closed after 1s grace period (goroutine `time.Sleep` then `Close`), **never** `defer`-ed on the success path. The 1s window drains any lookups in flight; the p99 budget is 500ms so 1s is safe.

**Hot-path shape**
4. `Get_city` on hot path; `Get_all` banned by Semgrep. LITE returns `"-"` for ISP/domain/mobile anyway — `Get_all` pays string-dereference cost for no benefit.
5. `Lookup` signature is `(netip.Addr) → (Record, error)`. Raw IP **never** appears in logs, errors, persisted structs, or ClickHouse schema.

**Reload trigger**
6. SIGHUP registered exactly once via `signal.Notify` in `main`; reload runs in its own goroutine to avoid blocking the signal handler.
7. **No `fsnotify` anywhere in the enrichment package.** fsnotify silently loses events on overlayfs, NFS, bind mounts, and kqueue (macOS). SIGHUP is the universal trigger.

**Resilience**
8. No-op fallback: `Lookup` returns zero `Record` + nil error when `cur == nil` (service boots without a BIN file; operator SCPs it in later). Never panics, never errors-up.

**Attribution (CC-BY-SA-4.0 obligation)**
9. `LICENSE-third-party.md` contains the exact string: *"This site or product includes IP2Location LITE data available from https://lite.ip2location.com."*
10. Dashboard footer and `/about` JSON surface the attribution. `--license` CLI flag alone does **not** satisfy CC-BY-SA §3(a)(1) — it must be user-visible.

**Privacy hardening**
11. No cache keyed by raw IP. Allowed cache key granularity: `/24` prefix or coarser. (Stops the "rebuild visitor IP from timing" attack class.)
12. `Lookup` is pure: no logs, no files, no external calls. Hot-path cleanliness.

**Supply chain**
13. `go.mod` pins `ip2location-go` to an explicit minor version (`v9.x.y`), never `latest`. Reproducible builds + CVE triage.
14. CI log-grep gate after 7K EPS k6 run: zero IPv4/IPv6 regex matches in any server log. `([0-9]{1,3}\.){3}[0-9]{1,3}` and `([0-9a-fA-F:]+:+)+[0-9a-fA-F]+` patterns.

## Semgrep rules (8)

Full bodies in [`semgrep/rules.yaml`](semgrep/rules.yaml) (verbatim from doc 28 lines 68–150):

| Rule ID | Severity | What it blocks |
|---|---|---|
| `geoip-ip-in-log` | ERROR | Raw IP in slog/zap key outside GeoIP wrapper |
| `geoip-ip-field-in-persisted-struct` | ERROR | `IP`/`RemoteAddr`/`ClientIP` field in persistence-layer struct |
| `geoip-get-all-banned` | WARNING | `Get_all()` on hot path; use `Get_city` or `Get_country_short` |
| `geoip-must-use-atomic-pointer` | ERROR | `sync.RWMutex` or `sync.Mutex` around `*ip2location.DB` |
| `geoip-sighup-handler-required` | ERROR | `main` missing `signal.Notify(..., syscall.SIGHUP, ...)` |
| `geoip-attribution-string-present` | ERROR | `IP2Location LITE` string missing from LICENSE-third-party.md + about + footer |
| `geoip-no-fsnotify-on-bin` | ERROR | `fsnotify.NewWatcher()` in enrichment package |
| `geoip-no-ip-key-cache` | ERROR | Cache keyed on `ip.String()` |

## CC-BY-SA-4.0 policy note — read before Week 17

**IP2Location LITE DB23 is CC-BY-SA-4.0.** The same license family as GeoLite2 and IPLocate.io, which the project's original "NO CC-BY-SA" policy explicitly rejects.

**The policy as written is unsatisfiable with any major free city-level GeoIP DB:**
- DB-IP Lite — CC-BY-4.0 (no ShareAlike, marginally more permissive)
- IPinfo Lite — CC-BY-SA-4.0
- IPLocate free — CC-BY-SA-4.0
- GeoLite2 — CC-BY-SA-4.0 **+ MaxMind EULA that mandates auto-updates** (air-gap-incompatible)

**Resolution (shipped in the CLAUDE.md update commit that follows this scaffold):** amend § License Rules with a CC-BY-SA carve-out for **non-linked data files** (GeoIP BIN databases). The binary surface gate stays strictly MIT/Apache/BSD/ISC. Attribution obligation is satisfied by this skill's 3-surface delivery matrix.

**If leadership rejects the carve-out:** budget for paid IP2Location DB23 Site License at Phase 10 (Filimo cutover). Price gated behind sales contact; comparable DBs range $99–$980/yr.

## Go patterns (canonical)

Full code in [`references/geoip-wrapper.md`](references/geoip-wrapper.md). Key shape:

```go
type dbHandle struct {
    db      *ip2location.DB
    version string
    loaded  time.Time
}

type GeoIP struct {
    path string
    cur  atomic.Pointer[dbHandle]
}

func (g *GeoIP) Lookup(ip netip.Addr) (Record, error) {
    h := g.cur.Load()
    if h == nil {
        noopFallbackTotal.Inc()
        return Record{}, nil  // no-op fallback; pipeline keeps flowing
    }
    rec, err := h.db.Get_city(ip.String())
    // ... no IP in error message
}

func (g *GeoIP) Reload() error {
    // 1. size floor: 50MB minimum
    // 2. OpenDB new handle
    // 3. probe 8.8.8.8 → US, 185.143.232.1 → IR
    // 4. version monotonicity
    // 5. atomic.Swap + 1s grace close of old
}
```

**Blocked shapes (by Semgrep):**
```go
// ❌ RWMutex — cache-line bounce at 9K EPS
type GeoIP struct { mu sync.RWMutex; db *ip2location.DB }

// ❌ Get_all — LITE returns "-" for most fields; pay serialization cost for nothing
return g.db.Get_all(ip)

// ❌ fsnotify — silently fails under overlayfs/NFS
w, _ := fsnotify.NewWatcher(); w.Add(cfg.GeoIPPath)
```

## CI gate summary

Four jobs in `.github/workflows/geoip-gate.yml` (full YAML in [`references/ci-geoip-gate.yml`](references/ci-geoip-gate.yml)):

1. **semgrep** — 8 rules on every PR touching the glob list.
2. **attribution** — `grep -q 'This site or product includes IP2Location LITE data' LICENSE-third-party.md` + `grep -rq 'IP2Location LITE' internal/about/`.
3. **hot-reload-integration** — `go test -race -tags=integration` runs 100 SIGHUPs-in-1s during 100 concurrent lookup goroutines; asserts p99 <500ms, no FD leak, zero lookup errors, observation of both v1 and v2 records.
4. **ip-leak-grep** — runs binary under 7K EPS k6, greps server log for IPv4/IPv6 patterns; any match fails the job.

Plus `license-audit` — `go-licenses report` asserts every direct + transitive dep is MIT/Apache-2.0/BSD-2-Clause/BSD-3-Clause/ISC. GeoIP BIN data falls under the carve-out, not the dep tree gate.

## Test strategies

- **Unit:** valid IP → expected country; invalid IP → empty record + nil error; nil `cur` → no-op metric incremented; reload on truncated BIN → rejected by size floor, old retained; failing probe → `rejected_validation` counter; version regression → `rejected_older_version`.
- **Integration (`-tags integration`):** real LITE BIN fixture (small sample cut), `syscall.Kill(os.Getpid(), syscall.SIGHUP)` during 100 concurrent lookup goroutines with `-race`, zero lookup errors, observe both v1 and v2 records across the cutover, FD count stable across 1000 swaps.
- **Property (`rapid`):** IPs in same `/24` yield same country; `Lookup` deterministic across N calls on one handle; `Record` fields always UTF-8 clean.
- **Chaos:** 100 SIGHUPs in 1s during 7K EPS load — p99 <500ms, no FD leak, last swap wins; SIGTERM during reload exits cleanly.
- **Regression:** Git-LFS checksum on `testdata/geoip-v1.BIN`; 50-IP JSON golden file (8.8.8.8, 1.1.1.1, Iranian ISP block, RFC1918, IPv6 sample).

## Opinionated defaults (6)

1. **`atomic.Pointer[*dbHandle]`, never `sync.RWMutex`.** Write-rare (monthly), read-ultra-heavy (9K EPS) — latency math wins unambiguously.
2. **Validate `8.8.8.8 → "US"` AND `185.143.232.1 → "IR"` AND size-floor 50MB AND version monotonicity BEFORE swap.** Bad BIN keeps old DB; no silent no-geo degradation.
3. **`Get_city` only; ban `Get_all` via Semgrep.** LITE zeros most `Get_all` fields anyway.
4. **SIGHUP is the only reload trigger.** No fsnotify. Matches the `scp && ssh kill -HUP` operator idiom used by `airgap-update-geoip.sh` (shipped in `iranian-dc-deploy` skill).
5. **Raw IP enters as `netip.Addr`, leaves as `Record` with no IP field ever.** CI log-grep after 7K EPS run gates the merge.
6. **Attribution on dashboard footer + `/about` JSON + `LICENSE-third-party.md`; `--license` CLI flag is nice-to-have only.**

## Scaffold status

Frontmatter + 14-item checklist + Semgrep skeleton shipped in this commit. Full Semgrep rule bodies + `references/geoip-wrapper.md` Go code + CI YAML + integration test harness land in **Phase 8 (Weeks 19–20)** per doc 28 §Full-optimization-roadmap, after `iranian-dc-deploy` ships and **after CLAUDE.md CC-BY-SA carve-out is merged** (next commit).