#!/usr/bin/env bash
# clock-skew.sh — chrony unable to sync; system clock drifts.
#
# Pushes the system clock forward 120 s and stops chronyd so it can't
# re-converge. The binary's IRST-keyed daily-salt rotation depends on
# Stratum ≤4 within 60 s of boot (CLAUDE.md Privacy Rule 2); 120 s
# drift triggers the salt-recovery path. WAL timestamps written under
# the drifted clock must still match the oracle's send_ts within the
# latency SLO (the generator clock is on a separate machine).

# shellcheck disable=SC1091
. "$(dirname "$0")/_lib.sh"

chaos_init clock-skew "$@"

_apply() {
	chaos_apply systemctl stop chrony 2>/dev/null || \
		chaos_apply systemctl stop chronyd 2>/dev/null || \
		chaos_log "chrony service not running (continuing)"

	# Stash the pre-skew offset so restore can undo precisely.
	local now
	now="$(date -u +%s)"
	if [ "${DRY:-0}" != "1" ]; then
		echo "$now" > "/var/run/statnive-chaos-clock-skew.preskew"
	fi

	# Push forward 120s.
	chaos_apply date -u -s "@$((now + 120))"
}

_restore() {
	chaos_assert_sentinel

	# Resume chrony — it'll discipline the clock back within seconds.
	chaos_apply systemctl start chrony 2>/dev/null || \
		chaos_apply systemctl start chronyd 2>/dev/null || true

	# Force a step immediately so the load-gate window doesn't see the
	# slow rate-of-change. Idempotent — chronyc returns 0 even if the
	# delta is already < threshold.
	chaos_apply chronyc -a 'makestep' 2>/dev/null || true

	rm -f "/var/run/statnive-chaos-clock-skew.preskew"
	chaos_clear_sentinel
}

case "$MODE" in
	restore)          _restore ;;
	apply|autonomous) _apply; chaos_done ;;
esac
