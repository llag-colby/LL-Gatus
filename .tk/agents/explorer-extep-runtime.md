# Explorer findings: can an external endpoint be registered at runtime?

**Verdict: NO.** `external-endpoints` is a pure YAML-parsed slice on `config.Config`.
Nothing in the codebase ever appends to it, and every push route hard-gates on
`cfg.GetExternalEndpointByKey(key) != nil`. An unknown key gets a flat `404 not found`.

There IS a config hot-reload, but it is a **whole-process restart-in-place driven by
file mtime polling**, not dynamic registration — and in the current Docker layout
`config.yaml` is baked into the image, not bind-mounted, so the reloader can never fire.

---

## 1. Where the list lives and how it is loaded

`config/config.go:95-96`

```go
// ExternalEndpoints is the list of all external endpoints
ExternalEndpoints []*endpoint.ExternalEndpoint `yaml:"external-endpoints,omitempty"`
```

Populated only by `yaml.Unmarshal` in `parseAndValidateConfigBytes` — `config/config.go:291`:

```go
if err = yaml.Unmarshal(yamlBytes, &config); err != nil {
```

Called from `LoadConfiguration` (`config/config.go:199-258`), which reads a single file or
deep-merges a directory of `.yml`/`.yaml`, then stamps `config.configPath` and
`config.UpdateLastFileModTime()` (`config/config.go:255-256`).

Lookup is a linear scan, lowercased key compare — `config/config.go:162-170`:

```go
func (config *Config) GetExternalEndpointByKey(key string) *endpoint.ExternalEndpoint {
	for i := 0; i < len(config.ExternalEndpoints); i++ {
		ee := config.ExternalEndpoints[i]
		if ee.Key() == strings.ToLower(key) {
			return ee
		}
	}
	return nil
}
```

Key format — `config/endpoint/external_endpoint.go:85-87` → `config/key/key.go:6-8`:
`sanitize(group) + "_" + sanitize(name)`, e.g. `Phones` + `Ivory Tower` → `phones_ivory-tower`.

**Exhaustive list of every reference to `cfg.ExternalEndpoints` (non-test):**

| file:line | what it does |
|---|---|
| `config/config.go:96` | declaration |
| `config/config.go:163-164` | read (key lookup) |
| `config/config.go:304` | read (alerting validation) |
| `config/config.go:490, 501, 551` | read (validation, dup-key detection, logging) |
| `main.go:153` | read (collect keys to preserve in store) |
| `main.go:193` | read (restore triggered alerts) |
| `api/endpoint_check.go:69` | read |
| `watchdog/watchdog.go:45` | read (start heartbeat goroutines) |

**No `append`, no mutex, no setter. The slice is write-once at parse time.**

Validation would also reject a runtime-created entry with no token —
`config/endpoint/external_endpoint.go:58-60`:

```go
if len(externalEndpoint.Token) == 0 {
	return ErrExternalEndpointWithNoToken
}
```

---

## 2. `POST /api/v1/endpoints/:key/external` with an unconfigured key → **404**

`api/external_endpoint.go:36-41` — the decisive code:

```go
key := c.Params("key")
externalEndpoint := cfg.GetExternalEndpointByKey(key)
if externalEndpoint == nil {
	logr.Errorf("[api.CreateExternalEndpointResult] External endpoint with key=%s not found", key)
	return c.Status(404).SendString("not found")
}
```

Full order of checks in `CreateExternalEndpointResult` (`api/external_endpoint.go:19-109`):

1. `:22-26` — missing/invalid `?success=` → **400** `"missing or invalid success query parameter"`
2. `:28-31` — no `Bearer ` prefix → **401** `"invalid Authorization header"`
3. `:32-35` — empty token → **401** `"bearer token must not be empty"`
4. `:36-41` — **key not in config → 404 `"not found"`** ← the answer
5. `:42-45` — token mismatch → **401** `"invalid token"`
6. `:51-54` — `monitoring.IsPaused(key)` → **200** `"OK (monitoring paused)"`, result discarded
7. `:56-87` — build `endpoint.Result`, `store.Get().InsertEndpointResult(...)`
8. `:97-103` — alerting, `:104-106` — metrics, `:108` — **200** empty body

Note the check order: the 404 is returned **before** the token is compared, so an
unknown key is distinguishable from a bad token (a minor enumeration leak, not a bug).

Confirmed by the table test — `api/external_endpoint_test.go:61-66`:

```go
{
	Name:                           "bad-key",
	Path:                           "/api/v1/endpoints/bad_key/external?success=true",
	AuthorizationHeaderBearerToken: "Bearer token",
	ExpectedCode:                   404,
},
```

### The store *would* auto-create, but is never reached

`storage/store/sql/sql.go:267-287`:

```go
func (s *Store) InsertEndpointResult(ep *endpoint.Endpoint, result *endpoint.Result) error {
	...
	endpointID, err := s.getEndpointID(tx, ep)
	if err != nil {
		if errors.Is(err, common.ErrEndpointNotFound) {
			// Endpoint doesn't exist in the database, insert it
			if endpointID, err = s.insertEndpoint(tx, ep); err != nil {
```

So the **storage layer is fully lazy/auto-creating** — the only thing standing between a
collector push and a live dashboard row is the config lookup at `api/external_endpoint.go:37`.
That is the single chokepoint to change if runtime registration is ever wanted.

Reinforcing that: `api/endpoint_status.go:28` serves the dashboard list from the **store**,
not from config:

```go
endpointStatuses, err := store.Get().GetAllEndpointStatuses(paging.NewEndpointStatusParams()...)
```

**But** `main.go:148-166` garbage-collects on every startup/reload, deleting any store row
whose key is not in the freshly parsed config:

```go
for _, ee := range cfg.ExternalEndpoints {
	keys = append(keys, ee.Key())
}
...
numberOfEndpointStatusesDeleted := store.Get().DeleteAllEndpointStatusesNotInKeys(keys)
```

So even if a row were injected into the store directly, it would survive only until the
next restart or config reload.

---

## 3. Dynamic registration / hot-reload mechanisms

- **No `fsnotify`** — absent from `go.mod` and `go.sum` entirely.
- **No `SIGHUP`** — `main.go:52` handles only `os.Interrupt` and `syscall.SIGTERM`.
- **No reload API route** — nothing in `api/api.go:109-271` mutates config.
- **No admin/registration endpoint** of any kind.

What *does* exist: a 30-second **mtime poll that restarts the whole app in-process** —
`main.go:258-284`:

```go
func listenToConfigurationFileChanges(cfg *config.Config) {
	for {
		time.Sleep(30 * time.Second)
		if cfg.HasLoadedConfigurationBeenModified() {
			logr.Info("[main.listenToConfigurationFileChanges] Configuration file has been modified")
			stop(cfg)
			time.Sleep(time.Second)
			save()
			updatedConfig, err := loadConfiguration()
			...
			store.Get().Close()
			initializeStorage(updatedConfig)
			start(updatedConfig)
			return
		}
	}
}
```

Started at `main.go:87` (`go listenToConfigurationFileChanges(cfg)`).
Detection is mtime-only — `config/config.go:174-190`:

```go
return !fileInfo.ModTime().IsZero() && config.lastFileModTime.Unix() < fileInfo.ModTime().Unix()
```

On reload, `stop()` → `controller.Shutdown()` kills the fiber app (`controller/controller.go:44-49`)
and `start()` → `controller.Handle(updatedConfig)` builds an entirely new router
(`controller/controller.go:18-20`), so every handler closure re-binds to the new `*config.Config`.
That is why handlers capturing `cfg` by pointer at route-registration time is safe.

### Why the reload never fires in this deployment

`docker-compose.yml:12-13` mounts **only** the data volume:

```yaml
    volumes:
      - ./data:/data
```

`config.yaml` is **baked into the image** — `Dockerfile:25`:

```dockerfile
COPY --from=builder /app/config.yaml ./config/config.yaml
```

…landing at `config/config.yaml`, which is `DefaultConfigurationFilePath`
(`config/config.go:38`), with `GATUS_CONFIG_PATH=""` (`Dockerfile:27`). The final stage is
`FROM scratch` (`Dockerfile:23`) — no shell, no editor. Editing `config.yaml` on the host
changes nothing inside the running container, so its mtime never advances.

The config file documents exactly this — `config.yaml:229` and `config.yaml:280`:

```
# NOTE: external-endpoints only (re)load on `docker compose down && up -d`.
```

**Consequence: adding an external endpoint today requires editing `config.yaml`, rebuilding
the image, and recreating the container.**

---

## 4. `/api/v1/phones/:key` inventory push

`api/phones_inventory.go`.

**Storage: a plain in-process map. No database, no sidecar, no file.**
`api/phones_inventory.go:23-30`:

```go
var (
	phonesInventoryMu    sync.RWMutex
	phonesInventoryStore = make(map[string]storedInventory)
	...
)
```

Comment at `:17-22`: *"Inventory is ephemeral (the collector re-reports every sweep)."*
Lost on every restart; rebuilt within one collector sweep.

**Pre-registration: REQUIRED, and it is the very first thing checked** —
`api/phones_inventory.go:47-51`:

```go
key := c.Params("key")
externalEndpoint := cfg.GetExternalEndpointByKey(key)
if externalEndpoint == nil {
	return c.Status(404).SendString("not found")
}
```

Auth then reuses the same external-endpoint token — `:52-59`:

```go
if len(token) == 0 || externalEndpoint.Token != token {
	return c.Status(401).SendString("invalid token")
}
```

Body must be `{"phones": [...], "status": ..., "counts": {...}}` (`:60-67`, else 400).
Paused → 200 + discard (`:72-74`). Then map write (`:75-82`) and
`recordCounts(key, payload.Counts)` (`:86`).

**Read back:** `GetPhonesInventory` (`api/phones_inventory.go:93-102`) — pure map read,
**no config lookup, no auth**. 404 `{"error":"no inventory reported yet"}` if the key has
never pushed.

**The one thing that IS persisted here:** exclusions, to a JSON file, not a DB —
`api/phones_inventory.go:32`: `const exclusionsPath = "/data/phones_exclusions.json"`,
read at `:113` and written at `:121`. `SetPhonesExclusion` (`:142`) does **no** config
lookup and no token check — it is gated only by `requireOperator` at the route.

Sibling persisted-JSON stores, same pattern (useful precedents for any runtime registry):

- `api/phones_settings.go:29` — `/data/phones_settings.json` (thresholds)
- `monitoring/monitoring.go:30` — `/data/monitoring.json` (paused keys)
- `api/phones_sweep.go:16-19` — in-memory only, deliberately

---

## 5. UniFi side-channel (`api.SetUniFiSnapshot`)

`api/unifi_inventory.go` — structurally identical to phones.

**Storage: in-process map, nothing persisted** — `api/unifi_inventory.go:28-31`:

```go
var (
	unifiMu    sync.RWMutex
	unifiStore = make(map[string]storedUniFi)
)
```

Comment `:25-27`: *"Snapshots are ephemeral: the collector re-reports every sweep, and
nothing is persisted. A key that stops reporting simply goes stale."*

**Pre-registration: REQUIRED** — `api/unifi_inventory.go:46-50`:

```go
key := c.Params("key")
externalEndpoint := cfg.GetExternalEndpointByKey(key)
if externalEndpoint == nil {
	return c.Status(404).SendString("not found")
}
```

Token check `:51-58`; body must carry a non-empty `kind` (`firewall`|`wireless`) else 400
(`:66-68`); pause guard `:73-75`; map write `:76-85`; `recordCounts` `:89`.

**Read back:** `GetUniFiSnapshot` (`:96-105`, single key) and `GetUniFiSnapshots`
(`:111-119`, whole map for the dashboard) — both plain map reads, **no config lookup, no auth**.

---

## Metric history sidecar (the only durable collector-pushed data)

`recordCounts` (`api/history.go:27-42`) decodes `counts` and calls
`history.Record(endpointKey, metrics, time.Now())` (`api/history.go:41`).

`history/history.go:128-148` — schema keyed by a **bare TEXT endpoint key**:

```sql
CREATE TABLE IF NOT EXISTS samples (
  endpoint_key TEXT NOT NULL,
  metric       TEXT NOT NULL,
  ts           INTEGER NOT NULL,
  value        REAL NOT NULL,
  PRIMARY KEY (endpoint_key, metric, ts)
)
```

plus `rollups` (hourly). `history.Record` (`:172`) does **no** existence check and never
returns an error — **this is the one store in the whole system that needs no
pre-registration**. Opened at `main.go:72` from `historyDatabasePath = "/data/history.db"`
(`main.go:29`), deliberately outside the Gatus store so `main.go:163`'s cascading delete
cannot take history with it.

---

## Auth / token model for the push routes

All three collector push routes are registered on `unprotectedAPIRouter`, i.e. **BEFORE**
`ApplySecurityMiddleware` — `api/api.go:124` creates that group, and the security
middleware is only applied at `api/api.go:238-246` to a *separate* `protectedAPIRouter`.
`api/api.go:237` states the rule outright:

```go
// ORDER IS IMPORTANT: all routes applied AFTER the security middleware will require authn
```

| Route | file:line | Gate |
|---|---|---|
| `POST /v1/endpoints/:key/external` | `api/api.go:162` | per-endpoint bearer token, inside the handler |
| `POST /v1/phones/:key` | `api/api.go:166` | same token, inside the handler |
| `POST /v1/unifi/:key` | `api/api.go:186` | same token, inside the handler |
| `GET /v1/phones/:key`, `/v1/unifi`, `/v1/unifi/:key`, `/v1/history/:key` | `api/api.go:171, 184, 187, 191` | **none** — fully anonymous |
| `POST /v1/phones/:key/sweep`, `/exclusions`, `/settings`, `/v1/monitoring/:key` | `api/api.go:170, 173, 175, 180` | `requireOperator` (session cookie) |
| `/v1/users/*` | `api/api.go:139` | `requireAdmin` |

Three independent auth mechanisms, deliberately not wired together:

1. **Per-endpoint bearer token** (`ExternalEndpoint.Token`, `config/endpoint/external_endpoint.go:35`)
   — machine identity, checked inline in each push handler.
2. **Local session accounts** — `RequireRole` in `api/auth.go:51-72`, cookie-based,
   **fails OPEN** when the auth DB is unavailable (`api/auth.go:59-61`):
   ```go
   if !auth.Enabled() {
       return c.Next()
   }
   ```
3. **Gatus's built-in `security:` block** — `security/config.go:48` (`ApplySecurityMiddleware`,
   basicauth or OIDC-via-g8). **Not configured in this deployment** (no `security:` key in
   `config.yaml`), so `cfg.Security == nil` and `api/api.go:239` skips it entirely.

Rationale is documented at `api/api.go:157-161` and `api/auth.go:21-26`:

> COLLECTOR PUSH: no session gate, and it must stay that way. […] Nothing is signed in
> on the collector box, so requiring a cookie here takes the dashboard's whole data feed down.

Even if `security:` were enabled, the push routes would still be unaffected — they are
registered on the unprotected group.

Note the token in `config.yaml` is a **single shared value** for all endpoints:
`token: "${PHONES_PUSH_TOKEN}"` on every entry (`config.yaml:233, 236, 239, 242, 283, 286, 289`).

Collectors push to: `collector/phone_collector.py:317` and `collector/unifi_collector.py:400`.

---

## If runtime registration is wanted — the minimum surface

1. `api/external_endpoint.go:37`, `api/phones_inventory.go:48`, `api/unifi_inventory.go:47`
   — three identical `GetExternalEndpointByKey` gates.
2. `config/config.go:96` — the slice needs a mutex; it is read concurrently by the
   watchdog goroutines (`watchdog/watchdog.go:45`) and every request.
3. `main.go:153-166` — `DeleteAllEndpointStatusesNotInKeys` would wipe any
   runtime-registered endpoint on the next restart unless the registry is persisted and
   merged into `keys`.
4. `main.go:258-284` — a reload re-parses from disk and discards in-memory state, so a
   runtime registry must live outside `Config` (the `/data/*.json` pattern at
   `monitoring/monitoring.go:30` and `api/phones_settings.go:29` is the house style).
5. `watchdog/watchdog.go:45-52` — heartbeat goroutines are spawned once at startup; a
   late-registered endpoint with a heartbeat would get no monitor.
