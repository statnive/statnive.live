# Phase 7e chaos-scenario catalog

Seven scenarios (A–G) modelling Iran-specific failure modes that the Phase 10 SamplePlatform cutover must survive without exceeding the analytics-invariant SLOs. Spec: [research doc 29 §5](../../../../jaan-to/docs/research/29-Production-load-simulation-gate-statnive-live-asiatech-tehran.md) + [doc 30 §3](../../../../jaan-to/docs/research/30-ga4-calibration-delta.md).

| Script | Source | Disruption | Knob |
|---|---|---|---|
| `A_bgp_cut.sh` | doc 29 §5.1 | Full BGP cut — non-IR egress dropped | `iptables` chain |
| `B_mobile_curfew.sh` | doc 29 §5.2 | 80% loss + 200 ms delay (Sept 2022 curfew) | `tc netem` |
| `C_dpi_rst.sh` | doc 29 §5.3 | 2% TLS RST injection by SNI | `xt_tls` (or fallback) |
| `D_tehran_ix_degrade.sh` | doc 29 §5.4 | 200 ms ± 50 ms jitter on Tehran-IX peer subnet | `tc prio + netem + u32` |
| `E_asiatech_partial.sh` | doc 29 §5.5 | Kill one CH replica; verify drain + reconciliation | `docker stop` |
| `F_clock_skew.sh` | doc 29 §5.6 | Step ±5 min across IRST midnight salt rotation | `date -s`, `chronyc makestep` |
| `G_intl_egress.sh` | **doc 30 §3** | 100 ± 20 ms jitter + 2% loss on Tehran-IX → Frankfurt path (38% diaspora cohort) | `tc prio + netem + u32` |

## Convention

Every script implements three subcommands:

- `up` — apply the disruption (idempotent — re-running is a no-op)
- `down` — remove the disruption (idempotent)
- `status` — print state, exit 0 if up, 1 if down

A fourth orchestration mode is provided via shared lib:

- `run-with-oracle` — capture pre-oracle, `up`, hold for `$CHAOS_HOLD_SEC` (default 300 s), `down`, capture post-oracle. Outputs land in [`runs/`](runs/) as `<scenario>-<run_id>-{pre,post}.json`.

The shared helpers in [`_lib.sh`](_lib.sh) require root for tc / iptables / chronyd manipulation. Scenario E (docker container kill) requires `docker` + the dev compose stack.

## Phase 7e dry-run

```bash
# pick up RUN_ID from the orchestrator (must match the harness)
export RUN_ID=$(uuidgen)
make chaos-matrix
```

Each scenario script in `make chaos-matrix` runs `up` → hold → `down`, captures pre/post oracle SQL, then proceeds to the next. Scenarios are sequential, not parallel — concurrent disruptions are not part of the doc 29 matrix.

## Phase 10 distributed runs

Phase 10 wires Ansible playbooks around these scripts so the chaos applies on the **target collector host**, not on the generator. The 2-node test bed is generator + collector co-located, so running the chaos script locally exercises both ends. At Phase 10 the collector lives on a separate Asiatech VPS rack from the generator fleet — the script gets shelled in via SSH from the orchestrator.

## Open scope (Phase 10 not Phase 7e)

- `G_intl_egress.sh` requires a Hetzner Frankfurt / Helsinki diaspora beacon to validate the 38% non-Iran cohort SLO. Phase 7e ships only the netem qdisc; the beacon-side measurement lands at Phase 10 procurement.
- `C_dpi_rst.sh` xt_tls fallback uses random sampling — Phase 10 either provisions an xt_tls-equipped staging kernel or accepts the looser fallback (decision rubric in [docs/runbook.md § Phase 10 chaos provisioning]).
