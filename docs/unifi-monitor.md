# UniFi Monitor (the Firewall and Wireless rows)

Two extra rows on every location card that has UniFi gear:

- **Firewall** — the site's gateway and its WAN uplinks.
- **Wireless** — the site's access points and clients.

Same shape as the Phones row: Gatus never talks to UniFi. `collector/unifi_collector.py`
reads Ubiquiti's **cloud** Site Manager API and pushes two things per row — a
pass/fail result to the external-endpoint, and the rich detail to a side channel.

Click either row label to drill in (`/endpoints/firewall_<slug>`,
`/endpoints/wireless_<slug>`).

---

## What it shows

- **On the card** — a bar history per row plus a trailing metric:
  - Firewall: `2/2 WAN` (red when fewer are up than total).
  - Wireless: `23/23 AP` with `412 cl` underneath.
  - A row only appears where a console is actually wired up, so sites without
    UniFi don't grow two permanently empty rows.
- **Firewall drill-down** — the gateway identity strip (model, hardware
  shortname, firmware, public IP, cloud state) and an *uplink ladder*: one rung
  per enabled WAN drawn as a path from gateway to internet, carrying its
  interface, port, speed type and address. Three states: carrying, linked but no
  address, no link. Plus the recorded check history.
- **Wireless drill-down** — status band, a counts strip (APs online, wifi
  clients, guests, wired clients, tx retry, SSIDs, updates pending), the check
  history, an AP roster map you can click to filter, and a sortable AP table
  (state, name, model, IP, firmware version, update status, uptime).

---

## Why the cloud API, not per-console

```
Base:   https://api.ui.com
Auth:   X-API-KEY: <key created at unifi.ui.com>
```

A key from unifi.ui.com is **global**: one credential covers every console on the
account, and the collector needs no route to any store's LAN. That is the whole
reason for the choice — the box running the collector is not on the rooftop
networks. The per-console Integration API
(`https://<console>/proxy/network/integration/v1/...`) is the alternative and
needs LAN reachability per site, so it is not used.

Three calls serve every site, once per sweep:

| Call | Used for |
|------|----------|
| `GET /v1/hosts` | Consoles, including `reportedState.wans` and `hardware.shortname`. |
| `GET /v1/sites` | Per-site `statistics.counts` and `percentages.txRetry`. |
| `GET /v1/devices` | Per-host device list with online/offline status. |

**The trap:** a *console-local* integration key is a different credential and
gets **401 from api.ui.com**. If you generated the key inside a console's own
settings rather than at unifi.ui.com, it will never work here.

---

## Architecture

```
api.ui.com  <--3 calls per sweep (30-60s)--  unifi-collector (python)
                                                     |
                        pass/fail  ->  /api/v1/endpoints/<key>/external
                        detail     ->  /api/v1/unifi/<key>        (side channel)
                                                     |
   browser  <--/api/v1/unifi (all rows, every 20s)---  api handlers
```

The external-endpoint result can only carry a boolean and an error string, so the
per-WAN state, gateway health, AP list and client counts go to the side channel.
Snapshots there are **ephemeral** — held in memory, nothing persisted. A
restarted Gatus shows blank detail until the next sweep; a key that stops
reporting simply goes stale, which the UI shows rather than hides.

### Backend files

| File | Purpose |
|------|---------|
| `collector/unifi_collector.py` | The whole collector: the `SITES` table, the cloud reads (`Estate`), `collect_firewall` / `collect_wireless`, and both pushes. Its module docstring is the reference for the data quirks below. |
| `api/unifi_inventory.go` | The side-channel store (`map[key]storedUniFi`, RWMutex) and three handlers: `SetUniFiSnapshot` (push-token auth, same as the phones inventory), `GetUniFiSnapshot`, `GetUniFiSnapshots`. |
| `api/api.go` | Registers `/v1/unifi`, `/v1/unifi/:key` (GET + POST) and the three `/v1/monitoring` routes. |
| `config.yaml` | The `external-endpoints` entries that make the rows exist. |
| `docker-compose.yml` | The `unifi-collector` service (`python:3-slim`, `LOOP=1`, `GATUS_PUSH_BASE=http://gatus:8080`, `./collector` bind-mounted). |

### Frontend files

| File | Purpose |
|------|---------|
| `web/app/src/components/LocationCard.vue` | Classifies endpoints into rows by group regex, and renders the Firewall/Wireless trailing metrics from the side-channel counts. |
| `web/app/src/store.js` | `unifiSnapshots` — one `/api/v1/unifi` fetch every 20s for the whole page, rather than two per location. |
| `web/app/src/views/EndpointDetailRouter.vue` | Routes `firewall_*` and `wireless_*` keys to their own detail pages. |
| `web/app/src/views/FirewallDetails.vue` | The uplink ladder page. Polls the snapshot + `statuses` every 20s. |
| `web/app/src/views/WirelessDetails.vue` | The AP roster page. Same polling. |
| `web/static/*` | The built bundle (`npm run build`; embedded into the Go binary). |

### HTTP endpoints (added)

| Method | Path | Returns |
|--------|------|---------|
| GET | `/api/v1/unifi` | Every reported snapshot, keyed by endpoint key. What the dashboard polls. |
| GET | `/api/v1/unifi/:key` | One snapshot; **404** with `{"error":"no UniFi snapshot reported yet"}` if nothing has been pushed. |
| POST | `/api/v1/unifi/:key` | Collector push. Requires `Authorization: Bearer <that endpoint's token>`; 404 for an unknown key, 401 for a bad token, 400 unless the body has a `kind`. |

---

## Two things worth knowing about the data

Both are documented in the collector's docstring; they are the reason the code
looks the way it does.

**Only gateway hardware has real WAN uplinks.** A CloudKey, an NVR or a UOS
Server *also* reports a `wans` array, but it is just that box's management NIC —
Decatur's CloudKey "WAN" is `10.10.1.26`, a LAN address. Reporting those as
firewall uplinks would invent a firewall that isn't there, so hosts are filtered
to gateway models (`UDM`, `UDR`, `UXG`, `UCG`, `USG`, `UDW`, minus an explicit
`UCKP`/`UCK`/`UNVR`/`UOSSERVER` deny list) and **a site with no gateway pushes no
Firewall row at all**.

**AP counts come from the site's own `statistics.counts`** (`wifiDevice` and
`offlineWifiDevice`), not from classifying models in the device list. The device
list is used only to *name* which APs are offline and to fill the roster. The two
can disagree — the wireless page says so out loud rather than reconciling them.

---

## Current topology

Only three locations have a console on this UniFi account (as of 2026-08-17):

| Location | Console | Gateway host | Wireless host | Rows |
|----------|---------|--------------|---------------|------|
| Alabaster | UDM Pro, 2 WANs (10G SFP+ and GbE) | `Alabaster` | — | Firewall only. That console has 0 adopted APs, so a Wireless row would sit permanently at "no access points", which reads as an outage. |
| Decatur GMC | CloudKey Plus `Decatur (GMC & KIA)` (23 APs) behind the Cloud Gateway Ultra `BA-DCU-FWE-KIA` | `BA-DCU-FWE-KIA` | `Decatur (GMC & KIA)` | Firewall + Wireless |
| Decatur KIA | same controller, same gateway | `BA-DCU-FWE-KIA` | `Decatur (GMC & KIA)` | Firewall + Wireless |
| Ivory Tower | UOS Server, 2 APs | — | `Ivory Tower Hosted` | Wireless only — a controller with no WAN uplink of its own. |

Both Decatur stores share one controller and sit behind the KIA gateway, the way
they share one PBX. The CloudKey named "Decatur (GMC & KIA)" is the **controller,
not the firewall**. Every other rooftop has no UniFi console on this account.

`gateway_host` / `wireless_host` are matched against a console's name in
`/v1/hosts` — exact match first, then substring, both case-insensitive.

---

## Key naming

The endpoint key is `slug(group)_slug(name)`, so group `Firewall` + name
`Decatur GMC` gives `firewall_decatur-gmc`. The collector builds the same string
as `<kind>_<slug>` from its `SITES` table, so the two must agree exactly or the
push 404s.

**External endpoints only register on `docker compose down && up -d`.** A plain
`restart` will not pick up a new entry in `config.yaml`.

---

## Configuration

Environment-driven, in `.env`, injected into the `unifi-collector` container via
`env_file`.

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| `UNIFI_API_KEY` | yes | — | Global Site Manager key from **unifi.ui.com**, not a console-local one. (`UNIFI_DECATUR_API_KEY` is still accepted as a fallback.) The collector exits 1 without it. |
| `UNIFI_PUSH_TOKEN` | yes | — | Push token; falls back to `PHONES_PUSH_TOKEN`. Must equal the `token` on the endpoint in `config.yaml`. Exits 1 if neither is set. |
| `PHONES_PUSH_TOKEN` | — | — | What `config.yaml` actually interpolates into every UniFi endpoint's `token`. Reused deliberately rather than minting a new secret: it is the same collector trust boundary, and every deployed box already has it set. |
| `GATUS_PUSH_BASE` | no | `http://localhost:8080` | Compose sets `http://gatus:8080` to reach Gatus over the compose network. |
| `UNIFI_TX_RETRY_DEGRADED_PCT` | no | `20` | Site tx-retry above this marks Wireless degraded. |
| `LOOP` | no | unset | `1` = daemon. Anything else means one sweep, then exit. |
| `SWEEP_MIN` | no | `30` | Low end of the jittered sleep, in seconds. |
| `SWEEP_MAX` | no | `60` | High end. UniFi state moves slower than phone registrations, hence the wider interval than the phone collector's. |

Run one sweep by hand:

```bash
docker compose exec unifi-collector python unifi_collector.py
```

---

## Health rules

Exactly what the collector implements. A WAN counts as **up** only when it is
`plugged` *and* has an `ipv4`; a WAN with link but no address is up-but-not-carrying.
Disabled WANs are skipped entirely.

**Firewall**

| Status | When |
|--------|------|
| `healthy` | Gateway connected and every enabled WAN is plugged with an IPv4. |
| `degraded` | At least one WAN up, but not all of them. |
| `down` | Gateway not connected; or no enabled WANs; or every WAN down; or no console matches `gateway_host`; or the matched console isn't gateway hardware. |

**Wireless**

| Status | When |
|--------|------|
| `healthy` | Every AP online and tx-retry at or below the threshold. |
| `degraded` | Some APs offline, or tx-retry above `UNIFI_TX_RETRY_DEGRADED_PCT`. |
| `down` | No AP online; or the site has no APs at all; or no console matches `wireless_host`; or the console reports no site statistics. |

**`degraded` is a warning and does not fail the check — only `down` does.** The
result is pushed with `success = (status != "down")`, matching how the Phones row
behaves. So a site running on its backup WAN records a pass, keeps its green bar,
and shows the amber `1/2 WAN` metric plus a note on the drill-down explaining
which uplink to chase.

---

## The "nothing reported" contract

"Nothing reported" is a different failure from "reported and failing": a site
whose collector couldn't read anything has no health signal, and painting that
red makes it look like a real outage. So when the cloud read itself fails, the
collector pushes an error string beginning **`no unifi reporting`** — a fixed
prefix the UI matches to paint the bar **BLACK** (`stbar-nodata`) rather than red.

Rewording that prefix breaks the UI silently. It is matched in
`web/app/src/views/WirelessDetails.vue` as:

```js
const NOT_REPORTING = /^no unifi reporting\b/i
```

`components/LocationCard.vue` and `views/SiteOverview.vue` accept both
collectors' prefixes:

```js
const NOT_REPORTING = /^no (phones|unifi) reporting\b/i
```

There are three copies of this rule in total (card, site overview, wireless
drill-in). Adding a third collector means widening all three, or the new rows
paint red for an absence rather than black.

A cloud-API outage is handled once per sweep, not per row: one `Estate` read
serves every row, and if it throws, every row reports "nothing reported" instead
of a fake failure.

---

## Adding a rooftop

1. Add **two** entries to `external-endpoints` in `config.yaml` — one with
   `group: Firewall`, one with `group: Wireless`, both `name:` the location
   exactly as the card is titled, both `token: "${PHONES_PUSH_TOKEN}"`. Omit
   whichever row the site has no hardware for.
2. Add a line to `SITES` in `collector/unifi_collector.py`:
   - `label` / `slug` — the slug must make the key match step 1.
   - `gateway_host` — the console name in `/v1/hosts` that is the **firewall**.
     `None` suppresses the Firewall row.
   - `wireless_host` — the console name that is the **controller**. `None`
     suppresses the Wireless row.
3. `docker compose down && docker compose up -d`. A plain restart will not
   register the new external endpoints.

The rows fill in on the next sweep, within a minute.

---

## Pausing monitoring

Per-endpoint "monitor this or not", shared by every browser and persisted
server-side to `/data/monitoring.json` so it survives updates. `monitoring/monitoring.go`
holds the state (lazily loaded, since `/data` only exists at runtime) and
`api/monitoring.go` exposes it.

| Method | Path | Body / returns |
|--------|------|----------------|
| GET | `/api/v1/monitoring` | `{"paused":[...],"disabled":[...]}` — the two keys are **aliases** of the same sorted list. `paused` is the backend's name, `disabled` is what the frontend reads. Don't "clean up" either. |
| GET | `/api/v1/monitoring/:key` | `{"key":"...","monitored":true|false}` |
| POST | `/api/v1/monitoring/:key` | `{"monitored":false}` → `{"key":"...","monitored":false}` |

Pausing is **real, not cosmetic**:

- The watchdog runs no check for that key — nothing stored, no alert, no metric
  (`watchdog/endpoint.go`), and no heartbeat check on external endpoints
  (`watchdog/external_endpoint.go`, so the pause can't manufacture its own
  failure).
- Collector pushes are accepted-but-discarded (`api/external_endpoint.go` returns
  `200 OK (monitoring paused)` — an error would make every collector log a
  warning every sweep).
- The endpoint is excluded from its card's status badge and the Overall row
  (`activeEndpoints` in `LocationCard.vue`), while its own row still renders
  muted and labelled `paused` so you can see it is paused rather than missing.
- Recorded history is deliberately left alone, so resuming continues the same
  timeline instead of starting a new one.

Where the controls are:

- A switch in each drill-down toolbar (`MonitorToggle.vue`, on the Firewall,
  Wireless and Phones pages).
- A gear popover on every location card (`CardSettingsMenu.vue`) with one switch
  per row plus a **Pause all / Resume all** button.

Toggles are optimistic and reverted with a toast if the server disagrees. The
frontend re-reads `/api/v1/monitoring` every 30s.

---

## Gotchas & troubleshooting

**401 from api.ui.com.** Wrong *kind* of key. A console-local integration key
(generated inside a console's own settings) is a different credential and is
rejected by the cloud API. Create the key at **unifi.ui.com** and put it in
`UNIFI_API_KEY`.

**A row is stuck black (nothing reported).** The cloud read failed for the whole
sweep. Check `docker compose logs unifi-collector` — the error is printed verbatim
in the `no unifi reporting (...)` string. Usual causes: `UNIFI_API_KEY` unset or
invalid, or no egress to `api.ui.com`.

**A row shows grey/empty bars and the drill-down says "no snapshot for this key
yet".** The push isn't landing. Either the key doesn't match (`slug(group)_slug(name)`
vs. the collector's `<kind>_<slug>`, giving a 404), the token doesn't match the
endpoint's `token` (401), or `config.yaml` was edited without
`docker compose down && up -d`. Side-channel snapshots are also in-memory only,
so a freshly restarted Gatus is blank for up to one sweep.

**A site shows 0 APs and reads as down.** Counts come from the site's
`statistics.counts.wifiDevice`, which is authoritative. 0 means that console has
no *adopted* wireless devices — Alabaster is exactly this case, which is why it
has no Wireless row. Either adopt the APs to that console (they appear on the
next sweep) or drop the `wireless_<slug>` row from `config.yaml` rather than
watching an empty site.

**The Firewall row never appears, or reports "is a `<model>`, not a gateway".**
That site has no gateway hardware on the account — the console you pointed
`gateway_host` at is a CloudKey, NVR or UOS Server, whose `wans` entry is a
management NIC. Point `gateway_host` at the actual gateway (Decatur's is
`BA-DCU-FWE-KIA`) or set it to `None` and drop the `firewall_<slug>` entry.

**"No console named ... on this UniFi account".** `gateway_host` /
`wireless_host` didn't match any name in `/v1/hosts`, exact or substring. Compare
against the console names as UniFi reports them, not the store name.

**The Firewall row's second line is blank.** That line only renders in
fullscreen, and it shows the primary uplink's `speedType` (e.g. `10G SFP+`) from
`detail.wans[0]`. It is empty when the gateway reports no `speedType`, which
happens on a UOS Server's virtual `br0` interface.
