#!/usr/bin/env python3
"""Seed the locally-run LL-Telemetry stack with demo runs.

Nothing writes to a telemetry database that runs on this box: the real field
scripts POST to telemetry.longlewis.local, not here. Without seeding, the
console renders perfectly and reports zero events, which looks like a broken
integration rather than an empty one.

This posts through the real ingest path (POST /api/v1/runs) rather than
inserting SQL directly, so it exercises server-side site resolution, field
clipping and replay de-duplication exactly as a field script would.

Stdlib only, matching the other collectors in this directory.

  docker compose -f docker-compose.yml -f docker-compose.telemetry.yml \
    --profile seed run --rm lltel-seed
"""

import json
import os
import random
import sys
import time
import urllib.error
import urllib.request
import uuid
from datetime import datetime, timedelta, timezone

def load_dotenv():
    here = os.path.dirname(os.path.abspath(__file__))
    for path in (os.path.join(here, ".env"), os.path.join(here, "..", ".env")):
        if os.path.isfile(path):
            with open(path, "r", encoding="utf-8") as fh:
                for raw in fh:
                    line = raw.strip()
                    if line and not line.startswith("#") and "=" in line:
                        k, v = line.split("=", 1)
                        os.environ.setdefault(k.strip(), v.strip())
            return


load_dotenv()

BASE = os.environ.get("LLTEL_BASE", "http://lltel-api:8080").rstrip("/")
TOKEN = os.environ.get("LL_INGEST_TOKEN", "")
# The upstream rate-limits ingest to 300 per rolling 300s per source IP, and
# every run here arrives from one container. The default stays under it so a
# plain run never has to wait; larger values pace themselves on Retry-After.
RUN_COUNT = int(os.environ.get("SEED_RUNS", "280"))
SPAN_DAYS = int(os.environ.get("SEED_DAYS", "3"))

# Subnets that SITE_MAP in telemetry/api/main.py resolves, plus one that it
# deliberately does not, so the console's "unmapped subnets" panel has content.
SUBNETS = [
    ("10.15.100.", "Ivory Tower"),
    ("10.15.105.", "Ivory Tower"),
    ("10.6.81.", "Muscle Shoals"),
    ("10.10.1.", "Bramlett / Decatur"),
    ("192.168.44.", None),
]

SCRIPTS = [
    ("LL-DomainTrust", "1.4.0"),
    ("LL-HarvestDrivers", "1.1.2"),
    ("LL-RenameJoin", "2.0.1"),
]

# Weighted so the estate reads mostly healthy with a believable tail of
# failures; the severity ribbon is dull if everything is one colour.
RESULTS = (
    ["HEALTHY"] * 46
    + ["OK"] * 18
    + ["REPAIRED"] * 10
    + ["NOT_JOINED"] * 7
    + ["PARTIAL"] * 5
    + ["CANCELLED"] * 3
    + ["FAILED"] * 6
    + ["ERROR"] * 3
    + ["NO_DC"] * 2
)

MODELS = [
    ("Dell Inc.", "OptiPlex 7010", "Windows 10 Pro", "19045"),
    ("Dell Inc.", "Latitude 5540", "Windows 11 Pro", "22631"),
    ("HP", "EliteDesk 800 G6", "Windows 10 Pro", "19045"),
    ("Lenovo", "ThinkCentre M70q", "Windows 11 Pro", "22631"),
]

TECHS = ["cwest", "jhicks", "rmartin", "dalvarez"]

LOG_LINES = {
    "HEALTHY": [
        "Checking secure channel to domain LONGLEWIS",
        "Test-ComputerSecureChannel returned True",
        "Domain trust intact, no action required",
    ],
    "OK": [
        "Enumerating driver store",
        "Harvested 14 driver packages",
        "Export complete",
    ],
    "REPAIRED": [
        "Test-ComputerSecureChannel returned False",
        "WARNING: secure channel broken, attempting repair",
        "Reset-ComputerMachinePassword succeeded",
        "Re-test returned True",
    ],
    "NOT_JOINED": [
        "Querying computer domain membership",
        "WARNING: workstation is not joined to a domain",
        "Skipping trust verification",
    ],
    "PARTIAL": [
        "Harvested 9 of 14 driver packages",
        "WARNING: 5 packages skipped, source path unavailable",
    ],
    "CANCELLED": [
        "Operator interrupt received",
        "WARNING: run cancelled before completion",
    ],
    "FAILED": [
        "Test-ComputerSecureChannel returned False",
        "Reset-ComputerMachinePassword failed",
        "ERROR: access denied resetting machine password",
    ],
    "ERROR": [
        "Unhandled exception in script body",
        "ERROR: The RPC server is unavailable. (0x800706BA)",
    ],
    "NO_DC": [
        "Locating domain controller for LONGLEWIS",
        "ERROR: no logon servers are available to service the request",
        "CRITICAL: cannot reach any domain controller",
    ],
}

EXIT_CODES = {
    "HEALTHY": 0, "OK": 0, "REPAIRED": 0,
    "NOT_JOINED": 2, "PARTIAL": 3, "CANCELLED": 1223,
    "FAILED": 1, "ERROR": 1, "NO_DC": 1355,
}


def post(path, payload, token):
    body = json.dumps(payload).encode("utf-8")
    request = urllib.request.Request(BASE + path, data=body, method="POST")
    request.add_header("Content-Type", "application/json")
    if token:
        request.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(request, timeout=10) as response:
        return response.status, response.read()


def wait_for_api(attempts=60):
    """The API container can be up before MariaDB finishes initialising."""
    for attempt in range(attempts):
        try:
            with urllib.request.urlopen(BASE + "/api/v1/health", timeout=5) as response:
                if response.status == 200:
                    return True
        except (urllib.error.URLError, urllib.error.HTTPError, OSError):
            pass
        if attempt == 0:
            print("waiting for the telemetry API to become healthy", flush=True)
        time.sleep(2)
    return False


def build_run(now):
    subnet, _site = random.choice(SUBNETS)
    script, script_version = random.choice(SCRIPTS)
    result = random.choice(RESULTS)
    manufacturer, model, os_name, os_build = random.choice(MODELS)
    host_index = random.randint(1, 60)
    duration = random.randint(3, 240)
    finished = now - timedelta(seconds=random.randint(0, SPAN_DAYS * 86400))
    started = finished - timedelta(seconds=duration)
    lines = LOG_LINES[result]
    stamped = "\n".join(
        "[{}] {}".format((started + timedelta(seconds=i * 2)).strftime("%H:%M:%S"), line)
        for i, line in enumerate(lines)
    )
    return {
        "schema": 1,
        "run_id": str(uuid.uuid4()),
        "script": script,
        "script_version": script_version,
        "result": result,
        "exit_code": EXIT_CODES[result],
        "started_utc": started.strftime("%Y-%m-%dT%H:%M:%S") + "Z",
        "finished_utc": finished.strftime("%Y-%m-%dT%H:%M:%S") + "Z",
        "duration_sec": duration,
        "hostname": "LL-WS-{:03d}".format(host_index),
        "serial": "5CG{:04d}{}".format(random.randint(0, 9999), random.choice("ABCDEFGH")),
        "manufacturer": manufacturer,
        "model": model,
        "os": os_name,
        "os_build": os_build,
        "ad_domain": "longlewis.net" if result != "NOT_JOINED" else None,
        "ip": subnet + str(random.randint(20, 240)),
        "mac": "00:1A:2B:{:02X}:{:02X}:{:02X}".format(
            random.randint(0, 255), random.randint(0, 255), random.randint(0, 255)
        ),
        "tech": random.choice(TECHS),
        "details": {"seeded": True, "reboot_required": result == "REPAIRED"},
        "log": stamped,
        "queued_offline": random.random() < 0.08,
        "tls_bypassed": random.random() < 0.12,
    }


def main():
    if not wait_for_api():
        print("telemetry API never became healthy; nothing seeded", file=sys.stderr)
        return 1
    now = datetime.now(timezone.utc).replace(tzinfo=None)
    accepted = 0
    failed = 0
    throttled = False
    for _ in range(RUN_COUNT):
        payload = build_run(now)
        # The API rate-limits ingest to 300 per 300s per source IP. Every run
        # here arrives from one container, so a large seed WILL hit it; wait the
        # window out rather than silently dropping runs.
        for attempt in range(4):
            try:
                status, _body = post("/api/v1/runs", payload, TOKEN)
                if 200 <= status < 300:
                    accepted += 1
                else:
                    failed += 1
                break
            except urllib.error.HTTPError as e:
                if e.code == 429 and attempt < 3:
                    # The window is a rolling 300s, so a fixed short sleep is
                    # not enough. The upstream tells us how long to wait.
                    try:
                        wait = int(e.headers.get("Retry-After", "60"))
                    except (TypeError, ValueError):
                        wait = 60
                    wait = max(1, min(wait, 305))
                    if not throttled:
                        print(
                            "ingest rate limit reached, waiting {}s for the window".format(wait),
                            flush=True,
                        )
                        throttled = True
                    time.sleep(wait)
                    continue
                failed += 1
                if failed == 1:
                    print("ingest rejected a run: {} {}".format(e.code, e.reason), file=sys.stderr)
                break
            except (urllib.error.URLError, OSError) as e:
                failed += 1
                if failed == 1:
                    print("ingest unreachable: {}".format(e), file=sys.stderr)
                break
    print("seeded {} runs ({} failed) into {}".format(accepted, failed, BASE), flush=True)
    return 0 if accepted else 1


if __name__ == "__main__":
    sys.exit(main())
