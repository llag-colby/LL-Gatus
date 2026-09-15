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

import wildix_s2s

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
    # Cullman is longlewisCU, not longlewisCL. longlewiscl exists and accepts a
    # token, but it is a different tenant: it serves the shared colleague
    # directory and has zero SIP registrations, which is why this row reported
    # "no phones reporting" indefinitely. The directory is authoritative — every
    # groupName=Cullman record carries pbx=longlewiscu.wildixin.com and
    # dialplan=usersCU. The token must be issued on longlewiscu.
    {
        "key": "phones_cullman",           # slug(group=Phones)_slug(name=Cullman)
        "label": "Cullman",
        "pbx": "https://longlewiscu.wildixin.com",
        "token_env": "PHONES_CULLMAN_TOKEN",
        # Which host serves Cullman has been contested, so stop asserting it and
        # let the registrations decide. Tried in order; the first that both
        # authenticates AND reports registered phones wins. Put a token issued
        # on longlewiscu in PHONES_CULLMAN_CU_TOKEN and it takes priority;
        # otherwise the general token is tried against both hosts.
        "pbx_candidates": [
            ("https://longlewiscu.wildixin.com", "PHONES_CULLMAN_CU_TOKEN"),
            ("https://longlewiscu.wildixin.com", "PHONES_CULLMAN_TOKEN"),
            ("https://longlewiscl.wildixin.com", "PHONES_CULLMAN_TOKEN"),
        ],
    },
    # Decatur GMC and Decatur KIA share ONE PBX (longlewisde) and one token, so
    # both cards will always show the same phone inventory. Two entries because
    # the dashboard cards are keyed by endpoint name.
    {
        "key": "phones_decatur-gmc",       # slug(group=Phones)_slug(name=Decatur GMC)
        "label": "Decatur GMC",
        "pbx": "https://longlewisde.wildixin.com",
        "token_env": "PHONES_DECATUR_TOKEN",
    },
    {
        "key": "phones_decatur-kia",       # slug(group=Phones)_slug(name=Decatur KIA)
        "label": "Decatur KIA",
        "pbx": "https://longlewisde.wildixin.com",
        "token_env": "PHONES_DECATUR_TOKEN",
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


def fetch_directory(pbx, token, label=""):
    """(ext -> {name, did, department, email}, home_pbx) — both best effort.

    home_pbx is the PBX host the directory says THIS site's users actually live
    on, taken from each record's `pbx` field and tallied over the records whose
    groupName matches the site label. The colleague directory is shared across
    every Long Lewis tenant, so a PBX can happily answer for a site whose phones
    register somewhere else entirely — that is exactly how Cullman sat on
    longlewiscl reporting zero phones. Used only to explain a zero-phone result,
    never to decide health.
    """
    info, homes = {}, {}
    want = (label or "").strip().lower()
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
                group = str(rec.get("groupName") or "").strip().lower()
                host = str(rec.get("pbx") or "").strip().lower()
                if want and group and host and (group in want or want in group):
                    homes[host] = homes.get(host, 0) + 1
    except (urllib.error.URLError, OSError, ValueError):
        pass
    home_pbx = max(homes, key=homes.get) if homes else ""
    return info, home_pbx


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


def inventory_from_presence(directory, label, presence, excluded):
    """Build a phone list from company-scoped presence rather than local SIP
    registrations, for a site whose own PBX we cannot authenticate against.

    Presence carries less than a registration does: no model, firmware, MAC or
    contact IP, because those live in the SIP registration on the home PBX. Those
    fields stay empty rather than being invented. The liveness signal, which is
    what the row is actually about, is just as good.

    One honest difference: local registrations are filtered to desk phones by
    user agent, and presence has no user agent, so a user with only a softphone
    counts here where they would not on a locally-read site. The reason line
    says so, and every phone is tagged source=presence.
    """
    want = (label or "").strip().lower()
    phones = []
    for ext, d in sorted(directory.items()):
        if str(d.get("department") or "").strip().lower() != want:
            continue
        seen = presence.get(str(ext))
        if seen is None:
            continue
        online = bool(seen.get("registered"))
        phones.append({
            "ext": ext,
            "name": d.get("name", ""),
            "did": d.get("did", ""),
            "department": d.get("department", ""),
            "email": d.get("email", ""),
            "ip": "", "mac": "", "model": "", "firmware": "",
            "sipStatus": "registered" if online else "unregistered",
            "online": online,
            "reachable": online,
            "excluded": ext in excluded,
            "source": "presence",
            "telephony": seen.get("telephony", ""),
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
    elif not phones:
        # PBX answered but reported ZERO registered desk phones. That is an
        # outage, not a healthy site — without this branch the empty case falls
        # through to "0 offline < threshold" and the card goes green with
        # nothing behind it.
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
    # Send the reason for a DEGRADED result too ("3 of 44 desk phones offline"),
    # not just a failure: Gatus stores it against the passing result so the
    # dashboard can paint it amber rather than a bare green.
    if error:
        q["error"] = error
    http(f"{base}/api/v1/endpoints/{key}/external?{urlencode(q)}", push_token, method="POST")


# --------------------------------------------------------------------------- #
def host_of(pbx):
    return pbx.split("//")[-1].strip("/").lower()


def pbx_host_is_up(host):
    """Does the PBX answer HTTPS at all, without a token?

    The WMS login redirect is a perfectly good liveness signal: it proves the
    box is serving even when we hold no credential for it. Used only to tell
    "we cannot read this PBX" apart from "this PBX is down", which are very
    different things to put on a wallboard.
    """
    try:
        req = urllib.request.Request(f"https://{host}/", method="HEAD")
        with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT, context=_ctx()):
            return True
    except urllib.error.HTTPError:
        return True  # it answered, just not with a 200
    except (urllib.error.URLError, OSError):
        return False


def candidates_for(loc):
    """(pbx_url, token_env) pairs to try, in order. Almost every site has exactly
    one; a site whose PBX is disputed can list several."""
    return list(loc.get("pbx_candidates") or [(loc["pbx"], loc["token_env"])])


def resolve_pbx(loc):
    """Pick the PBX that actually holds this site's phones.

    A candidate that authenticates AND reports registrations wins outright. If
    none reports any, the first that merely authenticates is used, so the row
    reads "reachable, 0 registered" against a real PBX rather than a 401 nobody
    can act on. Returns (pbx, token, tried), where tried is a short account of
    what each candidate did so the dashboard can show the evidence.
    """
    tried, first_ok = [], None
    for pbx, token_env in candidates_for(loc):
        host = host_of(pbx)
        token = os.environ.get(token_env, "")
        if not token:
            continue
        try:
            code, _ = http(f"{pbx}/api/v1/PBX/version/", token)
            if code != 200:
                tried.append(f"{host} HTTP {code}")
                continue
        except urllib.error.HTTPError as exc:
            tried.append(f"{host} HTTP {exc.code}")
            continue
        except (urllib.error.URLError, OSError) as exc:
            tried.append(f"{host} {type(exc).__name__}")
            continue
        if first_ok is None:
            first_ok = (pbx, token)
        try:
            code, body = http(f"{pbx}/api/v1/PBX/Users/Sip/Registrations", token)
            result = json.loads(body).get("result", {}) if code == 200 else {}
            count = len(result) if isinstance(result, dict) else 0
        except (urllib.error.URLError, OSError, ValueError):
            count = 0
        tried.append(f"{host} {count} registered")
        if count > 0:
            return pbx, token, tried
    if first_ok:
        return first_ok[0], first_ok[1], tried
    fallback_pbx, fallback_env = candidates_for(loc)[0]
    return fallback_pbx, os.environ.get(fallback_env, ""), tried


def run_location(loc, push_token, base):
    key = loc["key"]
    pbx, token, tried = resolve_pbx(loc)
    if not token:
        print(f"ERROR: no token set for {key}; skipping", file=sys.stderr)
        return
    if len(candidates_for(loc)) > 1:
        print(f"{key}: resolved to {host_of(pbx)} [{'; '.join(tried) or 'none tried'}]")
    error, phones, pbx_reachable, home_pbx = None, [], True, ""
    # Bound up front: the presence fallback below reads them even on the path
    # where the PBX was never reachable and the block that fills them is skipped.
    directory, excluded = {}, set()

    # Response time = ONLY the PBX API reachability call (how responsive the phone
    # system is). The registrations/Colleagues fetches below are data-gathering
    # overhead (the Colleagues directory is ~800 records) and must NOT inflate it.
    reach_start = time.monotonic()
    try:
        code, _ = http(f"{pbx}/api/v1/PBX/version/", token)
        duration_ms = (time.monotonic() - reach_start) * 1000.0
        if code != 200:
            error, pbx_reachable = f"PBX API unreachable (HTTP {code})", False
    except urllib.error.HTTPError as exc:
        duration_ms = (time.monotonic() - reach_start) * 1000.0
        # 401 is not "unreachable" — the PBX answered and rejected the token,
        # which almost always means the token was issued on a DIFFERENT PBX.
        # Naming the host here puts the diagnosis on the dashboard instead of
        # leaving a bare 401 that reads like an outage. HTTPError subclasses
        # URLError, so this has to be caught first.
        host = host_of(pbx)
        if exc.code in (401, 403):
            error = (f"token rejected by {host} (HTTP {exc.code}) - the token must "
                     f"be issued on THIS PBX, not another one")
        else:
            error = f"PBX API unreachable (HTTP {exc.code})"
        pbx_reachable = False
    except (urllib.error.URLError, OSError) as exc:
        duration_ms = (time.monotonic() - reach_start) * 1000.0
        error, pbx_reachable = f"PBX API unreachable ({exc})", False

    if pbx_reachable:
        try:
            code, body = http(f"{pbx}/api/v1/PBX/Users/Sip/Registrations", token)
            # A non-200 here (bad/expired token -> 401/403) used to fall through as
            # an empty map, which read as "0 phones, all fine". Fail loudly instead.
            if code != 200:
                raise ValueError(f"registrations HTTP {code}")
            reg_result = json.loads(body).get("result", {})
            # Wildix (PHP json_encode) serializes an EMPTY registrations map as a
            # JSON array [] rather than {} — a PBX with zero registered phones.
            # Coerce any non-dict (i.e. []) to {} so build_inventory doesn't crash.
            if not isinstance(reg_result, dict):
                reg_result = {}
            directory, home_pbx = fetch_directory(pbx, token, loc["label"])
            excluded = fetch_exclusions(base, key)
            phones = build_inventory(reg_result, directory, excluded)
        except (urllib.error.URLError, OSError, ValueError) as exc:
            error, pbx_reachable = f"registrations error ({exc})", False

    degraded_at, down_at = fetch_thresholds(base, key)
    status, counts = evaluate_health(phones, pbx_reachable, degraded_at, down_at)

    # A site's phones can live on a PBX we hold no token for. An empty
    # registration list from some OTHER node is then not this site's status, and
    # reporting it as "0 phones" states something we do not know. Report what is
    # actually knowable instead: whether that PBX is up.
    #
    # Up but unreadable is a PASS carrying its reason (amber) rather than a
    # failure, because nothing is known to be broken; the monitoring is what is
    # incomplete, and the reason says exactly what is missing. The moment a
    # token for that PBX exists, resolve_pbx picks it and real data returns with
    # no change here.
    reason_override = None
    if not phones:
        home = home_pbx or host_of(candidates_for(loc)[0][0])
        if home and home != host_of(pbx):
            # Registrations are local to the home PBX, but wda.wildix.com keys
            # presence on the COMPANY, so it reaches a PBX we hold no token for.
            # It needs S2S credentials; without them, fall through to reporting
            # whether that PBX is merely alive.
            if wildix_s2s.credentials() and directory:
                try:
                    exts = [e for e, d in directory.items()
                            if str(d.get("department") or "").strip().lower()
                            == loc["label"].strip().lower()]
                    seen = wildix_s2s.query_presence(
                        exts, company=os.environ.get("WILDIX_COMPANY_ID") or None)
                    if seen:
                        phones = inventory_from_presence(
                            directory, loc["label"], seen, excluded)
                        status, counts = evaluate_health(
                            phones, True, degraded_at, down_at)
                        print(f"{key}: presence fallback via {wildix_s2s.WDA_HOST} "
                              f"covered {len(seen)} of {len(exts)} extensions")
                except (urllib.error.URLError, OSError, ValueError, KeyError) as exc:
                    print(f"WARN: presence fallback failed for {key}: {exc}",
                          file=sys.stderr)

    if not phones:
        home = home_pbx or host_of(candidates_for(loc)[0][0])
        if home and home != host_of(pbx):
            if pbx_host_is_up(home):
                status = "degraded"
                reason_override = (
                    f"phone detail unavailable: {home} is up but no API token for it; "
                    f"the token in use is for {host_of(pbx)}, which hosts no "
                    f"{loc['label']} phones")
            else:
                status = "down"
                reason_override = f"no phones reporting ({home} is not answering)"

    # 'degraded' is NOT a hard failure (no red alarm); only 'down' fails the check.
    success = status != "down"
    reason = reason_override
    if reason_override:
        # The override already says exactly what is and is not known; the
        # branches below would replace it with a phone tally we do not have.
        pass
    elif status == "down":
        # NOTE: the "no phones reporting" wording is a CONTRACT with the UI —
        # LocationCard.vue matches it to paint the bar BLACK (nothing reported)
        # instead of red (phones present but offline). Don't reword the prefix.
        if error:
            reason = error
        elif not phones:
            detail = "PBX reachable, 0 desk phones registered"
            if len(candidates_for(loc)) > 1 and tried:
                detail += f" (tried {'; '.join(tried)})"
            queried = host_of(pbx)
            if home_pbx and home_pbx != queried:
                # The wrong-tenant case: say where the phones actually are
                # instead of leaving someone to rediscover it.
                detail += f"; directory says {loc['label']} users are on {home_pbx}, not {queried}"
            reason = f"no phones reporting ({detail})"
        else:
            reason = "all monitored phones offline"
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
