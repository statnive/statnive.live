# wal-durability-review — full spec

## Architecture rule

Encodes **CLAUDE.md Architecture Rule 4** (WAL for durability, batch 500 ms / 1000 rows) and the **"zero event loss on `kill -9`"** contract. Pairs with PLAN.md Verification items #9, #10, #11, #17.

## Research anchors

- [jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md](../../../../jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md) §Gap 1 — primary source.
- [jaan-to/docs/research/22-statnive-ingestion-pipeline-10-critical-gaps-drop-in-go-code.md](../../../../jaan-to/docs/research/22-statnive-ingestion-pipeline-10-critical-gaps-drop-in-go-code.md) §GAP 5 — original WAL design.
- [fsyncgate — LWN 752063](https://lwn.net/Articles/752063/) + [752093](https://lwn.net/Articles/752093/) + [wiki.postgresql.org/wiki/Fsync_Errors](https://wiki.postgresql.org/wiki/Fsync_Errors).

## Implementation phase

**Phase 7b — Backend cleanup + real-traffic verification.** PR #14 surfaced three known WAL gaps:
1. WAL replay decodes only 1 of N entries after SIGKILL (tidwall NoSync window).
2. Consumer drops events when ClickHouse unreachable (fill_ratio stays 0.0).
3. Post-restart drain unreliable.

This skill is the 10-item regression gate that Phase 7b fixes must satisfy.

## tidwall/wal semantics (what the skill must know)

- **Default options:** `NoSync: false, SegmentSize: 20 MB, LogFormat: Binary, SegmentCacheSize: 2, NoCopy: false`.
- **Binary format does NOT CRC individual records.** Replay relies on length-prefix validation. Torn-write detection is best-effort. If CRC-per-record becomes a compliance requirement, plan v2 migration to `rosedblabs/wal` (LevelDB-style 32 KB blocks with CRC-32 per chunk).
- **API surface:** `Open, Write, WriteBatch, Read, FirstIndex, LastIndex, TruncateFront, TruncateBack, Sync, Close, ClearCache, IsEmpty`. The `corrupt bool` flag on the `Log` struct surfaces to callers; the skill must ensure it's checked at replay time.

## Files

- `semgrep/rule.yml` — TODO: the 4 Semgrep rules from doc 27 §line 42.
- `test/kill9/harness.sh` — TODO: the kill-9 CI gate described below.
- `test/fixtures/should-trigger/` — TODO: ack-before-fsync, Sync-error-swallowed, truncate-before-commit, panic-in-write-path.
- `test/fixtures/should-not-trigger/` — TODO: group-commit pattern, os.Exit on Sync error, CH-commit-then-truncate.

## Capacity SLO (document, don't assume)

- 500 B enriched event × 9 K EPS sustained = 4.5 MB/s WAL bytes.
- SegmentSize 20 MB rotates every ~4.4 s at 9 K EPS.
- 10 GB disk cap / 4.5 MB/s ≈ **37 min of ClickHouse outage tolerance at 9 K EPS**.
- At 18 K burst, drops to ~18 min.
- These are documented SLO floors, not implicit; the skill's checklist item #6 (disk-full returns 503) enforces the behavior, PLAN.md Verification items encode the numbers.

## CI integration (TODO — Phase 7b)

```makefile
kill9-gate:
    ./.claude/skills/wal-durability-review/test/kill9/harness.sh 50

wal-semgrep:
    semgrep --config=.claude/skills/wal-durability-review/semgrep internal/ingest/

release-gate: lint test test-integration kill9-gate wal-semgrep
```

The `kill9-gate` target exec's the binary under controlled offsets, counts client-received-2xx against post-restart `count()` in events_raw, and fails if the numbers diverge by > 0.05% (the event-loss SLO from PLAN.md).

## Observability (mandatory per doc 27 §line 21)

Prometheus metrics the skill verifies are exposed:
- `wal_bytes_written_total` (counter)
- `wal_fsync_duration_seconds` (histogram, p99 alertable)
- `wal_segment_count` (gauge)
- `wal_replay_duration_seconds` (histogram)
- `wal_records_replayed_total` (counter)
- `wal_corrupt_records_total` (counter — alert on any non-zero increase)

Alert rules: `p99(wal_fsync_duration_seconds) > 50ms for 5m` (degraded disk); `wal_segment_count > 500` (ClickHouse consumer stuck).

## Library choice note

Keep `tidwall/wal` for v1 — it's MIT, vendored-friendly, pure Go, matches air-gap constraints. Plan v2 migration to `rosedblabs/wal` (CRC-per-record) only if a regulator asks "how do you detect silent bitrot in buffered events?". `hashicorp/raft-wal` is overkill for a single-binary ingest path.

## Pairs with

- `golang-concurrency` (community) — worker pool / errgroup patterns around the WAL goroutine.
- `golang-safety` (community) — `defer` in loops, slice aliasing — both common WAL bug sources.
- `systematic-debugging` (community) — when the kill-9 gate fails, this is the investigation loop.

## Scope

`internal/ingest/wal.go`, `internal/ingest/consumer.go`, `internal/ingest/handler.go` (fsync-before-ack path), plus any test under `test/perf/` touching crash recovery or disk-full. Does **not** apply to audit-log writes (`internal/audit/log.go` — that's a separate file sink, not the ingest WAL).
