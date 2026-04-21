#!/usr/bin/env bash
#
# wal-durability-review kill-9 CI gate.
#
# Builds the binary, starts ClickHouse via docker-compose, and for N
# iterations:
#   1. spawn the binary on $PORT
#   2. fire $EVENTS events via the existing Go client (a thin wrapper
#      over test/perf/perf.go:FireEvents)
#   3. SIGKILL at a random offset in [100ms, 2s]
#   4. restart the binary (same WAL dir)
#   5. poll count(events_raw) until quiescent (doesn't advance for 2s)
#   6. assert count >= sent * (1 - 0.0005)  -- 0.05% loss SLO
#
# Aggregates pass/fail across all iterations; non-zero exit on any
# failure. Usage: harness.sh [N]  (N defaults to 5; Phase 7b1b
# chained-into-audit variant runs 5, `make wal-killtest-full` runs 50).

set -euo pipefail

ITERATIONS="${1:-5}"
EVENTS="${WAL_KILLTEST_EVENTS:-10000}"
HOSTNAME="${WAL_KILLTEST_HOSTNAME:-wal-killtest.example.com}"
SITE_ID="${WAL_KILLTEST_SITE_ID:-905}"
PORT="${WAL_KILLTEST_PORT:-18090}"
CH_CONTAINER="${WAL_KILLTEST_CH_CONTAINER:-statnive-clickhouse-dev}"
CH_ADDR="${WAL_KILLTEST_CH_ADDR:-127.0.0.1:19000}"
LOSS_BUDGET="${WAL_KILLTEST_LOSS_BUDGET:-0.0005}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../../.." && pwd)"
cd "$REPO_ROOT"

GREEN=$(tput setaf 2 2>/dev/null || true)
RED=$(tput setaf 1 2>/dev/null || true)
YELLOW=$(tput setaf 3 2>/dev/null || true)
RESET=$(tput sgr0 2>/dev/null || true)

log()   { printf "%s[kill9]%s %s\n" "${GREEN}" "${RESET}" "$*"; }
warn()  { printf "%s[kill9]%s %s\n" "${YELLOW}" "${RESET}" "$*" 1>&2; }
fatal() { printf "%s[kill9]%s %s\n" "${RED}"   "${RESET}" "$*" 1>&2; exit 1; }

require() { command -v "$1" >/dev/null 2>&1 || fatal "missing required binary: $1"; }

require go
require docker
require awk

# 1. Ensure ClickHouse is up.
log "bringing up ClickHouse container (${CH_CONTAINER})"
if ! docker ps --format '{{.Names}}' | grep -q "^${CH_CONTAINER}$"; then
    docker compose -f deploy/docker-compose.dev.yml up -d clickhouse
fi

# Wait for CH to accept connections.
for _ in $(seq 1 30); do
    if docker exec "$CH_CONTAINER" clickhouse-client --port 9000 -q "SELECT 1" >/dev/null 2>&1; then
        break
    fi
    sleep 1
done

# 2. Build once into a temp dir so each iteration reuses the binary.
BIN="$(mktemp -d)/statnive-live"
log "building binary → $BIN"
CGO_ENABLED=0 go build -mod=vendor -o "$BIN" ./cmd/statnive-live

# 3. Seed the test site row. Idempotent.
log "seeding sites table (site_id=${SITE_ID}, hostname=${HOSTNAME})"
docker exec "$CH_CONTAINER" clickhouse-client --port 9000 -q \
    "INSERT INTO statnive.sites (site_id, hostname, slug, enabled) VALUES (${SITE_ID}, '${HOSTNAME}', 'wal-killtest', 1)" \
    2>/dev/null || true

# Build a tiny go program that fires events at the binary. Reuses
# test/perf/perf.go:FireEvents via the exported tag.
FIRE_BIN="$(mktemp -d)/fire"
cat > "${FIRE_BIN}.go" <<'FIREGO'
package main

import (
    "context"
    "flag"
    "fmt"
    "net/http"
    "os"
    "strings"
    "time"
)

func main() {
    url := flag.String("url", "http://127.0.0.1:18090/api/event", "")
    hostname := flag.String("hostname", "wal-killtest.example.com", "")
    count := flag.Int("count", 10000, "")
    rate := flag.Int("rate", 2000, "")
    flag.Parse()

    body := fmt.Sprintf(`{"hostname":%q,"pathname":"/k","event_type":"pageview","event_name":"pageview"}`, *hostname)
    client := &http.Client{Timeout: 2 * time.Second}
    interval := time.Second / time.Duration(*rate)
    t0 := time.Now()

    var sent int
    for i := 0; i < *count; i++ {
        req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, *url, strings.NewReader(body))
        req.Header.Set("User-Agent", "Mozilla/5.0 (Kill9/1.0) BrowserLike")
        req.Header.Set("Content-Type", "text/plain")
        resp, err := client.Do(req)
        if err == nil {
            if resp.StatusCode/100 == 2 {
                sent++
            }
            _ = resp.Body.Close()
        }
        next := t0.Add(time.Duration(i+1) * interval)
        if d := time.Until(next); d > 0 {
            time.Sleep(d)
        }
    }
    fmt.Fprintln(os.Stdout, sent)
}
FIREGO
go build -mod=vendor -o "$FIRE_BIN" "${FIRE_BIN}.go"

# 4. Iteration loop.
PASS=0
FAIL=0

for iter in $(seq 1 "$ITERATIONS"); do
    log "=== iteration $iter / $ITERATIONS ==="

    WAL_DIR="$(mktemp -d)/wal"
    AUDIT="$(mktemp -d)/audit.jsonl"
    MASTER_KEY="$(mktemp)"
    head -c 32 /dev/urandom | xxd -p -c 64 > "$MASTER_KEY"

    # Wipe prior events for this test's site. mutations_sync=2 makes the
    # DELETE block until the merge completes — without it the async mutation
    # races the consumer's inserts and silently wipes the very rows we're
    # about to assert exist (showed up as landed=0 in CI, while local was
    # green because slower CH timing hid the race).
    docker exec "$CH_CONTAINER" clickhouse-client --port 9000 -q \
        "ALTER TABLE statnive.events_raw DELETE WHERE site_id = ${SITE_ID} SETTINGS mutations_sync = 2" >/dev/null 2>&1 || true

    # Start binary.
    STATNIVE_SERVER_LISTEN="127.0.0.1:${PORT}" \
    STATNIVE_CLICKHOUSE_ADDR="${CH_ADDR}" \
    STATNIVE_INGEST_WAL_DIR="${WAL_DIR}" \
    STATNIVE_AUDIT_PATH="${AUDIT}" \
    STATNIVE_MASTER_SECRET_PATH="${MASTER_KEY}" \
    STATNIVE_RATELIMIT_REQUESTS_PER_MINUTE="120000" \
        "$BIN" &
    PID=$!

    # Wait for /healthz.
    for _ in $(seq 1 30); do
        if curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
            break
        fi
        sleep 0.2
    done

    # Fire in the background.
    "$FIRE_BIN" -url "http://127.0.0.1:${PORT}/api/event" -hostname "$HOSTNAME" -count "$EVENTS" -rate 2000 > "/tmp/wal-killtest-sent-${iter}" &
    FIRE_PID=$!

    # Random offset in [100, 2000] ms.
    OFFSET_MS=$(( (RANDOM % 1901) + 100 ))
    sleep "$(awk "BEGIN{print ${OFFSET_MS}/1000}")"

    log "SIGKILL at +${OFFSET_MS}ms"
    kill -9 "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
    # Let the firer notice the socket is gone.
    wait "$FIRE_PID" 2>/dev/null || true

    SENT=$(tail -n 1 "/tmp/wal-killtest-sent-${iter}" 2>/dev/null || echo 0)
    log "client received 2xx for $SENT events before kill"

    # Restart with same WAL dir.
    STATNIVE_SERVER_LISTEN="127.0.0.1:${PORT}" \
    STATNIVE_CLICKHOUSE_ADDR="${CH_ADDR}" \
    STATNIVE_INGEST_WAL_DIR="${WAL_DIR}" \
    STATNIVE_AUDIT_PATH="${AUDIT}" \
    STATNIVE_MASTER_SECRET_PATH="${MASTER_KEY}" \
        "$BIN" &
    PID=$!

    # Wait for /healthz again.
    for _ in $(seq 1 30); do
        if curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
            break
        fi
        sleep 0.2
    done

    # Poll count until quiescent (unchanged across 2s).
    PREV=-1
    CUR=0
    STABLE=0
    for _ in $(seq 1 30); do
        CUR=$(docker exec "$CH_CONTAINER" clickhouse-client --port 9000 -q \
              "SELECT count() FROM statnive.events_raw WHERE site_id = ${SITE_ID}" 2>/dev/null || echo 0)
        if [ "$CUR" = "$PREV" ]; then
            STABLE=$((STABLE + 1))
            if [ "$STABLE" -ge 2 ]; then
                break
            fi
        else
            STABLE=0
        fi
        PREV=$CUR
        sleep 1
    done

    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true

    # Assert the 0.05% loss SLO.
    MIN=$(awk "BEGIN{printf \"%d\", ${SENT} * (1 - ${LOSS_BUDGET})}")
    if [ "$CUR" -ge "$MIN" ]; then
        log "iter $iter ✅  sent=${SENT}  landed=${CUR}  (min=${MIN}; budget=${LOSS_BUDGET})"
        PASS=$((PASS + 1))
    else
        warn "iter $iter ❌  sent=${SENT}  landed=${CUR}  (min=${MIN}; budget=${LOSS_BUDGET})"
        FAIL=$((FAIL + 1))
    fi
done

log "=== summary: ${PASS} passed, ${FAIL} failed over ${ITERATIONS} iterations ==="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
