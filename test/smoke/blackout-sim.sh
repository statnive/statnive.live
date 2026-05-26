#!/usr/bin/env bash
# blackout-sim.sh — boot the binary under iptables -P OUTPUT DROP and
# assert the existing smoke harness still passes.
#
# Air-gap proof: the binary's real production surface (smoke harness)
# works with zero non-loopback / non-docker-bridge egress. Run nightly
# via .github/workflows/blackout-sim-nightly.yml; runnable locally on
# Linux via `sudo make blackout-sim`.
#
# Why an external wrapper rather than baking iptables into smoke:
#   - smoke harness is the per-PR fast path (1 min); iptables setup
#     adds ~5 s and needs root, so it stays opt-in.
#   - this wrapper layers blackout on top WITHOUT modifying the harness.
#     A regression in either path stays surgical.
#
# macOS / non-Linux: aborts with a clear skip message — iptables is
# Linux-only and CI runs on ubuntu-latest.

set -euo pipefail

if [ "$(uname -s)" != "Linux" ]; then
	echo "blackout-sim: skip — Linux only ($(uname -s) detected)"
	exit 0
fi

if [ "$(id -u)" -ne 0 ]; then
	echo "blackout-sim: must run as root (sudo) — iptables changes the host policy" >&2
	exit 1
fi

if ! command -v iptables >/dev/null 2>&1; then
	echo "blackout-sim: iptables not installed" >&2
	exit 1
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

# Snapshot existing rules + counters so we can restore on exit. This
# matters for ad-hoc local runs where the operator's box already has
# their own iptables policy.
SNAPSHOT="$(mktemp -t blackout-sim.iptables.XXXXXX)"
iptables-save > "$SNAPSHOT"

restore_iptables() {
	echo "blackout-sim: restoring iptables snapshot"
	iptables-restore < "$SNAPSHOT" || true
	rm -f "$SNAPSHOT"
}
trap restore_iptables EXIT

echo "blackout-sim: clearing OUTPUT chain + policy DROP"
iptables -F OUTPUT
iptables -P OUTPUT DROP

# Loopback — binary's HTTP listener + curl probes + CH at 127.0.0.1 if
# someone runs CH outside docker.
iptables -A OUTPUT -o lo -j ACCEPT

# Docker bridges — the smoke harness's CH is in `statnive-clickhouse-dev`
# container, reached via `docker exec`. The exec ALSO uses the docker
# unix socket on /var/run/docker.sock (which goes over loopback so it's
# already allowed). But the container-side traffic uses the bridge.
# Allow every bridge interface so the harness's `docker exec
# clickhouse-client` keeps working.
for iface in $(ip -o link show | awk -F': ' '{print $2}' | grep -E '^(docker0|br-)' || true); do
	echo "blackout-sim: allow OUTPUT via $iface"
	iptables -A OUTPUT -o "$iface" -j ACCEPT
done

# Negative-control gate: external egress MUST fail. If this passes,
# the rules are broken and the test is meaningless.
if curl -fsS --max-time 3 http://1.1.1.1/ >/dev/null 2>&1; then
	echo "blackout-sim: NEGATIVE-CONTROL FAIL — external egress still works after OUTPUT DROP" >&2
	exit 1
fi
echo "blackout-sim: negative control OK — external egress blocked"

# Reset OUTPUT byte/packet counter so the final assertion measures only
# what the smoke harness produced.
iptables -Z OUTPUT

echo "blackout-sim: running make smoke under blackout"
# Inherit the harness's env defaults; the binary will bind to 127.0.0.1
# and reach CH via docker exec — both allowed by the OUTPUT rules above.
if ! make smoke; then
	echo "blackout-sim: make smoke failed under blackout — air-gap contract REGRESSED" >&2
	exit 1
fi

# Final packet-count gate: every non-default-policy OUTPUT rule above
# is an ACCEPT for loopback or docker bridge. If the DROP policy
# accumulated any packets, the binary tried to reach an external
# address — air-gap violation even when smoke still passes.
POLICY_PKTS=$(iptables -L OUTPUT -v -n -x \
	| sed -n 's/^Chain OUTPUT (policy DROP \([0-9]\+\) packets.*/\1/p')

echo "blackout-sim: OUTPUT DROP-policy packet counter = ${POLICY_PKTS:-unknown}"

if [ -n "${POLICY_PKTS:-}" ] && [ "$POLICY_PKTS" -gt 0 ]; then
	echo "blackout-sim: FAIL — $POLICY_PKTS packets hit the DROP policy" >&2
	echo "  the binary tried to reach a non-loopback / non-docker-bridge address" >&2
	iptables -L OUTPUT -v -n --line-numbers >&2
	exit 1
fi

echo "blackout-sim: OK — air-gap contract upheld (zero DROPped packets)"
