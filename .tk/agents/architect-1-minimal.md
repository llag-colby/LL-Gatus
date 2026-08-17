# APPROACH: Minimal — "Embed one HTML blob, proxy nine paths, reuse the positional auth split"

Architect 1 of N. Optimizing for least new code / fewest moving parts.

**Total new Go code: one file, ~120 lines. New Go dependencies: zero. New `//go:embed` directives: one line added to an existing file. New compose services: two. Services deleted: one (Caddy).**

---

## 0. Verification notes (things I checked that change the design)

These were confirmed by reading the files, and two of them make the design meaningfully smaller than the brief assumes:

| Finding | Where | Consequence |
|---|---|---|
| The console has **zero external subresources** — no `<link>`, no `<script src>`, no `<img>`, no CDN. Styles are inline at line 7, JS inline at line 317. | `telemetry/dashboard/index.html` | Deliverable 3's "how do the iframe's subresource requests stay authenticated" has a trivial answer: **there are none.** The iframe issues exactly two kinds of request — its own document GET and its `fetch()` calls. Both are same-origin. |
| The console **has no SSE / EventSource**. It polls with `setInterval(...,5000)` at line 559. | line 559; no `EventSource` anywhere | **The `compress` `Next` predicate at `api/api.go:66-73` needs NO change.** The brief flagged this as a risk; it isn't one. Buffered proxying is correct and gzip is desirable. |
| Console API call sites are 9 concrete paths, not an open surface. | lines 507, 533, 542, 542, 605, 606, 608, 609, 610 | The proxy allowlist can be exact rather than a passthrough. Enumerated in §2. |
| `/api/v1/health` is **not** called by the console — health comes from `st.service` inside the `/stats` payload (line 543 `renderHealth(st.service)`). | line 542-543 | One fewer path to allow (I still allow it; see §2). |
| OIDC session cookie is `SameSite=Strict`. | `security/oidc.go:148` | Fine — a **same-origin** iframe is same-site, so Strict still sends it. Would have broken a cross-origin iframe. This is the load-bearing reason DECIDED #2 (same-origin framing) is right. |
| `config.yaml` has **no `security:` block at all**. | `config.yaml` (only `storage`, `ui`, `endpoints`) | Confirms `cfg.Security == nil`. Registering routes on `protectedAPIRouter` is currently a **no-op for auth**. Enabling security is the single riskiest part of this work — see §8. |
| Prod `Caddyfile` sets `X-Frame-Options: DENY`. | `telemetry/caddy/Caddyfile:84` | Another reason Caddy must not sit in front of the framed console. Dropping it removes the problem rather than working around it. |
| App.vue already imports icons from `lucide-vue-next`. | `App.vue:180` | Use a lucide component for the header button — **no new binary asset**, and no exposure to the `header img { filter: brightness(0) invert(1) }` custom-css rule that forced the Jira button to use a background-image span. |

---

## 1. File-by-file change list

### NEW files (3)

| Path | What | Why |
|---|---|---|
| `C:\Users\colby.west\Desktop\Projects\Gatus\web\telemetry\console.html` | Byte-for-byte copy of `LL-Telemetry\telemetry\dashboard\index.html`, with **one** change: line 590 `fmt(` → `fmtTime(`. | See §4 for why this location. The `fmt` fix is the confirmed bug; Keys is in scope so it must be fixed. Fix it upstream in LL-Telemetry too so the two copies stay identical. |
| `C:\Users\colby.west\Desktop\Projects\Gatus\api\telemetry.go` | The only new Go file. Two exported constructors: `TelemetryConsole()` and `TelemetryProxy()`. ~120 lines. | §2, §3. |
| `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\views\Telemetry.vue` | ~25 lines: a full-bleed `<iframe src="/api/v1/telemetry/console">`. | DECIDED #2. |

### EDITS (6)

| Path | Change | Why |
|---|---|---|
| `web\static.go` | Add one directive + var to the **existing** `package static`. No new package, no new file. | Reuse the one embed we already have. |
| `api\api.go` | +1 SPA line at ~139 (`app.Get("/telemetry", ...)`); +2 protected route lines after 185. | No catch-all fallback exists (verified), so the SPA deep link must be listed. |
| `web\app\src\router\index.js` | +1 route object `/telemetry`. | |
| `web\app\src\App.vue` | +`Activity` to the lucide import on line 180; +1 `<router-link>` after line 87. | |
| `docker-compose.yml` | +2 services, +1 volume, +`TELEMETRY_API_BASE` on `gatus`. | §5. |
| `config.yaml` | +`security:` block. | §3 — **without this, deliverable 3 is not satisfied.** |

### Explicitly NOT changed
- `api/api.go:66-73` compress `Next` predicate — no streaming (verified §0).
- `controller/controller.go` — the 15s `WriteTimeout` stays; the proxy timeout is set under it (§2).
- `go.mod` / `go.sum` — `middleware/proxy` ships inside `github.com/gofiber/fiber/v2 v2.52.13`, already in the module graph.
- `Dockerfile` — `COPY . ./` already picks up `web/telemetry/`.
- `LL-Telemetry/telemetry/api/main.py` — untouched (except the optional upstream `fmt` fix mirrored into `dashboard/index.html`).

---

## 2. The reverse-proxy handler

`api/telemetry.go`:

```go
package api

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"time"

	static "github.com/TwiN/gatus/v5/web"
	"github.com/TwiN/logr"
	fiber "github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/proxy"
)

const (
	// Comfortably under controller.Handle's 15s WriteTimeout so a slow upstream
	// surfaces as our 504 envelope rather than fasthttp killing the connection
	// mid-write (which the console renders as an opaque "Failed to fetch").
	telemetryProxyTimeout = 10 * time.Second
	// The only bodies that ever travel this path are {"label":"..."} (<=96 chars).
	telemetryMaxBody = 64 * 1024
	telemetryDefaultBase = "http://lltel-api:8080"
)

// Exact upstream paths the console uses, verified against dashboard/index.html.
// Anything not listed 404s here and never reaches FastAPI.
var (
	telemetryGET = regexp.MustCompile(
		`^(health|runs|runs/[A-Za-z0-9._:-]{1,64}|stats|timeline|keys)$`)
	telemetryPOST = regexp.MustCompile(
		`^(keys|keys/[0-9]{1,10}/(revoke|rotate))$`)
	telemetryDELETE = regexp.MustCompile(
		`^keys/[0-9]{1,10}$`)
)

func telemetryAllowed(method, sub string) bool {
	switch method {
	case fiber.MethodGet:
		return telemetryGET.MatchString(sub)
	case fiber.MethodPost:
		return telemetryPOST.MatchString(sub)
	case fiber.MethodDelete:
		return telemetryDELETE.MatchString(sub)
	}
	return false
}

func telemetryError(c *fiber.Ctx, status int, msg string) error {
	return c.Status(status).JSON(fiber.Map{"detail": msg})
}

// TelemetryProxy forwards /api/v1/telemetry/<sub> to <base>/api/v1/<sub>.
func TelemetryProxy() fiber.Handler {
	base := strings.TrimSuffix(os.Getenv("TELEMETRY_API_BASE"), "/")
	if base == "" {
		base = telemetryDefaultBase
	}
	logr.Infof("[api.TelemetryProxy] Proxying /api/v1/telemetry/* to %s/api/v1/*", base)
	return func(c *fiber.Ctx) error {
		sub := strings.Trim(c.Params("*"), "/")
		if !telemetryAllowed(c.Method(), sub) {
			return telemetryError(c, fiber.StatusNotFound, "no such telemetry endpoint")
		}
		if len(c.Body()) > telemetryMaxBody {
			return telemetryError(c, fiber.StatusRequestEntityTooLarge, "request body too large")
		}
		url := base + "/api/v1/" + sub
		if qs := c.Request().URI().QueryString(); len(qs) > 0 {
			url += "?" + string(qs)
		}
		// Never leak the Gatus credential (Basic header / gatus_session cookie)
		// into the telemetry container — it has no use for either.
		c.Request().Header.Del(fiber.HeaderAuthorization)
		c.Request().Header.Del(fiber.HeaderCookie)
		if err := proxy.DoTimeout(c, url, telemetryProxyTimeout); err != nil {
			logr.Errorf("[api.TelemetryProxy] %s %s: %s", c.Method(), sub, err.Error())
			return telemetryError(c, fiber.StatusBadGateway, "telemetry service unavailable")
		}
		c.Response().Header.Del(fiber.HeaderServer)
		return nil
	}
}
```

**Design answers:**

- **Path rewriting.** `/api/v1/telemetry/<sub>` → `<base>/api/v1/<sub>`. One `strings.Trim` + concat. The rewrite is a pure prefix swap, which is exactly why the console-side rewrite in §4 is a one-token change.
- **Allowed upstream paths.** Nine, from the verified call sites: `GET runs`, `GET runs/{id}`, `GET stats`, `GET timeline`, `GET keys`, `POST keys`, `POST keys/{id}/revoke`, `POST keys/{id}/rotate`, `DELETE keys/{id}`. Plus `GET health` — unused by the console but free, and it makes "is the proxy wired up?" answerable from the browser address bar.
  **`POST /api/v1/runs` (ingest) is deliberately NOT allowed.** Ingest is token-authenticated by FastAPI itself and must not be reachable behind the operator credential; it gets its own door (§5).
- **Timeouts.** 10s, vs the server's 15s `WriteTimeout` (`controller/controller.go:23`). Headroom matters: `DoTimeout` failing gives a clean `502`/`504` JSON body the console's `showErr()` can render; blowing the `WriteTimeout` instead truncates the response mid-flight.
- **Streaming vs buffering.** **Buffering.** Verified: the console polls, it does not stream. `proxy.DoTimeout` buffers, the global `compress` middleware gzips the result, and nothing in `api.go:66-73` needs touching.
- **Error envelope.** `{"detail": "..."}` — matching FastAPI's own `HTTPException` shape, so the console's `throw new Error(p+' → '+r.status)` path behaves identically whether the error came from FastAPI or from us. Upstream error bodies pass through untouched.
- **Request body limits.** Hard 64KB check before forwarding, plus fiber's global 4MB `BodyLimit` behind it. Only `{"label": "..."}` ever legitimately travels here.
- **Credential stripping.** `Authorization` and `Cookie` are deleted before forwarding. Fiber's `proxy.Do*` copies the inbound request wholesale, so without this the Gatus basic-auth credential would be handed to the telemetry container on every call.

---

## 3. Auth — on the proxy routes AND the console HTML

### The trick: serve the console from under `/api/`

`ApplySecurityMiddleware` is applied to `protectedAPIRouter`, which is `apiRouter.Group("/")` — i.e. it only ever covers `/api/*`. A console served at `/telemetry/console.html` would need a **second** security group, which means new plumbing in `security/config.go` and a second `ApplySecurityMiddleware` call site.

Instead, serve the console document itself from `/api/v1/telemetry/console`. It inherits the existing group with **zero new middleware plumbing**. The Vue view frames that URL.

`api/api.go`, appended after line 185:

```go
	// Telemetry console (LL-Telemetry). Served from under /api so it inherits
	// the security middleware above — there is no second protected group.
	// Static route registered BEFORE the wildcard so it isn't swallowed by it,
	// same reason as /v1/phones/sweep-pending and /v1/unifi above.
	protectedAPIRouter.Get("/v1/telemetry/console", TelemetryConsole())
	protectedAPIRouter.All("/v1/telemetry/*", TelemetryProxy())
```

Registration order handles the `console` / `*` overlap — the established house pattern (`api.go:104-105`, `112-119`). Belt and braces: `console` is not in the proxy allowlist either, so even if order were wrong it would 404 rather than hit FastAPI.

### Turning auth on

Currently `cfg.Security == nil`, so the protected group is protected in name only. `config.yaml` needs:

```yaml
security:
  basic:
    username: "itadmin"
    password-bcrypt-base64: "${GATUS_BASIC_BCRYPT_B64}"
```

**This is the highest-risk edit in the whole change.** See §8.

### Basic vs OIDC, concretely

There are only two requests to reason about, because the console has no subresources:

1. **The iframe document** — `GET /api/v1/telemetry/console`
2. **The console's `fetch()` calls** — `GET|POST|DELETE /api/v1/telemetry/<sub>`

Both are same-origin with the parent page.

**Basic** (`security/config.go:77-90`, fiber `basicauth`):
- The browser caches the credential per origin+realm after the first 401 challenge and replays it on every subsequent same-origin request, including iframe document loads and `fetch()` from inside that iframe.
- The SPA already hits `GET /api/v1/endpoints/statuses` (line 176) on first paint, so the challenge fires and is satisfied *before* the user can ever reach `/telemetry`. The iframe is authenticated by the time it loads.
- The console's `fetch()` calls use `credentials: 'same-origin'` (the default) — irrelevant for Basic, which travels as a cached header, but harmless.
- Edge case: if the credential is somehow *not* cached, the 401 renders a native browser auth prompt **inside the iframe**. Mitigation is the `Cache-Control: no-store` + the fact that the outer app 401s first. Acceptable.

**OIDC** (`security/config.go:49-67`, g8 gate + `gatus_session` cookie):
- Cookie is `SameSite=Strict` (`security/oidc.go:148`). A **same-origin** iframe is same-site, so Strict sends it. This is precisely why DECIDED #2's same-origin framing is load-bearing — a cross-origin iframe (e.g. `localhost:4554`) would silently drop the cookie and every console call would 401.
- Unauthenticated requests get a bare **401**, not a redirect. The console's `api()` throws and `showErr()` paints an error banner inside the frame. That's the honest failure mode, and it can only be reached if the session expired while the tab sat open — `App.vue` renders the OIDC login card instead of the router-view when unauthenticated, so `/telemetry` is unreachable without a session.
- Optional 1-line polish in `Telemetry.vue`: on `iframe.onload`, nothing to do; if you want expiry handling, `window.location.reload()` on a `message` from the frame. **Skip it** — out of scope for minimal.

### Framing headers

Gatus sets no `X-Frame-Options` today, and Caddy (which set `DENY`) is being removed, so framing works by default. `TelemetryConsole()` still sets `Content-Security-Policy: frame-ancestors 'self'` explicitly — it costs one line and prevents an external site from framing the console if Gatus is ever exposed.

---

## 4. Storing and serving the console HTML

### Where it lives: `web/telemetry/console.html`

Rejected alternative: `web/app/public/telemetry-console.html`. Vue CLI copies `public/` verbatim so it survives `npm run build` — but the output lands in `web/static/`, which `api.go:158-162` serves through `fiberfs` mounted at `/` **before** the protected router. The console would be readable by anyone, unauthenticated, at `/telemetry-console.html`. That directly violates requirement 3.

`web/telemetry/` is:
- **outside `outputDir` (`../static`)**, so Vue CLI's clean step cannot reach it — this is a structural guarantee, not a convention;
- **outside `fs.Sub(static.FileSystem, "static")`**, so `fiberfs` cannot accidentally serve it;
- reachable by the existing `//go:embed` mechanism with one added line.

`web/static.go` (edit — same file, same package, no new package):

```go
var (
	//go:embed static
	FileSystem embed.FS

	// LL-Telemetry operations console, served (gated) at
	// /api/v1/telemetry/console. Deliberately NOT under static/: everything in
	// static/ is served unauthenticated by fiberfs, and `npm run build` wipes
	// that directory.
	//go:embed telemetry/console.html
	TelemetryConsoleHTML []byte
)
```

### How `API` becomes `/api/v1/telemetry`: serve-time rewrite

**Chosen: serve-time, computed once at router construction.** Justification against the alternatives:

| Option | Verdict |
|---|---|
| Hand-edit line 318 in our copy | This is the "fork" the brief asks me to avoid. It creates a *silent* divergence: a future `diff` against upstream shows a real change and it's ambiguous whether it's intentional. |
| Build-time codegen / sed step | Requires a build step. There is no npm on this machine and the Dockerfile runs no generator. Adding one is a new moving part for a single token. |
| **Serve-time `bytes.Replace`, once, with an assertion** | Our copy stays diffable against upstream modulo the one real bug fix. The rewrite is self-documenting and **self-checking**. |

The assertion is the reason this beats forking: if upstream ever reformats line 318, a forked file would silently keep working *against the wrong base URL* until someone noticed every panel was empty. Here, Gatus refuses to start.

```go
// TelemetryConsole serves the embedded LL-Telemetry console with its API base
// repointed at our proxy prefix. The rewrite happens once, at router
// construction, and hard-fails if the upstream file changed shape — a silent
// miss would leave the console fetching Gatus's own /api/v1 and rendering empty.
func TelemetryConsole() fiber.Handler {
	const oldBase = `const API='/api/v1'`
	const newBase = `const API='/api/v1/telemetry'`
	if n := bytes.Count(static.TelemetryConsoleHTML, []byte(oldBase)); n != 1 {
		panic("api.TelemetryConsole: expected exactly 1 occurrence of `" +
			oldBase + "` in web/telemetry/console.html, found " + strconv.Itoa(n))
	}
	html := bytes.Replace(static.TelemetryConsoleHTML, []byte(oldBase), []byte(newBase), 1)
	return func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
		c.Set("Content-Security-Policy", "frame-ancestors 'self'")
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("Cache-Control", "no-store")
		return c.Send(html)
	}
}
```

Verified: `index.html:318` is exactly `const API='/api/v1';`, and it is the only base-URL definition. The needle omits the semicolon so it is robust to that one detail.

Note this is *not* rendered through `html/template` (unlike `SinglePageApplication` in `api/spa.go`) — the console contains `{{` -free JS, but running an untemplated file through `template.ParseFS` would be pointless work and a parse-error hazard. Plain `c.Send` is correct.

### Vue side

`web/app/src/views/Telemetry.vue` (new):

```vue
<template>
  <div class="w-full" style="height: calc(100vh - 12rem)">
    <iframe
      src="/api/v1/telemetry/console"
      title="LL-Telemetry Operations Console"
      class="w-full h-full border-0 rounded-md"
    ></iframe>
  </div>
</template>
```

`web/app/src/router/index.js` (edit) — add after the `/jira` entry:

```js
    {
        path: '/telemetry',
        name: 'Telemetry',
        component: () => import('@/views/Telemetry')
    }
```

`web/app/src/App.vue` (edit) — line 180 becomes:

```js
import { Activity, LogIn, Maximize, Minimize, RefreshCw, Volume2, VolumeX } from 'lucide-vue-next'
```

and after line 87 (`</router-link>` closing the Jira link):

```html
                <router-link
                  to="/telemetry"
                  class="inline-flex items-center justify-center h-9 w-9 rounded-md hover:bg-accent transition-colors"
                  data-tooltip="Script telemetry"
                  data-tip-pos="bottom"
                  aria-label="Script telemetry"
                >
                  <Activity class="h-5 w-5" />
                </router-link>
```

A lucide component, not a `.png` — so no new binary asset, and it sidesteps the `header img { filter: brightness(0) invert(1) }` rule in `config.yaml`'s `custom-css` that forced the Jira icon into a background-image span.

`api/api.go` (edit) — after line 138:

```go
	app.Get("/telemetry", SinglePageApplication(cfg.UI))
```

Required: `api.go:134-138` lists deep links explicitly and there is no catch-all. Without it, a hard refresh on `/telemetry` falls through to `fiberfs`, which 404s.

### The npm problem

Those three Vue edits require a rebuild, and `web/static/js/app.js` is committed build output with no npm on this box. One-shot container build:

```bash
docker run --rm -v "$PWD/web:/w" -w /w/app node:20-alpine sh -c "npm ci && npm run build"
```

Then commit the regenerated `web/static/`. `filenameHashing: false` means the diff is confined to `js/app.js`, `js/chunk-vendors.js`, `css/app.css` (+ a new lazy chunk for the `/telemetry` route) — the same files already dirty in `git status`.

**Zero-build fallback, if the container build is a blocker:** `App.vue:91-101` already renders `cfg.UI.Buttons` as header nav links. Adding

```yaml
ui:
  buttons:
    - name: Telemetry
      link: /api/v1/telemetry/console
```

gives a working, authenticated header link with **no frontend build at all**. It is a text nav item that opens in a new tab (`target="_blank"`) and is hidden below `md`, so it does not satisfy "small icon button, framed in-app" — but it lets the Go + compose half ship and be validated independently, and it is a genuine fallback if the build environment fights back. Recommend building the Vue view; keep this in the back pocket.

---

## 5. docker-compose additions

### Is Caddy needed? **No — delete it.**

Caddy did four jobs. Gatus now does three of them and the fourth moves:

| Caddy job | Now |
|---|---|
| TLS termination | Gatus's own `web.tls` (or none, for a local stack) |
| Basic-auth on dashboard + read + keys | **Gatus `security:` + the protected router.** Gatus is the auth boundary. |
| Serve `dashboard/index.html` | `TelemetryConsole()` from the Go embed |
| Route `POST /api/v1/runs` (ingest, no auth — token-checked by FastAPI) | A published port on `lltel-api` |

Dropping it removes one service and two volumes (`caddy-data`, `caddy-config`), and removes the `X-Frame-Options: DENY` that would otherwise have to be worked around.

### Appended to `C:\Users\colby.west\Desktop\Projects\Gatus\docker-compose.yml`

```yaml
  # LL-Telemetry database. Init scripts run ONLY on a fresh lltel-data volume.
  # Both files are mounted — upstream's compose mounts only 01, which is why the
  # api_keys table and runs.key_label never exist on a fresh stack (the Keys
  # panel then 500s). See ../LL-Telemetry/telemetry/db/.
  lltel-db:
    image: mariadb:11.4
    container_name: lltel-db
    restart: always
    environment:
      MARIADB_ROOT_PASSWORD: ${LL_DB_ROOT_PASS}
      MARIADB_DATABASE: lltelemetry
      MARIADB_USER: lltel
      MARIADB_PASSWORD: ${LL_DB_PASS}
      TZ: America/Chicago
    volumes:
      - ../LL-Telemetry/telemetry/db/01-schema.sql:/docker-entrypoint-initdb.d/01-schema.sql:ro
      - ../LL-Telemetry/telemetry/db/02-apikeys.sql:/docker-entrypoint-initdb.d/02-apikeys.sql:ro
      - lltel-data:/var/lib/mysql
    healthcheck:
      test: ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]
      interval: 10s
      timeout: 5s
      retries: 12
      start_period: 60s

  # LL-Telemetry FastAPI. Reached by gatus over the compose network as
  # http://lltel-api:8080 (TELEMETRY_API_BASE). Port 4555 is published for the
  # field-script ingest path only — POST /api/v1/runs enforces LL_INGEST_TOKEN in
  # application code, exactly as it did behind Caddy's unauthenticated @ingest
  # handler. The console never uses this port; it goes through the Gatus proxy.
  lltel-api:
    build: ../LL-Telemetry/telemetry/api
    image: lltel-api:local
    container_name: lltel-api
    restart: always
    ports:
      - "4555:8080"
    environment:
      LL_INGEST_TOKEN: ${LL_INGEST_TOKEN}
      LL_DB_HOST: lltel-db
      LL_DB_USER: lltel
      LL_DB_PASS: ${LL_DB_PASS}
      LL_DB_NAME: lltelemetry
      LL_INGEST_RATE_MAX: ${LL_INGEST_RATE_MAX:-300}
      LL_INGEST_RATE_WINDOW_SEC: ${LL_INGEST_RATE_WINDOW_SEC:-300}
      TZ: America/Chicago
    depends_on:
      lltel-db:
        condition: service_healthy

volumes:
  lltel-data:
```

And on the existing `gatus` service, add to `environment:`:

```yaml
      - TELEMETRY_API_BASE=http://lltel-api:8080
```

**Networks:** none declared. All five services land on the implicit `default` network and resolve each other by service name — identical to how `phone-collector` already reaches `GATUS_PUSH_BASE=http://gatus:8080`. Zero new network config.

**`LL_DB_HOST` changes from `db` to `lltel-db`.** Upstream's service is named `db`; renamed here to avoid a generic name colliding in a shared compose file. `main.py:18` reads it from env with a default, so no code change.

### Three compose gotchas, called out because they will bite

1. **Volume namespacing.** Upstream compose declares `name: ll-telemetry`, so its volume is `ll-telemetry_lltel-data`. Merged into Gatus's compose, the project name becomes `gatus`, so the volume is `gatus_lltel-data` — **a different, empty volume.** Existing telemetry data does not follow. If it must, declare it external:
   ```yaml
   volumes:
     lltel-data:
       external: true
       name: ll-telemetry_lltel-data
   ```
   With the external volume, the init scripts **will not run** (non-empty datadir), so `02-apikeys.sql` must be applied by hand — see step 6 in §7.
2. **Sibling-repo build context.** `build: ../LL-Telemetry/telemetry/api` reaches outside the Gatus repo. It breaks for anyone who has not checked out LL-Telemetry as a sibling. Acceptable for a local stack on this machine and it is by far the smallest option; the alternatives (vendoring the FastAPI into Gatus, or publishing an image to a registry) are both larger. Document it in `docs/`.
3. **`.env` merge.** Gatus's `gatus` service already does `env_file: [.env]`. The `LL_*` vars must go into Gatus's `.env` (which currently holds `JIRA_*`, `PHONES_*`, `UNIFI_API_KEY`). `lltel-db`/`lltel-api` read them via compose `${...}` interpolation, which reads `.env` from the compose file's directory — the same file. No `env_file:` needed on the new services.

---

## 6. Environment variables

Convention observed in the existing `.env`: `<FEATURE>_<SCOPE>_<KIND>` — `PHONES_PUSH_TOKEN`, `PHONES_IVORY_TOWER_TOKEN`, `JIRA_API_TOKEN`, `JIRA_BASE_URL`, `UNIFI_API_KEY`, `GATUS_PUSH_BASE`.

**New, consumed by Gatus (follow the convention):**

| Var | Value | Consumed at |
|---|---|---|
| `TELEMETRY_API_BASE` | `http://lltel-api:8080` | `api/telemetry.go`, `TelemetryProxy()`. Optional — falls back to that same default. |
| `GATUS_BASIC_BCRYPT_B64` | base64 of a bcrypt hash | `config.yaml` `security.basic.password-bcrypt-base64`. Gatus expands `${...}` in config.yaml already (see the `env_file` comment on line 17 of docker-compose.yml). |

**Pre-existing, consumed by the telemetry containers — keep the `LL_*` names:**

`LL_INGEST_TOKEN`, `LL_DB_PASS`, `LL_DB_ROOT_PASS`, `LL_INGEST_RATE_MAX`, `LL_INGEST_RATE_WINDOW_SEC`.

These are read by `main.py:17-30` via `os.environ[...]` with those exact literal keys. Renaming them to `TELEMETRY_*` would require editing `main.py`, diverging from upstream LL-Telemetry, and re-testing the whole ingest path — a real cost for a cosmetic gain. **Leave them.** They are also arguably already conformant: `LL` is the feature prefix.

**Deleted:** `LL_DASH_USER`, `LL_DASH_HASH` — they only fed Caddy's `basic_auth`, and Caddy is gone. Their role is taken by `GATUS_BASIC_BCRYPT_B64`. Note the `.env.example` warning about doubling `$` in the Caddy hash no longer applies; Gatus's format is base64-of-bcrypt, which contains no `$`.

---

## 7. Ordered implementation steps

Ordered so each step is independently verifiable, and so the risky auth flip lands last.

1. **Fix the two upstream bugs in LL-Telemetry** (`console.html` line 590 `fmt(` → `fmtTime(`; mount `02-apikeys.sql` in both `telemetry/docker-compose*.yml`). Do this in the LL-Telemetry repo first so the Gatus copy is a copy of something correct.
2. **Copy the console.** `LL-Telemetry\telemetry\dashboard\index.html` → `Gatus\web\telemetry\console.html`. No edits.
3. **Edit `web/static.go`** — add the `//go:embed telemetry/console.html` var.
4. **Write `api/telemetry.go`** (§2 + §4).
5. **Edit `api/api.go`** — the two protected route lines. *Skip the SPA line for now.* Build: `docker compose build gatus`.
6. **Compose additions** (§5), plus `LL_*` into Gatus's `.env`. `docker compose up -d lltel-db lltel-api`. If reusing an external volume, apply the missing migration by hand:
   ```bash
   docker exec -i lltel-db mariadb -ulltel -p"$LL_DB_PASS" lltelemetry \
     < ../LL-Telemetry/telemetry/db/02-apikeys.sql
   ```
7. **Verify the Go half with no frontend at all.** `docker compose up -d gatus`, then browse to `http://localhost:8080/api/v1/telemetry/console` — the full console should render and work, including Keys. **This is the key checkpoint: everything except the header button is done and provable before any npm runs.**
8. **Vue: view + route + App.vue button** (§4).
9. **Add `app.Get("/telemetry", SinglePageApplication(cfg.UI))`** to `api/api.go`.
10. **Container npm build**, commit the regenerated `web/static/`.
11. **Flip auth on last.** Add the `security:` block to `config.yaml` + `GATUS_BASIC_BCRYPT_B64` to `.env`. Restart. Re-verify: the dashboard, `/jira`, every wallboard, the phone/UniFi collectors, and the telemetry console. **Do this with the wallboards physically visible** — see §8.
12. **Doc**: `docs/telemetry-monitor.md`, mirroring the existing `docs/jira-monitor.md`.

---

## 8. What could break existing functionality

Ordered by likelihood × blast radius.

**1. Enabling `security:` breaks every unattended consumer. — HIGH, this is the real risk.**
Today `cfg.Security == nil`, so `/api/v1/endpoints/statuses`, `/api/v1/suites/statuses`, `/api/v1/endpoints/:key/check` and the `/api/v1/live` SSE stream (lines 176-185) are **completely open**. The moment `security.basic` is set, all four demand credentials. Casualties:
- **TV wallboards / kiosk browsers.** Per MEMORY, this deploy drives always-on status screens. A kiosk with no stored credential will 401 and show an empty page or a native auth prompt. `/api/v1/live` is an `EventSource`, and **`EventSource` cannot carry a Basic credential** unless the browser has already cached it for the origin — a fresh kiosk boot may or may not, depending on the browser.
- Any bookmark, script, or `curl` hitting those four routes.
- *Not* affected: the phone and UniFi collectors. They POST to `/v1/phones/*` and `/v1/unifi/*`, which are on the **unprotected** router (lines 100-122) and carry their own `PHONES_PUSH_TOKEN`. Verified.
- *Not* affected: `/api/v1/config`, `/health`, badges, the Jira routes — all unprotected (lines 87-132).
- **Mitigation:** flip it in step 11, separately, with the wallboards in view and a rollback (delete the `security:` block, restart) ready. If kiosks turn out to be incompatible with Basic, the fallback is OIDC — but that is a much larger change, and it is the point at which "minimal" stops being the right approach and the Clean architect's answer should win.

**2. `web/static/` regeneration churn. — MEDIUM.**
`git status` already shows `web/static/js/app.js`, `chunk-vendors.js` and `css/app.css` modified. A container `npm ci` may resolve caret-ranged deps (`lucide-vue-next: ^0.539.0`) to newer versions than whatever produced the committed bundle, so the rebuild can change unrelated UI. Mitigate with `npm ci` (respects `package-lock.json`) rather than `npm install`, and diff the rendered dashboard before committing.

**3. Volume identity change silently orphans telemetry data. — MEDIUM.**
`gatus_lltel-data` ≠ `ll-telemetry_lltel-data` (§5 gotcha 1). Symptom is not an error — it is a console that renders perfectly with zero runs in it. Decide external-vs-fresh **before** step 6.

**4. Port 4555 collision / ingest regression. — LOW-MEDIUM.**
Field scripts currently post to `https://telemetry.longlewis.local/api/v1/runs` (Caddy, TLS, port 443). Pointing them at `http://<host>:4555` is **plaintext, and the ingest token travels in the `Authorization` header.** On a trusted LAN this matches what the local compose already did, but it is a downgrade from the production posture. If the production telemetry host stays up on its own compose, leave field scripts alone entirely and treat the Gatus-local stack as console-plus-dev-data only — which is the safer reading of DECIDED #1.

**5. Route-order regression in `api.go`. — LOW.**
`/v1/telemetry/console` must be registered before `/v1/telemetry/*`. If reordered during review, the console 404s (it is not in the proxy allowlist). Cheap guard: a test in `api/api_test.go` asserting `GET /api/v1/telemetry/console` returns `text/html`.

**6. Startup panic from the `bytes.Count` assertion. — LOW, and intentional.**
If someone re-syncs `console.html` from an upstream that reformatted line 318, **Gatus will not start.** That is the designed behavior (§4) and it is strictly better than a silently mis-pointed console — but it means a telemetry-file update can take the whole status page down. Worth a comment in `web/telemetry/console.html`'s vicinity and a note in the docs.

**7. `fiberfs` shadowing. — LOW / verified non-issue.**
`app.Use("/", fiberfs.New(...))` at line 158 runs before the protected router and matches all paths. It 404s→`c.Next()` for unknown paths, which is exactly how `/api/v1/endpoints/statuses` already works. `/api/v1/telemetry/*` behaves identically. `Browse: true` does not change this.

**8. Compress middleware. — verified non-issue.** No SSE on the telemetry path (§0). `api.go:66-73` unchanged.

**9. `proxy.DoTimeout` and `Immutable: true`. — LOW.**
Fiber runs with `Immutable: true` (line 56), so `c.Params("*")` returns a safe copy — no zero-allocation aliasing hazard when it is retained across the proxy call. Confirmed safe, but it is the kind of thing that breaks subtly if `Immutable` is ever turned off.

---

## PROS

- **One new Go file, zero new Go dependencies, zero `go.mod` churn.** `middleware/proxy` and `basicauth` both already ship inside pinned deps.
- **Zero new middleware plumbing.** Serving the console from `/api/v1/telemetry/console` reuses the existing positional protection exactly as-is; no second security group, no `security/config.go` changes.
- **The console stays diffable against upstream** — one bug fix, no fork of the base URL — and the rewrite is self-checking rather than silent.
- **`web/telemetry/` is structurally npm-proof and structurally un-leakable**, rather than relying on a convention someone could undo.
- **Net −1 service.** Caddy is deleted; two are added.
- **The Go half is fully verifiable at step 7, before any npm runs** — which matters a lot given there is no node on this machine.
- Two real upstream bugs (`fmt(`, the unmounted `02-apikeys.sql`) get fixed in LL-Telemetry, not papered over in Gatus.

## CONS

- **Enabling `security:` is a genuinely breaking change with a blast radius far outside this feature** (wallboards, kiosks, the `/api/v1/live` SSE). This work is 80% done and 100% risky at step 11. It is the price of requirement 3 under *any* architecture, but a minimal design does nothing to soften it — no per-route opt-in, no allowlist for kiosk IPs.
- **`build: ../LL-Telemetry/telemetry/api` reaches outside the repo.** Fragile for anyone without the sibling checkout, and it means Gatus's compose can't stand alone.
- The proxy allowlist is **hand-maintained against the console's call sites**. If LL-Telemetry's console gains a panel calling a new endpoint, it 404s here until someone edits a regex in Go. That is deliberate (defense in depth) but it is real coupling between two repos.
- **The iframe is an iframe.** No shared theme (the console is hard-committed to `color-scheme: dark` while Gatus has a light/dark toggle), no shared tooltips, no deep-linking into a specific run, and a fixed `calc(100vh - 12rem)` height that will be wrong on some viewports. DECIDED #2 accepts this; it is still the main aesthetic cost.
- The startup panic on a malformed console file couples "telemetry file updated" to "status page won't boot."

## RECOMMENDATION

**Yes** — with one condition.

The Go and compose halves are genuinely small and low-risk, and step 7 makes them provable in isolation before any frontend build. The `/api/v1/telemetry/console` placement is the single highest-leverage decision here: it collapses "how do I protect a non-API HTML route" from a middleware-refactor into a route-ordering line, and it is what keeps this design at one new Go file.

The condition: **treat step 11 (enabling `security:`) as its own change, with its own verification pass and rollback plan.** It is not really part of the telemetry feature — it is a site-wide auth rollout that telemetry happens to require. Bundling it into one commit means a wallboard outage looks like "the telemetry thing broke the dashboard," and it will be rolled back wholesale. Ship steps 1-10 first with security still off (telemetry is then no less protected than `/api/v1/endpoints/statuses` already is, which is the honest status quo), then flip auth deliberately.

If wallboards turn out to be incompatible with Basic auth, stop and escalate — the minimal design has no answer there, and that is the point where a Clean/Pragmatic approach with a per-route auth policy earns its extra complexity.
