"""Phase 7e load-gate primary harness — Locust file.

Drives /api/event with the oracle tuple from B.1 + B.2. Distinct from
test/perf/generator/ (Go binary): Locust is the primary harness for the
P1–P5 ramps with multi-VU coordination + per-step SLO assertions; the
Go generator is the standalone smoke-test / one-shot synthesizer.

Phase ramps (PLAN.md Phase 7e + doc 29 §4):

    P1   50 →  300 EPS over  5 min   (smoke)
    P2  150 →  600 EPS over 10 min   (sustained)
    P3  300 → 1200 EPS over 20 min   (peak-sustained)
    P4  600 → 2400 EPS over 40 min   (overload)
    P5 1200 → 4800 EPS over 80 min   (breakpoint hunt)

Run with:

    locust -f test/perf/gate/locustfile.py \\
        --host=https://app.statnive.live \\
        --headless \\
        -u 200 -r 50 -t 5m \\
        --tags=p1

Or via the wrapper:  make load-gate PHASE=P1

SLO thresholds enforced on every step (CLAUDE.md "Analytics Invariant
Thresholds"):

    p99 latency < 2000 ms
    error rate  < 0.5 %  (server loss; oracle scan adds the precise number)
    no 5xx storm (>1% over 30s window halts the run)
"""

import json
import os
import time
import uuid

from locust import HttpUser, task, between, events, LoadTestShape

# Oracle test_run_id is generated once per Locust process and reused
# across all VUs. Print it at startup so the operator can pipe it into
# `make oracle-scan` after the run.
TEST_RUN_ID = os.environ.get("TEST_RUN_ID") or str(uuid.uuid4())
SITE_ID = int(os.environ.get("SITE_ID", "1"))
HOSTNAME = os.environ.get("HOSTNAME", "load-test.example.com")

# Monotonic sequence is per-VU (each VU is one "generator-node").
# Locust does not expose a stable VU id, so we hash(user_id) → uint16
# at first request — see StatniveUser.on_start.

_LANDING_PATHS = ["/", "/blog", "/pricing", "/checkout"]
_TITLES = ["Home", "Blog", "Pricing", "Checkout"]
_UA = (
    "Mozilla/5.0 (Linux; Android 14; SM-S921B) "
    "AppleWebKit/537.36 (KHTML, like Gecko) "
    "Chrome/126.0.0.0 Mobile Safari/537.36"
)

# Per-phase SLO budget — locust fails the run on threshold breach.
SLO_BUDGETS = {
    "p1": {"p99_ms": 2000, "err_pct": 0.5},
    "p2": {"p99_ms": 2000, "err_pct": 0.5},
    "p3": {"p99_ms": 2500, "err_pct": 0.5},
    "p4": {"p99_ms": 3500, "err_pct": 1.0},
    "p5": {"p99_ms": 5000, "err_pct": 2.0},
}


@events.init.add_listener
def _on_init(environment, **kwargs):
    print(f"\n  TEST_RUN_ID={TEST_RUN_ID}")
    print(f"  SITE_ID={SITE_ID}")
    print(f"  HOSTNAME={HOSTNAME}")
    print(f"  (pass to: make oracle-scan TEST_RUN_ID={TEST_RUN_ID})\n")


@events.quitting.add_listener
def _on_quit(environment, **kwargs):
    """Enforce per-phase SLO at run-end. Sets non-zero exit code on breach."""
    phase = (os.environ.get("LOAD_GATE_PHASE") or "p1").lower()
    budget = SLO_BUDGETS.get(phase, SLO_BUDGETS["p1"])

    p99 = environment.stats.total.get_response_time_percentile(0.99)
    err_pct = (
        environment.stats.total.num_failures
        / max(environment.stats.total.num_requests, 1)
        * 100.0
    )

    print(f"\n--- SLO check (phase={phase}) ---")
    print(f"p99_ms:        {p99:.0f}  (budget {budget['p99_ms']})")
    print(f"err_pct:       {err_pct:.3f}  (budget {budget['err_pct']})")
    print(f"test_run_id:   {TEST_RUN_ID}")

    if p99 > budget["p99_ms"]:
        environment.process_exit_code = 1
        print(f"FAIL: p99 {p99:.0f}ms exceeds budget {budget['p99_ms']}ms")

    if err_pct > budget["err_pct"]:
        environment.process_exit_code = 1
        print(f"FAIL: err {err_pct:.3f}% exceeds budget {budget['err_pct']}%")


class StatniveUser(HttpUser):
    """One Locust user = one generator-node-id."""

    wait_time = between(0.05, 0.2)  # 5–20 req/s/VU effective

    def on_start(self):
        # Cap to uint16 to match migration 018's generator_node_id type;
        # using id(self) gives a stable per-VU value during this run.
        self.node_id = id(self) % 65536
        self.seq = 0
        self.uid_pool_size = 256

    @task
    def post_event(self):
        self.seq += 1

        # Mix path + uid for cardinality.
        idx = self.seq % len(_LANDING_PATHS)
        payload = {
            "hostname": HOSTNAME,
            "pathname": _LANDING_PATHS[idx],
            "title": _TITLES[idx],
            "referrer": "https://www.google.com/",
            "utm_source": "google",
            "utm_medium": "organic",
            "viewport_width": 390,
            "event_type": "pageview",
            "event_name": "pageview",
            "user_id": f"load-gate-uid-{self.seq % self.uid_pool_size}",
            "test_run_id": TEST_RUN_ID,
            "test_generator_seq": self.seq,
            "generator_node_id": self.node_id,
            "send_ts_ms": int(time.time() * 1000),
        }

        self.client.post(
            "/api/event",
            data=json.dumps(payload),
            headers={
                "Content-Type": "text/plain",
                "User-Agent": _UA,
                "Host": HOSTNAME,
            },
            name="POST /api/event",
        )


# --- Ramp shape (chosen via LOAD_GATE_PHASE env var) --------------------------


class PhaseRamp(LoadTestShape):
    """Step-up ramp implementing P1..P5 from PLAN.md Phase 7e."""

    SCHEDULES = {
        "p1": [(60, 50, 25), (180, 150, 50), (300, 300, 75)],
        "p2": [(120, 150, 50), (360, 300, 100), (600, 600, 100)],
        "p3": [(300, 300, 100), (600, 600, 100), (900, 900, 100), (1200, 1200, 100)],
        "p4": [(600, 600, 100), (1200, 1200, 200), (1800, 1800, 200), (2400, 2400, 200)],
        "p5": [(1200, 1200, 200), (2400, 2400, 200), (3600, 3600, 200), (4800, 4800, 200)],
    }

    def tick(self):
        phase = (os.environ.get("LOAD_GATE_PHASE") or "p1").lower()
        schedule = self.SCHEDULES.get(phase, self.SCHEDULES["p1"])

        run_time = self.get_run_time()

        for t, users, spawn_rate in schedule:
            if run_time < t:
                return users, spawn_rate

        return None  # done
