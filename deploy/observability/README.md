# Observability stack (Phase 7e)

Six-service stack that runs on a **separate** outside-Iran VPS (Hetzner CX21, ~€7/mo) and scrapes the production statnive-live binary's `/metrics` endpoint over an SSH tunnel. Lives outside the Iranian-DC blast radius so that an Asiatech outage doesn't take the observability with it; talks to production over a single SSH-forwarded port.

```
┌──────────────────────────────────────────┐         ┌──────────────────────────────────┐
│ Hetzner CX21 (observability)             │   ssh   │ Asiatech AT-VPS-X (production)   │
│   prometheus ─────► localhost:9090       │ tunnel  │   statnive-live :8080            │
│   grafana    :3000 ◄── operator browser  │ ──────► │     /metrics (token-gated)       │
│   pyroscope  :4040                       │  fwd    │   ClickHouse :9000               │
│   vector.dev (log aggregator)            │         │   Node Exporter :9100            │
│   parca (continuous profiler)            │         │ (no inbound from observability)  │
│   falco (runtime security)               │         │                                  │
└──────────────────────────────────────────┘         └──────────────────────────────────┘
```

## What runs

| Service | Port | Purpose |
|---------|------|---------|
| Prometheus | 9090 | Pulls binary's `/metrics`, CH-exporter, node-exporter. ~7-day retention. |
| Grafana | 3000 | 4 provisioned dashboards (overview / WAL / CH / chaos-during-gate). |
| Pyroscope | 4040 | Continuous CPU + heap profiling for the Go binary. |
| Vector.dev | 8686 | Tails `/var/log/statnive-live/{audit,alerts}.jsonl`, ships to S3 long-term. |
| Parca | 7070 | Always-on profiling (complements Pyroscope; we keep both for cross-check). |
| Falco | 5060 | eBPF-based runtime security; alerts on unexpected syscalls / process exec. |

## First-time setup

```bash
# 1. Provision the Hetzner CX21 (Debian 12, 4 GB RAM, 80 GB disk)
ssh root@obs-cert-forge.example.com 'apt-get update && apt-get install -y docker.io docker-compose-plugin'

# 2. Clone this directory onto the box
git clone --depth 1 https://github.com/statnive/statnive.live /opt/obs-source
sudo install -d -m 0750 /etc/observability
sudo cp -r /opt/obs-source/deploy/observability/* /etc/observability/

# 3. Wire the SSH tunnel to production (autossh keeps it up)
sudo apt-get install -y autossh
sudo tee /etc/systemd/system/statnive-tunnel.service >/dev/null <<-EOF
[Unit]
Description=SSH tunnel to statnive-live /metrics
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/bin/autossh -M 0 -N \\
  -o "ServerAliveInterval=30" \\
  -o "ServerAliveCountMax=3" \\
  -o "ExitOnForwardFailure=yes" \\
  -L 127.0.0.1:18080:127.0.0.1:8080 \\
  -L 127.0.0.1:19100:127.0.0.1:9100 \\
  -L 127.0.0.1:19000:127.0.0.1:9000 \\
  -i /etc/observability/ssh/id_ed25519 \\
  root@<asiatech-host>

Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable --now statnive-tunnel

# 4. Set the metrics scrape token
echo "STATNIVE_METRICS_TOKEN=$(sudo ssh root@<asiatech-host> 'grep METRICS_TOKEN /etc/systemd/system/statnive-live.service.d/env.conf' | cut -d= -f2 | tr -d '\"')" \
  | sudo tee /etc/observability/.env

# 5. Boot the stack
cd /etc/observability && sudo docker compose up -d

# 6. Grafana initial admin password (rotate immediately)
sudo docker logs obs-grafana 2>&1 | grep "Admin password"

# Open https://obs-cert-forge.example.com:3000/ — dashboards auto-provisioned.
```

## Dashboards (provisioned JSON in `grafana/dashboards/`)

| File | Purpose | Key KPIs |
|------|---------|----------|
| `overview.json` | Top-level binary + ingest health | EPS in/out, WAL size, CH lag, /healthz status, alert count |
| `wal.json` | WAL deep-dive | bytes/sec write, fsync latency, replay rate, drop-oldest rate |
| `clickhouse.json` | CH cluster + insert health | active parts, merge backlog, async-insert queue, replication lag |
| `chaos-during-gate.json` | Live view during chaos-matrix runs | Per-scenario annotation overlays + SLO threshold lines |

## When to look at what

- **Locust / k6 SLO breach** → `overview.json` (request EPS / p99) + `wal.json` (back-pressure)
- **CH insert errors during P3+** → `clickhouse.json` (active parts > 600 = merge starvation)
- **Chaos matrix damage** → `chaos-during-gate.json` (each scenario overlayed)
- **Memory leak suspected** → Pyroscope direct (http://obs-cert-forge:4040/) — heap diff between t=0 and t=72h on the soak run

## Costs + sizing

| Component | Disk | RAM | Notes |
|-----------|------|-----|-------|
| Prometheus | 30 GB / 7 days | 1 GB | bump to 14 days for soak runs |
| Pyroscope | 20 GB / 14 days | 1 GB | continuous profiling is expensive — pin retention low |
| Grafana | 1 GB | 256 MB | dashboards + datasource config only |
| Vector | 5 GB buffer | 256 MB | streams to S3, doesn't store locally |
| Parca | 10 GB / 7 days | 512 MB | overlaps with Pyroscope — keep both during Phase 7e for cross-check |
| Falco | 1 GB | 256 MB | alerts to stdout → Vector → S3 |

Total: ~70 GB disk / ~3.5 GB RAM. A Hetzner CX21 (4 vCPU, 8 GB RAM, 80 GB disk) at €6.99/mo covers it comfortably.

## Hardening

- The SSH key under `/etc/observability/ssh/id_ed25519` only authorizes `command="echo no shell"` from the bastion in `/root/.ssh/authorized_keys` on Asiatech — port-forward only. A compromised observability box can read metrics; it cannot run commands on prod.
- Grafana behind nginx + LE cert; basic auth + IP allowlist for `/login`.
- Prometheus + Pyroscope + Parca have no public listener; only the operator's tunnel reaches them.
- Vector ships logs to an age-encrypted S3 bucket (compose's `vector.toml` sets `VECTOR_S3_KMS_KEY`).
