# Phase 7e chaos matrix

Seven scripted scenarios that exercise statnive-live under the failure modes most likely to hit an Iranian-DC deployment (doc 29 §scenarios A–F + doc 30 § scenario G). Each script is `<setup> | <wait/test> | <teardown>` — running just the script with no flags applies the chaos for a fixed window, then restores; passing `--apply` / `--restore` gives the load-gate harness control over the timing.

| Scenario | What it simulates | Knob |
|----------|-------------------|------|
| `bgp-cut.sh` | Iranian BGP cut from international (Asiatech ↔ Tehran-IX peering down) | `iptables -P OUTPUT DROP` toggle on the box |
| `mobile-curfew.sh` | Iranian government rolling mobile-internet curfew | drop 90% of requests with mobile UA at the binary's tracker handler |
| `dpi-rst.sh` | Iranian DPI injecting RST on long-lived connections | `iptables` REJECT with tcp-reset on conn-tracker entries > 60 s |
| `tehran-ix.sh` | Tehran-IX degradation (one of the IRNIC peering fabrics flapping) | `tc qdisc` adds 80–300 ms jitter + 1–3% packet loss on egress |
| `asiatech-outage.sh` | Full DC partition (Asiatech ⇄ Internet down) | block all `OUTPUT`+`INPUT` except loopback and the operator's SSH source |
| `clock-skew.sh` | Chrony unable to sync to Iranian NTP sources; system clock drifts | `chronyc -a 'settime now +120sec'` then revert |
| `intl-egress.sh` (doc 30 §G) | Outbound to non-IR ASN ranges blocked but IR-internal egress still works | `iptables` REJECT on egress to non-IR /8s |

## Calling convention

Each script supports three modes:

```bash
# Apply the chaos, sleep DURATION, restore. Default DURATION=60s.
sudo bash test/perf/chaos/bgp-cut.sh

# Operator-driven: apply, return immediately. The caller (typically the
# Locust/k6 harness or make chaos-matrix) restores at the right moment.
sudo bash test/perf/chaos/bgp-cut.sh --apply

# Restore (idempotent: noop if not applied).
sudo bash test/perf/chaos/bgp-cut.sh --restore
```

`--apply` and `--restore` are pure (no waits, no checks); `--help` prints the per-scenario synopsis.

## Why these specific seven

doc 29 §scenarios A–F cover the canonical Iranian-DC threat model. doc 30 added scenario G (international-egress) after the 2026-04-20 GA4 calibration showed 38% diaspora traffic — losing the international leg without losing the IR-internal leg is now its own degradation mode worth gating against.

The mobile-curfew script is the only one that doesn't manipulate kernel state — it depends on a binary-side feature flag `STATNIVE_CHAOS_DROP_MOBILE_UA_PCT=90` that the handler reads on each request. Operator must rebuild with this flag wired (see PLAN.md Phase 7e § chaos handler integration); without it, the script is a no-op.

## `make chaos-matrix`

```bash
make chaos-matrix STATNIVE_URL=https://load-gate.example.com
```

Runs all seven in series, each for 60 s, with a 30 s recovery window in between. Each scenario's window is captured in `build/chaos-<scenario>-<ts>.log` for postmortem.

The matrix only **applies** chaos; it does NOT drive load itself. Pair it with `make load-gate PHASE=P3` running in another shell — the chaos hits while the load is sustained, and the oracle scan afterwards reveals the per-scenario damage.

## Safety

Every script:

- Refuses to run as non-root (`if [ "$(id -u)" -ne 0 ]; then exit 1; fi`)
- Stamps `/var/run/statnive-chaos-<scenario>.applied` as a sentinel; `--restore` exits 0 if absent
- Sets a `trap '... --restore' EXIT INT TERM` so an operator Ctrl-C always restores
- Logs every command before running it (`set -x` at apply-time only)

**Never run these on a production cluster** — they include `iptables` rules that drop traffic to / from anywhere. The Phase 7e harness machine is a dedicated gate VPS; the production VPS only ever sees the load (not the chaos).
