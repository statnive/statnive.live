# tenancy-choke-point-enforcer — full spec

## Architecture rule

Encodes **CLAUDE.md Architecture Rule 8** (line 32) and **Project Goal 4** (line 11). The `whereTimeAndTenant` helper is the single grep target for the repository's `make lint` tenancy gate.

## Research anchors

- [jaan-to/docs/research/25-ai-claude-skills-SamplePlatform-grade-analytics-platform.md](../../../../jaan-to/docs/research/25-ai-claude-skills-SamplePlatform-grade-analytics-platform.md) §gap-analysis #1 — "highest-leverage skill you can possibly write".
- [jaan-to/docs/research/24-pirsch-pattern-extraction-reference-only.md](../../../../jaan-to/docs/research/24-pirsch-pattern-extraction-reference-only.md) §Sec 4 pattern 6 — central tenancy helper.

## Implementation phase

**Phase 1 — Ingestion Pipeline** (already shipped through PR #2). This skill must exist before any new `internal/storage/` code merges. The `make lint` `tenancy-grep` gate is already wired (see PLAN.md Verification item #24).

## Files

- `semgrep/rule.yml` — TODO: Semgrep pattern targeting `(db|conn|tx).Query(Context|Row)?|.Exec(Context)?` calls in `internal/storage/**/*.go` that do not reference `whereTimeAndTenant`. Also a rollup-DDL rule that fails if `ORDER BY` in `AggregatingMergeTree` engines does not start with `site_id`.
- `test/fixtures/should-trigger/` — TODO: .go + .sql cases that must be rejected.
- `test/fixtures/should-not-trigger/` — TODO: .go + .sql cases that must be allowed (including the existing Phase 3a Store implementations).

## CI integration (TODO)

```makefile
# Already shipped:
tenancy-grep:
    ./scripts/tenancy-grep.sh

# To add:
tenancy-semgrep:
    semgrep --config=.claude/skills/tenancy-choke-point-enforcer/semgrep internal/ clickhouse/

lint: tenancy-grep tenancy-semgrep
    golangci-lint run ./...
```

## Edge cases the final rule must handle

- `windowFunnel()` path touches `events_raw` directly — allow, but require `site_id = ?` first predicate and 1h cache wrapper.
- Admin CRUD mutations (Phase 3c `/api/admin/*`) may omit time range but still require `site_id = ?`.
- Migration runner SQL (`storage/migrate.go`) operates on `schema_migrations` — allow its internal queries without the gate.
- MCP v2 tools (v2 roadmap) must pass the gate.

## Scoping

This skill is scoped to `internal/storage/**/*.go` + `clickhouse/**/*.sql`. Other packages (`internal/ingest/`, `internal/enrich/`) may reference `site_id` without the helper — the helper's job is dashboard reads.