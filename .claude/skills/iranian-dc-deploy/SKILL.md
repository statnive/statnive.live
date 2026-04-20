---
name: iranian-dc-deploy
description: MUST USE when reviewing or writing code that touches `deploy/**`, `ops/**`, `infra/**`, DNS zones, TLS cert loading, NTP config, `*http.Client` construction, `internal/license/**`, or systemd unit files. Enforces the Iranian-DC operational contract — no Cloudflare on any IR-resident path (OFAC + no IR POP), no ACME/Let's Encrypt from inside Iran (issue outside, rsync PEM inward, SIGHUP swap), NTP synced to Iranian sources only with >60s skew alert, vendored deps only (`GOFLAGS=-mod=vendor`), Ed25519 offline license verify with zero `net.Dial`, file-sink NDJSON alerts during blackout, `.ir` + `.ایران` IDN bundle registered alongside `.live`. Blocks the first Filimo-destined PR that would fail a `blackout-sim` CI run under `iptables OUTPUT DROP`.
license: proprietary
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 8
  research: "jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md §Gap 2"
  hard_gate: "Weeks 21–24 Filimo cutover"
---

# iranian-dc-deploy

Encodes **CLAUDE.md § Isolation / Air-Gapped Capability** + **CLAUDE.md § Security #1 (TLS 1.3 via manual PEM)** for the Iranian DC deployment surface. National Information Network (NIN) blackout events (2019, 2026-01-08) confirm this is not a hypothetical — intl connectivity sat at ~1–4% of baseline for 100+ days during the most recent blackout while Iranian-resident services kept serving Iranian eyeballs. Every pattern in this skill has a blackout-survivability reason.

## When this skill fires

- Any file edit under `deploy/**`, `ops/**`, `infra/**`, `dns/**`, `nsd/**`, `systemd/**`, `cmd/**/main.go`, `internal/license/**`, or `internal/httpx*.go`.
- Any `*http.Client{}` literal or `http.DefaultClient` / `http.DefaultTransport` reference outside `internal/httpx.NewClient`.
- Any YAML / TOML under `**/config/*.{yaml,yml,toml}` that names external endpoints.
- Any new dependency in `go.mod` that pulls in Cloudflare, MaxMind, ArvanCloud, or `acme`-family libraries.

## 17-item blocking checklist (doc 28 §Gap 2)

**Egress gating**
1. No hardcoded `cloudflare.com`, `api.cloudflare.com`, `1.1.1.1`, `1.0.0.1` in production paths. Cloudflare has **no IR POP** + OFAC 31 CFR 560.540(b)(3) excludes commercial web-hosting for Iranian entities. Absolute ban.
2. No `acme-v0[12].api.letsencrypt.org`, `acme.zerossl.com`, `api.buypass.com/acme` in the Iran-resident binary. ACME runs on an outside-Iran `cert-forge` box; PEM rsync'd inward.
3. No `api.github.com`, `goproxy.io`, `proxy.golang.org`, `registry.npmjs.org` in runtime paths. Build-time only, gated by `!config.AirGap`.
4. Every `*http.Client` goes through `internal/httpx.NewClient` with `Timeout ≤ 10s` + AirGap deny rules. `http.DefaultClient` / `DefaultTransport` never used in production.
5. Egress call sites carry `if !config.AirGap { … }` guard OR `// airgap-exception: <reason>` annotation. CI's Semgrep `iran-require-airgap-guard-on-egress` rule blocks unannotated leaks.

**Dependency shape**
6. `go build` uses `GOFLAGS=-mod=vendor`; no `go mod download` at runtime or during air-gap bundle build.
7. `CGO_ENABLED=1` only for the ClickHouse client leg; everything else is pure Go for static linkage.

**Graceful degradation**
8. S3 backup sink degrades to file-writer when network unreachable (`s3.*(unreachable|timeout).*continuing` in log). Never `log.Fatal` on network failure.
9. Alerts write NDJSON to `/var/log/statnive/alerts.ndjson` during blackout; Slack/PagerDuty optional and gated `if !config.AirGap`. Outside-Iran ops box tails via rsync-over-SSH when connectivity restored.

**Time**
10. `chrony.conf` syncs to ≥2 of `time.asiatech.ir`, `ntp.nic.ir`, `ntp.aut.ac.ir`, `0-3.ir.pool.ntp.org`. Never `time.google.com`, `time.cloudflare.com`, `time.apple.com`, `pool.ntp.org`. `assertClockHealth()` pages on >60s skew.
11. Identity salt `YYYY-MM-DD IRST` → correct clock is load-bearing for dedup. Clock-skew integration test (`faketime 2026-04-20T23:59:58+03:30`) asserts midnight-roll at IRST not UTC.

**DNS + TLS**
12. `statnive.live` CAA record locks issuance to `letsencrypt.org` + `sectigo.com` with `issuewild ";"`. Fallback CAs: ZeroSSL (Sectigo-backed) + Buypass Go SSL (Norwegian, sanctions-neutral). Iranian CAs (Shenasa, SinaCert) not in Mozilla/CCADB — internal-only trust.
13. NSD on AT-VPS-B1 uses `hmac-sha256` TSIG for AXFR-in from Hetzner hidden-primary. ClouDNS as AXFR primary (Bunny likely does not support AXFR-out).
14. `.ir` defensive domain registered at IRNIC (via Pars.ir or Gandi EUR €80/yr — **US persons cannot register `.ir` per Gandi T&Cs**); DS record published; `.ایران` IDN bundled. IRNIC nameservers live inside NIN so `.ir` resolves during blackouts.

**License + systemd**
15. License verification path has zero `net.Dial*` / `http.Get` / `grpc.Dial`. Offline Ed25519 JWT only, `//go:embed statnive-licensing.pub` at build time. Signing key on offline YubiKey in non-US, non-Iran jurisdiction.
16. Go systemd unit carries `NoNewPrivileges=yes`, `ProtectSystem=strict`, `PrivateTmp=yes`, `CapabilityBoundingSet=CAP_NET_BIND_SERVICE`, `ExecReload=/bin/kill -HUP $MAINPID`.
17. Provider choice: Asiatech (AS43754) primary, ParsPack OR Shatel backup. **Never ArvanCloud** — sanctioned + 2022 breach. Cross-provider failover via low-TTL DNS at AT-VPS-B1 NSD, not BGP.

## Verification

`blackout-sim` CI job (GitHub Actions — authored in [references/ci-blackout-sim.yml](references/ci-blackout-sim.yml)) runs the binary under `sudo iptables -P OUTPUT DROP` with loopback + Docker bridge whitelisted, asserts:
- `/health/ready` returns 200 within 30s
- Dashboard loads (`<title>Statnive`)
- 50 sequential `POST /t` ingest requests succeed
- `/api/stats?range=1h` returns ≥50 pageviews (WAL → rollup works offline)
- S3 backup sink logs `s3.*(unreachable|timeout).*continuing|degraded mode`
- `alerts.ndjson` non-empty; no `slack|pagerduty|opsgenie` mentions

Semgrep rules in [semgrep/rules.yaml](semgrep/rules.yaml) cover the 7 egress-gating patterns (`iran-no-cloudflare`, `iran-no-letsencrypt-in-binary`, `iran-no-github-api-at-runtime`, `iran-http-client-must-have-timeout-and-airgap-gate`, `iran-require-airgap-guard-on-egress`, `iran-license-verify-must-be-offline`, `iran-no-hardcoded-non-iran-ntp`).

## Scaffold status

Frontmatter + checklist + Semgrep skeleton shipped in this commit. Full Semgrep rule bodies + CI YAML + NSD config samples + test fixtures land in **Phase 8 (Weeks 17–18)** per doc 28 §Full-optimization-roadmap — the first skill to ship because every other Filimo-destined PR gates against it.