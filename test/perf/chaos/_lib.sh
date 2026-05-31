# _lib.sh — shared helpers for the chaos matrix. Source from each
# scenario script with: . "$(dirname "$0")/_lib.sh"
#
# Provides:
#   chaos_init <scenario-name>  — parse --apply/--restore/--help; set
#                                 SCENARIO, SENTINEL, DURATION; require
#                                 root unless DRY=1.
#   chaos_apply  cmd...         — exec the apply command with audit trail
#   chaos_log    msg...         — operator-facing stderr line
#   chaos_done                  — write sentinel + (if not --apply) wait
#                                 DURATION then restore.

# shellcheck shell=bash

set -euo pipefail

chaos_log() { printf '[chaos:%s] %s\n' "${SCENARIO:-?}" "$*" >&2; }

chaos_apply() {
	chaos_log "apply: $*"
	if [ "${DRY:-0}" = "1" ]; then
		return 0
	fi
	"$@"
}

chaos_init() {
	SCENARIO="$1"
	SENTINEL="/var/run/statnive-chaos-${SCENARIO}.applied"
	DURATION="${CHAOS_DURATION:-60s}"
	MODE="autonomous"   # autonomous = apply + sleep + restore in one go

	while [ "$#" -gt 1 ]; do
		case "$2" in
			--apply)   MODE="apply"; shift ;;
			--restore) MODE="restore"; shift ;;
			--help|-h)
				sed -n '1,30p' "$0" | sed 's/^# \{0,1\}//'
				exit 0
				;;
			*) chaos_log "unknown arg: $2"; exit 2 ;;
		esac
	done

	if [ "${DRY:-0}" != "1" ] && [ "$(id -u)" -ne 0 ]; then
		chaos_log "must run as root (or DRY=1)"; exit 1
	fi
}

# chaos_done is called by the scenario after its apply block runs.
# Behavior:
#   --apply       → write sentinel, exit 0
#   autonomous    → write sentinel, sleep $DURATION, run --restore
#   --restore     → unreachable from here (scenarios call _restore directly)
chaos_done() {
	if [ "${DRY:-0}" = "1" ]; then
		chaos_log "DRY=1 — skipping sentinel write at $SENTINEL"
	else
		mkdir -p "$(dirname "$SENTINEL")"
		echo "$(date -u +%FT%TZ) ${SCENARIO}" > "$SENTINEL"
		chaos_log "applied; sentinel=$SENTINEL"
	fi

	if [ "$MODE" = "apply" ]; then
		return 0
	fi

	# autonomous: trap ensures we restore on interrupt
	trap "chaos_log 'interrupted — restoring'; bash \"$0\" --restore" EXIT INT TERM
	chaos_log "sleeping $DURATION before restore"
	sleep "$DURATION"
	chaos_log "restoring (autonomous mode)"
	# Re-exec ourselves in --restore mode so we hit the scenario's
	# restore branch without duplicating logic.
	bash "$0" --restore
	trap - EXIT INT TERM
}

# chaos_assert_sentinel returns 0 if applied (idempotent restore check).
chaos_assert_sentinel() {
	if [ ! -f "$SENTINEL" ]; then
		chaos_log "no sentinel at $SENTINEL — already restored (or never applied)"
		exit 0
	fi
}

chaos_clear_sentinel() {
	rm -f "$SENTINEL"
	chaos_log "restored; sentinel cleared"
}
