#!/usr/bin/env bash
# courier-iran.sh — backward-compat shim. Delegates to courier.sh with
# POSTURE=inside-iran. Accepts and forwards all env vars (VERSION, HOST,
# LICENSE, CERT_DIR, GEOIP_PATH, SKIP_BUILD, DRY_RUN, etc.).
#
# Prefer the generic `courier.sh` for new deployments:
#   POSTURE=inside-iran VERSION=... HOST=... LICENSE=... bash deploy/courier.sh
# or via Makefile:
#   make release-customer POSTURE=inside-iran VERSION=... HOST=... LICENSE=...
exec env POSTURE=inside-iran "$(dirname "$0")/courier.sh"
