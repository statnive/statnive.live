# LEARN.md — Institutional Memory

> Lessons from prior cutovers / outages / bug-discovery sessions. **Read this before planning any task that touches `deploy/`, `cmd/statnive-live/main.go` (config / flag-parsing), `config/statnive-live.yaml.example`, operator-facing scripts (`step-b.sh`, `step-d.sh`, `statnive-deploy.sh`, `courier-iran.sh`), or any cutover SOP in `docs/runbook.md`.** Workflow rule + update cadence in [CLAUDE.md § Workflow Rule — `LEARN.md` is canonical institutional memory](CLAUDE.md).

The point: avoid re-discovering bugs we already caught. Each lesson encodes a specific bug class with the **what / why / fix / preventive-measure** structure. Lessons live forever; mark `[obsolete]` when a CI gate now catches the bug, but don't delete.

## Index

- [A. Build & release](#a-build--release)
  - [1 — Cross-compile or use CI Linux runners](#lesson-1)
  - [2 — Bundle completeness must be verified post-build](#lesson-2)
  - [3 — Default `VERSION=dev` collides with `v*` regex](#lesson-3)
- [B. Config schema parity](#b-config-schema-parity)
  - [4 — Binary keys must match shipped example](#lesson-4)
  - [5 — Honor your own `-c` flag](#lesson-5)
  - [6 — Don't silently swallow `ConfigFileNotFoundError`](#lesson-6)
- [C. Install permissions + distro testing](#c-install-permissions--distro-testing)
  - [7 — Test parent-dir perms separately from file perms](#lesson-7)
  - [8 — Install scripts run as the actual service user during dev](#lesson-8)
  - [9 — Test on >=2 Linux distros (Ubuntu LTS + Debian stable)](#lesson-9)
- [D. SSH lockdown ordering](#d-ssh-lockdown-ordering)
  - [10 — Never disable password auth before key auth verified](#lesson-10)
  - [11 — Always populate a fallback root key](#lesson-11)
- [E. Operator workflow](#e-operator-workflow)
  - [12 — macOS Keychain holds passphrases silently](#lesson-12)
  - [13 — `Host * IdentityFile id_rsa` is a 2018 trap](#lesson-13)
  - [14 — Rescue System > VNC paste > physical typing](#lesson-14)
  - [15 — Real-browser UA (>=16 chars) for ingest tests](#lesson-15)
- [F. Privacy testing](#f-privacy-testing)
  - [16 — DNT='1' silently zeroes the tracker; test in clean Chrome incognito](#lesson-16)
  - [17 — `_statnive` cookie needs GDPR review for SaaS posture](#lesson-17)
  - [18 — Don't paste credentials into chat / transcripts](#lesson-18)
- [G. Pre-release validation](#g-pre-release-validation)
  - [19 — `make release-fresh` locally before any `v*` tag push](#lesson-19)
- [H. Deploy-time probes & VPS prereqs](#h-deploy-time-probes--vps-prereqs)
  - [20 — `statnive-deploy` healthz probe must read host:port from config](#lesson-20)
  - [21 — `/etc/statnive/release-key.pub` is a one-time VPS prereq](#lesson-21)
  - [22 — `release.yml` cannot auto-fire downstream workflows via `GITHUB_TOKEN`](#lesson-22)
  - [23 — Embed-size assertion for refreshed JSON to catch silent stale-build regressions](#lesson-23)
- [I. Privacy posture](#i-privacy-posture)
  - [24 — Don't make a privacy-policy decision in the tracker bundle](#lesson-24)
  - [25 — `statnive-deploy` healthz probe budget at 30 s is too tight for Netcup cold start](#lesson-25)
  - [26 — `statnive-deploy` healthz URL must honor systemd env, not just YAML](#lesson-26)
  - [27 — `/metrics received_total` only counts what reaches the binary; view-source the customer's HTML before chasing server-side drops](#lesson-27)
  - [28 — Default `scp` to Netcup is ~16 KB/s; always use `-C` for files >10 MB](#lesson-28)
  - [29 — `airgap-update-geoip.sh` rejects `/tmp` source; same-fs as `/etc` required, use `/var/tmp`](#lesson-29)
  - [30 — IP2Location LITE BINs emit an 80-char "parameter unavailable" sentinel for fields they don't carry](#lesson-30)
- [J. Tracker bundle hygiene](#j-tracker-bundle-hygiene)
  - [31 — SaaS tracker public-API namespace must not collide with the same-brand WP plugin's `window.statnive`](#lesson-31)
- [K. Schema migrations](#k-schema-migrations)
  - [32 — CH `FINAL` after table alias, plain MergeTree rejects `FINAL`](#lesson-32)
  - [33 — Migration-template string substitution needs config-time email validation](#lesson-33)

---

## A. Build & release

### Lesson 1

**Cross-compile or use CI Linux runners; never ship a Mac-built binary in a Linux-named tarball.**

1. **What we did** — Ran `make airgap-bundle` on Apple Silicon Mac. Tarball was named `statnive-live-dev-linux-amd64-airgap.tar.gz`. The binary inside was darwin/arm64.
2. **Why it broke** — `make airgap-bundle` invokes `make build` which runs `go build` without GOOS/GOARCH overrides. On Mac, that produces a darwin binary. The bundle naming hardcodes `linux-amd64` regardless. On the Linux VPS, attempting to run the binary returned `Exec format error`.
3. **The fix we applied** — Cross-compiled with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -mod=vendor -o /tmp/statnive-live-linux ./cmd/statnive-live`, then SCP'd and replaced `/usr/local/bin/statnive-live` on the VPS.
4. **Preventive measure** — `make airgap-bundle` should set `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` explicitly, OR fail loudly when the host platform doesn't match the bundle target. Better: rely on GHA `release.yml` (ubuntu-latest runner) for production releases — local builds become dev-only and should not be the production path.

### Lesson 2

**Bundle completeness must be verified post-build, not assumed from `cp` paths in the build script.**

1. **What we did** — Trusted `deploy/airgap-bundle.sh` to bundle every file the install path needs.
2. **Why it broke** — Twice it didn't. Missing `deploy/statnive-deploy.sh` (broke `step-b.sh § B.4`'s `install -m 0755 .../deploy/statnive-deploy.sh /usr/local/bin/statnive-deploy`). Missing `internal/enrich/crawler-user-agents.json` (binary logged `embedded crawler JSON empty or invalid; using fallback patterns` — bot detector silently fell back to ~60 patterns instead of full ~700). The bundling script has explicit `cp` calls for each file; new files in the repo (statnive-deploy.sh added in PR #48) don't auto-propagate.
3. **The fix we applied** — Manually SCP'd missing files to the VPS and `install`-ed them into the extracted bundle dir before re-running `step-b.sh`.
4. **Preventive measure** — Add a `make airgap-bundle-verify` target that extracts the tarball to a temp dir and walks every reference in `deploy/airgap-install.sh` + `step-b.sh` + `statnive-deploy.sh`, asserting each referenced file exists. CI-gate it after `make airgap-bundle`.

### Lesson 3

**Default `VERSION=dev` (no `v` prefix) collides with `v*` regex elsewhere. Pick one source of truth.**

1. **What we did** — `step-b.sh` glob expected `statnive-live-v*-linux-amd64-airgap.tar.gz`. `make airgap-bundle` produced `statnive-live-dev-linux-amd64-airgap.tar.gz`. Glob didn't match. Script aborted at § B.4.
2. **Why it broke** — Two assumptions diverged. `make airgap-bundle` uses `git describe --tags --dirty 2>/dev/null || echo dev`; with no tags, defaults to bare `dev`. Cutover scripts assumed every operator either tags first or has a v-prefixed default.
3. **The fix we applied** — Renamed the tarball on the VPS from `statnive-live-dev-...` to `statnive-live-v0.0.0-dev-...` to match the glob. Then renamed the extracted directory the same way (the directory inside the tarball still had the old name).
4. **Preventive measure** — Align defaults: change `make airgap-bundle` default from `dev` to `v0.0.0-dev`, OR widen the glob in cutover scripts to `statnive-live-*-linux-amd64-airgap.tar.gz`. Pick one source of truth.

---

## B. Config schema parity

### Lesson 4

**Binary's actual config keys must match the shipped `config.yaml.example`. Add a CI test that loads the example and asserts no keys end up as defaults.**

1. **What we did** — Followed `config/statnive-live.yaml.example` to set `audit.sink_path`, `clickhouse.dsn`, `geoip.bin_path`, `server.tls.cert_file`, `server.addr`. Binary started with all relative-path defaults instead of using the configured values.
2. **Why it broke** — The example file uses one schema; the binary's `loadConfig` uses a different one. Keys diverge: `audit.sink_path` ≠ binary's `audit.path`; `clickhouse.dsn` ≠ binary's `clickhouse.addr`; `geoip.bin_path` ≠ binary's `enrich.geoip_bin_path`; `server.tls.cert_file` ≠ binary's top-level `tls.cert_file`; `server.addr` ≠ binary's `server.listen`. Multiple sections silently no-op. Plus the example is missing the `ingest:` section entirely (so `ingest.wal_dir` defaults to relative `./wal`).
3. **The fix we applied** — Reverse-engineered the binary's actual schema from `cmd/statnive-live/main.go:858+` (every `v.SetDefault` and `v.GetString` call), then bypassed the example file and used systemd `Environment=STATNIVE_*=...` overrides for every path the binary reads.
4. **Preventive measure** — CI test that loads `config/statnive-live.yaml.example` through `loadConfig`, asserting no key in the file ends up at its hardcoded default. If any does, the example is shipping fake settings. Easy to write, hard to ship a bug past.

### Lesson 5

**If your binary's systemd unit passes `-c <path>`, the binary MUST honor `-c`. Otherwise the unit is theater.**

1. **What we did** — systemd unit had `ExecStart=/usr/local/bin/statnive-live -c /etc/statnive-live/config.yaml`. Trusted the binary read the file at that path.
2. **Why it broke** — `cmd/statnive-live/main.go` has zero flag parsing (no `flag.Parse()`, no `pflag` usage). Viper auto-detects only `./config/statnive-live.yaml` and `./statnive-live.yaml` relative to CWD (default `/` under systemd). The `-c` argument is silently discarded by Go's runtime. Binary uses hardcoded defaults forever.
3. **The fix we applied** — Tried `WorkingDirectory=/etc/statnive-live` (failed: systemd's `ProtectSystem=strict` blocks CHDIR for the service user). Switched to env-var-only configuration via systemd `Environment=STATNIVE_*=...` directives. Worked because viper's `SetEnvPrefix("STATNIVE") + AutomaticEnv + EnvKeyReplacer(".", "_")` IS implemented.
4. **Preventive measure** — Implement `-c <path>` flag parsing using `flag.NewFlagSet` early in main, before `loadConfig`. If passed, call `v.SetConfigFile(path)`. OR remove the `-c` flag from the systemd unit so the lie is gone.

### Lesson 6

**Don't silently swallow `viper.ConfigFileNotFoundError`. Log it loudly.**

1. **What we did** — Trusted `loadConfig` to error out if the config file was missing.
2. **Why it broke** — Code at `cmd/statnive-live/main.go:914`:
   ```go
   if readErr := v.ReadInConfig(); readErr != nil {
       var notFound viper.ConfigFileNotFoundError
       if !errors.As(readErr, &notFound) {
           return appConfig{}, fmt.Errorf("read config: %w", readErr)
       }
   }
   ```
   Swallows the not-found case entirely. Binary boots happily with all defaults — including relative paths (`./audit.jsonl`, `./wal`) that fail under systemd `ProtectSystem=strict`. Operator sees `audit log: open ./audit.jsonl: read-only file system` with no hint that the config file was never read.
3. **The fix we applied** — Used env-var overrides to reach a working state (binary defaults bypassed). The silent swallow remained.
4. **Preventive measure** — At minimum, log `WARN: config file not found at <searched paths>, using defaults`. Better: in production mode (e.g. when systemd `INVOCATION_ID` is set in the environment), promote ConfigFileNotFoundError to a hard error. Even better: implement Lesson 5's `-c` flag, then ConfigFileNotFoundError on an explicitly-passed path is unambiguously fatal.

---

## C. Install permissions + distro testing

### Lesson 7

**Test parent-directory perms separately from file perms. `chmod 0644 file` inside a `0700 dir` is useless.**

1. **What we did** — Trusted `airgap-install.sh`'s `install -d -m 0755 ...` to set `/etc/statnive-live` correctly. When the binary failed to read sources.yaml, set the file to `0644 root:statnive` for clean reads. Still failed.
2. **Why it broke** — `/etc/statnive-live/` was actually `0700 root:root` (something between airgap-install.sh and the production state changed it — possibly a `umask 077` from another script in the cutover sequence). The `statnive` user had no `x` (search) permission on the parent dir. Files inside were unreachable regardless of their own perms — `open(...): permission denied` on every file. Same applied to `/etc/statnive-live/tls/` (mode `0700 root:root`), so TLS PEMs would also have failed had the binary reached that point.
3. **The fix we applied** — `sudo chmod 0755 /etc/statnive-live` and `sudo chown root:statnive /etc/statnive-live/tls && sudo chmod 0750 /etc/statnive-live/tls`.
4. **Preventive measure** — Install scripts use `namei -l <path>` post-install to verify the path traversal works for the service user. Better: a script that runs `sudo -u statnive cat <every-file-the-binary-reads>` and fails on any `EACCES`. Add to `make smoke`.

### Lesson 8

**Install scripts must run as the actual service user during dev testing, not just root.**

1. **What we did** — `airgap-install.sh` was developed against root reads/writes; nobody tested whether the `statnive` user could traverse `/etc/statnive-live/`. The bug was invisible until production.
2. **Why it broke** — `chmod 0700` works fine when only root reads. But `User=statnive Group=statnive` in the systemd unit means the running process can't see anything in a `0700 root:root` directory. The bug surfaced only at first boot under systemd.
3. **The fix we applied** — Manual chmod/chown on the box (see Lesson 7).
4. **Preventive measure** — Extend `make smoke` to run a `sudo -u statnive --` pre-flight that opens every file the binary reads (config, sources.yaml, TLS PEMs, master.key, GeoIP BIN). Fails the smoke gate if any `EACCES`. This catches Lesson 7's bug class at the smoke layer.

### Lesson 9

**If you target "Linux", test on at least 2 distros (Ubuntu LTS + Debian stable). The surface delta is large.**

1. **What we did** — `airgap-install.sh`, `step-b.sh`, the runbook all assumed Ubuntu 24 LTS. Production VPS was Debian 13 (Netcup default image). Hit ~5 distinct distro deltas during cutover.
2. **Why it broke** —
   - `iptables` not installed by default on Debian 13 minimal image
   - `netplan` absent on Debian 13 (Ubuntu-only convention; Debian uses ifupdown / systemd-networkd)
   - Upstream `curl https://clickhouse.com/ | sh && ./clickhouse install` doesn't register a systemd unit on Debian 13 — works on Ubuntu / RHEL
   - ClickHouse upstream installer pulls latest (26.x) not 24.x as the runbook claimed
   - Default network interface name varies — Netcup Debian uses `eth0` but other Debian images can be `enp1s0` or `ens3`
3. **The fix we applied** — `apt install -y iptables`. Switched IPv6 binding from netplan to a systemd oneshot. Manually wrote `/etc/systemd/system/clickhouse-server.service` from upstream packaging template. Auto-detected `ETH_IFACE` from the IPv4 default route in step-b.sh.
4. **Preventive measure** — Matrix CI: `airgap-install.sh` + `step-b.sh` runs against `ubuntu:24.04` AND `debian:13` Docker images on every PR. Plus arm64 variants if those are ever supported. Catches the entire class on the way in.

---

## D. SSH lockdown ordering

### Lesson 10

**Never disable SSH password auth before key auth is verified working in a SEPARATE session.**

1. **What we did** — `step-b.sh § B.1` ran `sed PasswordAuthentication no` + `PermitRootLogin prohibit-password` + `systemctl reload ssh`, THEN proceeded to install ClickHouse + GHA seam + bundle install in §§ B.2–B.5.
2. **Why it broke** — § B.2 (ClickHouse) aborted on `systemctl enable clickhouse-server` (no unit on Debian 13 — see Lesson 9). The script exited via `set -e`. The operator's SSH session ended naturally. Tried to SSH back as root: `Permission denied (publickey)` (no key in `/root/.ssh/authorized_keys`). Tried as ops: `Permission denied (publickey)` (operator's `~/.ssh/id_ed25519` was passphrase-encrypted, passphrase forgotten — see Lesson 12). Full lockout. Required Netcup Rescue System to recover (~30 minutes off the cutover budget).
3. **The fix we applied** — Booted Rescue System, mounted `/dev/vda4`, replaced `/home/ops/.ssh/authorized_keys` with a fresh no-passphrase ed25519 pubkey, deactivated rescue, rebooted. SSH then worked.
4. **Preventive measure** — Split `step-b.sh` into `step-b-pre-lockdown.sh` and `step-b-lockdown.sh`. Pre-lockdown does everything EXCEPT disable password auth. Operator runs pre-lockdown, then verifies `ssh ops@host 'sudo whoami'` works in a second terminal, THEN runs lockdown. (Already in PLAN.md follow-up PR-D.)

### Lesson 11

**Always populate `/root/.ssh/authorized_keys` (or another fallback path) before locking down root.**

1. **What we did** — `step-b.sh` only installed the ops pubkey into `/home/ops/.ssh/authorized_keys`. Root's `authorized_keys` was never touched. After `PermitRootLogin prohibit-password`, root SSH had no key to accept and no password to fall back on.
2. **Why it broke** — One key path = single point of failure. If the ops account is corrupted or its key forgotten, root is unreachable too.
3. **The fix we applied** — Rescue System recovery (see Lesson 10). Then we explicitly wrote a fresh pubkey to `/home/ops/.ssh/authorized_keys`; we did NOT add a copy to `/root/.ssh/authorized_keys` because by then ops was working.
4. **Preventive measure** — Pre-lockdown script writes ops pubkey to BOTH `/home/ops/.ssh/authorized_keys` AND `/root/.ssh/authorized_keys`. Lockdown script removes the root copy at the very end, after a final `ssh ops@host 'sudo whoami'` self-check. Net: a continuous "at least one path open" invariant during the transition.

---

## E. Operator workflow

### Lesson 12

**macOS Keychain holds SSH passphrases silently. Try `ssh-add --apple-load-keychain` before assuming a key is unrecoverable.**

1. **What we did** — When operator's `~/.ssh/id_ed25519` prompted for a forgotten passphrase, immediately escalated to "generate new keypair + recover access via Rescue System". Spent ~45 min on the recovery path.
2. **Why it broke** — We didn't check Keychain first. macOS' built-in ssh-agent integration can hold passphrases for years without the user remembering them — `ssh-add --apple-use-keychain` stored it the first time the key was used; subsequent uses load it silently from Keychain. Operators stop seeing the prompt and forget the passphrase exists.
3. **The fix we applied** — Eventually added Phase 0 diagnostic to the recovery plan that runs `ssh-add --apple-load-keychain` + `ssh-add -l` to check. In this case Keychain didn't have the passphrase, so we proceeded with new-key generation.
4. **Preventive measure** — Any "lost SSH key passphrase" recovery flow starts with these two commands. Save 30 minutes of unnecessary recovery work in many cases. Document in the runbook's lockout-recovery section.

### Lesson 13

**`~/.ssh/config` `Host *` `IdentityFile id_rsa` is a 2018-pattern trap. Add per-host overrides in cutover docs.**

1. **What we did** — Operator's `~/.ssh/config` had `Host *` block with `IdentityFile ~/.ssh/id_rsa` (common 2018 pattern). SSH'd `ops@VPS` expecting id_ed25519 to be tried.
2. **Why it broke** — With `Host *` `IdentityFile id_rsa` set, SSH offers ONLY id_rsa to every host. `id_ed25519` is never tried — even though it's in the agent. Server rejects id_rsa (not authorized for ops). Connection fails with `Permission denied (publickey)` and no chance to try other keys.
3. **The fix we applied** — Added an explicit `Host 94.16.108.78 app.statnive.live ...` block with `IdentityFile ~/.ssh/id_ed25519` + `IdentitiesOnly yes` to override the wildcard.
4. **Preventive measure** — Cutover docs include this `~/.ssh/config` snippet as a Step A.5 (after key generation, before SCP). When "Permission denied (publickey)" hits, the diagnostic block lists `cat ~/.ssh/config` first.

### Lesson 14

**VPS lockout recovery: Rescue System always works, VNC paste is unreliable, physical typing is last resort.**

1. **What we did** — When locked out of the Netcup VPS, opened the noVNC console first (saw `>_screen`-confusing UI), then tried to type/paste recovery commands.
2. **Why it broke** — Netcup's noVNC has no clipboard sync. The on-screen virtual keyboard sends one keystroke per click. Physical keyboard works for typing but operators are typo-prone on 100+ char base64 SSH keys. We wasted ~15 minutes attempting paste tricks (Cmd-V, F8 menu, right-click) before switching strategies.
3. **The fix we applied** — Activated Rescue System (CCP → Media → Rescue System → Activate → DVD boot). SSH'd into the rescue Linux with the one-time password. Rescue's SSH supports clipboard paste normally. Mounted `/dev/vda4`, replaced authorized_keys, deactivated rescue, rebooted.
4. **Preventive measure** — Any "lost VPS access" recovery flow defaults to Rescue System. Don't waste 20 minutes on VNC paste attempts. Document the Rescue System path FIRST in the lockout-recovery runbook section.

### Lesson 15

**Real-browser-shaped UA (>=16 chars) for ingest tests; `curl/8.7.1` (10 chars) is fast-rejected.**

1. **What we did** — Sent `curl -X POST https://statnive.live/api/event` with default UA `curl/8.7.1`. Got `HTTP/2 204` (no body), no row in `events_raw`. Spent ~10 minutes assuming the binary's ingest path was broken.
2. **Why it broke** — Pre-pipeline fast-reject (CLAUDE.md Architecture Rule 6) drops payloads where `UA length < 16` (also non-ASCII UA, IP-as-UA, UUID-as-UA, prefetch headers). curl's default UA is 10 chars. Returns 204 with no event ingested. By design — prevents WAL spam.
3. **The fix we applied** — Re-ran with `curl -A 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15' ...`. Got `HTTP/2 202` + `set-cookie: _statnive=<UUID>`. Event landed in CH within 2 seconds.
4. **Preventive measure** — Any ingest-path verification step in the runbook MUST use a real-browser-shaped UA. Add a `make smoke-ingest` target that hardcodes a known-good UA. Document the fast-reject rules visibly so operators don't waste time chasing imaginary breakage.

---

## F. Privacy testing

### Lesson 16 [obsolete — superseded by Lesson 24]

**Operators with DNT='1' in their daily browser silently zero the tracker. Test in clean Chrome incognito.**

> **Status:** the underlying client-side `navigator.doNotTrack` / `navigator.globalPrivacyControl` short-circuit was removed when production diagnosis on `wp-slimstat.com` showed it was hiding 70-85% of *all* legitimate visitors, not just operator self-tests. See Lesson 24 for the full chain (under-count → counter-confirmed root cause → tracker fix → operator opt-in via `consent.respect_*`). The "test in Chrome Incognito" preventive measure is no longer load-bearing because the bug class no longer exists.


1. **What we did** — Visited `https://statnive.com/` in Safari (operator's daily browser). `tracker.js` loaded (200 OK in DevTools). No POST to `/api/event` ever fired. Spent ~15 minutes assuming the tracker was misconfigured on the marketing site.
2. **Why it broke** — Operator's Safari has `navigator.doNotTrack === '1'`. The tracker JS short-circuits at the very top (privacy-by-default per CLAUDE.md Privacy Rule #6):
   ```js
   if ("1" === navigator.doNotTrack || ... ) {
     statnive = { track: function(){}, identify: function(){} };
   }
   ```
   Both `track()` and `identify()` become no-ops. **The initial pageview is never sent.** This is correct behavior — but invisible to the operator unless they think to check.
3. **The fix we applied** — Tested in Chrome Incognito (default settings, no DNT). Pageviews flowed within 5 seconds.
4. **Preventive measure** — Cutover acceptance check explicitly says "test in Chrome Incognito or Firefox Private Window with no privacy extensions". Print `navigator.doNotTrack` in the operator's diagnostic output. Documented in the runbook's § Milestone N acceptance check.

### Lesson 17

**`_statnive` cookie set on `/api/event` responses needs GDPR review for SaaS posture.** [closed by PR-E2 — three configurable flags]

1. **What we did** — Confirmed binary works by sending an event via curl. Response included `set-cookie: _statnive=<UUID>; Max-Age=31536000; HttpOnly; SameSite=Lax`. Noted it; didn't investigate further during cutover.
2. **Why it broke** — Not a bug we hit; a privacy posture surfaced BY the cutover. CLAUDE.md says "Iran = cookies + user_id allowed; SaaS = GDPR applies to EU visitors". A 1-year HttpOnly visitor cookie may need consent gating in the SaaS posture even though it's privacy-preserving (random UUID, no PII, server-side rotation possible).
3. **The fix we applied** — Noted for follow-up review. No code change in the cutover.
4. **Preventive measure** — Three independently-toggleable server-side flags shipped via PR-E2 (Option C): `consent.required` (default `true`, requires `X-Statnive-Consent: given` to set the cookie or hash a `user_id`), `consent.respect_gpc` (default `true`, denies on `Sec-GPC: 1`), `consent.respect_dnt` (default `true`, denies on `DNT: 1`). Defaults are SaaS-safe; self-hosted Iran flips `required=false`; operators in jurisdictions without GPC/DNT legal weight may flip respect flags off but should pair with explicit in-product disclosure. Decision context archived in [`docs/privacy/cookie-posture.md`](docs/privacy/cookie-posture.md) for counsel review pre-Phase-11a.

### Lesson 18

**Don't paste credentials into chat / transcripts even temporarily. Rotate immediately if you do.**

1. **What we did** — Operator pasted the bootstrap admin password (`STATNIVE_BOOTSTRAP_ADMIN_PASSWORD` value generated by `step-b.sh § B.5`) into the cutover chat output to confirm the script worked. The 32-char password is now in the conversation transcript permanently.
2. **Why it broke** — Conversation logs persist (Anthropic-side + the operator's local logs + any chat-export artifacts). Exfiltration risk even if you trust the immediate channel — transcript could be referenced later, shared in a postmortem, or scraped by an aggregator.
3. **The fix we applied** — After verifying admin login worked, operator created a new admin via the SPA, disabled `ops@statnive.live`, commented out the `STATNIVE_BOOTSTRAP_*` envs in the systemd drop-in, restarted statnive-live, `shred -u`'d `/root/statnive-bootstrap-password.txt`. Net cost: ~10 minutes of operator time + one extra dashboard-side rotation.
4. **Preventive measure** — Cutover docs flag credentials with a DO NOT PASTE warning. `step-b.sh § B.5`'s summary block could refuse to print the password to stdout — write it to `/root/statnive-bootstrap-password.txt` (mode 0400) only and tell the operator "cat that file via SSH; never paste here". Eliminates the most common path to leakage.

---

## G. Pre-release validation

### Lesson 19

**Run `make release-fresh` locally end-to-end before pushing any `v*` tag — it is the only validated predictor of `release.yml`'s outcome.**

1. **What we did** — Pushed `v0.0.1-rc1` six times in one session. Each push triggered `release.yml`, surfaced one new gap, was fixed by a one-line PR, and the tag was deleted + re-pushed. Total cost: 7 PRs (#64 / #66 / #69 / #70 / #71 / #72 / #73 + this one), ~6 release.yml runs, ~30 minutes of CI minutes, ~2 hours of operator + Claude attention.
2. **Why it broke** — `make ci-local` (the local-CI mirror) does NOT include `airgap-bundle` + signing, and runs against a dev tree where `internal/dashboard/spa/dist/`, `web/dist/`, `bin/`, etc. already exist from prior runs. `release.yml` runs `make release` on a clean ubuntu-latest GHA runner with none of those caches. Every "works on dev because state is warm" gap surfaced one-at-a-time on the runner: parse-time `$(PKG)` race (Makefile evaluates `$(shell go list)` BEFORE `web-build` creates `dist/`), missing dev tools (semgrep / golangci-lint / govulncheck / go-licenses), token-budget caps tighter than current doc actuals, race-detector overhead invalidating the perf-budget assertion, and a self-targeting `mv build/SHA256SUMS.sig build/SHA256SUMS.sig` (no-op on macOS, exit-1 on GNU coreutils Linux).
3. **The fix we applied** — Added `make release-fresh` Makefile target that wipes `bin/`, `build/`, `internal/dashboard/spa/dist/`, `web/dist/` and then runs `make release`. Documented as the mandatory pre-tag step in `docs/runbook.md` § Phase 8 § Tagging. Same PR also fixed the `airgap-bundle.sh` self-`mv` bug surfaced by running it locally for the first time.
4. **Preventive measure** — Self-policed via runbook checklist; no automation possible without a server-side pre-receive hook (out of scope for v1). Cost is one local make invocation (~5 min on a warm box, ~10 min cold). Avoided cost is N PRs × CI minutes per release. Net savings positive after the second avoided whack-a-mole loop. **The rule:** if `make release-fresh` exits non-zero locally, do NOT push the tag — fix on a feature branch, merge, re-run `release-fresh`, then tag. Never debug the gate by re-tagging on the runner.

---

## H. Deploy-time probes & VPS prereqs

### Lesson 20

**`statnive-deploy`'s healthz probe must read host:port + scheme from `/etc/statnive-live/config.yaml`, not hardcode `http://127.0.0.1:8080`.**

1. **What we did** — Trusted `deploy-saas.yml`'s "Run on-box deploy" step to honestly report deploy success/failure for v0.0.1-rc1.
2. **Why it broke** — `/usr/local/bin/statnive-deploy` (built from `deploy/statnive-deploy.sh`) probed `http://127.0.0.1:8080/healthz` with a 30-s timeout. The binary listens on `0.0.0.0:443` with TLS per `server.listen` in `config.yaml`. Probe timed out → script reported "deploy failed" → auto-revert kicked in → revert probe also timed out → "manual intervention required." But the binary was healthy the whole time (verified out-of-band via `curl -ksS https://127.0.0.1/healthz` from the VPS itself: `status=ok`, `clickhouse=up`).
3. **The fix we applied** — `deploy/statnive-deploy.sh` now derives `HEALTHZ_URL` at runtime: `STATNIVE_HEALTHZ_URL` env wins; otherwise parse `server.listen` (rebinding `0.0.0.0` → `127.0.0.1`) + check `tls.cert_file` to choose `https://` (with `-k` to ignore cert-name mismatch on loopback) vs `http://`. Fallback to `http://127.0.0.1:8080/healthz` when `/etc/statnive-live/config.yaml` is unreadable. 4-scenario coverage: TLS prod / HTTP dev / env override / missing config — all green locally.
4. **Preventive measure** — Verification §63 (`docs/runbook.md` § Tagging a release / PLAN.md): integration test spins `bin/statnive-live` on a non-default port + scheme and asserts `statnive-deploy` derives the matching probe URL. Catches the bug class on any future config-shape change.

### Lesson 21

**`/etc/statnive/release-key.pub` is a one-time per-VPS prereq for GHA-driven deploys. Add `make ops-install-release-key` + a runbook checklist line; don't trust the comment-block prereq in `deploy-saas.yml`'s header.**

1. **What we did** — Configured GHA secrets (`STATNIVE_RELEASE_PRIVKEY`), pinned the matching pubkey at `deploy/keys/release-signing.pub`, triggered `deploy-saas.yml`. Bundle SCP succeeded; on-box `airgap-verify-bundle.sh` failed with `Ed25519 signature mismatch — REJECT`.
2. **Why it broke** — The on-box verifier reads `/etc/statnive/release-key.pub`. That file was never created. `deploy-saas.yml`'s top comment lists it as a prereq but no automation enforces it; first GHA-driven deploy on any new VPS hits this.
3. **The fix we applied** — `scp deploy/keys/release-signing.pub ops@<vps>:/tmp/release-key.pub` + `sudo install -d -m 0755 /etc/statnive && sudo install -m 0644 /tmp/release-key.pub /etc/statnive/release-key.pub`. Bundled into a new `make ops-install-release-key VPS_HOST=...` Makefile target so the SCP + install dance is one command and idempotent.
4. **Preventive measure** — `docs/runbook.md` § Phase 8 § Tagging now lists `make ops-install-release-key` as a one-time per-VPS step. Future enhancement (out of scope for this PR): `airgap-install.sh`'s post-install summary detects a missing pubkey and prints a reminder.

### Lesson 22

**`release.yml` cannot auto-fire `deploy-saas.yml` via the `release: published` trigger because `GITHUB_TOKEN`-created events don't start downstream workflows. Add an explicit `gh workflow run` step at the end of `release.yml`.**

1. **What we did** — Trusted `deploy-saas.yml`'s `on: release: types: [published]` trigger to fire when `release.yml` published the GitHub Release.
2. **Why it broke** — GitHub's recursion guard: events created by a workflow using `GITHUB_TOKEN` (e.g. `gh release create`) do NOT trigger other workflows, except `workflow_dispatch` and `repository_dispatch`. Documented at <https://docs.github.com/en/actions/using-workflows/triggering-a-workflow#triggering-a-workflow-from-a-workflow>.
3. **The fix we applied** — Manual `gh workflow run deploy-saas.yml -f version=v0.0.1-rc1` after each `release.yml` success was the workaround. The durable fix: a final `gh workflow run deploy-saas.yml --ref ${VERSION} -f version=${VERSION}` step at the end of `release.yml`'s `build + sign + publish` job + `actions: write` permission. The existing `release: published` trigger stays for back-compat with PAT-driven external automation.
4. **Preventive measure** — Verification §65: the next release tag must auto-fire `deploy-saas.yml` without manual `gh workflow run`. Replaces the manual workflow_dispatch step in the cutover SOP.

### Lesson 23

**`//go:embed` of an updated data file (`internal/enrich/crawler-user-agents.json`) shipped empty in the GHA-built v0.0.1-rc1 binary even though the bundle-completeness gate passed. Add a build-time embed-size assertion that CI can check + escalate the runtime log to ERROR.**

1. **What we did** — `internal/enrich/crawler-user-agents.json` was refreshed in PR-A to ~254 KB / 647 patterns. Local `make release-fresh` produced a binary that loaded 647 patterns. The GHA-built v0.0.1-rc1 binary running on the VPS logged `embedded crawler JSON empty or invalid; using fallback patterns` and runs with the 60-pattern fallback list.
2. **Why it broke** — Root cause unconfirmed as of this writeup. Two candidates: (a) `//go:embed` resolved at build time on the GHA runner read from a pre-refresh state somewhere in the cache chain (npm cache, Go cache, vendored copy), (b) bundle-completeness's pre-bundle size guard runs *after* the binary is compiled, so a build-time-empty file silently produces an empty embed even when the bundle's own copy of the file is full size. Investigation deferred — the regression is now caught regardless via the build-time check below.
3. **The fix we applied** — `internal/enrich/bot.go` exports `CrawlerEmbedBytes()` and `CrawlerEmbedMinBytes()` (100 KB floor). New `bin/statnive-live --check-embed-sizes` CLI flag prints all embed sizes + exits 1 if any fall below the floor; CI can call it after `make build-linux`. Runtime path stays graceful (fresh checkouts before `make refresh-bot-patterns` legitimately produce an empty embed) but logs ERROR-level (not WARN) with byte counts + LEARN.md cross-reference so operators can't miss it. Unit test `TestCrawlerEmbedSize` asserts the floor on every `make test`.
4. **Preventive measure** — Verification §64: CI step in the existing `airgap-bundle-verify` chain runs `bin/statnive-live --check-embed-sizes`. Future builds with stale-embed regressions fail loudly at bundle-verify time, not silently in production. Runtime FATAL was rejected because it would break legitimate fresh-checkout dev builds; the build-time gate is sufficient.

### Lesson 24

**Don't make a privacy-policy decision in the tracker bundle. The tracker can't be tuned per site or per jurisdiction; the binary can. A default-on tracker DNT/GPC short-circuit silently dropped 70-85% of legitimate Brave / Firefox-strict / Safari traffic from production dashboards because it ran *before* the POST.**

1. **What we did** — A customer (`wp-slimstat.com`) reported a ~88% under-count vs WP Analytics over 3 days (12 statnive visits vs ~100 actual). The diagnostic pivot was the `/metrics` drop-counter PR (#77): `received_total = 14` against expected ~100, with no `dropped{reason="*"}` buckets eating the difference. Conclusion: the loss happened *before* the server even saw the request. Code reading of `tracker/src/tracker.js:18-24` showed an IIFE-top short-circuit on `navigator.doNotTrack === '1' || navigator.globalPrivacyControl === true || navigator.webdriver === true || _phantom || callPhantom` that replaced `window.statnive` with no-ops. Brave (GPC always 1), Firefox-strict (DNT always 1), iOS Safari with Prevent Tracking, and Chrome with privacy extensions all hit this branch. Operator dashboards showed only the Chrome-without-extensions slice.
2. **Why it broke** — Privacy-by-default in the tracker is the wrong layer. The tracker bundle is one-size-fits-all (every operator gets the same JS), can't read per-site config, and runs *before* the POST so the server can't even count the visit anonymously. CLAUDE.md Privacy Rule 6 ("DNT + GPC respected by default on SaaS") was correct *as a posture*, but enforcing it client-side conflated two distinct concerns: should we count the visit (server can decide per-site), and should we hash identity (server already does via `consent.respect_gpc` / `consent.respect_dnt`).
3. **The fix we applied** — Removed the DNT/GPC checks from `tracker/src/tracker.js` (kept `webdriver` / `_phantom` — those are anti-automation, not privacy policy). The browser still attaches `DNT: 1` / `Sec-GPC: 1` headers automatically; the binary's existing `consent.respect_*` config flags are now the only enforcement path. Defaults flipped from `true` → `false` so every visit is counted by default; operators with EU visitors flip them on per their jurisdiction. Updated CLAUDE.md Privacy Rule 6, `docs/rules/privacy-detail.md` Rule 6 detail, and inverted the `tracker.test.mjs` privacy-short-circuit specs to assert POST fires under `DNT: 1` / `Sec-GPC: 1`.
4. **Preventive measure** — Two-layer defense. (a) Vitest spec at `tracker/test/tracker.test.mjs` § "DNT / Sec-GPC fire POST (server decides)" pins the new posture — any future "let's add a quick client-side check" reverts will fail this spec. (b) Operator-facing acceptance check in `docs/runbook.md` adds "scrape `/metrics`, confirm `received_total` matches WP-analytics visit count for the same window" — this is the diagnostic that surfaced the bug in the first place; institutionalize it. Per-site server toggles (`statnive.sites.respect_gpc UInt8 DEFAULT 0` + admin UI) are the v1.1 follow-up so multi-tenant operators can serve EU + non-EU customers from the same binary without re-editing config (deferred PR D2).

### Lesson 26

**`statnive-deploy` healthz probe URL was derived only from `/etc/statnive-live/config.yaml`, ignoring the systemd unit's `STATNIVE_*` env overrides — viper's AutomaticEnv wins over YAML at runtime, so the probe was hitting the wrong port and TLS scheme on every deploy.**

1. **What we did** — Tag-pushed `v0.0.1-rc3` (PR #78 + PR #79). Both `release.yml` and the GHA-triggered `deploy-saas.yml` ran. The on-box `statnive-deploy deploy v0.0.1-rc3` step swapped the symlink, restarted the service, then the new 90 s healthz probe (per Lesson 25) STILL timed out. Auto-revert fired, symlink rolled back to `v0.0.1-rc2`.
2. **Why it broke** — `derive_healthz_url` parsed `/etc/statnive-live/config.yaml` looking for `server.listen` + `tls.cert_file`. The shipped example file uses `server.addr` (LEARN.md Lesson 4 schema-drift survivor) so the awk parser found no match and fell through to the default `127.0.0.1:8080`. Then `cert_file:` matched the TLS branch, so the probe URL became `https://127.0.0.1:8080/healthz` — a port nothing was listening on. Meanwhile the binary read `STATNIVE_SERVER_LISTEN=0.0.0.0:443` from the systemd EnvironmentFile and bound to `:443`. The probe timed out for 90 s because no process answers `:8080` on the box. Operator confirmed by manually swinging the symlink + restarting the service: `/healthz` came back in 4 seconds on the right port.
3. **The fix we applied** — `derive_healthz_url` now also reads `STATNIVE_SERVER_LISTEN` / `STATNIVE_TLS_CERT_FILE` from the inherited env first, then from `/etc/systemd/system/statnive-live.service.d/env.conf` if not set in the running shell, before falling back to the YAML parse. The lookup order is now: env-var override (`STATNIVE_HEALTHZ_URL`) → systemd unit env → YAML → hardcoded fallback. Probe URL on this Netcup VPS now correctly resolves to `https://127.0.0.1:443/healthz`.
4. **Preventive measure** — Two layers. (a) The systemd-env reading in the script so future deploys on Netcup-class boxes (where YAML is mostly cosmetic and env wins) probe the right URL automatically. (b) Operator-facing acceptance: after any deploy, `gh run view <id> --log-failed | grep "healthz timed out"` should find nothing — if it does, the script's URL derivation has drifted again.

### Lesson 25

**`statnive-deploy` healthz probe budget at 30 s is too tight for the Netcup VPS 2000 G12 cold-start path. Auto-revert fires on a clean deploy, leaving production on the previous symlink. Bump to 90 s; operators on faster boxes can override via env.**

1. **What we did** — Tag-pushed `v0.0.1-rc2`, watched `release.yml` build + sign + publish + fire `deploy-saas.yml`. The on-box `statnive-deploy deploy "v0.0.1-rc2"` step ran, atomic-symlink-swapped to the new bundle, restarted `statnive-live.service`, and waited on `/healthz`. After 30 s the probe timed out and `statnive-deploy` auto-reverted: symlink rolled back to the previous bundle (which happened to be `v0.0.0-dev` from a manual install), the new bundle stayed unpacked but inactive. Operator only noticed because `/metrics` (newly added in PR #77) returned 404 against production.
2. **Why it broke** — Netcup VPS 2000 G12 NUE D1 takes ~35-50 s on cold start to (a) bind TLS via the manual PEM files (Phase 2a wiring), (b) replay any pending events from the WAL, (c) connect to ClickHouse + run pending migrations, (d) load the GeoIP DB into memory. The 30 s healthz probe was sized for the dev laptop, not for the 8c/16GB shared-tenant VPS. Verified by running the binary by hand on the VPS — it came up clean, just past the 30 s deadline.
3. **The fix we applied** — Manually swung the symlink (`/opt/statnive-live/current` → `v0.0.1-rc2`) + `install -m 0755`d the new binary, restarted `statnive-live.service`, confirmed `/api/about` returned the new git_sha, confirmed `/metrics` returned the four counter blocks. Production then ran rc2 content despite the failed deploy.
4. **Preventive measure** — Bumped `HEALTHZ_TIMEOUT_S` default from 30 → 90 in `deploy/statnive-deploy.sh`. Operators on faster boxes (Hetzner AX42, Asiatech P5) can override via the `STATNIVE_HEALTHZ_TIMEOUT_S` env var to keep the probe tight. The next tag-push (`v0.0.1-rc3`, shipping PR D + this fix) is the validation — if rc3 deploys cleanly without manual intervention, the bump is right-sized; if it still flags, raise to 120 s and add a deploy-saas-only `wait-for-binary` step that polls `/api/about` for `git_sha` match instead of the binary 200/timeout.

### Lesson 27

**`/metrics received_total` only counts events that reach the binary; sendBeacon / TLS / network / browser-block failures upstream are *invisible* server-side. When a customer reports under-count, view-source the customer's HTML before chasing server-side drops.**

1. **What we did** — Customer wp-slimstat.com reported pageview under-count vs GA4. We snapshotted `/metrics` (received_total, accepted{site_id=5}, dropped{reason=...}) and tried to reason about where the loss happened from those numbers alone. First diagnosis hypothesized a TLS-SAN mismatch on the apex `statnive.live`; the proposed fix plan was approved.
2. **Why it broke** — The first hypothesis was wrong. Re-running the same TLS probe a few minutes later returned 204 cleanly: the original "TLS hang" was a transient network blip, not a config issue. Meanwhile the cert already covered all three SAN names (`app.statnive.live`, `demo.statnive.live`, `statnive.live`, valid through Jul 2026). The actual root cause was a tracker-side `async defer` race losing Safari/Chrome fast-bouncers — entirely *upstream* of the binary's counter, so `received_total` could never have surfaced it. We learned this only after view-sourcing wp-slimstat.com's HTML and observing the `<script>` tag attributes + capture-by-browser breakdown in `events_raw` (Firefox 46% vs Chrome 7% vs Safari 2%).
3. **The fix we applied** — PR #87 added a `pagehide` backstop + `pageviewed` sentinel to `tracker/src/tracker.js` so any tracker that DID load fires the pageview before unload. Bundle stayed inside the 1500 B / 750 B gz budget (1466 / 744 final). 6 new Vitest cases pin the contract.
4. **Preventive measure** — Diagnostic discipline: when the customer-reported under-count gap exceeds 50%, the procedure is (a) view-source the customer's HTML and confirm the tracker tag's `src` + `data-statnive-endpoint`, (b) load the customer's site in a real Safari/Chrome session with DevTools open and watch the Network tab for the beacon, (c) ONLY THEN look at server-side `/metrics`. Codified in `docs/runbook.md` § "Customer under-count diagnostic" as a 3-step bullet list before the existing `/metrics` snapshot procedure.

### Lesson 28

**Default `scp` to Netcup VPS 2000 G12 NUE D1 runs at ~16 KB/s; `scp -C` runs at ~3 MB/s on the same path. Use `-C` for any file >10 MB; encode in operator runbook.**

1. **What we did** — `scp /tmp/IP2LOCATION-LITE-DB23.BIN ops@94.16.108.78:/tmp/IP2LOCATION-LITE-DB23.BIN` (95 MB BIN file). After 13 minutes, only 13 MB had transferred (~16 KB/s). Killed the transfer, re-ran with `scp -C` and the same 95 MB finished in ~30 seconds.
2. **Why it broke** — Netcup's host-to-customer-VPS network path appears to throttle non-compressed bulk SCP. The IP2Location BIN compresses well (b-tree-like structure with repeating IP ranges + city names), so SSH zlib compression turns the bottleneck into a non-issue. Same effect on any bulk asset transfer to that box.
3. **The fix we applied** — Re-run with `scp -C -o Compression=yes`. Operator-only fix; no code change.
4. **Preventive measure** — `docs/runbook.md` § "Operator transfers" updated with: "**ALWAYS use `scp -C` to the Netcup SaaS box.** Default is too slow to be usable for files >10 MB." Future bundle distributions (e.g. updated GeoIP DB monthly) inherit this rule.

### Lesson 29

**`airgap-update-geoip.sh` rejects `/tmp` source on Netcup because the atomic-mv guard requires same-filesystem as `/etc/`. Use `/var/tmp` as the staging path.**

1. **What we did** — SCP'd the new IP2Location BIN to `/tmp/IP2LOCATION-LITE-DB23.BIN`, ran `sudo /opt/statnive-bundles/.../deploy/airgap-update-geoip.sh /tmp/IP2LOCATION-LITE-DB23.BIN`. Script exited 1 with `update-geoip: new BIN is on a different filesystem than /etc/statnive-live/geoip — \`mv\` would not be atomic`.
2. **Why it broke** — The script intentionally enforces same-filesystem source so `mv` is atomic — a partial-file race during swap would have statnive-live read a half-written BIN and OOM / mis-resolve. On the Netcup VPS 2000 G12 NUE D1, `/tmp` is on a tmpfs separate from `/` (where `/etc` lives), so the source-must-share-fs check fails. The script's error message is helpful but the runbook didn't tell the operator where the right staging path is.
3. **The fix we applied** — `mv /tmp/IP2LOCATION-LITE-DB23.BIN /var/tmp/IP2LOCATION-LITE-DB23.BIN` (same-fs as `/etc`), re-ran the script, swap + SIGHUP succeeded.
4. **Preventive measure** — `docs/runbook.md` § "Refresh GeoIP DB" updated to direct operators to SCP into `/var/tmp/` (not `/tmp/`). Future enhancement (out of scope this PR): the script could detect the cross-fs case and offer to `cp` the file to `/var/tmp/` itself before moving — the safety guarantee survives because the `cp` happens before the atomic step.

### Lesson 30

**IP2Location LITE BINs (DB1/3/5/9/11) emit a verbatim 80-char "This parameter is unavailable for selected data file. Please upgrade the data file." string for fields not in the BIN — *not* `"-"`. Without filtering, that string lands in `events_raw.isp` / `events_raw.carrier` for every event.**

1. **What we did** — Installed IP2LOCATION-LITE-DB11.BIN on production (DB23 is the paid commercial tier; LITE only goes up to DB11 which omits ISP + Mobilebrand). Confirmed `country_code` / `province` / `city` populate correctly. Did NOT initially filter the LITE-tier "missing field" sentinel — `cleanGeoField` only stripped the standard `"-"` IP2Location no-record marker. Result: every event wrote the 80-char message into `events_raw.isp` and `events_raw.carrier`.
2. **Why it broke** — The upstream `ip2location-go/v9` library emits the "This parameter is unavailable..." string from an unexported `not_supported` constant at `vendor/github.com/ip2location/ip2location-go/v9/ip2location.go:219`. It's the library's signal "this BIN tier doesn't carry this field" but it looks nothing like the standard `"-"` no-record marker. Our `cleanGeoField` had no awareness of this LITE-vs-commercial split and let the sentinel pass through.
3. **The fix we applied** — PR #88 (`fix(enrich): filter IP2Location LITE 'parameter unavailable' sentinel`) — `cleanGeoField` now full-equality-matches against a named const `ip2locationUnavailableSentinel` mirroring the upstream constant. Anchored equality (vs `HasPrefix`) is intentional: a future library reword surfaces as junk in `events_raw` (loud failure, easy to grep), not silent over-match. 8 table-driven test cases in `internal/enrich/cleangeo_internal_test.go` including a regression for "This Mobile Co" passing through unchanged.
4. **Preventive measure** — Whenever bumping `ip2location-go` library version: re-verify the sentinel string at `vendor/github.com/ip2location/ip2location-go/v9/ip2location.go:219` against the const in `internal/enrich/geoip.go`. If the library has reworded the message, the existing tests will pass (they test our const) but production data will hold the new wording — which is how we want it to fail (loud, in-data, grep-findable).

---

## J. Tracker bundle hygiene

### Lesson 31

**The SaaS tracker's `<script src="app.statnive.live/tracker.js">` IIFE used `window.statnive` as both its de-duplication guard AND its public API. The free WP plugin (a separate same-brand product some customers also run) installs a `window.statnive` queue stub from same-origin before any cross-origin script resolves. Net effect on co-installed pages: tracker.js HTTP 200 in DevTools, zero `/api/event` POSTs ever fire. Renamed SaaS public surface to `window.statniveLive` so the namespace matches the product domain and cannot collide with the WP plugin.**

1. **What we did** — wp-slimstat.com (SaaS customer, also the maintainer of the free WP plugin) reported pageviews at ~1–22% of GA4-attested truth across a week, browser-skewed (Firefox 46%, Edge 9%, Chrome 7%, Safari 2%). PR #87 (`pagehide` backstop for the async/defer fast-bouncer race) shipped 2026-05-04 and didn't move the number. The new symptom diagnosed live with Playwright MCP + the Claude Code Chrome extension: `tracker.js` loads HTTP 200 on every visit, `/api/event` POST count is exactly 0, `typeof window.statnive === 'function'` (queue stub from the WP plugin), `typeof window.statniveLive === 'undefined'`. Pushing `history.pushState({}, '', '/?probe=…')` did not fire a beacon either — the SaaS IIFE never reached the line that wraps `pushState`.
2. **Why it broke** — `tracker/src/tracker.js` line 24 read `if (w.statnive) return;` as a self-de-duplication guard, then line 93 set `w.statnive = { track, identify }` as the customer-facing API. The free WP plugin at `wp-content/plugins/statnive/resources/tracker/tracker.js:21-23` installs a queue stub `window.statnive = window.statnive || function() { (window.statnive.q = window.statnive.q || []).push(arguments); };` — same-origin, parsed before the cross-origin SaaS tracker resolves. The SaaS guard fires, IIFE returns, no listeners installed. The bundle is fully parsed but does nothing. The earlier "browser-specific capture rate" reading was largely a *WP-plugin-first race* (whether the plugin's tracker had finished evaluating before the SaaS one), not the async/defer fast-bouncer race PR #87 was sized for.
3. **The fix we applied** — Renamed the SaaS tracker's public API from `window.statnive` → `window.statniveLive` (PR-Z, in v0.0.1-rc6). Three references in `tracker/src/tracker.js`, plus all assertions in `tracker/test/tracker.test.mjs` and `tracker/test/payload-golden.test.mjs`, plus the install snippets in `tracker/README.md` and `docs/runbook.md`, plus the JSDoc reference in `internal/ingest/event.go:31`. New Vitest case `wp-plugin global collision` pre-sets `window.statnive = function() {...}` before evaluating the IIFE and asserts `pageview()` still fires and the foreign stub remains untouched. Bundle 1466→1478 min / 744→747 gz against 1500/750 budget.
4. **Preventive measure** — Two layers. (a) The new Vitest regression test (`describe('wp-plugin global collision', …)` in `tracker/test/tracker.test.mjs`) catches any future re-introduction of a `window.statnive`-flavored collision guard at PR time. (b) A note in [.claude/skills/preact-signals-bundle-budget/](.claude/skills/preact-signals-bundle-budget/README.md) tracker contract: "the tracker bundle's public-API global MUST be `statniveLive` (or another name unique to `statnive-live`); never `statnive`, which is owned by the WP plugin product on `wordpress.org/plugins/statnive`. Two same-brand products on one page is supported and tested." Operator-facing acceptance: when investigating a customer under-count, after `/metrics` the next probe is `view-source:` + DevTools console `({ kind: typeof window.statnive, hasTrack: typeof window.statnive?.track === 'function', liveExists: typeof window.statniveLive !== 'undefined' })` — three values, three minutes, lock the bug class before chasing server-side drops.

---

## K. Schema migrations

### Lesson 32

**ClickHouse syntax: `FROM table FINAL alias` is rejected; `FROM table AS alias FINAL` parses. And plain MergeTree tables refuse `FINAL` at all — only Replacing/Aggregating/Collapsing variants accept it.**

1. **What we did** — Migration 010 (`user_sites` junction table for v0.0.9 per-site-admin) wrote a backfill `INSERT … SELECT u.user_id, u.site_id, u.role FROM statnive.users FINAL u WHERE u.disabled = 0` mirroring the in-Go SQL shape from `internal/auth/store.go:GetUserByEmail`. That Go query has no alias; mine did. CH rejected with `Syntax error: failed at position 180 ('u'), Expected one of: SAMPLE, table, JOIN, OpeningRoundBracket, PREWHERE, …`. Switched to `FROM statnive.users AS u FINAL`, ran. Then the operator-bootstrap CROSS JOIN tripped a second error: `Storage MergeTree doesn't support FINAL`. The `sites` table is plain MergeTree (per migration 001) — the engine simply does not implement FINAL semantics; only Replacing/Aggregating variants do.
2. **Why it broke** — Two distinct CH grammar surfaces, both subtly different from the SQL you'd write against PostgreSQL or MySQL. (1) `FINAL` is a table-side modifier, not a keyword in the FROM-list — the parser wants `FROM tbl AS alias FINAL`, not `FROM tbl FINAL alias`. (2) Whether `FINAL` is *valid* at all depends on the storage engine. Plain MergeTree has no "merge-resolution" pass — `FINAL` is a no-op-friendly default in ReplacingMergeTree (latest by ORDER BY tuple wins), but plain MergeTree has no such row-collapse policy and the optimizer rejects the modifier rather than silently doing nothing.
3. **The fix we applied** — (a) For Replacing tables (`users`), switched alias position to `FROM statnive.users AS u FINAL`. (b) For plain MergeTree tables (`sites`), removed `FINAL` entirely — the sites table is functionally append-only by site_id (per `internal/sites/sites.go:ListAdmin` comment "statnive.sites is plain MergeTree — FINAL is rejected. Duplicate rows can only appear if migration 001 changes engines"). The integration test in `test/admin_user_sites_e2e_test.go:TestMigration010_OperatorBootstrap_GrantsAllSites` now pins this — any future migration that re-introduces `FROM statnive.sites FINAL` fails CI before it reaches prod.
4. **Preventive measure** — (a) When writing a migration, check the engine of every table you read. Look at `system.tables.engine` or grep `ENGINE = ` in the migration that created the table. (b) For aliased CH reads, always use `FROM table AS alias FINAL` — never `FROM table FINAL alias` or `FROM table alias FINAL`. (c) The pattern `FROM statnive.users AS u FINAL` is now the canonical reference shape; copy it from existing migrations rather than retyping. (d) Integration tests for every new migration MUST apply against a fresh CH (via `make ch-up` + a per-test database) before tag-push; the test harness in `test/admin_user_sites_e2e_test.go` is the template.

### Lesson 33

**Migration templates that substitute config values into SQL string literals MUST validate the config value at boot time. The operator-bootstrap row in migration 010 (`WHERE u.email = '{{.OperatorEmail}}'`) is rendered directly into SQL, so a crafted YAML value `'; DROP TABLE users; --` would inject. Config-loader must reject anything that doesn't look like a plain email.**

1. **What we did** — Migration 010 introduced an operator-bootstrap branch that grants the operator (matched by email) admin on every enabled site. The email comes from `admin.operator_email` in YAML / `STATNIVE_ADMIN_OPERATOR_EMAIL` env. The migration template substitutes it as a SQL string literal. First draft of the config loader took the value verbatim — no validation.
2. **Why it broke** — In principle a malicious operator config value containing `'` or `;` would break out of the SQL literal and execute arbitrary SQL inside the migration runner's transaction. The threat is small (the operator already controls the binary's host) but the bug class is real: any future migration template that substitutes config values without escaping has the same shape. Better to gate at the config-loader so the bug class can't reappear.
3. **The fix we applied** — Added `operatorEmailRegex` in `cmd/statnive-live/main.go` (`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`). The config loader rejects any non-empty `admin.operator_email` that doesn't match. Boot fails loudly with `admin.operator_email %q: must be a plain email address`. Empty value is still allowed — disables the bootstrap, valid for dev / CI / fresh deploys. Unit test in `internal/storage/migrate_test.go:TestRenderMigration_010_OperatorEmail` pins the rendered SQL contains the literal email substring (so future migration rewrites that drop the substitution accidentally fail the gate).
4. **Preventive measure** — Workflow rule: any new migration template that substitutes a config value MUST validate the config value at the loader. The regex doesn't need to be RFC 5321-grade — just tight enough to reject `'` `;` `--` `/*` and similar SQL-injection bytes. Audit checklist when reviewing a new migration: (a) does the template contain `{{...}}` substitutions? (b) for each, where is the value sourced? (c) is there a boot-time validator that rejects the SQL-injection-shaped values? Three minutes of due diligence at PR review beats a CH-side incident.

---

## How to add a new lesson

When a cutover (Milestone N) completes, an outage is resolved, or a bug-discovery session catches ≥3 related bugs:

1. Pick the right category (A–F above; add a new category if genuinely new ground).
2. Number the lesson sequentially within the category — never reuse a number, even after `[obsolete]` markings.
3. Use the four-part format (what / why / fix / preventive measure).
4. Add an Index entry at the top.
5. PR-review checklist: does the lesson tell a future operator how to NOT repeat this? If yes, ship.

Cross-link from `PLAN.md` cutover-postmortem sections so the bug catalog and the lesson catalog stay in lockstep.
