---
name: clickhouse-cluster-migration
description: MUST USE when writing or reviewing any migration file under `clickhouse/migrations/**`. Enforces `{{if .Cluster}}` Go-template structure (doc 24 §Migration 0029) — single-node DDL stays ReplicatedMergeTree-ready; `ON CLUSTER '{cluster}'` correctly interpolated; every migration has a reversible down-path.
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 1
  research: "jaan-to/docs/research/25-ai-claude-skills-filimo-grade-analytics-platform.md §gap-analysis #4; doc 24 §Sec 2 Migration 0029"
---

# clickhouse-cluster-migration

> **Activation gate (Phase 1 lint + P5 cluster upgrade).** This skill's Semgrep rule bodies and CI wiring are scheduled for Phase 1 (`{{if .Cluster}}` templating lint at migration time). Until the corresponding `.github/workflows/cluster-templating-gate.yml` is green on main, treat this skill as **advisory-only** — surface the checklist to the reviewer, do not block merges, and flag any mismatch as `activation-pending` rather than auto-fixing.

Fills the gap the official `clickhouse-best-practices` skill calls out: "deeper knowledge on cluster configurations is coming later." We can't wait — SaaS scale (Phase C) flips the same binary from single-node to ReplicatedMergeTree + Distributed. Every migration ships templated from day 1.

## When this skill fires

- Any new file under `clickhouse/migrations/`.
- Any change to `internal/storage/migrate.go` (the template renderer).
- Any DDL authored inside Go source that does not live under `clickhouse/migrations/`.

## Enforced invariants

1. Every `CREATE TABLE` / `CREATE MATERIALIZED VIEW` is authored with the `{{if .Cluster}}` template placeholder:
   ```sql
   CREATE TABLE {{if .Cluster}}IF NOT EXISTS {{end}}events_raw
   {{if .Cluster}}ON CLUSTER '{cluster}'{{end}} (...)
   ENGINE = {{if .Cluster}}Replicated{{end}}MergeTree{{if .Cluster}}('/clickhouse/tables/{shard}/events_raw', '{replica}'){{end}}
   ```
2. `ENGINE = MergeTree` or `AggregatingMergeTree` becomes `Replicated…` variant under cluster mode — the template handles this.
3. Distributed shard key is `site_id` (tenant scoping holds across shards).
4. Every migration has a **down-path** (`-- +migrate Down` or equivalent) that reverses the schema change. Single-direction migrations are rejected.
5. Migration filenames follow `NNNN_description.sql` (four-digit zero-padded, underscore-separated).
6. Template rendering must produce valid SQL for both single-node (current) and cluster (future) modes — both are tested.
7. `ON CLUSTER` expansion does not break `schema_migrations` tracking — the Go runner applies migrations in order on both topologies.

## Should trigger (reject)

```sql
-- BAD — hardcoded engine, no template, no down-path
CREATE TABLE hourly_visitors (
    site_id UInt32,
    hour DateTime('UTC'),
    ...
) ENGINE = AggregatingMergeTree()
ORDER BY (site_id, hour);
```

## Should NOT trigger (allow)

```sql
-- +migrate Up
CREATE TABLE {{if .Cluster}}IF NOT EXISTS {{end}}hourly_visitors
{{if .Cluster}}ON CLUSTER '{cluster}'{{end}}
(
    site_id UInt32,
    hour DateTime('UTC'),
    path LowCardinality(String) DEFAULT '',
    visitor_hash_state AggregateFunction(uniqCombined64, FixedString(16))
)
ENGINE = {{if .Cluster}}Replicated{{end}}AggregatingMergeTree
{{if .Cluster}}('/clickhouse/tables/{shard}/hourly_visitors', '{replica}'){{end}}
ORDER BY (site_id, hour, path)
PARTITION BY toYYYYMM(hour);

-- +migrate Down
DROP TABLE {{if .Cluster}}IF EXISTS {{end}}hourly_visitors
{{if .Cluster}}ON CLUSTER '{cluster}'{{end}};
```

## Implementation (TODO — Phase 1)

- `templates/validator.go` — Go function that renders each migration with `.Cluster = false` and `.Cluster = true`, then parses both outputs with a lightweight SQL parser to detect drift.
- `templates/filename-format.sh` — regex check on migration filenames.
- `templates/down-path.sh` — grep for `-- +migrate Down` in every file.
- `test/fixtures/` — should-trigger (hardcoded engine, no down-path, bad filename) / should-not-trigger (the existing v1 migrations).

Full spec: [README.md](README.md).
