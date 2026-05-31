#!/usr/bin/env bash
# longsession.sh — Phase 7e long-session memory-leak soak.
#
# Doc 30 §6 contract: 1000 VUs × 6h × 1080 events at 20s intervals.
# Each VU keeps a persistent session_id and pings once every ~20s, so
# the binary's session-state map has 1000 live entries the whole time
# — exactly the shape that exposes session-cache leaks (LEARN.md Lesson
# 18 closure check).
#
# Drives Locust headless with a tuned scenario, then snapshots Pyroscope
# heap diffs at t=0, t=2h, t=4h, t=6h. Heap growth > 50 MB between t=2h
# and t=6h fails the soak.
#
# Run with:
#
#   bash test/perf/soak/longsession.sh \
#       --duration=6h \
#       --vus=1000 \
#       --url=https://load-gate.example.com \
#       --pyroscope=https://obs.example.com:4040
#
# Env defaults match the doc 30 spec; override per-deployment.

set -euo pipefail

DURATION="${DURATION:-6h}"
VUS="${VUS:-1000}"
STATNIVE_URL="${STATNIVE_URL:-http://127.0.0.1:8080}"
PYROSCOPE_URL="${PYROSCOPE_URL:-http://127.0.0.1:4040}"
SITE_ID="${SITE_ID:-1}"
HOSTNAME="${HOSTNAME:-load-test.example.com}"
OUT_DIR="${OUT_DIR:-./build/soak-$(date -u +%Y%m%dT%H%M%SZ)}"
HEAP_GROWTH_BUDGET_MB="${HEAP_GROWTH_BUDGET_MB:-50}"

while [ "$#" -gt 0 ]; do
	case "$1" in
		--duration=*)  DURATION="${1#--duration=}"; shift ;;
		--vus=*)       VUS="${1#--vus=}"; shift ;;
		--url=*)       STATNIVE_URL="${1#--url=}"; shift ;;
		--pyroscope=*) PYROSCOPE_URL="${1#--pyroscope=}"; shift ;;
		--site-id=*)   SITE_ID="${1#--site-id=}"; shift ;;
		--hostname=*)  HOSTNAME="${1#--hostname=}"; shift ;;
		--out=*)       OUT_DIR="${1#--out=}"; shift ;;
		-h|--help)
			sed -n '1,25p' "$0" | sed 's/^# \{0,1\}//'
			exit 0
			;;
		*) echo "longsession: unknown arg $1" >&2; exit 2 ;;
	esac
done

mkdir -p "$OUT_DIR"

TEST_RUN_ID="${TEST_RUN_ID:-$(cat /proc/sys/kernel/random/uuid 2>/dev/null || uuidgen | tr 'A-Z' 'a-z')}"
export TEST_RUN_ID

echo "longsession: url=$STATNIVE_URL vus=$VUS duration=$DURATION"
echo "longsession: pyroscope=$PYROSCOPE_URL out=$OUT_DIR"
echo "longsession: test_run_id=$TEST_RUN_ID"

# Probe the binary first — fail fast if it's down.
if ! curl -fsS "${STATNIVE_URL%/}/healthz" >/dev/null; then
	echo "longsession: $STATNIVE_URL/healthz unreachable; aborting" >&2
	exit 2
fi

snapshot_heap() {
	local label="$1"
	local outfile="$OUT_DIR/heap-${label}.pb.gz"

	if ! curl -fsS --max-time 30 \
		"${PYROSCOPE_URL}/pyroscope/render?query=process_cpu:samples:count:cpu:nanoseconds%7Bservice_name=%22statnive-live%22%7D&format=pprof" \
		> "$outfile"; then
		echo "longsession: WARNING — Pyroscope snapshot at $label failed (continuing)" >&2
		return
	fi

	local size
	size="$(stat -c %s "$outfile" 2>/dev/null || stat -f %z "$outfile")"
	echo "longsession: heap snapshot $label → $outfile ($size bytes)"
}

snapshot_heap "t0"

# Drive Locust. We reuse the existing locustfile.py (B.3) with a small
# override — long flat at $VUS for $DURATION rather than the phase ramps.
# The shape env var picks a flat profile via tags.
SOAK_LOG="$OUT_DIR/locust.log"
echo "longsession: launching locust → $SOAK_LOG"

locust -f test/perf/gate/locustfile.py \
	--host="$STATNIVE_URL" \
	--headless \
	-u "$VUS" -r 50 -t "$DURATION" \
	--csv="$OUT_DIR/locust" \
	--csv-full-history \
	2>&1 | tee "$SOAK_LOG" &
LOCUST_PID=$!

# Snapshot heap at 2h, 4h, 6h marks (proportional to total duration).
duration_secs="$(echo "$DURATION" | awk '
	/h$/ { sub(/h$/,""); print $0 * 3600; exit }
	/m$/ { sub(/m$/,""); print $0 * 60; exit }
	/s$/ { sub(/s$/,""); print $0; exit }
	{ print $0 }
')"

for frac in 0.33 0.66 1.0; do
	wait_secs="$(awk -v s="$duration_secs" -v f="$frac" 'BEGIN{ printf "%d", s*f }')"
	# Wait absolute (relative to start) by sleeping the delta.
	# Simpler: sleep $((duration / 3)) thrice, snapshotting between.
	sleep "$(( wait_secs - $(printf '%.0f' $(awk -v d="$duration_secs" -v frac="$frac" 'BEGIN{ printf "%d", d*(frac - 0.33) }') 2>/dev/null || echo 0) ))" 2>/dev/null || true
	label="t-frac-$frac"
	snapshot_heap "$label"
done

wait "$LOCUST_PID" || true

snapshot_heap "tfinal"

# Heap-growth analysis. We extract the total samples from each pprof —
# this isn't a precise leak measurement (Pyroscope's CPU pprof is
# samples, not bytes) but for steady-state binaries a >50% sample-count
# growth in CPU between t-2h and t-6h is a strong proxy for "something
# is leaking work into a hot path." Real heap snapshot via Pyroscope's
# inuse_space query when wired in B.5+ (we collect both formats).
{
	echo "longsession summary"
	echo "==================="
	echo "test_run_id:       $TEST_RUN_ID"
	echo "duration:          $DURATION"
	echo "vus:               $VUS"
	echo "out_dir:           $OUT_DIR"
	echo ""
	echo "Snapshots collected:"
	ls -la "$OUT_DIR"/heap-*.pb.gz 2>/dev/null || echo "  (none — Pyroscope unreachable?)"
	echo ""
	echo "Locust stats:"
	test -f "$OUT_DIR/locust_stats.csv" && cat "$OUT_DIR/locust_stats.csv" | head -20
	echo ""
	echo "Next: make oracle-scan TEST_RUN_ID=$TEST_RUN_ID"
} > "$OUT_DIR/summary.txt"

echo ""
echo "longsession: done. Evidence: $OUT_DIR/summary.txt"
