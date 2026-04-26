#!/usr/bin/env bash
# airgap-bundle-completeness.sh — bundle-completeness gate.
#
# Run after `make airgap-bundle` (or as the second step inside it). For
# every file the install path references (deploy/airgap-install.sh,
# deploy/statnive-deploy.sh, internal/enrich/crawler-user-agents.json,
# bin/statnive-live), assert it exists in the unpacked bundle. Plus
# verify the binary is ELF 64-bit LSB amd64 (not the host platform's
# native format).
#
# Distinct from deploy/airgap-verify-bundle.sh, which checks SHA256 +
# Ed25519 signatures on the *tarball* operators receive. This script
# checks the *unpacked* tree's structural completeness post-build.
#
# Closes Milestone 1 cutover bugs #1 (missing statnive-deploy.sh), #2
# (missing crawler-user-agents.json), #3 (Mac-built darwin/arm64 binary
# inside a linux-amd64-named tarball). LEARN.md Lesson 2.
#
# Usage: bash deploy/airgap-bundle-completeness.sh <BUNDLE_DIR>

set -euo pipefail

if [ $# -lt 1 ]; then
	echo "usage: $0 <BUNDLE_DIR>" >&2
	exit 2
fi

BUNDLE_DIR="$1"

if [ ! -d "$BUNDLE_DIR" ]; then
	echo "airgap-bundle-completeness: $BUNDLE_DIR not found" >&2
	exit 1
fi

# Required files. Each entry is checked for existence + non-zero size.
# Annotated with the install/runtime path that consumes it so future
# bundle-failure incidents at 2 AM can read the script in isolation.
REQUIRED=(
	"bin/statnive-live"                          # the binary; copied to /usr/local/bin
	"VERSION"                                    # version stamp consumed by /api/about + airgap-verify-bundle.sh
	"config/statnive-live.yaml.example"          # seeded as /etc/statnive-live/config.yaml on first install (PR-B schema)
	"config/sources.yaml"                        # channel-mapper + Iranian-referrer rules; seeded to /etc/statnive-live/sources.yaml
	"deploy/airgap-install.sh"                   # the installer the operator invokes
	"deploy/airgap-update-geoip.sh"              # GeoIP BIN drop-in script (manual monthly refresh)
	"deploy/airgap-verify-bundle.sh"             # tarball SHA + sig check (operator-side)
	"deploy/statnive-deploy.sh"                  # GHA / cutover deploy primitive; installs to /usr/local/bin/statnive-deploy
	"deploy/systemd/statnive-live.service"       # systemd unit
	"deploy/iptables/rules.v4"                   # opt-in egress lockdown (--apply-iptables)
	"deploy/iptables/rules.v6"                   # opt-in egress lockdown (--apply-iptables)
	"docs/runbook.md"                            # operator reference; pulled via airgap-verify-bundle.sh integrity check
	"internal/enrich/crawler-user-agents.json"   # bot-detector pattern data; size-guarded below
	"SHA256SUMS"                                 # inner integrity manifest written by airgap-bundle.sh
)

failed=0

for rel in "${REQUIRED[@]}"; do
	path="$BUNDLE_DIR/$rel"
	if [ ! -f "$path" ]; then
		echo "  MISSING: $rel"
		failed=1
		continue
	fi
	if [ ! -s "$path" ]; then
		echo "  EMPTY:   $rel"
		failed=1
	fi
done

# Crawler JSON has a known fail-soft behavior — bot detector falls back
# to ~60 hardcoded patterns when the embed is empty. Bug #2 was a 3-byte
# stub silently shipping. Demand >=1 KB so the JSON has actually been
# refreshed via `make refresh-bot-patterns` before bundling.
if [ -f "$BUNDLE_DIR/internal/enrich/crawler-user-agents.json" ]; then
	bytes="$(stat -f%z "$BUNDLE_DIR/internal/enrich/crawler-user-agents.json" 2>/dev/null || \
	         stat -c%s "$BUNDLE_DIR/internal/enrich/crawler-user-agents.json")"
	if [ "$bytes" -lt 1024 ]; then
		echo "  STUB:    internal/enrich/crawler-user-agents.json is ${bytes} bytes (<1024) — run 'make refresh-bot-patterns' before bundling"
		failed=1
	fi
fi

# Binary must be ELF 64-bit LSB executable, x86-64 (linux/amd64). On a
# Mac dev box, the native `file` output for a darwin binary would be
# "Mach-O 64-bit executable arm64" — that's the cutover Bug #3 path.
if [ -f "$BUNDLE_DIR/bin/statnive-live" ]; then
	desc="$(file -b "$BUNDLE_DIR/bin/statnive-live")"
	case "$desc" in
		*"ELF 64-bit LSB"*"x86-64"*)
			: # ok
			;;
		*)
			echo "  ARCH:    bin/statnive-live is not ELF 64-bit LSB x86-64 (got: $desc)"
			failed=1
			;;
	esac
fi

if [ "$failed" -ne 0 ]; then
	echo "airgap-bundle-completeness: FAIL — bundle is incomplete or arch-wrong"
	exit 1
fi

echo "airgap-bundle-completeness: ok ($BUNDLE_DIR)"
