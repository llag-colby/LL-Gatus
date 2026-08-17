# Architect 5 — Devil's Advocate

**Verdict: reject the plan as written.** Not because integration is a bad idea, but because
this plan is four separate projects (auth rollout, service relocation, reverse proxy, UI shell)
bundled as one, and the single biggest assumption underneath it — that a locally-run telemetry
stack will have data in it — is false. Steps 1, 2 and 5 should be deleted. Step 3 should be
replaced with a link.

Everything below cites verified files/lines. Two findings are live problems that exist
*right now*, before anyone writes a line of this feature.

---

## BLOCKER 0 — the repo is already broken on this machine

`web/static/` is **empty on disk**. Every tracked file under it shows as deleted:

```
 D web/static/index.html
 D web/static/js/app.js
 D web/static/js/chunk-vendors.js
 D web/static/css/app.css      ... (14 files total)
```

`web/static.go:6` is `//go:embed static`. A `go:embed` of a directory containing no embeddable
files is a **compile error**, so `docker compose build` will fail on the Gatus service right now.
Something ran a Vue build (which cleans `outputDir: '../static'`, `web/app/vue.config.js:9`) and
did not complete it. This is exactly the landmine the plan's own fact list names, already tripped.

**Fix, before anything else:**
```
git checkout -- web/static
```
Then confirm `docker compose build gatus` succeeds before starting any telemetry work. Also note
the tree has grown uncommitted edits since the session snapshot (`api/endpoint_check.go`,
`watchdog/*`, `api/phones_*` are now modified too) — other agents are writing to this tree
concurrently, and this plan modifies `api/api.go`, which is one of the contended files.

---

## BLOCKER 1 — the local telemetry stack will be empty. The feature shows nothing.

This is the objection that kills the plan and it is not in the fact list.

LL-Telemetry is a **push** system. `agent/LL-Report-Block.ps1` and every stamped field script POST
to the production telemetry host (`telemetry.longlewis.local` / `10.15.102.8`,
`telemetry/caddy/Caddyfile:9`). Standing up MariaDB + FastAPI as new services inside Gatus's
`docker-compose.yml` creates a **brand-new, empty database**. No agent knows about it. Nothing
will ever POST to it.

Worse, the volume identity guarantees it stays empty. LL-Telemetry's compose declares
`name: ll-telemetry` and `volumes: lltel-data`, producing `ll-telemetry_lltel-data`. Gatus's
compose project is `gatus`, so the same service definition binds `gatus_lltel-data` — a different,
empty volume. There is no path by which existing data appears.

So step 1 delivers a beautiful console rendering `No events match these filters.`

The only ways out are both large projects, not integration tasks:
- **Relocate telemetry to the Gatus box**: re-point every field script and every stamped ingest
  key, migrate the MariaDB volume, move the TLS certs, decommission IT-Telemetry-01. That is a
  migration with a rollback plan, not a compose edit.
- **Proxy to the existing host over the network**: don't run a second stack at all.

**Fix:** delete step 1 entirely. If you integrate, Gatus talks to the *existing* telemetry API at
`10.15.102.8`. If you don't, you link out. Either way, no second stack.

---

## BLOCKER 2 — folding the compose files together collides on names

Even setting aside the empty-data problem, the merge does not mechanically work:

- `telemetry/docker-compose.yml` hard-codes `container_name: lltel-db`, `lltel-api`, `lltel-caddy`.
  Container names are **daemon-global**. If the real telemetry stack ever runs on the same host
  (dev box, or if this ever lands on IT-Telemetry-01), `docker compose up` fails outright with
  "container name /lltel-db is already in use".
- `update.sh:70` runs `docker compose up -d --build --force-recreate --remove-orphans`.
  `--remove-orphans` deletes containers labelled with the project that aren't in the compose file.
  Get the merge half-right and this actively destroys containers.
- The db service bind-mounts `./db/01-schema.sql:/docker-entrypoint-initdb.d/01-schema.sql:ro`.
  From Gatus's compose that path becomes `../LL-Telemetry/telemetry/db/01-schema.sql`. **If that
  path does not exist, Docker silently creates a directory there** and MariaDB's entrypoint
  tries to source a directory as SQL. You get a running MariaDB with no `runs` table and no error
  anyone will notice until the console 500s.

**Fix:** covered by the fix to BLOCKER 1 — don't run the stack from Gatus's compose.

---

## BLOCKER 3 — the two-repo problem is unsolvable under `update.sh`

`update.sh:22-25`:
```
git fetch --prune origin main
git reset --hard origin/main
git clean -fd
```

Consequences, concretely:

1. **`../LL-Telemetry` is not on the prod box** and nothing in this flow puts it there. `docker
   compose` with `build: ../LL-Telemetry/telemetry/api` fails with "unable to prepare context: path
   does not exist". The update script has no pre-flight for it (it only checks `.env`, lines 39-46).
   The failure mode is `update.sh` aborting at line 70 under `set -euo pipefail` — leaving the box
   mid-update.
2. **A second manual clone becomes a permanent operational dependency.** Someone has to `git pull`
   two repos in the right order, forever, with no version pinning between them. The Gatus commit
   count is the build stamp (`update.sh:28`, `/api/v1/version`) and it says nothing about which
   telemetry commit is running. You lose the "does the running build match git" guarantee that
   `update.sh:82-88` exists to provide.
3. **`git clean -fd` deletes an untracked vendored copy.** If you instead copy telemetry files
   *into* the Gatus repo without committing them, they are gone on the next update. If you do
   commit them, you have forked (see below).
4. Bind-mounting `../LL-Telemetry` is viable only if you accept that the mounted content is
   outside version control from Gatus's point of view, which defeats the reason `update.sh`
   force-syncs in the first place.

**Fix:** the only clean answers are (a) telemetry stays a separate deployment on its own host and
Gatus references it by URL, or (b) LL-Telemetry becomes a git submodule/subtree of Gatus with a
pinned SHA and `update.sh` gains `git submodule update --init --recursive` after the reset. (b) is
real work and (a) is free. Take (a).

---

## BLOCKER 4 — you cannot build the UI change on this machine

Verified on this box:
```
$ node -v   → command not found
$ npm -v    → command not found
$ go version → command not found
```

Step 3 requires editing `web/app/src/router/index.js` (currently 5 routes: `/`, `/endpoints/:key`,
`/sites/:name`, `/suites/:key`, `/jira`) and a header component, then running
`vue-cli-service build`. There is no Node. The Dockerfile never runs npm — it does `COPY . ./` and
builds Go against the already-committed `web/static`.

Two compounding traps once you find a way to build:
- `vue.config.js` sets `outputDir: '../static'`, and Vue CLI **cleans that directory**. Anything
  you hand-place at `web/static/telemetry/index.html` is deleted by the next SPA build. That is
  BLOCKER 0, replayed.
- The correct location for a vendored console would be `web/app/public/telemetry/index.html`
  (`public/` is copied through and survives the clean) — but this is only correct if you're
  vendoring at all, which you shouldn't be.

**Fix:** if you must build the SPA, do it in Docker (`docker run --rm -v "$PWD/web/app":/app -w
/app node:20 sh -c "npm ci && npm run build"`) and add that as a documented step. But note the
whole reason you need it is step 3 — which the recommendation below deletes.

---

## BLOCKER 5 — proxying the Keys panel is indefensible

`cfg.Security == nil`, no `security:` block in `config.yaml`, and `docker-compose.yml:10-11`
publishes `8080:8080` with no fronting proxy. Every custom route in `api/api.go:86-132` is on
`unprotectedAPIRouter`.

The plan proxies all five key routes — `main.py:499` (list), `:517` (create), `:536` (revoke),
`:549` (rotate), `:571` (DELETE) — none of which have any auth in application code. In prod their
only boundary is `caddy/Caddyfile:52-59`. Proxy them through Gatus and **anyone on the LAN can
`curl -X POST http://<gatus>:8080/api/v1/telemetry/keys/1/revoke` and kill every field script's
ingest key.** DELETE is worse: `delete_key` removes the row, so you lose the audit trail too.

"But it'll be behind Gatus auth" — see BLOCKER 6 for why that auth flip is itself the problem, and
note the ordering hazard: protection in `api/api.go` is **positional** (comment at line 166), and
`protectedAPIRouter` is created at line 167, *after* the fiberfs mount at 158-162. Getting a new
route group on the correct side of that boundary is exactly the kind of thing that looks right in
review and ships open.

**Fix:** do not proxy `/keys` at all. Ever. Key administration is a five-times-a-year operation
performed by one person; it belongs on the telemetry host behind Caddy basic auth, where it
already works. Removing it also removes the only reason to proxy non-GET methods, which shrinks
the whole proxy surface to read-only GETs.

Bonus: the Keys panel is **broken anyway**. `dashboard/index.html:592` calls undefined `fmt()`.
You would be building auth, a proxy, and a UI shell to expose a panel that throws on render.

---

## BLOCKER 6 — enabling `security:` is a breaking change to every existing screen

Turning on auth is not additive. `api/api.go:176-185` puts on the protected router:
`/v1/endpoints/statuses`, `/v1/endpoints/:key/statuses`, `POST /v1/endpoints/:key/check`,
`/v1/suites/statuses`, and `/v1/live` (SSE). Those are the dashboard's entire data plane. The
moment `cfg.Security != nil`, **every wall display, kiosk, and the socat viewer on :8099 goes to a
401**, and they have no credential store, no keyboard, and nobody watching them.

Specifically:
- **EventSource cannot set headers.** `/api/v1/live` (line 185) is consumed via `EventSource` in
  the SPA. Under Basic auth it works only if the browser already holds cached credentials for the
  origin. A kiosk that gets 401 on EventSource enters an infinite reconnect loop — silently, with
  the screen frozen on stale data.
- **Basic auth prompts don't work well in frames.** Chrome suppresses the credential dialog for
  cross-origin subresources; even same-origin, a 401 raised by a fetch *inside an iframe* produces
  either no prompt or a prompt with no context. The plan's own console does
  `fetch(API+p,{headers:{accept:'application/json'}})` at `dashboard/index.html:333` with no
  credentials handling, relying entirely on ambient browser auth. That is precisely the pattern
  that fails inside a frame.
- **OIDC is worse for kiosks** — `gatus_session` cookie (`security/config.go:18`) plus an
  interactive login redirect. No kiosk survives that.
- **Collectors:** `phone_collector.py` and `unifi_collector.py` POST to
  `/v1/phones/:key`, `/v1/unifi/:key`, `/v1/endpoints/:key/external` — all on the *unprotected*
  router with their own bearer token. They survive the flip. But `GET /v1/phones/sweep-pending`
  (line 105) is also unprotected, so the flip doesn't break them and doesn't secure them either.
  You'd have half a security model.

**Fix:** do not enable global `security:` as a side effect of a telemetry feature. If Gatus needs
auth, that is its own project with its own rollout (kiosk allowlist by source IP, or a fronting
Caddy with a bypass for display subnets). Coupling it to telemetry means the telemetry feature
gets blamed when the Muscle Shoals wall display goes blank.

---

## SERIOUS — the 15s WriteTimeout will kill search, and you cannot raise it per route

`controller/controller.go:22-24` sets `ReadTimeout`, `WriteTimeout`, `IdleTimeout` to 15s on the
**fasthttp server** — global, not per route.

`main.py:305-311`, the `?q=` path:
```sql
(hostname LIKE %s OR script LIKE %s OR site LIKE %s
 OR tech LIKE %s OR result LIKE %s OR log LIKE %s)
```
with `f"%{q}%"` — leading wildcards, so no index is usable. `log` is `MEDIUMTEXT`
(`db/01-schema.sql:34`). And it runs **twice**: `count_sql` (line 316) and `list_sql` (line 317)
both carry the same WHERE. Two full scans over a table whose rows carry entire PowerShell
transcripts.

At any real data volume this exceeds 15s, fasthttp tears down the connection mid-write, and the
console surfaces a bare `'/runs?... → ' + r.status` error from line 333 — or nothing, because the
socket died before a status arrived. There is no per-route escape hatch in fiber v2 for this.

**Mitigations if you proceed:** drop `log` from the `q` predicate; cap `days` to 7 when `q` is
set; skip the COUNT when `q` is set and use `LIMIT n+1` for has-more; or add a FULLTEXT index
on `log`. None of these are Gatus changes — they are upstream LL-Telemetry changes, which means
this plan generates work in the other repo too.

## SERIOUS — compress + fasthttp proxy double-buffers multi-megabyte responses

`api/api.go:66-73` applies `compress.New()` globally. The `Next` predicate excludes only Gatus's
own SSE paths (`/api/v1/live`, `/api/v1/jira/live`, `/api/v1/jira/board/*/live`). Telemetry has no
SSE, so nothing is excluded — fine functionally, but:

- fiber's compress middleware **buffers the full response body** before compressing.
- fasthttp's proxy helpers also buffer the full upstream response — there is no streaming
  `httputil.ReverseProxy` equivalent in play here.
- `GET /runs?limit=1000` returns 1000 rows each carrying `details` (JSON) and, via the drawer path,
  potentially a MEDIUMTEXT `log`. That is a multi-MB body held in memory **twice** per request,
  inside a container that also runs every monitoring goroutine.

Add `Immutable: true` (`api/api.go:56`) — proxy code that retains request byte slices across a
handler boundary is a correctness trap here, and it is the kind of bug that shows as intermittent
garbled headers rather than a clean panic.

## SERIOUS — `X-Forwarded-For` is trusted unconditionally by the telemetry API

`main.py:163-169`:
```python
def client_ip(request: Request) -> str | None:
    src = request.headers.get("x-forwarded-for", "")
    src = src.split(",")[0].strip()
    if src: return src
    return request.client.host if request.client else None
```
No trusted-proxy check. Production is safe only because `caddy/Caddyfile:3-5` sets
`trusted_proxies static private_ranges` and rewrites the header.

That IP feeds two things: `rate_ok()` (`main.py:114`, the per-IP sliding window) and
`resolve_site()` (`main.py:141`, which maps source IP → site name via `SITE_MAP`). Put a naive
Gatus proxy in front and you get one of two failures:
- Proxy doesn't set XFF → every ingest collapses to the Gatus container's IP → **every machine is
  attributed to one site or to `None`**, and the rate limiter throttles the whole fleet as one
  client.
- Proxy forwards the client's XFF verbatim → **any caller forges its own site attribution and
  rate-limit identity**.

This only bites if ingest ever moves behind Gatus. The plan doesn't proxy ingest today — but
"maybe Caddy" in step 1 is exactly how it ends up there. Write down explicitly that ingest never
goes through Gatus.

## SERIOUS — a fresh stack 500s on the *main* view, not just the Keys panel

`db/02-apikeys.sql` is not mounted by either compose file (only `01-schema.sql` is, in both). It
adds `api_keys` **and** `ALTER TABLE runs ADD COLUMN key_label`. But `list_runs` at `main.py:317`
does:
```sql
SELECT id, run_id, received_utc, ..., tls_bypassed, key_label, details FROM runs
```
On a fresh stack, `GET /api/v1/runs` fails with `Unknown column 'key_label' in 'field list'` — a
500. That is the river view, the default screen, the whole point. The fact list undersells this as
"the Keys panel is broken"; the *entire console* is broken on a fresh stack. Which is exactly what
step 1 creates.

## SERIOUS — `Browse: true` turns a missing deep link into a directory listing

`api/api.go:158-162` mounts the embedded static FS with `Browse: true`, and SPA deep links are
enumerated by hand at 134-138 with no catch-all. Two consequences:

- A new `/telemetry` route requires a matching `app.Get("/telemetry", SinglePageApplication(cfg.UI))`
  line, or a hard refresh on that URL 404s. Easy to forget; the `/jira` line at 138 is the precedent.
- If you place a vendored console at `web/static/telemetry/`, hitting `/telemetry/` renders a
  **directory listing of your static tree** rather than the console or a 404. `Browse: true` is
  already a mild information leak of the whole embedded FS; adding directories under it makes it a
  bigger one.

## SERIOUS — the same-origin iframe is not a security boundary, and it launders the one you had

The plan removes `X-Frame-Options: DENY` (`caddy/Caddyfile:76`) by re-serving the console from
Gatus, and replaces it with nothing. A same-origin iframe shares cookies, `localStorage`, and full
DOM access via `parent.document`. So:

- Framing buys **style isolation only** (`*{margin:0;padding:0}` at the console's `<style>`, and
  unqualified `body`/`button` rules would otherwise trash the Tailwind SPA).
- It buys **zero** security isolation. Any future regression in the 619-line file you now own is a
  compromise of the Gatus origin, which is the origin that serves every status page in the company.

To be fair to the console: its escaping currently holds. `esc()` handles `& < > "` and every
interpolation into an attribute uses double quotes (`riverRow` at :464-477, `kv()` at :509, log
lines at :511). It does **not** escape `'`, so the first single-quoted attribute anyone adds is an
XSS. That is a maintenance hazard you are adopting, not a bug you are inheriting.

And remember what is in that data: hostnames, serials, MACs, internal IPs, AD domain, technician
identities, and **full PowerShell transcripts, full-text searchable**. Today that sits behind
Caddy basic auth on an internal-CA TLS host. The plan moves it to a port-published, currently
unauthenticated HTTP service.

---

## Is the iframe the right call? No — and the alternatives are cheaper than they look

**Strongest case against the iframe:** the iframe exists solely to contain a page you had to
vendor, and you only had to vendor it because you chose same-origin, and you only chose same-origin
because the console has no auth header (`dashboard/index.html:333` sends none) and prod Caddy sets
`X-Frame-Options: DENY`. **Every downstream cost in this plan traces back to the iframe decision.**
Remove it and BLOCKERs 2, 3, 4, and the vendored fork all evaporate.

Honest scoring of the three options:

| | Link out (new tab) | Iframe + vendored console | Full Vue port |
|---|---|---|---|
| Gatus code | one `<a href>` in the header | proxy + auth + route + SPA rebuild + vendored 619 lines | proxy + auth + ~1500 lines of Vue + chart work |
| Fork/drift cost | none | permanent, and it is *your* fork now | total rewrite; upstream changes are re-implemented, not merged |
| Auth | reuses working Caddy basic auth | requires enabling Gatus auth (BLOCKER 6) | same |
| Data | real prod data | empty (BLOCKER 1) unless you proxy to prod | same |
| Loses | shared header chrome, single tab | — | — |
| Time | 20 minutes | days, across two repos | weeks |

Notes on the fork specifically: the *minimum* patch is small — line 318
(`const API='/api/v1'` → `'/api/v1/telemetry'`) and the `fmt()` bug at 592. Two lines. The cost is
not the initial diff, it's that from then on every upstream change to a 619-line single-file app
is a manual three-way merge with no test suite on either side, performed by whoever remembers the
fork exists. Given that LL-Telemetry has its own `.tk/` and an active `.github/`, it is being
worked on. This fork will drift within a month.

Notes on the full Vue port: it is the *worst* option, not the best. The console's design leans on
`oklch()` custom properties, `color-scheme: dark` (`index.html:9`), a hand-drawn SVG timeline
(`svg.innerHTML=rects+sel` at :410), a brush interaction, and global `keydown` handlers for `/`
and `Escape` (:573-574). Porting that to Vue + Tailwind + the SPA's light/dark theming is a real
frontend project, and at the end you own *two* implementations of the same console.

Two things do argue *for* the iframe, and I'll concede them: the uncleaned 5s `setInterval` and
the document-level `keydown` hijack are both contained by a frame and would be genuine bugs in a
Vue port. (Watch out: if you wrap the iframe in `<keep-alive>`, the interval keeps polling forever
in the background across route changes.) But these are arguments for *not porting*, not arguments
for framing. Linking out contains them just as well.

---

## Scope realism: this is four projects

1. Enable and roll out authentication on a currently-open Gatus — with kiosk/wall-display handling.
2. Relocate or network-attach a production telemetry service (with its data).
3. Build a reverse proxy layer in fiber with correct XFF, method allowlisting, and timeouts.
4. Vendor, patch, and maintain a fork of a 619-line console, plus a Vue route and header button
   that currently cannot be built on this machine.

Any one of these is a week. Bundled, they fail together, and the failure mode is "the status board
everyone watches is now behind a login and shows an empty telemetry page."

### Recommended v1 (ship today, ~30 minutes)

- `git checkout -- web/static` and verify the build.
- Add a header link — same treatment as the Jira button — pointing at
  `https://telemetry.longlewis.local/` with `target="_blank" rel="noopener"`.
- Nothing else. No proxy, no compose changes, no auth change, no fork, no second stack.
  The telemetry host keeps its Caddy basic auth, its TLS, its `X-Frame-Options: DENY`, and its
  data. Users get one click from the status board to the console.

This does require an SPA rebuild (BLOCKER 4) since it touches the header component — do it in a
Node container, and commit the regenerated `web/static`.

### Recommended v2, only if v1 proves demand (a week, and it is optional)

A small native "Telemetry" card on the Gatus dashboard, fed by a **read-only, GET-only** proxy of
exactly two upstream routes: `/api/v1/stats` and `/api/v1/timeline`. Both are small aggregates —
no MEDIUMTEXT, no `?q=`, no 15s timeout risk. Render last-received age, run counts by severity,
and repeat-offender count in the SPA's own design language, and click through to the real console
in a new tab for detail. No iframe, no fork, no keys, no auth flip, and the proxy target is the
existing prod host over the network.

### Prerequisites before *any* of this that are their own tickets

- Fix `dashboard/index.html:592` (`fmt` undefined) — upstream, in LL-Telemetry.
- Mount `db/02-apikeys.sql` in both LL-Telemetry compose files, or `list_runs` 500s on any fresh
  deploy — upstream, in LL-Telemetry.
- Decide the Gatus auth question on its own merits, separately.

---

## What everyone missed (summary of the non-obvious ones)

| # | Finding | Rank |
|---|---|---|
| 0 | `web/static/` is currently empty; `//go:embed static` will not compile. Build is broken now. | **BLOCKER** |
| 1 | A local telemetry stack has no data — agents push to the prod host. The console renders empty. | **BLOCKER** |
| 2 | `container_name: lltel-*` is daemon-global; `name: ll-telemetry` volume becomes `gatus_lltel-data`. Merge collides or silently orphans data. `--remove-orphans` in `update.sh:70` makes it destructive. | **BLOCKER** |
| 3 | Missing bind-mount source → Docker creates a *directory* at `db/01-schema.sql` → MariaDB inits with no schema, no error. | **BLOCKER** |
| 4 | No `node`, no `npm`, **no `go`** on this machine. The Vue route in step 3 is not buildable as things stand. | **BLOCKER** |
| 5 | Proxied `/keys` on the currently-open router = LAN-wide ingest-key revocation and DELETE. | **BLOCKER** |
| 6 | Enabling `security:` 401s `/v1/live` (SSE, no headers possible), breaking every kiosk and the :8099 viewer. | **BLOCKER** |
| 7 | `list_runs` selects `key_label`, which `02-apikeys.sql` (never mounted) creates → the *main view* 500s on a fresh stack, not just Keys. | SERIOUS |
| 8 | 15s global `WriteTimeout` (`controller.go:22-24`) vs two full scans over MEDIUMTEXT for `?q=`. No per-route override exists. | SERIOUS |
| 9 | Global compress + fasthttp proxy both fully buffer → multi-MB bodies held twice in the monitoring process. | SERIOUS |
| 10 | `client_ip()` trusts XFF unconditionally; only Caddy's `trusted_proxies` saves prod. Site attribution and rate limiting are forgeable behind a naive proxy. | SERIOUS |
| 11 | `Browse: true` (`api.go:161`) turns a missing `/telemetry` deep link into a directory listing of the embedded FS. | SERIOUS |
| 12 | Same-origin iframe = zero isolation; discards `X-Frame-Options: DENY` and replaces it with nothing. `esc()` misses `'`. | SERIOUS |
| 13 | Two repos means the build stamp (`/api/v1/version` = Gatus commit count) no longer identifies what is running. Submodule or nothing. | SERIOUS |
| 14 | `<keep-alive>` around an iframe keeps the console's uncleaned 5s `setInterval` polling forever in the background. | MINOR |
| 15 | `jira.StartPoller()` has no `stop()` counterpart and no double-start guard — evidence that hot-reload in this codebase is untested. Any telemetry client added the same way inherits the leak. | MINOR |
| 16 | `git clean -fd` deletes an untracked vendored console on every `update.sh`; committing it is the fork. | MINOR |
| 17 | Concurrent agents are editing `api/api.go` in this same tree right now. This plan's central edit is on a contended file. | MINOR |
