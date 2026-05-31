#!/usr/bin/env bash
# asiatech-outage.sh — full Asiatech DC partition (no in or out).
#
# More aggressive than bgp-cut: blocks ALL non-loopback (no Tehran-IX
# carve-out, no internal-IR carve-out). Tests the WAL's behaviour under
# total isolation — events written from the operator-local SSH session
# accumulate, the binary keeps serving, and on restore the consumer
# drains.

# shellcheck disable=SC1091
. "$(dirname "$0")/_lib.sh"

chaos_init asiatech-outage "$@"

_apply() {
	# Preserve operator SSH so they're not locked out.
	local ssh_src=""
	if [ -n "${SSH_CONNECTION:-}" ]; then
		ssh_src="$(echo "$SSH_CONNECTION" | awk '{print $1}')"
	fi

	chaos_apply iptables -N STATNIVE_CHAOS_DC 2>/dev/null || true
	chaos_apply iptables -F STATNIVE_CHAOS_DC

	chaos_apply iptables -A STATNIVE_CHAOS_DC -o lo -j RETURN
	chaos_apply iptables -A STATNIVE_CHAOS_DC -i lo -j RETURN

	if [ -n "$ssh_src" ]; then
		chaos_apply iptables -A STATNIVE_CHAOS_DC -d "$ssh_src" -j RETURN
		chaos_apply iptables -A STATNIVE_CHAOS_DC -s "$ssh_src" -j RETURN
	fi

	chaos_apply iptables -A STATNIVE_CHAOS_DC -j DROP
	chaos_apply iptables -I OUTPUT -j STATNIVE_CHAOS_DC
	chaos_apply iptables -I INPUT  -j STATNIVE_CHAOS_DC
	chaos_apply iptables -I FORWARD -j STATNIVE_CHAOS_DC
}

_restore() {
	chaos_assert_sentinel
	chaos_apply iptables -D OUTPUT  -j STATNIVE_CHAOS_DC 2>/dev/null || true
	chaos_apply iptables -D INPUT   -j STATNIVE_CHAOS_DC 2>/dev/null || true
	chaos_apply iptables -D FORWARD -j STATNIVE_CHAOS_DC 2>/dev/null || true
	chaos_apply iptables -F STATNIVE_CHAOS_DC 2>/dev/null || true
	chaos_apply iptables -X STATNIVE_CHAOS_DC 2>/dev/null || true
	chaos_clear_sentinel
}

case "$MODE" in
	restore)          _restore ;;
	apply|autonomous) _apply; chaos_done ;;
esac
