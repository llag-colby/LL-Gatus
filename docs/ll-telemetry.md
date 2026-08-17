# LL-Telemetry console (`/ll-telemetry`)

The field-script telemetry console, reachable from the satellite-dish button in
the Gatus header. It is the operations console from the
[LL-Telemetry](https://github.com/llag-colby/LL-Telemetry) project, served by
Gatus and pointed at the telemetry API through a Gatus reverse proxy.

## What this is

Every `LL-*` field script reports each run to a central telemetry server: a
severity-ranked event river, a per-site status board, machine facts, and the
full scrubbed PowerShell transcript for any run. This page puts that console
behind the same header as the rest of Gatus instead of a second bookmark.

## How it fits together

```
Browser
  └─ GET /ll-telemetry                    Vue view (TelemetryConsole.vue)
       └─ <iframe src="/api/v1/telemetry/console">   same-origin
            ├─ GET  /api/v1/telemetry/console        the vendored console HTML
            └─ GET  /api/v1/telemetry/{stats,timeline,runs,keys,...}
                 └─ TelemetryGate  (basic auth, TELEMETRY_UI_*)
                      └─ TelemetryProxy  (allowlist)
                           └─ http://lltel-api:8080/api/v1/...
                                └─ MariaDB
```

Field scripts do **not** go through Gatus. They POST directly to the telemetry
host with their own per-batch `llk_` ingest keys, exactly as before.

## Why the console is framed rather than ported

The console is one self-contained 622-line file with its own OKLCH design
system, global `*` and `body` CSS rules, and no build step. Gatus is Vue 3 plus
Tailwind, whose `index.css` applies a bare `*` selector and Tailwind preflight.
Mounting the console into the Vue DOM would mean the two fight in both
directions. A same-origin iframe isolates the CSS completely while keeping the
button, the route and the browser credential shared.

Same-origin is load-bearing, not cosmetic: the console sends no `Authorization`
header of its own. It relies on the browser attaching the cached credential to
same-origin requests. A cross-origin frame would 401 every call, and the real
telemetry host additionally sends `X-Frame-Options: DENY`.

## Authentication

Telemetry has **its own credentials**, separate from Gatus's `security:` block.

That is deliberate. Gatus's built-in security is all-or-nothing across `/api`:
switching it on would also put `/api/v1/live` behind a login, and `EventSource`
cannot carry a basic credential, so unattended wallboards would enter a silent
reconnect loop. `TelemetryGate` gates only the telemetry routes and leaves every
existing screen untouched.

The routes are still registered under `protectedAPIRouter`, so if site-wide auth
is ever enabled they inherit that second layer for free.

**Telemetry fails closed.** If `TELEMETRY_UI_USER` and a password are not set,
the proxy refuses everything. This matters more than usual: the telemetry API
has no authentication of its own, so an unconfigured deployment that defaulted
open would expose every machine transcript and the ingest-key management
endpoints.

The not-configured page says only "This console is not available on this
instance." It is reachable **without any credential**, on an instance that may be
publicly exposed, so it deliberately names no environment variables, no internal
hostnames and no ports. The remediation detail is written to the Gatus log at
startup instead:

```
[api.telemetryCfg] LL-Telemetry is not configured (set TELEMETRY_UI_USER and
TELEMETRY_UI_PASSWORD_BCRYPT or TELEMETRY_UI_PASSWORD)
```

The `unreachable` and `slow` states are not public: they can only be reached
after authenticating, because the gate returns 401 first.

### Signing in

`/ll-telemetry` renders its own sign-in screen and exchanges the credentials for
a session cookie:

| Method | Path | Purpose |
|---|---|---|
| POST | `/api/v1/telemetry/session` | Sign in; sets `lltel_session` |
| DELETE | `/api/v1/telemetry/session` | Sign out; drops the session |

The cookie is `HttpOnly`, `SameSite=Strict`, scoped to `Path=/api/v1/telemetry`
so no other route on the origin ever receives it, `Secure` when the request
arrives over TLS, and valid for 12 hours. Sessions live in memory, so a Gatus
restart signs everyone out.

This replaces a browser basic-auth dialog, which was both ugly and unreliable
inside the console's iframe. The gate deliberately does **not** send
`WWW-Authenticate`, so no native dialog can appear. **Basic auth still works**
for curl and scripts, which send the header preemptively:

```sh
curl -u llops:PASSWORD http://localhost:8080/api/v1/telemetry/stats
```

Failed sign-ins are counted per source address: 10 within 5 minutes and the gate
returns 429 until the window rolls off. It is a sliding window, so each failure
re-anchors it.

The counter covers **both** the login form and the Basic-auth path. That matters
more than it sounds: Basic is reachable on every GET, so throttling only the
form would guard the one door an attacker never has to use. Requests carrying no
`Authorization` header are not counted, because the SPA probes `/health`
credential-less on every page load and would otherwise lock an operator out
after ten reloads.

### Environment variables

| Variable | Purpose |
|---|---|
| `TELEMETRY_UI_USER` | Username for the console. Required. |
| `TELEMETRY_UI_PASSWORD_BCRYPT` | Bcrypt hash of the password. Preferred; wins if both are set. |
| `TELEMETRY_UI_PASSWORD` | Plaintext password. Accepted fallback. |
| `TELEMETRY_UPSTREAM_URL` | Telemetry API base. Defaults to `http://lltel-api:8080`. |
| `TELEMETRY_UPSTREAM_TOKEN` | Optional bearer, only if the telemetry API grows its own auth. |

Generate a bcrypt hash with:

```sh
docker run --rm python:3-slim sh -c \
  "pip -q install bcrypt && python -c \"import bcrypt;print(bcrypt.hashpw(b'YOUR-PASSWORD',bcrypt.gensalt()).decode())\""
```

## What the proxy allows

`TelemetryProxy` is a deny-by-default allowlist, not a pass-through:

| Method | Path | Purpose |
|---|---|---|
| GET | `/health` | Service liveness |
| GET | `/stats` | Counters behind the stat strip |
| GET | `/timeline` | Severity ribbon buckets |
| GET | `/runs` | The event river, with search and pagination |
| GET | `/runs/{id}` | One run: machine facts and transcript |
| GET | `/keys` | List ingest keys |
| POST | `/keys` | Mint a key |
| POST | `/keys/{id}/revoke` | Revoke a key |
| POST | `/keys/{id}/rotate` | Rotate a key |
| DELETE | `/keys/{id}` | Delete a revoked key |

Anything else returns 403 without reaching the upstream.

**`POST /runs` is deliberately not proxied.** Ingest belongs to the field
scripts and their `llk_` keys. Exposing it here would let anyone past the gate
forge runs.

Three further rules worth knowing:

- **Any `%` in the path is rejected outright.** No allowlisted path needs
  percent-encoding, so refusing it eliminates traversal, encoded-slash and
  null-byte tricks in a single rule with no decode-ordering subtleties.
- **`X-Forwarded-For` is never forwarded.** The outbound request is built from
  an empty header map. `client_ip()` upstream trusts the first XFF entry with no
  trusted-proxy check, so forwarding it would let a caller both reset the
  per-IP ingest rate-limit bucket and forge `runs.site`.
- **Upstream error bodies are never echoed.** FastAPI puts raw exception text in
  `detail`; the proxy substitutes a static message per status code.
- **Cross-site writes are refused.** Basic credentials have no `SameSite`, so a
  browser attaches them to cross-site-initiated requests. A form POST to
  `/keys/{id}/revoke` is a "simple" request that is not preflighted, and
  revoking a key kills ingest for every script stamped with it. The gate
  requires `Sec-Fetch-Site: same-origin` (or a matching `Origin`) on anything
  that is not a GET. Requests carrying neither header are not from a browser, so
  curl and scripts still work.

### Status codes

| Status | Meaning |
|---|---|
| 401 | No or wrong telemetry credentials |
| 403 | Path not on the allowlist, or a cross-site write |
| 404 | No such ingest key |
| 409 | That key must be revoked before it can be deleted |
| 429 | Too many failed sign-ins from this address |
| 502 | The telemetry service is unreachable, or returned any 5xx |
| 503 | Telemetry is not configured on this Gatus instance |
| 504 | The telemetry service did not answer within 12s |

Every upstream 5xx is collapsed to 502 deliberately. The telemetry API returns
503 when its database is down, which would otherwise be indistinguishable from
Gatus's own "not configured" 503. The not-configured response additionally
carries an `X-Telemetry-Not-Configured: 1` header, which is what the Vue view
keys on rather than the bare status.

### Deleting an ingest key

`DELETE /keys/{id}` is a hard delete, and it breaks ingest for **every field
script stamped with that batch key**. The proxy refuses to delete a key that is
still active: revoke it first. The console's UI already only offers delete on
revoked keys; this enforces it server-side rather than trusting the UI.

## Content-Security-Policy

The console page is served with a CSP built at startup from a SHA-256 hash of
its single inline `<script>` block, with no `unsafe-inline` for scripts. That
can stay strict because the console assigns handlers as JS properties
(`el.onclick =`) rather than HTML `on*` attributes, and uses no `eval`, no
`new Function` and no `javascript:` URLs.

`style-src` does allow `'unsafe-inline'`. The console renders 12 inline
`style="..."` attributes, and a hash does not authorise style attributes: those
are governed by `style-src-attr`, which falls back to `style-src`. Hashing the
`<style>` block instead would leave every one of those attributes blocked and
the page visibly broken. CSS is not the vector that matters here.

`connect-src 'self'` is the load-bearing directive: even a stored XSS carried in
a machine transcript cannot exfiltrate anything off-origin.

## Running the telemetry stack locally

The telemetry server is a separate box in production. For local development,
`docker-compose.telemetry.yml` runs MariaDB and the FastAPI alongside Gatus. It
is **not** auto-loaded, and must be combined explicitly:

```sh
docker compose -f docker-compose.yml -f docker-compose.telemetry.yml up -d --build
```

It is deliberately not named `docker-compose.override.yml`, which Docker would
load automatically and which would then fail on the prod box, where
`../LL-Telemetry` does not exist.

Caddy is not run. In production it terminates TLS and is the entire auth
boundary; here Gatus is the boundary and serves the console itself.

The telemetry API is never published to the host: it is reachable only over the
compose network, and MariaDB sits on an `internal: true` network with no route
off the host.

### Seeding demo data

Nothing writes to a locally-run telemetry database. The real field scripts POST
to `telemetry.longlewis.local`, not to this box, so a fresh local stack renders
a perfectly working console reporting zero events.

```sh
docker compose -f docker-compose.yml -f docker-compose.telemetry.yml \
  --profile seed run --rm lltel-seed
```

This posts a few hundred runs through the real ingest path, so it exercises
server-side site resolution, field clipping and replay de-duplication the same
way a field script would. It includes one deliberately unmapped subnet so the
console's "unmapped subnets" panel has something to show.

Two optional knobs:

| Variable | Default | Meaning |
|---|---|---|
| `SEED_RUNS` | `280` | How many runs to post |
| `SEED_DAYS` | `3` | How far back to spread them |

The default stays just under the upstream's ingest rate limit (300 per rolling
300s per source IP), since every seeded run arrives from one container. Larger
values are fine: the seeder reads `Retry-After` on a 429 and waits the window
out rather than dropping runs.

## Two upstream bugs this depended on

Both are fixed on the `fix/keys-panel-and-schema-mount` branch of LL-Telemetry:

1. `dashboard/index.html` called `fmt()`, which is defined nowhere. Every key row
   threw a `ReferenceError` and the entire Keys panel rendered as an error box.
2. Both telemetry compose files mounted only `db/01-schema.sql`, so
   `db/02-apikeys.sql` never ran. On a fresh volume that leaves no `api_keys`
   table and no `runs.key_label` column, and because the runs SELECT names
   `key_label`, `GET /api/v1/runs` itself 500s, not just the key endpoints.

An already-initialised database needs `02-apikeys.sql` applied by hand: MariaDB
only runs `docker-entrypoint-initdb.d` scripts on an empty data directory.

## Keeping the console in sync with upstream

`api/assets/telemetry-console.html` is vendored byte-identical to LL-Telemetry's
`telemetry/dashboard/index.html`. Syncing is a copy, and drift is a `diff`.

The console's API base is repointed at serve time, not by editing the file.
`api/telemetry.go` asserts at startup that `const API='/api/v1'` appears exactly
once and **panics if it does not**. That is the point: if a future upstream
release reformats that line, Gatus fails loudly at boot instead of silently
serving a console aimed at Gatus's own `/api/v1`, which would render empty
panels forever.

## Files

| Path | Role |
|---|---|
| `api/telemetry.go` | Gate, console handler, CSP, proxy allowlist |
| `api/telemetry_test.go` | Allowlist, auth parsing, and fail-closed table tests |
| `api/assets/telemetry-console.html` | Vendored console, byte-identical to upstream |
| `api/api.go` | Route registration and the `/ll-telemetry` SPA deep link |
| `web/app/src/views/TelemetryConsole.vue` | The iframe host, with probe and states |
| `web/app/src/App.vue` | Header button |
| `web/app/src/router/index.js` | `/ll-telemetry` route |
| `docker-compose.telemetry.yml` | Local MariaDB + FastAPI stack |
| `collector/seed_telemetry.py` | Demo-data seeder |

## Site resolution

The telemetry API resolves a run's site server-side, in `SITE_MAP` in
`telemetry/api/main.py`. Ingest calls `resolve_site(run.ip)` and falls back to
`resolve_site(src)`, which is what gives the LAN layer priority — not the
ordering of the list.

1. **LAN subnets**, matched against the machine's own reported IP. These are the
   accurate ones and **the only layer that fires today**. Four are known:
   `10.15.100.0/22` and `10.15.104.0/22` (Ivory Tower), `10.6.81.0/24`
   (Muscle Shoals), `10.10.1.0/24` (Bramlett / Decatur).
2. **WAN egress addresses** for all 12 sites, from the Gatus WAN monitors in
   `config.yaml`.

**Be clear about layer 2: it is inert in the current topology.** The telemetry
service is internal-only (no public DNS, no NAT, `ufw` restricted to
`10.0.0.0/8`) and field PCs reach it over the tunnel, so the source address is
already private and a public egress IP never appears in either input. The layer
is kept because it costs nothing and is correct if that ever changes, not
because it is doing work.

So the eight sites without a LAN subnet still land as **unmapped**. Adding their
subnets to layer 1 is the only real fix, and they have to be confirmed from the
network rather than guessed.

`"Bramlett / Decatur"` is deliberately kept rather than renamed to match Gatus's
`Decatur GMC` / `Decatur KIA`. It is already written into historical rows, and
`GROUP BY site` in `/api/v1/stats` would split Decatur into two partial tiles.
Both Decatur rooftops also share one WAN address, so nothing could tell GMC from
KIA anyway. Every other name matches Gatus character-for-character.

## Known limitations

- The console's `1h` / `6h` / `24h` range buttons all request `days=1`, and the
  river is never filtered by range, so `1h` still lists 24 hours of events. This
  is upstream behaviour, left as-is: the correct fix is an `hours` parameter on
  the telemetry API, not a client-side filter that would desynchronise the
  footer counts and pagination from the ribbon.
- `GET /runs?q=` does a `LIKE '%...%'` scan across six columns including the
  `MEDIUMTEXT` transcript, with no index. On a large dataset it is the first
  thing that will exceed the proxy's 12s timeout.
