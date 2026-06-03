#!/usr/bin/env bash
#
# prod-probe-ch-oracle.sh — SSH wrapper that runs clickhouse-client
# queries on the production host for the live post-deploy probe.
# Mirrors web/e2e/fixtures/chOracle.ts but over SSH rather than
# `docker exec` against a local container.
#
# Usage:
#   prod-probe-ch-oracle.sh <sql>
#
# Env (all required, set by the parent prod-probe.sh):
#   STATNIVE_PROBE_SSH — ssh target, e.g. ops@94.16.108.78
#   STATNIVE_PROBE_CH_CONTAINER — defaults to statnive-clickhouse-prod
#
# Returns the query result on stdout. Exit code 0 on success.
#
# Safety: this script only RUNS the SQL passed by the caller. The
# caller (prod-probe.sh) is responsible for limiting the SQL to
# site_id=STATNIVE_PROBE_SITE_ID. The SSH key used should be
# restricted to clickhouse-client invocations via command="..." in
# ~/.ssh/authorized_keys on the production host — see the plan's
# § Production probe — safety design § "SSH key compromised".

set -euo pipefail

: "${STATNIVE_PROBE_SSH:?STATNIVE_PROBE_SSH is required (e.g. ops@host)}"

CH_CONTAINER="${STATNIVE_PROBE_CH_CONTAINER:-statnive-clickhouse-prod}"

if [ $# -eq 0 ]; then
    echo "usage: $0 <sql>" 1>&2
    exit 64
fi

SQL="$1"

# clickhouse-client --query "..." is the canonical one-shot path.
# Wrapping in `docker exec -i` keeps stdout clean; -T disables ssh
# pseudo-tty allocation so the output isn't decorated with terminal
# control codes.
ssh -T "${STATNIVE_PROBE_SSH}" "docker exec ${CH_CONTAINER} clickhouse-client --query \"${SQL//\"/\\\"}\""
