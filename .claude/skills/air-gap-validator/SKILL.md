---
name: air-gap-validator
description: MUST USE before adding a Go dep, introducing network code, or embedding frontend/tracker assets. Enforces Isolation — binary runs under `iptables -P OUTPUT DROP` with zero required outbound. Rejects runtime DNS/HTTP, CDN imports in `web/`/`tracker/`, telemetry, external font/script URLs. Full checklist in body.
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 0
  research: "jaan-to/docs/research/25-ai-claude-skills-SamplePlatform-grade-analytics-platform.md §gap-analysis #2; CLAUDE.md §Isolation; PLAN.md §Air-Gapped / Isolated Deployment"
---

# air-gap-validator

> **Activation gate (Phase 0, ongoing).** This skill's Semgrep rule bodies and CI wiring are scheduled for Phase 0 (release-gate integration). Until the corresponding `.github/workflows/airgap-gate.yml` is green on main, treat this skill as **advisory-only** — surface the checklist to the reviewer, do not block merges, and flag any mismatch as `activation-pending` rather than auto-fixing.

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
7. **Outbound allow-list for opt-in features (F2 — PLAN.md §51, CLAUDE.md §Security #14).** When any opt-in outbound path is enabled (ACME/LE, Polar.sh checkout, paid IP2Location DB23 download, license phone-home v2, Telegram, email SMTP), every outbound `http.Client` / `net.Dialer` goes through `internal/httpclient/guarded.go` which (a) rejects destinations not on the config-declared FQDN allow-list in `config.outbound.allowlist`, (b) rejects all RFC 1918 / loopback / link-local / CGNAT (`100.64.0.0/10`) ranges post-DNS resolution (DNS-rebinding guard), (c) forces `https://` scheme. Semgrep rule `airgap-no-raw-httpclient`: flag any `&http.Client{}` / `http.DefaultClient` / `http.Get` / `http.Post` call outside `internal/httpclient/`.
8. **Skill-content sanitizer (F6 — PLAN.md Phase 7d).** CI job greps every `SKILL.md` and `README.md` under `.claude/skills/` for (a) Unicode Tag Block (`U+E0000`–`U+E007F`), (b) zero-width chars (`U+200B`, `U+200C`, `U+200D`, `U+FEFF`, `U+2060`), (c) bidi overrides (`U+202A`–`U+202E`, `U+2066`–`U+2069`), (d) HTML comments (`<!--` … `-->`), (e) `display:none` / `visibility:hidden` CSS in inline HTML. Any hit halts CI. Bash scripts that parse skill frontmatter use `yq` (structured); grep/sed/awk on frontmatter is banned — regex-allowlist every frontmatter value before use. Schedule: advisory from Phase 0; promoted to hard gate at the kill-9 WAL gate milestone.
9. **TOCTOU-safe file I/O (F7 — PLAN.md Phase 7d, Go 1.24+ floor).** File I/O inside CI skill scripts or binary config-loader paths uses `os.Root.Open` (Go 1.24+, wraps `openat2` with `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS`), not `filepath.Clean` + `os.Open`. Applies to: license-file loader, PEM loader (TLS cert/key), GeoIP BIN reload, skill sanitizer scratch dir, license-JWT private-key loader. Advisory until `go.mod` commits the Go 1.24 floor; promoted to hard gate in that PR.

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