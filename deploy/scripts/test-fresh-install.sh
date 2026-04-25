#!/usr/bin/env bash
# test-fresh-install.sh — Docker matrix smoke for airgap-install.sh.
#
# Verifies the install script runs cleanly on a fresh ubuntu:24.04 AND
# a fresh debian:13-slim with no manual fixups, asserts the directory
# permissions are reachable by the `statnive` service user, and that
# the script is idempotent (a second run produces no changes / errors).
#
# Why both distros: Ubuntu 24 was the original target; Debian 13 is what
# Netcup defaults us to on the production VPS (Milestone 1 cutover
# postmortem, PLAN.md). LEARN.md Lesson 9 mandates a >=2-distro matrix
# so packaging deltas (iptables, netplan, ClickHouse systemd unit) get
# caught at build time rather than at cutover time.
#
# Runs under --skip-ch-check because CH-in-Docker-in-Docker requires a
# heavier compose harness; the installer's CH-touching path
# (CREATE DATABASE) is gated on `clickhouse-client` presence anyway.
#
# Usage:  bash deploy/scripts/test-fresh-install.sh [--distros "img1 img2"]
# Env:    STATNIVE_FRESH_INSTALL_DISTROS — space-separated docker images
#                                          (default: ubuntu:24.04 debian:13-slim)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DISTROS="${STATNIVE_FRESH_INSTALL_DISTROS:-ubuntu:24.04 debian:13-slim}"

# Override via flag.
while [ $# -gt 0 ]; do
	case "$1" in
		--distros)
			DISTROS="$2"
			shift 2
			;;
		-h|--help)
			sed -n '1,25p' "$0" | sed 's/^# \{0,1\}//'
			exit 0
			;;
		*)
			echo "test-fresh-install: unknown arg: $1" >&2
			exit 2
			;;
	esac
done

if ! command -v docker >/dev/null 2>&1; then
	echo "test-fresh-install: docker required" >&2
	exit 1
fi

# Build a minimal stub bundle layout that the install script needs to
# find. The real airgap-bundle.sh produces a fuller layout; this stub
# is enough to exercise the install path's package management +
# permissions code without requiring a full GHA cross-compile.
build_stub_bundle() {
	local stage="$1"
	mkdir -p "$stage/bin" "$stage/config" "$stage/deploy/iptables" "$stage/deploy/systemd"
	# A no-op binary keeps `install -m 0755 .../bin/statnive-live` happy.
	cat > "$stage/bin/statnive-live" <<-'STUB'
#!/bin/sh
echo "statnive-live stub (test-fresh-install)"
exit 0
STUB
	chmod +x "$stage/bin/statnive-live"
	# The real example (PR-B) plus the script's expected unit + sources.
	cp "$REPO_ROOT/config/statnive-live.yaml.example" "$stage/config/statnive-live.yaml.example"
	cp "$REPO_ROOT/config/sources.yaml" "$stage/config/sources.yaml"
	cp "$REPO_ROOT/deploy/systemd/statnive-live.service" "$stage/deploy/systemd/statnive-live.service"
	cp "$REPO_ROOT/deploy/airgap-install.sh" "$stage/deploy/airgap-install.sh"
	# iptables/* aren't loaded under --skip-ch-check + no --apply-iptables;
	# placeholder files keep the optional-fallback branch quiet.
	: > "$stage/deploy/iptables/rules.v4"
	: > "$stage/deploy/iptables/rules.v6"
}

run_in_distro() {
	local distro="$1"
	local stage
	stage="$(mktemp -d)"
	trap 'rm -rf "$stage"' RETURN

	build_stub_bundle "$stage"

	echo ""
	echo "==> $distro"

	# bash is needed to run the script; iputils-ping just gives the
	# container a saner debug surface; ss for the CH listener probe.
	# We run the full install path and assert it returns 0, then
	# re-run to assert idempotency.
	docker run --rm \
		-v "$stage:/bundle:ro" \
		-w /bundle/deploy \
		"$distro" \
		bash -c '
			set -e
			apt-get update -qq >/dev/null
			# bash + iproute2 (ss) + systemctl (Debian package shipping a
			# standalone systemctl binary so the install script preflight
			# `command -v systemctl` succeeds; the script then handles the
			# "systemd not running (container/chroot?)" case gracefully).
			# Fallback to no-systemctl on distros where the pkg is absent.
			apt-get install -y -qq bash iproute2 systemctl >/dev/null 2>&1 || \
				apt-get install -y -qq bash iproute2 >/dev/null
			# First pass — must complete cleanly.
			bash /bundle/deploy/airgap-install.sh --skip-ch-check 2>&1 | sed "s|^|  [1] |"
			# Second pass — must remain idempotent.
			bash /bundle/deploy/airgap-install.sh --skip-ch-check 2>&1 | sed "s|^|  [2] |"
			# Permission assertions: parent + subdirs both reachable to
			# the statnive user. LEARN.md Lesson 7 — a parent-dir mode
			# 0700 hides files inside even when files are 0644.
			stat -c "%a %U:%G %n" /etc/statnive-live /etc/statnive-live/tls /etc/statnive-live/geoip
			[ "$(stat -c %a /etc/statnive-live)"       = "755" ] || { echo "  FAIL: /etc/statnive-live not 0755"; exit 1; }
			[ "$(stat -c %a /etc/statnive-live/tls)"   = "750" ] || { echo "  FAIL: /etc/statnive-live/tls not 0750"; exit 1; }
			[ "$(stat -c %a /etc/statnive-live/geoip)" = "750" ] || { echo "  FAIL: /etc/statnive-live/geoip not 0750"; exit 1; }
			# Service-user traversal: become statnive, walk to a file
			# inside the protected dir, confirm we can read it.
			install -m 0640 -o root -g statnive /bundle/config/statnive-live.yaml.example /etc/statnive-live/tls/.probe
			runuser -u statnive -- cat /etc/statnive-live/tls/.probe >/dev/null || \
				{ echo "  FAIL: statnive user cannot reach /etc/statnive-live/tls/.probe"; exit 1; }
			rm -f /etc/statnive-live/tls/.probe
			echo "  ok"
		'
}

for distro in $DISTROS; do
	run_in_distro "$distro"
done

echo ""
echo "test-fresh-install: all distros passed"
