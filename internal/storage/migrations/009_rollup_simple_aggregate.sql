-- 009_rollup_simple_aggregate.sql — fix AggregatingMergeTree merge semantics
-- for the v1 rollup target tables.
--
-- Problem: hourly_visitors / daily_pages / daily_sources are
-- AggregatingMergeTree ORDER BY (site_id, hour|day|…), and their
-- numeric columns (`pageviews` / `views` / `goals` / `revenue`) were
-- declared as plain `UInt64`. For non-AggregateFunction columns,
-- AggregatingMergeTree behaves like ReplacingMergeTree — on merge, ONE
-- row per ORDER BY tuple survives (last-value wins, non-deterministic).
-- Each MV-driven INSERT creates a new part with the same (site_id, hour)
-- tuple; when CH merges them, the per-event counts collapse to "one of
-- the input parts' value" rather than summing across parts.
--
-- Empirical confirmation (production, 2026-05-11): hour 07:00 had 10
-- pageview events + 3 goal hits in events_raw, but hourly_visitors
-- reported pageviews=1, goals=0, revenue=0 — the merge silently dropped
-- 9 of 10 pageviews and all 3 goals.
--
-- Fix: convert the four numeric columns to
-- `SimpleAggregateFunction(sum, UInt64)`. The underlying storage is
-- identical to UInt64, so ALTER MODIFY COLUMN is metadata-only. The
-- type wrapper tells AggregatingMergeTree to sum across parts during
-- merge — restoring the documented rollup semantics.
--
-- The MVs themselves don't change. Their SELECT still emits plain
-- UInt64 from `count()` / `sum(is_goal)` / `sum(event_value)`; CH
-- silently wraps the value into the target's SimpleAggregateFunction
-- type at insert time.
--
-- Backfill: historical data is already partially lost (the merges have
-- already happened). This migration corrects FUTURE inserts. To recover
-- past sums, operators can replay from events_raw via:
--   INSERT INTO hourly_visitors SELECT … FROM events_raw GROUP BY …
-- (out of scope here — that's an operator-runbook step, not the
-- migration's job).
--
-- LEARN.md candidate: every plain numeric column in an
-- AggregatingMergeTree must be either an AggregateFunction or a
-- SimpleAggregateFunction. Reject `UInt64` / `UInt32` / `Int64` at PR
-- review for any AggregatingMergeTree target.

ALTER TABLE statnive.hourly_visitors{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MODIFY COLUMN pageviews SimpleAggregateFunction(sum, UInt64),
    MODIFY COLUMN goals     SimpleAggregateFunction(sum, UInt64),
    MODIFY COLUMN revenue   SimpleAggregateFunction(sum, UInt64);

ALTER TABLE statnive.daily_pages{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MODIFY COLUMN views   SimpleAggregateFunction(sum, UInt64),
    MODIFY COLUMN goals   SimpleAggregateFunction(sum, UInt64),
    MODIFY COLUMN revenue SimpleAggregateFunction(sum, UInt64);

ALTER TABLE statnive.daily_sources{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MODIFY COLUMN views   SimpleAggregateFunction(sum, UInt64),
    MODIFY COLUMN goals   SimpleAggregateFunction(sum, UInt64),
    MODIFY COLUMN revenue SimpleAggregateFunction(sum, UInt64);
