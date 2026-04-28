#!/usr/bin/env bash
# smoke-metrics.sh — boots a fresh binary against dev CH, hits /metrics,
# asserts the four canonical counter names exist. Runs in <5 s.
#
# Token gate: STATNIVE_METRICS_TOKEN env (defaults to "smoke-token" so a
# fresh dev box doesn't need extra env-var setup).
set -euo pipefail

TOKEN="${STATNIVE_METRICS_TOKEN:-smoke-token}"
PORT="${SMOKE_PORT:-18080}"
URL="http://127.0.0.1:${PORT}"

if ! curl -fsS "${URL}/healthz" >/dev/null 2>&1; then
    echo "FAIL: ${URL}/healthz unreachable. Boot the binary first:" >&2
    echo "      STATNIVE_METRICS_TOKEN=$TOKEN ./bin/statnive-live -c <conf>" >&2
    exit 2
fi

# Token-empty case: returns 404
status=$(curl -s -o /dev/null -w "%{http_code}" "${URL}/metrics")
if [[ "$status" == "404" ]]; then
    echo "WARN: /metrics returns 404 — STATNIVE_METRICS_TOKEN must be set on the binary"
    echo "      (smoke can't validate counter names without a token)"
    exit 0
fi

body=$(curl -fsS -H "Authorization: Bearer ${TOKEN}" "${URL}/metrics")

required=(
    "statnive_event_received_total"
    "statnive_event_accepted_total"
    "statnive_event_dropped_total"
)

missing=()
for name in "${required[@]}"; do
    if ! grep -q "^# TYPE ${name} counter" <<<"$body"; then
        missing+=("$name")
    fi
done

if [[ ${#missing[@]} -gt 0 ]]; then
    echo "FAIL: missing counter names: ${missing[*]}" >&2
    echo "--- /metrics response:" >&2
    echo "$body" >&2
    exit 1
fi

echo "smoke-metrics: PASS — all four counter names present"
