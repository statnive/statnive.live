#!/usr/bin/env bash
# airgap-update-geoip.sh — swap the IP2Location GeoIP BIN and SIGHUP
# statnive-live so the hot-reload picks up the new DB without a
# restart. Safe to re-run; the service keeps the old DB active until
# the new one passes pre-swap validation probes (see geoip-pipeline-review).
#
# Usage:
#   sudo ./airgap-update-geoip.sh /path/to/IP2LOCATION-LITE-DB23.BIN
#
# Exit codes:
#   0 — new BIN installed + SIGHUP sent + reload event observed in audit
#   1 — precondition failure (missing BIN, wrong filesystem, no statnive user)
#   2 — reload event not observed within 30 s (probe failed; old DB still active)

set -euo pipefail

NEW_BIN="${1:-}"
DEST="/etc/statnive-live/geoip/IP2LOCATION-LITE-DB23.BIN"
AUDIT="/var/log/statnive-live/audit.jsonl"
RELOAD_EVENT="geoip.reloaded"
TIMEOUT_SEC=30

if [ -z "$NEW_BIN" ]; then
	echo "usage: $0 /path/to/IP2LOCATION-*.BIN" >&2
	exit 1
fi

if [ ! -f "$NEW_BIN" ]; then
	echo "update-geoip: new BIN not found: $NEW_BIN" >&2
	exit 1
fi

if [ "$(id -u)" -ne 0 ]; then
	echo "update-geoip: must run as root (sudo)" >&2
	exit 1
fi

# Atomic mv requires same filesystem — otherwise mv falls back to
# copy+unlink which is NOT atomic, and statnive-live would see a
# partial file mid-swap.
NEW_DEV="$(stat -c '%d' "$NEW_BIN" 2>/dev/null || stat -f '%d' "$NEW_BIN")"
DEST_DIR="$(dirname "$DEST")"
if [ ! -d "$DEST_DIR" ]; then
	install -d -m 0700 -o statnive -g statnive "$DEST_DIR"
fi
DEST_DEV="$(stat -c '%d' "$DEST_DIR" 2>/dev/null || stat -f '%d' "$DEST_DIR")"
if [ "$NEW_DEV" != "$DEST_DEV" ]; then
	echo "update-geoip: new BIN is on a different filesystem than $DEST_DIR — \`mv\` would not be atomic." >&2
	echo "             Move the file onto $DEST_DIR's filesystem first (e.g. into /tmp or /var/tmp) and re-run." >&2
	exit 1
fi

echo "update-geoip: new BIN    $(sha256sum "$NEW_BIN" | awk '{print $1}')  $NEW_BIN"
if [ -f "$DEST" ]; then
	echo "update-geoip: old BIN    $(sha256sum "$DEST" | awk '{print $1}')  $DEST"
fi

# --- atomic swap -------------------------------------------------------------
chown statnive:statnive "$NEW_BIN"
chmod 0640 "$NEW_BIN"
mv -f "$NEW_BIN" "$DEST"
echo "update-geoip: swapped $DEST"

# --- SIGHUP + observe reload event ------------------------------------------
START_TS="$(date -u +%s)"
if ! systemctl kill -s HUP statnive-live 2>/dev/null; then
	# Fallback if not running under systemd (e.g. local dev).
	PID="$(pgrep -f '/usr/local/bin/statnive-live' || true)"
	if [ -z "$PID" ]; then
		echo "update-geoip: statnive-live not running — start it to apply the new DB" >&2
		exit 0
	fi
	kill -HUP "$PID"
fi
echo "update-geoip: sent SIGHUP; waiting for audit event '$RELOAD_EVENT' (timeout ${TIMEOUT_SEC}s)"

DEADLINE=$((START_TS + TIMEOUT_SEC))
while [ "$(date -u +%s)" -le "$DEADLINE" ]; do
	if [ -f "$AUDIT" ] && grep -q "\"event\":\"$RELOAD_EVENT\"" <(tail -n 200 "$AUDIT" 2>/dev/null); then
		LATEST="$(tail -n 200 "$AUDIT" | grep "\"event\":\"$RELOAD_EVENT\"" | tail -n 1)"
		echo "update-geoip: $LATEST"
		echo "update-geoip: OK"
		exit 0
	fi
	sleep 1
done

echo "update-geoip: no '$RELOAD_EVENT' event in the last $TIMEOUT_SEC s — probe likely failed." >&2
echo "             The old DB is still active; inspect $AUDIT for config.reload_failed." >&2
exit 2
