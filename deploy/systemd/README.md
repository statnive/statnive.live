# deploy/systemd — production unit file

Hardened `systemd` unit for the `statnive-live` binary. The directives
ship the options prescribed in
[`docs/rules/security-detail.md`](../../docs/rules/security-detail.md#rule-12--systemd-hardening-full-option-list):
`NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`,
`CapabilityBoundingSet=CAP_NET_BIND_SERVICE`, `SystemCallFilter=@system-service`,
and the namespace/syscall isolation rules. The unit targets
`systemd-analyze security` rating ≤ 1.5.

## Install

```bash
# 1. Create the unprivileged service user.
sudo useradd --system --no-create-home --shell /usr/sbin/nologin statnive

# 2. Create the data + log + config directories. ReadWritePaths in the
#    unit file points at these two; everything else is read-only.
sudo install -d -o statnive -g statnive -m 0750 /var/lib/statnive-live /var/log/statnive-live
sudo install -d -o root -g statnive -m 0750 /etc/statnive-live

# 3. Drop the binary, config, and unit file into place.
sudo install -m 0755 statnive-live /usr/local/bin/statnive-live
sudo install -m 0640 -o root -g statnive config/statnive-live.yaml /etc/statnive-live/config.yaml
sudo install -m 0644 deploy/systemd/statnive-live.service /etc/systemd/system/

# 4. Reload + enable + start.
sudo systemctl daemon-reload
sudo systemctl enable --now statnive-live

# 5. Verify.
sudo systemctl status statnive-live
sudo systemd-analyze security statnive-live.service   # expect <= 1.5
sudo journalctl -fu statnive-live                     # follow stdout/stderr
```

## SIGHUP reload

`ExecReload=/bin/kill -HUP $MAINPID` triggers the in-process fan-out at
[`cmd/statnive-live/main.go`](../../cmd/statnive-live/main.go):
TLS PEM reload → audit-log reopen → channel-mapper reload → goals snapshot
reload. Each subsystem fails independently, so a bad TLS swap doesn't
block channel or goals reload. Operator drill:

```bash
sudo vi /etc/statnive-live/config.yaml          # edit config
sudo systemctl reload statnive-live             # fan out SIGHUP
sudo journalctl -fu statnive-live | grep reload # expect four reload audit events
```

For logrotate: rotate then `systemctl reload statnive-live` — the audit
log `O_APPEND` reopen picks up the new file atomically.

## Verification

`deploy/systemd/harden-verify.sh` greps the unit file and asserts the
full hardening directive set is present. No systemd required — runs in
CI. Invoke directly:

```bash
bash deploy/systemd/harden-verify.sh deploy/systemd/statnive-live.service
```

Exit 0 on all-present; exit 1 with `FAIL:` lines listing each missing
directive.
