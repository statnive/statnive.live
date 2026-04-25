# iranian-dc-deploy

Full spec for the Iranian-DC deployment guardrail. Research anchors: [`jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md`](../../../../jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md) §Gap 2 (lines 336–577).

> **⚠️ DNS architecture update (2026-04-25 — Architecture C).** The DNS sections below (§ Architecture / DNS, the `statnive.live` zone file with three-NS mix, the AXFR fan-out, the "register both `statnive.live` + `statnive.ir`" line, the README references to ClouDNS / Bunny / hidden-primary) describe **Architecture B** from doc 26 § 3.2. The SamplePlatform deployment uses **Architecture C (dual-domain, disjoint customer sets)** instead — see `PLAN.md` §§ Domains / Phase 10 / Launch Sequence Phase B + doc 26 § 3.3a carve-out. Substitutions for Architecture C:
>
> - **`statnive.live`** zone is international-only, Bunny or Cloudflare DNS, → Netcup origin. No Iranian-side NS for this zone, no AXFR fan-out.
> - **`statnive.ir`** zone is Iranian-only, single self-hosted NSD primary on AT-VPS-B1 (no AXFR-in, no hidden-primary). Cloudflare ban absolute on this zone (`iran-no-cloudflare`). Tracker URL `https://SamplePlatform.statnive.ir/tracker.js` hardcoded at install time.
> - **Cloudflare** is now permitted on the `.live` zone only. The categorical ban below applies to the `.ir` zone and any Iranian-resident traffic, not to the international SaaS.
>
> Non-DNS items in this README (TLS issue-outside-deploy-inside, NTP to Iranian sources, offline Ed25519 JWT, Asiatech provider posture, ArvanCloud ban, blackout-sim CI) all remain canonical regardless of Architecture B vs C.

## Why this skill exists

**Iran is not a normal deployment surface.** Three constraints stack:

1. **National Information Network (NIN / شبکۀ ملی اطلاعات).** BGP-controlled domestic backbone with a single government gateway to the global Internet. Activated in full during the 2019 blackout and again from **2026-01-08** (day 103+ as of doc 28 authoring date 2026-04-20). During blackouts, international connectivity falls to ~1–4% of baseline (NetBlocks, Al Jazeera 2026-04-05, IEEE Spectrum). Internal-only connectivity means Iranian eyeballs still reach Iranian DCs.
2. **OFAC 31 CFR 560.540(b)(3).** Explicitly excludes commercial web-hosting for Iranian entities from the general license for personal communications. A SamplePlatform-bought analytics platform verified by offline Ed25519 JWT and deployed by Iranian operators to Asiatech sits outside OFAC direct reach *if no US person touches licensing issuance, deployment, or funds flow*.
3. **Cloudflare categorically unusable.** No IR POP per OFAC + Matthew Prince public confirmation. Not for DNS, TLS, DDoS, or KV. The Semgrep `iran-no-cloudflare` rule is absolute.

The skill encodes the operational patterns that survive all three constraints simultaneously.

## Architecture

### Provider + ASN posture

| Provider | ASN | Role | Notes |
|---|---|---|---|
| Asiatech | AS43754 | **Primary** | 85 peers, 83 downstreams, Milad Tower DC, default upstream AS12880 TIC |
| ParsPack | — | Backup | Multi-DC IR + DE/NL/UK, Toman + crypto payment, 99.99% SLA, 50GB free FTP backup on VPS tier |
| Shatel | AS31549 | Backup (alt) | Cisco/Volvo/Eaton-built colo, 24/7 NOC |
| Afranet | AS25184 | Backup (alt) | |
| **ArvanCloud** | AS202468 | **BLACKLISTED** | Sanctioned + 2022 breach — absolute ban |

All IR providers peer at **IXP.ir**, keeping intra-Iran latency under 15ms. Cross-provider failover is via low-TTL DNS at AT-VPS-B1 NSD, **not BGP** (simpler, blackout-time-safe).

Pricing (UNCONFIRMED, must quote at checkout — Asiatech login-gated at `cloud.ir` / `my.asiatech.ir`):
- AT-VPS-B1 ~1.5–2.5M IRR (~$18–30)
- AT-VPS-G2 8c/16GB ~16–22M IRR (~$190–260)
- AT-VPS-A1 8c/32GB ~28–36M IRR (~$330–425)
- Dedicated 8c/32GB/1TB NVMe ~55–90M IRR (~$650–1050)

### DNS: hidden-primary NSD + AXFR fan-out

**Outside Iran (Hetzner hidden-primary NSD)** → AXFR+TSIG to:
- **ClouDNS** (native AXFR-out confirmed; Premium S ~$3/mo, DDoS Protected ~$12.95/mo)
- **AT-VPS-B1 NSD secondary** in Tehran (185.88.153.10, inside NIN)
- **Bunny DNS** as third NS via BIND-file import from CI (Bunny AXFR-out is likely unsupported — verify)

Zone file `statnive.live` (canonical lines — full version in [references/dns-zone.md](references/dns-zone.md)):

```zone
$TTL 300
@   IN SOA ns-hidden.statnive.live. hostmaster.statnive.live. ( 2026042001 3600 600 1209600 300 )
@   IN NS   ns1.bunny.net.                ; outside anycast
@   IN NS   pns31.cloudns.net.            ; outside AXFR source (Premium)
@   IN NS   ns-tehran.statnive.live.      ; inside NIN — AT-VPS-B1
ns-tehran IN A 185.88.153.10              ; glue, AT-VPS-B1 public v4
@   IN CAA  0 issue "letsencrypt.org"
@   IN CAA  0 issue "sectigo.com"
@   IN CAA  0 issuewild ";"
@   IN CAA  0 iodef "mailto:secops@statnive.live"
```

Register **both `statnive.live` + `statnive.ir` + `.ایران` IDN bundle**. IRNIC nameservers (`a.irnic.ir` etc.) live inside NIN, so `.ir` resolution works during blackouts even when `.live` glue might be stale.

### TLS: issue outside, deploy inside

ACME runs on an outside-Iran Hetzner `cert-forge` box (DNS-01 against the outside hidden-primary — no Iran-side dependency). PEM rsync'd inward during calm weather, atomic rename + SIGHUP on the Go binary. Fallback CAs: **ZeroSSL** (Sectigo-backed), **Buypass Go SSL** (Norwegian, sanctions-neutral). Iranian CAs (Shenasa, SinaCert) not in Mozilla/CCADB — internal-only trust.

### NTP: Iranian sources only

`chrony.conf` syncs to `time.asiatech.ir`, `ntp.nic.ir`, `ntp.aut.ac.ir`, `0-3.ir.pool.ntp.org`. Load-bearing because **identity salt is keyed on `YYYY-MM-DD` in IRST (UTC+03:30)** — a >1 day skew hashes the same user to two daily-identity buckets and breaks dedup rollups. `assertClockHealth()` pages on >60s skew.

External NTP (`time.google.com`, `time.cloudflare.com`, `pool.ntp.org`) is blackout-unreachable. Semgrep rule `iran-no-hardcoded-non-iran-ntp` blocks them.

### Licensing: offline Ed25519 JWT

Generator runs on outside-Iran air-gapped workstation: reads PEM private key from **offline YubiKey** (`age-plugin-yubikey`) in **non-US, non-Iran jurisdiction**, issues `EdDSA` JWT with claims `{iss, sub, aud, iat, exp, tier, max_eps, air_gap, fp, nonce}`.

Verifier inside binary uses `//go:embed statnive-licensing.pub` + `ed25519.Verify` with **zero network calls** — enforced by Semgrep rule `iran-license-verify-must-be-offline`. Phone-home telemetry = "services rendered" under OFAC interpretation, which `560.540(b)(3)` excludes. Absolute ban.

### Go build constraints

- `GOFLAGS=-mod=vendor` — no `go mod download` at build time inside Iran.
- `CGO_ENABLED=1` only for ClickHouse client leg; everything else static.
- `-tags airgap` for the IR-resident binary to dead-code-eliminate optional network-touching features.
- `-trimpath -ldflags '-s -w'` for reproducible builds.

### Alerting degradation

Baseline: file-sink NDJSON at `/var/log/statnive/alerts.ndjson`. Slack/PagerDuty/Opsgenie optional and gated `if !config.AirGap`. Outside-Iran ops box tails via rsync-over-SSH when connectivity restored.

### Iranian mirror landscape

- **IUT** (repo.iut.ac.ir) — most comprehensive academic mirror.
- Asiatech, AUT, NIC.ir operate mirrors with UNCONFIRMED current coverage.
- **MiravaOrg/Mirava** — live health-check scripts (license UNCONFIRMED; verify before bundling).
- `goproxy.cn` often reachable but unreliable during blackout.
- `proxy.golang.org` unreachable from IR since 2019.

**Vendoring is the only deterministic strategy.** Mirrors are backup only.

## Semgrep rule summary

7 rules in [semgrep/rules.yaml](semgrep/rules.yaml) (body lifted verbatim from doc 28 lines 392–461):

| Rule ID | Severity | What it blocks |
|---|---|---|
| `iran-no-cloudflare` | ERROR | Cloudflare hostnames + 1.1.1.1 / 1.0.0.1 in production paths |
| `iran-no-letsencrypt-in-binary` | ERROR | ACME endpoints in IR-resident binary (move to `ops/cert-forge/**`) |
| `iran-no-github-api-at-runtime` | WARNING | `api.github.com`, `goproxy.io`, `proxy.golang.org`, `registry.npmjs.org` at runtime |
| `iran-http-client-must-have-timeout-and-airgap-gate` | ERROR | `*http.Client` outside `internal/httpx.NewClient` |
| `iran-require-airgap-guard-on-egress` | ERROR | Egress without `if !config.AirGap` guard or `// airgap-exception:` annotation |
| `iran-license-verify-must-be-offline` | ERROR | `net.Dial`, `http.Get`, `grpc.Dial` in `internal/license/**` |
| `iran-no-hardcoded-non-iran-ntp` | WARNING | Non-Iranian NTP servers |

## CI gate

Two jobs in `.github/workflows/blackout-sim.yml` (YAML body in [references/ci-blackout-sim.yml](references/ci-blackout-sim.yml)):

- **blackout-sim** — builds vendored airgap binary, starts ClickHouse on 127.0.0.1, installs `iptables -P OUTPUT DROP` rules (whitelisting loopback + Docker bridge only), starts statnive, curls `/health/ready`, dashboard, tracker POST, `/api/stats`, asserts S3 fails gracefully + alerts file-sink only.
- **semgrep** — runs the 7 rules on every PR touching listed globs.

## Chaos / integration test matrix

- **15-min sustained 7K EPS via vegeta** from a second Asiatech VPS inside IXP.ir, with `iptables OUTPUT DROP` + allow only Iranian CIDRs from RIPE (AS43754, AS31549, AS25184, ParsPack blocks). Assert: no ingest loss (WAL buffers), S3 fails silently, alerts file-only.
- **DNS blackout test.** `dig @5.202.100.100 +tcp statnive.live SOA` with non-Iranian CIDRs blocked — assert serial matches hidden-primary's latest (proves AXFR reached AT-VPS-B1). `dig DS statnive.ir @a.irnic.ir`.
- **TLS rotation mid-connection.** Keep `openssl s_client -reconnect` open, swap PEM atomically + SIGHUP — assert next handshake shows new `notAfter`, existing connections survive.
- **Persian UA + Jalali render.** `curl -H 'User-Agent: Mozilla/5.0 (Linux) فیلیمو/3.2 Android/13' -H 'Accept-Language: fa-IR'` — assert UA parsed, `tz=Asia/Tehran`, Jalali (شمسی) date in dashboard.
- **Clock-skew midnight roll.** `faketime 2026-04-20T23:59:58+03:30` + 1000 events spanning midnight — assert dedup bucket rolls at IRST midnight, not UTC.

## Opinionated defaults (6)

1. **Asiatech primary, ParsPack or Shatel backup — single-provider-primary, never ArvanCloud.** Simpler blackout-time failover via low-TTL DNS at AT-VPS-B1 NSD than cross-provider BGP.
2. **NSD not BIND** on both hidden-primary and Tehran secondary. Authoritative-only, smaller attack surface, native AXFR-in.
3. **Ed25519 not RSA not P-256** for license JWTs. 32-byte keys, deterministic, `crypto/ed25519` stdlib, `EdDSA` JOSE interop.
4. **Vendored deps, `GOFLAGS=-mod=vendor`, `CGO_ENABLED=1` only for ClickHouse.** No `go mod download` at build time inside Iran.
5. **File-sink NDJSON alerting as baseline; Slack/PagerDuty optional, gated `if !config.AirGap`.** Outside-Iran ops box tails via rsync-over-SSH when connectivity restored.
6. **Register `.live` + `.ir` + `.ایران` IDN bundle.** IRNIC serves inside NIN — `.ir` resolves during blackouts.

## Remaining uncertainties

- **Asiatech IRR pricing** — login-gated, must quote at checkout.
- **Bunny DNS AXFR-out** — likely not supported; ClouDNS as AXFR primary instead.
- **MiravaOrg/Mirava license** — unconfirmed; wrap functionality in-house if not permissive.
- **Ed25519 key custody jurisdiction** — operator decision; must be non-US AND non-Iran per OFAC posture.
- **`.ir` registrar choice** — Pars.ir (IRR) vs Gandi (€80/yr EUR). US persons cannot use Gandi per T&Cs.

## Research anchors

- Doc 28 §Gap 2 (lines 336–577) — full spec, Semgrep bodies, config samples, CI YAML.
- Doc 27 §Gap 2 — companion CGNAT rate-limit research (already live as `ratelimit-tuning-review` skill).
- CLAUDE.md § Isolation / Air-Gapped Capability — product contract this skill enforces.