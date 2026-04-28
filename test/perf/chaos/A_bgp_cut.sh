#!/usr/bin/env bash
# Scenario A — Full BGP cut (doc 29 §5.1).
#
# Models Iran's Nov-2019 / Nov-2022 / Jun-2025 / Feb-2026 full Internet
# severance: domestic NIN routes stay reachable, all non-IR traffic is
# black-holed at the egress.
#
# Phase 7e dry-run on a 2-node test bed approximates this with iptables
# DROP rules pointed at non-loopback ranges; Phase 10 wires real BGP
# withdrawals via the Asiatech ops contact (out-of-band).

source "$(dirname "$0")/_lib.sh"

CHAIN=STATNIVE_CHAOS_BGP_CUT

scenario_up() {
    require_root
    require_cmd iptables
    if iptables -L "$CHAIN" >/dev/null 2>&1; then
        echo "scenario A already up"; return 0
    fi
    # Defense-in-depth idempotency: -N may have raced with another up call
    # in distributed orchestration, so swallow EEXIST and let -A be the
    # truth-source. -I OUTPUT is also safe to re-apply (creates a duplicate
    # jump rule which `down` removes once).
    iptables -N "$CHAIN" 2>/dev/null || true
    iptables -I OUTPUT -j "$CHAIN"
    # Drop everything that isn't loopback, RFC1918 (Iranian DC LAN), or
    # documentation range used by the load-test XFF.
    iptables -A "$CHAIN" -o lo -j RETURN
    iptables -A "$CHAIN" -d 10.0.0.0/8 -j RETURN
    iptables -A "$CHAIN" -d 172.16.0.0/12 -j RETURN
    iptables -A "$CHAIN" -d 192.168.0.0/16 -j RETURN
    iptables -A "$CHAIN" -d 192.0.2.0/24 -j RETURN
    iptables -A "$CHAIN" -j DROP
    echo "scenario A up — non-IR egress dropped"
}

scenario_down() {
    require_root
    require_cmd iptables
    if ! iptables -L "$CHAIN" >/dev/null 2>&1; then
        echo "scenario A already down"; return 0
    fi
    iptables -D OUTPUT -j "$CHAIN" 2>/dev/null || true
    iptables -F "$CHAIN"
    iptables -X "$CHAIN"
    echo "scenario A down — egress restored"
}

scenario_status() {
    if iptables -L "$CHAIN" >/dev/null 2>&1; then
        echo "up"; exit 0
    else
        echo "down"; exit 1
    fi
}

dispatch A "$@"
