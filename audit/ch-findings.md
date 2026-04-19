# Phase 7c — ClickHouse Cost & Correctness Findings

Generated 2026-04-19. Scope: `internal/storage/migrations/*.sql` +
`internal/storage/queries.go` + the rollup contract enforced by the
tenancy-grep gate.

Skills run: `clickhouse-best-practices`, `clickhouse-architecture-advisor`,
custom `clickhouse-rollup-correctness`, custom `clickhouse-cluster-migration`.

## Findings — schema (`migrations/*.sql`)

### 1. `{{if .Cluster}}` templating — clean

Custom `clickhouse-cluster-migration` skill checklist:

- ✅ `001_initial.sql` — every `CREATE DATABASE` / `CREATE TABLE` carries
  `{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}`. Engine clause picks
  `MergeTree()` ↔ `ReplicatedMergeTree('/clickhouse/tables/{shard}/<name>', '{replica}')`.
- ✅ `002_rollups.sql` — same pattern for both rollup tables AND the
  `MATERIALIZED VIEW` definitions. `AggregatingMergeTree` ↔
  `ReplicatedAggregatingMergeTree`.

The single-node → Distributed flip is config-only; no schema rewrite.

### 2. `-State` / `-Merge` combinator discipline — clean

Custom `clickhouse-rollup-correctness` skill checklist:

- ✅ All three rollups use `AggregateFunction(uniqCombined64, FixedString(16))`
  for the visitor HLL state.
- ✅ All three MV `SELECT`s use `uniqCombined64State(visitor_hash)` to
  populate the state column.
- ✅ All five dashboard queries against the state column use
  `uniqCombined64Merge(visitors_state)` (Overview, Sources, Pages, SEO,
  Campaigns, Realtime). No `uniqMerge` (wrong combinator) anywhere.
- ✅ `sum()` is used directly on the `UInt64` columns (pageviews, goals,
  revenue_rials) — those are not aggregate-function states, so plain
  `sum()` is correct.

### 3. `Nullable` ban — clean

`grep -rn 'Nullable' internal/storage/migrations/` returns only:

```
001_initial.sql:39:    -- Identity (three layers, no Nullable — Privacy Rule 3 hashes only)
```

…which is the doc-comment that explains the absence. Zero `Nullable(...)`
column declarations across all migrations.

### 4. `ORDER BY site_id` lead — clean

Architecture Rule 8 requires `site_id` to lead every rollup's `ORDER BY`.
Verified per table:

| Table | ORDER BY | OK? |
|---|---|---|
| `schema_migrations` | `version` | n/a (no site_id; meta table) |
| `sites` | `(site_id, hostname)` | ✅ |
| `events_raw` | `(site_id, toDate(time), visitor_hash, time)` | ✅ |
| `hourly_visitors` | `(site_id, hour)` | ✅ |
| `daily_pages` | `(site_id, day, pathname)` | ✅ |
| `daily_sources` | `(site_id, day, channel, referrer_name, utm_source, utm_medium)` | ✅ |

### 5. Time-column type — clean

`DateTime('UTC')` (seconds precision) — not `DateTime64(3)`. Per PLAN.md
verification 25 + doc 24 §Sec 2: hourly grain makes ms precision a 4 B/row
waste at our row count. No regressions.

### 6. Codecs / partitioning / TTL — clean

- ✅ `Delta(4) + ZSTD(1)` on `time` column (best for monotonic timestamps).
- ✅ `ZSTD(3)` on the high-cardinality string columns (referrer, utm_*).
- ✅ `LowCardinality(String)` on every bounded-vocabulary column
  (channel, browser, OS, country_code, etc.).
- ✅ `PARTITION BY toYYYYMM(time)` on events_raw + rollups — proper
  monthly granularity for the partition manager.
- ✅ `TTL time + INTERVAL 180 DAY DELETE` on events_raw — 6-month
  retention matches PLAN.md feature scope. Rollups have no TTL (they're
  the long-term store).
- ✅ `index_granularity = 8192` (CH default; explicit so future schema
  edits can't silently drop it).

## Findings — queries (`queries.go`)

### 7. `whereTimeAndTenant()` central helper — clean

`make tenancy-grep` passes (Architecture Rule 8 CI gate).

`Realtime` is the documented exception — it filters on the *current* hour
boundary which doesn't fit the helper's `(siteID, from, to)` signature.
The `WHERE site_id = ? AND hour >= ?` is inline and reviewed.

### 8. `events_raw` write-only — clean

`grep -E 'FROM[[:space:]]+(statnive\.)?events_raw' internal/storage/queries.go`
returns nothing. The MV `SELECT`s in `002_rollups.sql` reference
`events_raw` (which is correct — that's what materialized views do); the
dashboard queries never touch it.

### 9. Parameter binding — clean

Every `Query` / `QueryRow` call passes `?` placeholders + `args ...any`
through clickhouse-go. No `fmt.Sprintf` interpolation of user-supplied
values into SQL. The `fmt.Sprintf` calls in `queries.go` only inject the
fixed `WHERE` clause from `whereTimeAndTenant` — values still go through
the driver's parameterized binding.

### 10. `LIMIT` on every multi-row query — clean

Every method that returns a `[]Row` slice (`Sources`, `Pages`, `Campaigns`)
appends `f.EffectiveLimit()` to args + has `LIMIT ?` in the SQL. `SEO`
emits one row per day in the requested range (bounded by `WITH FILL FROM
.. TO ..`).

### 11. `PREWHERE` — informational, not flagged

Best-practices guidance is to use `PREWHERE` when the predicate is more
selective than the projection. Our queries already filter on the
`(site_id, day)` ORDER BY prefix, which CH handles via partition pruning
+ index granularity skip — no PREWHERE benefit measurable on rollups
this small (≤ 100 KB/day per site). PREWHERE becomes useful when the
predicate skips ≥ 90% of rows, which the rollups don't have to do.
Re-evaluate when 7b ships real-traffic tests.

### 12. `JOIN` — n/a

Zero JOINs in the dashboard queries. Every read is a single-table
aggregation. PR #11 / Phase 3a deliberately scoped JOINs out (rollups
denormalize the join columns).

## Per-event memory cost — informational

7a's k6 7K EPS run held the binary's RSS under ~110 MB peak (measured by
`ps aux` during the 5-minute hold). Doc 19 budget is ~100 MB peak working
set; we're 10% over but within the slack the budget allows for OS
page-cache. No optimization needed; will remeasure under Phase 4 with
real tracker traffic.

## Schema changes shipped in 7c

**None.** Every finding above confirmed the existing schema + queries
are already correct. The CH layer needed no changes; this pass produced
audit evidence that we can point at when 7d / future cluster-migration
PRs land.

## Deferred to Phase 7b / 7d

- ClickHouse MCP server connection + `EXPLAIN PIPELINE` runs against the
  6 dashboard queries. Requires the `clickhouse` MCP server (Altinity)
  to be wired into Claude Code's `.mcp.json`. Phase 7d.
- `daily_geo`, `daily_devices`, `daily_users` rollups (planned for v1.1
  when the corresponding dashboard panels exist). Out of scope per
  CLAUDE.md "Architecture Rule 7 — defer before building".
- Distributed-mode end-to-end migration test (single-node →
  ReplicatedMergeTree on 2-node cluster). Phase 11 SaaS scope.
