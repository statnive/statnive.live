#!/usr/bin/env bash
# renew-hook.sh — invoked by acme.sh after a successful renewal. One
# arg: the fqdn whose cert just renewed. Delegates the push + SIGHUP to
# rsync-push.sh; keeps acme.sh's --renew-hook contract minimal.

set -euo pipefail

if [ $# -lt 1 ]; then
	echo "cert-forge renew-hook: missing fqdn arg" >&2
	exit 2
fi

FQDN="$1"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

bash "$SCRIPT_DIR/rsync-push.sh" "$FQDN"
