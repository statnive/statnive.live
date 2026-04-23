#!/usr/bin/env bash
# Wrap `docker run altinity/clickhouse-backup` so drill.sh can call it as
# if it were a local binary. Mounts the shared CH data volume, bind-mounts
# the workspace read-only so relative --config paths resolve inside the
# container, and forwards the canonical env set. Used by the nightly CI
# drill and reusable from a developer workstation.
#
# Env passthroughs:
#   CLICKHOUSE_PASSWORD, S3_ACCESS_KEY, S3_SECRET_KEY, S3_ENDPOINT,
#   CH_BACKUP_VERSION (defaults to 2.5.20 — Docker Hub format, no `v`;
#                       runbook SOP uses v2.5.20 for the GitHub tarball URL)
set -euo pipefail

WORKSPACE="${GITHUB_WORKSPACE:-$(pwd)}"
VERSION="${CH_BACKUP_VERSION:-2.5.20}"

exec docker run --rm --network=host \
  -v statnive_ch_data:/var/lib/clickhouse \
  -v "${WORKSPACE}:${WORKSPACE}:ro" \
  -w "${WORKSPACE}" \
  -e CLICKHOUSE_PASSWORD="${CLICKHOUSE_PASSWORD:-}" \
  -e S3_ACCESS_KEY="${S3_ACCESS_KEY:-}" \
  -e S3_SECRET_KEY="${S3_SECRET_KEY:-}" \
  -e S3_ENDPOINT="${S3_ENDPOINT:-}" \
  "altinity/clickhouse-backup:${VERSION}" "$@"
