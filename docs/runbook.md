# Operator Runbook (Phase 7a)

This runbook covers the **backend stress tests** introduced in Phase 7a.
Production deployment + backup + TLS rotation procedures land in Phase 7b.

## Prerequisites

- Docker + the dev ClickHouse container: `docker compose -f deploy/docker-compose.dev.yml up -d`
- Go 1.25+
- [k6](https://k6.io/docs/getting-started/installation/) — `brew install k6` on macOS

## The four invariant assertions

Every Phase 7a test exists to prove one of these holds under stress:

| Invariant | Test | Where measured |
|---|---|---|
| Server-side event loss ≤ 0.05% | `make crash-test` + `make load-test` | ClickHouse `count()` vs k6 `iterations` |
| 7K EPS sustained | `make load-test` | k6 `iteration_rate_per_sec` |
| `/api/event` p99 < 200 ms | `make load-test` | k6 `http_req_duration_p99_ms` |
| WAL drop-oldest preserves recent rows | `make disk-full-test` | `count()` after restart > 0 |

## Tests

### `make bench` — non-CH benchmarks (fast)

Runs every Go `BenchmarkXxx` in `internal/...`. No external deps. Use to
catch hot-path regressions before merging:

```
make bench
```

Look for: `BenchmarkBurstGuard_Allow` < 100 ns/op, `BenchmarkChannel_Classify`
within 2× of the last green baseline.

### `make load-test` — 7K EPS for 5 minutes

Pre-flight: seed the test site row.

```
docker exec statnive-clickhouse-dev clickhouse-client -q \
  "INSERT INTO statnive.sites (site_id, hostname, slug, enabled) \
   VALUES (999, 'load-test.example.com', 'load-test', 1)"
```

Start the binary in a separate terminal (HTTP only — k6 doesn't speak our cert):

```
STATNIVE_TLS_CERT_FILE="" STATNIVE_TLS_KEY_FILE="" \
STATNIVE_DASHBOARD_BEARER_TOKEN="" \
STATNIVE_RATELIMIT_REQUESTS_PER_MINUTE=600000 \
STATNIVE_MASTER_SECRET=$(openssl rand -hex 32) \
./bin/statnive-live
```

(Rate limit is bumped because k6 fires from a single IP; the per-IP
default 6000/min would 429 most requests.)

Then in another terminal:

```
make load-test
```

Verify the iteration count matches what landed in ClickHouse:

```
docker exec statnive-clickhouse-dev clickhouse-client -q \
  "SELECT count() FROM statnive.events_raw WHERE hostname='load-test.example.com'"
```

Compare against k6's `iterations` field. The difference must be ≤ 0.05%.
If higher, the WAL or consumer is dropping events — start by checking
`grep wal /var/log/...` and `audit.jsonl`.

### `make crash-test` — kill -9 + WAL replay

Spawns the binary, fires 5K events, sends SIGKILL mid-batch, restarts
the binary, fires 5K more, asserts ClickHouse received ≥ 99.95% of the
total. Takes ~30s.

If this fails, the WAL replay is broken. Check
`internal/ingest/wal.go:Replay` and the consumer's startup-replay path.

### `make ch-outage-test` — buffer-and-drain

Fires events with CH up, stops CH for ~10s while events keep flowing
(events buffer to WAL), restarts CH, asserts the WAL drains. Takes
~45s. The full 10-min outage variant is manual:

```
docker stop statnive-clickhouse-dev
sleep 600 && docker start statnive-clickhouse-dev
```

— while a load test is running. The 10 GB default WAL cap holds ~70 M
events at typical 150-byte sizes, so 10 minutes at 7K EPS ≈ 4.2 M
events well within the cap.

### `make disk-full-test` — WAL drop-oldest

Spawns the binary with a 1 MB WAL cap (vs 10 GB production), stops CH,
fires 10K events. Verifies (a) the binary stays up, (b) `/healthz`
reports `wal_fill_ratio` near 1.0, (c) after CH restarts, some events
drain (the oldest were dropped by design).

If the binary crashes here, the cap-handling logic in
`internal/ingest/wal.go:enforceCap` has regressed.

### `make perf-tests` — all three at once

Convenience target running crash + ch-outage + disk-full in sequence.
~2 minutes total.

## Reading `/healthz` under load

```
curl -s http://localhost:8080/healthz | jq
```

Key fields under load:

- `wal_fill_ratio` — target < 0.5 at 7K EPS. If trending upward,
  consumer is falling behind.
- `events_per_sec` — should match the k6 rate within 10%.
- `last_insert_age_seconds` — < 1s under healthy load.
- `burst_dropped_total` — non-zero only if a misbehaving client is
  exceeding 500 PVs/min/visitor; cross-reference with
  `audit.jsonl | grep ingest.burst_dropped`.

## Phase 7c — Optimization & Hardening (audit gates)

### Bench baseline

`bench.out` at the repo root holds the Phase 7c baseline (Apple M1).
Future PRs should run `make audit` and pipe output through `benchstat`:

```bash
go install golang.org/x/perf/cmd/benchstat@latest
make audit > bench.new.txt
benchstat bench.out bench.new.txt
```

A regression > 5% on any line is a PR-blocker. Improvements are
welcome but must be re-baselined in the same PR.

### Air-Gap Verification (manual)

The binary must function with **zero** outbound network. Verify on a
Linux host (Hetzner / Iranian DC / VM) before any new release:

```bash
# 1. Build + spin up CH locally (still on loopback).
make build
docker compose -f deploy/docker-compose.dev.yml up -d clickhouse

# 2. Block all outbound traffic for the user the binary runs as,
#    EXCEPT loopback (CH + tracker + dashboard all on 127.0.0.1).
sudo iptables -A OUTPUT -j DROP -m owner --uid-owner $(id -u) ! -d 127.0.0.1/8

# 3. Boot the binary. Should start cleanly.
./bin/statnive-live &
APP_PID=$!

# 4. Health check + sample event + dashboard query.
curl -fsSL http://127.0.0.1:8080/healthz
curl -fsSL -X POST http://127.0.0.1:8080/api/event \
  -H 'User-Agent: Mozilla/5.0 (AirgapTest/1.0) BrowserLike' \
  -d '{"hostname":"airgap.example.com","pathname":"/","event_type":"pageview","event_name":"pageview"}'

# 5. Tear down.
kill $APP_PID
sudo iptables -D OUTPUT -j DROP -m owner --uid-owner $(id -u) ! -d 127.0.0.1/8
```

**Expected:** all curl commands return 200 / 202 / 204. If any timeout,
something in the binary made a non-loopback call → bug, file under
Architecture Rule (Isolation). The vendored `ip2location-go`
webservice path is the only known suspect; we never enable it (config
omits the API key), so the code path stays dead.

### Dependency licenses (pre-merge)

Every PR that touches `go.mod` must run:

```bash
go install github.com/google/go-licenses@latest   # one-time
make licenses
```

Allowed: MIT, Apache-2.0, BSD-2/3-Clause, ISC. Currently allowed under
the same gate but called out explicitly: `hashicorp/golang-lru/v2`
(MPL-2.0). MPL-2.0 is permissible because we do **not** modify the
upstream files — only modified MPL-2.0 files would require source
disclosure. Any new MPL dep needs the same justification documented
inline.

## Phase 4 — Tracker (install + verify)

### Install

Paste once into the host page's `<head>` (or before `</body>`):

```html
<script src="https://your-statnive-host/tracker.js" defer></script>
```

The tracker is served first-party from the analytics binary itself
(`go:embed`). No external CDN, no third-party DNS hop, no SRI tag.

### Public API

```js
statnive.track(name, props, value);   // custom event; props is an object, value is a number
statnive.identify(uid);               // raw uid; server hashes via SHA-256 + master_secret
```

`pageview` fires automatically on initial load and on every
`history.pushState` / `replaceState` / `popstate` (SPA route changes).

### Privacy default-off conditions

The tracker silently disables itself (both `track` + `identify` become
no-ops) when **any** of these hold:

- `navigator.doNotTrack === '1'` (DNT)
- `navigator.globalPrivacyControl === true` (Sec-GPC)
- `navigator.webdriver === true`
- `window._phantom` / `window.callPhantom` is set

No banner is required for users who've opted out — the opt-out is
structural.

### Verification recipe

1. Spin the binary locally: `make build && ./bin/statnive-live`.
2. Open `http://127.0.0.1:8080/tracker.js` — should return the embedded
   JS with `Content-Type: application/javascript; charset=utf-8` and
   `Cache-Control: public, max-age=3600`.
3. Drop a tiny test page in `/tmp/index.html`:
   ```html
   <script src="http://127.0.0.1:8080/tracker.js" defer></script>
   ```
   Open in a browser; DevTools → Network → look for `POST /api/event` with
   `Content-Type: text/plain`.
4. Confirm the event reached ClickHouse:
   ```bash
   docker exec statnive-clickhouse-dev clickhouse-client \
     -q "SELECT count() FROM statnive.events_raw WHERE hostname='127.0.0.1'"
   ```
5. **GPC test:** in DevTools Console, `navigator.globalPrivacyControl =
   true`, reload the page, confirm **no** `POST /api/event` fires.
6. **Custom event:** in Console, `statnive.track('test_event', {plan:
   'pro'}, 99)`. Confirm a row appears with `event_type='custom'`,
   `event_name='test_event'`, `event_value=99`.
7. **identify() round-trip:** `statnive.identify('user_a83f');
   statnive.track('purchase', {}, 100);` then
   ```sql
   SELECT user_id_hash FROM statnive.events_raw
   WHERE event_name='purchase' ORDER BY time DESC LIMIT 1
   ```
   The result must be a 64-char hex string, **not** the literal
   `user_a83f`. Privacy Rule 4 is enforced at the handler boundary —
   the raw value never reaches the WAL or ClickHouse.

### Bundle budget

`make tracker-size` and `make audit` both gate the dist file:

| Metric | Budget | Current |
|---|---:|---:|
| Minified | ≤ 1500 B | 1336 B |
| Gzipped | ≤ 700 B | 677 B |

A regression in either fails CI. The Go-side
[`internal/tracker/tracker_test.go`](../internal/tracker/tracker_test.go)
re-checks the embedded bytes inside `make test` so a manual edit can't
slip past either gate.

## Future hardening (Phase 7b)

- Backup + restore drill (`clickhouse-backup` + `age` + `zstd`)
- Manual TLS rotation drill (replace PEM + SIGHUP + verify new cert
  served on next handshake)
- Real-tracker correctness (queries match expected aggregations from
  Phase 4 tracker payloads)
- WAL replay zero-loss after SIGKILL (currently ~80% loss tracked in
  `crash_recovery_test.go`) — gated by the new
  `wal-durability-review` skill (kill-9 CI gate, 50 runs per PR).
- Consumer buffer-on-CH-outage (currently drops; should fill WAL)
- Integration-level PII grep (audit log + WAL byte scan for raw
  user_id strings) — the unit-level test in
  `internal/ingest/handler_test.go` already enforces the contract at
  the handler boundary.
- Full deployment runbook (bare metal, air-gap install bundle)

These wait for Phase 2c operational hardening (and the new doc 27
WAL hard gate) to land first.
