#!/usr/bin/env bash
# Scenario B — Mobile curfew (doc 29 §5.2).
#
# 4 PM–midnight Tehran-time 80% drop on the egress NIC, modeling the
# Sep–Nov 2022 mobile-carrier curfew pattern. tc-netem applies the loss
# profile; downstream rates degrade to a fraction of normal throughput.

source "$(dirname "$0")/_lib.sh"

IFACE="${CHAOS_IFACE:-eth0}"

scenario_up() {
    require_root
    require_cmd tc
    if tc qdisc show dev "$IFACE" | grep -q netem; then
        echo "scenario B already up on $IFACE"; return 0
    fi
    tc qdisc add dev "$IFACE" root netem loss 80% delay 200ms
    echo "scenario B up — $IFACE: 80% loss + 200ms delay"
}

scenario_down() {
    require_root
    require_cmd tc
    if ! tc qdisc show dev "$IFACE" | grep -q netem; then
        echo "scenario B already down on $IFACE"; return 0
    fi
    tc qdisc del dev "$IFACE" root || true
    echo "scenario B down — $IFACE qdisc cleared"
}

scenario_status() {
    if tc qdisc show dev "$IFACE" | grep -q netem; then
        echo "up"; exit 0
    else
        echo "down"; exit 1
    fi
}

dispatch B "$@"
