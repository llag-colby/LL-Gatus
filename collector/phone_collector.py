#!/usr/bin/env python3
"""
phone_collector.py — push-based phone monitor for LL-Gatus.

Gatus never calls the PBX. This script (run on the docker host by systemd, or
as a one-shot) does, then pushes results into Gatus:

  1. Reachability : GET https://<pbx>/api/v1/PBX/version/                 -> 200
  2. Registrations: GET https://<pbx>/api/v1/PBX/Users/Sip/Registrations
     Shape: {"result": {"<ext>": {"registrations": [ {online, contact,
             received, useragent}, ... ]}}}
     Desk phones are the registrations whose useragent contains "ForcePro"
     (Wildix desk phones). "x-bees Web ..." are softphones and are ignored.
       - IP    : parsed from `contact`  (sip:<ext>@<ip>:<port>;...)
       - MAC   : last token of the useragent (12 hex) -> AA:BB:CC:DD:EE:FF
       - model : useragent model token (e.g. ForceProWPR5)
  3. Names      : GET https://<pbx>/api/v1/PBX/Colleagues/  (ext -> name)
  4. Health     : self-baseline. Healthy when online desk phones >= 90% of the
                  high-water baseline (stored in collector/.phones_state.json).
  5. Push       : full inventory -> POST /api/v1/phones/<key>
                  pass/fail       -> POST /api/v1/endpoints/<key>/external
                  both Authorization: Bearer <PHONES_PUSH_TOKEN>.

Run modes:
  python3 phone_collector.py            # one sweep, then exit
  LOOP=1 python3 phone_collector.py     # daemon: sweep every 15-45s (jittered)

Env: PHONES_PUSH_TOKEN, PHONES_IVORY_TOWER_TOKEN (loaded from a sibling .env if
present). Optional: GATUS_PUSH_BASE (default http://localhost:8080),
PHONES_STATE_FILE, VERIFY_TLS=0, LOOP=1, SWEEP_MIN/SWEEP_MAX (seconds).
"""

import json
import os
import random
import re
import ssl
import sys
import time
import urllib.error
import urllib.request

LOCATIONS = [
    {
        "key": "phones_ivory-tower",       # slug(group=Phones)_slug(name=Ivory Tower)
        "label": "Ivory Tower",
        "pbx": "https://longlewiscorporate.wildixin.com",
        "token_env": "PHONES_IVORY_TOWER_TOKEN",
    },
    {
        "key": "phones_alabaster",         # slug(group=Phones)_slug(name=Alabaster)
        "label": "Alabaster",
        "pbx": "https://longlewisab.wildixin.com",
        "token_env": "PHONES_ALABASTER_TOKEN",
    },
    {
        "key": "phones_bessemer",          # slug(group=Phones)_slug(name=Bessemer)
        "label": "Bessemer",
        "pbx": "https://longlewisbe.wildixin.com",
        "token_env": "PHONES_BESSEMER_TOKEN",
    },
    {
        "key": "phones_cullman",           # slug(group=Phones)_slug(name=Cullman)
        "label": "Cullman",
        "pbx": "https://longlewiscl.wildixin.com",
        "token_env": "PHONES_CULLMAN_TOKEN",
    },
    {
        "key": "phones_florence",          # slug(group=Phones)_slug(name=Florence)
        "label": "Florence",
        "pbx": "https://longlewisfl.wildixin.com",
        "token_env": "PHONES_FLORENCE_TOKEN",
    },
    {
        "key": "phones_hoover",            # slug(group=Phones)_slug(name=Hoover)
        "label": "Hoover",
        "pbx": "https://longlewishv.wildixin.com",
        "token_env": "PHONES_HOOVER_TOKEN",
    },
    {
        "key": "phones_muscle-shoals",     # slug(group=Phones)_slug(name=Muscle Shoals)
        "label": "Muscle Shoals",
        "pbx": "https://longlewisms.wildixin.com",
        "token_env": "PHONES_MUSCLE_SHOALS_TOKEN",
    },
    {
        "key": "phones_prattville",        # slug(group=Phones)_slug(name=Prattville)
        "label": "Prattville",
        "pbx": "https://longlewispr.wildixin.com",
        "token_env": "PHONES_PRATTVILLE_TOKEN",
    },
    {
        "key": "phones_tuscumbia",         # slug(group=Phones)_slug(name=Tuscumbia)
        "label": "Tuscumbia",
        "pbx": "https://longlewistu.wildixin.com",
        "token_env": "PHONES_TUSCUMBIA_TOKEN",
    },
]

# Health tolerance: >= this many MONITORED phones offline -> degraded.
# (Excluded phones never count.) Down = PBX unreachable or every monitored
# phone offline. Tunable via env.
DEGRADED_AT = int(os.environ.get("PHONES_DEGRADED_AT", "2"))
HTTP_TIMEOUT = 12
CONTACT_IP_RE = re.compile(r"@([0-9]{1,3}(?:\.[0-9]{1,3}){3}):")
MAC_RE = re.compile(r"^[0-9a-fA-F]{12}$")


# --------------------------------------------------------------------------- #
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


def _ctx():
    if os.environ.get("VERIFY_TLS", "1") == "0":
        c = ssl.create_default_context()
        c.check_hostname = False
        c.verify_mode = ssl.CERT_NONE
        return c
    return None


def http(url, token=None, method="GET", body=None):
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    data = body.encode() if isinstance(body, str) else body
    req = urllib.request.Request(url, headers=headers, method=method, data=data)
    with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT, context=_ctx()) as resp:
        return resp.getcode(), resp.read()


# --------------------------------------------------------------------------- #
def format_mac(token):
    if token and MAC_RE.match(token):
        t = token.lower()
        return ":".join(t[i:i + 2] for i in range(0, 12, 2)).upper()
    return ""


def parse_useragent(ua):
    """'Wildix ForceProWPR5 2.12.16.106 9c7514513042' -> (model, firmware, mac)."""
    parts = ua.split()
    model = parts[1].replace("ForcePro", "") or parts[1] if len(parts) >= 2 else ""
    firmware = parts[2] if len(parts) >= 3 else ""
    mac = format_mac(parts[-1]) if parts else ""
    return model, firmware, mac


def fetch_directory(pbx, token):
    """ext -> {name, did, department, email}, best effort."""
    info = {}
    try:
        code, body = http(f"{pbx}/api/v1/PBX/Colleagues/", token)
        if code == 200:
            for rec in (json.loads(body).get("result", {}) or {}).get("records", []):
                ext = str(rec.get("extension") or rec.get("login") or "")
                if ext:
                    info[ext] = {
                        "name": rec.get("name") or "",
                        "did": rec.get("officePhone") or "",
                        "department": rec.get("groupName") or "",
                        "email": rec.get("email") or "",
                    }
    except (urllib.error.URLError, OSError, ValueError):
        pass
    return info


def build_inventory(reg_result, directory, excluded):
    """Turn the registrations dict into a normalized desk-phone list."""
    phones = []
    for ext, entry in sorted(reg_result.items()):
        desk = None
        for r in entry.get("registrations", []):
            if "forcepro" in str(r.get("useragent", "")).lower():
                desk = r
                break
        if desk is None:
            continue  # softphone-only user (x-bees) — not a desk phone
        model, firmware, mac = parse_useragent(desk.get("useragent", ""))
        m = CONTACT_IP_RE.search(desk.get("contact", "") or "")
        ip = m.group(1) if m else ""
        online = str(desk.get("online", "")).strip() == "1"
        d = directory.get(ext, {})
        phones.append({
            "ext": ext,
            "name": d.get("name", ""),
            "did": d.get("did", ""),
            "department": d.get("department", ""),
            "email": d.get("email", ""),
            "ip": ip,
            "mac": mac,
            "model": model,
            "firmware": firmware,
            "sipStatus": "registered" if online else "unregistered",
            "online": online,
            "reachable": online,   # PBX registration is the liveness signal
            "excluded": ext in excluded,
        })
    return phones


def fetch_exclusions(base, key):
    """Excluded extensions for this endpoint (persisted server-side)."""
    try:
        code, body = http(f"{base}/api/v1/phones/{key}/exclusions")
        if code == 200:
            return set(str(e) for e in json.loads(body).get("excluded", []))
    except (urllib.error.URLError, OSError, ValueError):
        pass
    return set()


def fetch_thresholds(base, key):
    """Effective (degraded_at, down_at) thresholds — global or per-site override."""
    try:
        code, body = http(f"{base}/api/v1/phones/{key}/settings")
        if code == 200:
            e = json.loads(body).get("effective", {})
            return int(e.get("degradedAt", DEGRADED_AT)), int(e.get("downAt", 10))
    except (urllib.error.URLError, OSError, ValueError):
        pass
    return DEGRADED_AT, 10  # fallback defaults


def evaluate_health(phones, pbx_reachable, degraded_at, down_at):
    """Return (status, counts) applying the thresholds to MONITORED phones."""
    monitored = [p for p in phones if not p["excluded"]]
    online = sum(1 for p in monitored if p["online"])
    offline = len(monitored) - online
    counts = {
        "total": len(phones),
        "monitored": len(monitored),
        "online": online,
        "offline": offline,
        "excluded": sum(1 for p in phones if p["excluded"]),
    }
    if not pbx_reachable:
        status = "down"
    elif monitored and online == 0:
        status = "down"
    elif offline >= down_at:
        status = "down"
    elif offline >= degraded_at:
        status = "degraded"
    else:
        status = "healthy"
    return status, counts


# --------------------------------------------------------------------------- #
def push_inventory(base, key, phones, status, counts, push_token):
    body = json.dumps({"phones": phones, "status": status, "counts": counts})
    http(f"{base}/api/v1/phones/{key}", push_token, method="POST", body=body)


def push_result(base, key, success, error, duration_ms, push_token):
    from urllib.parse import urlencode
    q = {"success": "true" if success else "false", "duration": f"{int(duration_ms)}ms"}
    if error and not success:
        q["error"] = error
    http(f"{base}/api/v1/endpoints/{key}/external?{urlencode(q)}", push_token, method="POST")


# --------------------------------------------------------------------------- #
def run_location(loc, push_token, base):
    key, token = loc["key"], os.environ.get(loc["token_env"], "")
    if not token:
        print(f"ERROR: {loc['token_env']} not set; skipping {key}", file=sys.stderr)
        return
    error, phones, pbx_reachable = None, [], True

    # Response time = ONLY the PBX API reachability call (how responsive the phone
    # system is). The registrations/Colleagues fetches below are data-gathering
    # overhead (the Colleagues directory is ~800 records) and must NOT inflate it.
    reach_start = time.monotonic()
    try:
        code, _ = http(f"{loc['pbx']}/api/v1/PBX/version/", token)
        duration_ms = (time.monotonic() - reach_start) * 1000.0
        if code != 200:
            error, pbx_reachable = f"PBX API unreachable (HTTP {code})", False
    except (urllib.error.URLError, OSError) as exc:
        duration_ms = (time.monotonic() - reach_start) * 1000.0
        error, pbx_reachable = f"PBX API unreachable ({exc})", False

    if pbx_reachable:
        try:
            code, body = http(f"{loc['pbx']}/api/v1/PBX/Users/Sip/Registrations", token)
            reg_result = json.loads(body).get("result", {}) if code == 200 else {}
            # Wildix (PHP json_encode) serializes an EMPTY registrations map as a
            # JSON array [] rather than {} — a PBX with zero registered phones.
            # Coerce any non-dict (i.e. []) to {} so build_inventory doesn't crash.
            if not isinstance(reg_result, dict):
                reg_result = {}
            directory = fetch_directory(loc["pbx"], token)
            excluded = fetch_exclusions(base, key)
            phones = build_inventory(reg_result, directory, excluded)
        except (urllib.error.URLError, OSError, ValueError) as exc:
            error, pbx_reachable = f"registrations error ({exc})", False

    degraded_at, down_at = fetch_thresholds(base, key)
    status, counts = evaluate_health(phones, pbx_reachable, degraded_at, down_at)

    # 'degraded' is NOT a hard failure (no red alarm); only 'down' fails the check.
    success = status != "down"
    reason = None
    if status == "down":
        reason = error or "all monitored phones offline"
    elif status == "degraded":
        reason = f"{counts['offline']} of {counts['monitored']} desk phones offline"

    print(f"{key}: status={status} online={counts['online']} offline={counts['offline']} "
          f"excluded={counts['excluded']} dur={int(duration_ms)}ms")

    try:
        # Always push inventory — even an empty list — so the drill-in can show
        # "PBX healthy, 0 phones registered" instead of a misleading "collector
        # hasn't reported" placeholder.
        push_inventory(base, key, phones, status, counts, push_token)
    except (urllib.error.URLError, OSError) as exc:
        print(f"WARN: inventory push failed for {key}: {exc}", file=sys.stderr)
    try:
        push_result(base, key, success, reason, duration_ms, push_token)
    except (urllib.error.URLError, OSError) as exc:
        print(f"WARN: result push failed for {key}: {exc}", file=sys.stderr)


def sweep_once():
    push_token = os.environ.get("PHONES_PUSH_TOKEN", "")
    base = os.environ.get("GATUS_PUSH_BASE", "http://localhost:8080")
    if not push_token:
        print("ERROR: PHONES_PUSH_TOKEN not set", file=sys.stderr)
        sys.exit(1)
    for loc in LOCATIONS:
        try:
            run_location(loc, push_token, base)
        except Exception as exc:  # never let one site kill the run
            print(f"ERROR running {loc['key']}: {exc}", file=sys.stderr)


def sweep_requested(base):
    """Claim any pending force-sweep requests the UI POSTed. Returns True if a
    sweep was requested (the GET clears the pending set server-side)."""
    try:
        code, body = http(f"{base}/api/v1/phones/sweep-pending")
        if code == 200:
            return bool(json.loads(body).get("pending"))
    except (urllib.error.URLError, OSError, ValueError):
        pass
    return False


def main():
    load_dotenv()
    if os.environ.get("LOOP") == "1":
        lo = int(os.environ.get("SWEEP_MIN", "15"))
        hi = int(os.environ.get("SWEEP_MAX", "45"))
        base = os.environ.get("GATUS_PUSH_BASE", "http://localhost:8080")
        poll = float(os.environ.get("SWEEP_POLL", "2"))   # force-sweep responsiveness
        while True:
            sweep_once()
            # Interruptible wait: sleep the jittered interval in short chunks,
            # breaking early to sweep now if the UI asked for a force-sweep.
            wait, waited = random.uniform(lo, hi), 0.0
            while waited < wait:
                time.sleep(poll)
                waited += poll
                if sweep_requested(base):
                    break
    else:
        sweep_once()


if __name__ == "__main__":
    main()
