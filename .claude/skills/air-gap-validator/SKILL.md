---
name: air-gap-validator
description: MUST USE before adding a Go dependency, introducing network code, or embedding frontend/tracker assets. Enforces the statnive-live Isolation rule — the binary MUST run with zero required outbound connections under `iptables -P OUTPUT DROP`. Rejects new deps that do DNS/HTTP at runtime, CDN imports in web/ or tracker/, telemetry calls, and font/script URLs pointing outside the embedded bundle.
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 0
  research: "jaan-to/docs/research/25-ai-claude-skills-filimo-grade-analytics-platform.md §gap-analysis #2; CLAUDE.md §Isolation; PLAN.md §Air-Gapped / Isolated Deployment"
---

# air-gap-validator

Encodes the **CLAUDE.md Isolation** block (lines 69-86) and the **PLAN.md Air-Gapped / Isolated Deployment** section. Every network-touching feature in statnive-live must be optional and config-gated; the release gate runs the binary under `iptables -P OUTPUT DROP` and asserts all paths still work.

## When this skill fires

- `go get` / new line in `go.mod` / new import in any `.go` file that includes a network package (`net`, `net/http`, `net/url` with external hosts, `crypto/tls` with remote endpoints, `dns`, etc.).
- Any `.go` file containing `http.Get`, `http.Post`, `net.Dial`, `net.Resolver.*`, `tls.Dial`, `net/url.Parse` of a non-loopback URL, or `os/exec` invoking a network tool.
- Any `web/` or `tracker/` change that introduces `<link>` or `<script>` tags with external hosts, `@import url('https://...')` in CSS, `fetch('https://...')` / `new URL('https://...')` in JS/TS.
- Any new config key that defaults a network feature to ON (violates "opt-in only").

## Enforced invariants

1. No direct external-host `http.Get`/`net.Dial` at import, init, or boot time.
2. Any outbound capability is behind a config flag defaulting to `false` (license.phone_home, telegram.enabled, syslog.remote_enabled, etc.).
3. Tracker JS and dashboard SPA are served via `go:embed` — no CDN URLs in `web/index.html`, `tracker/src/**`, or CSS imports.
4. Fonts are self-hosted — no `fonts.googleapis.com`, `fonts.gstatic.com`, `cdn.jsdelivr.net`, `unpkg.com`, `cdnjs.cloudflare.com` strings anywhere in `web/` or `tracker/`.
5. `go.mod` additions must not introduce transitive dependencies that dial out at init (e.g. auto-updaters, telemetry SDKs).
6. Release gate: binary passes `iptables -P OUTPUT DROP` integration test with loopback + configured tracker clients only.

## Should trigger (reject)

```go
// BAD — outbound at init
func init() {
    resp, _ := http.Get("https://auth.statnive.live/publickey")
    embeddedPublicKey = resp.Body
}
```

```html
<!-- BAD — CDN font, CDN JS -->
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Fraunces">
<script src="https://cdn.jsdelivr.net/npm/uplot@1"></script>
```

## Should NOT trigger (allow)

```go
// OK — opt-in, config-gated, default false
if cfg.License.PhoneHome {
    go reportLicenseHeartbeat(ctx, cfg.License.Endpoint)
}
```

```html
<link rel="stylesheet" href="/assets/fraunces.woff2">   <!-- go:embed -->
<script src="/assets/uplot.js"></script>                <!-- go:embed -->
```

## Implementation (TODO — Phase 0)

- `checks/outbound-denylist.yml` — regex list of forbidden host patterns (CDN, GA, Cloudflare Analytics, Plausible, Matomo, Sentry, Telegram unless opt-in).
- `checks/import-denylist.yml` — Go import patterns that pull in HTTP clients at init.
- `scripts/airgap-acceptance.sh` — wraps `iptables -A OUTPUT -j DROP` + `make test-integration` (runs inside an isolated netns on Linux hosts, no-op on macOS dev with a warning).

Full spec + test fixtures: [README.md](README.md).