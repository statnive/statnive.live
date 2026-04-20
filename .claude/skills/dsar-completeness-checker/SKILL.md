---
name: dsar-completeness-checker
description: MUST USE when writing or reviewing any SQL migration file, any new file under `internal/audit/**`, any change to `internal/privacy/erase.go`, or any change to the `Store` interface in `internal/storage/store.go`. Enforces the DSAR sink matrix — every `CREATE TABLE` in migrations must have a matching erase-path entry in `erase.go`, every new slog sink must be PII-grep-tested, and the erase integration test must enumerate `system.tables` DYNAMICALLY (no hardcoded table list) so new tables fail the test by construction if not added to the erase path.
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 11
  research: "jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md §Gap 3"
---

# dsar-completeness-checker

Encodes the **Article 17 erasure completeness** contract across every sink in the statnive-live architecture. Paired with [`gdpr-code-review`](../gdpr-code-review/README.md) — that skill owns *privacy-by-design on the ingress side*, this skill owns *erasure on the egress side*. DSAR completeness is where most analytics projects silently fail.

## When this skill fires

- Any new `.sql` file under `clickhouse/migrations/`.
- Any change to `internal/privacy/erase.go`.
- Any new file under `internal/audit/**` (new slog sink).
- Any change to `internal/storage/store.go` (new `Store` method implies new query surface).
- Any new `slog.Info` / `slog.Debug` / `slog.Warn` call near ingest / identity / audit paths.

## Enforced invariants — the sink matrix (doc 27 §line 81)

Every sink that can contain a `visitor_hash` must be enumerated and covered by the erase handler. The statnive-live sink matrix:

| Sink | Erase mechanism | Skill check |
|---|---|---|
| `events_raw` | Mutation `ALTER TABLE … DELETE WHERE visitor_hash = ?` (NOT lightweight DELETE — compliance requires deterministic removal, not "eventual") | Verify `erase.go` uses `ALTER TABLE` |
| `hourly_visitors` (v1 rollup) | Not row-erased; rebuilt from post-erasure `events_raw` on weekly schedule | Verify rebuild schedule exists |
| `daily_pages` (v1 rollup) | Weekly rebuild | Verify rebuild schedule |
| `daily_sources` (v1 rollup) | Weekly rebuild | Verify rebuild schedule |
| v1.1 rollups (`daily_geo`, `daily_devices`, `daily_users`) | Weekly rebuild | Verify rebuild matrix is complete |
| WAL (`internal/ingest/wal.go`) | Naturally drains to CH within ~37 min at 9 K EPS; erase handler must wait for WAL drain or replay-and-filter before declaring erase complete | Verify erase handler blocks on WAL-empty condition |
| Audit log (`audit.events` or JSONL file) | Must be queryable by `visitor_hash` AND erasable via same path | Verify `erase.go` touches audit log |
| Bot-detection logs | Often forgotten. Must grep and flag if any visitor_hash lands there | Verify no visitor_hash fields in bot-log schema OR ensure erase path covers |
| Error logs (`slog` output) | Must NEVER contain raw IP / user_id / visitor_hash — aggressive PII rules | Pairs with `gdpr-code-review/semgrep/pii-rules.yml` |
| Backups | Document retention + fact that erasure propagates to next backup generation (WP29 acceptable if backup rotation is short) | Verify `docs/backup-retention.md` exists and names the rotation |
| Future replicas / cluster-mode | Document topology; ensure `ALTER TABLE DELETE ON CLUSTER` at cluster scale | Pairs with `clickhouse-cluster-migration` |

**The single most important rule:** every new `CREATE TABLE` in a migration must be matched by an entry in `internal/privacy/erase.go`. The integration test enumerates tables from `system.tables` dynamically — so new tables fail the DSAR test by construction if not added to the erase path.

## Enforced invariants — the 5-item checklist (doc 27 §Skills §dsar-completeness-checker)

1. **`events_raw` erase uses `ALTER TABLE DELETE`** — not lightweight DELETE. ClickHouse's own guidance (clickhouse.com/blog/handling-updates-and-deletes): "with no guarantees for removal, users with compliance requirements may wish to use mutations".
2. **Rollups are NOT row-erased.** They are rebuilt weekly from `events_raw` as the legal safety net. The skill verifies no code attempts `DELETE FROM hourly_visitors WHERE visitor_hash = ?` (wrong by construction — HLL state is not indexed by visitor_hash).
3. **Weekly rollup rebuild is scheduled.** PostHog pattern: batched weekly windows minimise mutation I/O cost. The skill verifies `robfig/cron` entry exists and runs against all 3 v1 + 3 v1.1 rollups.
4. **Integration test enumerates `system.tables` dynamically.** The test queries `SELECT name FROM system.tables WHERE database = 'statnive'`, filters to user-data tables, and asserts zero rows matching the synthetic visitor_hash. A new table added to a migration without an erase-path entry **fails this test on the next CI run** — this is the load-bearing automation.
5. **Erase handler blocks on WAL-empty before declaring success.** The 37-min WAL tail is a silent compliance gap if the erase handler returns before WAL drains; the skill verifies the handler calls `wal.WaitEmpty(ctx, timeout)` or replay-and-filter.

## Should trigger (reject)

```go
// BAD — hardcoded table list; a new table slips through
var erasableTables = []string{"events_raw", "hourly_visitors"}
for _, t := range erasableTables {
    db.Exec("ALTER TABLE " + t + " DELETE WHERE visitor_hash = ?", vh)
}

// BAD — lightweight DELETE; "eventual" removal fails Article 17
db.Exec("DELETE FROM events_raw WHERE visitor_hash = ?", vh)

// BAD — erase returns before WAL drains
func Erase(ctx context.Context, vh [16]byte) error {
    _ = db.Exec("ALTER TABLE events_raw DELETE WHERE visitor_hash = ?", vh)
    return nil  // WAL may still hold this visitor's events for ~37 min
}
```

## Should NOT trigger (allow)

```go
// OK — dynamic enumeration; new tables fail test if not in erase path
rows, _ := db.Query("SELECT name FROM system.tables WHERE database = 'statnive' AND name != 'schema_migrations'")
defer rows.Close()
for rows.Next() {
    var t string
    _ = rows.Scan(&t)
    if isRollup(t) {
        continue  // rollups are rebuilt, not row-erased
    }
    _, err := db.ExecContext(ctx, "ALTER TABLE "+t+" DELETE WHERE visitor_hash = ?", vh)
    if err != nil { return err }
}

// OK — wait for WAL drain before returning success
if err := wal.WaitEmpty(ctx, 60*time.Minute); err != nil {
    return fmt.Errorf("WAL drain timeout: %w", err)
}

// OK — weekly rebuild scheduled
cron.AddFunc("0 3 * * 0", rebuildAllRollups)  // Sunday 03:00 IRST
```

## Implementation (TODO — Phase 11)

- `test/enumerate-tables.sql` — the dynamic `system.tables` query.
- `test/integration/dsar_test.go` — TODO: creates synthetic visitor_hash, posts 100 events, calls `/api/privacy/erase`, waits for WAL drain, enumerates tables dynamically, asserts zero rows match. Runs in CI.
- `semgrep/rule.yml` — TODO:
  1. flag hardcoded `[]string{"events_raw", ...}` in erase.go.
  2. flag `DELETE FROM events_raw` (lightweight) instead of `ALTER TABLE events_raw DELETE`.
  3. flag `Erase` handler returning without a `wal.WaitEmpty` / replay-and-filter call.
  4. flag any `CREATE TABLE` migration without a matching `erase.go` entry (cross-file grep).
- `docs/backup-retention.md` — Phase 11 deliverable: document backup rotation + propagation-to-next-generation erase policy.

Full spec: [README.md](README.md).