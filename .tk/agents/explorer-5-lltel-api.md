# LL-Telemetry Backend — Exhaustive Reference (for Gatus/Go integration)

Source project: `C:\Users\colby.west\Desktop\Projects\LL-Telemetry`
App version: **0.4** (`APP_VERSION` at `telemetry/api/main.py:15`)
Stack: FastAPI (uvicorn) + PyMySQL + **MariaDB 11.4** + Caddy 2 (alpine) reverse proxy + static HTML dashboard.

Key files (all absolute):
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\api\main.py` (578 lines — the entire API)
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\api\Dockerfile`
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\api\requirements.txt`
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\api\tests\test_main.py`
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\api\tests\conftest.py`
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\caddy\Caddyfile`
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\caddy\Caddyfile.local`
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\db\01-schema.sql`
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\db\02-apikeys.sql`
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\docker-compose.yml`
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\docker-compose.local.yml`
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\.env.example`
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\dashboard\index.html` (619 lines, single-file console)
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\scripts\backup.sh`, `retention.sh`
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\agent\LL-Report-Block.ps1` (the client that POSTs)

---

## 1. THE COMPLETE API REFERENCE TABLE

Internal API listens on **`api:8080`** (uvicorn, container `lltel-api`). Every route is under `/api/v1`.
"Auth (app)" = what FastAPI itself enforces. "Auth (edge)" = what Caddy enforces in production.

| # | Method | Path | Auth (app) | Auth (edge, prod Caddy) | Params | Response shape | Purpose | main.py lines |
|---|--------|------|------------|--------------------------|--------|----------------|---------|---------------|
| 1 | GET | `/api/v1/health` | **none** | **none** (public on :443) | — | `{ok:bool, version:str}` | Liveness + DB ping. 503 if DB down. | 203–211 |
| 2 | POST | `/api/v1/runs` | **Bearer** (`llk_…` or legacy `LL_INGEST_TOKEN`) | none (Caddy passes through; body capped 2MB) | body `Run` | `{accepted, duplicate, run_id, site, key}` | **Ingest.** Idempotent on `run_id`. | 214–281 |
| 3 | GET | `/api/v1/runs` | **none** | **basic_auth** | `limit, offset, script, result, site, hostname, q, results, days` | `{count,total,limit,offset,runs:[…]}` | Paginated/filtered/searchable run list. | 284–337 |
| 4 | GET | `/api/v1/runs/{run_id}` | **none** | **basic_auth** | path `run_id` (the UUID, not `id`) | full run row incl. `log`, `details` | Drill-in detail for drawer. 404 if absent. | 340–355 |
| 5 | GET | `/api/v1/stats` | **none** | **basic_auth** | `days` (1–365, def 30) | 7-key aggregate object | Dashboard rollups + self-health. | 358–438 |
| 6 | GET | `/api/v1/timeline` | **none** | **basic_auth** | `hours` (1–2160, def 24), `buckets` (6–240, def 48) | `{hours,buckets,bucket_sec,results,series}` | Time-bucketed counts by result (severity ribbon). | 441–483 |
| 7 | GET | `/api/v1/keys` | **none** | **basic_auth** | — | `{keys:[…]}` | List ingest API keys (no secrets). | 499–513 |
| 8 | POST | `/api/v1/keys` | **none** | **basic_auth** | body `{label:str}` | **201** `{id,prefix,label,secret,created_utc}` | Create key; **plaintext returned once**. | 516–532 |
| 9 | POST | `/api/v1/keys/{key_id}/revoke` | **none** | **basic_auth** | path `key_id:int` | `{revoked:true,id}` | Revoke. 404 if missing/already revoked. | 535–545 |
| 10 | POST | `/api/v1/keys/{key_id}/rotate` | **none** | **basic_auth** | path `key_id:int` | **201** `{id,prefix,label,secret,rotated_from}` | Revoke old + mint new with same label. | 548–567 |
| 11 | DELETE | `/api/v1/keys/{key_id}` | **none** | **basic_auth** | path `key_id:int` | `{deleted:true,id}` | Hard delete row. 404 if missing. | 570–577 |
| — | any | any other `/api/*` | — | **405** hard-coded by Caddy | — | `"method or path not allowed"` | Catch-all deny. | Caddyfile:70–72 |
| — | GET | `/` and all non-`/api` | — | **basic_auth** + `file_server` | — | dashboard HTML | Static console from `/srv/dashboard`. | Caddyfile:74–80 |

**FastAPI freebies also exposed on :8080** (and reachable through prod Caddy? No — they are not `/api/*`, so they fall into the `handle {}` static block and 404 against the file server; on the **local** compose they ARE unreachable too since only `/api/*` proxies). Internally on `api:8080` you can still hit `/docs`, `/openapi.json`, `/redoc` — relevant if Gatus proxies directly to the container.

---

## 2. ROUTE-BY-ROUTE DETAIL WITH REALISTIC JSON

### 1) `GET /api/v1/health` — main.py:203–211
No auth anywhere. Opens a DB connection and runs `SELECT 1 AS ok`.

- **200**: `{"ok": true, "version": "0.4"}`
- **503**: `{"detail": "db unavailable: <exception text>"}` — note the raw exception is leaked into the detail.

This is the natural Gatus health-check target: `curl -k https://telemetry.longlewis.local/api/v1/health` per README §5.

### 2) `POST /api/v1/runs` — INGEST — main.py:214–281

**Auth**: `Authorization: Bearer <token>`. Details in §3.

**Request body — `Run` model (main.py:175–200).** Note `schema_version` uses **alias `schema`** with `populate_by_name=True`, so the wire field is `"schema"` (the PowerShell agent sends `$p.schema = 1`). Pydantic default-ignores unknown extra fields.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `schema` (alias of `schema_version`) | int | no | 1 | wire name is `schema` |
| `run_id` | str | **yes** | — | UUID string; unique key, drives dedupe. Clipped to 36 |
| `script` | str | **yes** | — | clipped 64 |
| `script_version` | str? | no | null | clipped 16 |
| `result` | str | **yes** | — | must be in `RESULTS` (case-insensitive), stored upper |
| `exit_code` | int? | no | null | |
| `started_utc` | str? | no | null | ISO8601; `Z` accepted; unparseable → NULL (no error) |
| `finished_utc` | str? | no | null | same |
| `duration_sec` | int? | no | null | |
| `hostname` | str | **yes** | — | clipped 64 |
| `serial` | str? | no | null | clipped 64 |
| `manufacturer` | str? | no | null | clipped 64 |
| `model` | str? | no | null | clipped 96 |
| `os` | str? | no | null | clipped 96 |
| `os_build` | str? | no | null | clipped 32 |
| `ad_domain` | str? | no | null | clipped 96 |
| `ip` | str? | no | null | clipped 45; **primary site-resolution input** |
| `mac` | str? | no | null | clipped 32 |
| `tech` | str? | no | null | clipped 96, e.g. `LL\jdoe` |
| `details` | object? | no | null | free-form, stored as JSON column |
| `log` | str? | no | null | **truncated to 300000 chars** (`MAX_LOG_CHARS`, line 23/236) |
| `queued_offline` | bool | no | false | stored as TINYINT |
| `tls_bypassed` | bool | no | false | stored as TINYINT |

**Server-computed, NOT accepted from client**: `received_utc` (server now, UTC naive), `site` (from `SITE_MAP`), `source_ip` (from XFF/peer), `key_label` (from the API key used).

**Allowed `result` values** — `RESULTS` set, main.py:41–44:
`HEALTHY, REPAIRED, FAILED, NOT_JOINED, NO_DC, OK, PARTIAL, ERROR, CANCELLED`

**Example request**
```http
POST /api/v1/runs HTTP/1.1
Authorization: Bearer llk_a1b2c3d4_9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c5b4a3928
Content-Type: application/json

{
  "schema": 1,
  "run_id": "11111111-1111-1111-1111-111111111111",
  "script": "LL-DomainTrust",
  "script_version": "0.2",
  "result": "REPAIRED",
  "exit_code": 0,
  "started_utc": "2026-08-17T14:03:11Z",
  "finished_utc": "2026-08-17T14:03:14Z",
  "duration_sec": 3,
  "hostname": "IT-WKS-014",
  "serial": "7X9QZ13",
  "manufacturer": "Dell Inc.",
  "model": "OptiPlex 7010",
  "os": "Microsoft Windows 11 Pro",
  "os_build": "26100",
  "ad_domain": "longlewis.local",
  "ip": "10.15.100.50",
  "mac": "AA-BB-CC-DD-EE-FF",
  "tech": "LL\\cwest",
  "details": {"secure_channel": false, "reset": true},
  "log": "…transcript, secrets already scrubbed client-side…",
  "queued_offline": false,
  "tls_bypassed": false
}
```

**200 response** (always 200 — even on duplicate):
```json
{"accepted": true, "duplicate": false, "run_id": "11111111-1111-1111-1111-111111111111", "site": "Ivory Tower", "key": "2026-Q3 USB batch"}
```
- `duplicate: true` when the `ON DUPLICATE KEY UPDATE run_id = run_id` no-op fired (`cur.rowcount == 0`, line 271).
- `site` is `null` when the IP matches no `SITE_MAP` entry.
- `key` is the key **label**, or `"shared-legacy"` for the env token.

**Status codes**
| Code | Cause | Line |
|---|---|---|
| 200 | accepted (incl. duplicate) | 278 |
| 401 | header not `Bearer ` prefixed | 216–217 |
| 401 | token empty, or neither `llk_`-prefixed nor equal to `LL_INGEST_TOKEN` (**pre-DB fast reject**) | 221–222 |
| 401 | `llk_` token not found / hash mismatch / revoked (post-DB) | 275–276 |
| 422 | `result` not in `RESULTS` | 224–225 |
| 422 | Pydantic validation (missing `run_id`/`script`/`result`/`hostname`, wrong types) | FastAPI default |
| 429 | rate limit; includes `Retry-After: <sec>` header | 228–234 |
| 500 | any DB exception, detail `write failed: <exc>` | 272–273 |
| 405 | (edge only) any non-POST to this path — Caddy | Caddyfile:70–72 |
| 413 | (edge only) body > 2MB — Caddy `request_body max_size` | Caddyfile:24–26 |

**Ordering quirk worth knowing for a Go reimplementation**: validation order is `Bearer` prefix → cheap token shape → `result` whitelist → **rate limit** → DB key validation. So a 429 can be returned *before* the token is actually verified against `api_keys`, and a `result` 422 is returned before rate limiting. Also note the fast-reject at line 221 means a valid managed key MUST literally start with `llk_`.

**Idempotency**: unique key `uq_run_id`; re-posting is a genuine no-op, nothing is updated.

### 3) `GET /api/v1/runs` — LIST/SEARCH — main.py:284–337

| Query param | Type | Default | Constraint | Effect |
|---|---|---|---|---|
| `limit` | int | 200 | ge=1, le=1000 | `LIMIT` |
| `offset` | int | 0 | ge=0 | `OFFSET` |
| `days` | int | 30 | ge=1, le=365 | `received_utc >= UTC_TIMESTAMP() - INTERVAL ? DAY` (always applied) |
| `script` | str? | — | — | exact `script = ?` |
| `result` | str? | — | — | exact `result = ?` (case-sensitive as given) |
| `site` | str? | — | — | exact `site = ?` |
| `hostname` | str? | — | — | exact `hostname = ?` |
| `results` | str? | — | — | comma list → `result IN (…)`, each `.strip().upper()` |
| `q` | str? | — | — | free-text `LIKE %q%` across `hostname, script, site, tech, result, log` (6 binds) |

All filters are parameterized (`%s` binds) — no injection surface; the only f-string interpolation is the assembled `WHERE` fragment of fixed column names.

Ordering is fixed: `ORDER BY received_utc DESC`.

**Selected columns** (deliberately excludes `log`, `started_utc`, `finished_utc`, `manufacturer`, `os`, `mac`, `ad_domain`, `source_ip`):
`id, run_id, received_utc, script, script_version, result, exit_code, hostname, serial, model, os_build, ip, site, tech, duration_sec, queued_offline, tls_bypassed, key_label, details`

**Example**: `GET /api/v1/runs?days=7&limit=50&offset=0&q=trust&site=Ivory%20Tower`
```json
{
  "count": 1,
  "total": 42,
  "limit": 50,
  "offset": 0,
  "runs": [
    {
      "id": 981,
      "run_id": "11111111-1111-1111-1111-111111111111",
      "received_utc": "2026-08-17T14:03:15Z",
      "script": "LL-DomainTrust",
      "script_version": "0.2",
      "result": "REPAIRED",
      "exit_code": 0,
      "hostname": "IT-WKS-014",
      "serial": "7X9QZ13",
      "model": "OptiPlex 7010",
      "os_build": "26100",
      "ip": "10.15.100.50",
      "site": "Ivory Tower",
      "tech": "LL\\cwest",
      "duration_sec": 3,
      "queued_offline": 0,
      "tls_bypassed": 0,
      "key_label": "2026-Q3 USB batch",
      "details": {"secure_channel": false, "reset": true}
    }
  ]
}
```
Notes for a Go client: `queued_offline` / `tls_bypassed` come back as **integers 0/1** here (raw TINYINT), NOT booleans — unlike `/api/v1/keys` where `revoked` is coerced to bool at line 512. `received_utc` is `"…isoformat…Z"` with **no timezone offset in the isoformat** (naive datetime + literal `Z` suffix, `iso()` at line 171–172), e.g. `2026-08-17T14:03:15Z`; microseconds appear only if non-zero. `details` is JSON-decoded into an object when it parses, otherwise left as the raw string.

### 4) `GET /api/v1/runs/{run_id}` — main.py:340–355
`SELECT *` by **`run_id`** (the UUID column), not the numeric `id`. Returns every column including the full `log` (MEDIUMTEXT, up to 300k chars) and `source_ip`. `received_utc`/`started_utc`/`finished_utc` are ISO+`Z`; `details` JSON-decoded. **404** `{"detail":"not found"}` if absent.

```json
{
  "id": 981, "run_id": "1111…", "received_utc": "2026-08-17T14:03:15Z",
  "started_utc": "2026-08-17T14:03:11Z", "finished_utc": "2026-08-17T14:03:14Z",
  "duration_sec": 3, "script": "LL-DomainTrust", "script_version": "0.2",
  "result": "REPAIRED", "exit_code": 0, "hostname": "IT-WKS-014",
  "serial": "7X9QZ13", "manufacturer": "Dell Inc.", "model": "OptiPlex 7010",
  "os": "Microsoft Windows 11 Pro", "os_build": "26100",
  "ad_domain": "longlewis.local", "ip": "10.15.100.50", "mac": "AA-BB-CC-DD-EE-FF",
  "site": "Ivory Tower", "tech": "LL\\cwest",
  "details": {"secure_channel": false, "reset": true},
  "log": "…full transcript…",
  "source_ip": "10.15.100.50", "queued_offline": 0, "tls_bypassed": 0,
  "key_label": "2026-Q3 USB batch"
}
```

### 5) `GET /api/v1/stats?days=30` — main.py:358–438
Six queries + one view read. Response keys, in order:

| Key | Source | Row shape |
|---|---|---|
| `by_script_result` | `GROUP BY script, result` | `{script, result, n}` |
| `by_site` | `GROUP BY site`, `COALESCE(site,'unmapped')` | `{site, n, failures}` — `failures` counts `FAILED/NO_DC/ERROR` |
| `repeat_offenders` | `SELECT * FROM v_repeat_offenders LIMIT 25` (**not** day-filtered) | `{hostname, site, model, trust_runs, repairs, last_seen}` |
| `tls_bypassed` | `WHERE tls_bypassed = 1` GROUP BY host+site, LIMIT 25 | `{hostname, site, n, last_seen}` |
| `unmapped_subnets` | `WHERE site IS NULL` GROUP BY `COALESCE(ip, source_ip)`, LIMIT 25 | `{addr, n, last_seen}` |
| `daily` | `GROUP BY DATE(received_utc)` | `{d:"2026-08-17", n, failures}` |
| `totals` | one row | `{total, hosts, offline_queued, tls_bypassed, avg_sec}` |
| `service` | self-health | `{version, last_received_utc, runs_last_hour}` — **never day-filtered**; `runs_last_hour` counts last 1 hour across the whole table |

```json
{
  "by_script_result": [{"script":"LL-DomainTrust","result":"HEALTHY","n":118}],
  "by_site": [{"site":"Ivory Tower","n":140,"failures":6},{"site":"unmapped","n":3,"failures":0}],
  "repeat_offenders": [{"hostname":"IT-WKS-014","site":"Ivory Tower","model":"OptiPlex 7010","trust_runs":9,"repairs":4,"last_seen":"2026-08-17T14:03:15Z"}],
  "tls_bypassed": [{"hostname":"MS-WKS-003","site":"Muscle Shoals","n":2,"last_seen":"2026-08-16T09:12:00Z"}],
  "unmapped_subnets": [{"addr":"10.22.7.14","n":3,"last_seen":"2026-08-15T18:40:02Z"}],
  "daily": [{"d":"2026-08-16","n":62,"failures":3},{"d":"2026-08-17","n":81,"failures":3}],
  "totals": {"total":143,"hosts":57,"offline_queued":4,"tls_bypassed":2,"avg_sec":3},
  "service": {"version":"0.4","last_received_utc":"2026-08-17T14:03:15Z","runs_last_hour":11}
}
```
`service.last_received_utc` + `runs_last_hour` are exactly what a Gatus "is telemetry still ingesting?" check would key on.

### 6) `GET /api/v1/timeline` — main.py:441–483
`hours` ge=1 le=**2160** (24*90); `buckets` ge=6 le=240 (def 48).
`bucket_sec = max(1, (hours*3600)//buckets)`. SQL floors `TIMESTAMPDIFF(SECOND, now-hours, received_utc)/bucket_sec`, groups by bucket+result. Python then builds a **dense** array of exactly `buckets` entries (zero-filled), clamping out-of-range indices into `[0, buckets-1]`. `t` is **epoch milliseconds** at bucket start.

```json
{
  "hours": 24, "buckets": 48, "bucket_sec": 1800,
  "results": ["FAILED", "HEALTHY"],
  "series": [
    {"t": 1755352995000, "counts": {"HEALTHY": 3}, "total": 3},
    {"t": 1755354795000, "counts": {}, "total": 0}
  ]
}
```
`results` is the sorted set of result values actually observed in the window.

### 7–11) API keys — main.py:491–577

`GET /api/v1/keys` → ordered `revoked ASC, created_utc DESC`; never returns hashes or secrets.
```json
{"keys":[{"id":3,"prefix":"llk_a1b2c3d4","label":"2026-Q3 USB batch","created_utc":"2026-08-01T10:00:00Z","last_used_utc":"2026-08-17T14:03:15Z","use_count":412,"revoked":false,"revoked_utc":null}]}
```

`POST /api/v1/keys` body `{"label":"2026-Q3 USB batch"}`; label `.strip()[:96]`; **422** `{"detail":"label required"}` if blank. **201**:
```json
{"id":4,"prefix":"llk_a1b2c3d4","label":"2026-Q3 USB batch","secret":"llk_a1b2c3d4_9f8e…3928","created_utc":"2026-08-17T15:00:00Z"}
```
`secret` is shown **once and never again** — only `prefix` + sha256 are persisted.

`POST /api/v1/keys/{id}/revoke` → `{"revoked":true,"id":5}`; 404 `key not found or already revoked` (guarded by `AND revoked = 0`).

`POST /api/v1/keys/{id}/rotate` → 404 `key not found` if the id doesn't exist; otherwise revokes the old row and inserts a new row with the same label. **201**:
```json
{"id":9,"prefix":"llk_77aa11bb","label":"2026-Q3 USB batch","secret":"llk_77aa11bb_…","rotated_from":5}
```
Note: rotate has **no `created_utc`** in its response (create does). Also rotate/revoke/create run without an explicit transaction — the connection is `autocommit=True` (line 65), so rotate's revoke+insert are two independent commits.

`DELETE /api/v1/keys/{id}` → `{"deleted":true,"id":5}`; 404 `key not found`. Hard delete; historical `runs.key_label` rows keep the label string (denormalized on purpose).

---

## 3. AUTH MODEL — EXACTLY WHERE THE SPLIT LIVES

### Write auth (ingest) — enforced **in FastAPI only**
Caddy proxies `POST /api/v1/runs` with **no** basic auth (Caddyfile:19–28) — it must, because field scripts only carry a Bearer token. So:

1. `main.py:216` — header must literally start with `"Bearer "`, else 401.
2. `main.py:221` — fast reject: token must be non-empty AND (`startswith("llk_")` OR `== INGEST_TOKEN`). Avoids a DB connect for garbage.
3. `check_ingest_key()` (main.py:88–111), called **inside** the DB transaction at line 255:
   - If token starts with `llk_`: split on `_`, require ≥3 parts, rebuild `prefix = parts[0]_parts[1]` (i.e. `llk_a1b2c3d4`), `SELECT id, key_hash, label, revoked FROM api_keys WHERE prefix = %s`.
   - Accept only if row exists AND `not revoked` AND `sha256(full_token).hexdigest() == row.key_hash`.
     - **Comparison is a plain `==`**, not `hmac.compare_digest` — not constant time (a theoretical timing oracle on a hash, low practical risk).
   - On accept: `UPDATE api_keys SET last_used_utc = <utcnow naive>, use_count = use_count + 1 WHERE id = ?`. Returns `(True, label)`.
   - Else falls through to `(False, None)` — **an `llk_` token never falls back to the legacy env token**.
   - Non-`llk_` token: accepted iff `INGEST_TOKEN` is truthy and `token == INGEST_TOKEN`, label `"shared-legacy"` (also a non-constant-time compare).

**Hashing at rest**: `hash_key()` (main.py:77–78) = plain `hashlib.sha256(token.encode()).hexdigest()` → 64 hex chars, stored in `api_keys.key_hash CHAR(64)`. **No salt, no KDF** — acceptable because the key is 24 bytes of `secrets.token_hex` entropy (not a guessable password), but it is not password hashing.

**Key generation** (`generate_key()`, main.py:81–85):
```
prefix = "llk_" + secrets.token_hex(4)          # llk_ + 8 hex  = 12 chars
full   = f"{prefix}_{secrets.token_hex(24)}"    # + "_" + 48 hex = 61 chars total
```
So the wire format is `llk_<8hex>_<48hex>`, 61 chars, ~192 bits of secret entropy. `api_keys.prefix VARCHAR(20)` comfortably holds the 12-char prefix.

### Read auth — enforced **in Caddy only**
**Every read endpoint (`/runs` GET, `/runs/{id}`, `/stats`, `/timeline`) and EVERY key-admin endpoint has ZERO auth in the FastAPI code.** There is no dependency, no header check, nothing. Read auth is 100% `basic_auth {$LL_DASH_USER} {$LL_DASH_HASH}` in the Caddyfile.

**Consequence that matters enormously for Gatus**: if a Go service reaches `api:8080` directly (same docker network, or by publishing the API port), it gets **unauthenticated full read access, including full logs, plus the ability to mint/revoke/delete ingest API keys**. The API container is deliberately *not* port-published in either compose file; only Caddy is. Any Gatus integration must preserve that, i.e. keep the API unpublished and either (a) proxy through Caddy carrying the basic-auth credential, or (b) be on the internal network and treat direct `:8080` access as privileged.

### `docs/SECURITY.md` / README §8 stated model
| Control | Detail |
|---|---|
| Ingest auth | Bearer token, checked server side |
| Ingest surface | `POST /api/v1/runs` only; Caddy returns 405 for anything else under `/api/` |
| Read auth | Separate basic-auth credential; the ingest token cannot read anything |
| Blast radius on token leak | Attacker can write junk rows. No read, no lateral movement |
| Body size | 2 MB at Caddy; `log` truncated to 300k chars at the API |
| Replay safety | `run_id` unique → re-post is a no-op |
| Rate limit | Per source IP; 429 makes the client re-queue, no data lost |
| Secret scrubbing | Client-side: the PS reporting block redacts password/token/secret/psk/apikey patterns before the log leaves the PC |
| Rotation | Change `LL_INGEST_TOKEN`, restart, redistribute scripts. Quarterly. |

### Production `Caddyfile` — exact rules
File: `telemetry/caddy/Caddyfile` (89 lines).

Global (lines 1–6): `admin off`; `servers { trusted_proxies static private_ranges }` — so `X-Forwarded-For` from private ranges is trusted, which is what makes `client_ip()` correct.

Site block: `telemetry.longlewis.local, 10.15.102.8` (line 8) — **both** the DNS name and bare IP are site addresses, hence the cert needs both SANs.
- `tls /certs/telemetry.crt /certs/telemetry.key` (line 10) — internal-CA cert, mounted read-only.
- `log` → `/data/access.log`, roll 20mb × 10 (lines 12–17).

Matchers and handlers **in order** (first match wins):

| Order | Matcher | Condition | Handler |
|---|---|---|---|
| 1 | `@ingest` (19–28) | `method POST` **AND** `path /api/v1/runs` (exact, no wildcard) | `request_body { max_size 2MB }` then `reverse_proxy api:8080`. **No basic auth.** |
| 2 | `@health` (30–36) | `method GET` AND `path /api/v1/health` (exact) | `reverse_proxy api:8080`. **No auth — publicly readable on the LAN.** |
| 3 | `@read` (38–47) | `method GET` AND `path /api/v1/runs*` **or** `/api/v1/timeline*` | `basic_auth` + proxy |
| 4 | `@stats` (49–58) | `method GET` AND `path /api/v1/stats*` | `basic_auth` + proxy |
| 5 | `@admin` (60–68) | `path /api/v1/keys*` — **no method restriction**, so GET/POST/DELETE all allowed | `basic_auth` + proxy |
| 6 | `handle /api/*` (70–72) | anything else under `/api/` | **`respond "method or path not allowed" 405`** |
| 7 | `handle {}` (74–80) | everything else | `basic_auth` + `root * /srv/dashboard` + `file_server` |

Response headers on the whole site (82–87): `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `-Server` (strip Server header).

**The 405 rule (lines 70–72)** is the linchpin of "ingest surface = POST /runs only": a leaked ingest token can't `GET /api/v1/runs` because that GET path *is* matched by `@read` → basic auth challenge (401), and anything not matched by rules 1–5 (e.g. `DELETE /api/v1/runs`, `POST /api/v1/stats`, `GET /api/v1/health` with a trailing segment, `/api/v1/anything-else`, `/docs`, `/openapi.json` under `/api`) gets a flat **405** with a plaintext body. Note that `/docs` and `/openapi.json` are NOT under `/api/`, so they hit rule 7 and 404 out of the file server (behind basic auth).

Also note rule 3's `path /api/v1/runs*` with a trailing `*` covers both the list and `/runs/{id}`, and covers query strings.

### `Caddyfile.local` — dev only
File: `telemetry/caddy/Caddyfile.local` (22 lines). Header comment: *"LOCAL DEVELOPMENT ONLY — http://localhost:4554, no TLS, no auth… Do not ship this file."*
- Global: `admin off`, **`auto_https off`**.
- Site `:4554`: `handle /api/* { reverse_proxy api:8080 }` — **no auth, no method matchers, no 405 rule, no 2MB body cap**; `handle { root * /srv/dashboard; file_server }`.
- Headers: only `X-Content-Type-Options nosniff` and `-Server`.

So in local mode the entire API including the keys admin is wide open on `http://localhost:4554/api/v1/...` with no credentials. **This is the easiest environment to develop a Gatus integration against.**

---

## 4. `SITE_MAP` — verbatim

main.py:32–39:
```python
# Site is resolved here, not in the scripts. Edit this map and restart the
# container. No script redistribution, no USB rebuild.
SITE_MAP = [
    ("10.15.100.0/22", "Ivory Tower"),
    ("10.15.104.0/22", "Ivory Tower"),
    ("10.6.81.0/24",   "Muscle Shoals"),
    ("10.10.1.0/24",   "Bramlett / Decatur"),
]
```

**4 CIDR entries → 3 distinct site names**: `Ivory Tower` (two /22s: 10.15.100.0–10.15.103.255 and 10.15.104.0–10.15.107.255), `Muscle Shoals` (10.6.81.0/24), `Bramlett / Decatur` (10.10.1.0/24).

Resolution logic `resolve_site()` (main.py:141–151): strips any `/len` suffix, `ipaddress.ip_address()` (returns `None` on `ValueError`, so garbage IPs are tolerated), then first-match linear scan. At ingest (line 237): `site = resolve_site(run.ip) or resolve_site(src)` — the **client-reported IP wins**, falling back to the observed source IP. Unmatched → `NULL` in the DB, surfaced as `"unmapped"` in `/stats.by_site` and listed in `/stats.unmapped_subnets` as the operator's to-do.

`client_ip()` (main.py:163–168): first comma-segment of `X-Forwarded-For`, trimmed; falls back to `request.client.host`. Uvicorn runs with `--proxy-headers --forwarded-allow-ips *` (Dockerfile) and Caddy sets XFF with `trusted_proxies static private_ranges`.

Cross-reference: Gatus's own site naming uses "Alabaster", "Decatur", "Ivory Tower", "Cullman" etc. — the telemetry site labels overlap only partially (`Ivory Tower` matches; `Bramlett / Decatur` is a compound label, `Muscle Shoals` and `Alabaster`/`Cullman` do not line up). Any join between the two systems needs an explicit site-name mapping table.

---

## 5. DATABASE — MariaDB 11.4

Both init files use `USE lltelemetry`, `ENGINE=InnoDB`, `utf8mb4 / utf8mb4_unicode_ci`.

### `db/01-schema.sql` (72 lines)
`CREATE DATABASE IF NOT EXISTS lltelemetry CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;`

#### Table `runs`
| Column | Type | Null | Default | Notes |
|---|---|---|---|---|
| `id` | BIGINT UNSIGNED AUTO_INCREMENT | NO | — | PK |
| `run_id` | CHAR(36) | NO | — | UUID, `UNIQUE uq_run_id` → idempotency |
| `received_utc` | DATETIME | NO | — | server-set at ingest |
| `started_utc` | DATETIME | YES | NULL | client-reported |
| `finished_utc` | DATETIME | YES | NULL | client-reported |
| `duration_sec` | INT | YES | NULL | |
| `script` | VARCHAR(64) | NO | — | |
| `script_version` | VARCHAR(16) | YES | NULL | |
| `result` | VARCHAR(32) | NO | — | stored uppercase |
| `exit_code` | INT | YES | NULL | |
| `hostname` | VARCHAR(64) | NO | — | |
| `serial` | VARCHAR(64) | YES | NULL | |
| `manufacturer` | VARCHAR(64) | YES | NULL | |
| `model` | VARCHAR(96) | YES | NULL | |
| `os` | VARCHAR(96) | YES | NULL | |
| `os_build` | VARCHAR(32) | YES | NULL | |
| `ad_domain` | VARCHAR(96) | YES | NULL | |
| `ip` | VARCHAR(45) | YES | NULL | 45 = IPv6 max |
| `mac` | VARCHAR(32) | YES | NULL | |
| `site` | VARCHAR(48) | YES | NULL | server-resolved; NULL = unmapped |
| `tech` | VARCHAR(96) | YES | NULL | `DOMAIN\user` |
| `details` | JSON | YES | NULL | free-form |
| `log` | MEDIUMTEXT | YES | NULL | ≤300k chars enforced in app |
| `source_ip` | VARCHAR(45) | YES | NULL | observed (XFF) |
| `queued_offline` | TINYINT(1) | NO | 0 | |
| `tls_bypassed` | TINYINT(1) | NO | 0 | flags machines missing the root CA |
| `key_label` | VARCHAR(96) | YES | NULL | **added by 02-apikeys.sql**, `AFTER tls_bypassed` |

**Indexes**: `PRIMARY KEY (id)`; `UNIQUE uq_run_id (run_id)`; `ix_host_time (hostname, received_utc)`; `ix_script (script, received_utc)`; `ix_result (result, received_utc)`; `ix_site (site, received_utc)`; `ix_serial (serial)`; `ix_received (received_utc)`.

Index gap worth noting: the `q=` free-text search does `LIKE '%…%'` over six columns **including `log` (MEDIUMTEXT)** — that is a full table scan with no index available, and it is the one endpoint that will get slow as the table grows. No FULLTEXT index exists.

#### View `v_latest_per_host` (schema lines 48–58)
```sql
CREATE OR REPLACE VIEW v_latest_per_host AS
SELECT r.* FROM runs r
JOIN (SELECT hostname, script, MAX(received_utc) AS mx FROM runs GROUP BY hostname, script) x
  ON x.hostname = r.hostname AND x.script = r.script AND x.mx = r.received_utc;
```
Latest run per (hostname, script). **Currently unused by the API** — no endpoint references it. It is available for a "current state per machine" view, which is likely the shape Gatus would want.

#### View `v_repeat_offenders` (schema lines 60–71)
```sql
CREATE OR REPLACE VIEW v_repeat_offenders AS
SELECT hostname, site, model,
       COUNT(*)                 AS trust_runs,
       SUM(result = 'REPAIRED') AS repairs,
       MAX(received_utc)        AS last_seen
FROM runs
WHERE script = 'LL-DomainTrust'
GROUP BY hostname, site, model
HAVING repairs >= 2
ORDER BY repairs DESC, last_seen DESC;
```
Machines whose domain trust has been repaired ≥2 times — the "this box keeps breaking" list. **Consumed by `/api/v1/stats` → `repeat_offenders` (LIMIT 25)**, hard-wired to `script = 'LL-DomainTrust'` and NOT time-windowed.

### `db/02-apikeys.sql` (22 lines)
Header: *"Runs on fresh init (docker-entrypoint-initdb.d) and is safe to re-run as a migration on an existing database (IF NOT EXISTS throughout)."*

#### Table `api_keys`
| Column | Type | Null | Default | Notes |
|---|---|---|---|---|
| `id` | INT UNSIGNED AUTO_INCREMENT | NO | — | PK |
| `prefix` | VARCHAR(20) | NO | — | plaintext id, e.g. `llk_a1b2c3d4`; `UNIQUE uq_prefix` |
| `key_hash` | CHAR(64) | NO | — | sha256 hex of full key; secret never stored |
| `label` | VARCHAR(96) | NO | — | e.g. `"2026-Q3 USB batch"` |
| `created_utc` | DATETIME | NO | — | |
| `last_used_utc` | DATETIME | YES | NULL | bumped on every accepted ingest |
| `use_count` | INT UNSIGNED | NO | 0 | incremented on every accepted ingest |
| `revoked` | TINYINT(1) | NO | 0 | |
| `revoked_utc` | DATETIME | YES | NULL | |

**Indexes**: `PRIMARY KEY (id)`; `UNIQUE uq_prefix (prefix)`; `ix_hash (key_hash)`; `ix_revoked (revoked)`. (Lookup is by `prefix`, so `ix_hash` is currently unused by the code path.)

Then: `ALTER TABLE runs ADD COLUMN IF NOT EXISTS key_label VARCHAR(96) NULL AFTER tls_bypassed;`

#### ⚠️ DEPLOYMENT BUG — `02-apikeys.sql` is never mounted
Both compose files mount **only** `./db/01-schema.sql` into `/docker-entrypoint-initdb.d/`:
- `docker-compose.yml:14` — `- ./db/01-schema.sql:/docker-entrypoint-initdb.d/01-schema.sql:ro`
- `docker-compose.local.yml:21` — same single line

`02-apikeys.sql` is **not** in either volume list. On a fresh stack there is no `api_keys` table and no `runs.key_label` column, so all five key endpoints 500 and any `llk_` ingest returns 401. It must be applied manually (`docker exec -i lltel-db mariadb -u root -p… lltelemetry < db/02-apikeys.sql`). Flag this before relying on v0.4 key features. Also note `docker-entrypoint-initdb.d` only runs on an **empty** data dir, and the local compose deliberately shares the `lltel-data` volume with prod, so re-running init is not a fix on an existing volume.

Every write happens on `autocommit=True` connections (main.py:65); there are no explicit transactions anywhere.

---

## 6. CONFIG / ENV

### `telemetry/.env.example` (31 lines)
Header: *"Copy to .env, fill in, then: chmod 600 .env"*

| Variable | Secret? | Default in code | Consumed by | Purpose |
|---|---|---|---|---|
| `LL_INGEST_TOKEN` | **YES** | **required, no default** — `os.environ[...]` at main.py:17, container crashes at import if unset | api | Legacy shared ingest bearer. `openssl rand -hex 32`. Goes in every field script. Write-only, POST `/runs` only. |
| `LL_DB_PASS` | **YES** | **required** — `os.environ[...]` main.py:20 | api + db | App DB user (`lltel`) password |
| `LL_DB_ROOT_PASS` | **YES** | — | db, `backup.sh`, `retention.sh` | MariaDB root; the scripts `set -a; . .env` to read it |
| `LL_DASH_USER` | no | — | caddy | Basic-auth username (example: `itadmin`) |
| `LL_DASH_HASH` | **YES** (bcrypt) | — | caddy | `caddy hash-password` bcrypt. **Every `$` must be doubled** (`$2a$14$…` → `$$2a$$14$$…`) because compose interpolates `$`. Token/DB passwords contain no `$`. |
| `LL_INGEST_RATE_MAX` | no | `300` (main.py:29, and `:-300` in compose) | api | Max ingests per IP per window |
| `LL_INGEST_RATE_WINDOW_SEC` | no | `300` (main.py:30, `:-300` in compose) | api | Window seconds |
| `LL_RETENTION_DAYS` | no | `365` (`retention.sh`) | retention.sh | Delete `runs` older than N days |

Not in `.env.example` but read by `main.py` (compose sets them literally): `LL_DB_HOST` (default `db`), `LL_DB_USER` (default `lltel`), `LL_DB_NAME` (default `lltelemetry`).

### `telemetry/docker-compose.yml` — PRODUCTION (62 lines)
`name: ll-telemetry`. Default bridge network (implicit `ll-telemetry_default`); **no explicit networks block**, so all three services resolve each other by service name.

| Service | Image/Build | Container | Restart | Ports | Volumes | Env | Healthcheck | depends_on |
|---|---|---|---|---|---|---|---|---|
| `db` | `mariadb:11.4` | `lltel-db` | unless-stopped | **none published** | `./db/01-schema.sql:/docker-entrypoint-initdb.d/01-schema.sql:ro`, `lltel-data:/var/lib/mysql` | `MARIADB_ROOT_PASSWORD`, `MARIADB_DATABASE=lltelemetry`, `MARIADB_USER=lltel`, `MARIADB_PASSWORD` | `healthcheck.sh --connect --innodb_initialized`, interval 10s, timeout 5s, retries 12, start_period 60s | — |
| `api` | `build: ./api` | `lltel-api` | unless-stopped | **none published — internal only, `api:8080`** | none | `LL_INGEST_TOKEN`, `LL_DB_HOST=db`, `LL_DB_USER=lltel`, `LL_DB_PASS`, `LL_DB_NAME=lltelemetry`, `LL_INGEST_RATE_MAX:-300`, `LL_INGEST_RATE_WINDOW_SEC:-300` | **none** | `db` with `condition: service_healthy` |
| `caddy` | `caddy:2-alpine` | `lltel-caddy` | unless-stopped | **`443:443`, `80:80`** | `./caddy/Caddyfile:/etc/caddy/Caddyfile:ro`, `./caddy/certs:/certs:ro`, `./dashboard:/srv/dashboard:ro`, `caddy-data:/data`, `caddy-config:/config` | `LL_DASH_USER`, `LL_DASH_HASH` | **none** | `api` (plain, no condition) |

Named volumes: `lltel-data`, `caddy-data`, `caddy-config`.

**API internal port: 8080.** From `api/Dockerfile`: `FROM python:3.12-slim`, `EXPOSE 8080`, `CMD ["uvicorn","main:app","--host","0.0.0.0","--port","8080","--proxy-headers","--forwarded-allow-ips","*"]` — single worker, no `--workers` flag (relevant to the in-process rate limiter, §7).

### `telemetry/docker-compose.local.yml` — DEV (62 lines)
Same `name: ll-telemetry`, so it **reuses the same containers and the same `lltel-data` volume** as production (header comment says so explicitly: "so seeded data persists"). Launch:
```
docker compose --env-file telemetry/.env -f telemetry/docker-compose.local.yml up -d
```
Differences from prod:
- `caddy` publishes **`4554:4554`** only (no 80/443), mounts `./caddy/Caddyfile.local` instead of `Caddyfile`, and mounts **no `./caddy/certs`**, and receives **no `LL_DASH_USER`/`LL_DASH_HASH`**.
- `api` omits `LL_INGEST_RATE_MAX` / `LL_INGEST_RATE_WINDOW_SEC` (falls back to the code defaults 300/300).
- Everything else (db service, volumes, healthcheck) is identical.

**What 4554 exposes**: the whole dashboard at `http://localhost:4554/` and the **entire unauthenticated API** at `http://localhost:4554/api/v1/*` — including `POST/DELETE /api/v1/keys*`. Ingest still requires a valid Bearer token (app-level), but reads and key admin do not.

---

## 7. RATE LIMITING, PAGINATION, RETENTION, BODY LIMITS

### Rate limiting — main.py:114–138, applied only at line 228
- **In-process, in-memory sliding window**, keyed by source IP, guarded by a `threading.Lock` (`_rate_lock`, line 58) over `_rate_hits: dict[str, list[float]]`.
- Limits: `RATE_MAX` = `LL_INGEST_RATE_MAX` or **300**; `RATE_WINDOW` = `LL_INGEST_RATE_WINDOW_SEC` or **300** seconds. So 300 ingests / 5 min / IP by default.
- Uses `time.monotonic()`. Evicts stamps older than the cutoff from the head of the list, then rejects if `len(hits) >= RATE_MAX`, returning `retry = int(hits[0] + RATE_WINDOW - now) + 1`, min 1.
- **Empty IP is never limited** (`if not ip: return True, 0`) — so a request with no XFF and no peer address bypasses the limiter entirely.
- Opportunistic GC: when `len(_rate_hits) > 4096`, purge keys with empty lists.
- **Applies ONLY to `POST /api/v1/runs`.** Read endpoints, stats, timeline, and all key-admin endpoints are completely unlimited.
- **Not shared across processes** — uvicorn runs 1 worker, so it holds today; scaling to multiple workers/replicas silently multiplies the effective limit.
- 429 body `{"detail":"ingest rate limit exceeded"}` + `Retry-After` header. Design intent (comment at lines 25–28): a 429 just makes the client re-queue, so no data is lost.

### Pagination
Only on `GET /api/v1/runs`: `limit` (1–1000, default 200) + `offset` (≥0), enforced by FastAPI `Query(ge=…, le=…)` → out-of-range gives 422. Response echoes `limit`/`offset` and returns both `count` (rows in this page) and `total` (full matching count from a separate `COUNT(*)`). Offset pagination, so deep offsets get slow. All other endpoints are unpaginated but internally capped: `/stats` sub-lists are `LIMIT 25` each; `/timeline` is capped by `buckets` ≤ 240.

### Retention
`scripts/retention.sh` — a cron/shell concern, NOT an API feature. Sources `../.env` (`set -a`), requires `LL_DB_ROOT_PASS`, validates `LL_RETENTION_DAYS` is an integer (default **365**), then:
```
docker exec lltel-db mariadb -u root -p"$LL_DB_ROOT_PASS" lltelemetry \
  -e "DELETE FROM runs WHERE received_utc < (UTC_TIMESTAMP() - INTERVAL ${DAYS} DAY);" \
  -e "SELECT ROW_COUNT() AS deleted;"
```
(The `${DAYS}` is shell-interpolated, hence the integer regex guard.) `api_keys` rows are never pruned.
`scripts/backup.sh` — nightly `mariadb-dump --single-transaction` → `backup/lltel-YYYY-MM-DD.sql.gz`, prunes dumps older than 30 days (`find -mtime +30 -delete`). Cron suggested at 03:15 (retention) and 03:30 (backup).

### Body / size limits
| Limit | Value | Where |
|---|---|---|
| Request body (ingest) | **2 MB** | Caddy `request_body { max_size 2MB }`, Caddyfile:24–26 — **prod only**, absent from `Caddyfile.local` |
| `log` field | **300000 chars** (`MAX_LOG_CHARS`) | truncated silently at main.py:236; the PS client also self-truncates at 300000 |
| All varchar fields | per `COLUMN_WIDTHS` (main.py:49–54) | `clip()` truncates rather than 4xx-ing — deliberate: the client retries forever on non-2xx, so an over-long model string must not reject a valid run |
| `label` on key create | 96 chars | `.strip()[:96]` main.py:518 |
| `days` | 1–365 | Query validators |
| `hours` | 1–2160 | Query validator |
| `buckets` | 6–240 | Query validator |
| `limit` | 1–1000 | Query validator |

`COLUMN_WIDTHS` verbatim: `run_id 36, script 64, script_version 16, result 32, hostname 64, serial 64, manufacturer 64, model 96, os 96, os_build 32, ad_domain 96, ip 45, mac 32, site 48, tech 96, key_label 96`. Every one matches the schema exactly.

---

## 8. TESTS — `telemetry/api/tests/test_main.py` (316 lines)

`conftest.py` inserts `api/` on `sys.path` and sets `LL_INGEST_TOKEN=test-token`, `LL_DB_PASS=test-pass` **before import** (main.py reads them at import time and would `KeyError` otherwise). Tests fake the DB via `FakeConn`/`FakeCursor` matched by SQL substring — **no live MariaDB required**. Autouse fixture clears `main._rate_hits` before and after each test.

| Test | Line | Contract asserted |
|---|---|---|
| `test_clip_truncates_to_column_width` | 92 | `clip` truncates to width, passes `None` through, no-ops for unknown columns |
| `test_resolve_site` | 98 | `10.15.100.20`→Ivory Tower, `10.6.81.5`→Muscle Shoals, `8.8.8.8`→None, `None`→None, `"not-an-ip"`→None |
| `test_rate_ok` | 106 | N allowed then blocked with `retry>=1`; **empty IP never limited** |
| `test_health_ok` | 119 | 200 `{"ok":true,"version":APP_VERSION}` |
| `test_health_db_down` | 126 | 503 when `db()` raises |
| `test_ingest_rejects_bad_token` | 137 | 401 on `Bearer nope` |
| `test_ingest_rejects_unknown_result` | 142 | 422 on `result="BOGUS"` |
| `test_ingest_success_resolves_site` | 147 | 200, `accepted:true`, `duplicate:false`, `site` derived from `x-forwarded-for` |
| `test_ingest_duplicate_is_noop` | 161 | `rowcount==0` → `duplicate:true`, still **200** |
| `test_ingest_clips_oversized_fields` | 168 | 100-char hostname reaches SQL as exactly 64 chars |
| `test_ingest_rate_limited` | 181 | 3rd post with `RATE_MAX=2` → 429 **with a `Retry-After` header** |
| `test_list_runs_pagination` | 195 | `total` from COUNT query, `count` from rows, `limit`/`offset` echoed, `received_utc` ends with `Z` |
| `test_stats_shape` | 220 | `totals.total`, `service.version`, and presence of `by_site, repeat_offenders, tls_bypassed, unmapped_subnets, daily` |
| `test_timeline_buckets` | 240 | dense `series` of exactly `buckets` entries; bucket 0 counts land; `results` = observed set |
| `test_list_runs_search_adds_like` | 256 | `q=` injects `LIKE` into the list SQL |
| `test_create_key_returns_secret_once` | 270 | 201; `secret` starts `llk_`; label echoed; **`prefix` is a substring of `secret`** |
| `test_ingest_accepts_managed_key` | 280 | round-trips a generated key: prefix lookup → hash match → 200 with `key == label`, and the label is bound into the `INSERT INTO runs` args |
| `test_ingest_rejects_revoked_key` | 298 | `revoked=1` → **401** |
| `test_revoke_key` | 311 | 200 `{"revoked":true}` |

**Not covered** (contract gaps to be careful about when reimplementing): `GET /api/v1/runs/{run_id}` and its 404; `rotate`; `delete`; `list_keys`; the 500 write-failure path; `results=` CSV filtering; `parse_dt` edge cases; anything about auth on read endpoints (because there is none in the app).

CI (`.github/workflows/ci.yml`): job `api` = `ruff check telemetry/api` + `pytest`; job `stack` = `docker compose config`, build the api image, and `caddy validate` the **production** Caddyfile (dummy `LL_DASH_USER/HASH`); job `package` = `tools/build-tarball.sh` → uploads `dist/ll-telemetry.tar.gz` (14-day retention).

---

## 9. `telemetry/README.md` — DEPLOY RUNBOOK SUMMARY

Target (§1): VM `IT-TELEMETRY` on host ONA-HV1 (Ivory Tower primary hypervisor), Ubuntu 26.04 LTS, 2 vCPU / 4 GB / 60 GB, static **10.15.102.8**, DNS `telemetry.longlewis.local` A → 10.15.102.8 on MS-DC001. **Internal only — no public DNS, no NAT, no firewall publish.** Reachable from every site over the existing VPN tunnels.

1. **Build VM** (§2): `apt install docker.io docker-compose-v2`, `systemctl enable --now docker`, `mkdir -p /opt/lltel` owned by the deploy user; copy the whole `telemetry/` folder to `/opt/lltel`.
2. **Certificate** (§3): issue a Web Server cert from the internal CA for `telemetry.longlewis.local` with SANs `DNS:telemetry.longlewis.local` **and** `IP:10.15.102.8`; export PEM to `/opt/lltel/caddy/certs/telemetry.crt` + `.key`. **Bake the LL root CA into the golden image** — a freshly imaged PC hasn't applied GPO so its first report fails validation; until then scripts fall back to an unvalidated POST and set `tls_bypassed = 1` so you can see exactly which machines need it.
3. **Secrets** (§4): `cp .env.example .env`; `openssl rand -hex 32` for the token; `docker run --rm caddy:2-alpine caddy hash-password --plaintext 'YourPassword'` for the hash (double every `$`); `chmod 600 .env`.
4. **Start** (§5): `docker compose up -d --build`, `docker compose ps`, `curl -k https://telemetry.longlewis.local/api/v1/health` → expect `{"ok":true,"version":"0.4"}`.
5. **Wire scripts** (§6): replace `REPLACE_WITH_INGEST_TOKEN` in each `.cmd` (3 scripts). Verify by checking `C:\ProgramData\LongLewis\Telemetry\queue` is empty after a run.
6. **Optional daily queue flush** (§7): a NinjaOne PowerShell script run daily on all Windows endpoints that drains the local queue folder, POSTing each `*.json` and `break`ing on first failure.
7. **Backup/retention** (§10): cron `retention.sh` at 03:15 and `backup.sh` at 03:30 into `/opt/lltel/backup`; point Hornet at that folder.
8. **Development** (§11): `cd telemetry/api && pip install -r requirements-dev.txt && pytest`; rebuild the deploy tarball with `bash tools/build-tarball.sh` (excludes secrets, certs, dumps, tests).

### How the dashboard is served
It is a **single static file**, `telemetry/dashboard/index.html` (619 lines, all HTML+CSS+JS inline, no build step, no framework, no CDN). Caddy mounts `./dashboard:/srv/dashboard:ro` and serves it with `file_server` from the catch-all `handle {}` block, **behind the same basic auth** as the read API (prod) or with no auth at all (local :4554).

Client-side API usage (dashboard/index.html):
- line 318: `const API='/api/v1';` — **same-origin relative**, so the browser reuses the basic-auth credential automatically.
- line 333: `api(p)` → `fetch(API+p,{headers:{accept:'application/json'}})`, throws `p + ' → ' + status` on non-ok.
- line 578: `apiSend(path, method, body)` → same-origin fetch with `content-type: application/json` for key mutations.
- Calls made: `/runs/${id}` (507, drawer detail), `/runs?${qs}` (533, river list — builds `days,limit,offset` plus optional `q`, `script`, `site`), `Promise.all([/stats?days=…, /timeline?hours=…&buckets=…])` (542, header/charts/ribbon), `/keys` (605 list), `POST /keys` (606 create), `POST /keys/{id}/revoke` (608), `POST /keys/{id}/rotate` (609), `DELETE /keys/{id}` (610).
- The dashboard never sends an Authorization header — it relies entirely on the browser's basic-auth session.

Version history (README §"Version history"): 0.1 initial (ingest API, schema, Caddy, dashboard, PS reporting block, 3 scripts); 0.2 (pagination, rate limiting, field clipping, stats panels, retention/backup, pytest, CI, tarball); 0.3 (dir renamed `telemetry/`, ops console dashboard, `/timeline` + run-search `q`/`results`); 0.4 (per-batch hashed ingest API keys + admin endpoints behind dashboard auth, console Keys panel, `key_label` on every run, `-Token` stamping in `Build-FieldScripts`).

---

## 10. THE CLIENT SIDE (what actually POSTs) — `agent/LL-Report-Block.ps1`

Relevant because a Go replacement must stay wire-compatible with already-deployed USB scripts.
- `$LLT_Url = 'https://telemetry.longlewis.local/api/v1/runs'` (line 22); `$LLT_TimeoutS = 4`; `$LLT_AllowInsecure = $true`; forces TLS 1.2/1.1.
- Headers: `@{ Authorization = "Bearer $LLT_Token"; 'Content-Type' = 'application/json' }` (line 68). Body is UTF-8 **bytes** of a compact `ConvertTo-Json -Depth 6`.
- On TLS validation failure it retries with validation off and stamps `tls_bypassed`.
- `LLT-Facts` (lines 30–55) gathers `hostname` (`$env:COMPUTERNAME`), `manufacturer`/`model`/`ad_domain` (Win32_ComputerSystem), `serial` (Win32_BIOS), `os`/`os_build` (Win32_OperatingSystem), `ip`/`mac` (default-route adapter), `tech` = `"$env:USERDOMAIN\$env:USERNAME"`. Each block is individually try/caught, so **any of these may be absent from the payload**.
- `LLT-Report` (lines 105–150) adds `schema=1`, a fresh `run_id` GUID, `script`, `script_version`, `result`, `exit_code`, `started_utc`/`finished_utc` as `ToString('s') + 'Z'`, `duration_sec`, `details`, `log` (scrubbed + truncated at 300000), `queued_offline=false`, `tls_bypassed=false`.
- On POST failure the JSON is written to `C:\ProgramData\LongLewis\Telemetry\queue\<run_id>.json` with `queued_offline` string-patched to `true`, and drained on a later run.
- **The scripts never fail because of telemetry**: 4s timeout, silent failure, queue to disk.

---

## 11. NOTES / RISKS FOR THE GATUS INTEGRATION

1. **Read + admin auth exists only at the proxy.** Anything reaching `api:8080` directly is fully privileged (read all logs, mint/revoke/delete ingest keys). If Gatus proxies to LL-Telemetry, decide deliberately whether Gatus becomes the new auth boundary or forwards to Caddy with the basic-auth credential. Do not port-publish the API container.
2. **`02-apikeys.sql` is not wired into either compose's initdb** — the v0.4 key features are dead on a fresh stack until it is applied by hand. Fix this if standing up a new environment.
3. **Two different boolean encodings**: `/runs` returns `queued_offline`/`tls_bypassed` as `0|1` ints; `/keys` returns `revoked` as a real bool. Model accordingly in Go.
4. **Timestamps are naive-UTC + literal `Z`** (`iso()` at main.py:171). `time.Parse(time.RFC3339, …)` works, but the DB stores naive DATETIME — there is no offset information anywhere, everything is UTC by convention.
5. **`schema` is the wire field name**, aliased to `schema_version` in Pydantic. A Go struct tag must be `json:"schema"`.
6. **Ingest returns 200 for duplicates**, never 409. Idempotency is on `run_id`.
7. **Rate limiter is per-process in-memory** — it does not survive a restart and does not coordinate across replicas. A Go rewrite gets this behavior for free with a similar in-memory limiter, but note the "empty IP is unlimited" carve-out.
8. **`q=` search LIKEs against the `log` MEDIUMTEXT column** with a leading wildcard — full scan, no index, no FULLTEXT. This is the endpoint that will hurt first.
9. **`v_latest_per_host` is defined but unused** — it is the ready-made "current status per machine" query if Gatus wants per-host state rather than a run river.
10. **Site vocabularies differ** between Gatus (Alabaster / Decatur / Ivory Tower / Cullman / …) and telemetry `SITE_MAP` (Ivory Tower / Muscle Shoals / Bramlett / Decatur). Only "Ivory Tower" matches verbatim; a mapping table is required to correlate.
11. **Health leaks the DB exception text** in its 503 detail; ingest leaks it in the 500 detail. Behind an internal-only proxy, but worth not reproducing.
12. Prod Caddy exposes **`GET /api/v1/health` with no auth at all** — that is the one endpoint Gatus can poll from outside the credential.
