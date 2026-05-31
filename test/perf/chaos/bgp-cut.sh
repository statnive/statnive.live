#!/usr/bin/env bash
# bgp-cut.sh — Iranian BGP cut from international peering.
#
# Simulates Asiatech ↔ Tehran-IX going down: all outbound to
# non-loopback (and inbound from non-loopback) drops at the kernel.
# Inside-Iran connectivity assumed already partitioned at the upstream
# carrier — we just snap egress.
#
# Loopback is preserved so the binary's CH client + dashboard still
# reach 127.0.0.1; SSH source IP is preserved so the operator doesn't
# lock themselves out.

# shellcheck disable=SC1091
. "$(dirname "$0")/_lib.sh"

chaos_init bgp-cut "$@"

_apply() {
	# Allow loopback + SSH-source IP (best-effort discovery from $SSH_CONNECTION).
	local ssh_src=""
	if [ -n "${SSH_CONNECTION:-}" ]; then
		ssh_src="$(echo "$SSH_CONNECTION" | awk '{print $1}')"
	fi

	chaos_apply iptables -N STATNIVE_CHAOS_BGP 2>/dev/null || true
	chaos_apply iptables -F STATNIVE_CHAOS_BGP
	chaos_apply iptables -A STATNIVE_CHAOS_BGP -o lo -j RETURN
	chaos_apply iptables -A STATNIVE_CHAOS_BGP -i lo -j RETURN

	if [ -n "$ssh_src" ]; then
		chaos_apply iptables -A STATNIVE_CHAOS_BGP -d "$ssh_src" -j RETURN
		chaos_apply iptables -A STATNIVE_CHAOS_BGP -s "$ssh_src" -j RETURN
	fi

	chaos_apply iptables -A STATNIVE_CHAOS_BGP -j DROP
	chaos_apply iptables -I OUTPUT -j STATNIVE_CHAOS_BGP
	chaos_apply iptables -I INPUT  -j STATNIVE_CHAOS_BGP
}

_restore() {
	chaos_assert_sentinel
	chaos_apply iptables -D OUTPUT -j STATNIVE_CHAOS_BGP 2>/dev/null || true
	chaos_apply iptables -D INPUT  -j STATNIVE_CHAOS_BGP 2>/dev/null || true
	chaos_apply iptables -F STATNIVE_CHAOS_BGP 2>/dev/null || true
	chaos_apply iptables -X STATNIVE_CHAOS_BGP 2>/dev/null || true
	chaos_clear_sentinel
}

case "$MODE" in
	restore)         _restore ;;
	apply|autonomous) _apply; chaos_done ;;
esac
