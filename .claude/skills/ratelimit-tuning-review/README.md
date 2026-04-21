# ratelimit-tuning-review — full spec

## Architecture rule

Encodes **CLAUDE.md Security #5** (rate limiting per IP via `go-chi/httprate`, 100 req/s, burst 200, NAT-aware) plus the doc-27 addition that the plain flat-per-IP config is unsafe for Iranian CGNAT traffic. The skill is the **hard gate on Phase 10 (SamplePlatform Iranian VPS cutover)** — ASN tiering must ship before the first byte of SamplePlatform traffic routes through.

## Research anchors

- [jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md](../../../../jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md) §Gap 2 — primary source.
- [chi issue #711](https://github.com/go-chi/chi/issues/711) — `LimitByRealIP` spoofing risk.
- [chi PR #967](https://github.com/go-chi/chi/pull/967) — VojtechVitek's trusted-prefix `ClientIP` design (not yet merged; we implement the pattern locally).
- [CAIDA, RIPEstat](https://stat.ripe.net/) — CGNAT confirmation for AS44244 / AS197207 / AS57218.

## Implementation phase

- **Phase 2a** (already shipped in PR #6) — skill is a regression guard against any `middleware.RealIP` / XFF-left / flat-per-IP regression.
- **Phase 10 (SamplePlatform cutover)** — **hard gate**. ASN tiering + compound-key + `iptoasn.com` TSV loader must ship before routing the first SamplePlatform pageview.

## SamplePlatform P5 config (calibrated per doc 27 §line 63)

```go
// internal/ratelimit/tier.go
tiers := []Tier{
    {ASNs: []uint32{44244, 197207, 57218}, Limit: 1000, Burst: 2000, Key: KeyIPSite},
    {ASNs: nil /* default */,              Limit: 100,  Burst: 200,  Key: KeyIP},
}
globalPerSite := 25_000  // req/s per site_id
```

## httprate semantics the skill must know

- `go-chi/httprate` implements **sliding-window-counter** (Cloudflare 2017 pattern), not token bucket. The core formula is `rate := prevCount * (windowLength - diff) / windowLength + currCount`.
- "Burst 200" in the current config is NOT a token-bucket burst — it's a 100 req/s rate ceiling over a 1 s window with 100-request headroom baked into the calling code.
- `LimitByRealIP` IP ladder: `r.Header.Get("True-Client-IP")` → `X-Real-IP` → leftmost `X-Forwarded-For` → `r.RemoteAddr`. **This ladder is spoofable** — skill rejects direct use of `LimitByRealIP` without a trusted-proxy CIDR list wrapper.

## 5-layer DDoS defense (all must be wired — doc 27 §line 54)

| Layer | Mechanism |
|---|---|
| L1 | OS: `net.core.somaxconn=8192`, `ulimit -n 65536` |
| L2 | TLS handshake cap: `golang.org/x/time/rate` gate on `tls.Config.GetClientHelloInfo`, ~500/s |
| L3 | HTTP: `http.Server{ReadHeaderTimeout: 5s, IdleTimeout: 30s, MaxHeaderBytes: 16 << 10}`, `MaxBytesReader(8<<10)` on body |
| L4 | httprate tiered (this skill's core) |
| L5 | ClickHouse backpressure via WAL — drops to 503 `Retry-After: 60` when WAL > 80% |

The skill verifies all 5 layers are present in the main.go middleware chain + systemd unit + ClickHouse config.

## ASN-DB licensing decision

| Source | License | Verdict |
|---|---|---|
| MaxMind GeoLite2 (MMDB) | CC-BY-SA-4.0 | ❌ Rejected — share-alike contaminates binary |
| IPLocate.io free DB | CC-BY-SA | ❌ Rejected — same issue |
| **iptoasn.com `ip2asn-v4.tsv.gz`** | Public domain | ✅ **Adopted** — hourly refresh, TSV loader, `(range_start, range_end, asn, country, description)` |

The iptoasn.com file ships hourly. Operator refreshes via cron on long-running deploys; boot-time load happens from `config/ip2asn.tsv.gz`. On air-gapped deploys, the operator SCPs the file and the skill does not require outbound — matches the Isolation rule.

## Trusted-proxy CIDR pattern (skill enforces)

Outside an edge CDN (Arvan Cloud / Bunny CDN, Phase 10+):
- `middleware.RealIP` is **off**.
- Limiter keys on `r.RemoteAddr`.

Behind a trusted edge:
- Load the edge's published CIDR list at boot (Arvan publishes edge ranges; Bunny's IP list at bunny.net/pullzone).
- `middleware.RealIP` is **on**, but reads from a **custom sanitizer** that walks `X-Forwarded-For` right-to-left, discarding any IP not in the trusted CIDR list, stopping at the first untrusted hop.
- A unique internal header `X-Statnive-Client-IP` set only by the trusted edge (rejected if present from outside) is the definitive client IP — this eliminates the spoofing class entirely.

## Files

- `semgrep/rule.yml` — TODO: 5 Semgrep rules.
- `k6/scenarios.json` — TODO: `normal` / `burst` / `ddos` / `cgnat` scenarios with assertions.
- `test/fixtures/` — TODO: should-trigger / should-not-trigger Go cases.

## CI integration (TODO)

```makefile
ratelimit-semgrep:
    semgrep --config=.claude/skills/ratelimit-tuning-review/semgrep internal/ cmd/

ratelimit-k6:
    k6 run .claude/skills/ratelimit-tuning-review/k6/scenarios.json

release-gate-phase-10: lint test test-integration ratelimit-semgrep ratelimit-k6
```

## Pairs with

- `golang-security` (community) — general input validation.
- `golang-observability` (community) — audit emit + Prometheus counter per-tier / per-reason 429.
- `air-gap-validator` (custom) — asserts no outbound ASN lookup at runtime (skill's reason iptoasn.com ships as a file drop, not an HTTP fetch).

## Scope

- `internal/ratelimit/**`, `internal/ingest/handler.go`, `cmd/statnive-live/main.go` middleware chain.
- Config schema (`ratelimit:`, `trusted_proxies:` blocks).
- Does **not** apply to dashboard `/api/stats/*` rate limiting — that's Phase 3b bearer-token rate limit, different concerns (per-tenant quota, not CGNAT).