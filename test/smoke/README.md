# End-to-end boot smoke (`make smoke`)

One-command verification that `cmd/statnive-live/main.go` — the real
production wiring — serves every surface correctly. Drives the binary in
its as-shipped form against docker-compose ClickHouse and probes the
five Phase 5a surfaces plus the ingest round-trip.

## Why this exists

Exercises `main.go`'s middleware graph — `rateLimitMW`,
`BackpressureMiddleware`, `dashboardAuthMW`, the SPA mount, the
security-headers wrapper — end-to-end against the real binary on every
PR. Integration tests under `test/*.go` build their own `chi.Router`
test-side; `wal-killtest-smoke` boots the binary but only asserts WAL
durability. This harness is the only thing that verifies the production
router graph as shipped.

It is also the canonical pre-cutover verification for Phase 10
(SamplePlatform air-gapped Iranian DC) — see `docs/runbook.md`
§ Pre-cutover verification.

## Prerequisites

- Docker daemon running (for ClickHouse)
- Node 20 + `npm --prefix web run build` completed (or just `make build`,
  which chains `web-build`)

## Usage

```bash
make smoke
```

`make smoke` depends on `make build`, so the harness always runs against
a freshly-built binary with up-to-date SPA bytes embedded via
`//go:embed all:dist`.

To run twice back-to-back (smoke is idempotent — site row is
DELETE-then-INSERT with `mutations_sync = 2`):

```bash
make smoke && make smoke
```

Override the listen port (default 18199) when something is already on
the default:

```bash
STATNIVE_SMOKE_PORT=18299 make smoke
```

Other env knobs (all optional, defaults cover the common path):

| var | default |
|---|---|
| `STATNIVE_SMOKE_PORT`          | `18199` |
| `STATNIVE_SMOKE_SITE`          | `997` |
| `STATNIVE_SMOKE_HOSTNAME`      | `smoke.example.com` |
| `STATNIVE_SMOKE_TOKEN`         | `smoke-tok-abc` |
| `STATNIVE_SMOKE_CH_CONTAINER`  | `statnive-clickhouse-dev` |
| `STATNIVE_SMOKE_CH_ADDR`       | `127.0.0.1:19000` |

## What each probe asserts

| Probe | Surface | Asserts |
|---|---|---|
| `probe_healthz`     | `GET /healthz`              | 200 + JSON body contains `status`, `wal_fill_ratio`, `clickhouse`, `wal_fsync_p99_ms` |
| `probe_tracker`     | `GET /tracker.js`           | 200, `Content-Type: application/javascript`, `X-Content-Type-Options: nosniff`, body ≤ 2 KB and starts with `!function` (IIFE wrapper) |
| `probe_spa_shell`   | `GET /app/`                 | 200, CSP `default-src 'self'`, nosniff, Referrer-Policy `strict-origin-when-cross-origin`, body has `<div id="statnive-app">`, bearer placeholder rewritten (no `STATNIVE_BEARER_PLACEHOLDER` in body; `content="$TOKEN"` present) |
| `probe_spa_asset`   | `GET /app/assets/*.js`      | 200, JavaScript MIME, `Cache-Control: public, max-age=31536000`, body ≥ 5 KB (real bundle, not HTML fallback); asset URL extracted from the shell HTML |
| `probe_ingest`      | `POST /api/event` (×10)     | Every request returns 202 (ack-after-fsync contract) |
| `probe_ingest_count`| `events_raw` in ClickHouse  | `count()` reaches exactly 10 within 10 s (50 ms poll) |
| `probe_stats_auth`  | `GET /api/stats/overview`   | Without `Authorization` header: 401. With the configured bearer: 200 and JSON body contains all 5 KPI keys (`pageviews`, `visitors`, `goals`, `revenue_rials`, `rpv_rials`) |

On any assertion failure the harness prints the exact response + context
and exits non-zero.

## Debugging a failing probe

Run the harness under `bash -x` to see every command:

```bash
bash -x ./test/smoke/harness.sh
```

The binary's stdout+stderr stream to a tempfile under `$WORK`; on early
boot failure the harness `cat`s that log before exiting so the failure
is in-terminal.

A failing probe prints the HTTP status + response headers + body prefix
that made it fail. Reproduce with the printed `curl` shape.

## Invariants this harness refuses to break

1. Drives the **real binary** — no `httptest.Server`, no manually-wired
   `chi.Router`.
2. ClickHouse comes from `deploy/docker-compose.dev.yml` — the same
   container name (`statnive-clickhouse-dev`) and ports as
   `integration-tests` and `wal-killtest-smoke`. Never a second CH image.
3. Every probe asserts a specific invariant with evidence on failure.
4. Idempotent — re-running without manual cleanup works.
5. Clean teardown via `trap cleanup EXIT INT TERM` — binary killed,
   tempdir removed, CH container left up (same convention as
   `wal-killtest`; callers control `docker compose down`).

## CI wiring

The `smoke-test` job in `.github/workflows/ci.yml` runs this harness on
every PR in parallel with the existing six jobs. Wall time ~90 s
(ClickHouse startup dominates; build is cached by `actions/setup-go`).

## Scope notes

This harness is intentionally small. Panel-level SPA interaction
(clicking through Sources / Pages / SEO tabs) lands in Phase 5c with
Playwright. Rate-limit burst / backpressure 503 / air-gap under
`iptables -P OUTPUT DROP` are covered by `test/perf/` and Phase 8's
`airgap-test` — keeping those out of smoke keeps it fast and
deterministic.
