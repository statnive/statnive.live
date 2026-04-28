#!/usr/bin/env python3
# locust-master.py — orchestrator wrapper around locust + post-run oracle scan.
#
# 1. Mints a fresh test_run_id (UUID) for the run unless one is supplied.
# 2. Probes the binary's /healthz before kicking off (fail-fast on dead box).
# 3. Execs locust headless with a published worker manifest if --distributed.
# 4. On exit (clean or signal), invokes `make oracle-scan RUN_ID=<id>` so
#    every gate run is paired with the four canonical CH queries from
#    doc 29 §6.2 (loss / duplicates / ordering / latency).
#
# Phase 7e usage (single-node 2-node test bed dry-run):
#   ./test/perf/gate/locust-master.py \
#     --target http://127.0.0.1:8080 \
#     --users 1000 --spawn-rate 100 --run-time 5m
#
# Phase 10 usage (distributed Asiatech 1 master + 3 workers):
#   ./test/perf/gate/locust-master.py \
#     --target https://collector-staging.statnive.live \
#     --distributed --workers test/perf/gate/worker-manifest.yaml \
#     --users 50000 --spawn-rate 1000 --run-time 72h

from __future__ import annotations

import argparse
import os
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

_HERE = Path(__file__).resolve().parent
_REPO_ROOT = _HERE.parent.parent.parent


def _probe_healthz(target: str, timeout_s: float = 5.0) -> None:
    try:
        with urllib.request.urlopen(f"{target}/healthz", timeout=timeout_s) as resp:
            if resp.status != 200:
                raise SystemExit(f"healthz: status {resp.status}")
    except (urllib.error.URLError, TimeoutError) as e:
        raise SystemExit(f"healthz unreachable: {e}") from e


def _run_oracle_scan(run_id: str) -> int:
    print(f"[load-gate] running oracle scan for run_id={run_id}")
    return subprocess.call(
        ["make", "oracle-scan", f"RUN_ID={run_id}"],
        cwd=str(_REPO_ROOT),
    )


def main(argv: list[str]) -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--target", default=os.environ.get("STATNIVE_URL", "http://127.0.0.1:8080"))
    p.add_argument("--run-id", default=os.environ.get("LOCUST_TEST_RUN_ID") or str(uuid.uuid4()))
    p.add_argument("--users", type=int, default=1000)
    p.add_argument("--spawn-rate", type=int, default=100)
    p.add_argument("--run-time", default="5m")
    p.add_argument("--distributed", action="store_true")
    p.add_argument("--workers", default=str(_HERE / "worker-manifest.yaml"))
    p.add_argument("--skip-oracle-scan", action="store_true")
    args = p.parse_args(argv)

    print(f"[load-gate] master starting test_run_id={args.run_id}")
    _probe_healthz(args.target)

    env = os.environ.copy()
    env["STATNIVE_URL"] = args.target
    env["LOCUST_TEST_RUN_ID"] = args.run_id

    cmd = [
        "locust",
        "-f", str(_HERE / "locustfile.py"),
        "--headless",
        "--users", str(args.users),
        "--spawn-rate", str(args.spawn_rate),
        "--run-time", args.run_time,
        "--host", args.target,
    ]
    if args.distributed:
        # Phase 10: orchestrator launches workers via Ansible against
        # worker-manifest.yaml; this branch keeps the master-only path
        # for the 2-node dry-run bed.
        cmd.append("--master")

    started = time.time()
    proc = subprocess.Popen(cmd, env=env)

    def _forward(signum, _frame):
        proc.send_signal(signum)

    signal.signal(signal.SIGINT, _forward)
    signal.signal(signal.SIGTERM, _forward)

    rc = proc.wait()
    elapsed = time.time() - started
    print(f"[load-gate] locust exited rc={rc} after {elapsed:.0f}s")

    if not args.skip_oracle_scan:
        scan_rc = _run_oracle_scan(args.run_id)
        if scan_rc != 0:
            print(f"[load-gate] oracle-scan FAILED rc={scan_rc}")
            return scan_rc

    return rc


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
