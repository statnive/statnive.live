#!/usr/bin/env bash
# load-gate-crosscheck.sh — k6 vs Locust p99 sanity check (doc 29 §3.1).
#
# Runs both load tools at the same arrival rate against the same target.
# Phase 7e ships a thin wrapper; expansion (json output parsing, real
# delta enforcement) lands at Phase 10 when this becomes a CI gate.
set -euo pipefail

TARGET="${1:-http://127.0.0.1:8080}"
DURATION="${LOAD_GATE_CROSSCHECK_DURATION:-1m}"

require() { command -v "$1" >/dev/null 2>&1 || { echo "FAIL: missing $1"; exit 2; }; }
require k6
require python3
if ! python3 -c "import locust" 2>/dev/null; then
    echo "FAIL: locust not installed. Activate venv + 'pip install -r test/perf/gate/requirements.txt'" >&2
    exit 2
fi

probe() {
    if ! curl -fsS "${TARGET}/healthz" >/dev/null; then
        echo "FAIL: ${TARGET}/healthz unreachable" >&2; exit 1
    fi
}
probe

echo "==> k6 ${DURATION}"
STATNIVE_URL="$TARGET" k6 run \
    --duration "$DURATION" \
    --quiet \
    test/perf/load.js | tee build/crosscheck-k6.txt

echo "==> locust ${DURATION} (single-node, single-worker)"
STATNIVE_URL="$TARGET" locust \
    -f test/perf/gate/locustfile.py \
    --headless \
    --users 200 \
    --spawn-rate 50 \
    --run-time "$DURATION" \
    --host "$TARGET" 2>&1 | tee build/crosscheck-locust.txt

echo "==> cross-check artifacts in build/. Manual p99 delta inspection;"
echo "==> Phase 10 wires automated parsing + 5%-delta assertion."
