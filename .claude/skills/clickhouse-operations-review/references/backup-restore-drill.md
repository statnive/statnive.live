# Backup / restore drill — `clickhouse-backup` + age + zstd

Canonical drill procedure + CI wiring per doc 28 §Gap 3 lines 594 + 799–822.

## Stack

| Component | Version | License | Role |
|---|---|---|---|
| [`Altinity/clickhouse-backup`](https://github.com/Altinity/clickhouse-backup) | v2.5.20+ | Apache-2.0 | Backup orchestration |
| [`filippo.io/age`](https://github.com/FiloSottile/age) | 1.2+ | BSD-3 | Encryption (recipient pubkey on operator workstation) |
| `zstd` | 1.5+ | BSD-3 | Compression level 19 |

## Cadence

| Type | Schedule | Retention |
|---|---|---|
| Full | Sunday 02:15 UTC | 30 days |
| Incremental | Every 6 hours | 30 days |
| Drill | Every release + nightly cron | n/a (validation only) |

## Storage

- **Primary sink:** S3 (or S3-compatible: Backblaze B2, Wasabi, MinIO).
- **Iranian DC:** Second sink to a ParsPack FTP bucket (50GB free on VPS tier). Outside-Iran sink reachable only when NIN connectivity is up.
- **Encryption:** All files piped through `age` with a single recipient pubkey. Private key on offline operator workstation. **Restore requires the private key in hand.**

## Config layout — `deploy/backup/config.yml`

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

## Drill script — `deploy/backup/drill.sh`

```bash
#!/usr/bin/env bash
# Nightly restore drill. Runs inside CI or on a dedicated drill host.
# Exits non-zero on any parity failure.
set -euo pipefail

DRILL_HOST="${DRILL_HOST:-127.0.0.1}"
DRILL_PORT="${DRILL_PORT:-19000}"
PROD_HOST="${PROD_HOST:-127.0.0.1}"
PROD_PORT="${PROD_PORT:-9000}"
NAME="drill-$(date -u +%Y%m%d-%H%M)"

echo "[$(date -u)] Creating backup $NAME"
clickhouse-backup --config deploy/backup/config.yml create "$NAME"
clickhouse-backup --config deploy/backup/config.yml upload "$NAME"

echo "[$(date -u)] Restoring $NAME to drill host"
clickhouse-backup --config deploy/backup/config-drill.yml restore_remote "$NAME"

echo "[$(date -u)] Row-count parity check"
for TABLE in events rollup_hourly rollup_daily; do
  P=$(clickhouse-client --host "$PROD_HOST" --port "$PROD_PORT" \
        -q "SELECT sum(rows) FROM system.parts WHERE table='$TABLE' AND active")
  D=$(clickhouse-client --host "$DRILL_HOST" --port "$DRILL_PORT" \
        -q "SELECT sum(rows) FROM system.parts WHERE table='$TABLE' AND active")
  if [ "$P" != "$D" ]; then
    echo "PARITY FAIL on $TABLE: prod=$P drill=$D"
    exit 1
  fi
  echo "  $TABLE: $P rows (OK)"
done

echo "[$(date -u)] Rollup mergeability check (uniqCombined64Merge)"
clickhouse-client --host "$DRILL_HOST" --port "$DRILL_PORT" \
  -q "SELECT uniqCombined64Merge(uniq_state) FROM rollup_daily FINAL FORMAT Null"

echo "[$(date -u)] Drill $NAME: PASS"
```

## CI job — nightly + labeled PR

```yaml
backup-restore-drill:
  runs-on: ubuntu-latest
  if: github.event_name == 'schedule' || contains(github.event.pull_request.labels.*.name, 'ops-drill')
  services:
    ch-prod:  { image: clickhouse/clickhouse-server:24.8, ports: ['9000:9000'] }
    ch-drill: { image: clickhouse/clickhouse-server:24.8, ports: ['19000:9000'] }
  steps:
    - uses: actions/checkout@v4
    - run: ./scripts/seed.sh --target 127.0.0.1:9000 --rows 1000000
    - run: |
        wget -q https://github.com/Altinity/clickhouse-backup/releases/download/v2.5.20/clickhouse-backup.tar.gz
        tar xzf clickhouse-backup.tar.gz
        sudo mv build/linux/amd64/clickhouse-backup /usr/local/bin/
    - run: bash deploy/backup/drill.sh
```

## Drill assertions

1. **Row-count parity** — `SUM(rows)` in `system.parts` matches between prod + drill for every active table. Catches silent corruption.
2. **Rollup mergeability** — `uniqCombined64Merge(uniq_state) FROM rollup_daily FINAL FORMAT Null` completes. Catches `AggregateFunction` state corruption that row-count wouldn't see.
3. **(Release drills only) Tracker byte-parity** — replay last 100K ingest requests against drill host, assert byte-identical `events` rows.

## Recovery runbook — disk full (Code 243)

Exact error text: `DB::Exception: Cannot reserve N.NN MiB, not enough space. (NOT_ENOUGH_SPACE) Code: 243`.

1. **First**, verify `/health/ready` flips to 503 (DiskMonitor did its job).
2. Free space options in order of preference:
   - `ALTER TABLE events DROP PARTITION '202603'` — drop oldest partition.
   - `ALTER TABLE events DROP DETACHED PART '...'` — drop detached parts from failed mutations.
   - If partition is bigger than `max_partition_size_to_drop` (default 50 GiB), override with `touch /var/lib/clickhouse/flags/force_drop_table`.
3. After free, confirm `DiskMonitor.Degraded()` flips back to false + `/ingest` accepts writes again.
4. Trigger WAL drain: `systemctl kill -HUP statnive.service` to signal the batcher.

## Research anchor

Doc 28 §Gap 3 lines 594–596 + 799–822.