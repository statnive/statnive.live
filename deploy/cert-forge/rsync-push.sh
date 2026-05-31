#!/usr/bin/env bash
# rsync-push.sh — push fullchain.pem + privkey.pem for one fqdn to the
# Asiatech production VPS, then SIGHUP statnive-live to hot-reload.
#
# Usage:  bash rsync-push.sh <fqdn>
# Env:    /etc/cert-forge/.env supplies ASIATECH_HOST + optional
#         ASIATECH_DOMAIN_TO_HOST mapping.

set -euo pipefail

if [ $# -lt 1 ]; then
	echo "usage: bash $0 <fqdn>" >&2
	exit 2
fi

FQDN="$1"
ENV_FILE="${CERT_FORGE_ENV:-/etc/cert-forge/.env}"

if [ -r "$ENV_FILE" ]; then
	# shellcheck disable=SC1090
	set -a; . "$ENV_FILE"; set +a
fi

# Resolve the target host. Per-domain mapping wins over the default.
TARGET="${ASIATECH_HOST:-}"
if [ -n "${ASIATECH_DOMAIN_TO_HOST:-}" ]; then
	# Parse "fqdn1:host1,fqdn2:host2" — fall back to default on no match.
	IFS=',' read -ra MAPPINGS <<< "$ASIATECH_DOMAIN_TO_HOST"
	for m in "${MAPPINGS[@]}"; do
		map_fqdn="${m%%:*}"
		map_host="${m#*:}"
		if [ "$map_fqdn" = "$FQDN" ]; then
			TARGET="$map_host"
			break
		fi
	done
fi

if [ -z "$TARGET" ]; then
	echo "cert-forge rsync-push: no ASIATECH_HOST and no mapping for $FQDN" >&2
	exit 1
fi

STATE_DIR="${CERT_FORGE_STATE:-/etc/cert-forge/state}"
SSH_KEY="${CERT_FORGE_SSH_KEY:-/etc/cert-forge/ssh/id_ed25519}"

# acme.sh's standard layout: state/<fqdn>/{fullchain.cer,*.key}. We push
# both as fullchain.pem + privkey.pem to match the statnive-live config
# example (config/statnive-live.yaml.example § "TLS").
CERT_SRC="$STATE_DIR/$FQDN/fullchain.cer"
KEY_SRC="$STATE_DIR/$FQDN/$FQDN.key"

for f in "$CERT_SRC" "$KEY_SRC"; do
	if [ ! -r "$f" ]; then
		echo "cert-forge rsync-push: $f not readable (issue.sh must run first)" >&2
		exit 1
	fi
done

# Stage to a temp dir with the binary-expected filenames, then rsync the
# whole tree. Keeps the source-side acme.sh paths and the target-side
# binary paths decoupled.
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
install -m 0644 "$CERT_SRC" "$STAGE/fullchain.pem"
install -m 0600 "$KEY_SRC"  "$STAGE/privkey.pem"

# Compression flag mandatory per LEARN.md Lesson 28 (Netcup→VPS hop).
SSH_OPTS=(-o "ConnectTimeout=10" -o "StrictHostKeyChecking=accept-new" -o "BatchMode=yes" -i "$SSH_KEY")

echo "cert-forge: rsync $STAGE/ → $TARGET:/etc/statnive-live/tls/"
rsync -avz --chmod=u=rw,g=r,o= --chown=root:statnive \
	-e "ssh ${SSH_OPTS[*]} -C" \
	"$STAGE/" "$TARGET:/etc/statnive-live/tls/"

# SIGHUP triggers the binary's cert-loader hot-reload (internal/cert/
# loader.go contract). We send via systemctl so the operator's audit log
# captures the reload event.
echo "cert-forge: SIGHUP statnive-live on $TARGET"
ssh "${SSH_OPTS[@]}" "$TARGET" 'sudo systemctl kill -s HUP statnive-live'

echo "cert-forge: $FQDN pushed + reload signaled"
