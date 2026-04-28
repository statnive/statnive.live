# Shared helpers for the seven Phase 7e chaos scripts (doc 29 §5.1–§5.6 +
# doc 30 §3 scenario G). Each scenario script sources this file.
#
# Convention — every chaos script implements three subcommands:
#   up      apply the disruption (idempotent — re-running is a no-op)
#   down    remove the disruption (idempotent — re-running is a no-op)
#   status  print the current state (0 = up, 1 = down, 2 = unknown)
#
# The load-gate orchestrator captures pre/post oracle-SQL output to
# test/perf/chaos/runs/<scenario>-<run_id>.json so each chaos pass leaves
# an auditable trail. Phase 7e dry-runs each script on the 2-node bed;
# Phase 10 wires them into `make chaos-matrix`.

set -euo pipefail

CHAOS_LIB_VERSION=1

require_root() {
    if [[ $EUID -ne 0 ]]; then
        echo "FAIL: $0 requires root (tc/iptables/chronyd)" >&2
        exit 1
    fi
}

require_cmd() {
    for c in "$@"; do
        if ! command -v "$c" >/dev/null 2>&1; then
            echo "FAIL: missing command: $c" >&2
            exit 2
        fi
    done
}

# capture_oracle <scenario> <phase>  — phase ∈ {pre,post}.
# Reads RUN_ID from env; writes JSON to test/perf/chaos/runs/.
capture_oracle() {
    local scenario="$1" phase="$2"
    local run_id="${RUN_ID:-unknown}"
    local repo_root
    repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
    local out_dir="$repo_root/test/perf/chaos/runs"
    mkdir -p "$out_dir"
    local out="$out_dir/${scenario}-${run_id}-${phase}.json"
    if make -C "$repo_root" oracle-scan RUN_ID="$run_id" --silent >"$out" 2>&1; then
        echo "captured oracle scan: $out"
    else
        echo "WARN: oracle-scan failed (continuing); see $out" >&2
    fi
}

dispatch() {
    local scenario="$1"
    shift
    local cmd="${1:-status}"

    # Validate the scenario script's contract (load-gate-harness invariant
    # `chaos-script-up-down-status`). Without this, a typo in scenario_up
    # silently no-ops on `make chaos-matrix`.
    for fn in scenario_up scenario_down scenario_status; do
        if ! declare -F "$fn" >/dev/null; then
            echo "FAIL: $0 must define $fn (chaos-script-up-down-status)" >&2
            exit 2
        fi
    done

    case "$cmd" in
        up)     scenario_up ;;
        down)   scenario_down ;;
        status) scenario_status ;;
        run-with-oracle)
            capture_oracle "$scenario" pre
            scenario_up
            sleep "${CHAOS_HOLD_SEC:-300}"
            scenario_down
            capture_oracle "$scenario" post
            ;;
        *) echo "usage: $0 {up|down|status|run-with-oracle}"; exit 64 ;;
    esac
}
