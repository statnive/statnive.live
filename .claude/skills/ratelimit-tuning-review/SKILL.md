---
name: ratelimit-tuning-review
description: MUST USE when editing `internal/ratelimit/**`, `internal/ingest/handler.go`, imports of `go-chi/httprate` / `x/time/rate`, or main.go middleware wiring. Enforces CGNAT-aware tiering (AS44244/AS197207/AS57218 on `(ip, site_id)` at 1K req/s; 100/s fallback; 25K/s per-site cap), trusted-proxy CIDRs for `middleware.RealIP`, XFF right-to-left parse, audit on every 429, `MaxBytesReader(8<<10)`. ASN via `iptoasn.com` (MaxMind / IPLocate rejected — CC-BY-SA).
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 2
  research: "jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md §Gap 2"
---

# ratelimit-tuning-review

> **Activation gate (Phase 10 SamplePlatform cutover — HARD GATE).** This skill's Semgrep rule bodies and CI wiring are scheduled for Phase 10 (ASN-tiered limiter + `iptoasn.com` TSV loader + k6 scenarios `normal`/`burst`/`ddos`/`cgnat`). Until the corresponding `.github/workflows/ratelimit-gate.yml` is green on main, treat this skill as **advisory-only** — surface the checklist to the reviewer, do not block merges, and flag any mismatch as `activation-pending` rather than auto-fixing.

Encodes **CLAUDE.md Security #5** (rate limiting per IP via `go-chi/httprate`) and the SamplePlatform P5-cutover requirement that 100 req/s per-IP brownouts entire Iranian apartment blocks under CGNAT. PR #6 shipped plain `httprate.LimitByRealIP(100, 1m)` — this skill is the gate that blocks SamplePlatform cutover (Phase 10) without ASN tiering.

## When this skill fires

- Any file under `internal/ratelimit/**`.
- `internal/ingest/handler.go` changes — especially to the IP-resolution ladder or `MaxBytesReader` call.
- Imports of `github.com/go-chi/httprate`, `golang.org/x/time/rate`.
- `cmd/statnive-live/main.go` middleware chain edits.
- Config schema changes under `ratelimit:` or `trusted_proxies:`.

## Enforced invariants — the 10-item checklist (doc 27 §line 58)

1. **`middleware.RealIP` is used ONLY when a trusted-proxy CIDR list is loaded.** Chi issue [#711](https://github.com/go-chi/chi/issues/711): `LimitByRealIP` trusts `True-Client-IP` / `X-Real-IP` / left-XFF by default — spoofable. If the deploy is air-gapped / no CDN, `RealIP` is off and the limiter keys on `r.RemoteAddr`.
2. **XFF parsed right-to-left, discarding any IP not in the trusted chain** — never left-to-right. Use a unique internal header like `X-Statnive-Client-IP` set only by the trusted edge, rejected if present from outside (Radware pattern).
3. **Iranian-ASN tier** configured with compound key `(ip, site_id)` at **1 K req/s sustained, 2 K burst** for AS44244 (Irancell), AS197207 (MCI), AS57218 (RighTel).
4. **Per-`site_id` global cap** at 25 K req/s (well above the 18 K event-peak budget) prevents cross-tenant noisy-neighbor.
5. **429 emits an audit event** containing `{site_id, ip_hash, xff_chain, tier, reason, bucket_state}` (not raw IP — that would violate Privacy Rule 1).
6. **`Retry-After: N` header on every 429 response.**
7. **In-memory counter has TTL eviction** (not unbounded `sync.Map`). Memory-exhaustion via cycled fake IPs is a known DoS vector.
8. **`MaxBytesReader(8<<10)` wraps every ingest body read.** Default `MaxHeaderBytes: 1 MB` is reckless; 8 KB matches the 34-field JSON event of ~900 B typical / 2 KB max.
9. **`http.Server.ReadHeaderTimeout ≤ 5s`, `IdleTimeout: 30s`.**
10. **Observe-only mode flag** (`RATELIMIT_DRY_RUN=true` env var) exists for rollout — logs what *would* be rejected without rejecting.

## Three opinionated defaults this skill encodes

- **(a) Compound-key tiering** not flat per-IP. Iranian CGNAT demands `(ip, site_id)` so a tenant seeing scraper traffic doesn't brownout another tenant on the same egress IP.
- **(b) `iptoasn.com` ASN DB** — public-domain TSV, hourly update. **Ruled out:** MaxMind GeoLite2 (CC-BY-SA — contaminates binary), IPLocate.io (also CC-BY-SA). `iptoasn.com` is the only unencumbered option matching CLAUDE.md License Rules.
- **(c) 5-layer DDoS defense must be explicit and numbered** — L1 OS (`somaxconn=8192`, `ulimit -n 65536`), L2 TLS handshake gate (~500/s), L3 HTTP protections (`ReadHeaderTimeout: 5s`, `MaxHeaderBytes: 16<<10`), L4 httprate tiered, L5 ClickHouse backpressure via WAL.

## CGNAT reality (document, don't assume)

- **AS44244 Irancell** — ~1.3 M IPv4 for tens of millions of subscribers. A single public IPv4 fronts 5 000–10 000 concurrent subscribers at peak.
- **AS197207 MCI** — 828 IPv4 prefixes, no IPv6, entire mobile base.
- **AS57218 RighTel** — smaller but same NAT444 pattern.
- 100 req/s per-IP throttles an entire apartment block the moment two neighbors load the homepage.

## Should trigger (reject)

```go
// BAD — RealIP without trusted-proxy config; attackers spoof True-Client-IP
r.Use(middleware.RealIP)
r.Use(httprate.LimitByRealIP(100, time.Minute))

// BAD — leftmost XFF trusted (attacker-controlled)
ip := strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]

// BAD — flat per-IP, no ASN tier, CGNAT apartment-block brownout
r.Post("/api/event", httprate.LimitByIP(100, time.Second)(ingestHandler))

// BAD — 429 without audit emit; no Retry-After
w.WriteHeader(429)
```

## Should NOT trigger (allow)

```go
// OK — trusted-proxy CIDR list loaded first
trusted := mustLoadCIDRs(cfg.TrustedProxies)
r.Use(middleware.WithValue("trustedProxies", trusted))
r.Use(realIPBehind(trusted))  // custom: right-to-left XFF, trusted-chain discard

// OK — compound-key tiered
tiers := []Tier{
    {ASNs: []uint32{44244, 197207, 57218}, Limit: 1000, Burst: 2000, Key: KeyIPSite},
    {ASNs: nil, Limit: 100, Burst: 200, Key: KeyIP},
}
r.Use(asnTieredLimit(tiers, globalPerSite: 25_000))

// OK — 429 with audit + Retry-After
audit.Emit(audit.RateLimitExceeded, site, ipHash, xffChain, tier, reason)
w.Header().Set("Retry-After", "60")
w.WriteHeader(429)
```

## Implementation (TODO — Phase 2a regression guard; Phase 10 hard gate)

- `semgrep/rule.yml` — the 5 Semgrep rules from doc 27 §line 60:
  1. flag `middleware.RealIP` without a nearby `trustedProxies` config load.
  2. flag direct `r.Header.Get("X-Forwarded-For")` in business logic (must go through sanitizer).
  3. flag `httprate.Limit(` without a `WithKeyFuncs` compound key in ingest handlers.
  4. flag 429 responses without a matching `audit.Emit`.
  5. flag ingest handlers without `http.MaxBytesReader` on `r.Body`.
- `k6/scenarios.json` — seed scenarios from doc 27 §line 54:
  - `normal` (7 K EPS from 10 K IPs — `http_req_failed < 1%`).
  - `burst` (18 K EPS from 5 K IPs).
  - `ddos` (30 K EPS from 50 IPs — MUST 429, `http_req_failed > 50%`).
  - `cgnat` (7 K EPS from 100 IPs simulating Irancell — MUST NOT 429 if ASN tier wired).
- `test/fixtures/` — should-trigger / should-not-trigger Go cases.
- `ip2asn-v4.tsv.gz` — NOT bundled; loader fetches at boot from `config/ip2asn.tsv.gz` (operator-supplied), auto-refresh hourly in long-running deploys.

Full spec + SamplePlatform P5 config: [README.md](README.md).