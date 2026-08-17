# Architect 3 — Infrastructure + Reverse-Proxy Layer

Scope: docker-compose, networking, volumes, schema bootstrap, the Go proxy handler,
timeout budget, rate-limit impact, demo seeding, runbook.

Out of scope (other lanes): the Vue console UI, the FastAPI code itself.

---

## 0. BLOCKER found during verification — read this first

**`config.yaml` has no `security:` block.** Verified: the top-level keys are
`storage`, `ui`, `endpoints`, `external-endpoints`. Nothing else.

In `api/api.go:169-175`:

```go
protectedAPIRouter := apiRouter.Group("/")
if cfg.Security != nil {
    ...ApplySecurityMiddleware(protectedAPIRouter)...
}
```

With `cfg.Security == nil` the "protected" router has **zero middleware**.
`/api/v1/endpoints/statuses` is wide open today, and that is fine — it is read-only
status data on a LAN box.

It is **not** fine for telemetry. DECIDED #4 puts the Keys panel behind this proxy.
`POST /api/v1/keys` mints a live ingest key and **returns the plaintext secret once**
(`api/main.py:517-533`); `POST /keys/{id}/rotate` does the same; `DELETE /keys/{id}`
destroys one. Behind a nil-security router, anyone who can reach `:8080` mints
themselves a production telemetry ingest credential.

Decision **3 ("telemetry routes are gated behind Gatus auth") is currently
unimplementable.** Adding the routes to `protectedAPIRouter` is necessary but not
sufficient — the `security:` block must exist too. Both, or ship neither.

```yaml
# config.yaml — add at top level
security:
  basic:
    username: "itadmin"
    # bcrypt hash, then base64(URL-safe) of that hash
    password-bcrypt-base64: "${GATUS_ADMIN_BCRYPT_B64}"
```

Generate it (`security/config.go:70-73` does `base64.URLEncoding.DecodeString`):

```bash
docker run --rm caddy:2-alpine caddy hash-password --plaintext 'YourPassword' \
  | tr -d '\n' | base64 -w0 | tr '+/' '-_'
```

Put the result in `.env` as `GATUS_ADMIN_BCRYPT_B64=...` (the gatus service already
has `env_file: .env`, and `config.yaml` already expands `${...}` — that is how
`PHONES_PUSH_TOKEN` works).

**Consequence to accept knowingly:** Gatus basic auth is all-or-nothing across
`protectedAPIRouter`. Turning it on also puts `/v1/endpoints/statuses`,
`/v1/suites/statuses`, `/v1/live` (SSE) and `POST /v1/endpoints/:key/check` behind
the same single credential. The wall-mounted status screens will need the credential
once (basic auth is sticky per browser session). The collectors are unaffected —
they push to `unprotectedAPIRouter` routes (`/v1/phones/*`, `/v1/unifi/*`,
`/v1/endpoints/:key/external`), which stay open with their own token auth.

**Fallback if that trade is unacceptable:** ship the console read-only — proxy only
`GET /health|/runs|/runs/{id}|/stats|/timeline`, drop `/keys` entirely, and revisit
DECIDED #4. A read-only telemetry console on an already-open LAN dashboard is a
defensible posture. A key-minting endpoint on one is not.

---

## 1. Compose design

### 1.1 Is Caddy needed? No. Drop it.

Caddy does exactly four jobs in the LL-Telemetry stack. Verified against
`telemetry/caddy/Caddyfile`:

| Caddy job | Local status under Gatus |
| --- | --- |
| Internal-CA TLS on :443 | Moot. Traffic is `browser → gatus:8080` on localhost, then in-network to `api:8080`. |
| `basic_auth` on read/stats/keys | **Replaced by Gatus `security.basic` + `protectedAPIRouter`.** This is the whole point of DECIDED #3. Two auth boundaries in series = one of them is wrong sooner or later. |
| `handle /api/* { respond 405 }` catch-all | **Replaced by the proxy's route allowlist** (§4.3), which is tighter — it matches method *and* path shape, not just method. |
| `request_body { max_size 2MB }` on ingest | Moot — we do **not** proxy ingest (§4.3). Belt-and-braces cap in the proxy anyway. |
| Serving `dashboard/index.html` | See §1.2. |

Keeping Caddy would mean the request path `browser → gatus → caddy → api`, with a
second credential (`LL_DASH_USER`/`LL_DASH_HASH`) that Gatus would have to hold and
inject. Two hops, two auth systems, one extra container, zero benefit. Drop it.

### 1.2 What serves the console HTML?

`telemetry/dashboard/index.html` is one self-contained 619-line file. Line 318:

```js
const API='/api/v1';
```

Every call goes through that one constant (`api()` at line 333, the key-panel
`fetch` at line 578). So a single-line change to `/api/v1/telemetry` makes the whole
console — Keys panel included — speak the proxied path.

**Cross-lane dependency (frontend lane owns this):** the console should be ported
into the Gatus Vue SPA as a `/telemetry` view, so it inherits Gatus's nav, theme and
auth session. That also needs one line in `api/api.go` next to the other SPA routes:

```go
app.Get("/telemetry", SinglePageApplication(cfg.UI))
```

**Infra does not serve the HTML.** The Gatus image is `FROM scratch` — no shell, no
filesystem to bind-mount static assets into meaningfully, and `web/static` is
compiled into the binary via `embed`. Bind-mounting a file from outside the repo into
a scratch image and adding a Go file-server route for it would be a hack that only
exists to avoid a one-line frontend change. If the frontend lane needs an interim
stopgap, run the stock `caddy:2-alpine` + `Caddyfile.local` on `:4554` **temporarily
and separately**, and delete it once the Vue view lands.

### 1.3 Exact YAML

New file: `C:\Users\colby.west\Desktop\Projects\Gatus\docker-compose.telemetry.yml`

```yaml
# LL-Telemetry, run LOCALLY alongside Gatus. NOT auto-loaded — this file is only
# active when passed explicitly with -f (see .tk/agents/architect-3-infra.md §8).
#
#   docker compose -f docker-compose.yml -f docker-compose.telemetry.yml up -d
#
# Deliberately NOT named docker-compose.override.yml: compose auto-loads that name,
# which would drag telemetry onto the prod box the next time ./update.sh runs.
#
# Neither service publishes a port. The API is reachable only as http://lltel-api:8080
# from inside the compose network — the Gatus proxy is the sole way in.

services:
  # MariaDB. Lives on an internal-only network; the API is its only client.
  lltel-db:
    image: mariadb:11.4
    container_name: lltel-db
    restart: unless-stopped
    environment:
      MARIADB_ROOT_PASSWORD: ${LL_DB_ROOT_PASS:?LL_DB_ROOT_PASS missing from .env}
      MARIADB_DATABASE: lltelemetry
      MARIADB_USER: lltel
      MARIADB_PASSWORD: ${LL_DB_PASS:?LL_DB_PASS missing from .env}
      TZ: America/Chicago
    volumes:
      # SCHEMA BUG FIX — mount the whole db/ dir, not just 01-schema.sql. See §3.
      # Files execute in lexical order: 01-schema.sql then 02-apikeys.sql.
      - ../LL-Telemetry/telemetry/db:/docker-entrypoint-initdb.d:ro
      - lltel-data:/var/lib/mysql
    networks:
      - lltel-db-net
    healthcheck:
      test: ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]
      interval: 10s
      timeout: 5s
      retries: 12
      start_period: 60s

  # FastAPI. On `default` so the gatus container can reach it by name, and on the
  # internal db net. No ports: — unreachable from the host or the LAN.
  lltel-api:
    build:
      context: ../LL-Telemetry/telemetry/api
    image: ll-telemetry-api:local
    container_name: lltel-api
    restart: unless-stopped
    environment:
      # Both of these are os.environ[...] in main.py — the container crash-loops
      # at import if either is unset. :? fails fast at `up` instead.
      LL_INGEST_TOKEN: ${LL_INGEST_TOKEN:?LL_INGEST_TOKEN missing from .env}
      LL_DB_PASS: ${LL_DB_PASS:?LL_DB_PASS missing from .env}
      LL_DB_HOST: lltel-db
      LL_DB_USER: lltel
      LL_DB_NAME: lltelemetry
      LL_INGEST_RATE_MAX: ${LL_INGEST_RATE_MAX:-300}
      LL_INGEST_RATE_WINDOW_SEC: ${LL_INGEST_RATE_WINDOW_SEC:-300}
      TZ: America/Chicago
    depends_on:
      lltel-db:
        condition: service_healthy
    networks:
      - default
      - lltel-db-net
    healthcheck:
      # python:3.12-slim has no curl/wget. urlopen raises on 503, so a db-down API
      # correctly reports unhealthy.
      test: ["CMD", "python", "-c",
             "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8080/api/v1/health', timeout=3)"]
      interval: 15s
      timeout: 5s
      retries: 5
      start_period: 30s

  # One-shot demo seeder. Never runs on a normal `up` — needs --profile seed. See §7.
  lltel-seed:
    image: python:3-slim
    container_name: lltel-seed
    profiles: ["seed"]
    depends_on:
      lltel-api:
        condition: service_healthy
    environment:
      - LL_INGEST_TOKEN=${LL_INGEST_TOKEN}
      - LLTEL_API=http://lltel-api:8080
      - SEED_RUNS=${SEED_RUNS:-400}
      - PYTHONUNBUFFERED=1
      - TZ=America/Chicago
    volumes:
      - ./collector:/collector
    working_dir: /collector
    command: ["python", "seed_telemetry.py"]
    restart: "no"
    networks:
      - default

networks:
  # `default` is Gatus's existing gatus_default — inherited, not redefined.
  lltel-db-net:
    name: gatus_lltel_db
    internal: true   # no route off-host; the db cannot reach or be reached externally

volumes:
  lltel-data:
```

### 1.4 Why these choices

**Networks.** `lltel-db` sits *only* on `internal: true` — Docker installs no NAT
route for it, so the database has no path to or from anything off-host, and the API
is its only possible client. `lltel-api` bridges both nets: `default` for the Gatus
proxy to reach it and for outbound (unused, but harmless), `lltel-db-net` for MariaDB.

**Do not touch the `gatus` service's networks.** This is a live trap. `gatus` has no
`networks:` key in the base compose, so it is implicitly on `default`. Adding
`networks: [lltel-net]` in an override **replaces** that implicit default rather than
adding to it — `gatus` would silently drop off `gatus_default`, and both collectors
would start failing on `http://gatus:8080` with a DNS error. The design above avoids
the trap entirely by never mentioning `gatus`: `lltel-api` joins `default` instead.
If you ever do need to add a network to `gatus`, you must write
`networks: [default, lltel-net]` explicitly.

**No `ports:` anywhere.** This is the entire external-reachability story. Compose
bridge networks are not routable from the LAN; without a published port the API and
DB are reachable only from sibling containers. Verify with §8.

**Volume.** Named volume `lltel-data`, realized as **`gatus_lltel-data`** (project
name is `gatus`, confirmed via `docker compose config`). This is a *different* volume
from LL-Telemetry's own `ll-telemetry_lltel-data`. Two consequences, both good:
the local stack starts with an empty data dir, so the §3 schema fix applies cleanly
with no migration; and nothing here can corrupt a real telemetry database.

**Healthchecks.** The DB check is upstream's (`healthcheck.sh` ships in the mariadb
image). The API check is new — upstream has none, so `depends_on: service_healthy`
was impossible for anything downstream of the API. It matters for the seeder, which
must not fire before the API can serve.

**Restart policy.** `unless-stopped`, not Gatus's `always`. `always` restarts a
container even after you deliberately `docker stop` it, which makes debugging a
crash-looping API miserable. It also matches upstream LL-Telemetry.

**Ordering.** `lltel-db (healthy) → lltel-api (healthy) → lltel-seed`.
**`gatus` deliberately does NOT depend on telemetry.** Gatus is the production status
board for nine dealerships; a MariaDB that fails to come up must never delay or block
it. The proxy returns 502/504 when the upstream is down (§4.6) and every other Gatus
page keeps working. Coupling them would be a self-inflicted outage.

---

## 2. Where the LL-Telemetry source lives

**Decision: bind the build context and the SQL directory to `../LL-Telemetry`.
Do not vendor. Do not pre-build an image.**

Three options, evaluated:

**(a) Vendor a copy into the Gatus repo — REJECTED.** Two reasons, either one fatal.
First, `.gitignore` line comment says it outright: *"Secrets — never commit (repo is
public)"*. **The Gatus repo is public.** Copying LL-Telemetry's `api/main.py`,
schema, and Caddyfiles into it publishes the internal telemetry system's source, its
`SITE_MAP` (real internal CIDRs for Ivory Tower, Muscle Shoals, Bramlett/Decatur),
and its auth model. Second, it forks a live repo — `main.py` would then exist at two
paths with no sync mechanism, and the copy would rot the first time LL-Telemetry ships
a v0.5.

**(b) Pre-build `ll-telemetry-api:local` out-of-band, reference `image:` only —
viable fallback.** Keeps every path inside the Gatus repo. Cost: an invisible manual
step. `docker compose up -d` succeeds against a stale image with no signal, and after
`git clean -fd` there is nothing left to tell you the image is three weeks old. Keep
this in your pocket for the day the LL-Telemetry checkout is not on the box:

```bash
docker build -t ll-telemetry-api:local ../LL-Telemetry/telemetry/api
# then in docker-compose.telemetry.yml, replace the build: block with:
#   image: ll-telemetry-api:local
```

**(c) Bind to `../LL-Telemetry` — CHOSEN.** From
`C:\Users\colby.west\Desktop\Projects\Gatus\docker-compose.telemetry.yml`, the
relative path `../LL-Telemetry/telemetry/api` resolves to
`C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\api`. Compose resolves
relative paths against the compose file's directory, and Docker Desktop happily takes
a build context outside the repo. The API image is four pip deps and one `main.py` —
rebuild is seconds. Edits in LL-Telemetry go live on the next `up -d --build`, with
LL-Telemetry remaining the single source of truth.

### 2.1 Surviving `./update.sh`

`update.sh` runs `git reset --hard origin/main` then `git clean -fd`. Two rules follow:

1. **Commit `docker-compose.telemetry.yml`.** Untracked, `git clean -fd` deletes it.
   Tracked, it survives — and because compose does *not* auto-load that filename, the
   prod box's `docker compose up -d --build` (which reads only `docker-compose.yml`,
   plus `docker-compose.override.yml` if present) ignores it completely. Committing
   the file is safe; it is inert without an explicit `-f`.
2. **Never put the telemetry services in `docker-compose.yml`, and never name the
   file `docker-compose.override.yml`.** Either would make prod try to build
   `../LL-Telemetry/telemetry/api` — a path that does not exist there — and
   `update.sh`'s `docker compose up -d --build` would hard-fail, taking the whole
   Gatus deploy down with it. This is the single highest-consequence mistake available
   in this design.

`.env` is gitignored and survives `git clean -fd`, so the new `LL_*` keys persist.
`config.yaml` is *also* gitignored (verified) — so the `security:` block from §0 is
local-only and will not reach prod on its own. Note the flip side: it also will not be
restored by `update.sh` if you ever lose it.

**Rejected convenience:** setting `COMPOSE_FILE=docker-compose.yml:docker-compose.telemetry.yml`
in `.env` so bare `docker compose up -d` picks both up. It works, but if the telemetry
file ever goes missing, *every* compose command in the directory errors out — including
the ones `update.sh` needs to recover Gatus. Explicit `-f` flags fail only the command
you typed.

---

## 3. The schema-mount bug

**Bug (confirmed).** `telemetry/docker-compose.yml` and `docker-compose.local.yml`
both mount only:

```yaml
- ./db/01-schema.sql:/docker-entrypoint-initdb.d/01-schema.sql:ro
```

`db/02-apikeys.sql` is never mounted, so it never executes. On a fresh volume the
`api_keys` table and the `runs.key_label` column do not exist. Blast radius, traced
through `api/main.py`:

- `GET /api/v1/keys` (l.500) → `SELECT ... FROM api_keys` → **500**. The whole Keys
  panel is dead, which is precisely the DECIDED #4 feature.
- `POST /keys`, `/keys/{id}/revoke`, `/keys/{id}/rotate`, `DELETE /keys/{id}` → **500**.
- `POST /api/v1/runs` with an `llk_` key → `check_ingest_key` (l.88) queries
  `api_keys`, throws, and the handler's `except` wraps it as **500** — not a clean 401.
- Even with the legacy shared token, the INSERT at l.240 names `key_label` in its
  column list → unknown column → **every ingest 500s**. The stack is not merely
  missing a feature, it cannot accept data at all.

**Fix — mount the directory, not the file:**

```yaml
- ../LL-Telemetry/telemetry/db:/docker-entrypoint-initdb.d:ro
```

Already in the §1.3 YAML. The MariaDB entrypoint executes every `*.sql`, `*.sql.gz`
and `*.sh` in that directory in lexical order — `01-schema.sql`, then
`02-apikeys.sql`. Verified the directory contains only those two `.sql` files, so
there is nothing unexpected to execute. This fixes the *class* of bug: `03-*.sql` will
apply automatically instead of silently not applying.

**Upstream patch for LL-Telemetry** (separate repo, separate change — apply the same
edit to both `telemetry/docker-compose.yml` and `telemetry/docker-compose.local.yml`):

```yaml
      - ./db:/docker-entrypoint-initdb.d:ro
```

**Already-initialized volumes.** `docker-entrypoint-initdb.d` runs *only* when
`/var/lib/mysql` is empty. The mount fix does nothing to an existing database.

`02-apikeys.sql` was written to be idempotent — `CREATE TABLE IF NOT EXISTS` and
`ALTER TABLE ... ADD COLUMN IF NOT EXISTS` (MariaDB supports the latter; MySQL does
not, so this file is MariaDB-only). Apply it by hand:

```bash
cd "C:/Users/colby.west/Desktop/Projects/Gatus"
set -a; . ./.env; set +a
docker compose -f docker-compose.yml -f docker-compose.telemetry.yml \
  exec -T lltel-db mariadb -ulltel -p"$LL_DB_PASS" lltelemetry \
  < ../LL-Telemetry/telemetry/db/02-apikeys.sql

# verify
docker compose -f docker-compose.yml -f docker-compose.telemetry.yml \
  exec -T lltel-db mariadb -ulltel -p"$LL_DB_PASS" lltelemetry \
  -e "SHOW TABLES LIKE 'api_keys'; SHOW COLUMNS FROM runs LIKE 'key_label';"
```

Both rows must come back non-empty.

**Locally, prefer the clean path.** The Gatus-project volume is brand new anyway, and
if it ever gets into a bad state it holds nothing but seeded demo data:

```bash
docker compose -f docker-compose.yml -f docker-compose.telemetry.yml down -v
```

`-v` here is scoped to the compose project — it removes `gatus_lltel-data`. It does
**not** touch `./data` (Gatus's sqlite history), which is a host bind-mount, not a
named volume. Still, prefer `docker volume rm gatus_lltel-data` after a plain `down`
if you want to be certain you are only deleting the telemetry database.

---

## 4. The Go reverse proxy

New file: `api/telemetry.go`.

### 4.1 `httputil.ReverseProxy` via adaptor, or hand-rolled fiber? — Hand-rolled.

The instinct is `adaptor.HTTPHandler(httputil.NewSingleHostReverseProxy(u))`. It is
wrong here, for four reasons:

1. **The streaming advantage does not survive the adaptor.** Gatus runs fasthttp
   (fiber v2.52.13). `adaptor.HTTPHandler` goes through `fasthttpadaptor`, which
   materializes the request body into a `bytes.Reader` and collects the response into
   an in-memory `netHTTPResponseWriter` before copying it back to the fasthttp ctx.
   `ReverseProxy`'s incremental `io.Copy` and `FlushInterval` are buffered away. You
   pay the abstraction and get none of its benefit.
2. **The precedent already exists and points the other way.** `api/sse.go` and the
   Jira live streams need real streaming and are explicitly excluded from the compress
   middleware in `api.go:66-71` — the codebase already knows the adaptor is not a
   streaming path. Nothing being proxied here streams: every telemetry endpoint is a
   single bounded JSON document.
3. **House style.** `jira/jira.go:391-432` is a hand-rolled `&http.Client{Timeout:
   25*time.Second}` with explicit headers, an `io.LimitReader(resp.Body, 8<<20)` cap,
   and a status check. That is the established pattern for Gatus talking to an upstream
   HTTP service. A second, different mechanism for the same job is churn.
4. **Control is the requirement, not transparency.** `ReverseProxy` is built to pass
   things through faithfully. This proxy's entire job is the opposite — it is the auth
   and authorization boundary that replaces Caddy. It must *drop* headers by default,
   allowlist route shapes, and refuse to be transparent. Fighting `ReverseProxy`'s
   defaults with a `Director` plus `ModifyResponse` plus an `ErrorHandler` is more code
   than writing the 90 lines directly.

### 4.2 Registration

`api/api.go`, in the **protected** block, after `ApplySecurityMiddleware` — order is
load-bearing, and `api.go:167` already says so in a comment:

```go
	protectedAPIRouter.Get("/v1/live", newSSEHub().Handler)
	// LL-Telemetry console proxy. MUST stay in the protected block: the upstream
	// FastAPI has NO auth of its own (Caddy used to hold it), so Gatus's security
	// middleware is the only thing standing in front of the ingest-key admin API.
	protectedAPIRouter.All("/v1/telemetry/*", TelemetryProxy(cfg))
	return app
```

`All` rather than `Get`/`Post` — the handler owns method rejection so that a
disallowed method returns a deliberate 405 with an `Allow` header instead of fiber's
generic 404.

### 4.3 Route allowlist — and why `POST /runs` is not on it

```go
// allowedRoutes is the authorization boundary that replaces the Caddyfile's
// method matchers and its `handle /api/* { respond 405 }` catch-all. Anything
// not matched here never reaches the upstream.
//
// Deliberately ABSENT: POST /runs (field-script ingest). See the note below.
var allowedRoutes = []struct {
	method string
	re     *regexp.Regexp
}{
	{"GET", regexp.MustCompile(`^health$`)},
	{"GET", regexp.MustCompile(`^runs$`)},
	{"GET", regexp.MustCompile(`^runs/[A-Za-z0-9._-]{1,36}$`)}, // run_id is a UUID
	{"GET", regexp.MustCompile(`^stats$`)},
	{"GET", regexp.MustCompile(`^timeline$`)},
	{"GET", regexp.MustCompile(`^keys$`)},
	{"POST", regexp.MustCompile(`^keys$`)},
	{"POST", regexp.MustCompile(`^keys/[0-9]{1,10}/revoke$`)},
	{"POST", regexp.MustCompile(`^keys/[0-9]{1,10}/rotate$`)},
	{"DELETE", regexp.MustCompile(`^keys/[0-9]{1,10}$`)},
}
```

**Why ingest is excluded.** `POST /api/v1/runs` is the one route field scripts call,
and it does not belong behind this proxy:

- Field scripts authenticate with an `llk_` ingest key, not the Gatus dashboard
  credential. Behind `protectedAPIRouter` they would get 401 before the API ever saw
  their token.
- They post to the real telemetry host (`telemetry.longlewis.local`) directly. This
  local stack is a console for looking at data, not a second ingest endpoint.
- There are no field scripts running against this box at all. Demo data is seeded
  in-network (§7), never through the proxy.
- Excluding it makes the rate-limit problem in §6 largely disappear.

The regexes also do the path-traversal work: no `.` beyond the run-id charset, no
`/`, no `%2e`. A suffix like `runs/../keys` cannot match any pattern.

### 4.4 Handler

```go
package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/TwiN/gatus/v5/config"
	"github.com/TwiN/logr"
	fiber "github.com/gofiber/fiber/v2"
)

const (
	telemetryUpstreamDefault = "http://lltel-api:8080"
	telemetryMaxRequestBody  = 1 << 20 // 1 MiB; the largest legit body is {"label":"..."} 
	telemetryMaxResponseBody = 8 << 20 // 8 MiB, matching jira/jira.go's LimitReader
	telemetryTimeout         = 12 * time.Second // must stay under controller's 15s WriteTimeout
)

// telemetryClient is package-level and reused so connections are pooled across
// requests. Same shape as jira.newClient (jira/jira.go:392-397).
var telemetryClient = &http.Client{
	Timeout: telemetryTimeout,
	Transport: &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     60 * time.Second,
	},
}

func telemetryUpstream() string {
	if v := strings.TrimRight(os.Getenv("TELEMETRY_UPSTREAM"), "/"); v != "" {
		return v
	}
	return telemetryUpstreamDefault
}

// TelemetryProxy forwards /api/v1/telemetry/* to the LL-Telemetry FastAPI at
// /api/v1/*. It is the auth boundary for that API: the upstream enforces nothing
// on read and admin routes (Caddy used to), so this handler MUST only ever be
// registered on the protected router.
func TelemetryProxy(cfg *config.Config) fiber.Handler {
	base := telemetryUpstream()
	return func(c *fiber.Ctx) error {
		// --- 1. path + method allowlist -----------------------------------
		suffix := strings.TrimPrefix(c.Params("*"), "/")
		method := c.Method()
		allowed := false
		for _, r := range allowedRoutes {
			if r.method == method && r.re.MatchString(suffix) {
				allowed = true
				break
			}
		}
		if !allowed {
			// Distinguish "wrong method on a real path" from "no such path", so
			// the console gets a usable error instead of a bare 405.
			for _, r := range allowedRoutes {
				if r.re.MatchString(suffix) {
					c.Set(fiber.HeaderAllow, r.method)
					return c.Status(fiber.StatusMethodNotAllowed).
						JSON(fiber.Map{"error": "method not allowed"})
				}
			}
			return c.Status(fiber.StatusNotFound).
				JSON(fiber.Map{"error": "no such telemetry route"})
		}

		// --- 2. build the upstream URL ------------------------------------
		// url.URL escapes Path on String(); RawQuery is copied verbatim, which is
		// safe because FastAPI validates every query param (Query(ge=..., le=...)).
		u := url.URL{Scheme: "", Opaque: ""}
		target := base + "/api/v1/" + suffix
		if raw := string(c.Request().URI().QueryString()); raw != "" {
			target += "?" + raw
		}
		if _, err := url.Parse(target); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "bad path"})
		}
		_ = u

		// --- 3. request body ----------------------------------------------
		var body io.Reader
		if method == fiber.MethodPost || method == fiber.MethodDelete {
			raw := c.Body()
			if len(raw) > telemetryMaxRequestBody {
				return c.Status(fiber.StatusRequestEntityTooLarge).
					JSON(fiber.Map{"error": "request body too large"})
			}
			if len(raw) > 0 {
				// Immutable:true is set on the fiber app (api.go:57), but copy
				// anyway — c.Body() is only valid for the handler's lifetime.
				buf := make([]byte, len(raw))
				copy(buf, raw)
				body = strings.NewReader(string(buf))
			}
		}

		// --- 4. outbound request ------------------------------------------
		ctx, cancel := context.WithTimeout(c.UserContext(), telemetryTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, method, target, body)
		if err != nil {
			return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "bad upstream request"})
		}
		// Header allowlist — nothing from the client is forwarded by default.
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		// No Authorization: the upstream requires none on these routes. The
		// browser's Gatus credential is deliberately NOT forwarded.
		// No X-Forwarded-For: see §6 — the upstream trusts it blindly.

		resp, err := telemetryClient.Do(req)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				logr.Warnf("[api.TelemetryProxy] upstream timeout: %s %s", method, suffix)
				return c.Status(fiber.StatusGatewayTimeout).
					JSON(fiber.Map{"error": "telemetry API timed out", "path": suffix})
			}
			logr.Errorf("[api.TelemetryProxy] upstream error: %s", err.Error())
			return c.Status(fiber.StatusBadGateway).
				JSON(fiber.Map{"error": "telemetry API unreachable"})
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(io.LimitReader(resp.Body, telemetryMaxResponseBody))
		if err != nil {
			return c.Status(fiber.StatusBadGateway).
				JSON(fiber.Map{"error": "telemetry API read failed"})
		}

		// --- 5. response ---------------------------------------------------
		if ct := resp.Header.Get("Content-Type"); ct != "" {
			c.Set(fiber.HeaderContentType, ct)
		} else {
			c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		}
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			c.Set("Retry-After", ra)
		}
		// Everything else from upstream is dropped: Server, Date, Content-Length
		// (fiber recomputes), and all hop-by-hop headers.
		c.Set("X-Content-Type-Options", "nosniff")
		return c.Status(resp.StatusCode).Send(data)
	}
}
```

*(The `u := url.URL{}` / `_ = u` pair above is scaffolding noise — drop it and keep
just the `url.Parse` validation when implementing.)*

### 4.5 Headers — the full table

| Header | Direction | Action | Why |
| --- | --- | --- | --- |
| `Authorization` | in → up | **strip** | The browser's Gatus basic credential. Forwarding it leaks the dashboard password to a service that ignores it. |
| `Cookie` / `gatus_session` | in → up | **strip** | Session identity has no meaning upstream. |
| `X-Forwarded-For` | in → up | **strip, never set** | `client_ip()` (`main.py:163-169`) trusts the *first* XFF entry unconditionally, with no trusted-proxy check. Forwarding a client-supplied value hands the caller control of the rate-limit bucket **and** of `resolve_site()`. See §6. |
| `Host` | in → up | replaced | `net/http` sets it from the URL. |
| `Connection`, `Keep-Alive`, `TE`, `Trailer`, `Transfer-Encoding`, `Upgrade`, `Proxy-*` | in → up | **strip** | Hop-by-hop. Never proxied. Stripped for free by building a fresh `http.Request`. |
| `Accept` | added | `application/json` | Every upstream route returns JSON. |
| `Content-Type` | added | `application/json` on bodied requests | Matches `jira/jira.go:412-414`. |
| `Accept-Encoding` | in → up | **not set** | Left unset so Go's transport negotiates gzip and decompresses transparently. Gatus's own `compress` middleware then re-compresses to the browser. Forwarding the client's value would hand back a body we'd have to decode ourselves. |
| `Content-Type` | up → out | **copy** | The only response header the console needs. |
| `Retry-After` | up → out | **copy** | Set on 429s (`main.py:236-240`). Harmless to keep; free correctness if ingest is ever added. |
| `Server`, `Date`, `Content-Length` | up → out | **drop** | No upstream fingerprinting; fiber recomputes length. |
| `X-Content-Type-Options: nosniff` | added | out | Replaces the header Caddy used to add. |

**Upstream auth injection: none required.** Worth stating plainly because it looks
like an omission. `GET /runs|/stats|/timeline|/keys` and all key mutations have **no
auth check at all** in `main.py` — Caddy held it. `LL_INGEST_TOKEN` guards only
`POST /runs`, which this proxy does not forward. Hence: nothing to inject, and the
API must never be port-published, because publishing it is equivalent to publishing
an unauthenticated key-minting endpoint.

### 4.6 Streaming vs buffering

**Buffer.** Justified by measured response sizes, not by convenience:

- `GET /runs` (`main.py:317-325`) selects an explicit column list that **excludes
  `log`**. Worst case `limit=1000` with `details` JSON lands in the low single-digit
  MB.
- `GET /runs/{id}` does `SELECT *`, which *does* include `log`, capped at
  `MAX_LOG_CHARS = 300_000` (l.23). JSON-escaped worst case ≈ 600 KB.
- `/stats`, `/timeline`, `/keys` are aggregates — kilobytes.

The 8 MiB `LimitReader` clears the worst case with headroom and matches
`jira/jira.go:422`. Nothing here streams, nothing is unbounded, and the adaptor would
have buffered anyway (§4.1). Buffering also makes the timeout and size caps trivially
enforceable, which streaming would not.

---

## 5. Timeout budget

Hard ceiling: `controller/controller.go:23` sets `server.WriteTimeout = 15s`. fasthttp
tears down the connection mid-write past that — the browser sees a truncated body or a
reset, not an error page. Every budget must close inside it.

```
browser
  └─ gatus :8080          fasthttp Read/Write/Idle = 15s   ← HARD CEILING
       └─ TelemetryProxy   context 12s  +  http.Client.Timeout 12s
            └─ lltel-api   uvicorn, 1 worker, no app timeout
                 └─ mariadb  no statement timeout configured
```

| Hop | Budget | Rationale |
| --- | --- | --- |
| fasthttp WriteTimeout | 15s | Fixed. Not changing a global for one feature. |
| Proxy `context` + `Client.Timeout` | **12s** | Both, belt and braces: `Client.Timeout` covers dial→body-close; the context also cancels if the fiber request is aborted. |
| Serialize + write to browser | ~3s headroom | An 8 MiB buffered body over loopback is milliseconds. 3s is generous. |
| Upstream / DB | unbounded | The gap. See below. |

**Deliberately 12s, not 25s.** `jira/jira.go` uses 25s because it calls Atlassian over
the internet from a *background poller* with no HTTP response waiting on it. This
proxy runs inside a request whose ceiling is 15s. Copying the house number here would
guarantee truncated responses.

**The `?q=` search is the one route that can actually blow the budget.**
`main.py:307-312` builds `hostname LIKE %...% OR script LIKE ... OR site LIKE ... OR
tech LIKE ... OR result LIKE ... OR log LIKE %...%`. A leading-wildcard `LIKE` on a
`MEDIUMTEXT` column defeats every index in `01-schema.sql` — full table scan reading
every log blob. It is run **twice** per request (`count_sql` then `list_sql`,
l.315/326). Locally, against ~400 seeded rows, this is single-digit milliseconds and
will never be the thing that breaks. On a real corpus it is a genuine 10s+ query.

Behavior on overrun is defined, not accidental: at 12s the proxy returns
**`504 {"error":"telemetry API timed out","path":"runs"}`** and logs a
`logr.Warnf`. A visible, explainable failure instead of a hung tab or a truncated
response at 15s.

**Three notes for other lanes** (none are infra's to fix):

- *Frontend:* debounce the search box ≥400ms and never fire `?q=` on a single
  character.
- *API lane:* uvicorn runs a **single worker** (`api/Dockerfile` CMD) and the FastAPI
  handlers are sync `def`, so they run in the threadpool — but every one of them
  opens its own `pymysql` connection with no pool. One slow `?q=` does not block
  others, but a burst of them will exhaust threads. Consider `--workers 2` locally.
- *API lane:* the durable fix is `SET STATEMENT max_statement_time=10 FOR <query>` on
  the search path, or dropping `log` from the `?q=` predicate. Either is a one-line
  change worth far more than any proxy tuning.

---

## 6. Rate-limit distortion

**Assessment: with the §4.3 allowlist, impact is zero. No change required.**

`rate_ok()` (`main.py:114-139`) is called from exactly one place — the `POST
/api/v1/runs` handler at l.230. The proxy does not forward that route. The limiter is
never exercised through Gatus, so there is nothing to distort. The console's read
traffic (`/runs`, `/stats`, `/timeline`, `/keys`) is not rate-limited upstream at all,
before or after this change.

**If ingest is ever added to the allowlist, three things become true — record them
now:**

1. **The limit becomes global, not per-client.** `client_ip()` falls back to
   `request.client.host`, which would be the `gatus` container's address for every
   caller. 300 requests / 300s collapses from "300 per machine" to "1 run/sec across
   the entire fleet". Nine dealerships flushing offline queues after a WAN outage
   would trip it instantly. Every client gets a 429, re-queues, and retries into the
   same wall — a self-sustaining thundering herd. Raising `LL_INGEST_RATE_MAX` (already
   plumbed as an env var in §1.3) is the mitigation, but it is a blunt one.
2. **Forwarding `X-Forwarded-For` to restore per-client limits would be worse than
   the disease.** `client_ip()` reads the first XFF entry with no trusted-proxy check
   whatsoever (contrast Caddy's `trusted_proxies static private_ranges`, which the
   Caddyfile does configure — that guard is exactly what dropping Caddy removes). Any
   caller could then send `X-Forwarded-For: 1.2.3.4` to get a fresh rate-limit bucket
   on demand, **and** send `X-Forwarded-For: 10.6.81.5` to forge `site: "Muscle
   Shoals"` on their run via `resolve_site()` (l.229). The forged value is written
   straight into `runs.site` and `runs.source_ip`. That is data-integrity corruption of
   the telemetry record, not just a limiter bypass.
3. **Therefore: the proxy must strip inbound XFF unconditionally** — which the §4.4
   handler does by constructing a fresh `http.Request` and never copying client
   headers. This is the one thing in §6 that is not hypothetical: it is a real
   property the implementation must preserve, and a future "let's just pass the
   headers through" refactor would silently reintroduce the forgery.

**Upstream note for the LL-Telemetry lane:** `client_ip()` should gate on a trusted-
proxy allowlist rather than trusting XFF unconditionally. The current code is only
safe because Caddy's `trusted_proxies` sits in front of it in prod — an implicit
coupling that is not visible from `main.py`.

Two secondary properties of the limiter, noted for completeness: it is in-process, so
it resets on every API restart and would not be shared across `--workers 2`; and
`rate_ok("")` returns `True` unconditionally (l.116-117), so an empty source IP is
never limited at all.

---

## 7. Seeding demo data

There are no field-script runs on this box, so an empty console renders empty charts
and an empty timeline — useless for building or reviewing the UI.

**Seed by POSTing through the real ingest path, not by INSERTing SQL.** Posting
exercises `resolve_site()`, `clip()`, the `key_label` join, `ON DUPLICATE KEY UPDATE`
dedupe, and the `runs` column widths — so the seeded data is shaped exactly like
production data, and the seeder doubles as a smoke test of the ingest path. Raw
INSERTs would happily produce rows the API could never have created.

The seeder posts **directly to `http://lltel-api:8080` on the compose network**,
bypassing the Gatus proxy entirely — which is correct, since `POST /runs` is not a
proxied route (§4.3) and the seeder holds the ingest token, not the dashboard
credential.

Source IPs are drawn from the real `SITE_MAP` CIDRs so all three sites populate:
`10.15.100.0/22` + `10.15.104.0/22` → Ivory Tower, `10.6.81.0/24` → Muscle Shoals,
`10.10.1.0/24` → Bramlett / Decatur. Note the seeder sets the run's `ip` field —
`resolve_site` prefers `run.ip` over the source IP (l.229), so site resolution works
correctly even though every request physically originates from the seeder container.

New file: `collector/seed_telemetry.py` — stdlib only, `python:3-slim`, matching the
existing collector house style (`phone_collector.py`, `unifi_collector.py`).

```python
#!/usr/bin/env python3
"""Seed the local LL-Telemetry stack with demo runs so the console renders.

Posts through the real ingest API (not raw SQL) so every row is shaped exactly
as a field script would have produced it. Local demo data only — never point
this at the production telemetry host.
"""
import json
import os
import random
import urllib.error
import urllib.request
import uuid
from datetime import datetime, timedelta, timezone

API = os.environ.get("LLTEL_API", "http://lltel-api:8080")
TOKEN = os.environ["LL_INGEST_TOKEN"]
COUNT = int(os.environ.get("SEED_RUNS", "400"))
DAYS = int(os.environ.get("SEED_DAYS", "30"))

SITES = [
    ("Ivory Tower",        "10.15.{}.{}",  (100, 107)),
    ("Muscle Shoals",      "10.6.81.{}",   None),
    ("Bramlett / Decatur", "10.10.1.{}",   None),
]
SCRIPTS = [
    ("LL-DomainTrust",    ["HEALTHY", "HEALTHY", "HEALTHY", "REPAIRED", "NOT_JOINED", "NO_DC", "FAILED"]),
    ("LL-RenameJoin",     ["OK", "OK", "OK", "PARTIAL", "ERROR", "CANCELLED"]),
    ("LL-HarvestDrivers", ["OK", "OK", "PARTIAL", "ERROR"]),
]
MODELS = [
    ("Dell Inc.", "OptiPlex 7010", "Windows 11 Pro", "26100"),
    ("Dell Inc.", "Latitude 5540", "Windows 11 Pro", "26100"),
    ("HP",        "ProDesk 400 G7", "Windows 10 Pro", "19045"),
    ("Lenovo",    "ThinkCentre M70q", "Windows 11 Pro", "22631"),
]
TECHS = ["cwest", "jhall", "mbryant", None]


def site_ip(pattern, octet_range):
    if octet_range:
        return pattern.format(random.randint(*octet_range), random.randint(2, 250))
    return pattern.format(random.randint(2, 250))


def make_run(i):
    site, pattern, rng = random.choice(SITES)
    script, results = random.choice(SCRIPTS)
    mfr, model, os_name, build = random.choice(MODELS)
    result = random.choice(results)
    started = datetime.now(timezone.utc) - timedelta(
        seconds=random.randint(0, DAYS * 86400)
    )
    dur = random.randint(4, 240)
    host = f"LL-{site.split()[0][:3].upper()}-{random.randint(1, 60):03d}"
    log = "\n".join(
        f"[{started.isoformat()}] step {n}: {random.choice(['ok', 'ok', 'ok', 'retry', 'warn'])}"
        for n in range(random.randint(8, 40))
    )
    return {
        "schema": 1,
        "run_id": str(uuid.uuid4()),
        "script": script,
        "script_version": f"1.{random.randint(0, 4)}.{random.randint(0, 9)}",
        "result": result,
        "exit_code": 0 if result in ("HEALTHY", "OK", "REPAIRED") else random.choice([1, 2, 5]),
        "started_utc": started.isoformat().replace("+00:00", "Z"),
        "finished_utc": (started + timedelta(seconds=dur)).isoformat().replace("+00:00", "Z"),
        "duration_sec": dur,
        "hostname": host,
        "serial": f"{random.choice('ABCDEFGHJ')}{random.randint(10**6, 10**7 - 1)}",
        "manufacturer": mfr,
        "model": model,
        "os": os_name,
        "os_build": build,
        "ad_domain": "longlewis.local",
        # resolve_site() prefers run.ip over the request source IP, so the site
        # resolves correctly even though every POST comes from this container.
        "ip": site_ip(pattern, rng),
        "mac": ":".join(f"{random.randint(0, 255):02x}" for _ in range(6)),
        "tech": random.choice(TECHS),
        "details": {"seeded": True, "batch": i // 50},
        "log": log,
        "queued_offline": random.random() < 0.08,
        "tls_bypassed": random.random() < 0.03,
    }


def post(run):
    body = json.dumps(run).encode()
    req = urllib.request.Request(
        f"{API}/api/v1/runs",
        data=body,
        method="POST",
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {TOKEN}"},
    )
    with urllib.request.urlopen(req, timeout=15) as r:
        return json.loads(r.read())


def main():
    if "longlewis" in API or "10.15.102.8" in API:
        raise SystemExit(f"refusing to seed a non-local target: {API}")
    ok = fail = 0
    sites = {}
    for i in range(COUNT):
        try:
            res = post(make_run(i))
            ok += 1
            sites[res.get("site")] = sites.get(res.get("site"), 0) + 1
        except urllib.error.HTTPError as e:
            fail += 1
            if fail <= 3:
                print(f"  HTTP {e.code}: {e.read()[:300].decode(errors='replace')}")
        except Exception as e:
            fail += 1
            if fail <= 3:
                print(f"  {type(e).__name__}: {e}")
    print(f"seeded {ok} runs, {fail} failed")
    for s, n in sorted(sites.items(), key=lambda kv: -kv[1]):
        print(f"  {s or '(unresolved)'}: {n}")
    if fail:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
```

Also seed a couple of API keys so the Keys panel is not empty — this is the direct
test of the §3 schema fix:

```bash
docker compose -f docker-compose.yml -f docker-compose.telemetry.yml \
  run --rm --entrypoint python lltel-seed -c "
import json,urllib.request
for label in ['2026-Q3 USB batch','Bench imaging cart','Decatur field kit']:
    r=urllib.request.Request('http://lltel-api:8080/api/v1/keys',
        data=json.dumps({'label':label}).encode(),method='POST',
        headers={'Content-Type':'application/json'})
    print(json.loads(urllib.request.urlopen(r,timeout=10).read()))
"
```

That prints each plaintext secret once — the same one-time-reveal the console shows.
If any of these 500, the §3 fix did not take.

Seeding is idempotent-ish by accident: every run gets a fresh UUID, so re-running adds
400 more rows rather than replacing them. To reset, `down -v` and re-seed.

---

## 8. Operational runbook

All commands from `C:\Users\colby.west\Desktop\Projects\Gatus`. Git Bash, not
PowerShell.

### 8.1 One-time setup

```bash
cd "C:/Users/colby.west/Desktop/Projects/Gatus"

# 1. LL-Telemetry checkout must be a sibling of the Gatus repo
test -f ../LL-Telemetry/telemetry/api/main.py && echo "LL-Telemetry OK" || echo "MISSING"

# 2. Append the telemetry secrets to .env (gitignored, survives update.sh)
cat >> .env <<'EOF'

# --- LL-Telemetry (local console stack) -----------------------------------
# Local-only values. These are NOT the production telemetry secrets and must
# never be reused there.
LL_INGEST_TOKEN=REPLACE_openssl_rand_hex_32
LL_DB_PASS=REPLACE_local_db_password
LL_DB_ROOT_PASS=REPLACE_local_root_password
LL_INGEST_RATE_MAX=300
LL_INGEST_RATE_WINDOW_SEC=300
# Upstream the Gatus proxy dials. Compose service name, never a published port.
TELEMETRY_UPSTREAM=http://lltel-api:8080
# Gatus dashboard credential — see §0. base64(URLEncoding) of a bcrypt hash.
GATUS_ADMIN_BCRYPT_B64=REPLACE_see_section_0
EOF

# generate the two random values
openssl rand -hex 32   # -> LL_INGEST_TOKEN
openssl rand -hex 16   # -> LL_DB_PASS / LL_DB_ROOT_PASS

# 3. Gatus admin credential (§0)
docker run --rm caddy:2-alpine caddy hash-password --plaintext 'YourPassword' \
  | tr -d '\n' | base64 -w0 | tr '+/' '-_'
# -> paste into GATUS_ADMIN_BCRYPT_B64, then add the security: block to config.yaml

# 4. Now edit .env and replace every REPLACE_ placeholder.
grep -n REPLACE_ .env && echo "!! still placeholders" || echo "env OK"
```

### 8.2 Bring it up

```bash
# Define once per shell — every telemetry command needs both -f flags.
TC='-f docker-compose.yml -f docker-compose.telemetry.yml'

docker compose $TC config >/dev/null && echo "compose valid"

# NOTE: `up -d --build` does NOT re-register external-endpoints. config.yaml
# changed (the security: block), so this needs a full down/up.
docker compose down
docker compose $TC up -d --build

docker compose $TC ps
```

Expected: `gatus`, `phone-collector`, `unifi-collector`, `lltel-db (healthy)`,
`lltel-api (healthy)`. `lltel-seed` absent — it is behind the `seed` profile.

### 8.3 Verify

```bash
TC='-f docker-compose.yml -f docker-compose.telemetry.yml'
set -a; . ./.env; set +a

# 1. Gatus itself still fine, and the collectors did not lose it
curl -fsS http://localhost:8080/health && echo " gatus OK"
docker compose $TC logs --tail=20 phone-collector | grep -i "error\|refused" || echo "collector OK"

# 2. The API is NOT reachable from the host. Both MUST fail.
curl -sS --max-time 3 http://localhost:8080/../ >/dev/null 2>&1
! curl -fsS --max-time 3 http://localhost:8000/api/v1/health && echo "api not published OK"
docker compose $TC port lltel-api 8080 2>/dev/null && echo "!! PORT PUBLISHED - FIX" || echo "no published port OK"

# 3. The API IS reachable from inside the network
docker compose $TC exec -T lltel-api \
  python -c "import urllib.request;print(urllib.request.urlopen('http://127.0.0.1:8080/api/v1/health').read())"

# 4. Schema fix landed (this is the §3 check)
docker compose $TC exec -T lltel-db mariadb -ulltel -p"$LL_DB_PASS" lltelemetry \
  -e "SHOW TABLES LIKE 'api_keys'; SHOW COLUMNS FROM runs LIKE 'key_label';"
# both must return a row

# 5. Auth is actually on — this MUST be 401
curl -s -o /dev/null -w "no-creds: %{http_code}\n" \
  http://localhost:8080/api/v1/telemetry/health
# ...and this MUST be 200
curl -s -u itadmin:'YourPassword' -o /dev/null -w "with-creds: %{http_code}\n" \
  http://localhost:8080/api/v1/telemetry/health

# 6. Allowlist rejects what it should
curl -s -u itadmin:'YourPassword' -o /dev/null -w "ingest blocked: %{http_code}\n" \
  -X POST http://localhost:8080/api/v1/telemetry/runs        # expect 405
curl -s -u itadmin:'YourPassword' -o /dev/null -w "traversal: %{http_code}\n" \
  "http://localhost:8080/api/v1/telemetry/runs/../keys"      # expect 404
curl -s -u itadmin:'YourPassword' -o /dev/null -w "unknown: %{http_code}\n" \
  http://localhost:8080/api/v1/telemetry/openapi.json        # expect 404
```

### 8.4 Seed

```bash
TC='-f docker-compose.yml -f docker-compose.telemetry.yml'
docker compose $TC --profile seed run --rm lltel-seed
# expect: "seeded 400 runs, 0 failed" plus a per-site breakdown covering all 3 sites

curl -s -u itadmin:'YourPassword' \
  "http://localhost:8080/api/v1/telemetry/runs?limit=3" | head -c 400
curl -s -u itadmin:'YourPassword' \
  http://localhost:8080/api/v1/telemetry/keys
```

### 8.5 Day-to-day

```bash
TC='-f docker-compose.yml -f docker-compose.telemetry.yml'

docker compose $TC logs -f lltel-api                    # tail the API
docker compose $TC restart lltel-api                    # after editing SITE_MAP
docker compose $TC up -d --build lltel-api              # after editing main.py
docker compose $TC exec -T lltel-db mariadb -ulltel -p"$LL_DB_PASS" lltelemetry  # SQL shell
```

There is no shell in the `gatus` container (`FROM scratch`) — debug it via
`docker compose logs gatus` and `curl` from the host, not `exec`.

### 8.6 Tear down

```bash
TC='-f docker-compose.yml -f docker-compose.telemetry.yml'

# Stop telemetry, keep Gatus and the seeded database
docker compose $TC stop lltel-api lltel-db

# Remove telemetry containers, keep the data volume
docker compose $TC rm -sf lltel-api lltel-db lltel-seed

# Full reset INCLUDING the seeded database
docker compose $TC down
docker volume rm gatus_lltel-data

# Back to Gatus-only. --remove-orphans is what deletes the telemetry containers.
docker compose down
docker compose up -d --build --remove-orphans
```

**Do not run `docker compose $TC down -v`.** `-v` removes every named volume in the
project. Today that is only `gatus_lltel-data`, but the flag is unscoped and any
future named volume goes with it. Remove the volume by name instead — it is one extra
word and it cannot surprise you. (Gatus's sqlite history in `./data` is a host bind-
mount and is not affected either way.)

**Do not run `./update.sh` while developing this.** `git reset --hard` +
`git clean -fd` discards `docker-compose.telemetry.yml` and `collector/seed_telemetry.py`
until both are committed, and its bare `docker compose up -d` will not see the
telemetry file regardless.

---

## 9. Change inventory

| Path | Change |
| --- | --- |
| `docker-compose.telemetry.yml` | **new** — telemetry services. Commit it; never auto-loaded. |
| `collector/seed_telemetry.py` | **new** — stdlib demo seeder. |
| `api/telemetry.go` | **new** — `TelemetryProxy` + `allowedRoutes`. |
| `api/api.go` | +1 line in the **protected** block (after `ApplySecurityMiddleware`). |
| `api/api.go` | +1 SPA route `app.Get("/telemetry", ...)` — frontend lane. |
| `config.yaml` | **+`security.basic` block (§0 — blocking).** Gitignored; local only. |
| `.env` | +`LL_INGEST_TOKEN`, `LL_DB_PASS`, `LL_DB_ROOT_PASS`, `LL_INGEST_RATE_*`, `TELEMETRY_UPSTREAM`, `GATUS_ADMIN_BCRYPT_B64`. |
| `../LL-Telemetry/telemetry/docker-compose*.yml` | **upstream fix** — `./db:/docker-entrypoint-initdb.d:ro`. Separate repo, separate change. |
| `docker-compose.yml` | **UNCHANGED.** Do not touch — that file reaches prod. |

## 10. Open items for other lanes

- **Frontend:** port `telemetry/dashboard/index.html` into the Vue SPA; change
  `const API='/api/v1'` (l.318) to `/api/v1/telemetry`. Debounce the search box.
- **API/LL-Telemetry:** `client_ip()` should gate XFF on a trusted-proxy allowlist
  (§6). The `?q=` search needs a statement timeout or a narrower predicate (§5).
- **Whoever owns the decision:** §0 — turning on `security.basic` puts the existing
  status dashboard behind a credential too. That is a product call, and it is the
  gate on shipping the Keys panel at all.
