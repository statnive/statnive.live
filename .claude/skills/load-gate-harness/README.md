# load-gate-harness — full spec

## Architecture rule

Encodes [PLAN.md §283 Phase 7e](../../../PLAN.md) and [statnive-live/CLAUDE.md Architecture Rule 5 carve-out](../../../CLAUDE.md) (test-instrumentation columns use typed DEFAULT sentinels, never `Nullable(...)`). Ships the [doc 29 §6.1 generator_seq oracle](../../../../jaan-to/docs/research/29-Production-load-simulation-gate-statnive-live-asiatech-tehran.md) protocol as a CI-blocking guardrail.

## Research anchors

- [jaan-to/docs/research/29-Production-load-simulation-gate-statnive-live-asiatech-tehran.md](../../../../jaan-to/docs/research/29-Production-load-simulation-gate-statnive-live-asiatech-tehran.md) §3.1 (tool stack), §5 (chaos scenarios), §6 (oracle protocol), §8 (W1–W5 schedule).
- [jaan-to/docs/research/30-ga4-calibration-delta.md](../../../../jaan-to/docs/research/30-ga4-calibration-delta.md) §3 (international-egress scenario G), §6 (long-session soak).
- [statnive-live/PLAN.md §283–§296](../../../PLAN.md) — Phase 7e checklist + acceptance.

## Implementation phase

**Phase 7e — Load-simulation gate scaffolding (HARD GATE on Phase 10 P1 cutover).** This skill is the regression gate for the four invariants in [SKILL.md](SKILL.md). Phase 7e ships the harness, semgrep rules, and dry-run procedure on a 2-node test bed; Phase 10 wires the gate into the Asiatech generator fleet for the actual P1–P5 graduation runs.

## The four enforced invariants

| Rule ID | Surface | What it rejects |
|---|---|---|
| `oracle-fields-required` | `test/perf/generator/**` | Any HTTP request emit that omits one of the four `X-Statnive-*` headers |
| `chaos-script-up-down-status` | `test/perf/chaos/*.sh` | Any chaos script missing the up/down/status subcommands |
| `no-nullable-on-oracle-columns` | `internal/storage/migrations/*.sql` | Any future migration that uses `Nullable(...)` on `test_run_id`, `generator_node_id`, `test_generator_seq`, or `send_ts` |
| `observability-image-pin` | `deploy/observability/docker-compose*.yml` | Any image reference that uses `:latest` or omits `@sha256:` |

## Files

```
load-gate-harness/
├── README.md            — this file
├── SKILL.md             — frontmatter + invariant summary
├── semgrep/
│   └── rules.yml        — the four Semgrep rules
└── test/
    └── fixtures/
        ├── should-trigger/      — positive cases (rule must fire)
        └── should-not-trigger/  — negative cases (rule stays silent)
```

## Should trigger (reject)

```go
// BAD — emits without the four oracle headers
req, _ := http.NewRequest(http.MethodPost, target+"/api/event", body)
req.Header.Set("Content-Type", "text/plain")
client.Do(req)
```

```sql
-- BAD — Architecture Rule 5 violation
ALTER TABLE statnive.events_raw
    ADD COLUMN test_run_id Nullable(UUID);
```

```yaml
# BAD — image not pinned
services:
  prometheus:
    image: prom/prometheus:latest
```

## Should NOT trigger

```go
// OK — all four oracle headers set; matches the canonical generator pattern
req.Header.Set("X-Statnive-Test-Run-Id", gen.RunID.String())
req.Header.Set("X-Statnive-Generator-Node-Id", strconv.FormatUint(uint64(gen.NodeID), 10))
req.Header.Set("X-Statnive-Test-Generator-Seq", strconv.FormatUint(seq, 10))
req.Header.Set("X-Statnive-Send-Ts", strconv.FormatInt(time.Now().UnixMilli(), 10))
```

```sql
-- OK — typed DEFAULT sentinel, sparse-serialization-safe
ALTER TABLE statnive.events_raw
    ADD COLUMN test_run_id UUID DEFAULT toUUID('00000000-0000-0000-0000-000000000000') CODEC(ZSTD(1));
```

```yaml
# OK — SHA256 digest pinned
services:
  prometheus:
    image: prom/prometheus@sha256:abc123...
```

## Local invocation

```bash
# All four rules across all four surface dirs
semgrep --quiet --error \
    --config=.claude/skills/load-gate-harness/semgrep \
    test/perf/ deploy/observability/ internal/storage/migrations/

# Self-test (positive + negative fixtures)
semgrep --quiet --error \
    --config=.claude/skills/load-gate-harness/semgrep \
    .claude/skills/load-gate-harness/test/fixtures/
```

## Capacity SLO (document, don't assume)

The doc 29 §4 graduation matrix targets, repeated here as the guardrail's contract:

| Phase | Sustained EPS | Burst EPS | Cluster |
|---|---|---|---|
| P1 | 450 | 700 | AT-VPS-G2 |
| P2 | 1 000 | 1 500 | AT-VPS-G2 + AT-VPS-A1 |
| P3 | 4 000 | 7 500 | AT-VPS-A1 |
| P4 | 9 000 | 18 000 (match-day) | dedicated 16c/64GB |
| P5 | 40 000 | 60 000 (Tehran-derby Friday) | dedicated 32c/128GB |

The four oracle queries (loss / duplicates / ordering / latency) must return zero / zero / acceptable-out-of-order / within-SLO p99 at every phase before sub-phase cutover proceeds.

## CI integration (Phase 7e → Phase 10)

```makefile
load-gate-semgrep:
    semgrep --config=.claude/skills/load-gate-harness/semgrep \
            test/perf/ deploy/observability/ internal/storage/migrations/
```

Phase 7e ships this target as advisory. Phase 10 P1 cutover flips the GHA workflow flag `LOAD_GATE_ADVISORY` to `0` and the rule set becomes a HARD GATE on every subsequent PR — any new oracle-related code that violates one of the four rules fails the build.

## Pairs with

- [`clickhouse-cluster-migration`](../clickhouse-cluster-migration/README.md) — validates the `{{if .Cluster}}` template + reversibility on [migration 006](../../../internal/storage/migrations/006_load_gate_columns.sql).
- [`clickhouse-rollup-correctness`](../clickhouse-rollup-correctness/README.md) — keeps oracle columns out of the rollup ORDER BY (they're only on `events_raw`, never on rollups).
- [`gdpr-code-review`](../gdpr-code-review/README.md) — the Vector PII wire-scan in [`deploy/observability/vector/vector.toml`](../../../deploy/observability/vector/vector.toml) operates alongside this skill but is separately enforced.