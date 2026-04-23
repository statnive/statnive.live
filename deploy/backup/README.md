# deploy/backup — clickhouse-backup configs + drill automation

The manual SOP is in [`docs/runbook.md`](../../docs/runbook.md#backup--restore-phase-7b2)
(Phase 7b2). This directory automates the drill steps so the nightly
CI job can prove the backup pipeline is still restorable.

## Files

| File | Purpose |
|---|---|
| `config.yml` | Production `clickhouse-backup` config — env-var driven, S3 + age-encrypted. |
| `config-drill.yml` | Drill-host variant (read-only S3 creds, points at drill CH instance). |
| `config-ci.yml` | CI-only variant (MinIO sidecar, no age). Never deploy to production. |
| `drill.sh` | Automates runbook steps 4–8: restore_remote → row-count parity → rollup mergeability. |
| `docker-wrapper.sh` | Wraps `altinity/clickhouse-backup` in a docker invocation so `drill.sh` can treat it as a local binary. |

## Production drill (nightly cron)

Install `clickhouse-backup` v2.5.20+ on the drill host (not production),
drop `config-drill.yml` + the `age` private key at
`/etc/statnive-live/backup-age.key` (mode 0600), then schedule:

```cron
# /etc/cron.d/statnive-live-backup-drill
30 2 * * * statnive  CLICKHOUSE_PASSWORD=… S3_READ_ACCESS_KEY=… S3_READ_SECRET_KEY=… S3_REGION=… DATA_DIR=/var/lib/clickhouse /usr/local/bin/drill.sh --config=/etc/statnive-live/config-drill.yml --mode=full >> /var/log/statnive-live/backup-drill.log 2>&1
```

The script exits non-zero on any table parity failure or rollup-state
corruption. Wire the non-zero exit into the operator's alerting sink
(file alerts on v1; Telegram/syslog on v1.1).

## CI drill (nightly GitHub Actions)

`.github/workflows/backup-drill-nightly.yml` runs at 04:00 UTC:

1. Boot ClickHouse via `deploy/docker-compose.dev.yml` + a MinIO sidecar.
2. Migrate + seed ~10 K synthetic events (`test/seed/backup-drill.sh`).
3. Capture pre-backup row counts into `$EXPECT_ROWS`.
4. `clickhouse-backup create` + `upload` → MinIO bucket `statnive-backup`.
5. Tear down + recreate ClickHouse to simulate a cold host.
6. Re-migrate (so the schema exists) and run `deploy/backup/drill.sh`
   — the script restores from MinIO and asserts the restored table
   row counts match `$EXPECT_ROWS`.

The CI job is **nightly only**, mirroring `wal-killtest-nightly.yml`.
A full round-trip takes ~5 minutes; running on every PR is not worth
the catch-rate.

## Local smoke

`make backup-drill-local` runs the same flow against a locally-booted
ClickHouse + MinIO. Pre-reqs: `docker compose -f deploy/docker-compose.dev.yml up -d clickhouse`
and a MinIO container running on `127.0.0.1:9001`.

## Why three configs

- `config.yml` — production, age-encrypted, S3 remote.
- `config-drill.yml` — the drill host reads the same bucket but with
  read-only credentials. The separation is deliberate: the drill runs
  on a disposable host and must not be able to mutate production
  artifacts.
- `config-ci.yml` — GitHub runner boots MinIO as a sidecar; no age
  encryption because the CI job isn't a trust boundary and the data
  is ~10 K synthetic rows.
