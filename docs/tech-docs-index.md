# Technology docs cache — Context7 index (2026-04-17)

> Referenced from [PLAN.md](../PLAN.md). Full per-library doc cache lives at [`tech-docs/`](tech-docs/). Each file carries YAML frontmatter and distilled API snippets aligned to statnive-live's usage.

## Plan decisions that originated from this cache

1. **Rate limiting**: Switch from `golang.org/x/time/rate` (manual) to `go-chi/httprate` (MIT, chi-native). Use `httprate.LimitByRealIP(100, time.Minute)` on `/api/event`. Handles NAT/proxy correctly.
2. **Preact signals**: Use `@preact/signals` instead of useState for dashboard state. Signals auto-update JSX without re-renders — better for real-time metric displays. Pass `{signal}` directly in JSX (not `{signal.value}`) to bind to DOM text nodes with zero re-renders.
3. **ClickHouse rollups**: Schema uses `AggregateFunction(uniqCombined64, FixedString(16))` — HyperLogLog approximation with ~0.5% error, ~2–3× lower memory than `uniqExact`. All rollup `ORDER BY` clauses lead with `site_id` for multi-tenant index pruning.
4. **Config loader**: `spf13/viper` fsnotify-based `WatchConfig` + `OnConfigChange` replaces the SIGHUP hot-reload mechanism noted elsewhere in this plan — SIGHUP is kept only as a manual fallback.
5. **LRU cache**: `hashicorp/golang-lru/v2` with `v2/expirable` for TTL semantics; generics-ready and MPL-2.0 (weak copyleft, use unmodified).

## Libraries cached (14)

| Library | Context7 ID | Cache file | Delta vs prior snapshot |
|---------|-------------|------------|-------------------------|
| clickhouse-go/v2 | `/clickhouse/clickhouse-go` | [clickhouse-go.md](tech-docs/clickhouse-go.md) | None — `PrepareBatch → Append → Send` stable |
| go-chi/chi v5 | `/go-chi/docs` | [go-chi.md](tech-docs/go-chi.md) | None. Security warning on `middleware.RealIP`: only register behind a trusted reverse proxy |
| go-chi/httprate | `/go-chi/httprate` | [httprate.md](tech-docs/httprate.md) | None. `LimitByRealIP` vs `LimitByIP` choice depends on deployment topology |
| ClickHouse server | `/clickhouse/clickhouse-docs` | [clickhouse-server.md](tech-docs/clickhouse-server.md) | None. AggregatingMergeTree + MV + `PARTITION BY toYYYYMM()` pattern confirmed |
| @preact/signals | `/preactjs/signals` | [preact-signals.md](tech-docs/preact-signals.md) | None. Re-emphasised `{signal}` vs `{signal.value}` for zero-rerender DOM updates |
| uPlot | `/leeoniya/uplot` | [uplot.md](tech-docs/uplot.md) | None. `uPlot.sync()` cursor sync confirmed for cross-panel hover |
| k6 | `/grafana/k6-docs` | [k6.md](tech-docs/k6.md) | None. `maxVUs` anti-pattern warning — use `preAllocatedVUs` generously |
| spf13/viper (v1.20.1) | `/spf13/viper` | [viper.md](tech-docs/viper.md) | None. fsnotify-based hot reload (`WatchConfig` + `OnConfigChange`) supersedes SIGHUP approach |
| ip2location-go/v9 | `/ip2location/ip2location-go` | [ip2location-go.md](tech-docs/ip2location-go.md) | None. Use `Get_city` / `Get_country` over `Get_all` in hot path |
| medama-io/go-useragent | `/medama-io/go-useragent` | [go-useragent.md](tech-docs/go-useragent.md) | None. Singleton `NewParser()` pattern mandatory |
| bits-and-blooms/bloom (v3) | `/bits-and-blooms/bloom` | [bloom.md](tech-docs/bloom.md) | None. `NewWithEstimates(10M, 0.001) ≈ 18MB` matches PLAN budget |
| hashicorp/golang-lru (v2) | `/hashicorp/golang-lru` | [golang-lru.md](tech-docs/golang-lru.md) | v2 import path: `github.com/hashicorp/golang-lru/v2` — generics-ready. Note MPL-2.0 weak-copyleft caveat |
| Vite | `/websites/vite_dev` | [vite.md](tech-docs/vite.md) | **🔴 API DELTA** — see below |
| Vitest (v4) | `/vitest-dev/vitest` | [vitest.md](tech-docs/vitest.md) | None. v4 GA confirmed; `vi.useFakeTimers` / `vi.setSystemTime` stable |
| Preact | `/preactjs/preact-www` | [preact.md](tech-docs/preact.md) | None. `preact/hooks` + `@preact/signals` integration stable |

## Libraries NOT indexed in Context7

Documented in [`tech-docs/_unindexed.md`](tech-docs/_unindexed.md) with direct pkg.go.dev / GitHub references.

- **tidwall/wal** — Context7 surfaced only tidwall's BuntDB / Rtree / Tile38 / Pogocache, not `wal`. The `Open / Write / Read / TruncateFront / Sync` API is stable; consult pkg.go.dev.
- **lukechampine.com/blake3** — only Rust / reference / .NET BLAKE3 ports are indexed. Go port API stable: `blake3.Sum256(data)` or `blake3.New(16, key)` for BLAKE3-128 keyed hashing.

## 🔴 API deltas since 2026-04-17 snapshot

Only **Vite** has notable deprecations. All other libraries verify clean against the snapshot.

1. **Vite — `build.rollupOptions` → `build.rolldownOptions`.** Vite now bundles with **Rolldown** (Rust-based, rollup-compatible). `rollupOptions` still works as a deprecated alias. **Action:** author `web/vite.config.ts` with `rolldownOptions` from day 1. Reference: https://rolldown.rs/reference/
2. **Vite — JSX config moved from `esbuild.*` to `oxc.jsx.*`.** Preact importSource is now configured via `oxc: { jsx: { importSource: 'preact' } }`. The older `esbuild.jsxImportSource` is no longer the canonical path.
3. **hashicorp/golang-lru — v2 is the current line.** Import as `github.com/hashicorp/golang-lru/v2` and `…/v2/expirable`. v1 is legacy.
4. **Vitest — v4.0.7 is current** (v3.2.4 also indexed). No breaking changes for our planned usage.

All existing architectural decisions in the plan (schema, identity, transport, pipeline, license strategy) remain valid. The only concrete pre-Phase-0 code touch-up is the `vite.config.ts` option names.
