# clickhouse-operations-review

Full spec for ClickHouse operational discipline. Research anchors: [`jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md`](../../../../jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md) §Gap 3 (lines 580–851).

## Why this skill exists

`ClickHouse/agent-skills` ships **one** skill (`clickhouse-best-practices`) with 28 rules across 11 categories: Primary Key (4), Data Types (5), JOINs (5), Insert Batching (1), Mutation Avoidance (2), Partitioning (4), Skipping Indices (1), Materialized Views (2), Async Inserts (2), OPTIMIZE Avoidance (1), JSON (1). All design-time. Zero operational coverage. `samber/cc-skills-golang`'s `golang-database` is engine-agnostic. The operational gap is real.

This skill fills it with the patterns that keep 8c/32GB stable at 7K EPS sustained and 18K EPS spike.

## Why `OPTIMIZE FINAL` is forbidden (doc 28 line 590)

1. **Serializes merges** — blocks inserts while running.
2. **Non-idempotent on AggregatingMergeTree** — new inserts immediately create new parts, repeated calls churn without completing.
3. **Memory-intensive** — fastest way to OOM 8c/32GB.
4. **Write-amplifies `AggregateFunction(uniqCombined64, ...)` states with no analytics benefit** — reads already merge states.

**Sanctioned alternatives:**
- `OPTIMIZE TABLE t PARTITION 'YYYYMM' FINAL DEDUPLICATE` off-peak, OR
- `min_age_to_force_merge_seconds=3600` (background merges handle it).

## The `async_insert=1` gotcha (doc 28 line 592)

Defaults: `async_insert=0`, `wait_for_async_insert=1`, `async_insert_busy_timeout_ms=200` (OSS) / `1000` (Cloud), `async_insert_use_adaptive_busy_timeout=1` (24.2+) which **overrides manual timeout**, `async_insert_busy_timeout_min_ms=50`, `async_insert_max_data_size=10 MiB`, `async_insert_max_query_number=450`, **`async_insert_deduplicate=0`**.

The dedup default being **off** is the critical gotcha: retries after network drop produce duplicates in `AggregateFunction` states, **corrupting HLL cardinality**. Safety-valve-only rationale confirmed. Semgrep rule `ch-ops-async-insert-outside-fallback` ensures this is restricted to the `/ingest-fallback` path + dedicated `ingest_fallback` user profile.

## Single-node → cluster upgrade constraints (doc 28 line 598)

`ALTER TABLE MODIFY ENGINE` does **not** support MergeTree → ReplicatedMergeTree (CH issues #70118, #14576). Three supported paths:

1. **Hard-link `ATTACH PARTITION`** (preferred, near-zero disk cost, immutable inode hard links).
2. **ATTACH TABLE AS REPLICATED** (CH ≥ 23.8) via `convert_to_replicated` flag file + restart.
3. INSERT INTO SELECT (only for small tables, loses merge history).

Keeper replaces ZooKeeper at P5 — 3-node RAFT/NuRaft ensemble, ports 9181 client / 9234 raft, dedicated hosts recommended over embedded.

**`{{if .Cluster}}` is DDL templating only** — data migration remains manual. See paired `clickhouse-upgrade-playbook` skill for the runbook.

## Memory tuning for 8c/32GB INSERT-heavy

| Parameter | Default | Our value | Reason |
|---|---|---|---|
| `max_server_memory_usage_to_ram_ratio` | 0.9 | **0.80** | Leave headroom for system + ClickHouse process overhead |
| `max_memory_usage` (per query) | 10 GiB | **4 GiB** | 8-way concurrency × 4 GiB = 32 GiB ceiling |
| `max_concurrent_queries` | 100 | **40** | Of which ~12 are inserts |
| `mark_cache_size` | 5 GiB | **2 GiB** | Mark cache hits are the bottleneck, not size |
| `uncompressed_cache_size` | 8 GiB | **0** | HLL queries re-read marks, not blocks — cache wasted |
| `background_pool_size` | 16 | **24** | INSERT-heavy profile benefits from more merge threads |
| `background_merges_mutations_concurrency_ratio` | 2 | **3** | Bias toward merges to keep parts low |
| Per-query timeout | — | **15s** | Customer SELECT cap; ingest writes use context timeouts |

Dedicated `ingest_fallback` user profile turns on `async_insert=1` with `busy_timeout=500ms` — the safety valve.

## Per-tenant query budget (doc 28 line 602)

Every customer SELECT appends:

```sql
SETTINGS
  max_execution_time=10,
  read_overflow_mode='break',
  timeout_overflow_mode='break',
  max_memory_usage=2e9,
  max_threads=4
```

Quota `keyed_by=client_key`: **600 queries/min, 2B rows/min, 300s exec/min**.

`query_id = <site_id>:<route>:<uid>` enables per-tenant slicing via `system.query_log`. Combined, bad-tenant blast radius is capped to one minute of one site's budget.

## Backup / restore discipline

**Cadence:**
- Full: weekly, Sun 02:15 UTC.
- Incremental: every 6h.
- Retention: 30 days.
- Drill: every release + nightly cron.

**Stack** (all license-compatible):
- [`Altinity/clickhouse-backup`](https://github.com/Altinity/clickhouse-backup) — Apache-2.0.
- [`filippo.io/age`](https://github.com/FiloSottile/age) — BSD-3, recipient pubkey on operator workstation.
- `zstd` — BSD-3, level 19.

**Drill asserts:**
- Row-count parity: `SUM(rows)` from `system.parts` for each active table matches prod vs drill-restore.
- Rollup-state mergeability: `uniqCombined64Merge(uniq_state) FROM rollup_daily FINAL FORMAT Null` completes without error — catches `AggregateFunction` state corruption.

Full drill script in [`references/backup-restore-drill.md`](references/backup-restore-drill.md).

## Prometheus alert rules (9 minimum)

Copy [`Altinity/clickhouse-operator/deploy/prometheus/prometheus-alert-rules-clickhouse.yaml`](https://github.com/Altinity/clickhouse-operator) as the starting template. Must-have:

1. **ClickHousePartsActive** — warn > 100/table, critical > 150/table (before CH's own delay threshold)
2. **ClickHouseDiskUsage** — warn > 85%, page > 90%
3. **ClickHouseDelayedInserts** — any > 0 for 5m
4. **ClickHouseRejectedInserts** — any > 0 (SLO violation)
5. **ClickHouseReplicationQueueSize** — > 0 for 10m (cluster mode only)
6. **ClickHouseMutationsNotDone** — stuck > 1h
7. **ClickHouseMemoryTracking** — `MemoryTracking > 0.9 * max_server_memory_usage`
8. **ClickHouseMergePoolBusy** — `BackgroundPoolTask` queue > 100
9. **ClickHouseQueryDurationP99** — per-endpoint budget exceeded

**Dashboards** (reference only, not bundled):
- Grafana **14192** (official ClickHouse mixin)
- Grafana **12163** (Altinity operator)
- Grafana **23415** (Cloud prom-exporter v2)
- Grafana **2515** (query_log analysis)

## Full Semgrep rule bodies

See [`semgrep/rules.yaml`](semgrep/rules.yaml) for the 8 rules verbatim from doc 28 lines 638–709.

## CI gate

Three jobs in [`references/ci-ch-ops-gate.yml`](references/ci-ch-ops-gate.yml):

1. **semgrep** — runs 8 rules on every PR.
2. **backup-restore-drill** — nightly cron + labeled PR (`ops-drill`). Uses `clickhouse-backup` to create + upload + restore, asserts row-count parity + rollup mergeability.
3. **parts-ceiling-after-7k-eps** — labeled PR (`load-test`). k6 5min 7K EPS on `/ingest`, asserts max active parts < 100, zero RejectedInserts, DelayedInserts < 50, p99 `http_req_duration{kind:ingest}` < 500 ms.

## Chaos test matrix

| Test | Method | Pass criterion |
|---|---|---|
| kill-9 during 3K EPS | WAL replay drains on restart | Client event count = CH event count ±0 |
| CH down 10min under 3K EPS | WAL accumulates up to 10GB cap; CH restart drains | Zero loss, HLL drift <0.5% |
| Disk 90% full | `fallocate` balloon file | `/ingest` 503, CH 243 error, WAL clean |
| Migration — fresh | `apply migrations + first events` | Downtime <1s |
| Migration — 1M rows / 2 partitions | hard-link ATTACH | Downtime <5s, 100% parity |
| Migration — 100M rows / 24 partitions | hard-link ATTACH | Downtime <30s, `uniqCombined64Merge` parity |

## Opinionated defaults

1. **Alert at 100 active parts/table (warn), 150 (crit) — well before CH's `parts_to_delay_insert=150`.** Proactive runbook engagement, not reactive.
2. **`async_insert=1` only on `/ingest-fallback` + `ingest_fallback` profile.** WAL-first is primary. Semgrep blocks elsewhere.
3. **Backup cadence: full weekly + inc 6h + 30d retention + drill every release + nightly.** `clickhouse-backup` + age + zstd-19. Drill asserts row-count parity AND rollup-state mergeability.
4. **Keeper not ZooKeeper at P5. 3-node RAFT on dedicated hosts.** `{{if .Cluster}}` emits Replicated DDL; data migration manual via hard-link ATTACH.
5. **Every customer SELECT carries `SETTINGS max_execution_time=10, read_overflow_mode='break', max_memory_usage=2e9, max_threads=4` and `query_id=<site_id>:<route>:<uid>`.** Quota `keyed_by=client_key` 600qpm / 2B rows/min / 300s exec/min.

## Research anchors

- Doc 28 §Gap 3 (lines 580–851) — full spec, merge defaults, upgrade paths, Semgrep bodies.
- `ClickHouse/agent-skills` `clickhouse-best-practices` — schema/query design complement.
- CLAUDE.md § Architecture Rules 1–8 — product contract this skill enforces operationally.
