#!/usr/bin/env bash
# dpi-rst.sh — Iranian DPI injecting RST on long-lived connections.
#
# Uses conntrack to find ESTABLISHED TCP conns > 60 s old and snaps
# them with iptables REJECT --reject-with tcp-reset. The binary should
# accept the RST and re-handshake (Go's net/http auto-reconnects).
# This is the canonical "long-lived HTTP/2 connection gets killed mid-
# stream" scenario.

# shellcheck disable=SC1091
. "$(dirname "$0")/_lib.sh"

chaos_init dpi-rst "$@"

_apply() {
	# Conntrack-based age match needs the `conntrack` kernel module.
	if ! lsmod 2>/dev/null | grep -q nf_conntrack; then
		chaos_apply modprobe nf_conntrack
	fi

	chaos_apply iptables -N STATNIVE_CHAOS_DPI 2>/dev/null || true
	chaos_apply iptables -F STATNIVE_CHAOS_DPI
	# Only RST established conns older than 60s. conntrack's --ctexpire
	# semantics + ctstate ESTABLISHED give the equivalent of "long-
	# lived" without the kernel needing per-flow timing.
	chaos_apply iptables -A STATNIVE_CHAOS_DPI \
		-p tcp \
		-m conntrack --ctstate ESTABLISHED \
		-m connlimit --connlimit-above 0 \
		-m statistic --mode random --probability 0.20 \
		-j REJECT --reject-with tcp-reset
	chaos_apply iptables -I FORWARD -j STATNIVE_CHAOS_DPI
	chaos_apply iptables -I OUTPUT  -j STATNIVE_CHAOS_DPI
}

_restore() {
	chaos_assert_sentinel
	chaos_apply iptables -D FORWARD -j STATNIVE_CHAOS_DPI 2>/dev/null || true
	chaos_apply iptables -D OUTPUT  -j STATNIVE_CHAOS_DPI 2>/dev/null || true
	chaos_apply iptables -F STATNIVE_CHAOS_DPI 2>/dev/null || true
	chaos_apply iptables -X STATNIVE_CHAOS_DPI 2>/dev/null || true
	chaos_clear_sentinel
}

case "$MODE" in
	restore)          _restore ;;
	apply|autonomous) _apply; chaos_done ;;
esac
