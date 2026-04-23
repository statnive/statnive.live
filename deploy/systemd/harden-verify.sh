#!/usr/bin/env bash
# deploy/systemd/harden-verify.sh — asserts the systemd unit ships with
# every hardening directive prescribed in docs/rules/security-detail.md.
#
# Runs in CI without privileged access (grep-only — does NOT invoke
# systemd-analyze). Operators with systemd should additionally run
# `systemd-analyze security statnive-live.service` on the deployed host
# and confirm the rating is <= 1.5.

set -euo pipefail

UNIT="${1:-deploy/systemd/statnive-live.service}"

if [ ! -f "$UNIT" ]; then
  echo "FAIL: unit file not found at $UNIT" >&2
  exit 1
fi

# Required directive=value pairs from docs/rules/security-detail.md:45-73.
REQUIRED=(
  "NoNewPrivileges=yes"
  "ProtectSystem=strict"
  "ProtectHome=yes"
  "PrivateTmp=yes"
  "PrivateDevices=yes"
  "CapabilityBoundingSet=CAP_NET_BIND_SERVICE"
  "AmbientCapabilities=CAP_NET_BIND_SERVICE"
  "RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX"
  "ProtectKernelTunables=yes"
  "ProtectKernelModules=yes"
  "ProtectKernelLogs=yes"
  "ProtectControlGroups=yes"
  "SystemCallFilter=@system-service"
  "SystemCallErrorNumber=EPERM"
  "ExecReload=/bin/kill -HUP \$MAINPID"
  "Restart=always"
)

fail=0
for directive in "${REQUIRED[@]}"; do
  if ! grep -Fq "$directive" "$UNIT"; then
    echo "FAIL: missing '$directive'" >&2
    fail=1
  fi
done

# Catch common regressions.
if grep -Eq '^User=root' "$UNIT"; then
  echo "FAIL: User=root — must be a dedicated unprivileged user" >&2
  fail=1
fi
if ! grep -Eq '^User=' "$UNIT"; then
  echo "FAIL: missing User= directive" >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "harden-verify: $UNIT FAILED — fix the directives above" >&2
  exit 1
fi

echo "harden-verify: $UNIT OK (all ${#REQUIRED[@]} directives present)"
