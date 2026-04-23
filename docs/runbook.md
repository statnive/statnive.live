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
- **Nightly cron:** automated nightly drill via
  [`.github/workflows/backup-drill-nightly.yml`](../.github/workflows/backup-drill-nightly.yml)
  (Phase 2c). Host-side cron wrapper: see `deploy/backup/README.md` for
  the cron template.
- **Before any schema migration:** full + incremental snapshot
  immediately before `make migrate`. Same restore drill afterward
  proves the migration itself didn't corrupt the data set.

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
