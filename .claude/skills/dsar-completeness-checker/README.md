# dsar-completeness-checker — full spec

## Architecture rule

Encodes **GDPR Article 17 (right to erasure)** end-to-end across every statnive-live sink that can hold a visitor_hash. Paired with [`gdpr-code-review`](../gdpr-code-review/README.md) — Phase 11 (SaaS) hard gate.

## Research anchors

- [jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md](../../../../jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md) §Gap 3 (second half) — primary source.
- [ClickHouse handling updates and deletes](https://clickhouse.com/blog/handling-updates-and-deletes-in-clickhouse) — "with no guarantees for removal, users with compliance requirements may wish to use mutations".
- [PostHog weekly deletion pattern](https://posthog.com/blog/gdpr-erasure-at-scale) — batched weekly windows.

## Implementation phase

**Phase 11 — International SaaS self-serve.** Hard gate on first public signup. The integration test (`test/integration/dsar_test.go`) runs on every CI build once Phase 11 opens.

## The load-bearing automation (dynamic table enumeration)

The entire point of this skill is that **the test does not hardcode a table list.** If a future migration adds a new table and the dev forgets to add it to `erase.go`, the DSAR test **fails on the next CI run** because `system.tables` now returns a row that doesn't match any ALTER TABLE in erase.go.

This design mirrors a similar pattern that works well in the Pirsch project and is explicitly recommended by doc 27 §line 91.

```sql
-- test/enumerate-tables.sql
SELECT name
FROM system.tables
WHERE database = currentDatabase()
  AND name NOT IN ('schema_migrations')
  AND engine NOT LIKE 'View%';
```

```go
// test/integration/dsar_test.go (sketch)
func TestDSAR_AllTablesErased(t *testing.T) {
    ctx := context.Background()
    vh := postSyntheticEvents(t, ctx, 100)
    require.NoError(t, callEraseEndpoint(t, vh))
    require.NoError(t, wal.WaitEmpty(ctx, time.Minute))

    rows, err := db.QueryContext(ctx, loadSQL("enumerate-tables.sql"))
    require.NoError(t, err)
    defer rows.Close()

    for rows.Next() {
        var table string
        require.NoError(t, rows.Scan(&table))
        if isRollup(table) {
            continue  // rollups rebuilt weekly, not row-erased
        }
        var count uint64
        require.NoError(t, db.QueryRowContext(ctx,
            "SELECT count() FROM "+table+" WHERE visitor_hash = ?", vh,
        ).Scan(&count))
        require.Zerof(t, count, "table %s still has rows for erased visitor", table)
    }
}
```

## Why mutation DELETE, not lightweight DELETE

Lightweight DELETE (`DELETE FROM t WHERE ...`) in ClickHouse leaves rows on disk until the next merge — acceptable for query correctness, unacceptable for Article 17 because the "eventual deletion" guarantee is non-deterministic. Mutation-based `ALTER TABLE … DELETE` is deterministic: ClickHouse's compliance guidance (cited above) explicitly recommends this path for GDPR / Article 17 use cases.

Cost: mutation I/O is higher than lightweight. Mitigation: PostHog's batched-weekly-window pattern — accumulate erase requests in a queue, run a single batched mutation once per week.

## Weekly rollup rebuild (the legal safety net)

Rollups use `AggregateFunction(uniqCombined64, FixedString(16))` — HLL state. Row-level erasure on HLL state is not possible by design (the sketch is keyed by day/hour/path/channel, not visitor_hash). So the approach:

1. **Declare rollups anonymous** under Recital 26 + C-413/23 + Breyer + weekly rebuild (DPA language in `gdpr-code-review/README.md`).
2. **Weekly rebuild** from post-erasure `events_raw` — if a supervisory authority ever rejects the HLL-anonymous argument, statnive-live has a bounded-time (max 1 week) path to full compliance.
3. **`robfig/cron` entry** runs Sunday 03:00 IRST: `TRUNCATE <rollup>` then `INSERT INTO <rollup> SELECT …State(…) FROM events_raw GROUP BY …`. Zero-downtime via table-swap pattern.

The skill verifies (a) the cron entry exists, (b) all v1 + v1.1 rollup names appear in the rebuild matrix, (c) the rebuild is idempotent.

## WAL drain requirement

The ingest WAL can hold up to ~37 min of events on a ClickHouse outage. The erase handler **must** wait for WAL drain (or replay-and-filter the WAL) before returning success — otherwise a visitor who issues a DSAR during a CH outage still has events flowing to `events_raw` after the erase completes, and the erase is silently incomplete.

Pattern the skill enforces:
```go
func Erase(ctx context.Context, vh [16]byte) error {
    if err := db.ExecContext(ctx, "ALTER TABLE events_raw DELETE WHERE visitor_hash = ?", vh); err != nil {
        return err
    }
    return wal.WaitEmpty(ctx, 60*time.Minute)
}
```

## Files

- `test/enumerate-tables.sql` — TODO: the dynamic table enumeration.
- `test/integration/dsar_test.go` — TODO: the CI-run integration test.
- `semgrep/rule.yml` — TODO: 4 rules from SKILL.md §Implementation.
- `docs/backup-retention.md` — Phase 11 deliverable; operator-facing doc describing backup rotation + erase propagation.

## Pairs with

- [`gdpr-code-review`](../gdpr-code-review/README.md) — upstream privacy-by-design; this skill is downstream erasure completeness.
- `clickhouse-cluster-migration` — when SaaS goes Distributed, `ALTER TABLE DELETE ON CLUSTER` is the cluster-mode erase path; this skill verifies.
- `sanitize` (BehiSecc, community) — last-mile grep over audit logs + error logs to catch residual PII after erase.
- `grc` (Sushegaad, community) — outer checklist: maps the completeness finding to Article 17 & Recital 66.

## CI integration (TODO)

```makefile
dsar-test:
    go test -tags integration ./test/integration -run TestDSAR

dsar-semgrep:
    semgrep --config=.claude/skills/dsar-completeness-checker/semgrep internal/privacy/ clickhouse/migrations/

release-gate-phase-11: lint test test-integration gdpr-semgrep dsar-test dsar-semgrep
```

## Scope

- `clickhouse/migrations/*.sql` — new `CREATE TABLE` must match an erase-path entry.
- `internal/privacy/erase.go` — the canonical erase surface.
- `internal/audit/**` — new sinks must be visitor_hash-aware.
- `internal/storage/store.go` — new `Store` methods imply new query surfaces.
- Does **not** apply to purely operational tables (`schema_migrations`, `system.*`) — those don't hold visitor data.