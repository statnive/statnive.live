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

### Lesson 16

**Operators with DNT='1' in their daily browser silently zero the tracker. Test in clean Chrome incognito.**

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

**`_statnive` cookie set on `/api/event` responses needs GDPR review for SaaS posture.**

1. **What we did** — Confirmed binary works by sending an event via curl. Response included `set-cookie: _statnive=<UUID>; Max-Age=31536000; HttpOnly; SameSite=Lax`. Noted it; didn't investigate further during cutover.
2. **Why it broke** — Not a bug we hit; a privacy posture surfaced BY the cutover. CLAUDE.md says "Iran = cookies + user_id allowed; SaaS = GDPR applies to EU visitors". A 1-year HttpOnly visitor cookie may need consent gating in the SaaS posture even though it's privacy-preserving (random UUID, no PII, server-side rotation possible).
3. **The fix we applied** — Noted for follow-up review. No code change in the cutover.
4. **Preventive measure** — Add a `consent_required: bool` config flag (`STATNIVE_CONSENT_REQUIRED`). When `true`, gate the `_statnive` cookie behind a consent decision (request header / first-party-cookie banner integration). Default `true` for the SaaS binary, `false` for self-hosted Iran. Decide before Phase 11a (first public signup).

### Lesson 18

**Don't paste credentials into chat / transcripts even temporarily. Rotate immediately if you do.**

1. **What we did** — Operator pasted the bootstrap admin password (`STATNIVE_BOOTSTRAP_ADMIN_PASSWORD` value generated by `step-b.sh § B.5`) into the cutover chat output to confirm the script worked. The 32-char password is now in the conversation transcript permanently.
2. **Why it broke** — Conversation logs persist (Anthropic-side + the operator's local logs + any chat-export artifacts). Exfiltration risk even if you trust the immediate channel — transcript could be referenced later, shared in a postmortem, or scraped by an aggregator.
3. **The fix we applied** — After verifying admin login worked, operator created a new admin via the SPA, disabled `ops@statnive.live`, commented out the `STATNIVE_BOOTSTRAP_*` envs in the systemd drop-in, restarted statnive-live, `shred -u`'d `/root/statnive-bootstrap-password.txt`. Net cost: ~10 minutes of operator time + one extra dashboard-side rotation.
4. **Preventive measure** — Cutover docs flag credentials with a DO NOT PASTE warning. `step-b.sh § B.5`'s summary block could refuse to print the password to stdout — write it to `/root/statnive-bootstrap-password.txt` (mode 0400) only and tell the operator "cat that file via SSH; never paste here". Eliminates the most common path to leakage.

---

## How to add a new lesson

When a cutover (Milestone N) completes, an outage is resolved, or a bug-discovery session catches ≥3 related bugs:

1. Pick the right category (A–F above; add a new category if genuinely new ground).
2. Number the lesson sequentially within the category — never reuse a number, even after `[obsolete]` markings.
3. Use the four-part format (what / why / fix / preventive measure).
4. Add an Index entry at the top.
5. PR-review checklist: does the lesson tell a future operator how to NOT repeat this? If yes, ship.

Cross-link from `PLAN.md` cutover-postmortem sections so the bug catalog and the lesson catalog stay in lockstep.
