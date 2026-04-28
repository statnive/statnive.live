#!/usr/bin/env bash
# Should trigger chaos-script-up-down-status — no source of _lib.sh,
# no scenario_up function. Just one ad-hoc command, no idempotent
# subcommand surface.
set -euo pipefail
iptables -A OUTPUT -j DROP
echo "applied"