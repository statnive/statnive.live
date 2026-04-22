#!/usr/bin/env bash
#
# statnive-live end-to-end boot smoke.
#
# Drives the real cmd/statnive-live binary against docker-compose
# ClickHouse and probes every production surface a Phase 10 operator
# would touch: /healthz, /tracker.js, /app/ + hashed asset, POST
# /api/event with CH count-back, GET /api/stats/overview bearer auth.
#
# Usage:  ./test/smoke/harness.sh   (or `make smoke`)
# Env overrides: STATNIVE_SMOKE_{PORT,SITE,HOSTNAME,TOKEN,CH_CONTAINER,CH_ADDR}

set -euo pipefail

PORT="${STATNIVE_SMOKE_PORT:-18199}"
SITE_ID="${STATNIVE_SMOKE_SITE:-997}"
HOSTNAME_="${STATNIVE_SMOKE_HOSTNAME:-smoke.example.com}"
TOKEN="${STATNIVE_SMOKE_TOKEN:-smoke-tok-abc}"
CH_CONTAINER="${STATNIVE_SMOKE_CH_CONTAINER:-statnive-clickhouse-dev}"
CH_ADDR="${STATNIVE_SMOKE_CH_ADDR:-127.0.0.1:19000}"
EVENT_COUNT=10
COUNT_DEADLINE_SEC=10

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

GREEN=$(tput setaf 2 2>/dev/null || true)
RED=$(tput setaf 1 2>/dev/null || true)
YELLOW=$(tput setaf 3 2>/dev/null || true)
RESET=$(tput sgr0 2>/dev/null || true)

log()   { printf "%s[smoke]%s %s\n" "${GREEN}" "${RESET}" "$*"; }
warn()  { printf "%s[smoke]%s %s\n" "${YELLOW}" "${RESET}" "$*" 1>&2; }
fatal() { printf "%s[smoke]%s %s\n" "${RED}"   "${RESET}" "$*" 1>&2; exit 1; }

require() { command -v "$1" >/dev/null 2>&1 || fatal "missing required binary: $1"; }

require curl
require docker
require awk
require grep

# _assert <name> <condition-exit-code> <evidence>
# Invariant: condition is pre-evaluated by the caller — this just prints
# pass/fail. Keeps the assertion-site expressive (caller owns the boolean
# expression) while centralizing the print + exit semantics.
_assert() {
    local name="$1"
    local cond="$2"
    local evidence="$3"

    if [ "$cond" = "0" ]; then
        printf "%s ✅  %s%s\n" "${GREEN}" "${name}" "${RESET}"
    else
        printf "%s ❌  %s%s\n" "${RED}" "${name}" "${RESET}" 1>&2
        if [ -n "${evidence}" ]; then
            printf "    evidence:\n" 1>&2
            printf "%s\n" "${evidence}" | sed 's/^/      /' 1>&2
        fi
        exit 1
    fi
}

if command -v lsof >/dev/null 2>&1; then
    if lsof -nPiTCP:"${PORT}" -sTCP:LISTEN >/dev/null 2>&1; then
        fatal "port ${PORT} already in use — set STATNIVE_SMOKE_PORT to a free port"
    fi
fi

log "ensuring ClickHouse container (${CH_CONTAINER}) is up"
if ! docker ps --format '{{.Names}}' | grep -q "^${CH_CONTAINER}$"; then
    docker compose -f deploy/docker-compose.dev.yml up -d clickhouse
fi

for _ in $(seq 1 30); do
    if docker exec "$CH_CONTAINER" clickhouse-client --port 9000 -q "SELECT 1" >/dev/null 2>&1; then
        break
    fi
    sleep 1
done

docker exec "$CH_CONTAINER" clickhouse-client --port 9000 -q "SELECT 1" >/dev/null 2>&1 \
    || fatal "ClickHouse never accepted queries after 30s"

BIN="${REPO_ROOT}/bin/statnive-live"
[ -x "$BIN" ] || fatal "binary missing — run 'make build' (or invoke via 'make smoke')"

WORK="$(mktemp -d)"
WAL_DIR="${WORK}/wal"
AUDIT_PATH="${WORK}/audit.jsonl"
MASTER_KEY="${WORK}/master.key"
mkdir -p "$WAL_DIR"
head -c 32 /dev/urandom | xxd -p -c 64 > "$MASTER_KEY"
# Binary refuses master.key files looser than 0600 (config.LoadMasterSecret).
chmod 0600 "$MASTER_KEY"

cleanup() {
    if [ -n "${PID:-}" ]; then
        kill "${PID}" 2>/dev/null || true
        wait "${PID}" 2>/dev/null || true
    fi
    rm -rf "${WORK}" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

log "starting bin/statnive-live on 127.0.0.1:${PORT}"
STATNIVE_SERVER_LISTEN="127.0.0.1:${PORT}" \
STATNIVE_MASTER_SECRET_PATH="${MASTER_KEY}" \
STATNIVE_INGEST_WAL_DIR="${WAL_DIR}" \
STATNIVE_AUDIT_PATH="${AUDIT_PATH}" \
STATNIVE_CLICKHOUSE_ADDR="${CH_ADDR}" \
STATNIVE_DASHBOARD_SPA_ENABLED=true \
STATNIVE_DASHBOARD_BEARER_TOKEN="${TOKEN}" \
    "$BIN" >"${WORK}/stdout.log" 2>&1 &
PID=$!

for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
        break
    fi
    # If the binary already died, surface its logs immediately rather
    # than spinning for 30s against a dead socket.
    if ! kill -0 "$PID" 2>/dev/null; then
        cat "${WORK}/stdout.log" 1>&2 || true
        fatal "binary exited during boot — see logs above"
    fi
    sleep 0.2
done

curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1 \
    || fatal "/healthz never responded within 6s"
log "binary up + migrations applied"

# Seed runs AFTER /healthz so the binary's startup migrations have
# already created statnive.sites / statnive.events_raw. Idempotent via
# mutations_sync=2 DELETE — mirrors storagetest.SeedSite.
docker exec "$CH_CONTAINER" clickhouse-client --port 9000 -q \
    "ALTER TABLE statnive.sites DELETE WHERE site_id = ${SITE_ID} OR hostname = '${HOSTNAME_}' SETTINGS mutations_sync = 2" \
    >/dev/null 2>&1 || true
docker exec "$CH_CONTAINER" clickhouse-client --port 9000 -q \
    "ALTER TABLE statnive.events_raw DELETE WHERE site_id = ${SITE_ID} SETTINGS mutations_sync = 2" \
    >/dev/null 2>&1 || true
docker exec "$CH_CONTAINER" clickhouse-client --port 9000 -q \
    "INSERT INTO statnive.sites (site_id, hostname, slug, enabled) VALUES (${SITE_ID}, '${HOSTNAME_}', 'smoke', 1)" \
    >/dev/null 2>&1 || fatal "seed site row failed"

# ---------- Probes ----------

# probe_healthz: /healthz returns 200 + JSON with the four Phase 5a keys
#   (status / wal_fill_ratio / clickhouse / wal_fsync_p99_ms).
probe_healthz() {
    local body
    body=$(curl -fsS "http://127.0.0.1:${PORT}/healthz")
    local cond=1
    if echo "$body" | grep -q '"status"' \
        && echo "$body" | grep -q '"wal_fill_ratio"' \
        && echo "$body" | grep -q '"clickhouse"' \
        && echo "$body" | grep -q '"wal_fsync_p99_ms"'; then
        cond=0
    fi
    _assert "healthz: 200 + all 4 keys present" "$cond" "$body"
}

# _header_value: case-insensitive value lookup against a curl -D dump.
# Works under BSD grep (macOS) + GNU grep — awk IGNORECASE is GAWK-only.
# Normalizes CRLF and lowercases the output for easy substring matching.
_header_value() {
    local dump="$1"
    local name="$2"
    # `tr -d '\r'` drops curl's CRLF; `head -1` handles duplicate headers.
    grep -i "^${name}:" "$dump" | tr -d '\r' | tr 'A-Z' 'a-z' | head -1
}

# probe_tracker: /tracker.js returns the embedded IIFE with
# application/javascript + nosniff + the expected body shape.
probe_tracker() {
    local tmp
    tmp=$(mktemp)
    local status ctype nosniff size
    status=$(curl -sS -o "$tmp" -D "${tmp}.h" -w '%{http_code}' "http://127.0.0.1:${PORT}/tracker.js")
    ctype=$(_header_value "${tmp}.h" "content-type")
    nosniff=$(_header_value "${tmp}.h" "x-content-type-options")
    size=$(wc -c < "$tmp" | tr -d ' ')

    local cond=1
    if [ "$status" = "200" ] \
        && echo "$ctype" | grep -q 'application/javascript' \
        && echo "$nosniff" | grep -q 'nosniff' \
        && [ "$size" -gt 0 ] && [ "$size" -le 2000 ] \
        && head -c 10 "$tmp" | grep -q '^!function'; then
        cond=0
    fi
    local ev
    ev="status=${status} ctype=${ctype:-<missing>} nosniff=${nosniff:-<missing>} size=${size}"
    rm -f "$tmp" "${tmp}.h"
    _assert "tracker.js: 200 + application/javascript + nosniff + IIFE body" "$cond" "$ev"
}

# probe_spa_shell: /app/ returns the SPA index with CSP / nosniff /
# Referrer-Policy headers + mount div + bearer placeholder replaced.
probe_spa_shell() {
    local tmp
    tmp=$(mktemp)
    local status csp nosniff refpol
    status=$(curl -sS -L -o "$tmp" -D "${tmp}.h" -w '%{http_code}' "http://127.0.0.1:${PORT}/app/")
    csp=$(_header_value "${tmp}.h" "content-security-policy")
    nosniff=$(_header_value "${tmp}.h" "x-content-type-options")
    refpol=$(_header_value "${tmp}.h" "referrer-policy")

    local cond=1
    if [ "$status" = "200" ] \
        && echo "$csp" | grep -q "default-src 'self'" \
        && echo "$nosniff" | grep -q 'nosniff' \
        && echo "$refpol" | grep -q 'strict-origin-when-cross-origin' \
        && grep -q '<div id="statnive-app">' "$tmp" \
        && grep -q "content=\"${TOKEN}\"" "$tmp" \
        && ! grep -q 'STATNIVE_BEARER_PLACEHOLDER' "$tmp"; then
        cond=0
    fi
    local ev
    ev=$(printf 'status=%s\ncsp=%s\nnosniff=%s\nrefpol=%s\nbody-head=%s' \
        "$status" "${csp:-<missing>}" "${nosniff:-<missing>}" "${refpol:-<missing>}" \
        "$(head -c 400 "$tmp")")
    rm -f "$tmp" "${tmp}.h"
    _assert "/app/: 200 + CSP+nosniff+refpol + mount div + bearer injected" "$cond" "$ev"
}

# probe_spa_asset: pull the hashed .js filename out of the shell HTML,
# curl it, assert 200 + long-cache + body ≥ 5 KB (real bundle, not HTML
# fallback). Extraction is resilient to either single or double quotes
# around the src attribute.
probe_spa_asset() {
    local html
    html=$(curl -fsS "http://127.0.0.1:${PORT}/app/")
    local asset
    asset=$(printf '%s' "$html" | grep -oE '/app/assets/index-[A-Za-z0-9_-]+\.js' | head -1)
    if [ -z "$asset" ]; then
        _assert "/app/assets/*.js: asset URL extracted from shell" 1 \
            "no /app/assets/index-*.js match in shell HTML"
        return
    fi

    local tmp
    tmp=$(mktemp)
    local status ctype cache
    status=$(curl -sS -o "$tmp" -D "${tmp}.h" -w '%{http_code}' "http://127.0.0.1:${PORT}${asset}")
    ctype=$(_header_value "${tmp}.h" "content-type")
    cache=$(_header_value "${tmp}.h" "cache-control")
    local size
    size=$(wc -c < "$tmp" | tr -d ' ')

    local cond=1
    if [ "$status" = "200" ] \
        && echo "$ctype" | grep -q 'javascript' \
        && echo "$cache" | grep -q 'max-age=31536000' \
        && [ "$size" -ge 5000 ]; then
        cond=0
    fi
    local ev
    ev="asset=${asset} status=${status} ctype=${ctype:-<missing>} cache=${cache:-<missing>} size=${size}"
    rm -f "$tmp" "${tmp}.h"
    _assert "/app/assets/*.js: 200 + javascript + long-cache + size≥5KB" "$cond" "$ev"
}

# probe_ingest: fire EVENT_COUNT pageviews; each MUST return 202. Header
# set mirrors the real tracker + the integration test: Content-Type is
# text/plain (sendBeacon contract), UA is BrowserLike so FastReject
# doesn't 204 us.
probe_ingest() {
    local accepted=0
    local i body status last_body=""
    for i in $(seq 1 "$EVENT_COUNT"); do
        body=$(printf '{"hostname":"%s","pathname":"/smoke-%02d","event_type":"pageview","event_name":"pageview"}' \
               "$HOSTNAME_" "$i")
        status=$(curl -sS -o /dev/null -w '%{http_code}' \
            -H "Content-Type: text/plain" \
            -H "User-Agent: Mozilla/5.0 (SmokeTest/1.0) BrowserLike" \
            -X POST --data-binary "$body" \
            "http://127.0.0.1:${PORT}/api/event")
        if [ "$status" = "202" ]; then
            accepted=$((accepted + 1))
        else
            last_body="event #${i} got status=${status} (body=${body})"
        fi
    done
    local cond=1
    [ "$accepted" -eq "$EVENT_COUNT" ] && cond=0
    _assert "ingest: ${accepted}/${EVENT_COUNT} events returned 202" "$cond" "$last_body"
}

# probe_ingest_count: poll count(events_raw) until ≥ EVENT_COUNT or the
# deadline expires. 200 ms cadence — docker-exec overhead alone is
# ~30-80ms per call, so tighter polling wastes CPU without gaining
# detection latency (consumer flush interval is 500ms).
probe_ingest_count() {
    local deadline=$((SECONDS + COUNT_DEADLINE_SEC))
    local got=0
    while [ "$SECONDS" -lt "$deadline" ]; do
        got=$(docker exec "$CH_CONTAINER" clickhouse-client --port 9000 -q \
              "SELECT count() FROM statnive.events_raw WHERE site_id = ${SITE_ID}" 2>/dev/null \
              | tr -d '[:space:]' || echo 0)
        if [ "${got:-0}" -ge "$EVENT_COUNT" ]; then
            break
        fi
        sleep 0.2
    done
    local cond=1
    if [ "${got:-0}" -eq "$EVENT_COUNT" ]; then
        cond=0
    fi
    _assert "ClickHouse: events_raw count == ${EVENT_COUNT} (got ${got})" "$cond" \
        "polled every 200ms for up to ${COUNT_DEADLINE_SEC}s"
}

# probe_stats_auth: bearer enforcement AND happy-path response shape.
#   - No header → 401 (middleware is wired)
#   - Correct header → 200 + all 5 KPI keys in the JSON body
probe_stats_auth() {
    local url="http://127.0.0.1:${PORT}/api/stats/overview?site=${SITE_ID}"

    local status_no
    status_no=$(curl -sS -o /dev/null -w '%{http_code}' "$url")
    local cond=1
    [ "$status_no" = "401" ] && cond=0
    _assert "stats/overview without bearer: 401" "$cond" "got status=${status_no}"

    local tmp status_yes body
    tmp=$(mktemp)
    status_yes=$(curl -sS -o "$tmp" -w '%{http_code}' -H "Authorization: Bearer ${TOKEN}" "$url")
    body=$(cat "$tmp")
    rm -f "$tmp"
    cond=1
    if [ "$status_yes" = "200" ] \
        && echo "$body" | grep -q '"pageviews"' \
        && echo "$body" | grep -q '"visitors"' \
        && echo "$body" | grep -q '"goals"' \
        && echo "$body" | grep -q '"revenue_rials"' \
        && echo "$body" | grep -q '"rpv_rials"'; then
        cond=0
    fi
    _assert "stats/overview with bearer: 200 + 5 KPI keys" "$cond" \
        "status=${status_yes} body=${body}"
}

# ---------- Run the probe matrix ----------

log "probing /healthz + /tracker.js + /app/ + /app/assets/"
probe_healthz
probe_tracker
probe_spa_shell
probe_spa_asset

log "probing /api/event (ingest round-trip to ClickHouse)"
probe_ingest
probe_ingest_count

log "probing /api/stats/overview (bearer auth + KPI shape)"
probe_stats_auth

log "=== all probes green ==="
exit 0
