#!/usr/bin/env bash
# mobile-curfew.sh — rolling Iranian mobile-internet curfew.
#
# Drops 90% of requests whose UA matches a mobile-shape regex AT THE
# BINARY (not the kernel — kernel iptables can't see UA without DPI).
# Requires the binary to be built/run with the chaos feature flag:
#
#   STATNIVE_CHAOS_DROP_MOBILE_UA_PCT=90
#
# the handler reads it on every request and 503s the matching share.
# This script just toggles the flag in the systemd drop-in and reloads
# the unit.

# shellcheck disable=SC1091
. "$(dirname "$0")/_lib.sh"

chaos_init mobile-curfew "$@"

DROPIN=/etc/systemd/system/statnive-live.service.d/chaos.conf

_apply() {
	chaos_apply install -d -m 0755 /etc/systemd/system/statnive-live.service.d
	# Write a NEW drop-in named "chaos.conf" rather than touching
	# posture.conf (L2 owns that). systemd merges all *.conf files.
	chaos_apply tee "$DROPIN" >/dev/null <<-EOF
		[Service]
		Environment="STATNIVE_CHAOS_DROP_MOBILE_UA_PCT=90"
	EOF
	chaos_apply systemctl daemon-reload
	chaos_apply systemctl restart statnive-live
}

_restore() {
	chaos_assert_sentinel
	chaos_apply rm -f "$DROPIN"
	chaos_apply systemctl daemon-reload
	chaos_apply systemctl restart statnive-live
	chaos_clear_sentinel
}

case "$MODE" in
	restore)          _restore ;;
	apply|autonomous) _apply; chaos_done ;;
esac
