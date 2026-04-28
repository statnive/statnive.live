#!/usr/bin/env bash
# Scenario D — Tehran-IX peering degradation → TIC fallback (doc 29 §5.4).
#
# Inject 200 ms+jitter on the Tehran-IX peer subnet; TIC fallback path
# should stay clean. Approximated on the 2-node test bed by shaping the
# generator-side egress to a configurable peer subnet (CHAOS_PEER_NET).

source "$(dirname "$0")/_lib.sh"

IFACE="${CHAOS_IFACE:-eth0}"
PEER_NET="${CHAOS_PEER_NET:-10.99.0.0/16}"

scenario_up() {
    require_root
    require_cmd tc
    if tc qdisc show dev "$IFACE" | grep -q "prio.*statnive-d"; then
        echo "scenario D already up"; return 0
    fi
    # Root qdisc with a marked class for traffic destined to PEER_NET.
    tc qdisc add dev "$IFACE" root handle 1: prio
    tc qdisc add dev "$IFACE" parent 1:3 handle 30: netem delay 200ms 50ms distribution normal
    tc filter add dev "$IFACE" protocol ip parent 1:0 prio 3 u32 \
        match ip dst "$PEER_NET" flowid 1:3
    echo "scenario D up — $IFACE: 200ms+50ms jitter on $PEER_NET"
}

scenario_down() {
    require_root
    require_cmd tc
    if ! tc qdisc show dev "$IFACE" | grep -q "prio"; then
        echo "scenario D already down"; return 0
    fi
    tc qdisc del dev "$IFACE" root || true
    echo "scenario D down"
}

scenario_status() {
    if tc qdisc show dev "$IFACE" | grep -q "prio"; then
        echo "up"; exit 0
    else
        echo "down"; exit 1
    fi
}

dispatch D "$@"
