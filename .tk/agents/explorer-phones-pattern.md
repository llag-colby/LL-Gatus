# Explorer: phone_collector.py pattern study

Target: mirror the phones collector pattern for UniFi devices.

## HEADLINE FINDING — the learned baseline does not exist any more

`collector/.phones_state.json` is a **dead artifact**. The current
`collector/phone_collector.py` contains **zero** code that reads or writes it.
The only surviving references are stale prose in the module docstring and the
README. Do not mirror it — it was deliberately replaced by server-side
thresholds.

Evidence:

- Grep for `state|baseline|high.water|0.9|90%` over `collector/phone_collector.py`
  returns only three lines, all comments/docstring: `:18`, `:19`, `:30`.
- No `state_path()`, `load_state()`, `save_state()`, no `import math`,
  no `THRESHOLD` constant in the current file.
- File mtimes: `collector/.phones_state.json` last written `2026-07-10 13:23`;
  `collector/phone_collector.py` last committed `2026-08-17 14:11`. The state
  file has not been touched in over a month of active collector development.
- Its content is a single stale location: `{"phones_ivory-tower": {"baseline": 65, "last": 65}}`
  — the other ten locations in `LOCATIONS` never got an entry.
- `.gitignore:36` still ignores it; `.tk/agents/explorer-6-config-deploy.md:126`
  still documents `PHONES_STATE_FILE` as a live env var. Both stale.

### What the baseline WAS (recovered from git, commit `ea9c244`)

Superseded; recorded so the design intent is on the record.

```python
def state_path():
    return os.environ.get("PHONES_STATE_FILE",
                          os.path.join(os.path.dirname(os.path.abspath(__file__)), ".phones_state.json"))

def load_state():
    try:
        with open(state_path(), "r", encoding="utf-8") as fh:
            return json.load(fh)
    except (OSError, ValueError):     # missing file OR corrupt JSON -> {}
        return {}

def save_state(state):
    try:
        with open(state_path(), "w", encoding="utf-8") as fh:
            json.dump(state, fh)
    except OSError:                   # unwritable -> silently continue
        pass
```

Learn / update / drift-detect, inside `run_location`:

```python
    success = False
    if error is None:
        baseline = max(int(state.get(key, {}).get("baseline", 0)), online)
        state[key] = {"baseline": baseline, "last": online}
        if baseline == 0:
            success = True                       # first run: nothing learned yet, pass
        else:
            need = math.ceil(THRESHOLD * baseline)
            success = online >= need
            if not success:
                error = f"{online}/{baseline} desk phones online (need >={need})"
```

Lifecycle:

- **Shape** — one dict keyed by endpoint key, value `{"baseline": int, "last": int}`.
- **Learned on first sight** — `max(stored, online)`; a high-water mark only.
- **Updated over time** — ratchets up, never down. A site that grows to 70 phones
  keeps 70 forever; there is no decay, no rolling window, no manual reset.
- **Drift detected** — `online < ceil(0.90 * baseline)` is a failure.
- **First run, no state file** — `load_state()` returns `{}`, `baseline` computes
  to `online`, `need == online`, so the first sweep always passes. There is no
  warm-up or confidence gate.
- **Corruption / missing** — both collapse into the same `except (OSError, ValueError)`
  returning `{}`, i.e. silently re-learn from scratch. Write failures are
  swallowed by `except OSError: pass`.
- **Load/save cadence** — loaded once per sweep in `sweep_once`, passed down by
  reference into every `run_location`, saved once after the whole loop.

### Why it was replaced, and what to build instead

The ratchet was unfixable: a high-water mark that only goes up means one busy
afternoon permanently raises the bar, and a site that legitimately shrinks
alarms forever with no way to reset short of deleting a gitignored file on the
docker host. It was replaced by **operator-set thresholds persisted server-side
and polled by the collector each sweep** (`fetch_thresholds`, below). That is the
pattern to mirror for UniFi — not the baseline file.

Note that `collector/unifi_collector.py` **already exists** (455 lines, wired into
`docker-compose.yml:45-61`) and already implements the three-state model and the
inventory push. What it lacks versus phones is the settings/exclusions/sweep
feedback loop — there are no `/api/v1/unifi/.../settings`, `/exclusions` or
`sweep-pending` routes. That is the actual gap.

---

## 1. Three-state health model

`collector/phone_collector.py:272-300`:

```python
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
```

Ordering matters: unreachable, then empty-inventory, then all-offline, then the
two numeric thresholds. The empty-inventory branch is the one that is easy to
omit and produces a green card with nothing behind it.

Mapping status to the push, `:363-383`:

```python
    # 'degraded' is NOT a hard failure (no red alarm); only 'down' fails the check.
    success = status != "down"
    reason = None
    if status == "down":
        if error:
            reason = error
        elif not phones:
            detail = "PBX reachable, 0 desk phones registered"
            ...
            reason = f"no phones reporting ({detail})"
        else:
            reason = "all monitored phones offline"
    elif status == "degraded":
        reason = f"{counts['offline']} of {counts['monitored']} desk phones offline"
```

`success = status != "down"` is the whole trick: **degraded is a pass that
carries its reason.**

## 2. push_result — how amber is encoded

`collector/phone_collector.py:309-317`:

```python
def push_result(base, key, success, error, duration_ms, push_token):
    from urllib.parse import urlencode
    q = {"success": "true" if success else "false", "duration": f"{int(duration_ms)}ms"}
    # Send the reason for a DEGRADED result too ("3 of 44 desk phones offline"),
    # not just a failure: Gatus stores it against the passing result so the
    # dashboard can paint it amber rather than a bare green.
    if error:
        q["error"] = error
    http(f"{base}/api/v1/endpoints/{key}/external?{urlencode(q)}", push_token, method="POST")
```

Encoding rules, all three enforced server-side:

- `success` must be the literal string `"true"` or `"false"` — validated at
  `api/external_endpoint.go:22-25`, anything else is a 400.
- `duration` must carry a **unit** — parsed with Go `time.ParseDuration` at
  `api/external_endpoint.go:61-68`. A bare number is a 400. Hence `f"{int(ms)}ms"`.
- `error` is appended **regardless of `success`** —
  `api/external_endpoint.go:73-79`:

```go
// Record the pushed reason even on a SUCCESSFUL result. ... A
// successful result carrying errors now means "passed, with a warning", which
// the UI renders amber. Alerting keys off Success alone, so this does not turn
// warnings into alerts.
if errorFromQuery := c.Query("error"); len(errorFromQuery) > 0 {
    result.AddError(errorFromQuery)
}
```

Alerting reads `result.Success` only, so a degraded push never fires a down
alert. This is the documented divergence from upstream Gatus.

Frontend consumption, `web/app/src/components/LocationCard.vue:212-217`:

```js
// A collector that reports three states (healthy / degraded / down) pushes
// degraded as a PASS carrying its reason, so a partial problem doesn't fire a
// down alert. A successful result with errors therefore means "passed, with a
// warning" — 1 of 2 WAN uplinks down, 4 of 23 APs offline — and reads AMBER.
// Painting it green would hide exactly the problems this dashboard exists for.
const isWarning = (result) => !!result && result.success && (result.errors || []).length > 0
```

Cell token selection, `LocationCard.vue:219-227`:

```js
    if (result.success) return { token: isWarning(result) ? 'amber' : 'green', result }
    return { token: isNotReporting(result) ? 'nodata' : 'red', result }
```

## 3. Inventory push

`collector/phone_collector.py:304-306`:

```python
def push_inventory(base, key, phones, status, counts, push_token):
    body = json.dumps({"phones": phones, "status": status, "counts": counts})
    http(f"{base}/api/v1/phones/{key}", push_token, method="POST", body=body)
```

Per-phone object, built at `collector/phone_collector.py:231-245` — eleven fields,
every one always present (empty string rather than omitted):

```python
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
```

`counts` is produced by `evaluate_health` (quoted above):
`total`, `monitored`, `online`, `offline`, `excluded`.

Always push, even when empty — `collector/phone_collector.py:388-394`:

```python
    try:
        # Always push inventory — even an empty list — so the drill-in can show
        # "PBX healthy, 0 phones registered" instead of a misleading "collector
        # hasn't reported" placeholder.
        push_inventory(base, key, phones, status, counts, push_token)
    except (urllib.error.URLError, OSError) as exc:
        print(f"WARN: inventory push failed for {key}: {exc}", file=sys.stderr)
```

Server side, `api/phones_inventory.go:60-67` — the per-phone shape is **never
modelled in Go**, it is stored and re-served verbatim:

```go
var payload struct {
    Phones json.RawMessage `json:"phones"`
    Status string          `json:"status"`
    Counts json.RawMessage `json:"counts"`
}
if err := json.Unmarshal(c.Body(), &payload); err != nil || len(payload.Phones) == 0 {
    return c.Status(400).SendString(`invalid body: expected {"phones": [...]}`)
}
```

Stored shape adds a server timestamp, `api/phones_inventory.go:34-39`:

```go
type storedInventory struct {
	UpdatedAt string          `json:"updatedAt"`
	Status    string          `json:"status,omitempty"`
	Counts    json.RawMessage `json:"counts,omitempty"`
	Phones    json.RawMessage `json:"phones"`
}
```

Storage is an **in-memory map** (`phonesInventoryStore`, `api/phones_inventory.go:23-32`),
wiped on restart; the drill-in 404s until the next sweep. Only the numeric
`counts` are persisted, to a sqlite sidecar via `recordCounts` →
`history.Record` (`api/history.go:27-42`, DB at `/data/history.db`).

Paused endpoints: the push is accepted with **200** and silently dropped
(`api/phones_inventory.go:72-74`) — the collector cannot tell, by design.

UniFi's existing equivalent, `collector/unifi_collector.py:387-392`, uses a
different but parallel body — note `detail` instead of a flat device array:

```python
    body = {"kind": kind, "status": status, "site": site["label"],
            "counts": counts, "detail": detail}
    try:
        push(f"{base}/api/v1/unifi/{key}", push_token, body)
```

## 4. Exclusions and settings feedback loop

Both are **pull, unauthenticated, best-effort, once per sweep per location**.

`collector/phone_collector.py:249-269`:

```python
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
```

Both call `http(url)` with **no token** — `http()` only sets `Authorization`
when one is passed (`:153-156`). Confirmed unauthenticated server-side: the GET
routes carry neither a bearer check nor `requireOperator` (`api/api.go:172,174`).

The server resolves global-vs-override and hands back a precomputed `effective`
block, so the collector never does the merge. Settings persist to
`/data/phones_settings.json`; exclusions to `/data/phones_exclusions.json`
(`api/phones_inventory.go:32`, `api/phones_settings.go:29`).

Threshold shape, `api/phones_settings.go:17-20,31-34`:

```go
type phoneThresholds struct {
	DegradedAt int `json:"degradedAt"`
	DownAt     int `json:"downAt"`
}
type phonesSettingsFile struct {
	Global    phoneThresholds            `json:"global"`
	Overrides map[string]phoneThresholds `json:"overrides"`
}
```

### UI writes (operator-gated)

`web/app/src/views/PhoneDetails.vue:499-504` — exclusion toggle, optimistic with
revert on failure:

```js
    const res = await fetch(`/api/v1/phones/${route.params.key}/exclusions`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ext: p.ext, excluded: next }),
    })
```

`PhoneDetails.vue:539-542` — threshold save, scope-aware:

```js
      body: JSON.stringify({ scope: scope.value, degradedAt: form.value.degradedAt, downAt: form.value.downAt }),
```

`PhoneDetails.vue:552-555` — clear a site override back to the global default:

```js
      body: JSON.stringify({ scope: 'site', clear: true }),
```

All three are gated `if (!can('operator')) return` client-side **and**
`requireOperator` server-side (`api/api.go:170,173,175`). The client guard is
explicitly described as defence in depth, `PhoneDetails.vue:467-468`:
"Guarded as well as disabled: a stale render or a console call must not put work
on the collector."

### Force sweep

Collector side, `collector/phone_collector.py:414-423`:

```python
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
```

Server side confirms the destructive read, `api/phones_sweep.go:40-49`:

```go
func ClaimPhonesSweeps(c *fiber.Ctx) error {
	sweepMu.Lock()
	pending := make([]string, 0, len(sweepPending))
	for k := range sweepPending {
		pending = append(pending, k)
	}
	sweepPending = map[string]bool{}
	sweepMu.Unlock()
	return c.Status(200).JSON(fiber.Map{"pending": pending})
}
```

The map is reassigned inside the lock — the Python comment is accurate. Two
consequences worth knowing before copying this: a second poller **steals** the
request, and the collector ignores which key was requested, treating any
non-empty list as "sweep everything".

Route-ordering hazard, `api/api.go:167-171`: `/v1/phones/sweep-pending` is
registered **before** `/v1/phones/:key`, or `:key` swallows it. The same comment
appears for `/v1/unifi` at `api.go:183`.

UI trigger, `PhoneDetails.vue:474-484` — POST then poll `updatedAt` for ~15s at
1.5s intervals to detect the fresh push.

## 5. Main loop

`collector/phone_collector.py:401-411`:

```python
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
```

The isolation rule: a **bare `except Exception`** around each location, with the
justifying comment inline. This is the one place in the file where a broad catch
is correct, and it is the reason eleven PBXes behind one flaky VPN still produce
ten good rows. `unifi_collector.py:435-438` repeats it verbatim per row.

Note the two-tier failure policy: a missing **push** token is fatal
(`sys.exit(1)`, nothing can be reported); a missing **per-location** token is a
skip with a stderr line (`:322-325`), because the other ten sites are fine.

`collector/phone_collector.py:426-444`:

```python
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
```

- `LOOP=1` (exact string compare against `"1"`) selects daemon mode; anything
  else is one sweep then exit. Set in `docker-compose.yml:32`.
- Jitter is `random.uniform(lo, hi)` — a fresh interval every cycle, not a fixed
  period. Phones 15-45s, UniFi 30-60s (`unifi_collector.py:444-445`, slower
  because "UniFi state moves slower than phone registrations").
- The chunked wait is what makes force-sweep feel instant: worst case
  `SWEEP_POLL` (2s) latency instead of up to 45s. `unifi_collector.py:448` still
  uses a plain blocking `time.sleep(random.uniform(lo, hi))` — it has no
  force-sweep, so nothing to interrupt.

Per-location error handling inside `run_location` is layered
(`collector/phone_collector.py:326-361`):

- Reachability probe catches `(urllib.error.URLError, OSError)` → `pbx_reachable = False`.
- Registrations fetch **raises on non-200** rather than degrading to an empty
  map — the comment at `:344-346` explains why: a 401 from an expired token used
  to read as "0 phones, all fine".
- A PHP `json_encode` quirk is coerced explicitly (`:349-353`): an empty map
  serializes as `[]`, so `if not isinstance(reg_result, dict): reg_result = {}`.
- `fetch_directory` swallows everything (`:209-210`) — it is cosmetic, it must
  never fail a health check.

Timing discipline, `collector/phone_collector.py:328-334`:

```python
    # Response time = ONLY the PBX API reachability call (how responsive the phone
    # system is). The registrations/Colleagues fetches below are data-gathering
    # overhead (the Colleagues directory is ~800 records) and must NOT inflate it.
    reach_start = time.monotonic()
```

Always `time.monotonic()`, never `time.time()`. UniFi solves the same problem
differently (`unifi_collector.py:376-379`): one shared estate read is charged to
every row, because the per-row work afterwards is pure local computation and
would otherwise report a meaningless 0ms.

## 6. Conventions to copy

**Logging** — no `logging` module. Bare `print()`. Success/status lines to
stdout, problems to `file=sys.stderr`. Severity is a literal prefix:
`ERROR: ` for fatal-or-skipped, `WARN: ` for a failed push that does not stop
the sweep. One `key=value` line per location, `collector/phone_collector.py:385-386`:

```python
    print(f"{key}: status={status} online={counts['online']} offline={counts['offline']} "
          f"excluded={counts['excluded']} dur={int(duration_ms)}ms")
```

`PYTHONUNBUFFERED=1` in compose (`docker-compose.yml:34,56`) so `docker logs -f`
streams live.

**Env var naming** — `SCREAMING_SNAKE`, subsystem-prefixed
(`PHONES_*`, `UNIFI_*`), except the shared infra names `GATUS_PUSH_BASE`, `LOOP`,
`SWEEP_MIN`, `SWEEP_MAX`, `SWEEP_POLL`, `VERIFY_TLS`. Per-location secrets are
named by env var *reference* in the `LOCATIONS` table
(`"token_env": "PHONES_ALABASTER_TOKEN"`, `:54`) rather than resolved at import,
so the table stays declarative. Tunables read at import with an inline default:

```python
DEGRADED_AT = int(os.environ.get("PHONES_DEGRADED_AT", "2"))          # :124
TX_RETRY_DEGRADED_PCT = float(os.environ.get("UNIFI_TX_RETRY_DEGRADED_PCT", "20"))   # unifi:120
```

UniFi also does graceful token fallback (`unifi_collector.py:406-409`):
`UNIFI_PUSH_TOKEN` or `PHONES_PUSH_TOKEN`.

**`load_dotenv`** — hand-rolled, no dependency. Identical in both collectors
(`phone_collector.py:131-141`, `unifi_collector.py:124-134`):

```python
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
```

Four properties worth preserving: sibling `.env` beats repo-root `.env`; it
stops at the **first** file found (`return` inside the loop); `setdefault` means
a real environment variable always wins over the file; and it is called **once**
from `main()` (`:427`), never at import.

**Dependencies** — stdlib only. `urllib.request`, `json`, `ssl`, `random`,
`re`, `time`, `os`, `sys`. That is why compose can run a bare `python:3-slim`
with the source bind-mounted and no build step (`docker-compose.yml:24,36-39`).

**Timeouts** — a single module constant, applied on every call.
`HTTP_TIMEOUT = 12` (`phone_collector.py:125`), `= 25` for UniFi
(`unifi_collector.py:68`, a cloud API). Passed explicitly:
`urllib.request.urlopen(req, timeout=HTTP_TIMEOUT, context=_ctx())` (`:159`).

**Retries** — there are **none**, deliberately. The loop is the retry: a failed
sweep is simply re-attempted 15-45s later, and a transient failure shows as one
amber/red cell rather than a stall. Do not add backoff.

**TLS** — `VERIFY_TLS=0` produces an unverified context (`:144-150`); default is
verified. Phones only; the UniFi cloud API is always verified.

**Regexes** — precompiled at module level, `:126-127`:

```python
CONTACT_IP_RE = re.compile(r"@([0-9]{1,3}(?:\.[0-9]{1,3}){3}):")
MAC_RE = re.compile(r"^[0-9a-fA-F]{12}$")
```

**Comment style** — the dominant convention in this codebase. Comments explain
*why*, and specifically record the failure that motivated the code. Examples:
the Cullman wrong-tenant note (`:62-67`), the PHP `json_encode` `[]` coercion
(`:349-351`), the zero-phones-is-an-outage branch (`:286-289`), the
CloudKey-is-not-a-gateway note (`unifi_collector.py:36-40`). Mirror this density.

**Declarative site table at the top** — `LOCATIONS` (`:43-119`) /
`SITES` (`unifi_collector.py:77-107`), a list of dicts, with the key-derivation
rule spelled out in a comment on the first entry:

```python
        "key": "phones_ivory-tower",       # slug(group=Phones)_slug(name=Ivory Tower)
```

Keys must match `config.yaml`'s `external-endpoints:` as `slug(group)_slug(name)`
(`config.yaml:236-300`). Per-site oddities live as comments **in the table**,
not in a separate doc — Decatur sharing a PBX (`:74-76`), Alabaster having no
adopted APs (`unifi_collector.py:83-86`), Decatur GMC having no console on the
current account (`config.yaml:287-291`).

### The NOT_REPORTING contract

A fixed error **prefix** is a load-bearing API between the collectors and the
Vue frontend. Collector side, `collector/phone_collector.py:367-379`:

```python
        # NOTE: the "no phones reporting" wording is a CONTRACT with the UI —
        # LocationCard.vue matches it to paint the bar BLACK (nothing reported)
        # instead of red (phones present but offline). Don't reword the prefix.
        ...
            reason = f"no phones reporting ({detail})"
```

UniFi side, `collector/unifi_collector.py:368-375`:

```python
        # Contract with LocationCard.vue: this prefix paints the bar BLACK
        # ("nothing reported") instead of red ("reported and failing").
        status, counts, detail, reason = "down", {}, {}, f"no unifi reporting ({fatal})"
```

Frontend side, `web/app/src/components/LocationCard.vue:201-208`:

```js
// "Nothing reported" is a distinct failure from "reported and failing": a site
// with 0 phones registered, or a UniFi console we can't read, has no health
// signal at all, and painting that red makes it look like an outage. Both
// collectors flag it with a fixed error prefix — "no phones reporting" and
// "no unifi reporting" — and it renders BLACK. Keep this in step with the
// prefixes in collector/phone_collector.py and collector/unifi_collector.py.
const NOT_REPORTING = /^no (phones|unifi) reporting\b/i
const isNotReporting = (result) =>
  !!result && !result.success && (result.errors || []).some((e) => NOT_REPORTING.test(e))
```

**Why it matters:** it is a three-way distinction the `success` boolean cannot
carry. Green = fine, amber = reported and partially broken, red = reported and
down, black = *no signal at all*. Without the black state, a console the
collector cannot read looks identical to a site whose every AP is down, and the
dashboard cries wolf. The regex is anchored `^` and uses `\b`, so the
parenthesised detail after the prefix is free text — put the diagnosis there
(`no phones reporting (PBX reachable, 0 desk phones registered; directory says
Cullman users are on longlewiscu.wildixin.com, not longlewiscl.wildixin.com)`,
`:376-379`).

**Four duplicated copies** of the regex, which must be kept in step:

- `web/app/src/components/LocationCard.vue:206` — `/^no (phones|unifi) reporting\b/i`
- `web/app/src/views/FirewallDetails.vue:253` — same
- `web/app/src/views/SiteOverview.vue:332` — same
- `web/app/src/views/WirelessDetails.vue:287` — `/^no unifi reporting\b/i` (narrower)

`WirelessDetails.vue:394` shows the token mapping:

```js
  return errorsOf(r).some(e => NOT_REPORTING.test(e)) ? 'stbar-nodata' : 'stbar-down'
```

`LocationCard.vue:240-246` also applies it to the rollup: an Overall row whose
entire failing slice is not-reporting renders `nodata`, not red — "if the ONLY
thing that failed was a not-reporting feed, there is no outage to call red."

---

## Auth summary (collector-relevant)

| Route | Bearer | Session role |
|---|---|---|
| `POST /v1/endpoints/:key/external` | required | none |
| `POST /v1/phones/:key` | required | none |
| `POST /v1/unifi/:key` | required | none |
| `GET /v1/phones/:key/exclusions` | none | none |
| `GET /v1/phones/:key/settings` | none | none |
| `GET /v1/phones/sweep-pending` | none | none (destructive) |
| `POST /v1/phones/:key/exclusions` | none | `requireOperator` |
| `POST /v1/phones/:key/settings` | none | `requireOperator` |
| `POST /v1/phones/:key/sweep` | none | `requireOperator` |

Bearer check is inlined identically in all three push handlers
(`api/phones_inventory.go:48-59`, `api/unifi_inventory.go:51-58`,
`api/external_endpoint.go:28-45`) and compares against the token on the matching
`external-endpoints:` entry. Plain `!=` compare, not constant-time.

Two **stale comments** flagged by the backend sweep: `api/phones_inventory.go:140-141`
and `api/phones_settings.go:108-109` both still claim the write routes are
"Unauthenticated (internal LAN tool)". They are now `requireOperator`-gated at
`api/api.go:173,175`. Code is authoritative.

---

## Recommendation for the UniFi equivalent

1. **Do not port the baseline file.** It is dead code in the phones collector
   and its ratchet semantics were the reason. Mirror `fetch_thresholds` instead.
2. The gap is the **feedback loop**, not the collector. `unifi_collector.py`
   already has three-state health, the inventory push, the NOT_REPORTING
   contract and per-row error isolation.
3. To close the gap, add alongside the existing `/v1/unifi` routes:
   `GET/POST /v1/unifi/:key/settings` (thresholds — AP-offline count, tx-retry
   percent, WAN-down count) and `GET/POST /v1/unifi/:key/exclusions` (exclude a
   known-dead AP), modelled directly on `api/phones_settings.go` and the
   exclusions half of `api/phones_inventory.go`. Register any static route
   **before** `/v1/unifi/:key`.
4. Replace the hardcoded `TX_RETRY_DEGRADED_PCT` (`unifi_collector.py:120`) with
   a server-polled `effective` block, keeping the env value as the fallback —
   exactly the `fetch_thresholds` shape.
5. If force-sweep is wanted, also swap the blocking `time.sleep`
   (`unifi_collector.py:448`) for the chunked interruptible wait from
   `phone_collector.py:437-442`, and be aware the claim is destructive: only one
   poller may consume it.
