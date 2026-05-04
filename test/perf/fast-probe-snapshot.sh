#!/usr/bin/env bash
# Snapshot /metrics + /healthz from the production Netcup VPS.
# Usage:
#   STATNIVE_METRICS_TOKEN=... ./test/perf/fast-probe-snapshot.sh before > releases/load-gate/fast-probe-before.txt
#   STATNIVE_METRICS_TOKEN=... ./test/perf/fast-probe-snapshot.sh after  > releases/load-gate/fast-probe-after.txt
#
# Phase 7e Fast probe — Plan: ~/.claude/plans/phase-7e-load-gate-scaffolding-wise-puppy.md
set -euo pipefail

LABEL="${1:-snapshot}"
TOKEN="${STATNIVE_METRICS_TOKEN:?Set STATNIVE_METRICS_TOKEN before running}"
SSH_TARGET="${STATNIVE_SSH:-ops@94.16.108.78}"

echo "# Fast-probe ${LABEL} snapshot"
echo "# at $(date -u +%Y-%m-%dT%H:%M:%SZ) UTC"
echo "# host: ${SSH_TARGET}"
echo
echo "## /healthz"
ssh "${SSH_TARGET}" "curl -k -fsS -m 10 https://127.0.0.1/healthz"
echo
echo
echo "## /metrics (counters only)"
ssh "${SSH_TARGET}" "curl -k -fsS -m 10 -H 'Authorization: Bearer ${TOKEN}' https://127.0.0.1/metrics" \
  | grep -E "^(statnive_event_received_total|statnive_event_accepted_total|statnive_event_dropped_total)" \
  | sort
