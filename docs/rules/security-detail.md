# Security rule detail (reference)

> Extended operational detail for [CLAUDE.md § Security](../../CLAUDE.md#security-12-features-in-v1-2-phase-1011-deferred). The 14 rules themselves (12 v1 + 2 Phase 10/11 deferred) stay inline in CLAUDE.md; this file holds the reference tables (fallback CAs, systemd hardening options, LUKS reasoning) that agents need only when provisioning new infrastructure or triaging a deployment mismatch.

## Rule 1 — TLS 1.3 via manual PEM files

**Deployment modes:**

| Mode | Cert source | Renewal |
|---|---|---|
| Hetzner / SaaS | Let's Encrypt via `certbot` cron on the host | OS-side cron; binary reads PEMs + SIGHUP |
| Iranian DC | `cert-forge` outside-Iran ACME box → rsync PEM inward | Manual; no ACME from inside Iran |
| Enterprise | Customer root CA signed by customer infra | Customer-managed; operator drops PEM + SIGHUP |

**Fallback CAs (when primary is unavailable or geoblocked):**

| Primary | Fallback | Notes |
|---|---|---|
| Let's Encrypt | ZeroSSL (Sectigo-backed) | Same chain root trust |
| Let's Encrypt | Buypass Go SSL (Norwegian) | Sanctions-neutral issuer |
| Sectigo | ZeroSSL | Same parent — drop-in |

**Iranian CAs NOT in Mozilla/CCADB.** Shenasa and SinaCert are internal-trust only — not accepted by browsers. Cannot use for public-facing `statnive.live`.

**CAA record locks:** `statnive.live` CAA = `issue "letsencrypt.org"`, `issue "sectigo.com"`, `issuewild ";"`. Prevents unauthorized issuance.

## Rule 9 — Disk encryption (LUKS optional)

**Why optional, not mandatory:** LUKS adds 40–50% I/O overhead on the ClickHouse write path (measured on ext4/LUKS1 with AES-XTS-512). At 7 K EPS sustained ingestion, this translates to 3–5 K EPS effective ceiling — a hard regression.

**When to enable:**
- Cloud VPS deployments where the underlying disk is shared with other tenants (Hetzner, Asiatech shared pool).
- Laptop / dev workstation where the disk leaves the physical premises.

**When to skip:**
- Dedicated hardware in a locked cage with encrypted backups — physical security + encrypted snapshots cover the same threat model without the I/O hit.
- Iranian DC deployments where the DC operator has physical control and encrypted `clickhouse-backup` archives are shipped off-box.

**Mandatory replacement:** encrypted `clickhouse-backup` + `age` with zstd + 30-day retention + restore-drill on every release. The backup path MUST be encrypted even when the live disk is not.

## Rule 12 — systemd hardening (full option list)

Canonical unit file for production:

```ini
[Service]
ExecStart=/usr/local/bin/statnive-live -c /etc/statnive-live/config.yaml
ExecReload=/bin/kill -HUP $MAINPID
User=statnive
Group=statnive
# Filesystem isolation
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ReadWritePaths=/var/lib/statnive-live /var/log/statnive-live
# Capability gate
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
# Network isolation
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
# Namespace isolation
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
# Syscall gate
SystemCallFilter=@system-service
SystemCallErrorNumber=EPERM
Restart=always
RestartSec=5
```

**Why each option:**
- `NoNewPrivileges` — child processes can't escalate (blocks SUID exploits).
- `ProtectSystem=strict` + `ReadWritePaths` — read-only filesystem except for data dir and log dir.
- `PrivateTmp` — private `/tmp` per unit (blocks `/tmp/.X11-unix` + tmpfile attacks).
- `CapabilityBoundingSet=CAP_NET_BIND_SERVICE` — only the capability to bind ports < 1024; everything else dropped.
- `RestrictAddressFamilies` — no raw sockets, no `AF_NETLINK`.
- `SystemCallFilter=@system-service` — restricts to the systemd-defined system-service allowlist.

**Tests:**
- `systemd-analyze security statnive-live.service` should report rating ≤ 1.5 ("OK, safe to deploy").
- `deploy/systemd/harden-verify.sh` compares the unit file against this canonical list.

## Deferred to Phase 10 / Phase 11

### Rule 13 — CGNAT-aware rate-limit tiering (Phase 10 cutover gate)

**Status:** Deferred to Phase 10 SamplePlatform cutover. v1's `internal/ratelimit/` has basic per-IP + X-Forwarded-For only; no ASN lookup code in the binary. The design below is the Phase 10 target spec.

Target detail lives in the skill spec and (future) `internal/ratelimit/asn.go`:
- [`.claude/skills/ratelimit-tuning-review`](../../.claude/skills/ratelimit-tuning-review/README.md) — 10-item enforcement checklist; **enforced only when `PHASE_10_GATE=1` env var is set**. Advisory in v1.
- `iptoasn.com/data/ip2asn-v4.tsv.gz` — operator-downloaded monthly; hourly-reload via file-mtime check.

**Iranian ASNs targeted for CGNAT treatment (Phase 10):**
- AS44244 — Irancell (mobile)
- AS197207 — MCI (mobile)
- AS57218 — RighTel (mobile)
- AS31549 — Shatel (fixed — residential fiber with CGNAT at peak)
- AS43754 — Asiatech (business — may carry SamplePlatform employees behind NAT)

### Rule 14 — Outbound allow-list / SSRF guard (Phase 11 gate)

**Status:** Deferred to Phase 11 (when the first opt-in outbound feature ships — ACME, Polar.sh, IP2Location DB23 download, etc.). v1 ships zero outbound paths; `config.outbound.allowlist: []` is the air-gap default. The file `internal/httpclient/guarded.go` does NOT exist in v1 and is intentionally absent — there is nothing for it to guard.

When the first opt-in outbound path lands, the guard MUST be wired before the path is mergeable. Enforcement: [`air-gap-validator`](../../.claude/skills/air-gap-validator/README.md) Semgrep rule `airgap-no-raw-httpclient`.

## Cross-references

- [`CLAUDE.md § Security`](../../CLAUDE.md#security-12-features-in-v1-2-phase-1011-deferred) — rule list (12 v1 + 2 deferred)
- [`docs/tooling.md § Doc 28 additions`](../tooling.md) — skill-roster evolution
- [`.claude/skills/iranian-dc-deploy`](../../.claude/skills/iranian-dc-deploy/README.md) — blackout-sim CI gate
