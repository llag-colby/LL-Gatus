# Side-channel persistence audit — what exists today

Scope: the three collector-fed side channels (phones inventory, UniFi firewall,
UniFi wireless) plus the persisted control state (exclusions, thresholds, pause).
Question being answered: **what is persisted vs thrown away**, so no history is
promised that does not exist.

**Headline: none of the three side-channel payloads are persisted. All three are
in-memory single-slot stores, overwritten every sweep, lost on container restart.
There is zero historical data for them today — not one hour of it.**

---

## 1. Phones inventory — EPHEMERAL, in-memory, single slot per key

**Store**
`api/phones_inventory.go:23-30`

```go
phonesInventoryMu    sync.RWMutex
phonesInventoryStore = make(map[string]storedInventory)
```

- Data structure: a plain Go `map[string]storedInventory` guarded by a
  `sync.RWMutex`. **One slot per endpoint key** — there is no slice, no ring
  buffer, no timestamped series.
- `api/phones_inventory.go:34-39` — the stored value:

```go
type storedInventory struct {
	UpdatedAt string          `json:"updatedAt"`
	Status    string          `json:"status,omitempty"`
	Counts    json.RawMessage `json:"counts,omitempty"`
	Phones    json.RawMessage `json:"phones"`
}
```

  Note `Counts`/`Phones` are `json.RawMessage`: **Gatus never parses them.** They
  are opaque bytes stored and echoed back verbatim. Nothing server-side can chart
  them today without new parsing code (or the collector sending typed fields).

- Write path: `api/phones_inventory.go:75-82` — a bare map assignment. The
  previous snapshot is **discarded** (`phonesInventoryStore[key] = ...`).
- `UpdatedAt` is stamped server-side at receive time, RFC3339 UTC
  (`api/phones_inventory.go:77`).
- Read path: `GetPhonesInventory` (`api/phones_inventory.go:89-98`) returns the
  single current slot, or 404 `{"error":"no inventory reported yet"}`.
- Paused keys: the push is **accepted and dropped** (200 OK,
  `api/phones_inventory.go:72-74`), so a paused key freezes at its last snapshot.
- **Container restart: everything is gone.** No disk write anywhere in the file
  for the inventory. The header comment says so explicitly at
  `api/phones_inventory.go:21-22` ("Inventory is ephemeral (the collector
  re-reports every sweep)"). Recovery is implicit — the next sweep (≤45s) refills
  the current slot, but the past is unrecoverable.

**What a snapshot actually contains** (payload built by the collector)

`counts` — `collector/phone_collector.py:277-283`, all `int`:

| JSON path | meaning |
|---|---|
| `counts.total` | every desk phone found (monitored + excluded) |
| `counts.monitored` | phones not excluded |
| `counts.online` | monitored phones with `online == true` |
| `counts.offline` | `monitored - online` |
| `counts.excluded` | phones on the exclusion list |

`phones` — array of objects, `collector/phone_collector.py:231-245`:
`ext`, `name`, `did`, `department`, `email`, `ip`, `mac`, `model`, `firmware`,
`sipStatus` (`"registered"`/`"unregistered"`), `online` (bool), `reachable`
(bool, currently identical to `online` — see the comment at line 243),
`excluded` (bool).

`status` — `"healthy" | "degraded" | "down"`, decided by `evaluate_health`
(`collector/phone_collector.py:272-300`) against the server-side thresholds.

---

## 2. Phones exclusions and settings — PERSISTED to /data (the precedent to follow)

Both use the same shape: lazy load once on first use, full-file rewrite on every
mutation, `0o644`, `MarshalIndent`. Failure to write is logged, never returned.

### Exclusions

- Path: `/data/phones_exclusions.json` — `api/phones_inventory.go:32`
- In-memory: `exclusionsData = map[string][]string{}` (`:28`), keyed by endpoint
  key, value = sorted extension strings.
- Load: `ensureExclusionsLoaded()` `api/phones_inventory.go:102-112` — one-shot
  guarded by `exclusionsLoaded`; a missing/corrupt file silently means "nothing
  excluded".
- Write: `persistExclusions()` `api/phones_inventory.go:114-121`.
- **On-disk shape** (verified against the live `data/phones_exclusions.json`):

```json
{
  "phones_ivory-tower": []
}
```

  i.e. `{ "<endpoint key>": ["<ext>", ...] }`.

### Settings (health thresholds)

- Path: `/data/phones_settings.json` — `api/phones_settings.go:29`
- Load `ensurePhonesSettingsLoaded()` `:36-54`; write `persistPhonesSettings()`
  `:56-64`. Defaults `{degradedAt: 2, downAt: 10}` at `:24`.
- File struct `phonesSettingsFile` `:31-34`; value struct `phoneThresholds`
  `:17-20`.
- **On-disk shape** (verified against the live file):

```json
{
  "global":    { "degradedAt": 2, "downAt": 10 },
  "overrides": {}
}
```

  `overrides` is `{ "<endpoint key>": {"degradedAt": N, "downAt": N} }`.

**Precedent notes for a history feature:** these are tiny mutable-config files
rewritten whole. They are a good precedent for *shape* (a `/data/<thing>.json`
lazily loaded, mutex-guarded, atomic-ish full rewrite) but a **bad precedent for
a time series** — full-file rewrite on every append does not scale to ~43k
samples/day (see §7). The right precedent for history is the SQLite store
(`/data/data.db`, already mounted, already schema-migrated — see §8).

---

## 3. UniFi snapshots — EPHEMERAL, in-memory, single slot per key

**Store** `api/unifi_inventory.go:28-31`

```go
unifiMu    sync.RWMutex
unifiStore = make(map[string]storedUniFi)
```

- Same pattern as phones: one `map`, one slot per key, mutex-guarded.
- `api/unifi_inventory.go:33-40`:

```go
type storedUniFi struct {
	UpdatedAt string          `json:"updatedAt"`
	Kind      string          `json:"kind,omitempty"`   // firewall | wireless
	Status    string          `json:"status,omitempty"` // healthy | degraded | down
	Site      string          `json:"site,omitempty"`
	Counts    json.RawMessage `json:"counts,omitempty"`
	Detail    json.RawMessage `json:"detail,omitempty"` // everything else, verbatim
}
```

  Again `Counts`/`Detail` are unparsed `json.RawMessage`.
- Write `api/unifi_inventory.go:76-85` — overwrite; previous snapshot discarded.
- Paused keys accepted-and-dropped `api/unifi_inventory.go:73-75`.
- Reads: `GetUniFiSnapshot` (`:92-101`, one key) and `GetUniFiSnapshots`
  (`:107-115`, whole map in one request — the dashboard's per-refresh call).
- **Container restart: gone.** Explicit comment `api/unifi_inventory.go:25-27`:
  "Snapshots are ephemeral … nothing is persisted."

### `kind: "firewall"` — `collect_firewall`, `collector/unifi_collector.py:229-286`

`counts` (`:263`) — only two fields, both `int`:

- `counts.wansUp` — WANs where `plugged && ipv4` (see `:253`)
- `counts.wansTotal` — enabled WANs (disabled ones skipped at `:247-248`)

On the early-out paths (`:233`, `:237`) counts are `{"wansUp":0,"wansTotal":0}`
and `detail` is `{}`. On the `fatal` path (`run_row`, `:370`) **both `counts` and
`detail` are `{}`** — so a chart must treat missing counts as a gap, not a zero.

`detail` (`:264-274`):

- `detail.gateway.name`, `.model`, `.shortname`, `.version`, `.ip`
- `detail.gateway.state` — `"ONLINE"` or the upper-cased UniFi state string
- `detail.wans[]` — per uplink: `id` (e.g. `WAN`/`WAN2`, sorted primary-first at
  `:260`), `interface`, `port`, `up` (bool), `plugged` (bool), `ip`, `mac`,
  `speedType`

**There is no throughput, bandwidth, latency or loss data in the firewall
payload at all.** The only per-sweep number that moves is `wansUp` (and the
gateway/WAN up booleans). Response time for the row comes from the *endpoint
result*, not this payload (`:379`).

### `kind: "wireless"` — `collect_wireless`, `collector/unifi_collector.py:289-357`

`counts` (`:326-333`):

- `counts.apsOnline` — `int`, `wifiDevice - offlineWifiDevice`, floored at 0 (`:304`)
- `counts.apsTotal` — `int`, site `statistics.counts.wifiDevice`
- `counts.clients` — `int`, `wifiClient`
- `counts.guests` — `int`, `guestClient`
- `counts.wiredClients` — `int`, `wiredClient`
- `counts.txRetryPct` — `float` rounded to 1 decimal, **or `null`** — `num()`
  returns `None` on a non-numeric (`:157-161`, `:305`). Must be nullable in any
  schema.

Early-out paths (`:293`, `:297`) give `{"apsOnline":0,"apsTotal":0,"clients":0}`
— **missing `guests`/`wiredClients`/`txRetryPct` entirely**, another reason to
treat absent as null rather than 0.

`detail` (`:334-345`):

- `detail.site`, `detail.controller` — strings
- `detail.aps[]` — per AP: `name`, `model`, `ip`, `state` (upper-cased, `"ONLINE"`
  when up), `version`, `firmware`, `since` (`startupTime`)
- `detail.wlan.offlineNames[]` — names of offline APs
- `detail.wlan.totalDevices` — `int` (all adopted devices, not just APs)
- `detail.wlan.offlineDevices` — `int`
- `detail.wlan.pendingUpdate` — `int`
- `detail.wlan.ssids` — `int` (`wifiConfiguration`)

Degradation thresholds: any offline AP degrades (`:351-354`); `txRetryPct` above
`TX_RETRY_DEGRADED_PCT` (default 20, env `UNIFI_TX_RETRY_DEGRADED_PCT`, `:120`)
degrades (`:355-356`).

---

## 4. Monitoring pause state — PERSISTED to /data

- Path: `/data/monitoring.json` — `monitoring/monitoring.go:30`
- In-memory `paused = map[string]bool{}` (`:26`), `sync.RWMutex`, one-shot lazy
  load `ensureLoaded()` (`:39-54`).
- File struct `monitoringFile` (`:32-34`); keys always written sorted via
  `sortedKeys()` (`:67-74`) so the file is diff-stable.
- Write `persist()` (`:56-64`), called from `SetPaused` (`:87-97`).
- **On-disk shape** (verified against the live file):

```json
{
  "paused": [
    "phones_cullman"
  ]
}
```

- Deliberate design note at `monitoring/monitoring.go:15-17`: pausing leaves
  already-recorded history alone, so un-pausing resumes the same timeline. Any
  history feature should honour the same rule — do not backfill or zero-fill
  across a pause window; leave a gap.
- Import constraint worth respecting: `monitoring` must not import `api` or
  `watchdog` (`monitoring/monitoring.go:21-23`). A new `history` package feeding
  both would face the same cycle risk if it imports `api`.

Full inventory of `/data` writers — there are exactly three, nothing else:

```
api/phones_settings.go:29   /data/phones_settings.json
api/phones_inventory.go:32  /data/phones_exclusions.json
monitoring/monitoring.go:30 /data/monitoring.json
```

plus the SQLite store at `/data/data.db` (`config.yaml:1-3`). Live `data/`
directory confirms: `data.db`, `data.db-shm`, `data.db-wal`, `monitoring.json`,
`phones_exclusions.json`, `phones_settings.json`.

---

## 5. What is worth charting — exhaustive numeric field list

All paths below are relative to the stored snapshot object as served by
`GET /api/v1/phones/:key` and `GET /api/v1/unifi/:key`.

### Phones (per key; 11 keys)

| JSON path | type | series value |
|---|---|---|
| `counts.online` | int | **primary** — desk phones registered |
| `counts.offline` | int | primary — the alarm number |
| `counts.monitored` | int | denominator; moves when phones are added/excluded |
| `counts.total` | int | fleet size |
| `counts.excluded` | int | how much of the fleet is being ignored |
| derived: `counts.online / counts.monitored` | float 0-1 | registration ratio, comparable across sites |
| `status` | enum | healthy/degraded/down state ribbon |
| derived from `phones[]`: count where `online==true` | int | cross-check of `counts.online` |
| derived from `phones[]`: distinct `firmware` values | int | firmware drift / fleet-upgrade progress |
| derived from `phones[]`: distinct `model` values | int | model mix (slow-moving; daily granularity is plenty) |
| per-extension `phones[].online` keyed by `phones[].ext` | bool | per-phone availability heat map — **high cardinality, see §7** |

### Firewall (per key; 3 keys)

| JSON path | type | series value |
|---|---|---|
| `counts.wansUp` | int | **primary** — uplinks carrying traffic |
| `counts.wansTotal` | int | denominator (changes only on config change) |
| derived: `wansTotal - wansUp` | int | uplinks down |
| derived: `detail.gateway.state == "ONLINE"` | bool→0/1 | gateway reachability |
| per-WAN `detail.wans[].up` keyed by `detail.wans[].id` | bool→0/1 | **the useful one** — per-circuit up/down over time (WAN vs WAN2 flapping) |
| per-WAN `detail.wans[].plugged` | bool→0/1 | link vs. carrying: plugged-but-no-IPv4 is a distinct failure |
| derived: `detail.wans[].ip` changed since last sample | bool | WAN IP churn (DHCP flap) — event, not a gauge |
| `status` | enum | state ribbon |

Nothing else numeric exists here. **No bytes, no bandwidth, no per-WAN latency.**

### Wireless (per key; 3 keys)

| JSON path | type | series value |
|---|---|---|
| `counts.apsOnline` | int | **primary** — APs up |
| `counts.apsTotal` | int | denominator |
| derived: `apsTotal - apsOnline` | int | APs down |
| `counts.clients` | int | **primary** — wifi clients (the most interesting daily curve: occupancy/business-hours shape) |
| `counts.guests` | int | guest clients |
| `counts.wiredClients` | int | wired clients |
| `counts.txRetryPct` | float, **nullable** | **primary** — RF health / airtime quality |
| `detail.wlan.totalDevices` | int | all adopted devices |
| `detail.wlan.offlineDevices` | int | adopted devices down (incl. switches, not just APs) |
| `detail.wlan.pendingUpdate` | int | devices awaiting firmware |
| `detail.wlan.ssids` | int | configured WLANs (near-constant; use as an annotation, not a line) |
| derived: `len(detail.wlan.offlineNames)` | int | cross-check of offline APs |
| per-AP `detail.aps[].state == "ONLINE"` keyed by `detail.aps[].name` | bool→0/1 | per-AP uptime heat map (~23 APs at Decatur) |

### Already-persisted numbers (do NOT rebuild these)

Every collector sweep also POSTs to `/api/v1/endpoints/<key>/external`, and that
path **does** persist through the normal Gatus store
(`api/external_endpoint.go:81` → `store.Get().InsertEndpointResult`). So per key
there is already history for:

- success/failure (bool per push) → `endpoint_results`
- response time in ms (PBX reachability call for phones, UniFi cloud round-trip
  for UniFi rows) → `endpoint_results.duration`
- the `error`/reason string, including the *degraded* reason — the collector sends
  it on passing results too (`collector/phone_collector.py:312-317`,
  `collector/unifi_collector.py:394-398`)
- hourly aggregate: `endpoint_uptimes` (see §8)

**Caveat on how far back that goes:** `config.yaml:9` sets
`maximum-number-of-results: 3000` per endpoint. At the phones cadence (~2880
pushes/day, §6) that is roughly **1 day** of raw results for a phones key, ~1.5
days for a UniFi key. The hourly/daily uptime table retains 30 days
(`uptimeRetention = 30 * 24h`, `storage/store/sql/sql.go:38`) but only as
success-ratio + average-response-time, not counts.

---

## 6. Sampling cadence — measured from the loop code

### Phones — `collector/phone_collector.py:426-444`

```python
lo = int(os.environ.get("SWEEP_MIN", "15"))
hi = int(os.environ.get("SWEEP_MAX", "45"))
poll = float(os.environ.get("SWEEP_POLL", "2"))
while True:
    sweep_once()
    wait, waited = random.uniform(lo, hi), 0.0
    while waited < wait:
        time.sleep(poll)
        waited += poll
        if sweep_requested(base):
            break
```

- Enabled by `LOOP=1`, set in `docker-compose.yml:32`. No `SWEEP_MIN`/`SWEEP_MAX`
  override in compose, so **defaults apply: uniform jitter in [15s, 45s), mean
  30s.**
- The wait is quantised to the 2s poll (`waited += poll` after each sleep), so the
  real wait is `ceil(wait/2) * 2` — negligible skew, but it also means an extra
  HTTP GET to `/v1/phones/sweep-pending` every 2s.
- **Plus sweep duration**: `sweep_once()` (`:401-411`) iterates all 11 locations
  **sequentially**, each doing up to 4 HTTPS calls (version, registrations,
  Colleagues ≈800 records, exclusions, settings) with `HTTP_TIMEOUT = 12`
  (`:125`). Realistically several to tens of seconds per sweep, so the effective
  period is **~35-60s**, not a clean 30s.
- **Force-sweep path**: `sweep_requested()` (`:414-423`) GETs
  `/v1/phones/sweep-pending`, which **clears the pending set server-side**
  (`api/phones_sweep.go:40-49`). A pending request breaks the wait early → an
  immediate extra sweep. The UI POSTs `/v1/phones/:key/sweep`
  (`api/phones_sweep.go:24-36`), refused with 409 while the key is paused. This
  path is in-memory only and unbounded in rate from the UI's side — an interactive
  user clicking "force ping" can produce bursts well above the nominal cadence.

### UniFi — `collector/unifi_collector.py:441-450`

```python
lo = int(os.environ.get("SWEEP_MIN", "30"))
hi = int(os.environ.get("SWEEP_MAX", "60"))
while True:
    sweep_once()
    time.sleep(random.uniform(lo, hi))
```

- `LOOP=1` set in `docker-compose.yml:54`; no overrides → **uniform jitter in
  [30s, 60s), mean 45s.**
- Plain `time.sleep` — **no force-sweep path and no interruptibility** for UniFi.
- Sweep cost: exactly 3 cloud API calls per sweep total (`/v1/hosts`, `/v1/sites`,
  `/v1/devices` — `Estate.__init__`, `:168-171`), `HTTP_TIMEOUT = 25` (`:68`).
  One estate read serves all 6 rows, so per-sweep overhead is small and the
  effective period is close to the nominal ~45-50s.

### Jira (for contrast) — fixed-interval ticker, `jira/jira.go:245`, interval from
`cfg.pollInterval` (`:166`, `:222`). Not jittered.

---

## 7. Volume estimate

### Configured endpoints (`config.yaml`, `external-endpoints:` block `:173-243`)

- **Phones: 11** — Ivory Tower, Alabaster, Bessemer, Cullman, Decatur GMC,
  Decatur KIA, Florence, Hoover, Muscle Shoals, Prattville, Tuscumbia
  (`config.yaml:174-207`). Matches the collector's `LOCATIONS`
  (`collector/phone_collector.py:43-119`) exactly, 11 entries.
- **Firewall: 3** — Alabaster, Decatur GMC, Decatur KIA (`config.yaml:224-232`).
- **Wireless: 3** — Decatur GMC, Decatur KIA, Ivory Tower (`config.yaml:235-243`).
- **Total side-channel keys: 17.**

Cross-check against `collector/unifi_collector.py` `SITES` (`:77-107`): Alabaster
firewall-only (`wireless_host: None`), Decatur GMC both, Decatur KIA both, Ivory
Tower wireless-only (`gateway_host: None`) → 3 firewall + 3 wireless = 6 rows per
sweep (`sweep_once`, `:429-438`). Consistent.

### Arithmetic — one row per key per push

Seconds per day = 86,400.

**Phones**, nominal mean 30s:
```
sweeps/day  = 86,400 / 30          = 2,880
rows/day    = 2,880 × 11 keys      = 31,680
rows/30 day = 31,680 × 30          = 950,400
```

**Firewall**, mean 45s:
```
sweeps/day  = 86,400 / 45          = 1,920
rows/day    = 1,920 × 3 keys       = 5,760
rows/30 day = 5,760 × 30           = 172,800
```

**Wireless**, mean 45s, same 1,920 sweeps/day:
```
rows/day    = 1,920 × 3 keys       = 5,760
rows/30 day = 172,800
```

**Combined**
```
rows/day    = 31,680 + 5,760 + 5,760 = 43,200
rows/30 day = 43,200 × 30            = 1,296,000
```

Sanity band using the realistic effective periods from §6 (phones ~40s, UniFi
~50s), which is the number to actually plan against:
```
phones   : 86,400/40 = 2,160 sweeps × 11 = 23,760/day →   712,800 / 30d
firewall : 86,400/50 = 1,728 sweeps ×  3 =  5,184/day →   155,520 / 30d
wireless : 1,728 × 3                     =  5,184/day →   155,520 / 30d
total                                    = 34,128/day → 1,023,840 / 30d
```

So: **~35k-43k rows/day, ~1.0M-1.3M rows per 30 days** if every push is persisted
raw at full cadence. Force-sweeps push the phones figure higher, unbounded.

### Byte cost — this is the decision point

- **Counts only** (key, ts, plus ≤7 numeric columns): ~60-80 bytes of payload,
  call it ~100 B/row with SQLite overhead and one index →
  **~1.3 M × 100 B ≈ 130 MB per 30 days.** Viable, but it doubles the size of the
  current 13.5 MB `data.db` roughly ten times over, and every insert competes on
  the same SQLite connection as the WAN checks.
- **Full payload blobs**: a phones snapshot is ~44 phones × ~200 B ≈ **9 KB**.
  Persisting `phones[]` per sample = 950 k × 9 KB ≈ **8.5 GB per 30 days.**
  Not viable. Per-extension and per-AP series must be either dropped, sampled far
  more coarsely, or stored as change-events rather than snapshots.
- **Hourly buckets instead**: 24 buckets/day × 17 keys = **408 rows/day →
  ~12,240 rows per 30 days.** Three orders of magnitude cheaper, and it matches
  the existing `endpoint_uptimes` precedent (§8). Recommended default, with raw
  samples retained only for a short recent window (mirroring `config.yaml:4-9`'s
  reasoning: raw for short windows, hourly averages beyond).

---

## 8. Anything already time-bucketed?

**For the three side channels: no. Nothing. Zero per-hour or per-day aggregate
exists for phones counts, firewall counts or wireless counts anywhere in the
fork.** Both stores are single-slot maps (§1, §3), and there is no writer to
`/data` other than the three config JSON files (§4).

Two genuine precedents exist elsewhere, though:

### (a) `endpoint_uptimes` — hourly-bucket-then-roll-up (the strongest precedent)

Schema `storage/store/sql/specific_sqlite.go:87-95` (identical in
`specific_postgres.go:87`):

```sql
CREATE TABLE IF NOT EXISTS endpoint_uptimes (
	endpoint_uptime_id    INTEGER PRIMARY KEY,
	endpoint_id           INTEGER NOT NULL REFERENCES endpoints(endpoint_id) ON DELETE CASCADE,
	hour_unix_timestamp   INTEGER NOT NULL,
	total_executions      INTEGER NOT NULL,
	successful_executions INTEGER NOT NULL,
	total_response_time   INTEGER NOT NULL,
	UNIQUE(endpoint_id, hour_unix_timestamp)
)
```

Mechanics worth copying wholesale:

- **Upsert into an hour bucket, never append a row per sample** —
  `updateEndpointUptime`, `storage/store/sql/sql.go:665-687`:
  `result.Timestamp.Truncate(time.Hour).Unix()` as the bucket key, then
  `ON CONFLICT(endpoint_id, hour_unix_timestamp) DO UPDATE SET total = excluded + existing`.
- **Store sums, divide at read time** — `total_response_time` is a running sum;
  the average is computed on read (`sql.go:929-934`). The same trick gives
  min/max/avg for `counts.clients` or `counts.txRetryPct` with 4 columns
  (`sum`, `count`, `min`, `max`) and no per-sample rows.
- **Hourly→daily compaction** —
  `mergeHourlyUptimeEntriesOlderThanMergeThresholdIntoDailyUptimeEntries`
  (`sql.go:1050-1120`), triggered when a key exceeds
  `uptimeTotalEntriesMergeThreshold = 100` (`sql.go:37`), with
  `uptimeHourlyBuffer = 48h` (`:39`) keeping the recent 48h at hourly resolution
  and `uptimeRetention = 30 * 24h` (`:38`) / `uptimeAgeCleanUpThreshold = 32 * 24h`
  (`:37`) bounding total growth. This yields ~48 hourly + ~30 daily rows per key
  — permanently bounded.
- Read helpers to mirror: `GetHourlyAverageResponseTimeByKey`
  (`sql.go:213-235`), and the range-scan pattern at `sql.go:913-934`.
- Raw-result pruning for the short window: `maximum-number-of-results`
  (`storage/config.go:34-35`, default 100 at `:8`, set to 3000 in
  `config.yaml:9`) with `resultsAboveMaximumCleanUpThreshold = 10`
  (`sql.go:34`) so cleanup is amortised, not per-insert.

Applied to the side channels: 17 keys × (48 hourly + 30 daily) ≈ **1,326 rows
total, steady state** — versus the 1.3 M raw rows of §7.

### (b) `jira.Trend` / `DayPoint` — daily buckets, but NOT stored history

`jira/jira.go:43-48`:

```go
type DayPoint struct {
	Date     string `json:"date"` // YYYY-MM-DD
	Created  int    `json:"created"`
	Resolved int    `json:"resolved"`
}
```

Attached to `Project.Trend []DayPoint` (`jira/jira.go:85`), inside `Snapshot`
(`:90-99`), held in the same kind of in-memory single slot as the side channels
(`store` var, `jira/jira.go:101-107`; `GetSnapshot`/`setSnapshot` `:110-125`) —
**also not persisted, also lost on restart.**

How the trend is built — `buildTrend`, `jira/jira.go:666-690`:

1. Pre-allocate a **contiguous, gap-free** series of `days` points, oldest first,
   one per calendar day, in server local time (`:670-677`). Empty days therefore
   render as explicit zeroes rather than missing points.
2. Index by `YYYY-MM-DD` string key (`idx` map, `:669`, `:676`).
3. Bucket each raw record into its day via a closure that skips unparseable
   timestamps (`:678-686`), converting to `.Local()` before formatting (`:681`).
4. Two passes: `created` on `Fields.Created`, `resolved` on
   `Fields.ResolutionDate` (`:687-688`).

Source of the raw data — `jira/jira.go:359-368`: two JQL queries
(`created >= -Nd`, `resolutiondate >= -Nd`) re-fetched **on every poll**. So Jira
itself is the historical store; Gatus recomputes the whole trend from scratch each
cycle and keeps only the latest computed result. Companion windowed aggregates:
`windowCounts` (today / last-7d, `:692-710`) and `averageResolutionHours`
(`:712-727`).

**Why this is only half a precedent:** the *shape* is exactly right — dense
contiguous buckets, oldest first, `YYYY-MM-DD` (or hour-unix) keys, gap-filled so
the chart cannot lie by omission — and it is worth copying for the API response
format. But the *mechanism* does not transfer: Jira can be re-queried for its own
past, whereas a UniFi or PBX snapshot that was never written down is gone for
good. For the side channels there is no upstream to backfill from, which is
exactly why history has to start accumulating from the moment the write path
lands.

---

## Bottom line for planning

1. **Do not promise any historical side-channel data before the feature ships.**
   Every chart starts empty and fills forward. There is no backfill source:
   UniFi's cloud API and the Wildix PBX both serve *current* state only, and the
   collectors keep no local sample history (the only local file is
   `collector/.phones_state.json`, a high-water baseline mentioned at
   `collector/phone_collector.py:24`, not a series).
2. What *does* already have ~1 day raw / 30 day hourly history per key: pass/fail,
   response time, and the reason string — via the normal external-endpoint push
   into `endpoint_results` / `endpoint_uptimes` (§5, §8a).
3. Persist **counts only**, hourly-bucketed via upsert-with-sums, following
   `endpoint_uptimes` exactly. Keep a short raw window if per-minute resolution
   is wanted. Do not persist `phones[]`, `detail.aps[]` or `detail.wans[]`
   snapshots per sample (§7 byte math).
4. Schema must tolerate: `null` `txRetryPct`; entirely **absent** `counts` on the
   `fatal`/early-out paths; and pause gaps that must stay gaps
   (`monitoring/monitoring.go:15-17`).
5. `Counts` is `json.RawMessage` today — Gatus never parses it. Any history writer
   needs a typed unmarshal step (or a collector change to send typed fields), and
   should be resilient to unknown/missing keys since the collector can add fields
   without a Go change.
