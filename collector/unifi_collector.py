#!/usr/bin/env python3
"""
unifi_collector.py — push-based UniFi monitor for LL-Gatus.

Same shape as phone_collector.py: Gatus never calls UniFi. This script reads
Ubiquiti's **cloud** Site Manager API and pushes two rows per site into Gatus.

  Firewall row  (key: firewall_<site-slug>)
      The site's gateway and its WAN uplinks. Healthy when every enabled WAN is
      plugged, degraded when at least one is up but not all, down when the
      gateway is offline or every WAN is down.

  Wireless row  (key: wireless_<site-slug>)
      The site's access points and clients. Healthy when every AP is online,
      degraded when some are offline or tx-retry is high, down when no AP is up.

Why the cloud API
-----------------
    Base:   https://api.ui.com
    Auth:   X-API-KEY: <key from unifi.ui.com>

A key from unifi.ui.com is global: one credential covers every console on the
account, and the collector needs no route to any store's LAN. That matters here
because the box running this is not on the rooftop networks. The per-console
Integration API (https://<console>/proxy/network/integration/v1/...) is the
alternative and needs LAN reachability per site, so it is not used.

Three calls per sweep serve every site:

    GET /v1/hosts     consoles, incl. reportedState.wans and hardware.shortname
    GET /v1/sites     per-site statistics.counts + percentages.txRetry
    GET /v1/devices   per-host device list with online/offline status

Two things worth knowing about the data
---------------------------------------
  * Only *gateway* hardware has real WAN uplinks. A CloudKey, an NVR or a UOS
    Server also reports a `wans` array, but it is just that box's management
    NIC — Decatur's CloudKey "WAN" is 10.10.1.26, a LAN address. Reporting those
    as firewall uplinks would invent a firewall that isn't there, so hosts are
    filtered to gateway models and a site with no gateway pushes no firewall row.
  * AP counts come from the site's own `statistics.counts` (authoritative), not
    from classifying models in the device list. The device list is used only to
    name which APs are offline.

Run modes
---------
  python3 unifi_collector.py            # one sweep, then exit
  LOOP=1 python3 unifi_collector.py     # daemon: sweep every 30-60s (jittered)

Env
---
  UNIFI_API_KEY                         global Site Manager key (required)
  UNIFI_PUSH_TOKEN or PHONES_PUSH_TOKEN push token (matches config.yaml)
  GATUS_PUSH_BASE                       default http://localhost:8080
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

API_BASE = "https://api.ui.com"
HTTP_TIMEOUT = 25

# --------------------------------------------------------------------------- #
# Dashboard rows. `gateway_host` and `wireless_host` match a console's name in
# /v1/hosts (exact first, then substring). gateway_host=None means the site has
# no UniFi gateway, so no Firewall row is pushed for it.
#
# Keys must match config.yaml: slug(group)_slug(name).
#   group "Firewall" + name "Decatur GMC" -> firewall_decatur-gmc
# Console name in UniFi -> the dashboard card it belongs to. Only needed when
# the two differ; anything not listed uses the console's own name. Name a
# console after its site and it wires itself up with no entry here.
SITE_ALIASES = {
    # "BA-DCU-FWE-KIA": "Decatur KIA",
}

# Console names to ignore entirely, comma separated. Recorders and standalone
# cameras register as hosts but are not a site.
SKIP_CONSOLES = set(
    n.strip() for n in os.environ.get("UNIFI_SKIP_CONSOLES", "").split(",") if n.strip()
)

# Console hardware that actually routes traffic. Everything else (UCKP CloudKey,
# UNVR recorder, UOSSERVER) is a controller whose `wans` entry is a management
# NIC, not an internet uplink.
GATEWAY_PREFIXES = ("UDM", "UDR", "UXG", "UCG", "USG", "UDW")
NOT_GATEWAY = {"UCKP", "UCKG2", "UCK", "UNVR", "UNVRPRO", "UOSSERVER"}

# Device shortname prefixes that are NOT wireless, used only to name offline APs.
NON_WIRELESS_PREFIXES = ("US", "UCK", "UNVR", "UDM", "UDR", "UXG", "UCG",
                         "UOSSERVER", "UVC", "UA-", "UP ", "UP-")

# tx-retry above this on a site's wireless counts as degraded.
TX_RETRY_DEGRADED_PCT = float(os.environ.get("UNIFI_TX_RETRY_DEGRADED_PCT", "20"))


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


def api(path, key):
    req = urllib.request.Request(API_BASE + path,
                                 headers={"X-API-KEY": key, "Accept": "application/json"})
    with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT) as resp:
        if resp.getcode() != 200:
            raise ValueError(f"{path} -> HTTP {resp.getcode()}")
        return json.loads(resp.read())


def push(url, token, body=None):
    headers = {"Authorization": f"Bearer {token}"}
    data = None
    if body is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(body).encode()
    req = urllib.request.Request(url, headers=headers, method="POST", data=data)
    with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT) as resp:
        return resp.getcode()


def sanitize(s):
    """Mirror of sanitize() in config/key/key.go.

    A dashboard key is sanitize(group) + "_" + sanitize(name). This has to match
    the Go side character for character or every push 404s, so keep the two in
    step if that function ever gains a rule.
    """
    s = (s or "").strip().lower()
    for ch in ("/", "_", ".", ",", " ", "#", "+", "&"):
        s = s.replace(ch, "-")
    return s


def discover_sites(estate):
    """Turn every console on the account into a site, giving each only the rows
    it can actually fill.

    This replaces a hand-maintained table. A site got a Firewall row if somebody
    had typed a gateway_host and a Wireless row if somebody had typed a
    wireless_host, so a newly adopted console stayed invisible until a human
    noticed. The two deliberate suppressions in that table (Alabaster has no
    adopted APs; Ivory Tower used to be a controller with no WAN of its own)
    also had to be maintained by hand. Deriving both from the data reproduces
    those suppressions on its own and undoes them the moment the hardware
    changes underneath.
    """
    sites = []
    for host in estate.hosts:
        name = Estate._name(host)
        if not name or name in SKIP_CONSOLES:
            continue
        label = SITE_ALIASES.get(name, name)
        wans = [w for w in ((host.get("reportedState") or {}).get("wans") or [])
                if w.get("enabled", True)]
        devices = estate.devices_for_host(host)
        sites.append({
            "label": label,
            "slug": sanitize(label),
            "console": name,
            # A Firewall row needs a box that routes and at least one WAN port.
            "gateway_host": name if (is_gateway(host) and wans) else None,
            # A Wireless row needs at least one adopted wireless device. An
            # always-empty row reads as an outage, which is exactly why
            # Alabaster never had one.
            "wireless_host": name if any(is_wireless(d) for d in devices) else None,
        })
    sites.sort(key=lambda site: site["label"].lower())
    return sites


def fetch_uplink_settings(base, key):
    """How many uplinks this site is expected to have carrying traffic.

    0 means auto: expect exactly the ports that are plugged in, so an empty WAN2
    is not a permanent amber. Operator-set, persisted server side and polled
    once per row per sweep, the same shape as the phones collector's thresholds.
    Best effort: any error falls back to auto rather than failing the row.
    """
    try:
        req = urllib.request.Request(f"{base}/api/v1/unifi/{key}/settings",
                                     headers={"Accept": "application/json"})
        with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT) as resp:
            data = json.loads(resp.read())
        return int((data.get("effective") or {}).get("expectedWans") or 0)
    except (urllib.error.URLError, OSError, ValueError, TypeError, AttributeError):
        return 0


def known_rows(base):
    """Rows Gatus already holds a UniFi snapshot for.

    Used only when the cloud API is unreachable: discovery returns nothing then,
    and without this every row would keep showing stale data instead of going
    black. Reading them back means a dead API still reports "nothing reported"
    against exactly the rows that existed a minute ago.
    """
    try:
        req = urllib.request.Request(f"{base}/api/v1/unifi",
                                     headers={"Accept": "application/json"})
        with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT) as resp:
            data = json.loads(resp.read()) or {}
        rows = []
        for key, snap in data.items():
            kind, _, slug = key.partition("_")
            if kind in COLLECTORS and slug:
                rows.append((kind, {"slug": slug,
                                    "label": (snap or {}).get("site") or slug}))
        return sorted(rows, key=lambda r: (r[1]["label"].lower(), r[0]))
    except (urllib.error.URLError, OSError, ValueError, AttributeError):
        return []


def render_unregistered_yaml(rows):
    """A paste-ready external-endpoints block for consoles Gatus does not know.

    Gatus cannot register an external endpoint at runtime: the push handler
    looks the key up in the parsed config and 404s if it is absent, and
    config.yaml is baked into the image, so the file-watcher can never see a
    change. Discovery therefore stops at telling you exactly what to add.
    """
    out = ["",
           f"WARN: {len(rows)} UniFi row(s) have no external-endpoint in config.yaml and",
           "      were rejected with 404. Gatus cannot create these at runtime. Paste",
           "      this into the external-endpoints section of config.yaml, then run",
           "      `docker compose down && docker compose up -d`:",
           ""]
    for kind, site in rows:
        out.append(f"  - name: {site['label']}")
        out.append(f"    group: {kind.capitalize()}")
        out.append('    token: "${PHONES_PUSH_TOKEN}"')
    out.append("")
    return "\n".join(out)


def num(value, digits=1):
    try:
        return round(float(value), digits)
    except (TypeError, ValueError):
        return None


# --------------------------------------------------------------------------- #
class Estate:
    """One snapshot of the whole UniFi account: hosts, sites and devices."""

    def __init__(self, key):
        self.hosts = api("/v1/hosts", key).get("data") or []
        self.sites = api("/v1/sites", key).get("data") or []
        self.devices = api("/v1/devices", key).get("data") or []

    @staticmethod
    def _name(host):
        return str((host.get("reportedState") or {}).get("name") or "").strip()

    def host_by_name(self, wanted):
        """Exact name match, then substring, both case-insensitive."""
        if not wanted:
            return None
        w = wanted.strip().lower()
        for host in self.hosts:
            if self._name(host).lower() == w:
                return host
        for host in self.hosts:
            name = self._name(host).lower()
            if name and (w in name or name in w):
                return host
        return None

    def site_for_host(self, host):
        """The site record belonging to a console."""
        if not host:
            return None
        hid = host.get("id")
        for site in self.sites:
            if site.get("hostId") == hid:
                return site
        return None

    def devices_for_host(self, host):
        if not host:
            return []
        hid = host.get("id")
        for group in self.devices:
            if group.get("hostId") == hid:
                return group.get("devices") or []
        return []


def is_gateway(host):
    shortname = str(((host.get("reportedState") or {}).get("hardware") or {})
                    .get("shortname") or "").upper()
    if not shortname or shortname in NOT_GATEWAY:
        return False
    return shortname.startswith(GATEWAY_PREFIXES)


def is_wireless(device):
    if str(device.get("productLine") or "network").lower() != "network":
        return False
    shortname = str(device.get("shortname") or device.get("model") or "").upper()
    if not shortname or device.get("isConsole"):
        return False
    return not shortname.startswith(NON_WIRELESS_PREFIXES)


# --------------------------------------------------------------------------- #
def collect_firewall(site, estate, expected_wans=0):
    """Gateway + WAN uplinks. Returns (status, counts, detail, reason).

    expected_wans is the operator-set number of uplinks this site should have
    carrying traffic; 0 means auto, which expects exactly the ports that are
    plugged in. A gateway reports every physical WAN port whether or not
    anything is in it, and counting empty ports parked Bessemer and Ivory Tower
    on a permanent "1 of 2 WAN uplinks down" that nobody could clear.
    """
    host = estate.host_by_name(site["gateway_host"])
    if host is None:
        return "down", {"wansUp": 0, "wansTotal": 0}, {}, \
            f"no console named {site['gateway_host']!r} on this UniFi account"
    if not is_gateway(host):
        hw = ((host.get("reportedState") or {}).get("hardware") or {}).get("shortname")
        return "down", {"wansUp": 0, "wansTotal": 0}, {}, \
            f"{Estate._name(host)} is a {hw}, not a gateway — it has no WAN uplinks"

    state = host.get("reportedState") or {}
    hardware = state.get("hardware") or {}
    online = str(state.get("state") or "").lower() == "connected"

    wans = []
    for wan in (state.get("wans") or []):
        if not wan.get("enabled", True):
            continue
        # `plugged` is the link state; a WAN with no IPv4 is up but not carrying.
        wans.append({
            "id": wan.get("type") or wan.get("interface") or f"wan{len(wans) + 1}",
            "interface": wan.get("interface") or "",
            "port": wan.get("port"),
            "up": bool(wan.get("plugged")) and bool(wan.get("ipv4")),
            "plugged": bool(wan.get("plugged")),
            "ip": wan.get("ipv4") or "",
            "mac": wan.get("mac") or "",
            "speedType": wan.get("speedType") or "",
        })
    # WAN before WAN2 before WAN3, so the primary reads first.
    wans.sort(key=lambda w: (len(w["id"]), w["id"]))

    up = [w for w in wans if w["up"]]
    plugged = [w for w in wans if w["plugged"]]
    expected = expected_wans if expected_wans > 0 else len(plugged)
    # wansTotal drives the "n/m WAN" label on the card, so it carries the
    # EXPECTED count. The raw port census stays beside it for the drill-in.
    counts = {"wansUp": len(up), "wansTotal": expected,
              "wansPresent": len(wans), "wansPlugged": len(plugged),
              "wansExpectedSource": 1 if expected_wans > 0 else 0}
    detail = {
        "gateway": {
            "name": Estate._name(host) or hardware.get("name") or "Gateway",
            "model": hardware.get("name") or hardware.get("shortname") or "",
            "shortname": hardware.get("shortname") or "",
            "version": state.get("version") or "",
            "ip": host.get("ipAddress") or "",
            "state": "ONLINE" if online else str(state.get("state") or "").upper(),
        },
        "wans": wans,
    }

    if not online:
        return "down", counts, detail, f"gateway {Estate._name(host)} is not connected to UniFi"
    if not wans:
        return "down", counts, detail, "gateway reports no enabled WAN uplinks"
    if expected == 0:
        # Every port is empty. Nothing is configured to carry traffic, so there
        # is nothing to call down; say that rather than invent an outage.
        return "down", counts, detail, "no WAN uplink is plugged in"
    if not up:
        return "down", counts, detail, "all WAN uplinks down"
    if len(up) < expected:
        missing = ", ".join(w["id"] for w in wans if not w["up"]) or "an unplugged port"
        return "degraded", counts, detail, (
            f"{expected - len(up)} of {expected} WAN uplinks down ({missing})")
    return "healthy", counts, detail, None


def collect_wireless(site, estate):
    """Access points + clients. Returns (status, counts, detail, reason)."""
    host = estate.host_by_name(site["wireless_host"])
    if host is None:
        return "down", {"apsOnline": 0, "apsTotal": 0, "clients": 0}, {}, \
            f"no console named {site['wireless_host']!r} on this UniFi account"
    record = estate.site_for_host(host)
    if record is None:
        return "down", {"apsOnline": 0, "apsTotal": 0, "clients": 0}, {}, \
            f"{Estate._name(host)} reports no site statistics"

    stats = record.get("statistics") or {}
    c = stats.get("counts") or {}
    total = int(c.get("wifiDevice") or 0)
    offline = int(c.get("offlineWifiDevice") or 0)
    online_count = max(0, total - offline)
    tx_retry = num((stats.get("percentages") or {}).get("txRetry"))

    # The device list only names the offline APs; the counts above are truth.
    offline_names = sorted(
        d.get("name") or d.get("model") or d.get("mac") or "?"
        for d in estate.devices_for_host(host)
        if is_wireless(d) and str(d.get("status") or "").lower() != "online"
    )
    aps = sorted(
        ({
            "name": d.get("name") or d.get("model") or d.get("mac") or "?",
            "model": d.get("shortname") or d.get("model") or "",
            "ip": d.get("ip") or "",
            "state": str(d.get("status") or "").upper(),
            "version": d.get("version") or "",
            "firmware": d.get("firmwareStatus") or "",
            "since": d.get("startupTime") or "",
        } for d in estate.devices_for_host(host) if is_wireless(d)),
        key=lambda a: a["name"].lower(),
    )

    # Everything adopted to this console, not only the radios. is_wireless()
    # filters switches, CloudKeys, recorders, PDUs and cameras out of the AP
    # list above, but they are already in hand and a switch going dark matters,
    # so the full roster ships alongside it. Radios sort first, then by name.
    devices = sorted(
        ({
            "name": d.get("name") or d.get("model") or d.get("mac") or "?",
            "model": d.get("shortname") or d.get("model") or "",
            "ip": d.get("ip") or "",
            "state": str(d.get("status") or "").upper(),
            "version": d.get("version") or "",
            "firmware": d.get("firmwareStatus") or "",
            "since": d.get("startupTime") or "",
            "mac": d.get("mac") or "",
            "wireless": is_wireless(d),
        } for d in estate.devices_for_host(host)),
        key=lambda d: (not d["wireless"], d["name"].lower()),
    )
    devices_offline = sum(1 for d in devices if d["state"] != "ONLINE")

    counts = {
        "apsOnline": online_count,
        "apsTotal": total,
        "devicesTotal": len(devices),
        "devicesOffline": devices_offline,
        "clients": int(c.get("wifiClient") or 0),
        "guests": int(c.get("guestClient") or 0),
        "wiredClients": int(c.get("wiredClient") or 0),
        "txRetryPct": tx_retry,
    }
    detail = {
        "site": (record.get("meta") or {}).get("desc") or Estate._name(host),
        "controller": Estate._name(host),
        "aps": aps,
        "devices": devices,
        "wlan": {
            "offlineNames": offline_names,
            "totalDevices": int(c.get("totalDevice") or 0),
            "offlineDevices": int(c.get("offlineDevice") or 0),
            "pendingUpdate": int(c.get("pendingUpdateDevice") or 0),
            "ssids": int(c.get("wifiConfiguration") or 0),
        },
    }

    if total == 0:
        return "down", counts, detail, "no access points on this site"
    if online_count == 0:
        return "down", counts, detail, "every access point is offline"
    if offline:
        listed = ", ".join(offline_names[:4]) or f"{offline} AP(s)"
        return "degraded", counts, detail, \
            f"{offline} of {total} access points offline ({listed})"
    if tx_retry is not None and tx_retry > TX_RETRY_DEGRADED_PCT:
        return "degraded", counts, detail, f"wireless tx-retry {tx_retry}%"
    return "healthy", counts, detail, None


# --------------------------------------------------------------------------- #
COLLECTORS = {"firewall": collect_firewall, "wireless": collect_wireless}


def run_row(kind, site, estate, push_token, base, fatal=None, api_ms=0.0,
            discovery=None):
    """Collect and push one row. Returns True if Gatus does not know this key.

    A 404 from the snapshot push is not an error to shout about once per sweep:
    it is how a console that nobody has added to config.yaml announces itself.
    The caller gathers those and prints one paste-ready block at the end.
    """
    key = f"{kind}_{site['slug']}"
    started = time.monotonic()
    if fatal:
        # Contract with LocationCard.vue: this prefix paints the bar BLACK
        # ("nothing reported") instead of red ("reported and failing").
        status, counts, detail, reason = "down", {}, {}, f"no unifi reporting ({fatal})"
    else:
        try:
            if kind == "firewall":
                status, counts, detail, reason = collect_firewall(
                    site, estate, fetch_uplink_settings(base, key))
            else:
                status, counts, detail, reason = collect_wireless(site, estate)
        except (urllib.error.URLError, OSError, ValueError, KeyError) as exc:
            status, counts, detail, reason = "down", {}, {}, f"no unifi reporting ({exc})"
    # Response time = the UniFi API round-trip. One estate read serves every row,
    # so each row is charged that shared cost; the per-row work after it is pure
    # local computation and would otherwise report a meaningless 0ms.
    duration_ms = api_ms + (time.monotonic() - started) * 1000.0

    # 'degraded' is a warning, not a hard failure — only 'down' fails the check,
    # matching how the phones row behaves.
    success = status != "down"
    print(f"{key}: status={status} counts={counts} dur={int(duration_ms)}ms"
          + (f" reason={reason}" if reason else ""))

    # What discovery saw this sweep rides along on every snapshot, so the UI can
    # diff it against the keys it already holds and say "2 consoles found that
    # are not on the dashboard" without another route or another poll.
    if discovery is not None:
        detail = dict(detail)
        detail["discovery"] = discovery

    body = {"kind": kind, "status": status, "site": site["label"],
            "counts": counts, "detail": detail}
    try:
        push(f"{base}/api/v1/unifi/{key}", push_token, body)
    except urllib.error.HTTPError as exc:
        # HTTPError subclasses URLError, so this has to be caught first.
        if exc.code == 404:
            return True
        print(f"WARN: snapshot push failed for {key}: {exc}", file=sys.stderr)
    except (urllib.error.URLError, OSError) as exc:
        print(f"WARN: snapshot push failed for {key}: {exc}", file=sys.stderr)
    q = {"success": "true" if success else "false", "duration": f"{int(duration_ms)}ms"}
    # Send the reason for a DEGRADED result too, not just a failure. Gatus stores
    # it against the passing result, which is how the dashboard can paint "1 of 2
    # WAN uplinks down" amber instead of green without raising a down alert.
    if reason:
        q["error"] = reason
    try:
        push(f"{base}/api/v1/endpoints/{key}/external?{urllib.parse.urlencode(q)}", push_token)
    except urllib.error.HTTPError as exc:
        if exc.code == 404:
            return True
        print(f"WARN: result push failed for {key}: {exc}", file=sys.stderr)
    except (urllib.error.URLError, OSError) as exc:
        print(f"WARN: result push failed for {key}: {exc}", file=sys.stderr)
    return False


def sweep_once():
    api_key = (os.environ.get("UNIFI_API_KEY")
               or os.environ.get("UNIFI_DECATUR_API_KEY") or "").strip()
    push_token = (os.environ.get("UNIFI_PUSH_TOKEN")
                  or os.environ.get("PHONES_PUSH_TOKEN") or "").strip()
    base = os.environ.get("GATUS_PUSH_BASE", "http://localhost:8080")
    if not push_token:
        print("ERROR: neither UNIFI_PUSH_TOKEN nor PHONES_PUSH_TOKEN is set", file=sys.stderr)
        sys.exit(1)
    if not api_key:
        print("ERROR: UNIFI_API_KEY not set", file=sys.stderr)
        sys.exit(1)

    # One estate read serves every row. If the cloud API is unreachable, every
    # row reports "nothing reported" rather than a fake failure.
    estate, fatal = None, None
    api_started = time.monotonic()
    try:
        estate = Estate(api_key)
    except (urllib.error.URLError, OSError, ValueError) as exc:
        fatal = f"UniFi cloud API unreachable: {exc}"
        print(f"ERROR: {fatal}", file=sys.stderr)
    api_ms = (time.monotonic() - api_started) * 1000.0

    if fatal:
        # Discovery needs the API that just failed, so fall back to the rows
        # Gatus already holds. Without this a dead cloud API would leave every
        # row showing stale data instead of going black.
        rows = known_rows(base)
        discovery = None
    else:
        sites = discover_sites(estate)
        rows = [(kind, site) for site in sites for kind in ("firewall", "wireless")
                if site.get("gateway_host" if kind == "firewall" else "wireless_host")]
        discovery = {
            "consoles": [{"label": site["label"], "slug": site["slug"],
                          "firewall": bool(site["gateway_host"]),
                          "wireless": bool(site["wireless_host"])}
                         for site in sites],
            "sweptAt": int(time.time()),
        }
        print(f"discovered {len(sites)} console(s), {len(rows)} row(s): "
              + ", ".join(f"{k}_{s['slug']}" for k, s in rows))

    unregistered = []
    for kind, site in rows:
        try:
            if run_row(kind, site, estate, push_token, base, fatal, api_ms, discovery):
                unregistered.append((kind, site))
        except Exception as exc:  # never let one row kill the sweep
            print(f"ERROR running {kind}_{site['slug']}: {exc}", file=sys.stderr)

    if unregistered:
        print(render_unregistered_yaml(unregistered), file=sys.stderr)


def main():
    load_dotenv()
    if os.environ.get("LOOP") == "1":
        lo = int(os.environ.get("SWEEP_MIN", "30"))
        hi = int(os.environ.get("SWEEP_MAX", "60"))
        while True:
            sweep_once()
            time.sleep(random.uniform(lo, hi))
    else:
        sweep_once()


if __name__ == "__main__":
    main()
