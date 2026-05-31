#!/usr/bin/env bash
# breakpoint.sh — Phase 7e breakpoint sweep using vegeta.
#
# Steps the binary from a low EPS up through whatever it can sustain
# until either (a) latency p99 blows past 5000 ms or (b) HTTP 5xx
# rate exceeds 1%. The last successful step is the empirical breakpoint.
#
# Vegeta picked over wrk2 for two reasons: scripting-friendly (one tool
# per step, JSON reports stack), and the latency-correction model
# matches our SLO definition (request-time, not service-time).
#
# Run with:
#
#   bash test/perf/gate/breakpoint.sh [URL=http://...] [SITE_ID=1] [HOSTNAME=...]
#
# Env (mirrors locust + k6):
#   STATNIVE_URL    binary base URL (default http://127.0.0.1:8080)
#   SITE_ID         site_id in payload (default 1)
#   HOSTNAME        Host header + payload hostname (default load-test.example.com)
#   STEPS           comma-separated EPS steps to sweep (default
#                   "100,250,500,1000,2000,3500,5000,7000,10000")
#   STEP_DURATION   how long to run each step (default 30s)
#   OUT_DIR         where to write per-step *.json + *.txt (default
#                   ./build/breakpoint-<timestamp>/)
#
# Exit code:
#   0  the sweep completed and at least the lowest step met SLO
#   1  even the lowest step failed (binary not healthy — sanity bail)
#   2  preflight failed (vegeta missing, URL unreachable, etc.)

set -euo pipefail

if ! command -v vegeta >/dev/null 2>&1; then
	echo "breakpoint: vegeta not found in PATH. Install: go install github.com/tsenart/vegeta/v12@latest" >&2
	exit 2
fi

STATNIVE_URL="${STATNIVE_URL:-http://127.0.0.1:8080}"
SITE_ID="${SITE_ID:-1}"
HOSTNAME="${HOSTNAME:-load-test.example.com}"
STEPS="${STEPS:-100,250,500,1000,2000,3500,5000,7000,10000}"
STEP_DURATION="${STEP_DURATION:-30s}"

TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT_DIR="${OUT_DIR:-./build/breakpoint-${TS}}"
mkdir -p "$OUT_DIR"

TEST_RUN_ID="${TEST_RUN_ID:-$(cat /proc/sys/kernel/random/uuid 2>/dev/null || uuidgen | tr 'A-Z' 'a-z')}"

echo "breakpoint: target=$STATNIVE_URL site_id=$SITE_ID hostname=$HOSTNAME"
echo "breakpoint: steps=$STEPS step_duration=$STEP_DURATION"
echo "breakpoint: test_run_id=$TEST_RUN_ID"
echo "breakpoint: out_dir=$OUT_DIR"

# Probe reach first — if /healthz isn't green there's no point sweeping.
if ! curl -fsS "${STATNIVE_URL%/}/healthz" >/dev/null 2>&1; then
	echo "breakpoint: $STATNIVE_URL/healthz unreachable" >&2
	exit 2
fi

# Per-step payload generator. Vegeta calls this with -body @<file> so we
# pre-stage one payload file per step (each step re-uses the same body
# to keep request-shape constant; only the oracle seq differs across
# requests, and we accept that vegeta keeps it identical inside a step).
SEQ_START=1

last_ok_step=""
final_status=0

IFS=',' read -ra STEP_ARR <<< "$STEPS"

for eps in "${STEP_ARR[@]}"; do
	stage_dir="$OUT_DIR/step-${eps}eps"
	mkdir -p "$stage_dir"

	# One payload per step. test_generator_seq=$SEQ_START — within a step,
	# the oracle sees N copies of the same seq, but the duplicate scan at
	# oracle_queries.sql Q2 reports those, which we want — confirms that
	# the binary itself is exactly-once even when vegeta is exactly-N.
	body_file="$stage_dir/body.json"
	cat > "$body_file" <<EOF
{
  "hostname": "$HOSTNAME",
  "pathname": "/breakpoint",
  "title": "Breakpoint",
  "referrer": "https://www.google.com/",
  "viewport_width": 390,
  "event_type": "pageview",
  "event_name": "pageview",
  "user_id": "load-gate-breakpoint",
  "test_run_id": "$TEST_RUN_ID",
  "test_generator_seq": $SEQ_START,
  "generator_node_id": 1,
  "send_ts_ms": $(( $(date +%s) * 1000 ))
}
EOF
	SEQ_START=$(( SEQ_START + 1 ))

	target_file="$stage_dir/targets.txt"
	{
		printf 'POST %s/api/event\n' "$STATNIVE_URL"
		printf 'Content-Type: text/plain\n'
		printf 'Host: %s\n' "$HOSTNAME"
		printf '@%s\n' "$body_file"
	} > "$target_file"

	echo ""
	echo "==> step ${eps} EPS for $STEP_DURATION"

	# vegeta attack → JSON results → vegeta report renders the summary.
	# -workers grows with EPS so the producer doesn't bottleneck.
	vegeta_workers=$(( eps / 50 + 10 ))
	vegeta attack \
		-rate="${eps}/s" \
		-duration="$STEP_DURATION" \
		-workers="$vegeta_workers" \
		-targets="$target_file" \
		> "$stage_dir/results.bin"

	vegeta report -type=text  < "$stage_dir/results.bin" > "$stage_dir/report.txt"
	vegeta report -type=json  < "$stage_dir/results.bin" > "$stage_dir/report.json"
	cat "$stage_dir/report.txt"

	# Extract p99 + success rate from the JSON via grep + cut — avoid jq
	# dep so the bastion doesn't need extra packages.
	p99_ns=$(grep -o '"99th":[0-9]*' "$stage_dir/report.json" | head -1 | cut -d: -f2)
	p99_ms=$(( p99_ns / 1000000 ))
	success_pct=$(grep -o '"success":[0-9.]*' "$stage_dir/report.json" | head -1 | cut -d: -f2)

	echo "step ${eps} EPS → p99=${p99_ms}ms success=${success_pct}"

	# SLO breach = empirical breakpoint reached.
	if [ "$p99_ms" -gt 5000 ]; then
		echo "breakpoint REACHED at ${eps} EPS (p99 ${p99_ms}ms > 5000ms)"
		final_status=0
		break
	fi

	# Success rate < 99% (1% errors) is the other breakpoint condition.
	# Bash float compare via awk to avoid bc dep.
	if awk -v s="$success_pct" 'BEGIN{ exit !(s < 0.99) }'; then
		echo "breakpoint REACHED at ${eps} EPS (success ${success_pct} < 0.99)"
		final_status=0
		break
	fi

	last_ok_step="$eps"
done

if [ -z "$last_ok_step" ]; then
	echo "breakpoint: even the lowest step failed; binary is not healthy"
	final_status=1
fi

cat > "$OUT_DIR/summary.txt" <<EOF
breakpoint summary (${TS})
==============================
target:            $STATNIVE_URL
site_id:           $SITE_ID
hostname:          $HOSTNAME
steps:             $STEPS
step_duration:     $STEP_DURATION
test_run_id:       $TEST_RUN_ID
last_ok_step_eps:  ${last_ok_step:-NONE}
out_dir:           $OUT_DIR

Next: make oracle-scan TEST_RUN_ID=$TEST_RUN_ID
EOF
echo ""
echo "breakpoint: written $OUT_DIR/summary.txt"
exit "$final_status"
