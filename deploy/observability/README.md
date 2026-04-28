# Phase 7e observability stack

Single-VPS observability stack for the Phase 10 P1–P5 graduation gate. Runs on a separate Asiatech VPS rack from generators + target (doc 29 §3.3 §6.3). Ships:

| Service | Role | License |
|---|---|---|
| Prometheus | Metrics scrape + alerting | Apache-2.0 |
| Grafana | Dashboards (annotations, oracle stats, chaos overlay) | AGPL-3.0 (separate process — not linked) |
| Pyroscope | Continuous CPU + heap profiling | AGPL-3.0 (separate process — not linked) |
| Loki | Log aggregation | AGPL-3.0 (separate process) |
| Vector | PII wire-scan via VRL regex (doc 29 §6.3) | MPL-2.0 |
| Parca | eBPF profiling — backup to Pyroscope | Apache-2.0 |
| Falco | Runtime syscall rules — air-gap burn-in | Apache-2.0 |

CLAUDE.md § License Rules apply to **what's linked into the binary**, not to operator-installed daemons. Pyroscope/Grafana/Loki run out-of-binary and never see the binary's symbol table.

## Bring-up

```bash
cd deploy/observability
# Pin SHA256 digests in docker-compose.observability.yml first; the
# load-gate-harness skill semgrep rule observability-image-pin rejects
# the file if any image still uses ":latest" or an unpinned digest.
docker compose -f docker-compose.observability.yml up -d
```

The compose file deliberately ships zero-byte digest placeholders (`sha256:0000…`) — they're the canonical signal that the operator must pin real digests at deploy time. Skip this and the gate semgrep rule rejects the change.

Internal-registry mirror: each upstream image is also expected to be mirrored to `registry.internal.statnive/`. The mirror config substitutes the registry prefix at deploy via a docker-compose override; the digest stays identical.

## Air-gap burn-in

```bash
# Apply iptables OUTPUT DROP except observability VLAN
sudo iptables -P OUTPUT DROP
sudo iptables -A OUTPUT -d 10.99.0.0/16 -j ACCEPT      # observability VLAN
sudo iptables -A OUTPUT -d 127.0.0.0/8  -j ACCEPT      # loopback

# Burn-in for 10 min — strace must show zero rejected connects outside
# the allow-list while traffic flows.
sudo strace -f -e trace=connect -p $(pgrep statnive-live) -o /tmp/connects.log
```

Falco runs the same assertion continuously while the gate is hot — the `Unexpected outbound connection from statnive-live` rule fires on any non-allowlisted egress.

## Datasources + dashboards

Grafana provisions four datasources (Prometheus / Loki / Pyroscope / Parca) and one dashboard (`load-gate.json`) at startup. The dashboard reads `test_run_id` as a template variable so a single dashboard panel-deck serves every gate run.

## PII wire-scan (doc 29 §6.3)

Vector tails `audit.jsonl` + `alerts.jsonl` from the binary's log directory and runs four VRL regex matches per record (ipv4, ipv6, email, raw user_id). On any positive match the record fans out to (a) Loki at `severity=critical`, (b) Prometheus counter `statnive_pii_leak_total`. The `PIILeakDetected` alert (`alerts.rules.yml`) fires immediately on any increase and halts the gate via the orchestrator's exit-code path.

## Phase 10 follow-ups

- Replace placeholder hosts in `prometheus/prometheus.yml` (`REPLACE_WITH_*`, `REPLACE_AT_PHASE_10_*`) with real Asiatech IPs at procurement.
- Provision Falco kernel module / eBPF probe (kernel ≥5.8 required for modern eBPF; modern Asiatech images ship 6.x).
- Add per-phase Grafana dashboard variants if P3+ surfaces show drift in panel utility.
