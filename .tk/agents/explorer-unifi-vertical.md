# UniFi vertical — end-to-end map

Read-only exploration. No code changed.

Purpose of this report: support (a) replacing the hardcoded `SITES` table with
auto-discovery from the UniFi Site Manager API, and (b) adding a full per-device
inventory push.

---

## 0. The vertical at a glance

```
UniFi Site Manager cloud (api.ui.com)
  GET /v1/hosts   GET /v1/sites   GET /v1/devices
        |
        v
collector/unifi_collector.py   (docker-compose.yml:45 unifi-collector, LOOP=1, 30-60s)
        |
        |-- POST /api/v1/unifi/<key>            rich snapshot   -> api/unifi_inventory.go:44
        |-- POST /api/v1/endpoints/<key>/external?success&duration&error  -> pass/fail bar
        v
Go: in-memory unifiStore map  +  sidecar sqlite history.db (numeric counts only)
        |
        |-- GET /api/v1/unifi        (all snapshots)  -> web/app/src/store.js:119 unifiSnapshots
        |-- GET /api/v1/unifi/<key>  (one snapshot)   -> FirewallDetails / WirelessDetails
        |-- GET /api/v1/history/<key>                 -> HistoryChart series
        v
Vue: LocationCard row metric  ->  /endpoints/<key>  ->  EndpointDetailRouter  ->  Firewall/WirelessDetails
```

---

## 1. `collector/unifi_collector.py` (454 lines)

### 1.1 Module constants

| Constant | Line | Value / meaning |
|---|---|---|
| `API_BASE` | :67 | `https://api.ui.com` |
| `HTTP_TIMEOUT` | :68 | 25s |
| `SITES` | :77-107 | the hardcoded dashboard row table (below) |
| `GATEWAY_PREFIXES` | :112 | `("UDM","UDR","UXG","UCG","USG","UDW")` — hardware that actually routes |
| `NOT_GATEWAY` | :113 | `{"UCKP","UCKG2","UCK","UNVR","UNVRPRO","UOSSERVER"}` |
| `NON_WIRELESS_PREFIXES` | :116-117 | `("US","UCK","UNVR","UDM","UDR","UXG","UCG","UOSSERVER","UVC","UA-","UP ","UP-")` — used to exclude non-AP devices |
| `TX_RETRY_DEGRADED_PCT` | :120 | env `UNIFI_TX_RETRY_DEGRADED_PCT`, default `20` |

### 1.2 The `SITES` table (:77-107) — the thing to replace

Four entries. Each is a dict with exactly four keys:

```python
{"label": str, "slug": str, "gateway_host": str|None, "wireless_host": str|None}
```

| label | slug | gateway_host | wireless_host |
|---|---|---|---|
| Alabaster | `alabaster` | `"Alabaster"` | `None` (no adopted APs on that console) |
| Decatur GMC | `decatur-gmc` | `"BA-DCU-FWE-KIA"` | `"Decatur (GMC & KIA)"` |
| Decatur KIA | `decatur-kia` | `"BA-DCU-FWE-KIA"` | `"Decatur (GMC & KIA)"` |
| Ivory Tower | `ivory-tower` | `None` (UOS Server, no WAN) | `"Ivory Tower Hosted"` |

Contract notes that constrain auto-discovery:

- `slug` must equal the slug of the **endpoint name** in `config.yaml`, because the
  Gatus key is `slug(group)_slug(name)` (:74-76). e.g. `group: Firewall` +
  `name: Decatur GMC` -> `firewall_decatur-gmc`. `config.yaml:266-300` holds the
  six matching `external-endpoints`, all with `token: "${PHONES_PUSH_TOKEN}"`.
- **A pushed key that has no `external-endpoints` entry is rejected 404** by
  `SetUniFiSnapshot` (`api/unifi_inventory.go:47-50`) and by the external-result
  endpoint. Auto-discovery therefore cannot create rows on its own — config.yaml
  must still list them, and `config.yaml:229` notes external-endpoints only reload
  on `docker compose down && up -d`.
- Two SITES entries deliberately point at the *same* console (Decatur GMC / KIA
  share `BA-DCU-FWE-KIA` and `Decatur (GMC & KIA)`). Any auto-discovery must keep
  a many-rows-to-one-console mapping possible, not assume 1 console = 1 row.
- `gateway_host`/`wireless_host` `None` means "push no row of that kind"
  (`sweep_once` :433).

### 1.3 Helpers

- `load_dotenv()` :124 — reads `collector/.env` then `../.env`, `setdefault` only.
- `api(path, key)` :137 — GET `API_BASE+path` with `X-API-KEY: <key>`, `Accept: application/json`; raises `ValueError` on non-200; returns parsed JSON.
- `push(url, token, body=None)` :146 — POST with `Authorization: Bearer <token>`; adds `Content-Type: application/json` and a JSON body only when `body is not None`. Returns HTTP code.
- `num(value, digits=1)` :157 — `round(float(v), 1)` or `None`.
- `is_gateway(host)` :211 — reads `host.reportedState.hardware.shortname`, uppercased; False if empty or in `NOT_GATEWAY`; else `startswith(GATEWAY_PREFIXES)`.
- `is_wireless(device)` :219 — False unless `productLine` (default `"network"`) == `network`; reads `shortname` or `model` uppercased; False if empty or `device.isConsole`; else NOT startswith `NON_WIRELESS_PREFIXES`. **This is a negative filter: anything not explicitly excluded counts as an AP.**

### 1.4 `class Estate` (:165-208)

One snapshot of the whole account, built from three calls in `__init__` (:168-171):

```python
self.hosts   = api("/v1/hosts",   key).get("data") or []
self.sites   = api("/v1/sites",   key).get("data") or []
self.devices = api("/v1/devices", key).get("data") or []
```

- `_name(host)` :173 — `host["reportedState"]["name"]`, stripped.
- `host_by_name(wanted)` :177 — case-insensitive exact match over `hosts`, then a substring match in **either direction** (`w in name or name in w`). Returns `None` if `wanted` is falsy.
- `site_for_host(host)` :191 — first `site` where `site["hostId"] == host["id"]`.
- `devices_for_host(host)` :201 — `/v1/devices` is a list of groups; returns `group["devices"]` for the group whose `hostId` matches.

Fields of the upstream API the collector actually touches (useful inventory surface):

- host: `id`, `ipAddress`, `reportedState.{name,state,version,wans[],hardware.{name,shortname}}`
- site: `hostId`, `meta.desc`, `statistics.counts.{wifiDevice,offlineWifiDevice,wifiClient,guestClient,wiredClient,totalDevice,offlineDevice,pendingUpdateDevice,wifiConfiguration}`, `statistics.percentages.txRetry`
- device: `hostId`, and per device `name`, `model`, `shortname`, `mac`, `ip`, `status`, `version`, `firmwareStatus`, `startupTime`, `productLine`, `isConsole`

### 1.5 `collect_firewall(site, estate)` (:229-286)

Returns `(status, counts, detail, reason)`.

Early exits (both return empty `detail = {}` and zeroed counts):
- :231-234 console not found -> `"down", {"wansUp":0,"wansTotal":0}, {}, "no console named ... on this UniFi account"`
- :235-238 found but not a gateway -> `"down", {"wansUp":0,"wansTotal":0}, {}, "<name> is a <hw>, not a gateway — it has no WAN uplinks"`

WAN list build (:244-260): iterates `reportedState.wans`, **skips `enabled == False`**, and builds per-WAN dicts, then sorts by `(len(id), id)` so `WAN` precedes `WAN2`.

**Exact `counts` shape (:263):**
```json
{ "wansUp": <int>, "wansTotal": <int> }
```

**Exact `detail` shape (:264-274):**
```json
{
  "gateway": {
    "name": "<reportedState.name | hardware.name | 'Gateway'>",
    "model": "<hardware.name | hardware.shortname | ''>",
    "shortname": "<hardware.shortname | ''>",
    "version": "<reportedState.version | ''>",
    "ip": "<host.ipAddress | ''>",
    "state": "ONLINE | <reportedState.state upper>"
  },
  "wans": [
    {
      "id":        "<wan.type | wan.interface | 'wanN'>",
      "interface": "<wan.interface | ''>",
      "port":      <wan.port, may be null>,
      "up":        <bool: plugged AND ipv4>,
      "plugged":   <bool>,
      "ip":        "<wan.ipv4 | ''>",
      "mac":       "<wan.mac | ''>",
      "speedType": "<wan.speedType | ''>"
    }
  ]
}
```

Status ladder (:276-286): not connected -> `down`; no WANs -> `down`; none up -> `down`; some up -> `degraded` with `"N of M WAN uplinks down (ids)"`; else `healthy, reason=None`.

### 1.6 `collect_wireless(site, estate)` (:289-357)

Early exits return `{"apsOnline":0,"apsTotal":0,"clients":0}` and `detail = {}`:
- :291-294 console not found
- :295-298 no site record for that host

Totals come from `record.statistics.counts` (:300-305), **not** from the device list:
`total = wifiDevice`, `offline = offlineWifiDevice`, `online_count = max(0, total-offline)`,
`tx_retry = num(statistics.percentages.txRetry)` (can be `None`).

`offline_names` (:308-312): sorted names of wireless devices whose `status` != `online`.
`aps` (:313-324): the **per-device list**, filtered by `is_wireless`, sorted by lowercased name.

**Exact `counts` shape (:326-333):**
```json
{
  "apsOnline":    <int>,
  "apsTotal":     <int>,
  "clients":      <int>,   // wifiClient
  "guests":       <int>,   // guestClient
  "wiredClients": <int>,   // wiredClient
  "txRetryPct":   <float|null>
}
```

**Exact `detail` shape (:334-345):**
```json
{
  "site":       "<site.meta.desc | console name>",
  "controller": "<console name>",
  "aps": [
    {
      "name":     "<device.name | model | mac | '?'>",
      "model":    "<device.shortname | device.model | ''>",
      "ip":       "<device.ip | ''>",
      "state":    "<device.status uppercased>",
      "version":  "<device.version | ''>",
      "firmware": "<device.firmwareStatus | ''>",
      "since":    "<device.startupTime | ''>"
    }
  ],
  "wlan": {
    "offlineNames":   ["<name>", ...],
    "totalDevices":   <int>,   // totalDevice
    "offlineDevices": <int>,   // offlineDevice
    "pendingUpdate":  <int>,   // pendingUpdateDevice
    "ssids":          <int>    // wifiConfiguration
  }
}
```

Status ladder (:347-357): `total == 0` -> `down` "no access points on this site";
`online_count == 0` -> `down`; any offline -> `degraded` naming up to 4;
`tx_retry > TX_RETRY_DEGRADED_PCT` -> `degraded`; else `healthy`.

### 1.7 `run_row(kind, site, estate, push_token, base, fatal, api_ms)` (:364-402) — the push

- Key: `f"{kind}_{site['slug']}"` (:365).
- Fatal path (:369-370) or any collector exception (:374-375) -> `status="down", counts={}, detail={}, reason=f"no unifi reporting ({...})"`. **That `no unifi reporting` prefix is a cross-layer contract** matched by `LocationCard.vue:206`, `FirewallDetails.vue:253`, `WirelessDetails.vue:287`, `SiteOverview.vue:332` to paint grey/black "nothing reported" instead of red.
- `duration_ms = api_ms + local elapsed` (:379) — every row is charged the shared estate read.
- `success = status != "down"` (:383) — **`degraded` pushes as a PASS carrying `error`**.

**Push 1 — the rich snapshot (:387-392):**
```
POST {base}/api/v1/unifi/{kind}_{slug}
Authorization: Bearer <push_token>
Content-Type: application/json
{"kind": "firewall"|"wireless", "status": "...", "site": "<label>",
 "counts": {...}, "detail": {...}}
```
Failure is a non-fatal stderr WARN.

**Push 2 — the pass/fail bar (:393-402):**
```
POST {base}/api/v1/endpoints/{kind}_{slug}/external?success=true|false&duration=<N>ms[&error=<reason>]
Authorization: Bearer <push_token>
(no body)
```
`error` is sent for degraded results too (:396-398) — that is what makes the amber bar.

**Token:** a single value, resolved in `sweep_once` (:408-409) as
`UNIFI_PUSH_TOKEN` or `PHONES_PUSH_TOKEN`; both pushes use it. It must equal the
`token:` on that endpoint in `config.yaml` (all six are `${PHONES_PUSH_TOKEN}`).

### 1.8 `sweep_once()` (:405-438)

1. Resolve `api_key` = `UNIFI_API_KEY` or `UNIFI_DECATUR_API_KEY`; `push_token` as above; `base` = `GATUS_PUSH_BASE` (default `http://localhost:8080`; compose sets `http://gatus:8080`). `sys.exit(1)` if either is missing (:412-416).
2. Build **one** `Estate` for the whole sweep, timing it into `api_ms` (:421-427). On failure, `fatal` is set and every row pushes "no unifi reporting".
3. For each site x `("firewall","wireless")`: skip when the matching `*_host` is falsy (:433); call `run_row` inside a bare `except Exception` so one row can't kill the sweep (:435-438).

### 1.9 `main()` (:441-450)

`load_dotenv()`, then if `LOOP == "1"` loop forever sleeping `random.uniform(SWEEP_MIN=30, SWEEP_MAX=60)`; otherwise one sweep and exit.

---

## 2. Go side

### 2.1 Storage — `api/unifi_inventory.go`

- `unifiMu sync.RWMutex` + `unifiStore map[string]storedUniFi` (:28-31). **Process memory only, never persisted.** A Gatus restart blanks every snapshot until the next sweep (WirelessDetails.vue:53-54 says exactly this to the user).
- `storedUniFi` (:33-40):
  ```go
  UpdatedAt string          `json:"updatedAt"`
  Kind      string          `json:"kind,omitempty"`
  Status    string          `json:"status,omitempty"`
  Site      string          `json:"site,omitempty"`
  Counts    json.RawMessage `json:"counts,omitempty"`
  Detail    json.RawMessage `json:"detail,omitempty"`
  ```
  `Counts` and `Detail` are `json.RawMessage` — **stored and re-served verbatim.
  The Go layer never parses `detail`, so an expanded device inventory needs zero
  Go changes to flow through.**

- `SetUniFiSnapshot(cfg)` (:44-93):
  1. `key := c.Params("key")`; `cfg.GetExternalEndpointByKey(key)`; **404 if the key is not in config.yaml** (:47-50).
  2. `Authorization: Bearer <token>` must equal `externalEndpoint.Token`, else 401 (:51-58).
  3. Unmarshal the body; **400 if `payload.Kind == ""`** (:66-68) — `kind` is the only required field.
  4. `monitoring.IsPaused(key)` -> returns **200 "OK (monitoring paused)" without storing** (:73-75).
  5. Store with `UpdatedAt = time.Now().UTC().Format(time.RFC3339)` (:76-85).
  6. `recordCounts(key, payload.Counts)` (:89) — after the pause guard on purpose.
  7. Logs `[api.SetUniFiSnapshot] Stored <kind> snapshot for key=<key> status=<status>`.

- `GetUniFiSnapshot(c)` (:96-105) — one key; 404 `{"error":"no UniFi snapshot reported yet"}` if absent; else 200 with the `storedUniFi` JSON.
- `GetUniFiSnapshots(c)` (:111-119) — copies the whole map under `RLock` and returns `{ "<key>": storedUniFi, ... }`.

**Size caution for a big inventory push:** the whole map is copied and serialized
on every `/api/v1/unifi` call, and `store.js` polls it every 20s for every browser.
A per-device inventory would ride that dashboard-wide payload too (see 3.4).

### 2.2 Metric history — `api/history.go` + `history/history.go`

- `recordCounts(key, counts)` (`api/history.go:27-41`): unmarshals `counts` into `map[string]any` and keeps **only JSON numbers** (`float64`) — nulls, strings, booleans, arrays and objects are skipped. Explicitly generic: *"any count a collector starts sending is charted with no change here"* (:21-25). So new numeric `counts` fields chart automatically; nothing inside `detail` is ever charted.
- `history.Record` (`history/history.go:172`) writes a raw sample + hourly rollup into a **separate sqlite file (`/data/history.db`)**, deliberately not Gatus's store (`history/history.go:1-26`). Retention: raw 48h (:41), hourly rollups 90d (:46), pruned hourly (:52).
- Read-back: `GET /api/v1/history/:key?range=1h|6h|24h|7d|30d` -> `GetMetricHistory` (`api/history.go:49-80`) returns `{key, range, resolution, from, to, series}` where `series` is `{ "<metricName>": {timestamps[], values[]} }`.

### 2.3 Route registration — `api/api.go:181-187`

```go
unprotectedAPIRouter.Get("/v1/unifi", GetUniFiSnapshots)        // :184 (static route first)
unprotectedAPIRouter.Post("/v1/unifi/:key", SetUniFiSnapshot(cfg)) // :186 COLLECTOR PUSH, no session gate
unprotectedAPIRouter.Get("/v1/unifi/:key", GetUniFiSnapshot)   // :187
```
Plus `api/api.go:161` `POST /v1/endpoints/:key/external` (the pass/fail push) and
`api/api.go:190` `GET /v1/history/:key`.

All three UniFi routes are on `unprotectedAPIRouter` — no session cookie. The push
routes authenticate on the per-endpoint bearer token; the GETs are open (anonymous
read-only is the documented posture).

### 2.4 JSON the frontend receives

`GET /api/v1/unifi/firewall_alabaster`:
```json
{"updatedAt":"2026-09-15T14:03:11Z","kind":"firewall","status":"healthy",
 "site":"Alabaster","counts":{...},"detail":{...}}
```
`GET /api/v1/unifi`: the same objects in a map keyed by endpoint key.

---

## 3. Frontend

### 3.1 `web/app/src/store.js:117-130`

```js
export const unifiSnapshots = ref({})            // :119
export async function refreshUniFiSnapshots()    // :120  fetch('/api/v1/unifi', {cache:'no-store'})
refreshUniFiSnapshots()                          // :128  on module load
setInterval(refreshUniFiSnapshots, 20000)        // :129  every 20s, forever, dashboard-wide
```
Errors are swallowed to keep the last snapshot rather than blanking rows (:124-126).

### 3.2 `LocationCard.vue` — what a card row reads

- `unifiCounts(ep)` :287-290 -> `snap.counts`; `unifiDetail(ep)` :291-294 -> `snap.detail`.
- `firewallMetric` :296-310 reads **`counts.wansUp`, `counts.wansTotal`, `detail.wans[0].speedType`, `detail.wans[0].ip`**. Falls back to raw latency when `counts.wansTotal == null`.
- `wirelessMetric` :311-321 reads **`counts.apsOnline`, `counts.apsTotal`, `counts.clients`**. Falls back to latency when `counts.apsTotal == null`.
- Nothing from `detail.aps` is read on the card.

### 3.3 `FirewallDetails.vue` — fields rendered

Fetch: `GET /api/v1/unifi/<key>` (:503), `GET /api/v1/endpoints/<key>/statuses?page=1&pageSize=40` (:520), `GET /api/v1/history/<key>?range=` (:443), `GET /api/v1/endpoints/<key>/uptime-series?range=` (:466). Poll 20s (:243), history 300s (:247).

| Source field | Where used |
|---|---|
| `updatedAt` | :511 -> `reportingFresh` (<90s, :301-305), `updatedLabel` :306 |
| `status` | :512 -> `statusMeta` :298 (`healthy`/`degraded`/`down`, else "Unknown") |
| `site` | :513 -> `siteName` :270-274 (falls back to de-slugged key) |
| `counts.wansUp`, `counts.wansTotal` | `uplinkSummary` :286-291, `uplinkChartSub` :402-409, `emptySnapshot` :335-339 |
| `detail.gateway.model` | template :69 "Model" |
| `detail.gateway.shortname` | template :73 "Hardware" |
| `detail.gateway.version` | template :77 "Firmware" |
| `detail.gateway.ip` | template :81 "Public IP" |
| `detail.gateway.state` | template :60-62, :85-87 — compared `=== 'ONLINE'` |
| `detail.wans[].id` | rung label :128, `:key` :126, `notCarrying` :284 |
| `detail.wans[].interface` | :136 (`|| 'unnamed port'`), `:key` :126 |
| `detail.wans[].port` | :137, rendered only when not null/undefined |
| `detail.wans[].speedType` | :139, conditional |
| `detail.wans[].ip` | :141 (`|| 'no address'`) |
| `detail.wans[].up`, `.plugged` | `wanState` :281 -> `up` / `linked` / `down` |
| `detail.wans[].mac` | **pushed but never rendered** |
| history series `wansUp`, `wansTotal` | :389, :403 |

### 3.4 `WirelessDetails.vue` — fields rendered (this is the per-device precedent)

Fetch: `GET /api/v1/unifi/<key>` (:666 — **note: no `encodeURIComponent`, unlike FirewallDetails:503**), statuses pageSize 50 (:681), history + uptime-series. Poll 20s (:280), history 300s (:284).

Snapshot accessors :297-302: `counts`, `detail`, `wlan = detail.wlan`, `aps = detail.aps` (array-guarded), `controller = detail.controller`.

| Source field | Where used |
|---|---|
| `status` | `statusMeta` :334 |
| `updatedAt` | `asDate` :426, `fresh` (<180s) :431, `updatedLabel` :427 |
| `detail.site` -> else `snapshot.site` -> else de-slugged key | `siteName` :304-309 |
| `detail.controller` | band :67, empty-state copy :209 |
| `counts.apsTotal` / `counts.apsOnline` | :316-319, header :14, band :66, tiles :180, table head :219; **falls back to `aps.length` / filtered count when null** |
| `counts.clients` | band :66, cell "Wifi clients" :351, chart `clients` :549 |
| `counts.guests` | cell "Guests" :352-355 |
| `counts.wiredClients` | cell "Wired clients" :356 |
| `counts.txRetryPct` | cell "Tx retry" :357-362, tone thresholds 20/35 :338-344, chart :550 |
| `detail.wlan.ssids` | cell "SSIDs" :363 |
| `detail.wlan.pendingUpdate` | cell "Updates pending" :364-367 |
| `detail.wlan.totalDevices`, `.offlineDevices`, `.offlineNames` | **pushed but never rendered** (offlineNames only reaches the UI indirectly, inside the collector's `error` string) |
| `detail.aps[].name` | tile :191, table :241, `:key` :188/:235, selection identity `pick`/`selected` :458-461, filter :494, sort tiebreak :524 |
| `detail.aps[].model` | tile sub :192, table :242, tooltip :452, filter :494 |
| `detail.aps[].ip` | table :243, `:key` :188, tooltip :453, filter + `ipKey` sort :498-505 |
| `detail.aps[].state` | `isOnline` :311 (`=== 'ONLINE'` uppercased), lamp :238, `stateLabel` :437, filter :494 |
| `detail.aps[].version` | table :244, tooltip :455, filter :494 |
| `detail.aps[].firmware` | `upToDate` :312 (strip non-alpha, `=== 'uptodate'`), pill :246, `firmwareLabel` :438-443, filter :494 |
| `detail.aps[].since` | `sinceLabel` :432-436 via `asDate` (accepts RFC3339 **or** epoch s/ms, :416-425) |
| history series `apsOnline`, `clients`, `txRetryPct` | :548-550; tx-retry chart hidden unless the series has a non-null value (:554-557) |

Table columns are declared at `WirelessDetails.vue:472-480`:
`state, name, model, ip, version, firmware, since` — sorted via `sortValue` :501-512
(state/firmware sort as booleans, ip via zero-padded octets, since via `asDate`).

Roster tiles are keyed `ap.name + ap.ip`; `selected` is **`ap.name` alone**, so
duplicate AP names would collide in selection and filtering.

### 3.5 Fields the UI reads that the collector may not always populate

These are the gaps to be aware of when expanding the payload:

1. **`detail` is `{}` on every collector early-exit and on the fatal path**
   (unifi_collector.py:233, :237, :293, :297, :370, :375). Both views handle it:
   FirewallDetails has an explicit `emptySnapshot`/`unreadable` branch (:335-340,
   template :97-107); WirelessDetails falls through to "No access points on this
   site" (:206-214). Any new `detail` sub-object must tolerate being absent.
2. **`counts.txRetryPct` is legitimately `null`** (`num()` returns None). Handled
   at :339, :359-360, and `recordCounts` skips nulls by design (api/history.go:23-25).
3. **`counts.guests` / `wiredClients` do not exist on the early-exit paths** —
   those return only `{apsOnline, apsTotal, clients}` (unifi_collector.py:293,:297).
   `fmt()` renders `—` (:337).
4. **`detail.gateway.state`** can be an arbitrary uppercased UniFi state string;
   the template only special-cases `'ONLINE'`.
5. **`wan.port` can be `null`** and is guarded (:137). `wan.id` can collide when
   `type`/`interface` are both missing (falls back to `wanN`), and it is part of
   the `:key`.
6. **`ap.since` may be `''`** -> "unknown"; may be epoch seconds on some firmwares.
7. **`ap.name` may be `'?'`** (fallback chain name->model->mac->'?'), which would
   break tile selection if several devices land on `'?'`.
8. `aps.length` vs `counts.apsTotal` divergence is expected and explained in-page
   by `rosterNote` (:323-327) — the site statistics are authoritative, the device
   list is best-effort.

---

## 4. Device-level data today — precise answer

**Firewall rows: no device list at all.** `collect_firewall` pushes exactly one
device — the gateway — as the scalar object `detail.gateway`
(`name, model, shortname, version, ip, state`, unifi_collector.py:265-272), plus
the `detail.wans[]` array which describes *ports*, not devices. Switches, cameras,
CloudKeys and every other device on the gateway's console are **never pushed**.
`api/unifi_inventory.go` has no gateway-specific parsing, and `FirewallDetails.vue`
has no device table — only the five-cell identity strip (:66-89) and the WAN ladder
(:117-150).

**Wireless rows: a per-device list already exists, but only for APs.**
`detail.aps` (unifi_collector.py:313-324) is a real per-device array with
`name, model, ip, state, version, firmware, since` — 7 fields. It is built from
`estate.devices_for_host(host)` filtered by `is_wireless()`, which **excludes
switches (`US*`), CloudKeys, NVRs, gateways, cameras (`UVC`), access (`UA-`) and
PDUs (`UP*`)** (:116-117, :219-225). It is rendered in full by
`WirelessDetails.vue` as the roster tile map (:176-203) and the sortable,
filterable table (:217-258).

So today: **aggregate counts everywhere, plus one per-device list that covers only
access points on wireless-row consoles.** Everything else the `/v1/devices` call
already returns is fetched, filtered out, and discarded.

Fields from the upstream device record that are fetched but **not** carried into
`detail.aps`: `mac`, `productLine`, `isConsole`, `hostId`, and anything else
`/v1/devices` returns (uptime, adoption time, uplink/topology, client counts per
device, etc. — the collector only ever reads the seven named keys plus `status`,
`shortname`, `model`, `mac`, `name`, `ip`, `version`, `firmwareStatus`,
`startupTime`). `offline_names` (:308-312) is computed separately from the same
list and lands in `detail.wlan.offlineNames`, which **no view renders**.

---

## 5. Routing — there is no `/firewall/<key>` or `/wireless/<key>` route

This is the single most important correction to the brief. Both drill-ins live
under the **one** endpoint route and are selected by key prefix.

`web/app/src/router/index.js:16-20`:
```js
{ path: '/endpoints/:key', name: 'EndpointDetails', component: EndpointDetailRouter }
```
There are only seven routes total: `/`, `/endpoints/:key`, `/sites/:name`,
`/suites/:key`, `/jira`, `/ll-telemetry`, `/settings` (:10-52).

`web/app/src/views/EndpointDetailRouter.vue` dispatches on the key prefix (:23-29):
```js
if (key.startsWith('phones_'))   return 'phones'
if (key.startsWith('firewall_')) return 'firewall'
if (key.startsWith('wireless_')) return 'wireless'
return 'endpoint'
```
-> `PhoneDetails` / `FirewallDetails` / `WirelessDetails` / `EndpointDetails` (:5-8).

**The link from a card row** — `LocationCard.vue:371-388`, `pushEndpointRow`:
```js
to: endpoint ? `/endpoints/${endpoint.key}` : null,   // :378
```
Called for the UniFi rows at :430-431:
```js
if (s.firewall) pushEndpointRow('Firewall', s.firewall, 'firewall')
if (s.wireless) pushEndpointRow('Wireless', s.wireless, 'wireless')
```
Row slotting is by **endpoint group regex**, not by key: `classify()`
`LocationCard.vue:137-149` matches `/firewall|gateway|edge/` and
`/wireless|wi-?fi|wlan|access\s*point/`; `slots` (:178-188) takes the **first**
matching endpoint per card and pushes any second one into `others`.
The overall row links to `/sites/<name>` (:449).

Consequence for both planned changes: **the endpoint key prefix (`firewall_`,
`wireless_`) is load-bearing for view selection, and the endpoint `group` string is
load-bearing for card row placement.** A new site added by auto-discovery renders
correctly only if its config.yaml entry keeps `group: Firewall` / `group: Wireless`
and its name slugs to the collector's slug.

---

## 6. Implications for the two planned changes

### Replacing the hardcoded `SITES` table with auto-discovery

- Discovery can derive `label`/`gateway_host`/`wireless_host` from `/v1/hosts` +
  `/v1/sites` — `is_gateway()` (:211) already classifies gateway vs controller.
- It **cannot** invent rows: `SetUniFiSnapshot` 404s and the external-result push
  fails for any key absent from `config.yaml:230+`, and external-endpoints only
  reload on a full `down && up -d` (config.yaml:229). Plan for a discovery output
  that *proposes* config.yaml entries, or accept that discovered-but-unconfigured
  sites are skipped with a log line.
- The slug must match `slug(name)` in config.yaml exactly. Deriving a slug from a
  UniFi console name will NOT match today's rows (`BA-DCU-FWE-KIA` serves
  `decatur-gmc` and `decatur-kia`; `Decatur (GMC & KIA)` serves both too). Keep an
  explicit override/alias map, or the four existing rows break.
- Preserve the deliberate suppressions: Alabaster has `wireless_host: None`
  because an always-empty AP row reads as an outage (:82-86), and Ivory Tower has
  `gateway_host: None` because a UOS Server has no WAN (:102). Naive discovery
  re-introduces both.
- `host_by_name`'s bidirectional substring fallback (:183-188) is a fuzzy matcher
  that auto-discovery would make redundant — discovery should carry host `id`s
  instead, which `site_for_host`/`devices_for_host` already key on.

### Adding a full per-device inventory push

- **No Go changes needed.** `Detail` is `json.RawMessage`, stored and re-served
  verbatim (`api/unifi_inventory.go:39`, :83, :104). Only the `counts` object is
  parsed, and only for numbers.
- New numeric `counts` fields chart automatically (`api/history.go:20-25`); new
  `detail` fields never chart.
- Follow the `detail.aps[]` precedent: a flat array of objects with short, stable
  camelCase keys. The wireless table/tile/sort/filter code (`WirelessDetails.vue`
  :188-258, :472-526) is a ready template for a generic device table.
- Watch the payload budget: `GET /api/v1/unifi` returns **every** key's full
  `detail` and is polled every 20s by every open dashboard tab
  (`store.js:122-129`), while `LocationCard` only needs six scalar fields from it.
  A large per-device inventory pushed into `detail` inflates that dashboard-wide
  poll. Consider a separate detail key, a separate route, or having the card poll
  a counts-only projection.
- Nothing in the Go layer bounds the body size of a snapshot push, and the store
  is an unbounded in-memory map keyed by endpoint key.
- Any new view has to reach the user through `EndpointDetailRouter.vue`'s prefix
  dispatch or through the existing Firewall/Wireless views — there is no route to
  add a device-inventory page under today's router without a new entry in
  `router/index.js`.
