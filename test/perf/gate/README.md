# Phase 7e graduation-gate harness

Locust (primary) load harness for the Phase 10 P1–P5 SamplePlatform cutover gate. This directory scaffolds the gate; the actual sustained-load runs happen against the Asiatech generator fleet at Phase 10.

Spec: [PLAN.md §283](../../../PLAN.md), [research doc 29](../../../../jaan-to/docs/research/29-Production-load-simulation-gate-statnive-live-asiatech-tehran.md), [research doc 30 §3 + §6](../../../../jaan-to/docs/research/30-ga4-calibration-delta.md). Guarded by [`load-gate-harness` skill](../../../.claude/skills/load-gate-harness/README.md).

## Files

| Path | Role |
|---|---|
| `locustfile.py` | `FastHttpUser` mirroring the k6 [load.js](../load.js) scenario; emits the four oracle fields per request as HTTP headers |
| `locust-master.py` | Orchestrator wrapper — mints `test_run_id`, probes `/healthz`, runs Locust, runs `make oracle-scan` on stop |
| `requirements.txt` | Pinned Locust + transitive deps (MIT/Apache only; verify with `pip-licenses` before bumping) |
| `worker-manifest.yaml` | Phase 10 Asiatech generator-fleet inventory (placeholder hosts; real IPs at procurement) |

The canonical Persian-paths + Iranian-UA list is at [`../generator/shape/load-shape.json`](../generator/shape/load-shape.json). The Go generator embeds it via `go:embed`; the Locust harness reads it at startup. Update the JSON once and both code paths follow.

## 2-node dry-run (Phase 7e acceptance)

The Phase 7e exit criterion is "P1 dry-run passes on a 2-node isolated test bed" — one binary host + one generator host, both behind the loopback firewall.

```bash
# Terminal A — binary
make ch-up && make build && ./bin/statnive-live -c config/statnive-live.yaml.example

# Terminal B — install Locust + run 5-min smoke
python3 -m venv .venv-load-gate && source .venv-load-gate/bin/activate
pip install -r test/perf/gate/requirements.txt
./test/perf/gate/locust-master.py --users 200 --spawn-rate 50 --run-time 5m
```

On stop, the master invokes `make oracle-scan RUN_ID=<minted-uuid>`, which runs the four canonical queries from doc 29 §6.2 against `events_raw` filtered by `test_run_id` and asserts the analytics-invariant thresholds (loss ≤ 0.05% server, duplicates ≤ 0.1%, latency p99 within doc 29 §6.4).

## k6 cross-check

`make load-gate-crosscheck` runs both [load.js](../load.js) and `locustfile.py` at the same arrival rate against a freshly-restarted binary and asserts p99 latency deltas within 5% (doc 29 §3.1 — Locust's `FastHttpUser` and k6's HTTP/1.1 client are expected to agree at our scale).

## Phase 10 distributed runs

```bash
# Master VPS (1×AT-VPS-G2)
./test/perf/gate/locust-master.py \
  --distributed --workers test/perf/gate/worker-manifest.yaml \
  --target https://collector-staging.statnive.live \
  --users 50000 --spawn-rate 1000 --run-time 72h
```

Three worker VPS (`AT-VPS-B1` × 3) each export `LOCUST_GENERATOR_NODE_ID={1,2,3}` before joining `--worker --master-host=<master-ip>`. The four oracle fields keep loss accounting per (run, node) — generator restarts get a **fresh** node-id; never resume a seq counter.

## Open follow-up — handler-side wire-up

The four oracle fields ride as HTTP headers (`X-Statnive-Test-Run-Id`, `X-Statnive-Generator-Node-Id`, `X-Statnive-Test-Generator-Seq`, `X-Statnive-Send-Ts`). The binary's `/api/event` handler must copy them into the corresponding events_raw columns (added by [migration 006](../../../internal/storage/migrations/006_load_gate_columns.sql)) before WAL write. Phase 7e ships the harness side; the handler patch is a follow-up tracked in the [`load-gate-harness` skill](../../../.claude/skills/load-gate-harness/README.md) and is required before the P1 dry-run can return non-default values from `oracle-scan`.

## Threshold mirror (doc 29 §4 + CLAUDE.md)

- Event loss server ≤ 0.05% / client ≤ 0.5%
- Duplicates ≤ 0.1%
- Attribution correctness ≥ 99.5%
- PII leaks (Vector/VRL pipeline) = 0
- TTFB overhead ≤ +10% / +25 ms

Any breach during the 72-h soak or within the 7-scenario chaos matrix halts the gate and blocks the corresponding Phase 10 sub-phase cutover.
