---
name: tenancy-choke-point-enforcer
description: MUST USE when generating or reviewing SQL in `internal/storage/**`. Enforces Architecture Rule 8 — every dashboard query routes through `queries.go:whereTimeAndTenant()`; `WHERE site_id = ?` is the first predicate; rollup `ORDER BY` / `PARTITION BY` lead with site_id. Rejects raw queries that bypass the helper or misorder the filter. Full body checklist.
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 1
  research: "jaan-to/docs/research/25-ai-claude-skills-SamplePlatform-grade-analytics-platform.md §gap-analysis #1; doc 24 §Sec 4 pattern 6"
---

# tenancy-choke-point-enforcer

> **Activation gate (Phase 1).** This skill's Semgrep rule bodies and CI wiring are scheduled for Phase 1 (`internal/storage/` build-out). Until the corresponding `.github/workflows/tenancy-gate.yml` is green on main, treat this skill as **advisory-only** — surface the checklist to the reviewer, do not block merges, and flag any mismatch as `activation-pending` rather than auto-fixing.

Encodes `statnive-live/CLAUDE.md` **Architecture Rule 8** and **Project Goal 4** as a CI-blocking check. A single missing `WHERE site_id = ?` leaks one tenant's analytics into another tenant's dashboard — this is the bug that would end the company.

## When this skill fires

- Any SQL string assembled inside `internal/storage/` (esp. `queries.go`, `store.go`, `cached_store.go`).
- Any new `CREATE TABLE` / `CREATE MATERIALIZED VIEW` under `clickhouse/migrations/`.
- Any `db.Query` / `db.Exec` / `clickhouse.QueryContext` call added outside `internal/storage/`.
- Any review of dashboard or MCP read-tool code.

## Enforced invariants

1. Every dashboard-read SQL constructed in Go must call `whereTimeAndTenant(*Filter, column)` — the helper owns the first predicate and is the single grep target for the CI gate.
2. `WHERE site_id = ?` is the **first** predicate (before time range, path filters, referrer filters, etc.).
3. Rollup and raw-table `ORDER BY` clauses lead with `site_id` (see `clickhouse-rollup-correctness` for the rollup-specific combinator checks).
4. `PARTITION BY` clauses, where used, also lead with `site_id` (or an expression that preserves tenant index pruning).
5. No dashboard SQL queries `events_raw` except the `windowFunnel()` path, which is cached 1h (Architecture Rule 1).
6. Any MCP read tool (v2) must enforce `WHERE site_id IN (...)` sourced from the token's allowed_site_ids.

## Should trigger (reject)

```go
// BAD — bypasses helper
rows, err := db.QueryContext(ctx,
    `SELECT channel, count() FROM daily_sources WHERE toDate(day) >= ? AND site_id = ?`,
    from, siteID)
```

```sql
-- BAD — site_id not first in ORDER BY
CREATE TABLE hourly_visitors (...) ENGINE = AggregatingMergeTree()
ORDER BY (hour, site_id, path);
```

## Should NOT trigger (allow)

```go
where, args := whereTimeAndTenant(f, "day")
q := "SELECT channel, uniqMerge(visitor_hash_state) FROM daily_sources " + where + " GROUP BY channel"
```

```sql
CREATE TABLE hourly_visitors (...) ENGINE = AggregatingMergeTree()
ORDER BY (site_id, hour, path);
```

## Implementation (TODO — Phase 1)

- `semgrep/rule.yml` — pattern-match `db.Query`/`db.Exec`/`QueryContext` in `internal/storage/**/*.go` that do NOT reference `whereTimeAndTenant`; also match rollup DDL where `ORDER BY` does not start with `site_id`.
- `test/fixtures/` — should-trigger and should-not-trigger .go + .sql cases.
- Wire into `make lint` via `tenancy-grep` target (already present) + semgrep invocation.

Full spec, research anchors, and CI wiring: see [README.md](README.md).
