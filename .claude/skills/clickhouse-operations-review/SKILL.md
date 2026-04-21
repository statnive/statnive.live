---
name: clickhouse-operations-review
description: MUST USE when editing `migrations/*.sql`, `internal/ingest/**`, `internal/query/**`, `internal/wal/**`, `config/ch*.go`, `config/clickhouse/**`, `prometheus/*.rules.yml`, or `deploy/backup/**`. Enforces WAL-first writes (no direct `INSERT INTO events`), `OPTIMIZE FINAL` ban without `PARTITION`, `async_insert=1` only in `/ingest-fallback`, every customer SELECT carries `SETTINGS max_execution_time`, parts ceiling at 100 active/table, disk 85% warn / 90% page, `Nullable(...)` requires `-- NULLABLE-OK:`. Complements `clickhouse-best-practices` (design); this is the ops layer. Full 11-item body.
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 8
  research: "jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md §Gap 3"
  complements:
    - clickhouse-best-practices  # schema/query design (28 rules)
    - clickhouse-upgrade-playbook  # single-node → cluster (advisory)
---

# clickhouse-operations-review

> **Activation gate (Phase 8 Weeks 20–22; required before Week 23 load-rehearsal).** This skill's 8 Semgrep rule bodies + backup-restore drill + parts-ceiling-after-7K-EPS CI job are scheduled for Phase 8 Weeks 20–22, overlapping SamplePlatform rehearsal. Until the corresponding `.github/workflows/ch-ops-gate.yml` is green on main, treat this skill as **advisory-only** — surface the checklist to the reviewer, do not block merges, and flag any mismatch as `activation-pending` rather than auto-fixing.

Encodes the operational discipline that separates a working demo from a production cutover at 7K EPS sustained. ClickHouse/agent-skills ships **one** skill (`clickhouse-best-practices`, 28 rules in 11 categories) — all targeting schema/query/ingestion *design*. Zero operational coverage. This skill closes that gap: WAL-first writes, merge-pressure safety, backup-restore drills, disk-full handling, memory tuning for 8c/32GB, per-tenant query budgets.

## When this skill fires

- `migrations/*.sql` or `migrations/**/*.tmpl` — DDL review.
- `internal/ingest/**/*.go`, `internal/wal/**/*.go`, `internal/query/**/*.go` — Go-side CH interaction.
- `config/ch*.go`, `config/clickhouse/**/*.xml` — server/user profile config.
- `prometheus/*.yml`, `prometheus/**/*.rules.yml`, `monitoring/*.yml` — alerting.
- `deploy/backup/**` — clickhouse-backup config + drill scripts.

## 11-item blocking checklist (doc 28 §Gap 3)

**Writes**
1. No `INSERT INTO <raw_table>` in Go that bypasses `wal.Write(...)`. All writes go through WAL → batcher → sync INSERT. Direct-raw-insert blocked by Semgrep `ch-ops-direct-raw-insert-bypasses-wal`.
2. `async_insert=1` appears **only** in `profiles.ingest_fallback` user profile and `/ingest-fallback` handler. Primary writes are WAL-first + sync INSERT. Async is safety-valve only because `async_insert_deduplicate=0` default **corrupts `AggregateFunction` states on retry after network drop**.
3. No bare `OPTIMIZE TABLE ... FINAL` without `PARTITION '...'` in non-test SQL. Rationale: serializes merges, blocks inserts, non-idempotent on AggregatingMergeTree, OOM on 8c/32GB. Sanctioned alternative: `OPTIMIZE TABLE t PARTITION '202604' FINAL DEDUPLICATE` off-peak, or `min_age_to_force_merge_seconds=3600`.

**Reads**
4. Every customer SELECT appends `SETTINGS max_execution_time=10, read_overflow_mode='break', timeout_overflow_mode='break', max_memory_usage=2e9, max_threads=4`. Combined with `keyed_by=client_key` quota (600 queries/min, 2B rows/min, 300s exec/min), bad-tenant blast radius caps at one-minute-of-one-site's budget.
5. `query_id = <site_id>:<route>:<uid>` on every SELECT — enables per-tenant slicing via `system.query_log`.

**Schema**
6. No `Nullable(...)` in migrations without `-- NULLABLE-OK: <reason>` comment on the same line. Nullable costs 10–200% on aggregations (doc 20 measured 2× on `Nullable(Int8)`).
7. Migrations wrap DDL in `{{if .Cluster}} ON CLUSTER {{.ClusterName}} {{end}}` where cluster-safe (see `clickhouse-upgrade-playbook` for when this is safe vs unsafe).

**Ops posture**
8. Prometheus scrape config has CH's built-in `:9363/metrics` endpoint (no exporter dependency).
9. Alert rules cover: **parts active > 100 per table** (warn, before CH's `parts_to_delay_insert=150`), **disk > 85%** (warn) / **> 90%** (page), `DelayedInserts > 0 for 5m`, `RejectedInserts > 0`, `replication_queue_size > 0 for 10m`, `mutations_not_done > 1h`.
10. No hardcoded `/var/lib/clickhouse` in Go; read from `config.DataDir`. CH path varies per operator deployment.

**Backup**
11. `clickhouse-backup` + `age` + `zstd` cron present: full weekly (Sun 02:15 UTC) + incremental every 6h + 30-day retention + restore-drill on every release + nightly. Drill asserts row-count parity AND rollup-state mergeability via `uniqCombined64Merge`. Age recipient public key path documented in [`references/backup-restore-drill.md`](references/backup-restore-drill.md).

## Merge-pressure quick reference

Defaults (verified against CH docs + MergeTreeData.cpp):

| Setting | Default | Our override (8c/32GB INSERT-heavy) |
|---|---|---|
| `parts_to_delay_insert` | 150 | keep |
| `parts_to_throw_insert` | 300 | keep |
| `max_delay_to_insert` | 1s | keep |
| `background_pool_size` | 16 | **24** |
| `background_merges_mutations_concurrency_ratio` | 2 | **3** |
| `max_server_memory_usage_to_ram_ratio` | 0.9 (docs bug #28002 says 0) | **0.80** |
| `max_memory_usage` per query | 10 GiB | **4 GiB** |
| `max_concurrent_queries` | 100 | **40** (inserts 12) |
| `mark_cache_size` | 5 GiB | **2 GiB** |
| `uncompressed_cache_size` | 8 GiB | **0** (HLL queries re-read marks, not blocks) |

**Alert threshold** at **100 active parts/table** (warn), **150** (critical) — engages runbook proactively, before CH's own `parts_to_delay_insert=150` starts throttling inserts.

**Exact error text we alert on:** `DB::Exception: Too many parts (N). Merges are processing significantly slower than inserts. (TOO_MANY_PARTS)` and `DB::Exception: Cannot reserve N.NN MiB, not enough space. (NOT_ENOUGH_SPACE) Code: 243`.

## Semgrep rules (8)

Full bodies in [`semgrep/rules.yaml`](semgrep/rules.yaml) (verbatim from doc 28 lines 638–709):

| Rule ID | Severity | What it blocks |
|---|---|---|
| `ch-ops-no-optimize-final-in-sql` | ERROR | `OPTIMIZE TABLE ... FINAL` without `PARTITION` |
| `ch-ops-async-insert-outside-fallback` | ERROR | `async_insert=1` outside `/ingest-fallback` / `ingest_fallback` profile |
| `ch-ops-hardcoded-clickhouse-path` | WARNING | `"/var/lib/clickhouse"` string in Go; use `config.DataDir` |
| `ch-ops-select-missing-max-execution-time` | ERROR | Customer SELECT without `SETTINGS max_execution_time` |
| `ch-ops-direct-raw-insert-bypasses-wal` | ERROR | `INSERT INTO (events\|rollup_\w+)` outside WAL / fallback |
| `ch-ops-prom-missing-parts-ceiling-alert` | WARNING | Prometheus rules file missing parts-ceiling alert |
| `ch-ops-prom-missing-disk-threshold-alert` | WARNING | Prometheus rules file missing disk-usage alert |
| `ch-ops-no-nullable-without-justification` | ERROR | `Nullable(...)` without `-- NULLABLE-OK:` comment |

## Go patterns (canonical)

**Query builder appends SETTINGS + `query_id`** ([`references/query-builder.md`](references/query-builder.md)):

```go
func (b *Builder) Build() (string, []any, string) {
    q := b.sb.String() + fmt.Sprintf(
      " SETTINGS max_execution_time = %d, read_overflow_mode='break', "+
      "timeout_overflow_mode='break', max_memory_usage=2000000000, max_threads=4",
      int(b.maxExec.Seconds()))
    return q, b.args, fmt.Sprintf("%s:%s:%s", b.siteID, b.route, shortUUID())
}
```

**WAL-first writer** ([`references/wal-writer.md`](references/wal-writer.md)):

```go
func (w *Writer) Write(ctx context.Context, ev Event) error {
    payload, _ := ev.MarshalBinary()
    idx, err := w.log.WriteIndexed(payload) // fsync inside — ack-after-fsync
    if err != nil {
        if errors.Is(err, wal.ErrOutOfSpace) {
            return w.fallback.Send(ctx, ev) // /ingest-fallback + async_insert profile
        }
        return err
    }
    w.batcher.Enqueue(idx, payload)
    return nil
}
```

**Disk monitor degrades `/healthz`:**

```go
func (d *DiskMonitor) Run(ctx context.Context) {
    for range time.NewTicker(10 * time.Second).C {
        var st syscall.Statfs_t
        _ = syscall.Statfs(d.path, &st)  // d.path = config.DataDir
        used := 1 - float64(st.Bavail*uint64(st.Bsize))/float64(st.Blocks*uint64(st.Bsize))
        switch {
        case used >= 0.90: d.degraded.Store(true); d.sink.Emit("disk_critical", nil)
        case used >= 0.85: d.sink.Emit("disk_warn", nil)
        default:           d.degraded.Store(false)
        }
    }
}
```

## CI gate summary

Three jobs in `.github/workflows/ch-ops-gate.yml` (full YAML in [`references/ci-ch-ops-gate.yml`](references/ci-ch-ops-gate.yml)):

- **semgrep** — 8 rules on every PR.
- **backup-restore-drill** — nightly + labeled PR runs. Seed 1M rows, `clickhouse-backup create` + `upload` + `restore_remote`, assert row-count parity + `uniqCombined64Merge` rollup integrity.
- **parts-ceiling-after-7k-eps** — labeled PR runs. k6 5min 7K EPS, assert max active parts < 100, zero RejectedInserts, DelayedInserts < 50.

## Chaos tests

- **PLAN #11 (kill -9 during ingest):** `kill -9` Go at 3K EPS, restart, assert WAL replay drains, `count(system.parts active)` stable, client events = CH events ±0.
- **PLAN #12 (CH down for 10min):** `systemctl stop clickhouse-server` 600s under 3K EPS. WAL accumulates up to 10GB cap; restart CH; batcher resumes; zero loss; `uniqCombined64Merge` HLL drift <0.5%. WAL-cap → `/ingest` returns `503 Retry-After: 30`.
- **Disk pressure:** `fallocate -l <size> /var/lib/clickhouse/.balloon` to 90%. Expect `DiskMonitor.Degraded()→true`, `/ingest` 503, CH returns Code 243, WAL not corrupted. `rm balloon` → healthy within 30s, drains WAL, zero duplicates.
- **Migration matrix:** fresh (<1s downtime), 1M rows / 2 partitions (<5s), 100M rows / 24 partitions (<30s via hard-link ATTACH, `uniqCombined64Merge` parity).

## Scaffold status

Frontmatter + checklist + Semgrep skeleton + references-stubs shipped in this commit. Semgrep rule bodies + backup-drill script + CI wiring land in **Phase 8 (Weeks 20–22)** per doc 28 §Full-optimization-roadmap, overlapping SamplePlatform rehearsal.
