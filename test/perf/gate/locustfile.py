# Locust harness — primary load tool for the Phase 7e graduation gate
# (PLAN.md §283, doc 29 §3.1). Replicates the existing k6 load.js scenario
# (Persian paths, Iranian UAs, 1500-visitor pool, 7K EPS sustained) and emits
# the four generator_seq oracle fields per event so post-run ClickHouse
# queries can compute loss / duplicates / ordering / latency from a single
# (test_run_id, generator_node_id, test_generator_seq, send_ts) primary key.
#
# Doc 29 §6.1 oracle protocol — fields go on the wire as HTTP headers, the
# binary's /api/event handler copies them into events_raw before WAL write.
# The handler-side wire-up is a Phase 7e follow-up (TODO in load-gate-harness
# skill); this file scaffolds the generator side.
#
# Run:
#   LOCUST_TEST_RUN_ID=$(uuidgen) \
#   STATNIVE_URL=http://127.0.0.1:8080 \
#   locust -f test/perf/gate/locustfile.py --headless \
#          --users 1000 --spawn-rate 100 --run-time 5m
#
# Pre-flight (same as load.js): seed the load-test site row.

from __future__ import annotations

import json
import os
import random
import time
import uuid
from pathlib import Path
from threading import Lock

from locust import FastHttpUser, between, events, task

# Canonical load-shape data (Persian paths + Iranian UAs) lives with the Go
# generator at test/perf/generator/shape/load-shape.json — single source of
# truth shared with k6 (load.js inlines for portability) and the generator
# (//go:embed). Updating one place updates both code paths.
_SHAPE_PATH = (
    Path(__file__).resolve().parent.parent / "generator" / "shape" / "load-shape.json"
)
with _SHAPE_PATH.open(encoding="utf-8") as _f:
    _SHAPE = json.load(_f)

# 1500-visitor cookie pool (mirrors load.js exactly).
_VISITORS = [f"v-{i:08x}" for i in range(1500)]
_PERSIAN_PATHS = _SHAPE["persianPaths"]
_IRANIAN_UAS = _SHAPE["iranianUserAgents"]

_TARGET_URL = os.environ.get("STATNIVE_URL", "http://127.0.0.1:8080")
_TEST_RUN_ID = os.environ.get("LOCUST_TEST_RUN_ID") or str(uuid.uuid4())
_GENERATOR_NODE_ID = int(os.environ.get("LOCUST_GENERATOR_NODE_ID", "1"))


class _SeqCounter:
    """Per-process atomic monotonic counter for test_generator_seq.

    Locust runs one master + N worker processes. Each worker is its own
    `generator_node_id` (set via env on the worker invocation), so a single
    process-local counter is sufficient — workers never share a node-id.
    """

    def __init__(self) -> None:
        self._n = 0
        self._lock = Lock()

    def next(self) -> int:
        with self._lock:
            self._n += 1
            return self._n


_SEQ = _SeqCounter()


@events.test_start.add_listener
def _emit_run_metadata(environment, **_kwargs) -> None:
    print(
        f"[load-gate] starting run test_run_id={_TEST_RUN_ID} "
        f"generator_node_id={_GENERATOR_NODE_ID} target={_TARGET_URL}"
    )


class StatniveTracker(FastHttpUser):
    """Mirrors the k6 load.js per-iteration shape one-to-one."""

    host = _TARGET_URL
    wait_time = between(0.0, 0.0)

    @task
    def post_event(self) -> None:
        visitor = random.choice(_VISITORS)
        ua = random.choice(_IRANIAN_UAS)
        path = random.choice(_PERSIAN_PATHS)
        seq = _SEQ.next()
        send_ts_ms = int(time.time() * 1000)

        body = (
            '{"hostname":"load-test.example.com",'
            f'"pathname":"{path}",'
            '"event_type":"pageview","event_name":"pageview"}'
        )

        # Four oracle fields ride as headers (doc 29 §6.1). The binary's
        # /api/event handler must copy these into events_raw — wire-up is
        # a Phase 7e follow-up tracked in the load-gate-harness skill.
        # 192.0.2.0/24 is IETF-reserved documentation space; never routable.
        headers = {
            "User-Agent": ua,
            "Cookie": f"_statnive={visitor}",
            "X-Forwarded-For": f"192.0.2.{random.randint(1, 254)}",
            "Content-Type": "text/plain",
            "X-Statnive-Test-Run-Id": _TEST_RUN_ID,
            "X-Statnive-Generator-Node-Id": str(_GENERATOR_NODE_ID),
            "X-Statnive-Test-Generator-Seq": str(seq),
            "X-Statnive-Send-Ts": str(send_ts_ms),
        }

        with self.client.post(
            "/api/event", data=body, headers=headers, catch_response=True
        ) as resp:
            if 200 <= resp.status_code < 300:
                resp.success()
            else:
                resp.failure(f"status={resp.status_code}")
