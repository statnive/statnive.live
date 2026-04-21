---
name: clickhouse-rollup-correctness
description: MUST USE when writing or reviewing `AggregatingMergeTree` rollups, MVs, or `uniqCombined64` state-merge code. Enforces -State / -Merge / -MergeState discipline, `FixedString(16)` identity-hash column, three-rollup v1 naming (`hourly_visitors`, `daily_pages`, `daily_sources`), Nullable ban. Flags chained-MV footguns (CH #58062, #19753).
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 1
  research: "jaan-to/docs/research/25-ai-claude-skills-SamplePlatform-grade-analytics-platform.md §gap-analysis #3; doc 20 §MV; CLAUDE.md §Database"
---

# clickhouse-rollup-correctness

> **Activation gate (Phase 1).** This skill's Semgrep rule bodies and CI wiring are scheduled for Phase 1 (first rollup DDL ships). Until the corresponding `.github/workflows/rollup-gate.yml` is green on main, treat this skill as **advisory-only** — surface the checklist to the reviewer, do not block merges, and flag any mismatch as `activation-pending` rather than auto-fixing.

Extends the official `clickhouse-best-practices` skill into HyperLogLog / AggregatingMergeTree territory that the official skill explicitly does **not** cover yet. Guards the v1 rollup contract: three AggregatingMergeTree rollups, all using `AggregateFunction(uniqCombined64, FixedString(16))` for visitor dedup, all keyed by `site_id` first.

## When this skill fires

- Any `CREATE MATERIALIZED VIEW` statement.
- Any `CREATE TABLE ... ENGINE = AggregatingMergeTree(...)` statement.
- Any Go code constructing SQL that includes `uniqCombined64`, `uniqMerge`, `*State`, `*Merge`, `*MergeState`, or `AggregateFunction`.
- Any change to `clickhouse/migrations/*.sql` touching rollup schema.
- Any introduction of a `Nullable(...)` column anywhere in `clickhouse/` or `internal/storage/migrate.go`.

## Enforced invariants

1. **Combinator discipline:** source MV `SELECT` uses `-State` variants (e.g. `uniqCombined64State(visitor_hash)`); the target rollup column is `AggregateFunction(uniqCombined64, FixedString(16))`; dashboard reads use `uniqCombined64Merge(...)` — never mixed.
2. **No chained MVs** unless the footgun docs (CH issues #58062, #19753) have been read and mitigated. Prefer two independent MVs over one MV fed by another MV.
3. **Identity hash type is `FixedString(16)`** — matches BLAKE3-128 output. Not `String`, not `UInt128`.
4. **`ORDER BY` leads with `site_id`** (cross-check with `tenancy-choke-point-enforcer`); second column is time grain (`hour`/`day`).
5. **No `Nullable(...)` columns anywhere.** Per CLAUDE.md Architecture Rule 5, use `DEFAULT ''` or `DEFAULT 0`. CI lint already rejects; this skill makes the rejection explainable.
6. **Time column is `DateTime('UTC')`**, not `DateTime64(3)` — hourly grain makes ms precision a 4 B/row waste (doc 24 §Sec 2 Migration 0012).
7. **v1 rollup names are fixed:** `hourly_visitors`, `daily_pages`, `daily_sources`. The three v1.1 additions (`daily_geo`, `daily_devices`, `daily_users`) ship with their dashboard panels, not before.
8. **No `OPTIMIZE FINAL`** — this is a footgun at 10–20M DAU scale. Let `MergeTree` do its job.
9. **Reject mutable-row engines** (`CollapsingMergeTree`, `VersionedCollapsingMergeTree`) — AggregatingMergeTree sidesteps cancel-row ordering bugs.

## Should trigger (reject)

```sql
-- BAD — Nullable, String instead of FixedString, ORDER BY missing site_id
CREATE TABLE hourly_visitors (
    hour DateTime,
    site_id UInt32,
    visitor_hash_state Nullable(String),
    count AggregateFunction(count, UInt64)
) ENGINE = AggregatingMergeTree()
ORDER BY (hour, site_id);
```

```sql
-- BAD — mixing -Merge in a source MV; should be -State
CREATE MATERIALIZED VIEW hourly_visitors_mv TO hourly_visitors AS
SELECT toStartOfHour(time) AS hour, site_id,
       uniqCombined64Merge(visitor_hash) AS visitor_hash_state
FROM events_raw GROUP BY hour, site_id;
```

## Should NOT trigger (allow)

```sql
CREATE TABLE hourly_visitors (
    site_id UInt32,
    hour DateTime('UTC'),
    path LowCardinality(String) DEFAULT '',
    visitor_hash_state AggregateFunction(uniqCombined64, FixedString(16))
) ENGINE = AggregatingMergeTree()
ORDER BY (site_id, hour, path)
PARTITION BY toYYYYMM(hour);

CREATE MATERIALIZED VIEW hourly_visitors_mv TO hourly_visitors AS
SELECT site_id, toStartOfHour(time) AS hour, path,
       uniqCombined64State(visitor_hash) AS visitor_hash_state
FROM events_raw GROUP BY site_id, hour, path;
```

## Implementation (TODO — Phase 1)

- `ddl-validators/combinator.yml` — regex + AST rules to match `-State` / `-Merge` / `-MergeState` usage.
- `ddl-validators/nullable.sh` — grep `Nullable(` in `clickhouse/migrations/` and `internal/storage/migrate.go`.
- `ddl-validators/rollup-names.yml` — whitelist of v1 + v1.1 rollup names.
- `test/fixtures/should-trigger/` — Nullable, wrong combinator, `OPTIMIZE FINAL`, `DateTime64`, CollapsingMergeTree.
- `test/fixtures/should-not-trigger/` — the current 3 v1 rollup DDLs.

Full spec: [README.md](README.md).
