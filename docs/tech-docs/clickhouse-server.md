---
title: ClickHouse Server Documentation
library_id: /clickhouse/clickhouse-docs
type: context7-reference
created: 2026-04-17
updated: 2026-04-17
context7_mode: code
topic: aggregating-merge-tree-rollups
tags: [context7, clickhouse, aggregating-merge-tree, materialized-view, rollup]
source: Context7 MCP
cache_ttl: 7 days
---

# ClickHouse — AggregatingMergeTree + Materialized Views (confirmed)

## Canonical rollup pattern (matches statnive-live's 6-rollup design)

```sql
CREATE TABLE events_rollup_1h
(
    bucket_start Date,
    country      LowCardinality(String),
    event_type   LowCardinality(String),
    users_uniq   AggregateFunction(uniqExact, UInt64),
    value_sum    AggregateFunction(sum, Float64),
    value_avg    AggregateFunction(avg, Float64),
    events_count AggregateFunction(count)
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(bucket_start)
ORDER BY (bucket_start, country, event_type);

CREATE MATERIALIZED VIEW mv_events_rollup_1h
TO events_rollup_1h
AS SELECT
    toStartOfHour(event_time) AS bucket_start,
    country,
    event_type,
    uniqExactState(user_id)   AS users_uniq,
    sumState(value)           AS value_sum,
    avgState(value)           AS value_avg,
    countState()              AS events_count
FROM events_raw
GROUP BY bucket_start, country, event_type;
```

## Plan verifications

- ✅ `PARTITION BY toYYYYMM(bucket_start)` — correct for monthly drops.
- ✅ `ORDER BY (bucket, dim1, dim2)` — low-cardinality dimensions first.
- ✅ `AggregateFunction(uniqExact, UInt64)` confirmed. statnive-live uses `uniqCombined64` (HLL approximation) which is also native.
- ✅ `LowCardinality(String)` for country/event_type saves 60–80% on dictionary encoding.

## Cascading MV (hourly → monthly)

```sql
CREATE MATERIALIZED VIEW mv_monthly_from_hourly
TO events_rollup_1mo
AS SELECT
    toDate(toStartOfMonth(bucket_start)) AS month,
    country,
    sumMerge(value_sum) AS value_sum
FROM events_rollup_1h
GROUP BY month, country;
```

Use `*Merge()` (not `*MergeState()`) when reading from an already-aggregated source.

## No API deltas vs 2026-04-17 snapshot.
