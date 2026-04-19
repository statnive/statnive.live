# clickhouse-cluster-migration — full spec

## Architecture rule

Encodes the **CLAUDE.md Database** line "Migrations use `{{if .Cluster}}` Go templates from day 1 (doc 24 §Migration 0029) so single-node → Distributed is a config flip, not a rewrite." Cross-references **CLAUDE.md Architecture Rule 8** (tenancy) and the **PLAN.md v2 Roadmap** SaaS scale-out.

## Research anchors

- [jaan-to/docs/research/25-ai-claude-skills-filimo-grade-analytics-platform.md](../../../../jaan-to/docs/research/25-ai-claude-skills-filimo-grade-analytics-platform.md) §gap-analysis #4.
- [jaan-to/docs/research/24-pirsch-pattern-extraction-reference-only.md](../../../../jaan-to/docs/research/24-pirsch-pattern-extraction-reference-only.md) §Sec 2 Migration 0029 (authoritative source).

## Implementation phase

**Phase 1 — Ingestion Pipeline.** Skill is needed before the first migration file, and must stay alive through Phase C (International SaaS) where the template finally renders in cluster mode on production data.

## Files

- `templates/validator.go` — TODO: renders each migration with `.Cluster=false` and `.Cluster=true`, asserts both outputs parse.
- `templates/filename-format.sh` — TODO: asserts `NNNN_description.sql` pattern.
- `templates/down-path.sh` — TODO: every migration has `-- +migrate Down`.
- `test/fixtures/` — TODO: single-node valid, cluster valid, broken templates.

## Pairs with

- `clickhouse-rollup-correctness` — validates the DDL inside the template (combinators, `Nullable`, engine blacklist).
- `tenancy-choke-point-enforcer` — validates `ORDER BY` starts with `site_id`.
- `clickhouse-best-practices` — official skill for general CH DDL guidance.

## CI integration (TODO)

```makefile
migrations-template-test:
    go test ./internal/storage/... -run TestMigrations_RenderBothModes

migrations-filename-check:
    ./.claude/skills/clickhouse-cluster-migration/templates/filename-format.sh

migrations-down-path-check:
    ./.claude/skills/clickhouse-cluster-migration/templates/down-path.sh

migrations-gate: migrations-template-test migrations-filename-check migrations-down-path-check
```

## Distributed-mode expansion

When `.Cluster = true`, the template produces:

- `ReplicatedMergeTree` / `ReplicatedAggregatingMergeTree` variants with `/clickhouse/tables/{shard}/<name>` + `{replica}` args.
- `ON CLUSTER '{cluster}'` on every DDL.
- A companion `Distributed` table per rollup: `CREATE TABLE <name>_all ... ENGINE = Distributed('{cluster}', default, <name>, sipHash64(site_id))` — the `sipHash64(site_id)` shard key preserves tenancy isolation across shards (every row for a given site lives on one shard, cross-tenant queries hit all shards in parallel).
- Migrations authored against the `_all` Distributed view for writes; reads go through the same view.

Authoring example in PLAN.md §Air-Gapped (the template renderer is offline, no runtime network).

## Scope

- All `.sql` under `clickhouse/migrations/`.
- `internal/storage/migrate.go` template renderer.
- Any Go code authoring DDL.

Does **not** apply to:
- Dashboard `SELECT` queries (owned by `tenancy-choke-point-enforcer`).
- Operator CLI `statnive migrate` subcommand (applies migrations, doesn't author them).
