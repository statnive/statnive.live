#!/usr/bin/env bash
# issue.sh — one-shot first-issue driver for a single domain. Run once
# per customer hostname; subsequent rotations are handled automatically
# by acme.sh's daemon (or the systemd timer).
#
# Usage:
#   bash issue.sh <fqdn>
#
# Env (sourced from /etc/cert-forge/.env via the caller):
#   PARSIR_API_KEY            credentials for the DNS-01 challenge
#   PARSIR_API_ENDPOINT       (default: https://api.pars.ir/v1/dns)
#   ACME_EMAIL                LE account email
#   ACME_SERVER               LE directory URL (empty = production)
#   ACME_RENEW_DAYS           days-before-expiry to renew (default 30)

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

: "${PARSIR_API_KEY:?cert-forge: PARSIR_API_KEY required (see .env.example)}"
: "${ACME_EMAIL:?cert-forge: ACME_EMAIL required}"
ACME_SERVER_FLAG=()
if [ -n "${ACME_SERVER:-}" ]; then
	ACME_SERVER_FLAG=(--server "$ACME_SERVER")
fi
ACME_RENEW_DAYS="${ACME_RENEW_DAYS:-30}"

STATE_DIR="${CERT_FORGE_STATE:-/etc/cert-forge/state}"
mkdir -p "$STATE_DIR" /var/log/cert-forge

echo "cert-forge: issuing cert for $FQDN via Pars.ir DNS-01"

# Run acme.sh inside its container if installed via docker-compose;
# otherwise call the host-installed binary at /usr/local/bin/acme.sh.
if command -v docker >/dev/null 2>&1 && docker ps --format '{{.Names}}' | grep -q '^cert-forge-acme$'; then
	exec_acme() { docker exec cert-forge-acme acme.sh "$@"; }
elif command -v acme.sh >/dev/null 2>&1; then
	exec_acme() { acme.sh "$@"; }
else
	echo "cert-forge: neither cert-forge-acme container nor host acme.sh found" >&2
	echo "cert-forge: run 'docker compose -f /etc/cert-forge/docker-compose.yml up -d' first" >&2
	exit 1
fi

# Register account once (idempotent — acme.sh skips on re-run).
exec_acme --register-account -m "$ACME_EMAIL" "${ACME_SERVER_FLAG[@]}" >/dev/null

# Issue. dns_parsir reads PARSIR_API_KEY from the container env (loaded
# from /etc/cert-forge/.env via docker-compose env_file).
exec_acme --issue --dns dns_parsir -d "$FQDN" \
	"${ACME_SERVER_FLAG[@]}" \
	--days "$ACME_RENEW_DAYS" \
	--renew-hook "bash /etc/cert-forge/renew-hook.sh $FQDN" \
	2>&1 | tee -a /var/log/cert-forge/issue.log

echo "cert-forge: issued $FQDN; subsequent renewals are automatic"
echo "cert-forge: run 'bash /etc/cert-forge/rsync-push.sh $FQDN' to push the cert now"
