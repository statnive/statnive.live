#!/usr/bin/env bash
# Scenario C — Stealth DPI throttle / RST injection (doc 29 §5.3).
#
# 2025–2026 Iranian DPI behavior: a fraction of TLS handshakes get an
# RST injected after ClientHello. The xt_tls iptables module matches by
# SNI; missing on most distros (out-of-tree kernel module). When absent
# we fall back to a coarser tcp-flag-based RST insertion which is less
# realistic but still exercises the binary's connection-reset paths.

source "$(dirname "$0")/_lib.sh"

CHAIN=STATNIVE_CHAOS_DPI_RST
SNI_PATTERN="${CHAOS_SNI_PATTERN:-statnive.live}"

scenario_up() {
    require_root
    require_cmd iptables
    if iptables -L "$CHAIN" >/dev/null 2>&1; then
        echo "scenario C already up"; return 0
    fi
    # Idempotency: -N races with concurrent up calls in distributed
    # orchestration; swallow EEXIST and let -A become truth.
    iptables -N "$CHAIN" 2>/dev/null || true
    iptables -I OUTPUT -p tcp --dport 443 -j "$CHAIN"
    if iptables -m tls --help 2>&1 | grep -q tls-host; then
        # xt_tls available — match by SNI on a 2% sample.
        iptables -A "$CHAIN" -m tls --tls-host "$SNI_PATTERN" \
            -m statistic --mode random --probability 0.02 \
            -j REJECT --reject-with tcp-reset
        echo "scenario C up — xt_tls SNI=$SNI_PATTERN @ 2% RST"
    else
        # Fallback: random 2% RST on outbound 443. Less realistic but
        # exercises the same binary code path.
        iptables -A "$CHAIN" -p tcp --dport 443 \
            -m statistic --mode random --probability 0.02 \
            -j REJECT --reject-with tcp-reset
        echo "scenario C up — fallback (no xt_tls): 2% random 443/tcp RST"
    fi
}

scenario_down() {
    require_root
    require_cmd iptables
    if ! iptables -L "$CHAIN" >/dev/null 2>&1; then
        echo "scenario C already down"; return 0
    fi
    iptables -D OUTPUT -p tcp --dport 443 -j "$CHAIN" 2>/dev/null || true
    iptables -F "$CHAIN"
    iptables -X "$CHAIN"
    echo "scenario C down"
}

scenario_status() {
    if iptables -L "$CHAIN" >/dev/null 2>&1; then
        echo "up"; exit 0
    else
        echo "down"; exit 1
    fi
}

dispatch C "$@"
