# geoip-pipeline-review

Full spec for the GeoIP enrichment guardrail. Research anchors: [`jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md`](../../../../jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md) §Gap 1 (lines 7–331).

## Why this skill exists

No public Claude skill covers GeoIP review. `anthropics/skills` (17 top-level), `samber/cc-skills-golang`, `trailofbits/skills`, `ClickHouse/agent-skills` — all zero hits. Pirsch analytics (Go reference) uses a `Tracker.SetGeoDB` thread-safe swap pattern but ships on GeoLite2 (CC-BY-SA + MaxMind EULA — unusable here).

statnive-live runs ~9K EPS sustained GeoIP lookup on an 8c/32GB box. The hot-path cost difference between `atomic.Pointer[T]` load (~1–3ns) and `sync.RWMutex.RLock` (~20–100ns, bounces cache lines across all CPUs serving lookups) is material at this volume.

## Why IP2Location LITE DB23

BIN options ranked (doc 28 §Recommended libraries, line 20):

| Source | Cost | License | Air-gap | Verdict |
|---|---|---|---|---|
| **IP2Location LITE DB23** | free | CC-BY-SA-4.0 | ✅ operator SCP | **v1 default** |
| IP2Location paid DB23 Site License | $99–$980/yr | Commercial | ✅ | **Phase 10 SamplePlatform upgrade** |
| DB-IP Lite | free | CC-BY-4.0 (no SA) | ✅ operator SCP | fallback only |
| MaxMind GeoLite2 | free | CC-BY-SA + EULA | ❌ auto-update required | **rejected** |
| IPLocate free | free | CC-BY-SA-4.0 | ✅ | fallback |
| IPinfo Lite | free | CC-BY-SA-4.0 | ✅ | fallback |

Operator SCP workflow: each operator registers their own LITE account (third-party redistribution forbidden), downloads the monthly BIN, `scp`s it to the server, runs `airgap-update-geoip.sh` (shipped in `iranian-dc-deploy/scripts/`, Phase 8).

## Why `atomic.Pointer[*dbHandle]` over `sync.RWMutex`

| Pattern | Read cost | Write frequency | Verdict |
|---|---|---|---|
| `sync.RWMutex` + `*ip2location.DB` | 20–100ns `RLock` (cache-line bounce across ~all CPUs serving 9K EPS) | monthly | ❌ rejected |
| `atomic.Pointer[*dbHandle]` | 1–3ns `.Load()` | monthly | ✅ adopted |

Go 1.19+ canonical. `ip2location-go/v9` exposes `OpenDB(path) (*DB, error)` with independent file handles, so `OpenDB(newPath)` can run while the old `*DB` still serves lookups — enabling the clean-swap pattern without mutex.

## Why SIGHUP over fsnotify

fsnotify silently loses events on:
- overlayfs (Docker)
- NFS / SMB
- Bind mounts
- kqueue (macOS)

The Go binary runs in all of these in various environments. SIGHUP is universal (`scp new.BIN server:/opt/statnive/geoip/ && ssh server systemctl kill -s HUP statnive`).

Note: `viper` uses fsnotify for config reload — that's fine for dev ergonomics. GeoIP BIN reload is **not** fine for fsnotify because a missed event = stale geo data for 30 days.

## Why pre-swap validation (both probes, both mandatory)

Operator ships a BIN. Before swapping, verify:

| Probe | Expected | What it catches |
|---|---|---|
| Size ≥ 50MB | >50M | Truncated download / partial SCP |
| `8.8.8.8 → "US"` | "US" | File is a real IP2Location DB (not random bytes) |
| `185.143.232.1 → "IR"` | "IR" | File actually has Iranian coverage (guards against wrong product) |
| `DatabaseVersion() ≥ current` | monotonic | Prevents accidental downgrade |

Bad BIN keeps the old DB in place. Metric `reload_total{result="rejected_*"}` increments. No silent no-geo degradation — CLAUDE.md Privacy Rule 1 assumes GeoIP is always present for IP-drop.

## Why 1-second grace close (not `defer`)

During the atomic swap, lookups in flight hold a pointer to the old `dbHandle`. If we `defer old.db.Close()` on success, we close **before** in-flight lookups complete → panic.

Correct pattern:

```go
go func() {
    time.Sleep(1 * time.Second)  // > p99 500ms budget
    _ = old.db.Close()
}()
```

1s is 2× p99 budget. Every in-flight lookup drained by the time close runs.

## Attribution delivery matrix (CC-BY-SA-4.0 §3(a)(1))

**Required string** (exact, no paraphrase):

> This site or product includes IP2Location LITE data available from https://lite.ip2location.com.

**Three surfaces** — all mandatory, none optional:

1. **`LICENSE-third-party.md`** at repo root — machine-checkable.
2. **`/about` JSON endpoint** — public, programmatic.
3. **Dashboard footer** — user-visible rendering in the SPA.

`--license` CLI flag alone does not satisfy the "reasonable, user-visible manner" requirement. The dashboard footer is the canonical IP2Location-recommended placement.

Delivery checked by Semgrep rule `geoip-attribution-string-present` (regex match across all three files) + CI job `attribution` that runs a literal `grep -q` for the string.

## Go patterns — full canonical wrapper

See [`references/geoip-wrapper.md`](references/geoip-wrapper.md) for the full 80-line Go file. Key structure (lifted from doc 28 lines 154–235):

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
        return Record{}, nil
    }
    rec, err := h.db.Get_city(ip.String())
    if err != nil {
        if errors.Is(err, ip2location.NoMatchError) {
            return Record{}, nil
        }
        return Record{}, fmt.Errorf("geoip lookup: %w", err)  // NO ip in message
    }
    return Record{rec.Country_short, rec.Country_long, rec.Region, rec.City}, nil
}

const minExpectedBytes = 50 * 1024 * 1024

func (g *GeoIP) Reload() error {
    // Pre-swap validation, version monotonicity, 1s grace close
    // [full impl in references/geoip-wrapper.md]
}
```

**SIGHUP wiring in main:**

```go
hup := make(chan os.Signal, 1)
signal.Notify(hup, syscall.SIGHUP)
defer signal.Stop(hup)
go func() {
    for range hup {
        if err := geo.Reload(); err != nil {
            slog.Error("geoip reload failed; keeping previous", "err", err)
            continue
        }
        slog.Info("geoip reloaded")
    }
}()
```

## CI gate

Five jobs in [`references/ci-geoip-gate.yml`](references/ci-geoip-gate.yml) (body from doc 28 lines 267–319):

1. **semgrep** — 8 rules.
2. **attribution** — literal `grep -q` for IP2Location LITE string in `LICENSE-third-party.md` + `internal/about/`.
3. **hot-reload-integration** — `go test -race -tags=integration -timeout=5m ./internal/enrich/...` — runs 100 concurrent lookup goroutines, fires SIGHUP 100× during load, asserts zero FD leak and correct v1→v2 record transition.
4. **ip-leak-grep** — 7K EPS k6 run → `grep -E '([0-9]{1,3}\.){3}[0-9]{1,3}' server.log` must return zero matches. Catches any new slog/zap callsite that leaks `remote_addr`.
5. **license-audit** — `go-licenses report ./...` asserts every direct + transitive dep is MIT/Apache-2.0/BSD-2-Clause/BSD-3-Clause/ISC. GeoIP BIN data falls under the carve-out, not this gate.

## Remaining uncertainties

- **Paid IP2Location DB23 Site License quote** — Phase 10 (SamplePlatform) decision; sales-gated.
- **CC-BY-SA-4.0 interpretation edge case** — "non-linked data files" carve-out in CLAUDE.md is our stance; legal review at next CLA review cycle.
- **Operator-specific LITE account registration** — third-party redistribution forbidden, so each self-hosted operator needs their own login at lite.ip2location.com. Document in operator onboarding, not in CI.

## Research anchors

- Doc 28 §Gap 1 (lines 7–331) — full spec, Semgrep bodies, CI YAML, wrapper code, probe validation rationale.
- CLAUDE.md § License Rules — CC-BY-SA carve-out added for non-linked data files.
- `iranian-dc-deploy/scripts/airgap-update-geoip.sh` — operator-facing SCP + SIGHUP workflow (Phase 8).