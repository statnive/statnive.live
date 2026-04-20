---
name: wal-durability-review
description: MUST USE when writing or reviewing code that touches `internal/ingest/wal.go`, `internal/ingest/consumer.go`, tidwall/wal imports, or any `.Sync()` / `.Flush()` / `.WriteBatch()` / `.TruncateFront()` call. Enforces fsync-before-ack semantics, Sync-error-is-terminal (os.Exit on EIO per fsyncgate 2018), `TruncateFront` only after the ClickHouse commit, and the presence of a kill-9 CI gate. tidwall/wal does NOT CRC individual records in binary format — replay is best-effort, this skill guards the surrounding invariants.
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 7
  research: "jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md §Gap 1"
---

# wal-durability-review

Encodes **CLAUDE.md Architecture Rule 4** ("WAL for durability, batch 500 ms / 1000 rows") and the **"zero event loss on `kill -9`"** contract implicit in PLAN.md §Phase 1. PR #14 surfaced three WAL correctness gaps that Phase 7b must close; this skill is the regression guard.

## When this skill fires

- Any file edit under `internal/ingest/wal.go` or `internal/ingest/consumer.go`.
- Any `*_test.go` touching `wal`, `consumer`, crash recovery, or disk-full scenarios.
- Any import of `github.com/tidwall/wal`.
- Any call site of `.Sync(`, `.Flush(`, `.WriteBatch(`, `.TruncateFront(`.
- Any new Prometheus metric name matching `wal_*`.

## Enforced invariants — the 10-item checklist (doc 27 §Gap 1)

1. **Every HTTP handler path that returns 2xx is gated on a completed `Sync()`** for the event it just wrote. The skill prefers **ack-after-fsync with group commit** (one goroutine fsyncs every 100 ms; all waiting handlers return 200 together) over ack-before-fsync.
2. **`Sync()` errors are terminal.** An EIO from `wal.Sync()` must `os.Exit(1)` — never retry-and-continue. Pre-4.13 Linux `fsync()` marks affected pages clean on EIO and forgets the error; the next `Sync()` returns success but the data is gone (LWN 752063, fsyncgate 2018). Postgres responded by making `fsync` failures PANIC.
3. **`TruncateFront` is called only AFTER the ClickHouse transaction for that index range has committed.** Truncate-then-commit loses data on CH failure between the two.
4. **The WAL directory is on the same filesystem as its parent** so `rename` is atomic. Cross-FS `os.Rename` is not atomic.
5. **A `TestKillMidFlush` test exists and runs in CI with at least 20 random kill points.** Exec the binary, push 10 K events, SIGKILL at random 100 ms–2 s offset, restart, assert `count() FROM events_raw = count the client received 200 for`.
6. **Disk-full returns 503 with the existing WAL untouched.** Tested with a sparse 100 MB loopback mount.
7. **`wal_fsync_duration_seconds` p99 metric is exposed.** Alert if p99 > 50 ms for 5 minutes (indicates degraded disk).
8. **Replay on startup is idempotent** and logs with `{segment_first, segment_last, records_replayed, bytes_replayed, elapsed_ms, corrupt_detected}`.
9. **`LastIndex()` is asserted monotonic** in a post-replay invariant check.
10. **Corrupt segment detection triggers an alert, not a silent skip.** `Log.corrupt` flag or replay error path emits `wal_corrupt_records_total` and surfaces in the runbook.

## Three opinionated defaults this skill encodes

- **(a) Ack-after-fsync with group commit** — not ack-before-fsync. ~50 ms p50 latency cost is within the 500 ms p99 SLO. Only config that honors the kill-9 guarantee as worded.
- **(b) Sync-error is `os.Exit(1)`** — orchestrator restarts; retry on same FD is not safe.
- **(c) Document the SLO** — at 9 K EPS × 500 B, the 10 GB WAL cap gives ~37 min of ClickHouse outage tolerance; at 18 K burst it drops to ~18 min. These numbers are a documented SLO, not an implicit assumption.

## Should trigger (reject)

```go
// BAD — ack before fsync; 100 ms of loss on kill -9
func (h *Handler) ingest(w http.ResponseWriter, r *http.Request) {
    _ = h.wal.Write(idx, bytes)
    w.WriteHeader(204)   // acked; fsync hasn't happened
}

// BAD — Sync error swallowed, loop continues
for {
    if err := log.Sync(); err != nil {
        slog.Warn("sync failed", "err", err)
        continue   // fsyncgate: silent data loss
    }
}

// BAD — TruncateFront before CH commit
func (c *Consumer) flush(ctx context.Context, batch []Event) error {
    _ = c.wal.TruncateFront(batch[len(batch)-1].Idx + 1)
    return c.ch.InsertBatch(ctx, batch)   // if this fails, data is gone
}
```

## Should NOT trigger (allow)

```go
// OK — group-commit fsync; handlers wait for the barrier
func (g *GroupCommit) Wait(ctx context.Context, idx uint64) error {
    g.mu.Lock()
    g.pending = append(g.pending, idx)
    ch := g.notify
    g.mu.Unlock()
    select {
    case <-ch:              // next fsync tick wakes us
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}

// OK — Sync error is terminal
if err := log.Sync(); err != nil {
    slog.Error("wal sync failed, exiting", "err", err)
    os.Exit(1)
}

// OK — truncate only after CH ack
if err := c.ch.InsertBatch(ctx, batch); err != nil {
    return err   // WAL still intact; retry next tick
}
_ = c.wal.TruncateFront(batch[len(batch)-1].Idx + 1)
```

## Implementation (TODO — Phase 7b)

- `semgrep/rule.yml` — three Semgrep rules from doc 27 §Gap 1:
  1. detect `wal.Write(...)` in a handler where no `Sync` is called before `w.WriteHeader(2xx)`.
  2. detect `log.Sync(); err != nil` without `os.Exit` or equivalent terminal action.
  3. detect `TruncateFront` called before the ClickHouse commit in the same function.
  4. detect `panic(` / `os.Exit(` in the write-path outside of `Sync` error handling.
- `test/fixtures/` — should-trigger / should-not-trigger Go cases.
- `test/kill9/` — CI harness: exec binary, push 10 K events, SIGKILL random offset, restart, assert count match. 50 runs per PR.

Full spec + library-choice note: [README.md](README.md).
