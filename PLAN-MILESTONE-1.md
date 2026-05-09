# Milestone 1 cutover postmortem (2026-04-25)

> **Frozen historical record.** This file documents the v0.0.1-rc1 → rc8 cutover bug catalog. Source-of-truth for "what we caught and where" during the first production launch on Netcup. Cross-link from [`PLAN.md` § Milestone 1 cutover postmortem](PLAN.md). Lessons distilled from these bugs live in [`LEARN.md`](LEARN.md).
>
> Do not extend in place — new release post-mortems get their own dated file (`PLAN-MILESTONE-2.md`, etc.) so this one stays self-contained and quotable.

---

**60-second narrative.** Cutover took ~4 hours wall time vs the 90-minute runbook estimate. Binary now serving production HTTPS on `app.statnive.live` with real browser pageviews from all three locales landing in `events_raw` within 2 seconds of the visit. Path was non-trivial: 24 distinct bugs hit between the bundle / config / install / cutover-script layers, including one full SSH lockout that required Netcup Rescue System recovery (~30 min). All bugs cataloged below; lessons distilled in [LEARN.md](LEARN.md).

**VPS final state (canonical record).**

| Item | Value |
|---|---|
| Provider | Netcup VPS 2000 G12 iv NUE — Nuremberg |
| Procured | 2026-04-24 (commit `4ff19dd67`) |
| OS | Debian 13 (trixie), kernel `6.12.74+deb13+1-amd64` |
| IPv4 | `94.16.108.78` |
| IPv6 | `2a03:4000:51:f0c::1/64` (gw `fe80::1`, persisted via systemd oneshot — netplan absent on Debian 13) |
| ClickHouse | `26.5.1.68` (upstream installer pulled latest, NOT 24.x as the runbook claimed) |
| TLS cert | Let's Encrypt 3-SAN (`statnive.live` + `app.statnive.live` + `demo.statnive.live`); valid until 2026-07-24 |
| DNS | Cloudflare (`statnive.live` zone, DNS-only / grey cloud); [`deploy/dns/statnive.live.zone`](deploy/dns/statnive.live.zone) is canonical |
| Binary | Cross-compiled Linux amd64 (NOT from `make airgap-bundle` — see Bug 3); `/usr/local/bin/statnive-live` listening on `0.0.0.0:443` |
| Service users | `ops` (operator, key-auth, NOPASSWD sudo); `deploy` (GHA, NOPASSWD sudo for `/usr/local/bin/statnive-deploy`); `statnive` (binary service); `clickhouse` (CH service) |
| Sites seeded | site_id=1 statnive.com (Europe/Berlin); =2 statnive.de (Europe/Berlin); =3 fr.statnive.com (Europe/Paris) |
| Admin user | new admin (operator-rotated; bootstrap `ops@statnive.live` disabled because the password leaked into the cutover transcript — see Bug 18 / Lesson 18) |
| Demo viewer | `demo@statnive.live` (role=viewer, site_id=1) |
| Bootstrap envs | commented out in `/etc/systemd/system/statnive-live.service.d/env.conf`; `/root/statnive-bootstrap-password.txt` shredded |
| GHA pipeline | release.yml + deploy-saas.yml + rollback-saas.yml shipped (PR #48); **secrets not yet configured in repo settings** — first GHA-driven release pending |

**Bug catalog.** Severity reflects production impact during the cutover (not a CVSS-style severity).

#### CRITICAL — stopped boot until manually worked around

| # | File:line | Bug | Workaround | Suggested fix PR |
|---|---|---|---|---|
| 5 | `config/statnive-live.yaml.example` | Schema mismatch: example uses `audit.sink_path`, `clickhouse.dsn`, `geoip.bin_path`, `server.tls.cert_file`, `server.addr`; binary expects `audit.path`, `clickhouse.addr`, `enrich.geoip_bin_path`, top-level `tls.cert_file`, `server.listen`. Multiple sections silently no-op. | Use systemd `Environment=STATNIVE_*=...` env-var overrides for every path; bypass the example file entirely. | **PR-B** |
| 6 | `cmd/statnive-live/main.go` | Binary ignores the `-c` flag passed by the systemd unit. No flag parsing exists. Viper auto-detects only `./config/statnive-live.yaml` and `./statnive-live.yaml` relative to CWD. | Same as #5: env-var overrides. systemd `WorkingDirectory=` blocked by `ProtectSystem=strict`. | **PR-B** |
| 7 | `cmd/statnive-live/main.go:914` | `viper.ReadInConfig` returns `ConfigFileNotFoundError`; `errors.As` swallows it silently. Binary boots with all defaults including relative paths that fail under systemd hardening. | Env-var overrides + the operator chasing log lines until they realized the config wasn't being read. | **PR-B** |
| 9 | `deploy/airgap-install.sh` | Sets `/etc/statnive-live/` to `0700 root:root` (or it ends up that way). `statnive` service user can't traverse → all reads from inside fail with `permission denied`. | `sudo chmod 0755 /etc/statnive-live`. | **PR-C** |
| 10 | `deploy/airgap-install.sh` | Sets `/etc/statnive-live/tls/` to `0700 root:root`. `statnive` service user can't traverse → TLS PEM reads would fail. | `sudo chown root:statnive /etc/statnive-live/tls && sudo chmod 0750 /etc/statnive-live/tls`. | **PR-C** |

#### HIGH — silent data loss / wrong-arch binary / lockout risk

| # | File:line | Bug | Workaround | Suggested fix PR |
|---|---|---|---|---|
| 1 | `deploy/airgap-bundle.sh` | Doesn't include `deploy/statnive-deploy.sh` in the bundle. `step-b.sh § B.4` aborts trying to install it from `/opt/statnive-bundles/<base>/deploy/statnive-deploy.sh`. | Manual SCP + `install -m 0755`. | **PR-A** |
| 2 | `deploy/airgap-bundle.sh` | Doesn't include `internal/enrich/crawler-user-agents.json`. Binary logs `embedded crawler JSON empty or invalid; using fallback patterns`; bot detector silently falls back from full ~700 patterns to ~60. | None applied — accepted the reduced bot-detection until follow-up PR. | **PR-A** |
| 3 | `Makefile`, `deploy/airgap-bundle.sh` | `make airgap-bundle` doesn't cross-compile. Mac-built tarball ships a `darwin/arm64` binary inside a `linux-amd64`-named tarball. Linux runs hit `Exec format error`. | `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build`. | **PR-A** |
| 14 | `step-b.sh § B.1` | Disables SSH password auth + `prohibit-password` BEFORE verifying ops key auth works. If anything in §§ B.2–B.5 aborts (which happened — see Bug 12), operator may be fully locked out. | Netcup Rescue System. ~30 min recovery. | **PR-D** |
| 18 | `internal/tracker/tracker.js` (or wherever the JS is built) | Tracker JS doesn't fall back to deriving the endpoint from `e.currentScript.src` when `data-statnive-endpoint` is absent. Defaults to relative `/api/event` on the page's origin → silent 404 sink on cross-origin marketing sites. | Set `data-statnive-endpoint="https://statnive.live/api/event"` explicitly in `Tracker.astro`. | **PR-E** |

#### MEDIUM — friction with available workaround

| # | File:line | Bug | Workaround | Suggested fix PR |
|---|---|---|---|---|
| 4 | `Makefile` | `VERSION` defaults to `dev` (no `v` prefix) when no git tag. Collides with `step-b.sh`'s `statnive-live-v*-...tar.gz` glob. | Rename tarball + extracted dir to `v0.0.0-dev` form. | **PR-A** |
| 8 | `config/statnive-live.yaml.example` | Missing `ingest:` section entirely → `ingest.wal_dir` defaults to relative `./wal`. | Env-var override `STATNIVE_INGEST_WAL_DIR`. | **PR-B** |
| 11 | `deploy/airgap-install.sh:138` | Hardcodes `iptables-restore`. Fails on minimal Debian 13 images without the `iptables` package. | `apt install -y iptables`. | **PR-C** |
| 12 | upstream `clickhouse install` script | Doesn't register a systemd unit on Debian 13. step-b.sh's `systemctl enable clickhouse-server` aborts. | step-b.sh now writes `/etc/systemd/system/clickhouse-server.service` from the canonical upstream packaging template if missing. | **PR-D** (already partially in step-b.sh) |
| 15 | `step-b.sh § B.1b` (original) | Used netplan for IPv6 binding. Debian 13 doesn't ship netplan by default. | Switched to systemd oneshot service `/etc/systemd/system/statnive-ipv6-bind.service`. | **PR-D** (already in step-b.sh) |
| 16 | `step-b.sh § B.4` | Bundle glob requires `v*` prefix. Brittle against `make airgap-bundle` defaults. | See Bug 4 workaround. | **PR-D** |
| 17 | `step-b.sh § B.4` | Extracted-dir-name check expects directory inside tarball to match the (possibly renamed) tarball name. Renaming the tarball doesn't rename the dir inside. | Rename both tarball AND extracted dir. | **PR-D** |
| 19 | binary `/api/event` handler | Sets `_statnive=<UUID>; Max-Age=31536000; HttpOnly; SameSite=Lax` cookie on responses. Needs GDPR review for SaaS posture. | Noted for follow-up; not a code change in this cutover. | **PR-E** + privacy review |
| 20 | binary fast-reject filter | Manual curl tests with default UA `curl/8.7.1` (10 chars) hit pre-pipeline fast-reject (UA length <16 → 204). | Use `curl -A 'Mozilla/5.0 ...'` with a real-browser-shaped UA. | Document in runbook; not a binary fix |
| 23 | Netcup noVNC client | No clipboard sync. Operators can't paste long pubkeys. | Use Netcup Rescue System for any "fix authorized_keys" recovery (clipboard works in rescue SSH). | Document in runbook |

#### LOW — cosmetic / one-off

| # | File:line | Bug | Workaround | Suggested fix PR |
|---|---|---|---|---|
| 13 | `docs/runbook.md` | Claims ClickHouse 24+; upstream installer pulls 26.x. CH is mostly backward-compatible but the docs drift. | Note the ≥24 floor and whatever-upstream-ships actuality. | doc-only, fold into PR-C |
| 21 | operator's `~/.ssh/config` | `Host *` `IdentityFile id_rsa` blocks all other keys (common 2018 pattern). | Add per-host stanza overriding `IdentityFile`. | Document in runbook |
| 22 | macOS Keychain auto-load | Operator's id_ed25519 passphrase was actually retrievable via `ssh-add --apple-load-keychain`; we didn't try this first. | Try Keychain load before assuming a key is unrecoverable. | Document in runbook + LEARN.md Lesson 12 |
| 24 | `PLAN.md` | PR #47 ↔ PR #48 conflict on Phase 10 Iranian-DC bullet (both touched `make airgap-bundle → SCP → install` line). | Manual rebase resolution: keep both edits. | Process; merge order matters |

**Suggested follow-up PR groupings.** The 24 bugs collapse to 5 PRs, each independently mergeable:

| PR | Scope | Bugs |
|---|---|---|
| **PR-A** | `airgap-bundle.sh` completeness — add `statnive-deploy.sh` + `crawler-user-agents.json` to the bundle; `make airgap-bundle` cross-compiles via `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` env defaults; widen / align version glob defaults. | 1, 2, 3, 4 |
| **PR-B** | Config schema parity — rewrite `config/statnive-live.yaml.example` to match binary's `loadConfig` keys; add `-c <path>` flag parsing in `cmd/statnive-live/main.go`; promote silent `ConfigFileNotFoundError` to a logged warning (or hard error in production mode). Add CI test that loads the example through `loadConfig` and asserts no key falls back to default. | 5, 6, 7, 8 |
| **PR-C** | `airgap-install.sh` perms + Debian compat — `chmod 0755 /etc/statnive-live`, `chmod 0750 /etc/statnive-live/tls` with `chown root:statnive`; install fallback for missing `iptables` package; auto-detect ClickHouse systemd unit and write canonical template if missing. | 9, 10, 11, 12, 13 |
| **PR-D** | Cutover-script hardening — split `step-b.sh` into `step-b-pre-lockdown.sh` + `step-b-lockdown.sh` (operator runs lockdown only after verifying key auth in second terminal); also write ops pubkey to `/root/.ssh/authorized_keys` during pre-lockdown for fallback; widen bundle glob; rename extracted dir if bundle was renamed. | 14, 15, 16, 17, 20 |
| **PR-E** | Tracker fallback + cookie scope review — tracker JS derives endpoint from `e.currentScript.src` when `data-statnive-endpoint` absent; add `consent_required: bool` config flag gating the `_statnive` cookie in SaaS posture (default `true` for SaaS, `false` for self-hosted Iran). | 18, 19 |

**Lessons captured in [LEARN.md](LEARN.md)** — 23 lessons across 8 categories (build & release, config schema parity, install perms + distro testing, SSH lockdown ordering, operator workflow, privacy testing, pre-release validation, deploy-time probes & VPS prereqs). Read before planning Phase 10 P1 cutover or any deploy/install/config work.

**Postscript — v0.0.1-rc1 release-attempt chain (2026-04-26 to 2026-04-27).** First GHA-driven release. 8 fix PRs (#64 / #66 / #69 / #70 / #71 / #72 / #73 / #74) before `release.yml` ran end-to-end green. Each PR fixed a single CI gap surfaced one-at-a-time on the GHA runner — strategic shift to local `make release-fresh` validation (LEARN.md Lesson 19) closes that pattern permanently. Four deploy-time follow-ups (Lessons 20–23) shipped together in `fix/v0.0.1-rc1-deploy-followups`: probe-port mismatch, VPS pubkey prereq, GITHUB_TOKEN recursion guard workaround, embedded JSON regression hard gate.

---

**History note (2026-05-09):** Tag `v0.0.4` was burned by the `token-budget` release-gate when `PLAN.md` exceeded its 720-line cap. `v0.0.5` ships the same vermilion landing + Go 1.25.10 bump + this PLAN.md → PLAN-MILESTONE-1.md split.
