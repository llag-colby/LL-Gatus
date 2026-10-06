#!/usr/bin/env python3
"""
smb_collector.py — push-based SMB share monitor for LL-Gatus.

Same shape as phone_collector.py and unifi_collector.py: Gatus never calls the
file servers. This script actually connects to each share, lists its root and
reads its free space, then pushes a pass/degraded/fail result plus a detail
snapshot into Gatus.

Why this exists
---------------
Gatus has no SMB client. The only thing it can check natively is tcp/445 on the
file server, which proves the service is listening and NOTHING else. A share can
be unshared, ACL'd shut, full, or pointed at a dead DFS referral while port 445
stays wide open — which is exactly the outage people actually hit. So the row on
the dashboard used to go green while nobody could open the drive.

This collector tests the thing that matters, in four stages, and reports which
stage failed:

    connect   TCP + SMB negotiate to the server
    auth      NTLM session as the service account
    mount     open the share itself  (catches "share is gone")
    list      read the root directory + free space  (catches "ACL'd shut", "full")

A row is only green when all four pass.

Status
------
    healthy    all four stages passed, free space above the warning floor
    degraded   share works but something needs attention (low free space, slow)
    down       a stage failed — the share is not usable

degraded is pushed as a PASS carrying its reason, so a nearly-full share reads
amber on the wall without firing a down alert. That is the same convention the
phones and UniFi rows use.

Credentials
-----------
SMB needs an account; there is no anonymous read on these shares. Set a
read-only service account in .env:

    SMB_USER=longlewis\\svc-gatus      (or svc-gatus@longlewis.local)
    SMB_PASS=...

Without them every row reports "no smb reporting", which the dashboard paints
BLACK rather than red — an absent signal, not a failure. Grant the account only
Read on the watched shares; it never writes anything.

Run modes
---------
    python3 smb_collector.py            # one sweep, then exit
    LOOP=1 python3 smb_collector.py     # daemon: sweep every 45-90s (jittered)

Env
---
    SMB_USER, SMB_PASS                  service account (required)
    SMB_PUSH_TOKEN or PHONES_PUSH_TOKEN push token (matches config.yaml)
    GATUS_PUSH_BASE                     default http://localhost:8080
    SMB_FREE_WARN_PCT                   degraded below this % free (default 10)
    SMB_FREE_WARN_GB                    degraded below this GB free (default 20)
    SMB_SLOW_MS                         degraded above this listing time (default 4000)
    SMB_TIMEOUT                         per-server connection timeout (default 20)
    LOOP, SWEEP_MIN, SWEEP_MAX
"""

import json
import os
import random
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

import smbclient
from smbprotocol.exceptions import (
    AccessDenied,
    BadNetworkName,
    ObjectNameNotFound,
    ObjectPathNotFound,
    SMBAuthenticationError,
    SMBException,
    SMBOSError,
)

HTTP_TIMEOUT = 25
GB = 1024.0 ** 3

# --------------------------------------------------------------------------- #
# The shares to watch.
#
# `host` is an IP on purpose: this runs in a container whose resolver is the
# Docker host's, which does not reliably resolve the AD short names. The server
# NAME is still carried so the dashboard can show the UNC path an operator
# actually types, and auth is pinned to NTLM below because Kerberos cannot issue
# a ticket for an SPN addressed by IP.
#
# Keys must match config.yaml: slug(group)_slug(name).
#   group "L:" + name "SMB Shares" -> "l:_smb-shares"
SHARES = [
    {"key": "l:_smb-shares", "drive": "L:", "server": "rr-fs01", "host": "10.6.102.66", "share": "Company Hub"},
    {"key": "k:_smb-shares", "drive": "K:", "server": "llfs01", "host": "10.6.102.27", "share": "exports"},
    {"key": "p:_smb-shares", "drive": "P:", "server": "llfs01", "host": "10.6.102.27", "share": "ReportHub(Shoals)"},
]

# A root listing is capped: these are real file shares and some roots are large.
# The count is what gets charted, so it stops at the cap and says so rather than
# walking 40,000 entries every sweep.
LIST_CAP = 500

# Prefix that marks "we could not even try". LocationCard and the drill-in match
# on it and paint the row BLACK instead of red, because an absent signal is not
# the same as a reported failure. Keep in step with the NOT_REPORTING regex in
# web/app/src/components/LocationCard.vue.
NOT_REPORTING = "no smb reporting"


def push(url, token, body=None):
    headers = {"Authorization": f"Bearer {token}"}
    data = None
    if body is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(body).encode()
    req = urllib.request.Request(url, headers=headers, method="POST", data=data)
    with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT) as resp:
        return resp.getcode()


def unc(server, share):
    """The path an operator pastes into Explorer, for display only."""
    return "\\\\" + server + "\\" + share


def smb_path(host, share):
    """The path actually used for the connection — IP, not name. See SHARES."""
    return "\\\\" + host + "\\" + share


def classify(exc, stage="connect"):
    """Map an SMB exception to (reason, stage-it-really-failed-at).

    The library raises the same SMBOSError family for several very different
    problems, and telling them apart is most of the value of this collector:
    "wrong password" and "share was deleted" need completely different people.
    """
    if isinstance(exc, SMBAuthenticationError):
        return "authentication failed for the service account", "auth"
    if isinstance(exc, BadNetworkName):
        return "the server has no share by that name", "mount"
    if isinstance(exc, AccessDenied):
        return "the service account is denied access to this share", "mount"
    if isinstance(exc, (ObjectNameNotFound, ObjectPathNotFound)):
        return "the share path does not resolve on the server", "mount"
    text = str(exc).strip() or exc.__class__.__name__
    # A share that does not exist does NOT reliably raise BadNetworkName. Windows
    # answers STATUS_BAD_NETWORK_NAME, but Samba answers STATUS_OBJECT_PATH_NOT_FOUND
    # (0xc0000225) wrapped in a plain SMBOSError, which would otherwise surface as
    # an unreadable "[Error 2] [NtStatus 0xc0000225]". Verified against a live
    # Samba server. At the mount stage either one means the same thing.
    lowered = text.lower()
    # Logon problems arrive as a generic SMBException carrying an NTSTATUS, not
    # as SMBAuthenticationError — verified against a live server. These four are
    # worth separating because each one goes to a different fix, and "SMB error:
    # Received unexpected status from the server: ... (3221225581)" sends nobody
    # anywhere.
    for codes, message in (
        (("0xc000006d", "0xc000006e", "logon is invalid", "logon failure"),
         "authentication failed - bad username or password"),
        (("0xc0000071", "password expired"), "the service account's password has expired"),
        (("0xc0000234", "account locked"), "the service account is locked out"),
        (("0xc0000072", "account disabled"), "the service account is disabled"),
    ):
        if any(c in lowered for c in codes):
            return message, "auth"
    if stage == "mount" and ("0xc0000225" in lowered or "0xc00000cc" in lowered
                             or "no such file" in lowered or "bad network name" in lowered):
        return "the server has no share by that name", "mount"
    # Denied on the share itself. Like the two above, this does NOT arrive as the
    # AccessDenied class — it is an SMBOSError carrying STATUS_ACCESS_DENIED, seen
    # live against llfs01\HighSecurityHub. The raw form reads
    # "[Error 0] [NtStatus 0xc0000022] Unknown NtStatus error returned", which
    # tells nobody that the answer is "grant the service account Read".
    if "0xc0000022" in lowered or "access_denied" in lowered or "access is denied" in lowered:
        return "the service account is denied access to this share", stage
    if isinstance(exc, SMBOSError):
        return f"SMB error: {text}", stage
    if isinstance(exc, SMBException):
        return f"SMB error: {text}", stage
    return f"{exc.__class__.__name__}: {text}", stage


def check_share(entry, user, password, timeout, thresholds):
    """Connect to one share and report what happened, stage by stage.

    Returns (status, reason, counts, detail). Never raises: a share that cannot
    be reached is a result, not a crash, and one bad server must not stop the
    other rows from reporting.
    """
    path = smb_path(entry["host"], entry["share"])
    steps = []
    counts = {}

    # Drop any cached session for this server FIRST.
    #
    # smbclient caches connections per server, so without this the second and
    # third share on one host reuse the first one's session: register_session
    # returns instantly, reports "connect 0.0 ms, ok", and never re-authenticates.
    # That had two consequences, both found by testing against a live Samba
    # server rather than by reading the docs:
    #
    #   * a wrong or expired password came back HEALTHY for every share after
    #     the first one on that host, and
    #   * the per-stage timings on the drill-in were a fiction for those shares.
    #
    # K:, P: and S: all live on llfs01, so this is the difference between three
    # independent signals and one signal wearing three labels. The cost is four
    # handshakes per sweep instead of two, which at a 45-90s interval is nothing.
    try:
        smbclient.delete_session(entry["host"])
    except Exception:  # noqa: BLE001 - nothing cached yet is the normal case
        pass

    def step(name, fn):
        """Run one stage, time it, and record it. Returns (ok, value)."""
        started = time.monotonic()
        try:
            value = fn()
            ms = (time.monotonic() - started) * 1000.0
            steps.append({"name": name, "ok": True, "ms": round(ms, 1)})
            return True, value, ms
        except Exception as exc:  # noqa: BLE001 - every failure is a reportable result
            ms = (time.monotonic() - started) * 1000.0
            reason, blamed = classify(exc, name)
            steps.append({"name": name, "ok": False, "ms": round(ms, 1), "error": reason})
            return False, (reason, blamed), ms

    # --- connect + auth -----------------------------------------------------
    # register_session does the TCP connect, the SMB negotiate AND the NTLM
    # bind, so a failure here is either network or credentials; classify() is
    # what separates them.
    ok, value, connect_ms = step("connect", lambda: smbclient.register_session(
        entry["host"], username=user, password=password,
        connection_timeout=timeout, auth_protocol="ntlm",
    ))
    if not ok:
        reason, blamed = value
        # Re-label the stage: register_session covers two of them.
        if steps and blamed == "auth":
            steps[-1]["name"] = "auth"
        return "down", reason, counts, {"steps": steps}
    counts["connectMs"] = round(connect_ms, 1)

    # --- mount + free space -------------------------------------------------
    # stat_volume is the first call that touches the SHARE rather than the
    # server, so "share is gone" and "ACL'd shut" surface here.
    ok, value, volume_ms = step("mount", lambda: smbclient.stat_volume(path))
    if not ok:
        reason, _ = value
        return "down", reason, counts, {"steps": steps}
    volume = value
    total = float(getattr(volume, "total_size", 0) or 0)
    # caller_available_size honours a per-user quota, which is what the service
    # account would actually be able to write. That is the honest number.
    avail = float(getattr(volume, "caller_available_size", 0) or 0)
    free_pct = (avail / total * 100.0) if total > 0 else None
    counts["mountMs"] = round(volume_ms, 1)
    counts["totalGB"] = round(total / GB, 2)
    counts["freeGB"] = round(avail / GB, 2)
    if free_pct is not None:
        counts["freePct"] = round(free_pct, 2)

    # --- list root ----------------------------------------------------------
    # Proves the directory is actually readable, which a volume stat does not.
    def list_root():
        names = []
        truncated = False
        for item in smbclient.scandir(path):
            names.append(item.name)
            if len(names) >= LIST_CAP:
                truncated = True
                break
        return names, truncated

    ok, value, list_ms = step("list", list_root)
    if not ok:
        reason, _ = value
        return "down", reason, counts, {"steps": steps}
    names, truncated = value
    counts["listMs"] = round(list_ms, 1)
    counts["entries"] = len(names)

    detail = {
        "steps": steps,
        "server": entry["server"],
        "host": entry["host"],
        "share": entry["share"],
        "drive": entry["drive"],
        "unc": unc(entry["server"], entry["share"]),
        "account": user,
        "auth": "ntlm",
        "totalBytes": int(total),
        "availBytes": int(avail),
        "entriesTruncated": truncated,
        # A few names prove to a human that this really read the share, and make
        # a wrong-share mix-up obvious at a glance.
        "sample": sorted(names)[:8],
    }

    # --- degraded checks ----------------------------------------------------
    # The share WORKS, so these are warnings carried on a pass, never failures.
    reasons = []
    if free_pct is not None and total > 0:
        if free_pct < thresholds["free_pct"] and (avail / GB) < thresholds["free_gb"]:
            reasons.append(
                f"only {free_pct:.1f}% free ({avail / GB:.0f} GB of {total / GB:.0f} GB)")
    if list_ms > thresholds["slow_ms"]:
        reasons.append(f"root listing took {list_ms / 1000.0:.1f}s")
    if reasons:
        return "degraded", "; ".join(reasons), counts, detail
    return "healthy", "", counts, detail


def report(base, token, entry, status, reason, counts, detail):
    """Push the snapshot, then the pass/fail result. Mirrors unifi_collector."""
    key = entry["key"]
    # Keep the colon literal. These keys are "l:_smb-shares", and the routes
    # that validate a key against config.yaml (this snapshot push and the
    # /external result push) read the raw path param without unescaping it, so
    # a %3A does not match the configured key and 404s. A colon is legal in a
    # path segment, so the fix is to not encode it in the first place.
    safe_key = urllib.parse.quote(key, safe=":")
    success = status != "down"

    body = {"status": status, "drive": entry["drive"],
            "path": unc(entry["server"], entry["share"]),
            "counts": counts, "detail": detail}
    try:
        push(f"{base}/api/v1/smb/{safe_key}", token, body)
    except urllib.error.HTTPError as exc:
        # HTTPError subclasses URLError, so this has to be caught first. A 404
        # means config.yaml has no external-endpoint for this key yet.
        if exc.code == 404:
            print(f"WARN: {key} has no external-endpoint in config.yaml", file=sys.stderr)
            return
        print(f"WARN: snapshot push failed for {key}: {exc}", file=sys.stderr)
    except (urllib.error.URLError, OSError) as exc:
        print(f"WARN: snapshot push failed for {key}: {exc}", file=sys.stderr)

    q = {"success": "true" if success else "false",
         "duration": f"{int(sum(s.get('ms', 0) for s in detail.get('steps', [])))}ms"}
    # Send the reason for a DEGRADED result too, not just a failure: Gatus
    # stores it against the passing result, which is how the dashboard paints
    # "only 4% free" amber instead of green without raising a down alert.
    if reason:
        q["error"] = reason
    try:
        push(f"{base}/api/v1/endpoints/{safe_key}/external?{urllib.parse.urlencode(q)}", token)
    except urllib.error.HTTPError as exc:
        if exc.code == 404:
            print(f"WARN: {key} has no external-endpoint in config.yaml", file=sys.stderr)
            return
        print(f"WARN: result push failed for {key}: {exc}", file=sys.stderr)
    except (urllib.error.URLError, OSError) as exc:
        print(f"WARN: result push failed for {key}: {exc}", file=sys.stderr)


def report_not_reporting(base, token, entry, reason):
    """No credentials, so nothing was attempted. Black row, not a red one."""
    detail = {"steps": [], "server": entry["server"], "share": entry["share"],
              "drive": entry["drive"], "unc": unc(entry["server"], entry["share"])}
    report(base, token, entry, "down", f"{NOT_REPORTING}: {reason}", {}, detail)


def sweep_once():
    user = (os.environ.get("SMB_USER") or "").strip()
    password = os.environ.get("SMB_PASS") or ""
    token = (os.environ.get("SMB_PUSH_TOKEN")
             or os.environ.get("PHONES_PUSH_TOKEN") or "").strip()
    base = os.environ.get("GATUS_PUSH_BASE", "http://localhost:8080")
    timeout = int(os.environ.get("SMB_TIMEOUT", "20"))
    thresholds = {
        "free_pct": float(os.environ.get("SMB_FREE_WARN_PCT", "10")),
        "free_gb": float(os.environ.get("SMB_FREE_WARN_GB", "20")),
        "slow_ms": float(os.environ.get("SMB_SLOW_MS", "4000")),
    }

    if not token:
        print("ERROR: neither SMB_PUSH_TOKEN nor PHONES_PUSH_TOKEN is set", file=sys.stderr)
        sys.exit(1)

    # No credentials is a CONFIGURATION gap, not a share outage. Every row says
    # so explicitly and reads black, because painting four shares red would send
    # someone hunting an outage that does not exist.
    if not user or not password:
        print("ERROR: SMB_USER and SMB_PASS are not set — pushing 'no smb reporting' "
              "for every share. Add a read-only service account to .env.", file=sys.stderr)
        for entry in SHARES:
            report_not_reporting(base, token, entry, "SMB_USER/SMB_PASS not configured")
        return

    for entry in SHARES:
        started = time.monotonic()
        status, reason, counts, detail = check_share(entry, user, password, timeout, thresholds)
        elapsed = (time.monotonic() - started) * 1000.0
        print(f"{entry['key']}: status={status} "
              f"free={counts.get('freeGB', '?')}GB entries={counts.get('entries', '?')} "
              f"dur={int(elapsed)}ms" + (f" reason={reason}" if reason else ""))
        report(base, token, entry, status, reason, counts, detail)

    # Belt and braces: check_share already drops the cached session before each
    # share, so auth is exercised per share. This clears whatever the last share
    # left open so nothing is held between sweeps either.
    try:
        smbclient.reset_connection_cache()
    except Exception:  # noqa: BLE001 - teardown must never fail a sweep
        pass


def main():
    if os.environ.get("LOOP") != "1":
        sweep_once()
        return
    low = int(os.environ.get("SWEEP_MIN", "45"))
    high = int(os.environ.get("SWEEP_MAX", "90"))
    while True:
        try:
            sweep_once()
        except Exception as exc:  # noqa: BLE001 - a daemon must not die on one bad sweep
            print(f"ERROR: sweep failed: {exc}", file=sys.stderr)
        time.sleep(random.uniform(low, high))


if __name__ == "__main__":
    main()
