# Phase 7e load-gate synthesizer

Standalone Go program emitting doc 29 §6.1 generator_seq events. Complements the [Locust harness](../gate/README.md) for two cases pure HTTP load doesn't cover:

1. **Replay** — line-protocol NDJSON from SamplePlatform anonymized exports (chain-of-custody per [docs/replay-attestation-template.md](../../../docs/replay-attestation-template.md)).
2. **Synth curve formulas** — `match_spike()` 2.5–4× peak (doc 29 §2.5), `ramadan_diurnal()` 1.8–2.2× iftar peak, doc 30 §6 long-session 1000 VUs × 6 h × 1080 @ 20 s pings. Phase 7e ships the binary as scaffolding; the curve-formula wire-up lands at the Phase 10 P3+ gate.

## Build + run

```bash
go build -o bin/load-gate-generator ./test/perf/generator
sudo ./test/perf/generator/sysctl.sh                        # one-time per host
bin/load-gate-generator --mode=synth --eps 450 --duration 5m
```

Synth mode emits Iranian UAs + Persian paths + 1500-visitor cookies against the binary's `/api/event`. Each request carries the four oracle fields as headers — same wire format as the Locust harness.

## Replay mode

```bash
bin/load-gate-generator \
  --mode=replay \
  --replay-file=path/to/SamplePlatform-anon-2026-Q2.ndjson \
  --target=https://collector-staging.statnive.live
```

Replay file is one JSON event per line. Chain-of-custody is signed off via [docs/replay-attestation-template.md](../../../docs/replay-attestation-template.md) per phase (P1…P5).

## Oracle field discipline (non-negotiable)

Every emit, both modes, carries the four oracle fields as HTTP headers:

| Header | Source |
|---|---|
| `X-Statnive-Test-Run-Id` | `--run-id` flag (UUID) — fresh per run; mint via `uuidgen` if not supplied |
| `X-Statnive-Generator-Node-Id` | `--node-id` flag (UInt16) — fresh per generator host; **never resume on restart** |
| `X-Statnive-Test-Generator-Seq` | dense monotonic per-process counter (atomic.Uint64) |
| `X-Statnive-Send-Ts` | wall-clock ms since epoch at emit time |

The binary's `/api/event` handler must copy these headers into the events_raw columns added by [migration 006](../../../internal/storage/migrations/006_load_gate_columns.sql). Handler-side wire-up is a Phase 7e implementation follow-up tracked in the [`load-gate-harness` skill](../../../.claude/skills/load-gate-harness/README.md).

## License posture

- Stdlib + `github.com/google/uuid` (MIT) — both already vendored. No transitive AGPL.
- Verify on bump: `make licenses` against the generator package path.
- Air-gap clean: `--target` is the sole network egress; no telemetry, no DNS lookups beyond the target hostname.
