#!/usr/bin/env bash
# Scenario F — Clock skew / identity-salt rollover (doc 29 §5.6).
#
# Step the system clock ±5 min across the IRST midnight salt-rotation
# boundary. The binary's salt = HMAC(master_secret, site_id || YYYY-MM-DD IRST);
# crossing midnight rotates the salt. A clock that jumps backwards then
# forwards must NOT corrupt visitor_hash continuity for in-flight visitors.

source "$(dirname "$0")/_lib.sh"

STEP_SEC="${CHAOS_CLOCK_STEP_SEC:-300}"   # default ±5 min
STATE_FILE=/tmp/statnive-chaos-F-stepped

scenario_up() {
    require_root
    require_cmd date
    if [[ -f "$STATE_FILE" ]]; then
        echo "scenario F already up"; return 0
    fi
    # Step backwards by STEP_SEC then forwards by 2*STEP_SEC, leaving the
    # clock offset by STEP_SEC ahead of real time. Recorded offset lets
    # `down` restore reasonably (operator restarts NTP for full sync).
    date -s "$(date -d "@$(($(date +%s) - STEP_SEC))")" >/dev/null
    echo "$STEP_SEC" >"$STATE_FILE"
    echo "scenario F up — clock stepped -${STEP_SEC}s; restart NTP via 'down'"
}

scenario_down() {
    require_root
    require_cmd date
    if [[ ! -f "$STATE_FILE" ]]; then
        echo "scenario F already down"; return 0
    fi
    rm -f "$STATE_FILE"
    if command -v chronyc >/dev/null 2>&1; then
        chronyc makestep || true
        echo "scenario F down — chronyc makestep issued"
    elif command -v ntpdate >/dev/null 2>&1; then
        ntpdate -u "${CHAOS_NTP_HOST:-pool.ntp.org}" || true
        echo "scenario F down — ntpdate sync attempted"
    else
        echo "WARN: no NTP client found; manual time sync required" >&2
        return 1
    fi
}

scenario_status() {
    if [[ -f "$STATE_FILE" ]]; then
        echo "up"; exit 0
    else
        echo "down"; exit 1
    fi
}

dispatch F "$@"
