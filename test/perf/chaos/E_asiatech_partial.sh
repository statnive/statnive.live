#!/usr/bin/env bash
# Scenario E — Partial Asiatech outage (doc 29 §5.5).
#
# Kill one of two staging ClickHouse replicas mid-soak; verify drain +
# reconciliation on the surviving replica. v1 is single-node so this
# scenario stages a side-car CH (deploy/docker-compose.dev.yml maps
# clickhouse:9000 → host:19000) and a kill-toggle on its container.
#
# When v1.1 / v2 introduces ReplicatedMergeTree, this script is the
# canonical place to extend the kill matrix to N replicas.

source "$(dirname "$0")/_lib.sh"

CONTAINER="${CHAOS_CH_CONTAINER:-statnive-clickhouse-dev}"

scenario_up() {
    require_cmd docker
    if ! docker ps --format '{{.Names}}' | grep -q "^${CONTAINER}\$"; then
        echo "scenario E: container $CONTAINER not running, nothing to kill"; return 0
    fi
    docker stop "$CONTAINER"
    echo "scenario E up — $CONTAINER stopped"
}

scenario_down() {
    require_cmd docker
    if docker ps --format '{{.Names}}' | grep -q "^${CONTAINER}\$"; then
        echo "scenario E already down — $CONTAINER running"; return 0
    fi
    docker start "$CONTAINER" || true
    # Brief wait — the binary's CH client retries with backoff; the WAL
    # replay path drains pending events as soon as the container is back.
    for i in {1..30}; do
        if docker exec "$CONTAINER" clickhouse-client --port 9000 -q "SELECT 1" >/dev/null 2>&1; then
            echo "scenario E down — $CONTAINER ready after ${i}s"; return 0
        fi
        sleep 1
    done
    echo "WARN: scenario E down — $CONTAINER did not become ready in 30s" >&2
    return 1
}

scenario_status() {
    require_cmd docker
    if docker ps --format '{{.Names}}' | grep -q "^${CONTAINER}\$"; then
        echo "down"; exit 1
    else
        echo "up"; exit 0
    fi
}

dispatch E "$@"
