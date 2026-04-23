-- 005_goals_schema.sql — goal definitions for ingest-side is_goal marking.
-- Same ReplacingMergeTree pattern as migration 004 (users, sessions).
-- Rule 8 — ORDER BY leads with site_id. Rule 5 — no Nullable; typed defaults.
-- Privacy Rule 3 — pattern is a cleartext event-name string, never visitor data.
--
-- v1 scope: match_type = 'event_name_equals' only, per doc 17 row 17 +
-- doc 18 row 17 ("any event name = a goal"). v1.1 will extend Enum8 with
-- 'path_equals' + 'path_prefix' (Enum8 is extensible without migration).
-- v2 adds 'path_regex' alongside the filter/drill-down work.
--
-- No inline comments on column rows — the migration runner strips full-
-- line "--" comments but the clickhouse-go prepared-statement path
-- miscounts parens in "-- ...(..)" tails (lesson from migration 004).

CREATE TABLE IF NOT EXISTS statnive.goals{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    goal_id       UUID,
    site_id       UInt32,
    name          String,
    match_type    Enum8('event_name_equals' = 1),
    pattern       String,
    value_rials   UInt64          DEFAULT 0,
    enabled       UInt8           DEFAULT 1,
    created_at    DateTime('UTC') DEFAULT now(),
    updated_at    DateTime('UTC') DEFAULT now()
)
ENGINE = {{if .Cluster}}ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/goals', '{replica}', updated_at){{else}}ReplacingMergeTree(updated_at){{end}}
ORDER BY (site_id, goal_id);
