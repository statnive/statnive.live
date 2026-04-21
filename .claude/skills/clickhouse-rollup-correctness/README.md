# clickhouse-rollup-correctness — full spec

## Architecture rule

Encodes **CLAUDE.md Architecture Rule 2** (line 26) and the Database section at lines 17 — "3 AggregatingMergeTree rollups in v1 … using `AggregateFunction(uniqCombined64, FixedString(16))` (HyperLogLog, 0.5% error). Rollup `ORDER BY` leads with `site_id`."

## Research anchors

- [jaan-to/docs/research/25-ai-claude-skills-SamplePlatform-grade-analytics-platform.md](../../../../jaan-to/docs/research/25-ai-claude-skills-SamplePlatform-grade-analytics-platform.md) §gap-analysis #3 — extends `clickhouse-best-practices` into combinator-correctness territory.
- [jaan-to/docs/research/20-dev-go-clickhouse-analytics-implementation-blueprint.md](../../../../jaan-to/docs/research/20-dev-go-clickhouse-analytics-implementation-blueprint.md) §MV.
- [jaan-to/docs/research/24-pirsch-pattern-extraction-reference-only.md](../../../../jaan-to/docs/research/24-pirsch-pattern-extraction-reference-only.md) §Sec 2 Migration 0012 (`DateTime` vs `DateTime64(3)`).

## Implementation phase

**Phase 1 — Ingestion Pipeline.** The three v1 rollups (`hourly_visitors`, `daily_pages`, `daily_sources`) are already authored and shipped through PR #2. This skill is the regression guard for any change to them and the gate for the three v1.1 additions (`daily_geo`, `daily_devices`, `daily_users`).

## Files

- `ddl-validators/combinator.yml` — TODO: rules for `-State` / `-Merge` / `-MergeState` pairing.
- `ddl-validators/nullable.sh` — TODO: grep guard against `Nullable(` in rollup DDL.
- `ddl-validators/rollup-names.yml` — TODO: whitelist of approved rollup names.
- `ddl-validators/engine-blacklist.yml` — TODO: reject `CollapsingMergeTree`, `VersionedCollapsingMergeTree`.
- `test/fixtures/` — TODO: should-trigger / should-not-trigger fixtures.

## Pairs with

- `clickhouse-best-practices` (already installed) — official 28-rule skill, handles the non-rollup ClickHouse guidance (partitioning, LowCardinality, JOIN optimization, insert batching).
- `tenancy-choke-point-enforcer` — handles the `ORDER BY site_id` check on the tenancy dimension; this skill handles the remaining columns.
- `clickhouse-cluster-migration` — handles the `{{if .Cluster}}` template structure; this skill validates the rollup DDL inside the template.

## CI integration (TODO)

```makefile
rollup-lint:
    ./.claude/skills/clickhouse-rollup-correctness/ddl-validators/nullable.sh
    semgrep --config=.claude/skills/clickhouse-rollup-correctness/ddl-validators clickhouse/

lint: tenancy-grep rollup-lint
    golangci-lint run ./...
```

## HyperLogLog-specific checks

The `uniqCombined64` function has three forms — the skill must validate the correct one is used at each layer:

| Layer | Correct form | Common mistake |
|---|---|---|
| Source MV `SELECT` | `uniqCombined64State(visitor_hash)` | `uniqCombined64(...)` — produces plain `UInt64`, not mergeable state |
| Target rollup column type | `AggregateFunction(uniqCombined64, FixedString(16))` | `UInt64` or `AggregateFunction(uniq, ...)` |
| Dashboard `SELECT` | `uniqCombined64Merge(visitor_hash_state)` | `uniqCombined64(visitor_hash_state)` — returns wrong type |
| Chained MV (avoid) | `uniqCombined64MergeState(state)` | double-`State`; fix with direct two-MV design |

## Scope

Applies to all `.sql` under `clickhouse/migrations/` plus any Go code in `internal/storage/` constructing DDL strings. Dashboard query reads are covered by the `tenancy-choke-point-enforcer` + `clickhouse-best-practices` pair.
