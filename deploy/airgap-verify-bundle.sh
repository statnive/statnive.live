#!/usr/bin/env bash
# airgap-verify-bundle.sh — check SHA256 (+ Ed25519 signature if present)
# on a received bundle BEFORE unpacking on the target host. Safe to run
# anywhere sha256sum (coreutils) + optionally ssh-keygen (OpenSSH 8.0+)
# are available. Does NOT require network access.
#
# Usage:
#   ./airgap-verify-bundle.sh <bundle.tar.gz> [public-key]
#
# Exit codes:
#   0 — SHA256 matches (and signature verifies if both key + .sig provided)
#   1 — SHA256 mismatch
#   2 — signature missing / mismatch (only when public-key was given)
#   3 — missing tools / bad arguments

set -euo pipefail

if [ $# -lt 1 ]; then
	cat >&2 <<-EOF
		Usage: $0 <bundle.tar.gz> [trusted-ssh-public-key]

		Expects SHA256SUMS to live next to the tarball, and optionally
		SHA256SUMS.sig (ssh-keygen -Y sign output) for Ed25519 check.
	EOF
	exit 3
fi

BUNDLE="$1"
PUBKEY="${2:-}"

if [ ! -f "$BUNDLE" ]; then
	echo "airgap-verify: bundle not found: $BUNDLE" >&2
	exit 3
fi

BUNDLE_DIR="$(cd "$(dirname "$BUNDLE")" && pwd)"
BUNDLE_BASE="$(basename "$BUNDLE")"
SUMS_FILE="$BUNDLE_DIR/SHA256SUMS"
SIG_FILE="$BUNDLE_DIR/SHA256SUMS.sig"

if [ ! -f "$SUMS_FILE" ]; then
	echo "airgap-verify: SHA256SUMS not found next to bundle — aborting" >&2
	exit 3
fi

# --- SHA256 check ------------------------------------------------------------
echo "airgap-verify: checking SHA256 against $SUMS_FILE"
if ! (cd "$BUNDLE_DIR" && grep " $BUNDLE_BASE$" SHA256SUMS | sha256sum -c -); then
	echo "airgap-verify: SHA256 mismatch — REJECT bundle" >&2
	exit 1
fi

# --- Optional Ed25519 signature check ---------------------------------------
if [ -n "$PUBKEY" ]; then
	if [ ! -f "$PUBKEY" ]; then
		echo "airgap-verify: public key not found: $PUBKEY" >&2
		exit 3
	fi

	if [ ! -f "$SIG_FILE" ]; then
		echo "airgap-verify: public key supplied but SHA256SUMS.sig missing — REJECT" >&2
		exit 2
	fi

	if ! command -v ssh-keygen >/dev/null 2>&1; then
		echo "airgap-verify: ssh-keygen not installed — cannot verify signature" >&2
		exit 3
	fi

	# ssh-keygen -Y verify requires the pubkey in allowed_signers format;
	# write a throwaway one on the fly.
	ALLOWED="$(mktemp)"
	trap 'rm -f "$ALLOWED"' EXIT
	printf 'signer %s\n' "$(cat "$PUBKEY")" > "$ALLOWED"

	if ! ssh-keygen -Y verify -f "$ALLOWED" -I signer -n statnive-live-airgap \
		-s "$SIG_FILE" < "$SUMS_FILE" 2>&1; then
		echo "airgap-verify: Ed25519 signature mismatch — REJECT" >&2
		exit 2
	fi

	echo "airgap-verify: Ed25519 signature OK"
else
	echo "airgap-verify: no public key supplied — skipping signature check (SHA256-only)"
fi

echo "airgap-verify: OK — bundle ready to unpack"
