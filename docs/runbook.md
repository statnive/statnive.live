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

### Pre-cutover verification — `make smoke`

`make smoke` is the canonical one-command readiness check for the
production wiring. It drives the real `cmd/statnive-live/main.go` binary
against docker-compose ClickHouse and probes every surface a Phase 10
operator would touch:

1. `GET /healthz` — health reporter keys (`status`, `wal_fill_ratio`,
   `clickhouse`, `wal_fsync_p99_ms`)
2. `GET /tracker.js` — embedded IIFE tracker + nosniff +
   `application/javascript`
3. `GET /app/` — SPA shell + CSP + nosniff + Referrer-Policy + bearer
   placeholder rewritten
4. `GET /app/assets/*.js` — hashed SPA bundle + long-cache
5. `POST /api/event` (×10) — ingest → WAL → consumer → ClickHouse
   count-back
6. `GET /api/stats/overview` — bearer-auth enforcement (401 without,
   200 + 5 KPI keys with)

```bash
make smoke
```

Wall time ≈ 90 s on a laptop (ClickHouse boot dominates; the probes
themselves run in <2 s). Idempotent — re-run back-to-back with no
manual cleanup.

**Phase 10 operator use:** after the offline bundle lands on the
Iranian-DC VPS and `deploy/airgap-install.sh` completes, run `make
smoke` against the freshly-installed binary before cutover. Every probe
green = every production surface is serving correctly. See
[`test/smoke/README.md`](../test/smoke/README.md) for env overrides and
debugging notes.

### Dashboard e2e debugging (`npm --prefix web run e2e`)

Phase 5c ships 25 Playwright tests at `web/e2e/` covering auth, panels,
navigation, filters, realtime, and multi-tenant site-switching. Each
test's highest-tier assertion is a CH-oracle correlation query
(`docker exec clickhouse-client`) — the UI's rendered KPI must match
what the rollup tables report.

**Running locally:**

```bash
docker compose -f deploy/docker-compose.dev.yml up -d clickhouse
make build                      # produces bin/statnive-live with fresh SPA embedded
npm --prefix web ci             # first run only
npm --prefix web run e2e        # 25 tests, ~30s wall time
```

**Interactive debugging:**

```bash
npm --prefix web run e2e:ui     # Playwright UI mode — filter, replay, inspect traces
```

**Inspecting a failing CI run:** download the `playwright-report`
artifact from the failed `dashboard-e2e` job, then:

```bash
unzip playwright-report.zip -d /tmp/report && cd /tmp/report
npx playwright show-report      # launches interactive HTML report + trace viewer
```

**Reuse state:** globalSetup boots `bin/statnive-live` on port 18299
with bearer `e2e-tok-xyz` and seeds site_ids 801 + 802. globalTeardown
cleans those rows so local CH stays tidy. Port 18299 is distinct from
the smoke harness (18199) so both can coexist during dev.

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

## Backup & restore (Phase 7b2)

Operator-facing copy of the encrypted backup procedure. The skill
reference at
`.claude/skills/clickhouse-operations-review/references/backup-restore-drill.md`
is the source of record; this section is the SOP an on-call operator
reads. Keep them in sync — when the skill reference changes, port the
delta here.

### Stack

| Component | Version | License | Role |
|---|---|---|---|
| [`Altinity/clickhouse-backup`](https://github.com/Altinity/clickhouse-backup) | v2.5.20+ | Apache-2.0 | Backup orchestration |
| [`filippo.io/age`](https://github.com/FiloSottile/age) | 1.2+ | BSD-3 | Encryption (recipient pubkey on operator workstation) |
| `zstd` | 1.5+ | BSD-3 | Compression level 19 |

### Cadence

| Type | Schedule | Retention |
|---|---|---|
| Full | Sunday 02:15 UTC | 30 days |
| Incremental | Every 6 hours | 30 days |
| Drill | Every release + nightly cron | n/a (validation only) |

### Storage

- **Primary sink:** S3 (or S3-compatible: Backblaze B2, Wasabi, MinIO).
- **Iranian DC:** Second sink to a ParsPack FTP bucket (50GB free on
  VPS tier). Outside-Iran sink reachable only when NIN connectivity is
  up.
- **Encryption:** All files piped through `age` with a single recipient
  pubkey. Private key on offline operator workstation. **Restore
  requires the private key in hand.**

### Config — `deploy/backup/config.yml`

```yaml
general:
  remote_storage: s3
  backups_to_keep_local: 2
  backups_to_keep_remote: 120  # 30d × 4/day

clickhouse:
  username: default
  password: ${CLICKHOUSE_PASSWORD}
  host: 127.0.0.1
  port: 9000
  data_path: ${DATA_DIR}  # read from env; never hardcode /var/lib/clickhouse

s3:
  access_key: ${S3_ACCESS_KEY}
  secret_key: ${S3_SECRET_KEY}
  bucket: statnive-backup
  region: ${S3_REGION}
  path: clickhouse/{cluster}/{shard}
  compression_format: zstd
  compression_level: 19

# age encryption sidecar — age_recipient_file is the OPERATOR's pubkey
custom_encryption:
  pre_upload_command: 'age -r $(cat /etc/statnive/backup-age.pub) -o $FILE.age $FILE && rm $FILE'
  post_download_command: 'age -d -i /etc/statnive/backup-age.key -o $FILE ${FILE}.age && rm ${FILE}.age'
```

### Restore drill — manual procedure (Phase 7b2)

Automated by `deploy/backup/drill.sh` + `.github/workflows/backup-drill-nightly.yml`
as of Phase 2c (PR #36). The manual procedure below is still the SOP
when an operator is triaging a drill failure by hand on a dedicated
host (NOT production):

1. Install `clickhouse-backup` on the drill host (same version as
   production):
   ```bash
   wget -q https://github.com/Altinity/clickhouse-backup/releases/download/v2.5.20/clickhouse-backup.tar.gz
   tar xzf clickhouse-backup.tar.gz
   sudo mv build/linux/amd64/clickhouse-backup /usr/local/bin/
   ```
2. Copy `deploy/backup/config-drill.yml` from the production host
   (drill-only; points at the drill ClickHouse instead of production).
3. Place the `age` private key at `/etc/statnive/backup-age.key` and
   set `chmod 0600`.
4. List remote backups:
   ```bash
   clickhouse-backup --config deploy/backup/config-drill.yml list remote
   ```
5. Pick the most recent full backup `NAME` and restore it:
   ```bash
   clickhouse-backup --config deploy/backup/config-drill.yml restore_remote "$NAME"
   ```
6. **Row-count parity check** — for each table, compare drill ↔ prod:
   ```bash
   for TABLE in events_raw hourly_visitors daily_pages daily_sources; do
     P=$(clickhouse-client --host PROD_HOST -q \
           "SELECT sum(rows) FROM system.parts WHERE table='$TABLE' AND active")
     D=$(clickhouse-client --host DRILL_HOST -q \
           "SELECT sum(rows) FROM system.parts WHERE table='$TABLE' AND active")
     [ "$P" = "$D" ] && echo "$TABLE OK ($P rows)" || echo "$TABLE FAIL prod=$P drill=$D"
   done
   ```
7. **Rollup mergeability check** — catches `AggregateFunction` state
   corruption that row-count alone misses:
   ```bash
   clickhouse-client --host DRILL_HOST -q \
     "SELECT countMerge(visitors_hll_state) FROM hourly_visitors FINAL FORMAT Null"
   ```
8. Tear down the drill instance.

### When to run the drill

- **Every release:** before `git tag v*`, restore last night's backup,
  walk steps 5–7, confirm parity. A failed drill blocks the release.
- **Nightly cron (host-side):** authoritative nightly drill runs on
  the operator's drill host via cron — see `deploy/backup/README.md`
  for the template. This is the release-blocking SoT.
- **CI drill (on-demand):** `.github/workflows/backup-drill-nightly.yml`
  can be triggered manually via `gh workflow run backup-drill-nightly`
  or the GitHub UI. See "Known issues" below for why the scheduled
  nightly cron was removed.
- **Before any schema migration:** full + incremental snapshot
  immediately before `make migrate`. Same restore drill afterward
  proves the migration itself didn't corrupt the data set.

### Known issues

- **CI drill is workflow_dispatch-only (2026-04-23).** The
  `backup-drill-nightly` GitHub Actions workflow used to run on a
  nightly `0 4 * * *` cron. It was demoted to manual-dispatch because
  `clickhouse/clickhouse-server:24.12-alpine` refuses `DROP TABLE IF
  EXISTS` against materialized-view inner tables without the
  `/var/lib/clickhouse/flags/force_drop_table` flag — even for empty
  tables and with `max_table_size_to_drop=10_000_000_000_000` (10 TB)
  set in a `config.d/` fragment — breaking `clickhouse-backup
  restore_remote`'s pre-create drop loop. Reproduced in PRs #36→#40.
  Operator-side drills via `deploy/backup/drill.sh` against real CH
  are unaffected. Tracked as **v1.1-ci-drill** in `PLAN.md`;
  re-enablement depends on either (a) a CH point release that drops
  the MV-inner-table flag requirement, (b) a workaround that
  continuously recreates the flag file during restore, or (c) a
  different CH image with the same Atomic-engine semantics.

## Disk full (CH error code 243)

Exact error text:
`DB::Exception: Cannot reserve N.NN MiB, not enough space. (NOT_ENOUGH_SPACE) Code: 243`.

1. Verify `/healthz` flips to 503 and `wal_fill_ratio` is climbing
   toward 0.80 — the back-pressure middleware (Phase 7b1b) should
   already be returning 503 to the tracker.
2. Free space, in order of preference:
   - `ALTER TABLE events_raw DROP PARTITION '202603'` — drop oldest
     partition.
   - `ALTER TABLE events_raw DROP DETACHED PART '...'` — drop
     detached parts from failed mutations.
   - If partition is bigger than `max_partition_size_to_drop`
     (default 50 GiB), override with
     `touch /var/lib/clickhouse/flags/force_drop_table`.
3. After free, confirm the consumer drains the WAL (`wal_fill_ratio`
   shrinks toward 0.0 in `/healthz`).
4. If WAL is still pinned high, check the audit log for
   `wal.ch_insert_failed` entries — the consumer's bounded retry will
   eventually give up; restart the binary to retry from scratch.

## Phase 2b — Auth + RBAC operator SOPs

### First-run admin bootstrap

Set two env vars before the first boot. The binary hashes the password
with bcrypt cost 12 and creates a single admin user at `site_id =
auth.bootstrap.site_id` (default 1). Re-booting with the same vars is
idempotent — it does not re-hash the password.

```bash
STATNIVE_BOOTSTRAP_ADMIN_EMAIL='admin@example.com' \
STATNIVE_BOOTSTRAP_ADMIN_PASSWORD='correct horse battery staple …' \
./bin/statnive-live
```

Confirm the user landed:

```bash
clickhouse-client -q "SELECT email, role, disabled
FROM statnive.users FINAL"
```

Expect one row: `admin@example.com    admin    0`.

Audit-trail assertion: the `auth.jsonl` sink carries one
`auth.bootstrap` event with `email_hash` (SHA-256) and `user_id` — the
raw email MUST NOT appear anywhere in the log.

### Rotate the admin password

There is no self-serve password-reset flow in v1 (ships in v1.1).
Operators rotate via a direct-DB update:

```bash
# Generate a new bcrypt cost-12 hash on the operator laptop.
htpasswd -bnBC 12 "" 'new-passphrase' | tr -d ':\n' | sed 's/\$2y/\$2a/'
# Paste the hash into the CH update:
clickhouse-client -q "INSERT INTO statnive.users (
  user_id, site_id, email, username, password_hash, role, disabled,
  created_at, updated_at
) SELECT user_id, site_id, email, username, '<paste hash>',
  role, disabled, created_at, now() FROM statnive.users FINAL
  WHERE email = 'admin@example.com'"
```

After the new row lands, every active session for that user MUST be
revoked server-side — see "Revoke all sessions" below. The
CachedStore wrapper in production cascades this automatically when
the password change routes through the admin API (Phase 3c); direct-DB
updates bypass that cascade, so operators run the revoke step
explicitly.

### Revoke all sessions for a user

```bash
clickhouse-client -q "INSERT INTO statnive.sessions (
  session_id_hash, user_id, site_id, role, created_at, last_used_at,
  expires_at, revoked_at, updated_at, ip_hash, user_agent
) SELECT session_id_hash, user_id, site_id, role, created_at,
  last_used_at, expires_at, now(), now(), ip_hash, user_agent
FROM statnive.sessions FINAL
WHERE user_id = '<user_id>' AND revoked_at = toDateTime(0)"
```

The in-memory session cache in the running binary has a 60-second
TTL; operators who need immediate effect can either wait 60 s or
restart the binary (every session cookie then fails lookup → 401
until re-login).

### Local dev without TLS

`auth.session.secure=true` is enforced in production. For local HTTP
dev set `STATNIVE_DEV=1` AND `STATNIVE_AUTH_SESSION_SECURE=false`;
any other combination (Secure=false without STATNIVE_DEV=1) is
rejected at boot.

### API-token provisioning (CI + long-lived automation)

Operators can issue `api_only` tokens without going through the login
flow. The binary accepts raw tokens via env var, hashes them at boot,
and never persists the raw form:

```bash
STATNIVE_API_TOKENS='ci-smoke:<raw-token>,backup-cron:<raw-token>' \
./bin/statnive-live
```

Each raw token maps to a synthetic `*User` with `Role=api`. Rotating =
restart with a new env-var value. Phase 3c adds `POST
/api/admin/tokens` for admin-API rotation.

### Password-policy posture

Phase 2b is **admin-seeded only** — the binary does not enforce a
password-complexity policy, because the operator is trusted to pick
a strong passphrase. Phase 11 (SaaS self-serve signup) MUST add NIST
800-63B (8+ chars, HaveIBeenPwned top-10k blocklist, no composition
rules). Until then the operator is responsible for choosing
passwords of adequate length + entropy.

## Phase 3c — Admin CRUD operator SOPs

### Provision a new operator user (via dashboard)

Sign in to `/app/` as an existing admin → "Admin" nav tab → "Users"
→ fill the form (email, username, password, role) → "Create user".
The new user can sign in at `/app/login` immediately. Role choices:

- `admin` — full `/api/admin/*` CRUD + stats dashboard
- `viewer` — read-only stats dashboard; no admin nav tab
- `api` — `/api/stats/*` access via `Authorization: Bearer <token>`
  only; no cookie session, no admin routes

All mutations live in `audit.jsonl` as `admin.user.*` events with
`actor_user_id` + `target_user_id` + hashed email — raw email never
hits the audit sink.

### Disable a user (e.g. credential compromise)

Admin → Users tab → row → "Disable". Server-side:
1. CachedStore flips `users.disabled = 1` (ReplacingMergeTree bump).
2. `RevokeAllUserSessions` cascade invalidates every active session
   for that user (in-memory cache cleared; CH sessions marked revoked).
3. Next request carrying the user's cookie returns 401 within the
   60-second session-cache TTL.

Re-enable is **not supported in v1** (the Enable endpoint is a no-op
202 pending `auth.Store.SetDisabled` in v1.1). To recover a disabled
user today, create a new user with the same email and delete the
disabled row via `clickhouse-client` if the email conflict is a
concern.

### Add a goal (mark custom events as conversions)

Admin → Goals tab → fill form → "Create goal".

- **Event name (exact match)** — the literal `event_name` your tracker
  sends via `statnive.track('purchase', {...})`. v1 ships exact-match
  only (`event_name_equals`). Path-based matching (`path_prefix`,
  `path_regex`) lands in v1.1 — the Enum8 column extends without a
  migration.
- **Value (rials)** — optional revenue attribution. When set, the
  ingest pipeline stamps this on every matching event's `event_value`
  **overriding** any client-supplied value (server-authoritative; see
  Security note below). Leave 0 to use whatever value the tracker
  sends (e.g. dynamic checkout totals).

Canonical SamplePlatform examples (from research doc 18 §112):

| Event name  | Value (rials) | Notes                     |
|-------------|---------------|---------------------------|
| `subscribe` | 0             | Free signup conversion    |
| `signup`    | 0             | Account creation          |
| `watch`     | 0             | Content engagement        |
| `purchase`  | 500_000       | Override tracker amount   |

**Security note — why server-wins on `event_value`.** The `/api/event`
endpoint intentionally has no HMAC signature (CLAUDE.md Security #3
— hostname validation is the tracker's only auth). Any visitor's
browser JS can POST `{"event_value": 99999999}` and inflate RPV /
revenue dashboards. When you set a non-zero `value_rials` on a goal,
the server overrides the client value; when you leave it 0, the
tracker value passes through. v1.1 adds HMAC-signed tracker payloads
for trusted client values.

### Rotate the goals cache without a restart

Admin mutations reload the in-memory snapshot inline — no action
needed. For direct-CH goal INSERTs (e.g. `clickhouse-client` from the
operator laptop), send `SIGHUP` to the running binary:

```bash
kill -HUP $(pgrep -f statnive-live)
```

`audit.jsonl` will carry a `goals.reload_succeeded` event within
~100 ms (or `goals.reload_failed` if the reload errored; previous
snapshot is retained — fail-closed).

### First-run: load SamplePlatform's canonical goals

Not seeded automatically (per research doc 18 — event names are
site-specific). Create them through the dashboard after first login,
or use `clickhouse-client` + SIGHUP:

```bash
docker exec statnive-clickhouse-dev clickhouse-client -q "
INSERT INTO statnive.goals (goal_id, site_id, name, match_type, pattern, value_rials, enabled)
VALUES
  (generateUUIDv4(), 1, 'Subscribe', 'event_name_equals', 'subscribe', 0, 1),
  (generateUUIDv4(), 1, 'Signup', 'event_name_equals', 'signup', 0, 1),
  (generateUUIDv4(), 1, 'Watch', 'event_name_equals', 'watch', 0, 1);
"
kill -HUP $(pgrep -f statnive-live)
```

## Phase 7b status (2026-04-21)

All Phase 7b deliverables shipped:

- ✅ WAL replay zero-loss after SIGKILL (Phase 7b1b — 50-iter kill-9
  gate green; wired into CI nightly via Phase 7b2).
- ✅ Consumer buffer-on-CH-outage (Phase 7b1b — `wal.Ack` only on CH
  commit; bounded retry).
- ✅ Backup + restore drill — runbook above; CI automation in Phase
  2c.
- ✅ Manual TLS rotation drill — automated regression at
  `internal/cert/rotation_e2e_test.go`.
- ✅ Real-tracker correctness — `test/tracker_correctness_test.go`
  replays the JS-emitted golden against the full pipeline.
- ✅ Integration-level PII grep — `test/pii_leak_test.go` byte-scans
  WAL segments + audit JSONL + `events_raw`.
- ✅ `wal_fsync_p99_ms` on `/healthz` (closes
  `wal-durability-review` item 7 — last open of 10).

Next slice: **Phase 5 frontend** is unblocked.

---

## Phase 8 — Deploy + airgap bundle (operator SOPs)

### Air-gap bundle install

Applies to any Linux 5.x+ host (Hetzner CX32 staging, Asiatech Iranian
DC, enterprise on-prem). ClickHouse 24+ must already be running, bound
to 127.0.0.1:9000. The bundle is architecture-specific
(`*-linux-amd64-airgap.tar.gz`); arm64 variants build the same way by
overriding `GOARCH` at `make build`.

```bash
# 1. From a trusted bastion, verify the received tarball.
cd /tmp
sudo ./deploy/airgap-verify-bundle.sh statnive-live-v0.8.0-linux-amd64-airgap.tar.gz /etc/statnive/release-key.pub
# exit 0 = SHA256 + Ed25519 both OK
# exit 1 = SHA256 mismatch — REJECT
# exit 2 = signature mismatch / missing — REJECT

# 2. Unpack + install.
tar -xzf statnive-live-v0.8.0-linux-amd64-airgap.tar.gz
cd statnive-live-v0.8.0-linux-amd64-airgap
sudo ./deploy/airgap-install.sh

# 3. Seed the master secret (choose ONE).
#    Option A — file:
sudo openssl rand -hex 32 > /etc/statnive-live/master.key
sudo chmod 0600 /etc/statnive-live/master.key
sudo chown statnive:statnive /etc/statnive-live/master.key
#    Option B — systemd env (drop-in):
sudo mkdir -p /etc/systemd/system/statnive-live.service.d
cat <<'EOF' | sudo tee /etc/systemd/system/statnive-live.service.d/env.conf
[Service]
Environment="STATNIVE_MASTER_SECRET=<64-hex-chars>"
EOF
sudo systemctl daemon-reload

# 4. First-run bootstrap envs (one-shot; comment out after first start).
cat <<'EOF' | sudo tee -a /etc/systemd/system/statnive-live.service.d/env.conf
Environment="STATNIVE_BOOTSTRAP_ADMIN_EMAIL=ops@example.com"
Environment="STATNIVE_BOOTSTRAP_ADMIN_PASSWORD=<32+ chars>"
Environment="STATNIVE_BOOTSTRAP_ADMIN_USERNAME=ops"
EOF
sudo systemctl daemon-reload

# 5. Start + verify.
sudo systemctl start statnive-live
curl -s http://127.0.0.1:8080/healthz | jq .
# status=ok, clickhouse=up, wal_fill_ratio=0
```

Common troubleshooting:

- **`systemctl status` shows `activating` then `failed`** — master secret
  is not readable. `sudo journalctl -u statnive-live -n 50`; look for
  `master secret: …`. The loader now uses `os.OpenRoot` (Phase 7d F7),
  so symlinks pointing outside `/etc/statnive-live/` fail by design.
- **iptables conflict with UFW** — `airgap-install.sh --apply-iptables`
  loads `deploy/iptables/rules.v4`, which default-DROPs INPUT. If UFW
  is managing firewall rules, skip the flag and edit UFW's rulebook to
  permit 80 / 443 / the tracker-client subnet.
- **systemd Type=notify mismatch** — the unit ships with `Type=simple`
  (the default). If an operator changed it to `notify`, the binary
  doesn't call `sd_notify` and systemd times out.

### GeoIP update procedure

Monthly cadence for the LITE DB23. SamplePlatform cutover (Phase 10)
will upgrade to paid DB23 with attribution waived.

```bash
# On the operator workstation (outside the air-gap if needed):
wget -O /tmp/IP2LOCATION-LITE-DB23.BIN https://lite.ip2location.com/download?file=DB23LITEBIN

# Transfer to the target host:
scp /tmp/IP2LOCATION-LITE-DB23.BIN root@statnive-host:/var/tmp/

# On the target host (same filesystem as the GeoIP dir — required for
# atomic mv; the script aborts otherwise):
sudo /opt/statnive-bundle/deploy/airgap-update-geoip.sh /var/tmp/IP2LOCATION-LITE-DB23.BIN

# Observe success in audit.jsonl (should appear within 2 seconds):
sudo tail -n 5 /var/log/statnive-live/audit.jsonl | grep geoip.reloaded
```

Exit codes:

- `0` — new BIN installed + SIGHUP sent + reload event observed.
- `1` — precondition failure (missing BIN, wrong filesystem, not root).
- `2` — reload event not observed within 30 s. The pre-swap probe
  (`8.8.8.8` → non-empty country; `185.143.232.1` → `IR`) failed; the
  OLD BIN is still active. Inspect `audit.jsonl` for
  `geoip.reload_failed` + the attached `err` field.

### Alerts file format (`/var/log/statnive-live/alerts.jsonl`)

Phase 8 adds a file-sink alert channel. Event schema (one JSON object
per line):

| Field      | Type    | Notes                                                              |
|------------|---------|--------------------------------------------------------------------|
| `time`     | RFC3339 | UTC; slog's default time field.                                    |
| `alert`    | string  | Event taxonomy (see below).                                        |
| `severity` | string  | `warn` / `critical` / `info`. `info` only on paired `resolved`.    |
| `band`     | int     | `1` / `2` / `3` on enter-band; `0` on recovery.                    |
| `resolved` | bool    | `false` on enter; `true` when the condition clears.                |
| `value`    | number  | Current observation (0.0–1.0 for WAL/disk ratios).                 |
| `host`     | string  | Populated when `alerts.host_tag` is set in YAML. Optional.         |

Event taxonomy (Phase 8 ships 4 types; v1.1 adds Telegram/syslog fan-out):

- `wal_high_fill_ratio` — WAL disk utilization crossing 0.80 / 0.90 / 0.95.
- `clickhouse_down` / `clickhouse_up` — ClickHouse Ping state change. Paired.
- `disk_high_fill_ratio` — `/var/lib/statnive-live` mountpoint crossing 0.85 / 0.90 / 0.95.
- `tls_expiry_warn` / `tls_expiry_critical` — manual-PEM expiry <30 d / <7 d.

Grep recipes:

```bash
# All alerts emitted in the last 24 hours
sudo find /var/log/statnive-live -name 'alerts.jsonl*' -mtime -1 -exec cat {} \;

# Unresolved events (the "still-active" alert set)
sudo jq -c 'select(.resolved == false)' /var/log/statnive-live/alerts.jsonl

# CH-down incidents (time + duration proxy via up-event)
sudo jq -c 'select(.alert | startswith("clickhouse_"))' /var/log/statnive-live/alerts.jsonl

# Disk-full escalation path
sudo jq -c 'select(.alert == "disk_high_fill_ratio" and .band >= 2)' /var/log/statnive-live/alerts.jsonl
```

### Log rotation (advisory)

`alerts.jsonl` is append-only and unrotated by default. Operators who
use logrotate can drop this at `/etc/logrotate.d/statnive-live`:

```
/var/log/statnive-live/*.jsonl {
    weekly
    rotate 12
    compress
    copytruncate
    notifempty
    create 0640 statnive statnive
}
```

`copytruncate` avoids the SIGHUP-based reopen; the runtime also handles
SIGHUP-driven reopen if you prefer `create` semantics — just call
`systemctl kill -s HUP statnive-live` in `postrotate`.

### Phase 8 deferred items (tracked for later)

- **Docker tarball** (`docker save`) — deployment.md §87 → v1.1.
- **Telegram / syslog remote alert sinks** — deployment.md §98,101 → v1.1.
- **Alerts dashboard UI** — Phase 6-polish-5 Notice primitive; the
  `GET /api/ops/alerts` read endpoint ships there, not Phase 8.
- **Ed25519 signing CLI** — Phase 8 ships only the verify side. Signing
  stays operator-side in the age-encrypted vault until v1.1 / Phase 11b.
- **Backup cron automation** — SOP shipped above; operator-owned cron
  line (commented exemplar in the install script hints).

---

## Phase 9 — Dogfood on statnive.com / .de / fr.statnive.com (operator SOPs)

### Provision a fresh Netcup VPS 2000 G12 NUE for dogfood / free-tier SaaS

Target D1 host for Milestone 1 + early SaaS customers. Actual
procurement (2026-04-24, commit `4ff19dd`): **Netcup VPS 2000 G12 iv
NUE hourly-based** — 8 vCore AMD EPYC x86_64 / 16 GB DDR5 ECC / 512 GB
NVMe / 2.5 Gbit unlimited / IPv4 + IPv6 / Nuremberg, Germany. Billing
€25.48/mo + €5 one-time setup, no lock-in (cancel anytime). Per
research-doc-36 §4.1 this is the Hetzner-fallback path promoted to
primary because Hetzner requires photo-ID / passport / proof-of-
address doc-verification at signup; Netcup's Mastercard-only signup
has no such block.

Prior commit `07141c3` documents Hetzner CX43 as the intended D1;
that runbook SOP is preserved as the future-growth tier for when the
doc-verification blocker clears. Hardware-wise Netcup VPS 2000 is
comparable (8 vCore, 16 GB RAM) + ships 3× more NVMe (512 vs 160 GB)
+ unlimited bandwidth (vs 20 TB/mo Hetzner cap) — strictly better for
dogfood traffic; 80 % more expensive than Hetzner's 12-mo-prepaid
CX43 but cancel-anytime flexibility is worth it during Phase A
uncertainty.

```bash
# 1. Netcup Customer Control Panel → VPS → order VPS 2000 G12 iv NUE
#    hourly. Ubuntu 24.04 LTS image. IPv4 + IPv6 both assigned by
#    default — no surcharge. Snapshot-at-provision for rollback.
# 2. Wait for the setup email (~5 min); root password + IPv4/IPv6
#    addresses arrive there. SSH in, set up a non-root user, install
#    your SSH key, disable root password auth:
ssh root@94.16.108.78    # current Netcup IPv4 (live as of 2026-04-25)
adduser --disabled-password --shell /bin/bash ops
mkdir -p /home/ops/.ssh && chmod 700 /home/ops/.ssh
# Paste your ~/.ssh/id_ed25519.pub into /home/ops/.ssh/authorized_keys
chmod 600 /home/ops/.ssh/authorized_keys && chown -R ops:ops /home/ops/.ssh
usermod -aG sudo ops
sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config
systemctl reload ssh

# 3. Install ClickHouse 24 from the official Altinity script:
curl -s https://clickhouse.com/ | sh        # unpacks clickhouse binary
sudo ./clickhouse install                    # creates clickhouse-server + -client
sudo systemctl enable --now clickhouse-server
clickhouse-client --query 'SELECT version()' # expect >= 24.12

# 4. Bind CH to 127.0.0.1 only (Security Rule 2) — default config is
#    bound to 127.0.0.1 on the Altinity package; verify:
grep listen_host /etc/clickhouse-server/config.xml

# 4b. Bind IPv6 on eth0 (load-bearing — without this, AAAA queries
#     answer but TCP6 connections fail). Netcup assigns a /64 subnet
#     (e.g. 2a03:4000:51:f0c::/64) but does NOT bind a routable host
#     address by default — only the link-local fe80::/10. Pick a stable
#     address from the /64 (convention: ::1) and bind it via netplan
#     so it survives reboots:
sudo tee /etc/netplan/60-statnive-ipv6.yaml >/dev/null <<'EOF'
network:
  version: 2
  ethernets:
    eth0:
      addresses:
        - 2a03:4000:51:f0c::1/64
      routes:
        - to: ::/0
          via: fe80::1       # Netcup IPv6 default gateway (link-local)
          on-link: true
EOF
sudo chmod 0600 /etc/netplan/60-statnive-ipv6.yaml   # netplan 0.106+ refuses world-readable
sudo netplan apply
ip -6 addr show eth0 | grep '2a03:4000:51:f0c::1'    # should match
ping -6 -c2 google.com                                # confirm routing works

# 5. Continue with § Air-gap bundle install from step 1.
```

### Bind IPv6 on Netcup VM

Step 4b above is the canonical procedure; this heading exists so cross-references from `PLAN.md § Domains` and `deployment.md` resolve cleanly. **Do not skip step 4b** — without it, AAAA queries for `statnive.live` answer (Cloudflare returns the record) but TCP6 connections to the VPS fail (no routable IPv6 bound on `eth0`). The chosen address is `2a03:4000:51:f0c::1`, picked from the Netcup-assigned `2a03:4000:51:f0c::/64` subnet.

**Why `::1` and not the EUI-64 SLAAC address (`2a03:4000:51:f0c:b8c5:98ff:fe09:1428`)?** EUI-64 is derived from the vNIC MAC and changes if the VM is rebuilt. A static `::1` from the /64 is stable across rebuilds, easier to remember, and decouples the AAAA record from VM lifecycle. Cloudflare's AAAA for `statnive.live` points at `::1`; if you change the binding, update [`deploy/dns/statnive.live.zone`](../deploy/dns/statnive.live.zone) and re-import.

If the VM is already provisioned and you need to add the IPv6 binding standalone (without re-running steps 1–4):

```bash
# Standalone IPv6 binding — only run if step 4b above was skipped.
sudo tee /etc/netplan/60-statnive-ipv6.yaml >/dev/null <<'EOF'
network:
  version: 2
  ethernets:
    eth0:
      addresses:
        - 2a03:4000:51:f0c::1/64
      routes:
        - to: ::/0
          via: fe80::1
          on-link: true
EOF
sudo chmod 0600 /etc/netplan/60-statnive-ipv6.yaml
sudo netplan apply
ip -6 addr show eth0 | grep '2a03:4000:51:f0c::1'
```

### Provision the GHA deploy seam (Phase 8b — one-time per VPS)

Wires the box for `.github/workflows/deploy-saas.yml` so future releases ship via a tag push.

```bash
# 1. Create the deploy user (no password, SSH-key only).
sudo useradd --create-home --shell /bin/bash --comment 'GHA deploy user' deploy
sudo install -d -m 0700 -o deploy -g deploy /home/deploy/.ssh

# 2. Authorise the GHA deploy SSH pubkey. Generate the keypair locally
#    (operator laptop), keep the private key in the repo's GHA secret
#    NETCUP_SSH_KEY, paste the public part here:
#       ssh-keygen -t ed25519 -f netcup-deploy -C 'gha-deploy@statnive'
sudo tee /home/deploy/.ssh/authorized_keys <<'EOF'
ssh-ed25519 AAAA... gha-deploy@statnive
EOF
sudo chmod 0600 /home/deploy/.ssh/authorized_keys
sudo chown deploy:deploy /home/deploy/.ssh/authorized_keys

# 3. Provision the bundle-staging tree.
sudo install -d -m 0750 -o root   -g root            /etc/statnive
sudo install -d -m 0755 -o root   -g root            /opt/statnive-live
sudo install -d -m 0755 -o root   -g root            /opt/statnive-bundles
sudo groupadd --system statnive-deploy 2>/dev/null || true
sudo usermod -aG statnive-deploy deploy
sudo install -d -m 0775 -o root -g statnive-deploy /opt/statnive-bundles/incoming
# Future bundles extracted into /opt/statnive-bundles/<version>/ are
# owned by root; deploy user can only WRITE into incoming/, not modify
# already-deployed trees.

# 4. Drop the release pubkey (matches the GHA secret STATNIVE_RELEASE_PRIVKEY).
#    The pubkey is the standard `ssh-keygen -t ed25519 -f release-key`
#    output; share-safe (public).
sudo tee /etc/statnive/release-key.pub <<'EOF'
ssh-ed25519 AAAA... statnive-release@<host>
EOF
sudo chmod 0644 /etc/statnive/release-key.pub

# 5. Install the on-box deploy primitive (ships in the bundle's
#    deploy/statnive-deploy.sh; copy from the first installed bundle or
#    direct from the repo for the bootstrap deploy).
sudo install -m 0755 /opt/statnive-bundles/<initial-version>/deploy/statnive-deploy.sh \
  /usr/local/bin/statnive-deploy

# 6. NOPASSWD sudoers entry — deploy user can ONLY run statnive-deploy.
sudo tee /etc/sudoers.d/10-statnive-deploy <<'EOF'
# Allow the GHA deploy user to invoke the deploy primitive only.
# Restricts blast radius from "shell access" to "ship a verified bundle".
deploy ALL=(root) NOPASSWD: /usr/local/bin/statnive-deploy *
Defaults!/usr/local/bin/statnive-deploy !requiretty
EOF
sudo chmod 0440 /etc/sudoers.d/10-statnive-deploy
sudo visudo -c   # syntax check

# 7. Verify deploy-user shell access works as expected:
ssh deploy@<host> sudo /usr/local/bin/statnive-deploy versions
# Expected: "no bundles installed" on first run, then a list with `*`
# marking the current after the first GHA run.
```

GitHub Actions repo secrets to add (Settings → Secrets and variables → Actions → New repository secret):

| Secret | Source |
|---|---|
| `STATNIVE_RELEASE_PRIVKEY` | OpenSSH Ed25519 PRIVATE key (the matching pubkey lives at `/etc/statnive/release-key.pub`). Generated once: `ssh-keygen -t ed25519 -f release-key -N ''`. |
| `NETCUP_SSH_HOST`          | FQDN or IP — current Netcup VPS: `94.16.108.78` (or `app.statnive.live` once DNS propagates). |
| `NETCUP_SSH_USER`          | `deploy`. |
| `NETCUP_SSH_KEY`           | OpenSSH PRIVATE key for the deploy user. Generated once: `ssh-keygen -t ed25519 -f netcup-deploy -N ''`. |
| `NETCUP_SSH_KNOWN_HOSTS`   | Output of `ssh-keyscan -t ed25519 <host>` — pin the host key. Re-keyscan and update if the box is reprovisioned. |

Optional repo variable (Settings → Variables → Actions, NOT a secret):

| Variable | Default |
|---|---|
| `STATNIVE_ABOUT_URL` | `https://app.statnive.live/api/about` — overridden when the dashboard host differs from defaults. |

Once 1–7 are done, **the next release is one tag push**:

```bash
git tag v0.10.0 && git push origin v0.10.0
# release.yml:    builds + signs the bundle, attaches to a GitHub Release
# deploy-saas.yml: SCPs the bundle, runs `statnive-deploy deploy v0.10.0`,
#                  asserts /api/about .git_sha matches
```

Failure modes auto-handled by `statnive-deploy`:
- Bundle SHA256 / Ed25519 mismatch → reject before extraction.
- `systemctl restart` succeeds but `/healthz` does not turn `clickhouse=up` within 30 s → auto-revert symlink + binary + unit to the previous version, restart, re-poll. Workflow exits 1 so you get a notification.

To roll back manually after the deploy window: Actions → `rollback-saas` → workflow_dispatch with the previous version tag.

### Phase 9 breakglass — manual cutover (when GHA is unavailable)

Use this only if `release.yml` / `deploy-saas.yml` are blocked (GitHub-side incident, secret rotation in flight, etc.). The 13-step path the GHA pipeline replaces:

```bash
# 0. Build the bundle on a trusted laptop:
make release   # produces build/*.tar.gz + SHA256SUMS + SHA256SUMS.sig

# 1. SCP to the box.
scp build/statnive-live-vX.Y.Z-linux-amd64-airgap.tar.gz \
    build/SHA256SUMS \
    build/SHA256SUMS.sig \
    deploy@<host>:/opt/statnive-bundles/incoming/

# 2. SSH in, run the deploy primitive directly (same script GHA uses).
ssh deploy@<host>
sudo /usr/local/bin/statnive-deploy deploy vX.Y.Z

# 3. Confirm /api/about advanced.
curl -s https://app.statnive.live/api/about | jq -r .git_sha

# 4. Confirm /healthz green.
curl -s https://app.statnive.live/healthz | jq .
```

If the on-box `statnive-deploy` is itself broken (extremely rare), fall back to the lower-level primitives shipped in the bundle: `airgap-verify-bundle.sh` → manual extract to `/opt/statnive-bundles/<version>/` → `ln -sfn` the symlink → `cp bin/statnive-live /usr/local/bin/statnive-live` → `systemctl daemon-reload && systemctl restart statnive-live`. The atomic `mv` and symlink swap are the same operations the script performs; doing them by hand is acceptable for one-off recovery.

### DNS import to Cloudflare — `statnive.live`

Architecture C uses **Cloudflare free tier** for the international `.live` zone (Bunny ruled out 2026-04-25; Cloudflare permitted only because no Iranian resolver queries this zone — see `PLAN.md` § Domains + `iran-no-cloudflare` Semgrep rule scope). All 12 records ship in DNS-only mode (grey cloud); the origin terminates TLS via Let's Encrypt directly per the next section.

```bash
# 1. The canonical zone file lives at deploy/dns/statnive.live.zone in
#    this repo. Inspect before importing — every A/AAAA carries
#    cf_tags=cf-proxied:false to force DNS-only regardless of dashboard
#    checkbox state:
cat deploy/dns/statnive.live.zone

# 2. In the Cloudflare dashboard:
#      DNS > Records > Import and Export > Import DNS records.
#    Upload deploy/dns/statnive.live.zone OR paste its contents.
#    UNCHECK "Proxy imported DNS records" (the cf-proxied:false tags
#    override anyway, but unchecking matches intent).
#    Click Import.

# 3. Cloudflare assigns 2 nameservers (e.g. dana.ns.cloudflare.com +
#    kirk.ns.cloudflare.com). Set those as the only 2 NS records at
#    Namecheap for statnive.live. Propagation usually <5 minutes.

# 4. Verify resolution from a non-Cloudflare resolver:
dig +short statnive.live        A   # → 94.16.108.78
dig +short app.statnive.live    A   # → 94.16.108.78
dig +short demo.statnive.live   A   # → 94.16.108.78
dig +short www.statnive.live    A   # → 94.16.108.78
dig +short statnive.live        AAAA  # → 2a03:4000:51:f0c::1
dig +short statnive.live        CAA   # → 4 lines (LE + Sectigo + issuewild ; + iodef)
dig +short NS statnive.live           # → 2 cloudflare.com nameservers
```

To change a record (e.g. swap the Netcup IP after a rebuild): edit `deploy/dns/statnive.live.zone`, commit, then in Cloudflare dashboard either re-import (overwrites) or hand-edit the affected record. Keeping the repo zone file as the single source of truth means the next operator can answer "what's in Cloudflare?" by reading one file.

### Issue + rotate TLS for `statnive.live` + `app.statnive.live` + `demo.statnive.live`

One Let's Encrypt cert with three SANs covers the dogfood surface.
Certbot runs in `--standalone` mode so we need to pause statnive-live
during issuance; the PEMs then drop into the loader's watched paths
and the next SIGHUP picks them up.

```bash
# Install certbot:
sudo apt update && sudo apt install -y certbot

# Stop statnive-live so certbot --standalone can bind :80:
sudo systemctl stop statnive-live

# Issue one cert for all three SANs:
sudo certbot certonly --standalone \
  -d statnive.live -d app.statnive.live -d demo.statnive.live \
  --agree-tos -m ops@statnive.live --non-interactive

# Copy PEMs to the loader paths (statnive user must be able to read):
sudo install -m 0600 -o statnive -g statnive \
  /etc/letsencrypt/live/statnive.live/fullchain.pem \
  /etc/statnive-live/tls/fullchain.pem
sudo install -m 0600 -o statnive -g statnive \
  /etc/letsencrypt/live/statnive.live/privkey.pem  \
  /etc/statnive-live/tls/privkey.pem

# Restart:
sudo systemctl start statnive-live

# Automatic renewal (LE certs are 90-day; certbot renews at ~30 d):
sudo tee /etc/cron.d/statnive-certbot <<'EOF'
# Monthly on the 1st, 02:00 UTC — renew-and-reload. The post-hook
# re-copies the fresh PEMs and SIGHUPs statnive-live; cert.Loader
# hot-swaps without a restart.
0 2 1 * * root \
  certbot renew --quiet --deploy-hook "cp /etc/letsencrypt/live/statnive.live/fullchain.pem /etc/statnive-live/tls/ && \
                                        cp /etc/letsencrypt/live/statnive.live/privkey.pem  /etc/statnive-live/tls/ && \
                                        chown statnive:statnive /etc/statnive-live/tls/*.pem && \
                                        systemctl kill -s HUP statnive-live"
EOF
```

Cert-expiry watcher (`internal/cert/expiry.go`) emits
`tls.expiry_warning` at <30 d and `tls.expiry_critical` at <7 d to
both `audit.jsonl` and `alerts.jsonl` — confirm renewal via
`grep tls.expiry /var/log/statnive-live/audit.jsonl` after each
certbot pass.

### Seed 3 sites for Milestone 1 (statnive.com / .de / fr.statnive.com)

Post-install, before cutover. Use the Admin UI (Phase 6-polish Sites
tab) — no CLI seed subcommand ships in v1.

1. Log in at `https://app.statnive.live/app/login` with the bootstrap
   admin credentials.
2. Admin → Sites → **Add site**:
   - hostname `statnive.com`, slug `statnive`, tz `Europe/Berlin` → site_id=1
   - hostname `statnive.de`, slug `statnive-de`, tz `Europe/Berlin` → site_id=2
   - hostname `fr.statnive.com`, slug `statnive-fr`, tz `Europe/Paris` → site_id=3
3. (Optional) Add a `demo` viewer user: Admin → Users → Add email
   `demo@statnive.live`, role `viewer`, site_id=1. Publish those
   credentials via `STATNIVE_AUTH_DEMO_BANNER` on the login page
   (systemd env drop-in; SIGHUP the binary).

### Milestone 1 acceptance check (24 h after DNS cutover)

Run against the live host, not localhost:

```bash
# 1. Three origins serve HTTPS + embed the tracker (not PostHog):
for h in statnive.com statnive.de fr.statnive.com; do
  curl -sI "https://$h" | head -1
  curl -s  "https://$h" | grep -q 'statnive.live/tracker.js' && echo "$h: tracker OK"
  curl -s  "https://$h" | grep -qi posthog && echo "$h: POSTHOG LEAK"
done

# 2. /api/about serves the CC-BY-SA verbatim text:
curl -s https://app.statnive.live/api/about | jq -r '.attributions[] | select(.name == "IP2Location LITE DB23") | .text'
# expect: "This site or product includes IP2Location LITE data available from https://lite.ip2location.com."

# 3. All three sites have non-zero pageviews in the dashboard
#    (via the smoke harness, which already probes /api/stats/*):
STATNIVE_E2E_BASEURL=https://app.statnive.live \
  npm --prefix web run e2e -- --reporter=list

# 4. Viewer is 403-blocked from /api/admin/*:
COOKIE=<viewer-sid>
curl -s -o /dev/null -w "%{http_code}\n" \
  -H "Cookie: sid=$COOKIE" \
  https://app.statnive.live/api/admin/users
# expect: 403

# 5. No unresolved alerts on the box:
sudo jq -c 'select(.resolved == false)' /var/log/statnive-live/alerts.jsonl
# expect: empty (no WAL high fill, no CH down, cert > 30 d out)
```

Green on all five → Milestone 1 achieved.
