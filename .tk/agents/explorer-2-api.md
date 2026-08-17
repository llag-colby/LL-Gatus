# Explorer 2 — Backend HTTP/API Architecture (for LL-Telemetry integration)

Repo: `C:\Users\colby.west\Desktop\Projects\Gatus` (fork of TwiN/gatus v5, Go + Fiber v2, Vue 3 SPA)

---

## 1. `api/api.go` — the entire router

**File:** `C:\Users\colby.west\Desktop\Projects\Gatus\api\api.go` (188 lines)

### Structure

| Lines | What |
|---|---|
| 26–46 | `type API struct { router *fiber.App }`, `New(cfg)`, `Router()` |
| 48–57 | `fiber.New(...)` config |
| 58–63 | CORS, dev only |
| 64–73 | `recover` + `compress` middleware (SSE excluded) |
| 74–80 | `/metrics` (Prometheus), conditional on `cfg.Metrics` |
| 82 | `apiRouter := app.Group("/api")` |
| 86–132 | **UNPROTECTED** routes |
| 133–138 | SPA routes |
| 139–144 | `/health` |
| 145–146 | `/css/custom.css` |
| 147–162 | redirect + embedded static filesystem fallback |
| 163–175 | `RegisterHandlers` + `ApplySecurityMiddleware` |
| 176–186 | **PROTECTED** routes |

### Fiber app construction (api.go:49–63) — verbatim

```go
app := fiber.New(fiber.Config{
    ErrorHandler: func(c *fiber.Ctx, err error) error {
        logr.Errorf("[api.ErrorHandler] %s", err.Error())
        return fiber.DefaultErrorHandler(c, err)
    },
    ReadBufferSize: cfg.Web.ReadBufferSize,
    Network:        fiber.NetworkTCP,
    Immutable:      true, // If not enabled, will cause issues due to fiber's zero allocation. See #1268 and https://docs.gofiber.io/#zero-allocation
})
if os.Getenv("ENVIRONMENT") == "dev" {
    app.Use(cors.New(cors.Config{
        AllowOrigins:     "http://localhost:8081",
        AllowCredentials: true,
    }))
}
```

> `Immutable: true` matters: any `c.Params()` / header string you keep beyond the handler is already copied, so no `utils.CopyString` needed.

### Compression middleware — SSE must be excluded (api.go:65–73) — verbatim

```go
app.Use(recover.New())
app.Use(compress.New(compress.Config{
    // Never compress the live SSE streams — they must stream unbuffered.
    Next: func(c *fiber.Ctx) bool {
        p := c.Path()
        return strings.HasPrefix(p, "/api/v1/live") || p == "/api/v1/jira/live" ||
            (strings.HasPrefix(p, "/api/v1/jira/board/") && strings.HasSuffix(p, "/live"))
    },
}))
```

**LL-Telemetry note:** if the new routes stream SSE (or proxy an SSE stream from FastAPI), you MUST add the prefix to this `Next` func or the stream will buffer forever.

### Full route registration block (api.go:82–186) — verbatim

```go
	// Define main router
	apiRouter := app.Group("/api")
	////////////////////////
	// UNPROTECTED ROUTES //
	////////////////////////
	unprotectedAPIRouter := apiRouter.Group("/")
	unprotectedAPIRouter.Get("/v1/config", ConfigHandler{securityConfig: cfg.Security, config: cfg}.GetConfig)
	unprotectedAPIRouter.Get("/v1/version", VersionHandler)
	unprotectedAPIRouter.Get("/v1/time", TimeHandler)
	unprotectedAPIRouter.Get("/v1/endpoints/:key/health/badge.svg", HealthBadge)
	unprotectedAPIRouter.Get("/v1/endpoints/:key/health/badge.shields", HealthBadgeShields)
	unprotectedAPIRouter.Get("/v1/endpoints/:key/uptimes/:duration", UptimeRaw)
	unprotectedAPIRouter.Get("/v1/endpoints/:key/uptimes/:duration/badge.svg", UptimeBadge)
	unprotectedAPIRouter.Get("/v1/endpoints/:key/response-times/:duration", ResponseTimeRaw)
	unprotectedAPIRouter.Get("/v1/endpoints/:key/response-times/:duration/badge.svg", ResponseTimeBadge(cfg))
	unprotectedAPIRouter.Get("/v1/endpoints/:key/response-times/:duration/chart.svg", ResponseTimeChart)
	unprotectedAPIRouter.Get("/v1/endpoints/:key/response-times/:duration/history", ResponseTimeHistory)
	// This endpoint requires authz with bearer token, so technically it is protected
	unprotectedAPIRouter.Post("/v1/endpoints/:key/external", CreateExternalEndpointResult(cfg))
	// Phones inventory side-channel: collector POSTs the rich per-phone table
	// (token-auth'd like /external); the phones drill-in GETs it.
	unprotectedAPIRouter.Post("/v1/phones/:key", SetPhonesInventory(cfg))
	// Force-sweep: static route registered BEFORE the :key GET so it isn't
	// swallowed by :key="sweep-pending". Collector claims pending sweeps here.
	unprotectedAPIRouter.Get("/v1/phones/sweep-pending", ClaimPhonesSweeps)
	unprotectedAPIRouter.Post("/v1/phones/:key/sweep", RequestPhonesSweep)
	unprotectedAPIRouter.Get("/v1/phones/:key", GetPhonesInventory)
	unprotectedAPIRouter.Get("/v1/phones/:key/exclusions", GetPhonesExclusions)
	unprotectedAPIRouter.Post("/v1/phones/:key/exclusions", SetPhonesExclusion)
	unprotectedAPIRouter.Get("/v1/phones/:key/settings", GetPhonesSettings)
	unprotectedAPIRouter.Post("/v1/phones/:key/settings", SetPhonesSettings)
	// Pause monitoring, per endpoint key. Static route first so it isn't
	// swallowed by :key (same reason as /v1/phones/sweep-pending above).
	unprotectedAPIRouter.Get("/v1/monitoring", GetMonitoring)
	unprotectedAPIRouter.Get("/v1/monitoring/:key", GetMonitoringForKey)
	unprotectedAPIRouter.Post("/v1/monitoring/:key", SetMonitoringForKey)
	// UniFi side-channel: the collector POSTs per-site firewall/wireless
	// snapshots; the dashboard GETs them all at once for the card rows.
	// Static route first so it isn't swallowed by :key.
	unprotectedAPIRouter.Get("/v1/unifi", GetUniFiSnapshots)
	unprotectedAPIRouter.Post("/v1/unifi/:key", SetUniFiSnapshot(cfg))
	unprotectedAPIRouter.Get("/v1/unifi/:key", GetUniFiSnapshot)
	// Jira service-desk metrics, refreshed by the background jira poller.
	unprotectedAPIRouter.Get("/v1/jira/metrics", GetJiraMetrics)
	// Jira ticket drill-down: fetches one issue's detail on demand.
	unprotectedAPIRouter.Get("/v1/jira/issue/:key", GetJiraIssue)
	// Jira live stream (SSE): pushes a fresh snapshot on every poll.
	unprotectedAPIRouter.Get("/v1/jira/live", JiraLive)
	// Jira Kanban: the agile boards themselves (columns, WIP limits, cards).
	unprotectedAPIRouter.Get("/v1/jira/boards", GetJiraBoards)
	unprotectedAPIRouter.Get("/v1/jira/board/:id", GetJiraBoard)
	unprotectedAPIRouter.Get("/v1/jira/board/:id/live", JiraBoardLive)
	// SPA
	app.Get("/", SinglePageApplication(cfg.UI))
	app.Get("/endpoints/:key", SinglePageApplication(cfg.UI))
	app.Get("/suites/:key", SinglePageApplication(cfg.UI))
	app.Get("/sites/:name", SinglePageApplication(cfg.UI))
	app.Get("/jira", SinglePageApplication(cfg.UI))
	// Health endpoint
	healthHandler := health.Handler().WithJSON(true)
	app.Get("/health", func(c *fiber.Ctx) error {
		statusCode, body := healthHandler.GetResponseStatusCodeAndBody()
		return c.Status(statusCode).Send(body)
	})
	// Custom CSS
	app.Get("/css/custom.css", CustomCSSHandler{customCSS: cfg.UI.CustomCSS}.GetCustomCSS)
	// Everything else falls back on static content
	app.Use(redirect.New(redirect.Config{
		Rules: map[string]string{
			"/index.html": "/",
		},
		StatusCode: 301,
	}))
	staticFileSystem, err := fs.Sub(static.FileSystem, static.RootPath)
	if err != nil {
		panic(err)
	}
	app.Use("/", fiberfs.New(fiberfs.Config{
		Root:   http.FS(staticFileSystem),
		Index:  "index.html",
		Browse: true,
	}))
	//////////////////////
	// PROTECTED ROUTES //
	//////////////////////
	// ORDER IS IMPORTANT: all routes applied AFTER the security middleware will require authn
	protectedAPIRouter := apiRouter.Group("/")
	if cfg.Security != nil {
		if err := cfg.Security.RegisterHandlers(app); err != nil {
			panic(err)
		}
		if err := cfg.Security.ApplySecurityMiddleware(protectedAPIRouter); err != nil {
			panic(err)
		}
	}
	protectedAPIRouter.Get("/v1/endpoints/statuses", EndpointStatuses(cfg))
	protectedAPIRouter.Get("/v1/endpoints/:key/statuses", EndpointStatus(cfg))
	// Force ping: runs one out-of-band check now instead of waiting out the
	// endpoint's interval (the "Force ping" button on the endpoint drill-in).
	protectedAPIRouter.Post("/v1/endpoints/:key/check", ForceEndpointCheck(cfg))
	protectedAPIRouter.Get("/v1/suites/statuses", SuiteStatuses(cfg))
	protectedAPIRouter.Get("/v1/suites/:key/statuses", SuiteStatus(cfg))
	// Live status stream (SSE) — a single broadcaster pushes the same snapshot
	// to every connected client so all screens stay in sync without refreshing.
	protectedAPIRouter.Get("/v1/live", newSSEHub().Handler)
	return app
```

### Critical ordering rules (these are the load-bearing facts)

1. **Two sibling groups on the same `/api` prefix.** `unprotectedAPIRouter` (line 86) and `protectedAPIRouter` (line 167) are BOTH `apiRouter.Group("/")`. The only thing making one "protected" is that `ApplySecurityMiddleware` is called on `protectedAPIRouter` *before* its routes are registered. Fiber middleware only applies to routes registered after `.Use()` on that group.
   → **A new route joins the unprotected set simply by being registered on `unprotectedAPIRouter` at lines 86–132.**
2. **Static-before-param.** Repeated twice in comments (lines 103–105, 112–113, 119). Fiber matches in registration order, so `GET /v1/unifi` must precede `GET /v1/unifi/:key`, and `GET /v1/phones/sweep-pending` must precede `GET /v1/phones/:key`. Same discipline applies to any `/v1/telemetry` + `/v1/telemetry/:key` pair.
3. **The static filesystem `app.Use("/", fiberfs...)` at line 158 is the catch-all** and is registered BEFORE the protected routes. It is `app.Use` (prefix-mounted middleware), not a route, so it only fires when no route matched; `/api/...` routes registered after it still work because Fiber tries route handlers in the chain and the fs middleware calls `Next()` on 404 within its own FS. **But**: any *non-`/api`* path you add after line 158 is shadowed risk — add new top-level page routes with the other `app.Get` SPA routes at lines 134–138.
4. There is **no explicit SPA fallback**. Unknown paths hit `fiberfs` with `Browse: true`, which returns the embedded static dir listing / 404 — NOT `index.html`. Deep links only work for the exact paths enumerated at lines 134–138. **So `/telemetry` needs its own `app.Get("/telemetry", ...)` line.**
5. CORS is **dev-only** (`ENVIRONMENT=dev`), fixed to `http://localhost:8081` with credentials — that's the `vue-cli-service serve` dev server.

---

## 2. Every file in `api/`

| File | Owns | Routes |
|---|---|---|
| `api.go` | Router construction, middleware, all route wiring | (all) |
| `badge.go` | SVG/shields badge generation | `/v1/endpoints/:key/health/badge.svg`, `.../badge.shields`, `.../uptimes/:duration/badge.svg`, `.../response-times/:duration/badge.svg` |
| `cache.go` | shared `gocache` instance for handlers | — |
| `chart.go` | `ResponseTimeChart`, `ResponseTimeHistory` | `.../chart.svg`, `.../history` |
| `config.go` | `ConfigHandler.GetConfig` — oidc/authenticated/announcements JSON | `/v1/config` |
| `custom_css.go` | `CustomCSSHandler.GetCustomCSS` | `/css/custom.css` |
| `endpoint_check.go` | `ForceEndpointCheck` + `claimForceCheck` debounce | `POST /v1/endpoints/:key/check` (protected) |
| `endpoint_status.go` | `EndpointStatuses`, `EndpointStatus`, `getEndpointStatusesFromRemoteInstances` | `/v1/endpoints/statuses`, `/v1/endpoints/:key/statuses` (protected) |
| `external_endpoint.go` | `CreateExternalEndpointResult` — bearer-token push ingest | `POST /v1/endpoints/:key/external` |
| **`jira.go`** | 6 handlers incl. 2 SSE | `/v1/jira/metrics`, `/v1/jira/issue/:key`, `/v1/jira/live`, `/v1/jira/boards`, `/v1/jira/board/:id`, `/v1/jira/board/:id/live` |
| `monitoring.go` | pause/resume per endpoint key | `/v1/monitoring`, `/v1/monitoring/:key` GET+POST |
| `phones_inventory.go` | phone snapshot store + exclusions (persisted to `/data`) | `POST /v1/phones/:key`, `GET /v1/phones/:key`, `/v1/phones/:key/exclusions` GET+POST |
| `phones_settings.go` | per-key thresholds, persisted JSON file | `/v1/phones/:key/settings` GET+POST |
| `phones_sweep.go` | force-sweep request/claim queue | `POST /v1/phones/:key/sweep`, `GET /v1/phones/sweep-pending` |
| `raw.go` | `UptimeRaw`, `ResponseTimeRaw` | `.../uptimes/:duration`, `.../response-times/:duration` |
| `spa.go` | `SinglePageApplication(uiConfig)` — templates index.html | `/`, `/endpoints/:key`, `/suites/:key`, `/sites/:name`, `/jira` |
| `sse.go` | `sseHub` broadcaster for live endpoint statuses | `/v1/live` (protected) |
| `suite_status.go` | `SuiteStatuses`, `SuiteStatus` | `/v1/suites/statuses`, `/v1/suites/:key/statuses` (protected) |
| **`unifi_inventory.go`** | in-memory UniFi snapshot store | `GET /v1/unifi`, `POST /v1/unifi/:key`, `GET /v1/unifi/:key` |
| `util.go` | `extractPageAndPageSizeFromRequest` | — |
| `version.go` | `VersionHandler`, `TimeHandler` | `/v1/version`, `/v1/time` |

Tests exist for: `api_test.go`, `badge_test.go`, `chart_test.go`, `config_test.go`, `endpoint_status_test.go`, `external_endpoint_test.go`, `raw_test.go`, `spa_test.go`, `suite_status_test.go`, `util_test.go`. **No tests for `jira.go`, `unifi_inventory.go`, `monitoring.go`, `phones_*.go`** — the bolt-on features are untested, which is the established (if unfortunate) house norm.

### House style for bolt-on features — the two canonical examples

**`api/unifi_inventory.go`** (untracked/new) is the cleanest template. Its shape:

- Long doc comment at the top of the file explaining WHY the side-channel exists and what the payload kinds are (lines 15–27).
- Package-level `sync.RWMutex` + `map[string]storedX` store (lines 28–31). **Ephemeral, not persisted** — "A key that stops reporting simply goes stale, which the UI shows rather than hiding."
- A `storedX` struct using `json.RawMessage` for the opaque detail blob so the Go side never has to model the collector's schema (lines 33–40).
- Writer handler is a **closure factory** `func SetUniFiSnapshot(cfg *config.Config) fiber.Handler` because it needs `cfg` for token lookup; reader handlers are plain `func(c *fiber.Ctx) error`.
- Auth: bearer token compared against the matching **external endpoint's** token (lines 47–58).
- `logr.Infof("[api.SetUniFiSnapshot] ...")` — the `[package.Func]` log prefix convention is used everywhere.
- Returns `c.Status(200).JSON(...)` / `c.Status(4xx).SendString("...")`.

Verbatim token-auth block (`unifi_inventory.go:45–58`) — reuse this if the telemetry server pushes to Gatus:

```go
key := c.Params("key")
externalEndpoint := cfg.GetExternalEndpointByKey(key)
if externalEndpoint == nil {
    return c.Status(404).SendString("not found")
}
authorizationHeader := string(c.Request().Header.Peek("Authorization"))
if !strings.HasPrefix(authorizationHeader, "Bearer ") {
    return c.Status(401).SendString("invalid Authorization header")
}
token := strings.TrimSpace(strings.TrimPrefix(authorizationHeader, "Bearer "))
if len(token) == 0 || externalEndpoint.Token != token {
    return c.Status(401).SendString("invalid token")
}
```

**`api/jira.go`** is the template for *proxying/reading an external service*. Its shape:

- Handlers are **thin**: all business logic lives in a sibling top-level package (`jira/`), the api file only does param parsing, `context.WithTimeout`, and status-code selection.
- **Per-handler timeouts** via `context.WithTimeout(context.Background(), N*time.Second)` — 20s for one issue, 25s for board list, 60s for a full board (`jira.go:26, 38, 51`).
- **"Always 200 with an `ok`/`error` field in the payload"** for polled/cached data (`GetJiraMetrics`, `GetJiraBoard`) so the UI renders a degraded state instead of an error toast; `502` only for a genuinely synchronous fetch failure (`GetJiraIssue`, line 30).
- Errors as `c.Status(502).JSON(fiber.Map{"error": err.Error()})`.
- Param validation: `strconv.Atoi` → `400` with `fiber.Map{"error": "..."}`.

**This is the pattern LL-Telemetry should follow: a new top-level `telemetry/` package holding the HTTP client + poller/cache, and a thin `api/telemetry.go` with the fiber handlers.**

### SSE handler pattern (`api/jira.go:106–143`) — verbatim, copy this if streaming

```go
func JiraLive(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	ch := jira.Subscribe()
	initial, _ := json.Marshal(jira.GetSnapshot())
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer jira.Unsubscribe(ch)
		writeEvent := func(payload []byte) bool {
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return false
			}
			return w.Flush() == nil
		}
		if initial != nil && !writeEvent(initial) {
			return
		}
		heartbeat := time.NewTicker(20 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case msg, ok := <-ch:
				if !ok || !writeEvent(msg) {
					return
				}
			case <-heartbeat.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
				if w.Flush() != nil {
					return
				}
			}
		}
	})
	return nil
}
```

---

## 3. How the SPA is served

### The embed — the ONLY `//go:embed` in the repo

**File:** `C:\Users\colby.west\Desktop\Projects\Gatus\web\static.go` (13 lines) — verbatim, whole file:

```go
package static

import "embed"

var (
	//go:embed static
	FileSystem embed.FS
)

const (
	RootPath  = "static"
	IndexPath = RootPath + "/index.html"
)
```

- Package name is `static`, imported in api.go as `static "github.com/TwiN/gatus/v5/web"` (api.go:12).
- Embeds the whole `web/static/` directory: `index.html`, `js/app.js`, `js/chunk-vendors.js`, `css/app.css`, favicons, `manifest.json`.
- `web/app/` is the Vue source; the build output is committed into `web/static/` (see git status: `web/static/js/app.js` etc. are tracked and modified).

Confirmed by grep: `//go:embed` appears exactly once in the whole repo (`web/static.go:6`). **There is no second embedded asset bundle — adding one for an LL-Telemetry dashboard means a new `//go:embed` directive, either extending `web/static.go` or a new package.**

### The template handler

**File:** `C:\Users\colby.west\Desktop\Projects\Gatus\api\spa.go` (41 lines) — verbatim, whole file:

```go
package api

import (
	_ "embed"
	"html/template"

	"github.com/TwiN/gatus/v5/config/ui"
	static "github.com/TwiN/gatus/v5/web"
	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

func SinglePageApplication(uiConfig *ui.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		vd := ui.ViewData{UI: uiConfig}
		{
			themeFromCookie := string(c.Request().Header.Cookie("theme"))
			if len(themeFromCookie) > 0 {
				if themeFromCookie == "dark" {
					vd.Theme = "dark"
				}
			} else if uiConfig.IsDarkMode() { // Since there's no theme cookie, we'll rely on ui.DarkMode
				vd.Theme = "dark"
			}
		}
		t, err := template.ParseFS(static.FileSystem, static.IndexPath)
		if err != nil {
			// This should never happen, because ui.ValidateAndSetDefaults validates that the template works.
			logr.Errorf("[api.SinglePageApplication] Failed to parse template. This should never happen, because the template is validated on start. Error: %s", err.Error())
			return c.Status(500).SendString("Failed to parse template. This should never happen, because the template is validated on start.")
		}
		c.Set("Content-Type", "text/html")
		err = t.Execute(c, vd)
		if err != nil {
			// This should never happen, because ui.ValidateAndSetDefaults validates that the template works.
			logr.Errorf("[api.SinglePageApplication] Failed to execute template. This should never happen, because the template is validated on start. Error: %s", err.Error())
			return c.Status(500).SendString("Failed to parse template. This should never happen, because the template is validated on start.")
		}
		return c.SendStatus(200)
	}
}
```

Note: `template.ParseFS` runs **on every request** (no caching). `t.Execute(c, vd)` writes directly to the fiber `*Ctx` (which implements `io.Writer`).

### The `window.config` template

**File:** `C:\Users\colby.west\Desktop\Projects\Gatus\web\static\index.html` — line 1 (minified single line). Verbatim:

```html
<!doctype html><html lang="en" class="{{ .Theme }}"><head><meta charset="utf-8"/><script>window.config = {logo: "{{ .UI.Logo }}", header: "{{ .UI.Header }}", dashboardHeading: "{{ .UI.DashboardHeading }}", dashboardSubheading: "{{ .UI.DashboardSubheading }}", link: "{{ .UI.Link }}", buttons: [], maximumNumberOfResults: "{{ .UI.MaximumNumberOfResults }}", defaultSortBy: "{{ .UI.DefaultSortBy }}", defaultFilterBy: "{{ .UI.DefaultFilterBy }}", loginSubtitle: "{{ .UI.LoginSubtitle }}"};{{- range .UI.Buttons}}window.config.buttons.push({name:"{{ .Name }}",link:"{{ .Link }}"});{{end}}
```

Rest of the placeholders on line 11: `<title>{{ .UI.Title }}</title>`, `<meta name="description" content="{{ .UI.Description }}"/>`, `<meta name="apple-mobile-web-app-title" content="{{ .UI.Title }}"/>`, `<meta name="application-name" content="{{ .UI.Title }}"/>`.

**Template data model** — `C:\Users\colby.west\Desktop\Projects\Gatus\config\ui\ui.go:175–178`:

```go
type ViewData struct {
	UI    *Config
	Theme string
}
```

`ui.Config` fields (ui.go:38–57): `Title, Description, DashboardHeading, DashboardSubheading, Header, Logo, Link, Favicon, Buttons []Button, CustomCSS, DarkMode *bool, DefaultSortBy, DefaultFilterBy, LoginSubtitle, MaximumNumberOfResults`.

**To pass a telemetry base URL to the SPA**, the path of least resistance is adding a field to `ui.Config` and a `{{ .UI.X }}` slot in this `window.config` object. Note `config/ui/ui.go:172` validates the template at startup (`t.Execute(&buffer, ViewData{UI: cfg, Theme: "dark"})`), so a broken placeholder fails fast on boot.

### Vue router

**File:** `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\router\index.js` — routes: `/`, `/endpoints/:key`, `/sites/:name`, `/suites/:key`, `/jira`. Every one of these has a matching `app.Get(...)` in api.go:134–138. **The two lists must be kept in sync manually** — that is the coupling to remember.

---

## 4. `security/` — how protection works

**Dir:** `C:\Users\colby.west\Desktop\Projects\Gatus\security\`

| File | Purpose |
|---|---|
| `config.go` (111 lines) | `Config{Basic, OIDC, gate}`, `ValidateAndSetDefaults`, `RegisterHandlers`, `ApplySecurityMiddleware`, `IsAuthenticated` |
| `basic.go` (17 lines) | `BasicConfig{Username, PasswordBcryptHashBase64Encoded}` |
| `oidc.go` (~160 lines) | OIDC login + callback, session issuance |
| `sessions.go` (5 lines) | in-memory session cache |

### `ApplySecurityMiddleware` (`security/config.go:46–93`) — verbatim

```go
// ApplySecurityMiddleware applies an authentication middleware to the router passed.
// The router passed should be a sub-router in charge of handlers that require authentication.
func (c *Config) ApplySecurityMiddleware(router fiber.Router) error {
	if c.OIDC != nil {
		// We're going to use g8 for session handling
		clientProvider := g8.NewClientProvider(func(token string) *g8.Client {
			if _, exists := sessions.Get(token); exists {
				return g8.NewClient(token)
			}
			return nil
		})
		customTokenExtractorFunc := func(request *http.Request) string {
			sessionCookie, err := request.Cookie(cookieNameSession)
			if err != nil {
				return ""
			}
			return sessionCookie.Value
		}
		// TODO: g8: Add a way to update cookie after? would need the writer
		authorizationService := g8.NewAuthorizationService().WithClientProvider(clientProvider)
		c.gate = g8.New().WithAuthorizationService(authorizationService).WithCustomTokenExtractor(customTokenExtractorFunc)
		router.Use(adaptor.HTTPMiddleware(c.gate.Protect))
	} else if c.Basic != nil {
		var decodedBcryptHash []byte
		if len(c.Basic.PasswordBcryptHashBase64Encoded) > 0 {
			var err error
			decodedBcryptHash, err = base64.URLEncoding.DecodeString(c.Basic.PasswordBcryptHashBase64Encoded)
			if err != nil {
				return err
			}
		}
		router.Use(basicauth.New(basicauth.Config{
			Authorizer: func(username, password string) bool {
				if len(c.Basic.PasswordBcryptHashBase64Encoded) > 0 {
					if username != c.Basic.Username || bcrypt.CompareHashAndPassword(decodedBcryptHash, []byte(password)) != nil {
						return false
					}
				}
				return true
			},
			Unauthorized: func(ctx *fiber.Ctx) error {
				ctx.Set("WWW-Authenticate", "Basic")
				return ctx.Status(401).SendString("Unauthorized")
			},
		}))
	}
	return nil
}
```

`RegisterHandlers` (config.go:35–44) adds `/oidc/login` and `/authorization-code/callback` to the **root app** (not the protected group) when OIDC is configured.

### Protected vs unprotected — the definitive list

**Protected** (only if `cfg.Security != nil` in `config.yaml`):
- `GET /api/v1/endpoints/statuses`
- `GET /api/v1/endpoints/:key/statuses`
- `POST /api/v1/endpoints/:key/check`
- `GET /api/v1/suites/statuses`
- `GET /api/v1/suites/:key/statuses`
- `GET /api/v1/live`

**Unprotected**: everything else — including all Jira, UniFi, phones, monitoring routes, the SPA, `/health`, `/metrics`, `/css/custom.css`, and all static assets. `api/monitoring.go:11–14` states the rationale explicitly: *"Unauthenticated, consistent with the rest of this API (internal LAN tool)."*

Note: **the SPA HTML itself is never protected** — only the data APIs are. Auth state reaches the frontend via `GET /api/v1/config`'s `authenticated` field (`api/config.go:17–29`).

### How a new route opts in / out

- **Opt out (default for the bolt-ons):** register on `unprotectedAPIRouter` within lines 86–132.
- **Opt in:** register on `protectedAPIRouter` after line 176.
- There is **no per-route auth decorator** — it is purely positional. The comment at api.go:166 is the whole contract: `// ORDER IS IMPORTANT: all routes applied AFTER the security middleware will require authn`.
- If the telemetry route needs machine-to-machine auth (a collector pushing), copy the external-endpoint bearer-token check instead of using the security middleware — that's what `unifi_inventory.go` and `phones_inventory.go` do.

---

## 5. Outbound HTTP

### Two distinct patterns coexist

**(A) Core Gatus — `client.GetHTTPClient(config)`**

`C:\Users\colby.west\Desktop\Projects\Gatus\client\client.go:46–54` — verbatim:

```go
// GetHTTPClient returns the shared HTTP client, or the client from the configuration passed
func GetHTTPClient(config *Config) *http.Client {
	if injectedHTTPClient != nil {
		return injectedHTTPClient
	}
	if config == nil {
		return defaultConfig.getHTTPClient()
	}
	return config.getHTTPClient()
}
```

Used by ~40 alerting providers (`client.GetHTTPClient(nil).Do(request)`) and `api/endpoint_status.go:59`.

**(B) The bolt-on features — a hand-rolled `*http.Client`**. `jira/` does NOT use the `client` package.

### The jira.go outbound-call pattern — THIS is the house style for a new integration

**File:** `C:\Users\colby.west\Desktop\Projects\Gatus\jira\jira.go:385–432` — verbatim:

```go
type client struct {
	cfg  config
	auth string
	http *http.Client
}

func newClient(cfg config) *client {
	return &client{
		cfg:  cfg,
		auth: "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.email+":"+cfg.token)),
		http: &http.Client{Timeout: 25 * time.Second},
	}
}

func (c *client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s -> %d: %s", method, path, resp.StatusCode, snippet(data))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return err
		}
	}
	return nil
}
```

Key details to replicate for LL-Telemetry:
- One generic `do(ctx, method, path, body, out)` helper; callers are one-liners: `c.do(ctx, http.MethodGet, "/rest/api/3/myself", nil, &out)` (jira.go:440).
- `http.NewRequestWithContext` — the per-handler `context.WithTimeout` in `api/jira.go` is what actually bounds each call; the client's own `Timeout: 25*time.Second` is a backstop.
- `io.LimitReader(resp.Body, 8<<20)` — 8 MiB response cap.
- Non-2xx becomes a rich error with a `snippet(data)` of the body (`jira.go:745`).
- **Config from env vars**, not config.yaml. `jira/jira.go:173–230` uses an `envOr(key, def)` helper and `loadConfig()` returning an unexported `config` struct with a `configured() bool` guard.
- **Background poller** started unconditionally from `main.go:56`: `jira.StartPoller()`, a no-op with a friendly log line if unconfigured (`jira.go:235–258`). It wraps each poll in `defer recover()` so a panic doesn't kill the ticker.
- **Pub/sub for SSE**: `Subscribe()/Unsubscribe()/broadcast()` at `jira/jira.go:128–147`, plus `GetSnapshot()/setSnapshot()` at 110–120 behind a mutex.

### TLS skip-verify — YES, supported, several precedents

| Location | Form |
|---|---|
| `client/config.go:51–52` | `Insecure bool \`yaml:"insecure,omitempty"\`` — *"determines whether to skip verifying the server's certificate chain and host name"* |
| `client/config.go:213–229` | `getHTTPClient()` builds `tls.Config{InsecureSkipVerify: c.Insecure}` into the `http.Transport` |
| `client/config.go:187, 218–219` | `HasTLSConfig()` + `configureTLS(tlsConfig, *c.TLS)` — **client certificate / custom CA support** |
| `client/client.go:193–194, 213–214, 418–419` | SMTP StartTLS, raw `tls.DialWithDialer`, websocket dialer — all honor `config.Insecure` |
| `alerting/provider/gitea/gitea.go:68` | `TLSClientConfig: &tls.Config{InsecureSkipVerify: true}` — inline, hardcoded |
| `alerting/provider/email/email.go:137` | `d.TLSConfig = &tls.Config{InsecureSkipVerify: true}` |

`client/config.go:213–229` verbatim (the reference implementation):

```go
// getHTTPClient return an HTTP client matching the Config's parameters.
func (c *Config) getHTTPClient() *http.Client {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: c.Insecure,
	}
	if c.HasTLSConfig() && c.TLS.isValid() == nil {
		tlsConfig = configureTLS(tlsConfig, *c.TLS)
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{
			Timeout: c.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 20,
				Proxy:               http.ProxyFromEnvironment,
				TLSClientConfig:     tlsConfig,
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if c.IgnoreRedirect {
					// Don't follow redirects
					return http.ErrUseLastResponse
				}
				// Follow redirects
				return nil
			},
		}
```

**Recommendation for the internal-CA telemetry server:** follow the jira.go hand-rolled style but give the client an explicit transport, driven by env vars in keeping with the jira config convention:

```go
tr := &http.Transport{
    TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.insecure},
}
// or, better, load the internal CA:
//   pool := x509.NewCertPool(); pool.AppendCertsFromPEM(pem)
//   tls.Config{RootCAs: pool}
http: &http.Client{Timeout: 25 * time.Second, Transport: tr}
```

`crypto/x509` is already imported in `client/client.go:7`, and `client/config.go`'s `configureTLS` shows the CA-pool approach if you'd rather not skip verification. Env var naming convention would be `TELEMETRY_BASE_URL`, `TELEMETRY_TOKEN`, `TELEMETRY_INSECURE`, `TELEMETRY_POLL_SECONDS` — mirroring `JIRA_*`.

---

## 6. Serving a standalone HTML page — NO precedent

Searched for `SendFile`, `c.Type("html")`, `text/html`, `//go:embed`. Results:

- `api/spa.go:32` — `c.Set("Content-Type", "text/html")` (the Vue SPA template, the only HTML-serving handler)
- `alerting/provider/sendgrid/sendgrid.go:170` — `Type: "text/html"` (email MIME, unrelated)
- `web/static.go:6` — the only `//go:embed`

**Conclusion: there is zero precedent for serving a standalone, non-SPA HTML file from a route.** Options, in order of fit with the codebase:

1. **New embed + new handler** (cleanest, closest to `spa.go`): a `//go:embed telemetry.html` in a small package (or extend `web/static.go`), plus a handler that does `c.Set("Content-Type", "text/html")` + `c.Send(bytes)`. Register it with the other page routes at api.go:134–138, i.e. **before** the `fiberfs` catch-all at line 158.
2. **Drop the file into `web/static/`** — it is already embedded wholesale, and the `fiberfs` mount at api.go:158 would serve `/telemetry.html` for free with no Go changes at all. Caveat: the Vue build pipeline overwrites `web/static/`, so a hand-placed file there is fragile; and the URL would carry the `.html` extension.
3. **Template it like the SPA** if it needs `window.config`-style server-injected values — `template.ParseFS(static.FileSystem, "static/telemetry.html")` mirroring spa.go:26–38.

---

## Key files to read (13)

1. `C:\Users\colby.west\Desktop\Projects\Gatus\api\api.go` — the entire router; where new routes go, and the ordering constraints
2. `C:\Users\colby.west\Desktop\Projects\Gatus\api\unifi_inventory.go` — newest bolt-on; the template for a new `api/*.go` (store + token auth + JSON handlers)
3. `C:\Users\colby.west\Desktop\Projects\Gatus\api\jira.go` — the template for thin handlers over an external service, incl. the SSE handler to copy
4. `C:\Users\colby.west\Desktop\Projects\Gatus\jira\jira.go` — the outbound HTTP client, env config, background poller, pub/sub. The blueprint for a `telemetry/` package
5. `C:\Users\colby.west\Desktop\Projects\Gatus\api\spa.go` — how HTML is templated and served
6. `C:\Users\colby.west\Desktop\Projects\Gatus\web\static.go` — the only `//go:embed`; must be touched to embed a new dashboard
7. `C:\Users\colby.west\Desktop\Projects\Gatus\web\static\index.html` — the `window.config` template
8. `C:\Users\colby.west\Desktop\Projects\Gatus\config\ui\ui.go` — `ui.Config` / `ViewData`; where to add a field the template can read (validated at startup, line 172)
9. `C:\Users\colby.west\Desktop\Projects\Gatus\security\config.go` — `ApplySecurityMiddleware`, protected/unprotected semantics
10. `C:\Users\colby.west\Desktop\Projects\Gatus\client\config.go` — `getHTTPClient`, `Insecure` / `InsecureSkipVerify`, `configureTLS` (internal CA)
11. `C:\Users\colby.west\Desktop\Projects\Gatus\api\external_endpoint.go` — the canonical bearer-token push-ingest handler
12. `C:\Users\colby.west\Desktop\Projects\Gatus\main.go` — `start()` at line 52; where `StartPoller()` for a telemetry poller would be wired
13. `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\router\index.js` — Vue routes that must stay in sync with api.go:134–138

Supporting: `controller/controller.go` (server timeouts — **15s Write timeout, note this for SSE/proxy**), `api/sse.go` (hub pattern), `api/monitoring.go` (simplest possible handler file).

---

## Gotchas for the LL-Telemetry work

1. **`controller/controller.go:22–24` sets `server.WriteTimeout = 15 * time.Second`.** The existing SSE handlers work anyway because fasthttp's `SetBodyStreamWriter` bypasses it — but a long-running *proxy* of a slow FastAPI response could hit it. Verify before assuming.
2. **Add any new streaming path to the `compress.Next` exclusion list** (api.go:68–72) or it will buffer.
3. **`/telemetry` needs an explicit `app.Get`** — there is no SPA fallback; unmatched paths fall into `fiberfs` with `Browse: true`.
4. **Register static segments before `:param` segments** (`/v1/telemetry/health` before `/v1/telemetry/:key`).
5. **New routes default to unprotected** by being placed in the 86–132 block; that matches every other bolt-on in this fork.
6. **Config via env vars**, not `config.yaml` — that's what jira/ and the collectors do. `docker-compose.yml` is where they get set.
7. `Immutable: true` on the fiber app means param strings are safe to retain.
