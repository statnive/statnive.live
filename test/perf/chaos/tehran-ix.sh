#!/usr/bin/env bash
# tehran-ix.sh — Tehran-IX peering degradation (latency + loss).
#
# Adds 80–300 ms uniform jitter + 1–3% packet loss to egress on the
# primary interface. Simulates the Tehran-IX fabric flapping under
# congestion; the binary must still ingest events (WAL absorbs the
# back-pressure) and the dashboard must stay reachable.

# shellcheck disable=SC1091
. "$(dirname "$0")/_lib.sh"

chaos_init tehran-ix "$@"

_detect_iface() {
	if [ -n "${CHAOS_IFACE:-}" ]; then
		IFACE="$CHAOS_IFACE"
		return
	fi

	if command -v ip >/dev/null 2>&1; then
		IFACE="$(ip route show default | awk '/default/ {print $5; exit}')"
		return
	fi

	if [ "${DRY:-0}" = "1" ]; then
		IFACE="<dry-run>"
		return
	fi

	chaos_log "could not detect default interface; set CHAOS_IFACE=eth0"; exit 1
}

_apply() {
	_detect_iface
	chaos_log "applying tc to $IFACE"
	# netem delay 150ms 75ms = 75–225ms uniform; loss 2% = ~2/100 dropped.
	chaos_apply tc qdisc replace dev "$IFACE" root netem \
		delay 150ms 75ms distribution normal loss 2%
}

_restore() {
	chaos_assert_sentinel
	_detect_iface
	if [ -n "$IFACE" ]; then
		chaos_apply tc qdisc del dev "$IFACE" root 2>/dev/null || true
	fi
	chaos_clear_sentinel
}

case "$MODE" in
	restore)          _restore ;;
	apply|autonomous) _apply; chaos_done ;;
esac
