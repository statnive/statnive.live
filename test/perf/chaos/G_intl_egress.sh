#!/usr/bin/env bash
# Scenario G — International-egress jitter (doc 30 §3, NEW vs doc 29).
#
# 3-hour tc-netem 80–120 ms jitter + 2% loss on Tehran-IX → Frankfurt
# peering, while NIN domestic paths stay clean. Pins the 38% non-Iran
# diaspora cohort (DE 19% / US 9% / FI 7.5% / UK 2.8% / FR 2.7% / CA 2.5%).
#
# Phase 7e ships the script; the diaspora beacon (Hetzner Helsinki or
# Falkenstein per doc 29 §6.4) lands at Phase 10 procurement, so the
# 2-node dry-run only validates that the netem qdisc applies cleanly —
# the cohort-split SLO assertion is a Phase 10 step.

source "$(dirname "$0")/_lib.sh"

IFACE="${CHAOS_IFACE:-eth0}"
INTL_NET="${CHAOS_INTL_NET:-10.88.0.0/16}"   # placeholder — Phase 10 fills the real Frankfurt subnet
HOLD_HOURS="${CHAOS_G_HOURS:-3}"

scenario_up() {
    require_root
    require_cmd tc
    if tc qdisc show dev "$IFACE" | grep -q "handle 1:.*prio"; then
        echo "scenario G already up"; return 0
    fi
    tc qdisc add dev "$IFACE" root handle 1: prio
    tc qdisc add dev "$IFACE" parent 1:3 handle 30: netem \
        delay 100ms 20ms distribution normal loss 2%
    tc filter add dev "$IFACE" protocol ip parent 1:0 prio 3 u32 \
        match ip dst "$INTL_NET" flowid 1:3
    echo "scenario G up — $IFACE: 100±20ms jitter + 2% loss on $INTL_NET (hold ${HOLD_HOURS}h)"
}

scenario_down() {
    require_root
    require_cmd tc
    if ! tc qdisc show dev "$IFACE" | grep -q "handle 1:.*prio"; then
        echo "scenario G already down"; return 0
    fi
    tc qdisc del dev "$IFACE" root || true
    echo "scenario G down"
}

scenario_status() {
    if tc qdisc show dev "$IFACE" | grep -q "handle 1:.*prio"; then
        echo "up"; exit 0
    else
        echo "down"; exit 1
    fi
}

dispatch G "$@"
