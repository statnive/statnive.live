#!/usr/bin/env bash
# intl-egress.sh — outbound to non-IR ASN ranges blocked; IR-internal
# egress still works. Doc 30 § scenario G — added after the 2026-04-20
# GA4 calibration showed 38% diaspora traffic. Losing the international
# leg without losing IR-internal is now its own degradation mode worth
# gating against.
#
# Allowlist: a handful of well-known IR /16 ranges (chosen from RIPE NCC
# AS44244, AS197207, AS57218 for SamplePlatform-class — the operator
# refines the list per deployment). Everything else egress-blocks.

# shellcheck disable=SC1091
. "$(dirname "$0")/_lib.sh"

chaos_init intl-egress "$@"

# Coarse allowlist — three /8s spanning the canonical Iranian ISP ASN
# ranges per CLAUDE.md Security §13 (Irancell / MCI / RighTel). The
# load-gate doesn't claim full IR-routing fidelity; it claims "events
# from IR-shaped IPs survive even when global egress is gone."
IR_ALLOW="${CHAOS_IR_ALLOWLIST:-5.0.0.0/8 31.0.0.0/8 188.0.0.0/8}"

_apply() {
	chaos_apply iptables -N STATNIVE_CHAOS_INTL 2>/dev/null || true
	chaos_apply iptables -F STATNIVE_CHAOS_INTL

	chaos_apply iptables -A STATNIVE_CHAOS_INTL -o lo -j RETURN

	# Operator SSH carve-out.
	if [ -n "${SSH_CONNECTION:-}" ]; then
		local ssh_src
		ssh_src="$(echo "$SSH_CONNECTION" | awk '{print $1}')"
		chaos_apply iptables -A STATNIVE_CHAOS_INTL -d "$ssh_src" -j RETURN
	fi

	# IR allowlist passes through.
	for cidr in $IR_ALLOW; do
		chaos_apply iptables -A STATNIVE_CHAOS_INTL -d "$cidr" -j RETURN
	done

	# Everything else dropped.
	chaos_apply iptables -A STATNIVE_CHAOS_INTL -j REJECT --reject-with icmp-host-prohibited
	chaos_apply iptables -I OUTPUT -j STATNIVE_CHAOS_INTL
}

_restore() {
	chaos_assert_sentinel
	chaos_apply iptables -D OUTPUT -j STATNIVE_CHAOS_INTL 2>/dev/null || true
	chaos_apply iptables -F STATNIVE_CHAOS_INTL 2>/dev/null || true
	chaos_apply iptables -X STATNIVE_CHAOS_INTL 2>/dev/null || true
	chaos_clear_sentinel
}

case "$MODE" in
	restore)          _restore ;;
	apply|autonomous) _apply; chaos_done ;;
esac
