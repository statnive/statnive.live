# clickhouse-upgrade-playbook

Advisory-only runbook for the single-node → replicated cluster migration. Companion to `clickhouse-operations-review` (enforcement) and `clickhouse-cluster-migration` (`{{if .Cluster}}` templating lint).

## Why advisory and not enforcement?

The migration is **manual by design**. No Semgrep rule can distinguish a correct hard-link `ATTACH PARTITION` procedure from an incorrect one — the difference is operational: storage headroom, Keeper quorum, read-only window, rollback readiness, post-migration parity checks.

This skill surfaces the runbook when the agent encounters a trigger (migration file containing `Engine=` or `{{if .Cluster}}`) so they don't believe the templating alone does the work.

## Quick decision tree

```
Need to go from single-node MergeTree → replicated cluster?
├─ Can you take a read-only window?
│   └─ Yes → use hard-link ATTACH PARTITION (preferred, near-zero disk cost)
├─ Is your CH ≥ 23.8 AND Keeper already running?
│   └─ Yes → use `convert_to_replicated` flag file (simpler per-table)
├─ Are tables < 10M rows?
│   └─ Yes → INSERT INTO SELECT is acceptable (loses merge history, pay at next merge)
└─ None of the above?
    → Schedule a read-only window; hard-link ATTACH is still the answer.
```

## Four hard constraints

See [SKILL.md](SKILL.md) for the full body. In summary:

1. `ALTER MODIFY ENGINE` does NOT convert MergeTree → Replicated (CH #70118, #14576).
2. Preferred: hard-link `ATTACH PARTITION ... FROM events` for each partition, then rename.
3. Alternative (CH ≥ 23.8): `convert_to_replicated` flag file + restart.
4. Keeper over ZooKeeper at P5 — 3-node RAFT, dedicated hosts.

## Companion skills

- [`clickhouse-operations-review`](../clickhouse-operations-review/README.md) — enforces WAL-first writes, ban on `OPTIMIZE FINAL`, parts-ceiling alerts. Required before any cluster work to ensure operational invariants hold at scale.
- [`clickhouse-cluster-migration`](../clickhouse-cluster-migration/README.md) — lints `{{if .Cluster}}` templating syntax in migration files. Shipped in doc 25 custom skills.

## Research anchor

Doc 28 §Gap 3 lines 598 (core constraints), 711–728 (skill spec).