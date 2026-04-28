---
name: load-gate-harness
description: MUST USE when editing `test/perf/gate/**`, `test/perf/chaos/**`, `test/perf/generator/**`, or `deploy/observability/**`. Enforces the four oracle fields on every event-emit path, the up/down/status convention on every chaos script, the Architecture Rule 5 carve-out (typed DEFAULT, never Nullable) on the four oracle columns, and SHA256-pinned digests on every observability container reference. Advisory during Phase 7e scaffolding; HARD GATE on Phase 10 P1 cutover.
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 7e
  research: "jaan-to/docs/research/29-Production-load-simulation-gate-statnive-live-asiatech-tehran.md §5–§6 + jaan-to/docs/research/30-ga4-calibration-delta.md §3 §6"
---

# load-gate-harness

> **Activation gate (Phase 7e — scaffolding live).** Semgrep rules + chaos-script convention + image-pin enforcement land in this PR. Until `test/perf/gate/` ships its handler-side wire-up (Phase 7e follow-up — binary's `/api/event` must copy the four oracle headers into events_raw) the skill stays advisory. Flips to HARD GATE on Phase 10 P1 cutover.

Encodes the [PLAN.md §283 Phase 7e contract](../../../PLAN.md), the [research doc 29 §5–§6 spec](../../../../jaan-to/docs/research/29-Production-load-simulation-gate-statnive-live-asiatech-tehran.md), and the [doc 30 §3 + §6 overlay](../../../../jaan-to/docs/research/30-ga4-calibration-delta.md). Pairs with [statnive-live/CLAUDE.md Architecture Rule 5](../../../CLAUDE.md) (test-instrumentation columns are typed DEFAULT, never Nullable).

## When this skill fires

- Any file edit under `test/perf/gate/**`, `test/perf/chaos/**`, `test/perf/generator/**`, or `deploy/observability/**`.
- Any new ClickHouse migration touching `test_run_id`, `generator_node_id`, `test_generator_seq`, or `send_ts`.
- Any reference to a docker image inside `deploy/observability/`.

## Enforced invariants — the 4-rule checklist

1. **Every event-emit path under `test/perf/generator/**` populates all four oracle headers.** Missing any one of `X-Statnive-Test-Run-Id`, `X-Statnive-Generator-Node-Id`, `X-Statnive-Test-Generator-Seq`, `X-Statnive-Send-Ts` makes the oracle scan return wrong loss / dup numbers (the canonical doc 29 §6.2 queries key on the four-tuple). Rejected by `oracle-fields-required`.

2. **Every chaos script under `test/perf/chaos/*.sh` implements `up` + `down` + `status` (and the shared-lib `run-with-oracle` wrapper).** Idempotency is non-negotiable — `make chaos-matrix` reruns scripts inside a control loop and assumes second-and-later invocations are no-ops. Rejected by `chaos-script-up-down-status`.

3. **No `Nullable(...)` on oracle columns in any future migration.** doc 29 §6.1 prescribed `Nullable`; CLAUDE.md Architecture Rule 5 carves out test-instrumentation columns explicitly as **typed DEFAULT** (UUID zero / UInt64 0 / UInt16 0 / DateTime64(3) 0) to preserve sparse serialization. Rejected by `no-nullable-on-oracle-columns`.

4. **Every container reference under `deploy/observability/` pins a SHA256 digest, not a tag.** `:latest` is rejected; `image: foo:1.2.3` (no `@sha256:…`) is rejected. The placeholder zero-byte digests in `docker-compose.observability.yml` are accepted at scaffold time but must be replaced with real digests at deploy. Rejected by `observability-image-pin`.

## Phase 7e follow-ups (open)

- **Handler-side wire-up** — the binary's `/api/event` handler must copy the four `X-Statnive-*` headers into the new events_raw columns before WAL write. Without this the oracle scan returns the default sentinel for every column and every loss query reports 100%. Tracked here for the Phase 7e completion PR.
- **Generator curve formulas** — `match_spike()` 2.5–4× / `ramadan_diurnal()` 1.8–2.2× / `long_session()` 1000 VUs × 6h × 1080 @ 20s pings stubs are present but not wired. Phase 10 P3+ surfaces these.
- **Diaspora beacon** — Scenario G (`test/perf/chaos/G_intl_egress.sh`) needs a Hetzner Frankfurt / Helsinki measurement endpoint. Procurement at Phase 10.
- **xt_tls kernel module** — Scenario C falls back to coarse random RST without `xt_tls`. Phase 10 either provisions an xt_tls-equipped staging kernel or accepts the fallback (decision rubric in `docs/runbook.md` § Phase 10 chaos provisioning).

## Files

- `semgrep/rules.yml` — the four Semgrep rules above.
- `test/fixtures/should-trigger/` — positive cases per rule.
- `test/fixtures/should-not-trigger/` — negative cases per rule.

## CI integration

Wire-up to `make ci-local` happens at Phase 7e completion (after the handler-side patch lands). Until then, the skill is invokable manually:

```bash
semgrep --quiet --error --config=.claude/skills/load-gate-harness/semgrep \
    test/perf/ deploy/observability/
```

The Phase 10 cutover gate adds this rule set to `.github/workflows/security-gate.yml` and flips it from advisory to HARD GATE.

## Posture toggle

Hard-gate enablement happens in one place — flip `LOAD_GATE_ADVISORY=0` in `.github/workflows/security-gate.yml` and the rule set fails the build on any violation. Until then it surfaces as a non-blocking annotation only.