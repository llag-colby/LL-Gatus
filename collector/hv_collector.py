#!/usr/bin/env python3
"""
hv_collector.py - push-based Hyper-V host monitor for LL-Gatus.

Same shape as phone_collector.py, unifi_collector.py and smb_collector.py:
Gatus never calls the hypervisors. This script runs one inventory script per
host over WinRM and pushes a pass/degraded/fail result plus a detailed snapshot
into Gatus.

Why not just ping
-----------------
"Is it up" is the least interesting question about a hypervisor. A host that
answers ping can still be out of memory, have a volume at 99 percent, or be
running six guests in a Critical state. So the row reflects host HEALTH, and
the drill-in carries CPU, memory, every volume and the full guest inventory.

Three stages are reported separately so a red row says which part broke:

    reachable   a plain TCP probe, so liveness never depends on WinRM
    winrm       connect and authenticate over WinRM (5985)
    collect     run the inventory script and parse its JSON

A host that is reachable but whose WinRM is silent reports DEGRADED, not down.
The box is alive; calling that an outage starts the wrong incident. Only an
unreachable host is down. That is the whole reason the reachable stage exists
as its own probe rather than being folded into the WinRM connect.

Status
------
    healthy    reachable, WinRM answered, nothing over a threshold
    degraded   alive but something needs attention (see DEGRADED CHECKS below)
    down       the host did not answer a TCP probe on any of its addresses

degraded is pushed as a PASS carrying its reason, so resource pressure reads
amber on the wall without firing a down alert. Same convention as the other
collectors.

Credentials
-----------
Set a WinRM account in .env. It falls back to the SMB collector's account so a
single service account can cover both:

    HV_USER=longlewis\\svc-gatus      (or SMB_USER)
    HV_PASS=...                       (or SMB_PASS)

Running Get-CimInstance and Get-VM over WinRM needs local Administrators on
each host in practice, or a delegated WinRM session configuration. Without
credentials every row reports "no hv reporting", which the dashboard paints
BLACK rather than red: not configured is not an outage.

If WinRM is not enabled on a host, `Enable-PSRemoting -Force` on that host is
the fix. Until then the row sits at degraded with "WinRM did not answer", which
is accurate and still carries liveness.

Run modes
---------
    python3 hv_collector.py            # one sweep, then exit
    LOOP=1 python3 hv_collector.py     # daemon: sweep every 60-120s (jittered)

Env
---
    HV_WINRM_MODE                      auto (default) | jea | ntlm
    HV_CLIENT_CERT, HV_CLIENT_KEY      client certificate for the hardened path
    HV_SERVER_CA                       CA bundle to verify the hosts (recommended)
    HV_JEA_PORT, HV_JEA_CONFIG         default 5986 and GatusInventory
    HV_USER, HV_PASS                   legacy WinRM account (falls back to SMB_USER/PASS)
    HV_PUSH_TOKEN or PHONES_PUSH_TOKEN push token (matches config.yaml)
    GATUS_PUSH_BASE                    default http://localhost:8080
    HV_CPU_WARN                        degraded above this CPU percent (default 90)
    HV_MEM_WARN                        degraded above this memory percent (default 92)
    HV_DISK_WARN                       degraded at or above this volume percent (default 90)
    HV_TCP_TIMEOUT                     reachability probe timeout (default 3)
    HV_WINRM_TIMEOUT                   WinRM read timeout (default 45)
    HV_WORKERS                         hosts probed in parallel (default: one per host)
    LOOP, SWEEP_MIN, SWEEP_MAX
"""

import base64
import json
import os
import random
import socket
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from concurrent.futures import ThreadPoolExecutor

import winrm
from winrm.protocol import Protocol

# pypsrp speaks the PowerShell remoting protocol, which is what a JEA
# endpoint requires: pywinrm only speaks WinRS, which cannot select a
# session configuration. Imported lazily so a box without it still runs the
# NTLM path.
try:
    from pypsrp.powershell import PowerShell, RunspacePool
    from pypsrp.wsman import WSMan
    HAVE_PSRP = True
except ImportError:  # pragma: no cover - optional until JEA is rolled out
    HAVE_PSRP = False

HTTP_TIMEOUT = 25
GIB = 1024.0 ** 3

# --------------------------------------------------------------------------- #
# The hosts to watch. Generated from the RMM device export and kept here as the
# source of truth; web/app/src/utils/hypervisors.js carries the same list for
# cold-start labels only.
#
# `addresses` is a list because these are multi-homed servers and the inventory
# export is not always current. They are tried in order and the collector
# reports which one answered, so a stale first entry costs one timeout rather
# than a false outage.
#
# ONA-HV1 carries two addresses for exactly that reason: the export lists
# 10.15.102.25 while AD DNS answered 10.15.101.230. The DNS one is listed FIRST
# because it is the one that actually answers; with the export's address first
# every sweep burned 9 seconds timing it out before falling back. If a host's
# probe time is consistently high on the drill-in, its first address is stale
# and reordering it here is the fix.
#
# Keys must match config.yaml: slug(group)_slug(name).
#   group "MS-HV01" + name "Hypervisors" -> "ms-hv01_hypervisors"
HOSTS = [
    {"key": "cu-hv01_hypervisors",     "host": "CU-HV01",   "site": "Cullman",        "addresses": ["10.28.106.231"],
     "model": "OptiPlex 7070",       "serial": "9T5BSZ2",  "ramGiB": 15.79},
    {"key": "fl-hv01_hypervisors",     "host": "FL-HV01",   "site": "Florence",       "addresses": ["10.8.102.172", "10.8.102.197"],
     "model": "PowerEdge R620",      "serial": "G8WWFX1",  "ramGiB": 31.94},
    {"key": "hoov-hv01_hypervisors",   "host": "HOOV-HV01", "site": "Hoover",         "addresses": ["10.16.102.6", "10.16.102.7"],
     "model": "PowerEdge R730xd",    "serial": "H6CLD42",  "ramGiB": 255.91},
    {"key": "hoov-hv02_hypervisors",   "host": "HOOV-HV02", "site": "Hoover",         "addresses": ["10.16.102.122"],
     "model": "PowerEdge T620",      "serial": "H9FW942",  "ramGiB": 223.94},
    {"key": "ms-hv01_hypervisors",     "host": "MS-HV01",   "site": "Muscle Shoals",  "addresses": ["10.6.108.125", "10.6.80.105"],
     "model": "PowerEdge R730xd",    "serial": "5G5FV52",  "ramGiB": 335.9},
    {"key": "ms-hv02_hypervisors",     "host": "MS-HV02",   "site": "Muscle Shoals",  "addresses": ["10.6.80.1", "10.6.80.44"],
     "model": "PowerEdge T630",      "serial": "HX5QJB2",  "ramGiB": 255.91},
    {"key": "ms-hv03_hypervisors",     "host": "MS-HV03",   "site": "Muscle Shoals",  "addresses": ["10.6.80.85", "192.168.3.199"],
     "model": "PowerEdge R750",      "serial": "7TYTQN3",  "ramGiB": 511.46},
    {"key": "ms-hv04_hypervisors",     "host": "MS-HV04",   "site": "Muscle Shoals",  "addresses": ["10.6.108.131", "10.6.80.99"],
     "model": "PowerEdge R740xd",    "serial": "150D7X2",  "ramGiB": 511.38},
    {"key": "ms-hv06_hypervisors",     "host": "MS-HV06",   "site": "Muscle Shoals",  "addresses": ["10.6.108.133", "10.6.80.12"],
     "model": "PowerEdge R750",      "serial": "4B1R314",  "ramGiB": 511.46},
    {"key": "ona-hv1_hypervisors",     "host": "ONA-HV1",   "site": "Ivory Tower",    "addresses": ["10.15.101.230", "10.15.102.25"],
     "model": "PowerEdge R750",      "serial": "6BRVRN3",  "ramGiB": 511.46},
    {"key": "ona-hv2_hypervisors",     "host": "ONA-HV2",   "site": "Ivory Tower",    "addresses": ["10.15.102.161", "192.168.1.58"],
     "model": "PowerEdge R750",      "serial": "6NJRWM3",  "ramGiB": 511.46},
]

# Liveness is proved by any of these answering, not by WinRM specifically. 5985
# is tried first because a host that answers it is one the next stage can use,
# and 135 second because on this estate it is very often the ONLY one that
# answers.
#
# 135 is not optional. It was dropped once on the theory that anything
# answering the RPC endpoint mapper also answers 445, and that was simply
# wrong: FL-HV01, HOOV-HV01/02, MS-HV01/03/04/06 answer 135 and nothing else,
# on four different subnets. Dropping it turned seven live hosts into "host did
# not answer on any address". Measured, not assumed: all 12 of their addresses
# accept 135 while 445, 5985 and 3389 all time out.
#
# The probe budget is still ports x addresses x timeout, so the order matters
# more than the length: a host answering 135 is found on the second try, and
# only a genuinely dead address pays the full four timeouts.
PROBE_PORTS = [5986, 5985, 135, 445, 3389]

# The two that decide whether a transport is even worth attempting.
#   5986  WinRM over HTTPS, which is what the hardened JEA path uses
#   5985  WinRM over HTTP, which is what the legacy password path uses
# 5986 was missing entirely, so the hardened path was never probed for.
WINRM_TLS_PORT = 5986
WINRM_PLAIN_PORT = 5985

# Guest states that are fine. Anything else is called out by name on the row,
# because a guest sitting in Paused or Saved after a failed migration is the
# kind of thing that goes unnoticed for weeks.
VM_OK_STATES = {"Running", "Off"}

NOT_REPORTING = "no hv reporting"

# --------------------------------------------------------------------------- #
# The inventory, as THREE small plain-text scripts.
#
# Why not one script, and why not compressed:
#
#   * pywinrm sends a script as `powershell -EncodedCommand <base64 of UTF-16LE>`.
#     That inflates by about 2.7x against an 8191 character command line, so the
#     full 4.8 KB inventory did not fit and every host answered "The command
#     line is too long".
#   * Packing it as gzip+base64 behind Invoke-Expression DID fit (6664 chars)
#     and is exactly what a malicious loader looks like. AMSI on ONA-HV1 blocked
#     it deterministically, surfacing as an opaque WinRM "Access is denied" at
#     run_command time. Proved by bisection: the same wrapper around a tiny
#     payload runs fine, a same-sized benign script runs fine, only the real
#     base64 blob is refused.
#
# So the work is split instead. Each part is plain readable PowerShell well
# inside the limit, emits a JSON object for the keys it owns, and the parts are
# merged in Python. All three run over ONE reused shell (see collect), which is
# both faster and what stops shells leaking.
#
# Every optional block is individually guarded: one missing feature must never
# cost the whole snapshot.
PS_PART_SYSTEM = r"""
$ErrorActionPreference = 'Stop'
$os = Get-CimInstance Win32_OperatingSystem
$cs = Get-CimInstance Win32_ComputerSystem
$bios = Get-CimInstance Win32_BIOS
$cpus = @(Get-CimInstance Win32_Processor)
$loads = @($cpus | Where-Object { $_.LoadPercentage -ne $null } | ForEach-Object { [double]$_.LoadPercentage })
$tk = [double]$os.TotalVisibleMemorySize
$fk = [double]$os.FreePhysicalMemory
[ordered]@{
  os = [ordered]@{
    caption = $os.Caption
    version = $os.Version
    build = $os.BuildNumber
    installedOn = if ($os.InstallDate) { $os.InstallDate.ToString('o') } else { $null }
    lastBoot = if ($os.LastBootUpTime) { $os.LastBootUpTime.ToString('o') } else { $null }
    uptimeHours = if ($os.LastBootUpTime) { [math]::Round(((Get-Date) - $os.LastBootUpTime).TotalHours, 2) } else { $null }
  }
  host = [ordered]@{
    name = $cs.Name
    domain = $cs.Domain
    manufacturer = $cs.Manufacturer
    model = $cs.Model
    serial = $bios.SerialNumber
    biosVersion = $bios.SMBIOSBIOSVersion
  }
  cpu = [ordered]@{
    name = $cpus[0].Name
    sockets = $cpus.Count
    coresTotal = ($cpus | Measure-Object -Property NumberOfCores -Sum).Sum
    logicalTotal = ($cpus | Measure-Object -Property NumberOfLogicalProcessors -Sum).Sum
    maxClockMHz = $cpus[0].MaxClockSpeed
    loadPercent = if ($loads.Count) { [math]::Round(($loads | Measure-Object -Average).Average, 1) } else { $null }
  }
  memory = [ordered]@{
    totalGB = [math]::Round($tk / 1048576, 2)
    freeGB = [math]::Round($fk / 1048576, 2)
    usedGB = [math]::Round(($tk - $fk) / 1048576, 2)
    usedPct = if ($tk -gt 0) { [math]::Round((($tk - $fk) / $tk) * 100, 1) } else { $null }
  }
} | ConvertTo-Json -Depth 4 -Compress
"""

PS_PART_STORAGE = r"""
$ErrorActionPreference = 'Stop'
$vols = @(Get-CimInstance Win32_LogicalDisk -Filter 'DriveType=3' | ForEach-Object {
  $sz = [double]$_.Size; $fr = [double]$_.FreeSpace
  [ordered]@{
    drive = $_.DeviceID
    label = $_.VolumeName
    fs = $_.FileSystem
    totalGB = [math]::Round($sz / 1073741824, 2)
    freeGB = [math]::Round($fr / 1073741824, 2)
    usedPct = if ($sz -gt 0) { [math]::Round((($sz - $fr) / $sz) * 100, 1) } else { $null }
  }
})
$ad = @()
try {
  $ad = @(Get-NetAdapter -ErrorAction Stop | Where-Object { $_.Status -ne 'Not Present' } |
    Sort-Object Name | ForEach-Object {
      [ordered]@{ name = $_.Name; status = [string]$_.Status; linkSpeed = [string]$_.LinkSpeed; mac = $_.MacAddress }
    })
} catch {}
$errs = $null
try {
  $since = (Get-Date).AddHours(-24)
  $errs = @(Get-WinEvent -FilterHashtable @{LogName='System'; Level=1,2; StartTime=$since} -MaxEvents 200 -ErrorAction Stop).Count
} catch {}
[ordered]@{ volumes = $vols; adapters = $ad; errors24h = $errs } | ConvertTo-Json -Depth 5 -Compress
"""

PS_PART_HYPERV = r"""
$ErrorActionPreference = 'Stop'
$hv = [ordered]@{ present = $false }
$vms = @()
try {
  $all = @(Get-VM)
  $hv.present = $true
  try {
    $h = Get-VMHost
    $hv.virtualHardDiskPath = $h.VirtualHardDiskPath
    $hv.virtualMachinePath = $h.VirtualMachinePath
    $hv.logicalProcessors = $h.LogicalProcessorCount
    $hv.memoryCapacityGB = [math]::Round($h.MemoryCapacity / 1073741824, 2)
  } catch {}
  $vms = @($all | Sort-Object Name | ForEach-Object {
    [ordered]@{
      name = $_.Name
      state = [string]$_.State
      status = [string]$_.Status
      cpuPercent = $_.CPUUsage
      memoryGB = if ($_.MemoryAssigned) { [math]::Round($_.MemoryAssigned / 1073741824, 2) } else { 0 }
      memoryDemandGB = if ($_.MemoryDemand) { [math]::Round($_.MemoryDemand / 1073741824, 2) } else { $null }
      vcpu = $_.ProcessorCount
      uptimeHours = if ($_.Uptime) { [math]::Round($_.Uptime.TotalHours, 2) } else { 0 }
      generation = $_.Generation
      heartbeat = [string]$_.Heartbeat
      replication = [string]$_.ReplicationHealth
    }
  })
} catch { $hv.error = $_.Exception.Message }
$cl = $null
try { $cl = [ordered]@{ name = (Get-Cluster -ErrorAction Stop).Name } } catch {}
[ordered]@{ hyperv = $hv; vms = $vms; cluster = $cl } | ConvertTo-Json -Depth 5 -Compress
"""

PS_PARTS = [("system", PS_PART_SYSTEM), ("storage", PS_PART_STORAGE), ("hyperv", PS_PART_HYPERV)]

# Hard ceiling for one `powershell -EncodedCommand` line. 8191 is the Windows
# command line limit; the prefix and a safety margin come off that.
MAX_ENCODED = 7600


def push(url, token, body=None):
    headers = {"Authorization": f"Bearer {token}"}
    data = None
    if body is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(body).encode()
    req = urllib.request.Request(url, headers=headers, method="POST", data=data)
    with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT) as resp:
        return resp.getcode()


def tcp_open(address, port, timeout):
    sock = socket.socket()
    sock.settimeout(timeout)
    try:
        sock.connect((address, port))
        return True
    except OSError:
        return False
    finally:
        sock.close()


def find_live_address(entry, timeout):
    """First address that answers, the port that answered, and which WinRM
    ports are open on it.

    Returns (address, port, winrm_ports) or (None, None, ()). Addresses are
    tried in order so the inventory's primary stays primary.

    Knowing the open WinRM ports is what stops the collector waiting on a
    transport that cannot work. A host with WinRM disabled used to cost a full
    WinRM timeout on the hardened path and then ANOTHER on the legacy path,
    about 103 seconds, which was then reported as the row's latency and also
    made a sweep outrun its own interval. Now an unopened port is skipped.
    """
    for address in entry["addresses"]:
        answered = None
        winrm_ports = []
        for port in PROBE_PORTS:
            if tcp_open(address, port, timeout):
                if answered is None:
                    answered = port
                if port in (WINRM_TLS_PORT, WINRM_PLAIN_PORT):
                    winrm_ports.append(port)
                elif answered is not None and not winrm_ports and port not in (
                        WINRM_TLS_PORT, WINRM_PLAIN_PORT):
                    # Liveness is settled and neither WinRM port is open; the
                    # remaining ports add nothing but time.
                    break
        if answered is not None:
            return address, answered, tuple(winrm_ports)
    return None, None, ()


def _collect_any(address, user, password, cfg, winrm_ports=None):
    """Inventory by the most hardened transport whose PORT IS ACTUALLY OPEN.

    Returns (document, description-of-how).

    Only attempting a transport whose port answered is the whole point. The
    previous version tried the hardened path and then the legacy path on every
    host regardless, so a host with WinRM switched off spent two full timeouts,
    around 103 seconds, before reporting a result that said nothing a 3 second
    probe had not already established.

    HV_WINRM_MODE controls which transports are eligible:
      jea    hardened only. A host that is not hardened yet reports the error.
      ntlm   legacy only.
      auto   prefer JEA, fall back to the legacy path (default). That is what
             makes a host-by-host rollout possible.
    """
    mode = (os.environ.get("HV_WINRM_MODE") or "auto").strip().lower()
    jea = cfg.get("jea")
    timeout = cfg["winrm_timeout"]
    # None means the caller did not probe, so attempt everything rather than
    # silently refusing to collect. An EMPTY TUPLE is different: it means the
    # probe ran and found no WinRM port, which is the whole case this exists to
    # short-circuit. Conflating the two is how a 135-only host still paid two
    # full timeouts.
    probed = winrm_ports is not None
    tls_open = (not probed) or (WINRM_TLS_PORT in winrm_ports)
    plain_open = (not probed) or (WINRM_PLAIN_PORT in winrm_ports)

    tried = []
    if mode in ("jea", "auto") and jea and tls_open:
        try:
            doc = collect_jea(address, jea, timeout)
            return doc, "JEA over TLS, client certificate" + (
                ", host verified" if jea["validation"] else ", host NOT verified")
        except Exception:  # noqa: BLE001 - fall through or re-raise below
            if mode == "jea":
                raise
            tried.append("JEA")

    if mode == "jea":
        if not jea:
            raise RuntimeError("HV_WINRM_MODE=jea but HV_CLIENT_CERT/HV_CLIENT_KEY are not set")
        raise RuntimeError("WinRM over TLS (5986) is not listening, so the hardened "
                           "path cannot be used. Run the setup script on this host.")

    if mode in ("ntlm", "auto") and plain_open:
        if not user or not password:
            raise RuntimeError("no client certificate accepted and no HV_USER/HV_PASS")
        doc = collect_ntlm(address, user, password, timeout)
        return doc, "WinRS over HTTP, password (not hardened)"

    # Nothing to try. Say which port is missing rather than timing out to
    # discover it.
    want = []
    if jea:
        want.append("5986 for the hardened path")
    if user and password:
        want.append("5985 for the legacy path")
    detail = " or ".join(want) if want else "5985/5986"
    raise RuntimeError("WinRM is not listening (needs %s). Run the setup script "
                       "on this host, or Enable-PSRemoting for the legacy path." % detail
                       + (" Tried: %s." % ", ".join(tried) if tried else ""))


def _jea_settings():
    """Certificate + JEA configuration from the environment, or None.

    Returns None when the client certificate is not configured, which is the
    state before winrm-harden.ps1 has been run anywhere.
    """
    cert = (os.environ.get("HV_CLIENT_CERT") or "").strip()
    key = (os.environ.get("HV_CLIENT_KEY") or "").strip()
    if not cert or not key:
        return None
    if not os.path.exists(cert) or not os.path.exists(key):
        print("WARN: HV_CLIENT_CERT/HV_CLIENT_KEY are set but the files are missing: "
              "%s %s" % (cert, key), file=sys.stderr)
        return None
    ca = (os.environ.get("HV_SERVER_CA") or "").strip()
    # cert_validation takes a CA bundle path to verify the host, or False to
    # skip it. Skipping leaves the channel encrypted and still proves the
    # COLLECTOR's identity, but not the host's. winrm-make-host-cert.sh exists
    # to make the first branch possible.
    if ca and os.path.exists(ca):
        validation = ca
    else:
        if ca:
            print("WARN: HV_SERVER_CA set but not found: %s" % ca, file=sys.stderr)
        validation = False
    return {
        "cert": cert,
        "key": key,
        "validation": validation,
        "port": int(os.environ.get("HV_JEA_PORT", "5986")),
        "config": os.environ.get("HV_JEA_CONFIG", "GatusInventory"),
    }


def collect_jea(address, jea, timeout):
    """Inventory over the hardened path: TLS, client certificate, JEA endpoint.

    The endpoint exposes exactly one function, so the whole request is the
    string "Get-GatusInventory". That is the point: no script is shipped, so
    neither the WinRM command line limit nor AMSI's script heuristics apply, and
    the credential cannot be used to run anything else.
    """
    if not HAVE_PSRP:
        raise RuntimeError("pypsrp is not installed, so the JEA path is unavailable")
    wsman = WSMan(
        address,
        port=jea["port"],
        ssl=True,
        auth="certificate",
        certificate_pem=jea["cert"],
        certificate_key_pem=jea["key"],
        cert_validation=jea["validation"],
        connection_timeout=timeout,
        operation_timeout=timeout,
        read_timeout=timeout + 10,
    )
    with RunspacePool(wsman, configuration_name=jea["config"]) as pool:
        powershell = PowerShell(pool)
        powershell.add_cmdlet("Get-GatusInventory")
        output = powershell.invoke()
        if powershell.had_errors:
            streams = getattr(powershell.streams, "error", None) or []
            detail = str(streams[0]) if streams else "no detail"
            raise RuntimeError("Get-GatusInventory failed: %s" % detail[:250])
    text = "".join(str(item) for item in output).strip()
    if not text:
        raise RuntimeError("Get-GatusInventory returned nothing")
    return json.loads(text)


def _encode(script):
    return base64.b64encode(script.encode("utf-16-le")).decode("ascii")


def _run_part(protocol, shell_id, script):
    """Run one part in an existing shell and return its stdout."""
    command_id = protocol.run_command(
        shell_id, "powershell -NoProfile -NonInteractive -EncodedCommand " + _encode(script))
    try:
        stdout, stderr, code = protocol.get_command_output(shell_id, command_id)
    finally:
        # Always reap the command, or the shell accumulates them.
        try:
            protocol.cleanup_command(shell_id, command_id)
        except Exception:  # noqa: BLE001 - cleanup must not mask the real error
            pass
    if code != 0:
        text = (stderr or b"").decode("utf-8", "replace")
        # WinRM wraps PowerShell errors in CLIXML, unreadable in a status row.
        for line in text.splitlines():
            line = line.strip()
            if line and not line.startswith("#<") and "<Objs" not in line:
                raise RuntimeError(line[:300])
        raise RuntimeError("inventory part exited %d" % code)
    return (stdout or b"").decode("utf-8", "replace").strip()


def collect_ntlm(address, user, password, timeout):
    """Legacy path: ship three scripts over WinRS with a password.

    Kept so a host that has not been hardened yet still reports. It needs an
    account with real privileges on the host, which is exactly what the JEA
    path removes the need for, so treat this as the transitional option.

    Runs every inventory part over ONE shell and returns the merged document.

    The shell is opened once and closed in a finally. pywinrm's convenience
    Session.run_ps opens a shell per call and leaks it whenever the call raises,
    which left 22 orphaned shells on ONA-HV1 against a two hour idle timeout.
    Those failures are the normal case here (a host with WinRM off fails every
    sweep), so the leak was unbounded.
    """
    protocol = Protocol(
        endpoint="http://%s:5985/wsman" % address,
        transport="ntlm",
        username=user,
        password=password,
        read_timeout_sec=timeout + 10,
        operation_timeout_sec=timeout,
    )
    shell_id = protocol.open_shell()
    try:
        doc = {}
        for name, script in PS_PARTS:
            try:
                doc.update(json.loads(_run_part(protocol, shell_id, script)))
            except ValueError as exc:
                raise RuntimeError("inventory part %s returned unparsable JSON: %s" % (name, exc))
        return doc
    finally:
        try:
            protocol.close_shell(shell_id)
        except Exception:  # noqa: BLE001 - a failed close must not hide the result
            pass


def evaluate(doc, thresholds):
    """Turn an inventory document into (counts, reasons).

    counts is numeric only, because api.recordCounts charts every number it is
    given and silently drops everything else.
    """
    counts = {}
    reasons = []

    cpu = doc.get("cpu") or {}
    mem = doc.get("memory") or {}
    osd = doc.get("os") or {}
    vols = doc.get("volumes") or []
    vms = doc.get("vms") or []

    if cpu.get("loadPercent") is not None:
        counts["cpuPct"] = float(cpu["loadPercent"])
        if counts["cpuPct"] >= thresholds["cpu"]:
            reasons.append(f"CPU at {counts['cpuPct']:.0f}%")
    if mem.get("usedPct") is not None:
        counts["memUsedPct"] = float(mem["usedPct"])
        if counts["memUsedPct"] >= thresholds["mem"]:
            reasons.append(f"memory at {counts['memUsedPct']:.0f}%")
    for name, source in (("memUsedGB", "usedGB"), ("memTotalGB", "totalGB"), ("memFreeGB", "freeGB")):
        if mem.get(source) is not None:
            counts[name] = float(mem[source])
    if osd.get("uptimeHours") is not None:
        counts["uptimeHours"] = float(osd["uptimeHours"])

    worst = None
    for vol in vols:
        pct = vol.get("usedPct")
        if pct is None:
            continue
        pct = float(pct)
        if worst is None or pct > worst:
            worst = pct
        if pct >= thresholds["disk"]:
            free = vol.get("freeGB")
            reasons.append(f"{vol.get('drive', '?')} at {pct:.0f}%"
                           + (f" ({free:.0f} GB free)" if isinstance(free, (int, float)) else ""))
    if worst is not None:
        counts["volMaxUsedPct"] = worst
    counts["volumes"] = float(len(vols))

    if vms:
        running = [v for v in vms if v.get("state") == "Running"]
        bad = [v for v in vms if v.get("state") not in VM_OK_STATES]
        critical = [v for v in vms if "critical" in str(v.get("status", "")).lower()]
        counts["vmsTotal"] = float(len(vms))
        counts["vmsRunning"] = float(len(running))
        counts["vmsBad"] = float(len({v["name"] for v in bad + critical}))
        if bad:
            listed = ", ".join(f"{v['name']} ({v.get('state')})" for v in bad[:4])
            reasons.append(f"{len(bad)} guest(s) not running or off: {listed}")
        if critical:
            listed = ", ".join(v["name"] for v in critical[:4])
            reasons.append(f"{len(critical)} guest(s) in a critical state: {listed}")
        assigned = sum(float(v.get("memoryGB") or 0) for v in running)
        counts["vmsAssignedGB"] = round(assigned, 2)
    elif (doc.get("hyperv") or {}).get("present"):
        counts["vmsTotal"] = 0.0
        counts["vmsRunning"] = 0.0

    if not (doc.get("hyperv") or {}).get("present"):
        reasons.append("the Hyper-V role is not responding on this host")

    if doc.get("errors24h") is not None:
        counts["errors24h"] = float(doc["errors24h"])

    return counts, reasons


def check_host(entry, user, password, cfg):
    """Probe and collect one host. Returns (status, reason, counts, detail)."""
    steps = []
    counts = {}

    started = time.monotonic()
    address, port, winrm_ports = find_live_address(entry, cfg["tcp_timeout"])
    probe_ms = (time.monotonic() - started) * 1000.0
    if not address:
        tried = ", ".join(entry["addresses"])
        steps.append({"name": "reachable", "ok": False, "ms": round(probe_ms, 1),
                      "error": f"no answer on {tried}"})
        return "down", f"host did not answer on any address ({tried})", counts, {"steps": steps}
    steps.append({"name": "reachable", "ok": True, "ms": round(probe_ms, 1),
                  "detail": f"{address}:{port}"})
    counts["probeMs"] = round(probe_ms, 1)

    if not cfg.get("jea") and (not user or not password):
        steps.append({"name": "winrm", "ok": False, "ms": 0.0,
                      "error": "no client certificate and no HV_USER/HV_PASS"})
        return ("degraded", f"{NOT_REPORTING}: no credentials configured",
                counts, {"steps": steps, "address": address})

    started = time.monotonic()
    try:
        doc, how = _collect_any(address, user, password, cfg, winrm_ports)
    except Exception as exc:  # noqa: BLE001 - every failure is a reportable result
        winrm_ms = (time.monotonic() - started) * 1000.0
        # pywinrm appends an extended fault dict to its message, which is pages
        # long and unreadable in a status row. Keep the first line only.
        text = (str(exc).strip() or exc.__class__.__name__).split("(extended fault data")[0].strip()
        lowered = text.lower()
        if "401" in lowered or "unauthorized" in lowered or "rejected by the server" in lowered:
            friendly = "WinRM rejected the credentials"
        elif "access is denied" in lowered or "2147942405" in lowered or "0x80070005" in lowered:
            # Authenticated, then refused a shell. This is the WinRM permission
            # set, not the password: the account is not in Remote Management
            # Users and has no explicit grant in the WinRM SDDL. Seen on
            # ONA-HV2, which authenticates fine and then denies the session.
            friendly = ("WinRM authenticated but denied this account a session "
                        "(add it to Remote Management Users on the host)")
        elif "authentication" in lowered:
            friendly = "WinRM rejected the credentials"
        elif "timed out" in lowered or "timeout" in lowered:
            friendly = "WinRM did not answer in time"
        elif "command line is too long" in lowered:
            friendly = ("the inventory script did not fit in a WinRM command line "
                        "(this is a collector bug, not a host problem)")
        elif "connection" in lowered or "refused" in lowered or "unreachable" in lowered:
            friendly = "WinRM is not listening (run Enable-PSRemoting on the host)"
        else:
            friendly = f"WinRM error: {text[:200]}"
        steps.append({"name": "winrm", "ok": False, "ms": round(winrm_ms, 1), "error": friendly})
        # Reachable but unreadable. The host is alive, so this is a warning on a
        # pass rather than an outage.
        #
        # noWinrmDuration tells report() not to bill this wait as the row's
        # latency. The dashboard's trailing number means "how fast did this
        # answer", and a 45 second timeout is not that: it is how long we
        # waited to learn nothing. The probe time is the real measurement.
        return ("degraded", friendly, counts,
                {"steps": steps, "address": address, "noWinrmDuration": True})

    winrm_ms = (time.monotonic() - started) * 1000.0
    steps.append({"name": "winrm", "ok": True, "ms": round(winrm_ms, 1), "detail": how})
    steps.append({"name": "collect", "ok": True, "ms": 0.0})
    counts["winrmMs"] = round(winrm_ms, 1)

    collected, reasons = evaluate(doc, cfg["thresholds"])
    counts.update(collected)

    detail = dict(doc)
    detail["steps"] = steps
    detail["address"] = address
    detail["inventory"] = {"model": entry["model"], "serial": entry["serial"],
                           "ramGiB": entry["ramGiB"], "site": entry["site"]}

    if reasons:
        return "degraded", "; ".join(reasons), counts, detail
    return "healthy", "", counts, detail


def report(base, token, entry, status, reason, counts, detail):
    key = entry["key"]
    # Keep the colon-safe helper habit from smb_collector: these keys have no
    # colon, but the routes that validate against config.yaml read the raw path
    # param, so nothing here should be over-encoded either.
    safe_key = urllib.parse.quote(key, safe=":")
    success = status != "down"

    body = {"status": status, "host": entry["host"], "site": entry["site"],
            "address": detail.get("address", ""), "counts": counts, "detail": detail}
    try:
        push(f"{base}/api/v1/hv/{safe_key}", token, body)
    except urllib.error.HTTPError as exc:
        if exc.code == 404:
            print(f"WARN: {key} has no external-endpoint in config.yaml", file=sys.stderr)
            return
        print(f"WARN: snapshot push failed for {key}: {exc}", file=sys.stderr)
    except (urllib.error.URLError, OSError) as exc:
        print(f"WARN: snapshot push failed for {key}: {exc}", file=sys.stderr)

    # Latency on the row should be a measurement, not a timeout. When the
    # inventory could not be read, bill only the reachability probe: that is the
    # one number that actually describes the host. Billing the WinRM wait put
    # 103000ms on rows whose hosts answer a TCP probe in 3ms.
    steps = detail.get("steps", [])
    if detail.get("noWinrmDuration"):
        duration = sum(s.get("ms", 0) for s in steps if s.get("name") == "reachable")
    else:
        duration = sum(s.get("ms", 0) for s in steps)
    q = {"success": "true" if success else "false", "duration": f"{int(duration)}ms"}
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


def sweep_once():
    user = (os.environ.get("HV_USER") or os.environ.get("SMB_USER") or "").strip()
    password = os.environ.get("HV_PASS") or os.environ.get("SMB_PASS") or ""
    token = (os.environ.get("HV_PUSH_TOKEN")
             or os.environ.get("PHONES_PUSH_TOKEN") or "").strip()
    base = os.environ.get("GATUS_PUSH_BASE", "http://localhost:8080")
    cfg = {
        "tcp_timeout": float(os.environ.get("HV_TCP_TIMEOUT", "3")),
        "winrm_timeout": int(os.environ.get("HV_WINRM_TIMEOUT", "45")),
        "thresholds": {
            "cpu": float(os.environ.get("HV_CPU_WARN", "90")),
            "mem": float(os.environ.get("HV_MEM_WARN", "92")),
            "disk": float(os.environ.get("HV_DISK_WARN", "90")),
        },
    }
    # One worker per host: this is entirely I/O bound, so the thread count costs
    # nothing and a sweep then takes as long as the SLOWEST host rather than the
    # sum of a few batches. With every host unreachable that is about 18 seconds
    # instead of over a minute.
    cfg["jea"] = _jea_settings()
    workers = int(os.environ.get("HV_WORKERS", str(max(4, len(HOSTS)))))

    if not token:
        print("ERROR: neither HV_PUSH_TOKEN nor PHONES_PUSH_TOKEN is set", file=sys.stderr)
        sys.exit(1)
    mode = (os.environ.get("HV_WINRM_MODE") or "auto").strip().lower()
    if cfg["jea"]:
        print("JEA transport available: cert=%s host-verification=%s config=%s mode=%s"
              % (cfg["jea"]["cert"],
                 "on" if cfg["jea"]["validation"] else "OFF",
                 cfg["jea"]["config"], mode))
    elif mode == "jea":
        print("ERROR: HV_WINRM_MODE=jea but no client certificate is configured",
              file=sys.stderr)
    if not cfg["jea"] and (not user or not password):
        print("WARN: no client certificate and no HV_USER/HV_PASS. Hosts will be "
              "probed for liveness only and report degraded with 'no hv reporting'.",
              file=sys.stderr)

    # Eleven hosts with a 4s probe timeout per address is up to a minute walked
    # serially, which is longer than the sweep interval. They are independent, so
    # they run in parallel and one dead host no longer delays the other ten.
    def one(entry):
        try:
            status, reason, counts, detail = check_host(entry, user, password, cfg)
        except Exception as exc:  # noqa: BLE001 - a daemon must not die on one host
            status, reason, counts = "down", f"collector error: {exc}", {}
            detail = {"steps": []}
        print(f"{entry['key']}: status={status} cpu={counts.get('cpuPct', '?')}% "
              f"mem={counts.get('memUsedPct', '?')}% vms={counts.get('vmsRunning', '?')}"
              f"/{counts.get('vmsTotal', '?')}" + (f" reason={reason}" if reason else ""))
        report(base, token, entry, status, reason, counts, detail)

    with ThreadPoolExecutor(max_workers=workers) as pool:
        list(pool.map(one, HOSTS))


def main():
    if os.environ.get("LOOP") != "1":
        sweep_once()
        return
    low = int(os.environ.get("SWEEP_MIN", "60"))
    high = int(os.environ.get("SWEEP_MAX", "120"))
    while True:
        try:
            sweep_once()
        except Exception as exc:  # noqa: BLE001 - a daemon must not die on one bad sweep
            print(f"ERROR: sweep failed: {exc}", file=sys.stderr)
        time.sleep(random.uniform(low, high))


if __name__ == "__main__":
    main()
