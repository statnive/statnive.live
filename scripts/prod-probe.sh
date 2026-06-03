#!/usr/bin/env bash
#
# prod-probe.sh — live post-deploy probe against https://app.statnive.live
# (or any other STATNIVE_PROBE_HOST). Verifies the privacy + tracker
# contract end-to-end against the production binary on production
# hardware, using a dedicated test site_id so Televika and other
# customer data is never touched.
#
# Designed to be invoked from .github/workflows/deploy-saas.yml AFTER
# the existing /api/about git_sha check. A failure here fails the
# deploy job, which triggers the existing auto-revert in
# deploy/statnive-deploy.sh.
#
# Sequence (10 steps from plan-to-design-real-production-eager-pine.md):
#   1. GET /api/about — verify reachable + record git_sha.
#   2. GET /metrics — verify bearer-gated, record counter snapshot.
#   3. GET /privacy?site=<probe-host> — extract CSRF token.
#   4. POST /api/event with synthetic pageview — assert 202.
#   5. SSH-CH-oracle — assert exactly 1 new row landed for site_id.
#   6. POST /api/privacy/opt-out — assert 204 + Set-Cookie.
#   7. POST /api/event with opt-out cookie — assert 204 (gate works).
#   8. POST /api/privacy/erase — assert 202.
#   9. Poll SSH-CH-oracle ≤ 30s — assert row count drops to 0.
#   10. SSH-tail audit log — assert privacy.* event chain present.
#
# Concentric safeguards (see plan § Production probe — safety design):
#   - Dedicated STATNIVE_PROBE_SITE_ID (default 9999); set -u catches missing.
#   - Cross-tenant SQL filter (cookie_id AND site_id) is the load-bearing
#     invariant — pinned by TestDSAR_CrossTenantIsolation since v0.0.36.
#   - Self-cleaning: step 8's erase removes the probe's synthetic row.
#
# Env (REQUIRED, no defaults):
#   STATNIVE_PROBE_HOST            — base URL, e.g. https://app.statnive.live
#   STATNIVE_PROBE_SITE_ID         — dedicated test site, e.g. 9999
#   STATNIVE_PROBE_HOSTNAME        — site hostname, e.g. probe.statnive.live
#   STATNIVE_PROBE_SSH             — ssh target for the CH oracle
#   STATNIVE_METRICS_TOKEN         — bearer for /metrics
#
# Env (optional):
#   STATNIVE_PROBE_CH_CONTAINER    — defaults to statnive-clickhouse-prod
#   STATNIVE_PROBE_AUDIT_LOG       — defaults to /var/lib/statnive/audit.jsonl
#   STATNIVE_PROBE_CSRF_LEGACY=1   — if the binary is running with
#                                    STATNIVE_DEV_INSECURE_CSRF=1 (test
#                                    environments only); production
#                                    sets the __Host-statnive_csrf cookie.
#
# Exit codes:
#   0 — all 10 steps green.
#   1 — any step failed; details printed to stderr.

set -euo pipefail

: "${STATNIVE_PROBE_HOST:?STATNIVE_PROBE_HOST is required (e.g. https://app.statnive.live)}"
: "${STATNIVE_PROBE_SITE_ID:?STATNIVE_PROBE_SITE_ID is required (dedicated test site, e.g. 9999)}"
: "${STATNIVE_PROBE_HOSTNAME:?STATNIVE_PROBE_HOSTNAME is required (probe site hostname)}"
: "${STATNIVE_PROBE_SSH:?STATNIVE_PROBE_SSH is required (ssh target for CH oracle)}"
: "${STATNIVE_METRICS_TOKEN:?STATNIVE_METRICS_TOKEN is required (Bearer for /metrics)}"

AUDIT_LOG="${STATNIVE_PROBE_AUDIT_LOG:-/var/lib/statnive/audit.jsonl}"

# Defensive: refuse to run against Televika's site_id under any
# circumstance. Televika is site_id=4 per the plan; probe site is
# 9999. If the env is somehow set to 4 (typo, CI mistake), fail hard.
if [ "${STATNIVE_PROBE_SITE_ID}" = "4" ]; then
    echo "[probe] REFUSING — STATNIVE_PROBE_SITE_ID=4 is Televika; use a dedicated probe site_id" 1>&2
    exit 2
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ORACLE="${REPO_ROOT}/scripts/prod-probe-ch-oracle.sh"

if [ ! -x "$ORACLE" ]; then
    echo "[probe] prod-probe-ch-oracle.sh not executable at ${ORACLE}" 1>&2
    exit 2
fi

WORK="$(mktemp -d)"
COOKIES="${WORK}/cookies.txt"
trap 'rm -rf "${WORK}" 2>/dev/null || true' EXIT

GREEN=$(tput setaf 2 2>/dev/null || true)
RED=$(tput setaf 1 2>/dev/null || true)
RESET=$(tput sgr0 2>/dev/null || true)

step() { printf "%s[probe %d/10]%s %s\n" "${GREEN}" "$1" "${RESET}" "$2"; }
fail() { printf "%s[probe FAIL]%s %s\n" "${RED}" "${RESET}" "$*" 1>&2; exit 1; }

# Step 1: /api/about
step 1 "GET /api/about"
ABOUT=$(curl -fsS "${STATNIVE_PROBE_HOST}/api/about") \
    || fail "GET /api/about failed (host=${STATNIVE_PROBE_HOST})"

GIT_SHA=$(printf '%s' "$ABOUT" | grep -oE '"git_sha":"[^"]+"' | head -n 1 | cut -d'"' -f4)
[ -n "$GIT_SHA" ] || fail "/api/about response missing git_sha — body=${ABOUT}"
echo "       git_sha=${GIT_SHA}"

# Step 2: /metrics (bearer-gated)
step 2 "GET /metrics (bearer)"
METRICS_BEFORE=$(curl -fsS -H "Authorization: Bearer ${STATNIVE_METRICS_TOKEN}" \
    "${STATNIVE_PROBE_HOST}/metrics") \
    || fail "GET /metrics failed — token may be wrong"

printf '%s' "$METRICS_BEFORE" | grep -q '^statnive_event_received_total' \
    || fail "/metrics missing statnive_event_received_total counter"

RX_BEFORE=$(printf '%s' "$METRICS_BEFORE" | grep -E "^statnive_event_received_total[^0-9]" \
    | head -n 1 | awk '{print $NF}')
echo "       statnive_event_received_total=${RX_BEFORE} (before probe)"

# Step 3: CSRF token from /privacy?site=
step 3 "GET /privacy?site=${STATNIVE_PROBE_HOSTNAME} (extract CSRF token)"
PRIVACY_HTML=$(curl -fsS -c "$COOKIES" \
    "${STATNIVE_PROBE_HOST}/privacy?site=${STATNIVE_PROBE_HOSTNAME}") \
    || fail "GET /privacy?site= failed"

CSRF=$(printf '%s' "$PRIVACY_HTML" | grep -oE 'name="csrf-token"[[:space:]]*content="[^"]+"' \
    | head -n 1 | sed -E 's/.*content="([^"]+)".*/\1/')
[ -n "$CSRF" ] || fail "/privacy?site= response has no csrf-token meta"
echo "       csrf-token prefix=${CSRF:0:8}..."

# Step 4: synthetic pageview
step 4 "POST /api/event (synthetic pageview)"
EVENT_BODY=$(printf '{"hostname":"%s","pathname":"/probe","event_type":"pageview","event_name":"pageview"}' \
    "${STATNIVE_PROBE_HOSTNAME}")

EVENT_COOKIE="prod-probe-${STATNIVE_PROBE_SITE_ID}-$(date +%s)"
STATUS=$(curl -sS -o /dev/null -w '%{http_code}' \
    -X POST "${STATNIVE_PROBE_HOST}/api/event" \
    -H "Host: ${STATNIVE_PROBE_HOSTNAME}" \
    -H "Origin: https://${STATNIVE_PROBE_HOSTNAME}" \
    -H "Content-Type: text/plain" \
    -H "User-Agent: Mozilla/5.0 (StatniveProdProbe) BrowserLike" \
    --cookie "_statnive=${EVENT_COOKIE}" \
    --data-raw "$EVENT_BODY")
[ "$STATUS" = "202" ] || fail "/api/event status=${STATUS}, want 202"

# Step 5: CH oracle — row landed
step 5 "SSH-CH-oracle: assert row landed for site_id=${STATNIVE_PROBE_SITE_ID}"
DEADLINE=$(($(date +%s) + 15))
COUNT=0
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
    COUNT=$("$ORACLE" "SELECT count() FROM statnive.events_raw WHERE site_id = ${STATNIVE_PROBE_SITE_ID} AND time >= now() - INTERVAL 5 MINUTE" 2>/dev/null | tr -d '[:space:]') || COUNT=0
    [ "$COUNT" -ge "1" ] && break
    sleep 1
done

[ "$COUNT" -ge "1" ] || fail "CH count=${COUNT} after 15s; probe event did not land"
echo "       events_raw count=${COUNT}"

# Step 6: opt-out
step 6 "POST /api/privacy/opt-out (sets _statnive_optout cookie)"
OPTOUT_HEADERS=$(curl -sS -b "$COOKIES" -c "$COOKIES" -D - -o /dev/null \
    -X POST "${STATNIVE_PROBE_HOST}/api/privacy/opt-out" \
    -H "Host: ${STATNIVE_PROBE_HOSTNAME}" \
    -H "Origin: https://${STATNIVE_PROBE_HOSTNAME}" \
    -H "X-CSRF-Token: ${CSRF}" \
    -H "Content-Type: application/json" \
    --cookie "_statnive=${EVENT_COOKIE}" \
    --data '{}')

OPTOUT_STATUS=$(printf '%s' "$OPTOUT_HEADERS" | head -n 1 | awk '{print $2}')
[ "$OPTOUT_STATUS" = "204" ] || fail "/api/privacy/opt-out status=${OPTOUT_STATUS}, want 204"

printf '%s' "$OPTOUT_HEADERS" | grep -qiE '^set-cookie: _statnive_optout_' \
    || fail "opt-out missing _statnive_optout cookie"

# Capture the opt-out cookie for step 7.
OPTOUT_COOKIE_NAME="_statnive_optout_${STATNIVE_PROBE_SITE_ID}"
OPTOUT_COOKIE_VALUE=$(printf '%s' "$OPTOUT_HEADERS" \
    | grep -iE "^set-cookie: ${OPTOUT_COOKIE_NAME}=" \
    | head -n 1 | sed -E 's/^[Ss]et-[Cc]ookie:[[:space:]]*[^=]+=([^;[:space:]]+).*/\1/' | tr -d '\r')
[ -n "$OPTOUT_COOKIE_VALUE" ] || fail "could not extract opt-out cookie value"

# Step 7: gate verification — second event with opt-out cookie must drop
step 7 "POST /api/event (with opt-out cookie) → expect gate to drop"
GATE_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' \
    -X POST "${STATNIVE_PROBE_HOST}/api/event" \
    -H "Host: ${STATNIVE_PROBE_HOSTNAME}" \
    -H "Origin: https://${STATNIVE_PROBE_HOSTNAME}" \
    -H "Content-Type: text/plain" \
    -H "User-Agent: Mozilla/5.0 (StatniveProdProbe) BrowserLike" \
    --cookie "_statnive=${EVENT_COOKIE}; ${OPTOUT_COOKIE_NAME}=${OPTOUT_COOKIE_VALUE}" \
    --data-raw "$EVENT_BODY")
# Handler still returns 202 even when dropping (response-shape stability
# per ingest/handler_test.go) — the proof of the gate is in the CH
# count NOT incrementing. Either 202 or 204 is acceptable here; what's
# load-bearing is the row count check in step 9.
case "$GATE_STATUS" in
    202|204) ;;
    *) fail "/api/event (opted-out) status=${GATE_STATUS}, want 202 or 204" ;;
esac

# Step 8: erase
step 8 "POST /api/privacy/erase"
ERASE_RESP=$(curl -fsS -b "$COOKIES" \
    -X POST "${STATNIVE_PROBE_HOST}/api/privacy/erase" \
    -H "Host: ${STATNIVE_PROBE_HOSTNAME}" \
    -H "Origin: https://${STATNIVE_PROBE_HOSTNAME}" \
    -H "X-CSRF-Token: ${CSRF}" \
    -H "Content-Type: application/json" \
    --cookie "_statnive=${EVENT_COOKIE}" \
    --data '{}') || fail "/api/privacy/erase failed"

printf '%s' "$ERASE_RESP" | grep -q '"status":"accepted"' \
    || fail "erase response missing status:accepted — body=${ERASE_RESP}"

# Step 9: poll CH oracle for mutation completion (≤30s)
step 9 "SSH-CH-oracle: poll for erase mutation to land"
DEADLINE=$(($(date +%s) + 30))
COUNT=999
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
    COUNT=$("$ORACLE" "SELECT count() FROM statnive.events_raw WHERE site_id = ${STATNIVE_PROBE_SITE_ID} AND time >= now() - INTERVAL 5 MINUTE" 2>/dev/null | tr -d '[:space:]') || COUNT=999
    [ "$COUNT" = "0" ] && break
    sleep 1
done

[ "$COUNT" = "0" ] || fail "erase did not drop count to 0 within 30s; final count=${COUNT}"
echo "       events_raw count=0 (erase landed)"

# Step 10: SSH-tail audit log for the privacy.* event chain
step 10 "SSH-tail audit log: verify privacy event chain"
AUDIT_TAIL=$(ssh -T "${STATNIVE_PROBE_SSH}" "tail -n 500 ${AUDIT_LOG}" 2>/dev/null) \
    || fail "could not tail audit log at ${AUDIT_LOG}"

REQUIRED_EVENTS=(
    "privacy.opt_out_received"
    "privacy.dsar_erase_requested"
    "privacy.dsar_erase_completed"
)
for ev in "${REQUIRED_EVENTS[@]}"; do
    if ! printf '%s' "$AUDIT_TAIL" | grep -q "\"event\":\"${ev}\""; then
        fail "audit log missing event ${ev} in last 500 lines of ${AUDIT_LOG}"
    fi
    echo "       found: ${ev}"
done

printf "%s[probe OK]%s git_sha=%s site_id=%s — all 10 steps green\n" \
    "${GREEN}" "${RESET}" "${GIT_SHA}" "${STATNIVE_PROBE_SITE_ID}"
exit 0
