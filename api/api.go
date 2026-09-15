package api

import (
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/TwiN/gatus/v5/auth"
	"github.com/TwiN/gatus/v5/config"
	"github.com/TwiN/gatus/v5/config/ui"
	"github.com/TwiN/gatus/v5/config/web"
	static "github.com/TwiN/gatus/v5/web"
	"github.com/TwiN/health"
	"github.com/TwiN/logr"
	fiber "github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/cors"
	fiberfs "github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/redirect"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type API struct {
	router *fiber.App
}

func New(cfg *config.Config) *API {
	api := &API{}
	if cfg.Web == nil {
		logr.Warnf("[api.New] nil web config passed as parameter. This should only happen in tests. Using default web configuration")
		cfg.Web = web.GetDefaultConfig()
	}
	if cfg.UI == nil {
		logr.Warnf("[api.New] nil ui config passed as parameter. This should only happen in tests. Using default ui configuration")
		cfg.UI = ui.GetDefaultConfig()
	}
	api.router = api.createRouter(cfg)
	return api
}

func (a *API) Router() *fiber.App {
	return a.router
}

func (a *API) createRouter(cfg *config.Config) *fiber.App {
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
	// Middlewares
	app.Use(recover.New())
	app.Use(compress.New(compress.Config{
		// Never compress the live SSE streams — they must stream unbuffered.
		Next: func(c *fiber.Ctx) bool {
			p := c.Path()
			return strings.HasPrefix(p, "/api/v1/live") || p == "/api/v1/jira/live" ||
				(strings.HasPrefix(p, "/api/v1/jira/board/") && strings.HasSuffix(p, "/live"))
		},
	}))
	// The SPA bundle is built with filenameHashing disabled, so index.html,
	// app.js and app.css keep the same URL forever. With no revalidation header a
	// browser may serve a cached copy indefinitely, so a deploy lands and the user
	// still sees the previous build. The symptom is a UI that looks broken or
	// half-updated rather than obviously stale, which is a genuinely expensive
	// thing to debug. Registered here, above the SPA routes, because those return
	// without calling Next() and would otherwise never reach this.
	app.Use(func(c *fiber.Ctx) error {
		p := c.Path()
		if strings.HasPrefix(p, "/api/") {
			return c.Next()
		}
		switch p {
		case "/js/app.js", "/js/chunk-vendors.js", "/css/app.css", "/index.html":
			c.Set("Cache-Control", "no-cache")
			return c.Next()
		}
		// Every SPA route serves the same index.html shell, including deep links
		// such as /endpoints/phones_hoover. They have no file extension, which is
		// what separates them from the hashed assets, images and fonts that are
		// safe to cache.
		if !strings.Contains(p[strings.LastIndex(p, "/")+1:], ".") {
			c.Set("Cache-Control", "no-cache")
		}
		return c.Next()
	})
	// Define metrics handler, if necessary
	if cfg.Metrics {
		metricsHandler := promhttp.InstrumentMetricHandler(prometheus.DefaultRegisterer, promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
			DisableCompression: true,
		}))
		app.Get("/metrics", adaptor.HTTPHandler(metricsHandler))
	}
	// Define main router
	apiRouter := app.Group("/api")
	// Role gates for the routes that change state, from the local accounts in the
	// auth package (see api/auth.go). This is a SEPARATE mechanism from Gatus's
	// own `security:` block below: that one is a single shared credential in front
	// of whole groups, this one is per-person and per-role, and the two are kept
	// apart on purpose. Built once and shared rather than per route, so a gate is
	// one value the route table points at instead of a closure per line.
	//
	// Both fail open when the auth database is unavailable, so a broken SQLite
	// file costs the login screen and nothing else.
	requireOperator := RequireRole(auth.RoleOperator)
	requireAdmin := RequireRole(auth.RoleAdmin)
	////////////////////////
	// UNPROTECTED ROUTES //
	////////////////////////
	unprotectedAPIRouter := apiRouter.Group("/")
	unprotectedAPIRouter.Get("/v1/config", ConfigHandler{securityConfig: cfg.Security, config: cfg}.GetConfig)
	unprotectedAPIRouter.Get("/v1/version", VersionHandler)
	unprotectedAPIRouter.Get("/v1/time", TimeHandler)
	// Sign-in. Registered here, before ApplySecurityMiddleware, because
	// /v1/auth/me must answer on every page load whether or not anybody is signed
	// in: the frontend reads it to decide what to render, and a 401 from the
	// site-wide security middleware would be indistinguishable from the server
	// being down.
	unprotectedAPIRouter.Post("/v1/auth/login", AuthLogin)
	unprotectedAPIRouter.Post("/v1/auth/logout", AuthLogout)
	unprotectedAPIRouter.Get("/v1/auth/me", AuthMe)
	unprotectedAPIRouter.Post("/v1/auth/password", AuthChangePassword)
	// Account administration. Grouped so the admin gate covers everything under
	// /v1/users, including anything added later.
	usersRouter := unprotectedAPIRouter.Group("/v1/users", requireAdmin)
	usersRouter.Get("/", ListUsers)
	usersRouter.Post("/", CreateUser)
	usersRouter.Patch("/:id", UpdateUser)
	usersRouter.Delete("/:id", DeleteUser)
	unprotectedAPIRouter.Get("/v1/endpoints/:key/health/badge.svg", HealthBadge)
	unprotectedAPIRouter.Get("/v1/endpoints/:key/health/badge.shields", HealthBadgeShields)
	unprotectedAPIRouter.Get("/v1/endpoints/:key/uptimes/:duration", UptimeRaw)
	unprotectedAPIRouter.Get("/v1/endpoints/:key/uptimes/:duration/badge.svg", UptimeBadge)
	unprotectedAPIRouter.Get("/v1/endpoints/:key/response-times/:duration", ResponseTimeRaw)
	unprotectedAPIRouter.Get("/v1/endpoints/:key/response-times/:duration/badge.svg", ResponseTimeBadge(cfg))
	unprotectedAPIRouter.Get("/v1/endpoints/:key/response-times/:duration/chart.svg", ResponseTimeChart)
	unprotectedAPIRouter.Get("/v1/endpoints/:key/response-times/:duration/history", ResponseTimeHistory)
	// Uptime as a series of buckets over a range, rather than the single ratio
	// /uptimes/:duration returns. Hourly for roughly the last 48h, daily beyond,
	// because the store compacts older buckets: the response says which.
	unprotectedAPIRouter.Get("/v1/endpoints/:key/uptime-series", GetUptimeSeries)
	// This endpoint requires authz with bearer token, so technically it is protected.
	// COLLECTOR PUSH: no session gate, and it must stay that way. It authenticates
	// on the per-endpoint bearer token inside CreateExternalEndpointResult, which
	// is a different mechanism with a different threat model. Nothing is signed in
	// on the collector box, so requiring a cookie here takes the dashboard's whole
	// data feed down.
	unprotectedAPIRouter.Post("/v1/endpoints/:key/external", CreateExternalEndpointResult(cfg))
	// Phones inventory side-channel: collector POSTs the rich per-phone table
	// (token-auth'd like /external); the phones drill-in GETs it.
	// COLLECTOR PUSH: no session gate, same reasoning as /external above.
	unprotectedAPIRouter.Post("/v1/phones/:key", SetPhonesInventory(cfg))
	// Force-sweep: static route registered BEFORE the :key GET so it isn't
	// swallowed by :key="sweep-pending". Collector claims pending sweeps here.
	unprotectedAPIRouter.Get("/v1/phones/sweep-pending", ClaimPhonesSweeps)
	unprotectedAPIRouter.Post("/v1/phones/:key/sweep", requireOperator, RequestPhonesSweep)
	unprotectedAPIRouter.Get("/v1/phones/:key", GetPhonesInventory)
	unprotectedAPIRouter.Get("/v1/phones/:key/exclusions", GetPhonesExclusions)
	unprotectedAPIRouter.Post("/v1/phones/:key/exclusions", requireOperator, SetPhonesExclusion)
	unprotectedAPIRouter.Get("/v1/phones/:key/settings", GetPhonesSettings)
	unprotectedAPIRouter.Post("/v1/phones/:key/settings", requireOperator, SetPhonesSettings)
	// Pause monitoring, per endpoint key. Static route first so it isn't
	// swallowed by :key (same reason as /v1/phones/sweep-pending above).
	unprotectedAPIRouter.Get("/v1/monitoring", GetMonitoring)
	unprotectedAPIRouter.Get("/v1/monitoring/:key", GetMonitoringForKey)
	unprotectedAPIRouter.Post("/v1/monitoring/:key", requireOperator, SetMonitoringForKey(cfg))
	// UniFi side-channel: the collector POSTs per-site firewall/wireless
	// snapshots; the dashboard GETs them all at once for the card rows.
	// Static route first so it isn't swallowed by :key.
	unprotectedAPIRouter.Get("/v1/unifi", GetUniFiSnapshots)
	// COLLECTOR PUSH: no session gate, same reasoning as /external above.
	unprotectedAPIRouter.Post("/v1/unifi/:key", SetUniFiSnapshot(cfg))
	// Expected-uplink settings, read by the collector each sweep so an empty WAN
	// port stops reading as an outage. Registered before the bare :key GET for
	// the same reason as /v1/phones/sweep-pending above.
	unprotectedAPIRouter.Get("/v1/unifi/:key/settings", GetUniFiSettings)
	unprotectedAPIRouter.Post("/v1/unifi/:key/settings", requireOperator, SetUniFiSettings)
	unprotectedAPIRouter.Get("/v1/unifi/:key", GetUniFiSnapshot)
	// Collector metric history: the counts behind the phones, firewall and
	// wireless rows, sampled over time. Raw for recent windows, hourly rollups
	// beyond; the response says which resolution it served.
	unprotectedAPIRouter.Get("/v1/history/:key", GetMetricHistory)
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
	app.Get("/settings", SinglePageApplication(cfg.UI))
	app.Get("/ll-telemetry", SinglePageApplication(cfg.UI))
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
	protectedAPIRouter.Post("/v1/endpoints/:key/check", requireOperator, ForceEndpointCheck(cfg))
	protectedAPIRouter.Get("/v1/suites/statuses", SuiteStatuses(cfg))
	protectedAPIRouter.Get("/v1/suites/:key/statuses", SuiteStatus(cfg))
	// Live status stream (SSE) — a single broadcaster pushes the same snapshot
	// to every connected client so all screens stay in sync without refreshing.
	protectedAPIRouter.Get("/v1/live", newSSEHub().Handler)
	// LL-Telemetry: the vendored operations console plus a deny-by-default
	// proxy to the telemetry API. TelemetryGate authenticates these routes on
	// its own credentials, independent of cfg.Security — the telemetry upstream
	// has no auth of its own, and gating it via cfg.Security would also put the
	// SSE stream behind a login and break unattended wallboards. Sitting under
	// protectedAPIRouter means it additionally inherits site-wide auth if that
	// is ever enabled.
	//
	// The console route is registered BEFORE the wildcard so it is not
	// swallowed by it (same trap as /v1/phones/sweep-pending above).
	telemetryRouter := protectedAPIRouter.Group("/v1/telemetry", TelemetryGate)
	telemetryRouter.Post("/session", TelemetryLogin)
	telemetryRouter.Delete("/session", TelemetryLogout)
	telemetryRouter.Get("/console", TelemetryConsole)
	telemetryRouter.All("/*", TelemetryProxy)
	return app
}
