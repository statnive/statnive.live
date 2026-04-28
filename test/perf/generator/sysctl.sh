#!/usr/bin/env bash
# Apply doc 29 §3.2 kernel tuning to a generator host.
# Idempotent — re-running is safe; values overwrite via `sysctl -w`.
#
# Persists across reboot via /etc/sysctl.d/99-statnive-load-gate.conf.
# Run as root on every Asiatech generator VPS before joining the fleet.
#
#   sudo ./test/perf/generator/sysctl.sh
#
# Reverse via:
#   sudo rm /etc/sysctl.d/99-statnive-load-gate.conf && sudo sysctl --system
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
    echo "FAIL: must run as root (sysctl -w requires CAP_SYS_ADMIN)" >&2
    exit 1
fi

# Doc 29 §3.2 — kernel knobs for high-EPS HTTP/1.1 generator workload.
# Values pinned for reproducibility; bump in the same PR as the doc edit.
declare -A KNOBS=(
    [net.core.somaxconn]=65535
    [net.ipv4.tcp_max_syn_backlog]=65535
    [net.ipv4.tcp_tw_reuse]=1
    [net.ipv4.tcp_fin_timeout]=15
    [net.ipv4.ip_local_port_range]="1024 65535"
    [net.core.netdev_max_backlog]=16384
    [fs.file-max]=1048576
)

CONF=/etc/sysctl.d/99-statnive-load-gate.conf
: >"$CONF"
for k in "${!KNOBS[@]}"; do
    v="${KNOBS[$k]}"
    sysctl -w "$k=$v"
    printf '%s = %s\n' "$k" "$v" >>"$CONF"
done

# ulimit -n is not a sysctl; bump via systemd drop-in or /etc/security/limits.conf
# at provision time. Documented here because doc 29 §3.2 ties the two together.
cat <<'EOF'

NOTE: ulimit -n must be raised separately (sysctl can't set per-process fd).
    Add LimitNOFILE=1048576 to the generator's systemd unit, OR edit
    /etc/security/limits.conf:

        *  soft  nofile  1048576
        *  hard  nofile  1048576

    Verify with `ulimit -n` after a fresh login.
EOF

echo "sysctl: applied $(wc -l < "$CONF") knobs to $CONF"
