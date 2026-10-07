package api

import (
	"io/fs"
	"net/http"
	"os"
	"strings"

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
	// Every route is open. The dashboard carries no sign-in: it is served on the
	// internal network and read by unattended wallboards, so there is no session
	// to hold and nothing to gate on. One credential remains, and it belongs to a
	// machine rather than a person: the per-endpoint bearer token the collectors
	// push with, checked inside the handlers that accept a push.
	//
	// The auth and security packages are still in the tree but nothing wires them
	// up. Re-enabling means registering their middleware here, nothing else.
	apiV1Router := apiRouter.Group("/")
	apiV1Router.Get("/v1/config", ConfigHandler{securityConfig: cfg.Security, config: cfg}.GetConfig)
	apiV1Router.Get("/v1/version", VersionHandler)
	apiV1Router.Get("/v1/time", TimeHandler)
	apiV1Router.Get("/v1/endpoints/:key/health/badge.svg", HealthBadge)
	apiV1Router.Get("/v1/endpoints/:key/health/badge.shields", HealthBadgeShields)
	apiV1Router.Get("/v1/endpoints/:key/uptimes/:duration", UptimeRaw)
	apiV1Router.Get("/v1/endpoints/:key/uptimes/:duration/badge.svg", UptimeBadge)
	apiV1Router.Get("/v1/endpoints/:key/response-times/:duration", ResponseTimeRaw)
	apiV1Router.Get("/v1/endpoints/:key/response-times/:duration/badge.svg", ResponseTimeBadge(cfg))
	apiV1Router.Get("/v1/endpoints/:key/response-times/:duration/chart.svg", ResponseTimeChart)
	apiV1Router.Get("/v1/endpoints/:key/response-times/:duration/history", ResponseTimeHistory)
	// Uptime as a series of buckets over a range, rather than the single ratio
	// /uptimes/:duration returns. Hourly for roughly the last 48h, daily beyond,
	// because the store compacts older buckets: the response says which.
	apiV1Router.Get("/v1/endpoints/:key/uptime-series", GetUptimeSeries)
	// This endpoint requires authz with bearer token, so technically it is protected.
	// COLLECTOR PUSH: no session gate, and it must stay that way. It authenticates
	// on the per-endpoint bearer token inside CreateExternalEndpointResult, which
	// is a different mechanism with a different threat model. Nothing is signed in
	// on the collector box, so requiring a cookie here takes the dashboard's whole
	// data feed down.
	apiV1Router.Post("/v1/endpoints/:key/external", CreateExternalEndpointResult(cfg))
	// Phones inventory side-channel: collector POSTs the rich per-phone table
	// (token-auth'd like /external); the phones drill-in GETs it.
	// COLLECTOR PUSH: no session gate, same reasoning as /external above.
	apiV1Router.Post("/v1/phones/:key", SetPhonesInventory(cfg))
	// Force-sweep: static route registered BEFORE the :key GET so it isn't
	// swallowed by :key="sweep-pending". Collector claims pending sweeps here.
	apiV1Router.Get("/v1/phones/sweep-pending", ClaimPhonesSweeps)
	apiV1Router.Post("/v1/phones/:key/sweep", RequestPhonesSweep)
	apiV1Router.Get("/v1/phones/:key", GetPhonesInventory)
	apiV1Router.Get("/v1/phones/:key/exclusions", GetPhonesExclusions)
	apiV1Router.Post("/v1/phones/:key/exclusions", SetPhonesExclusion)
	apiV1Router.Get("/v1/phones/:key/settings", GetPhonesSettings)
	apiV1Router.Post("/v1/phones/:key/settings", SetPhonesSettings)
	// Pause monitoring, per endpoint key. Static route first so it isn't
	// swallowed by :key (same reason as /v1/phones/sweep-pending above).
	apiV1Router.Get("/v1/monitoring", GetMonitoring)
	apiV1Router.Get("/v1/monitoring/:key", GetMonitoringForKey)
	apiV1Router.Post("/v1/monitoring/:key", SetMonitoringForKey(cfg))
	// UniFi side-channel: the collector POSTs per-site firewall/wireless
	// snapshots; the dashboard GETs them all at once for the card rows.
	// Static route first so it isn't swallowed by :key.
	apiV1Router.Get("/v1/unifi", GetUniFiSnapshots)
	// COLLECTOR PUSH: no session gate, same reasoning as /external above.
	apiV1Router.Post("/v1/unifi/:key", SetUniFiSnapshot(cfg))
	// Expected-uplink settings, read by the collector each sweep so an empty WAN
	// port stops reading as an outage. These carry an extra path segment, so
	// unlike /v1/phones/sweep-pending they cannot actually be swallowed by the
	// bare :key route; kept above it as a matter of habit, not necessity.
	apiV1Router.Get("/v1/unifi/:key/settings", GetUniFiSettings)
	apiV1Router.Post("/v1/unifi/:key/settings", SetUniFiSettings)
	apiV1Router.Get("/v1/unifi/:key", GetUniFiSnapshot)
	// SMB share side-channel, same shape as UniFi above: the collector POSTs one
	// snapshot per share, the drill-in GETs them all at once for its sibling
	// list. Static route first so it isn't swallowed by :key.
	apiV1Router.Get("/v1/smb", GetSMBSnapshots)
	// COLLECTOR PUSH: no session gate, same reasoning as /external above.
	apiV1Router.Post("/v1/smb/:key", SetSMBSnapshot(cfg))
	apiV1Router.Get("/v1/smb/:key", GetSMBSnapshot)
	// Hypervisor side-channel, same shape again: CPU, memory, volumes and the
	// guest inventory per Hyper-V host. Static route first so it isn't swallowed
	// by :key.
	apiV1Router.Get("/v1/hv", GetHVSnapshots)
	// COLLECTOR PUSH: no session gate, same reasoning as /external above.
	apiV1Router.Post("/v1/hv/:key", SetHVSnapshot(cfg))
	apiV1Router.Get("/v1/hv/:key", GetHVSnapshot)
	// Dashboard layout: which cards and rows are shown, in what order, under
	// what name. Presentation only, shared by every screen, and deliberately
	// separate from /v1/monitoring, which is the control that actually stops a
	// check. Static route registered before nothing in particular here, but kept
	// with its siblings for findability.
	apiV1Router.Get("/v1/layout", GetLayout)
	apiV1Router.Put("/v1/layout", SetLayout)
	apiV1Router.Delete("/v1/layout", ResetLayout)
	// Runtime target overrides: re-point a check without rebuilding the image.
	// The GET is static and registered before the :key routes below so it isn't
	// swallowed by :key="targets", same rule as /v1/phones/sweep-pending.
	//
	// The PATCH and DELETE are the ONLY gated routes on this server: they take
	// GATUS_EDIT_TOKEN as a bearer token, checked in authorizeEdit. Unset
	// means editing is off, not open. See api/endpoint_targets.go for why these
	// are treated differently from every other write route here.
	apiV1Router.Get("/v1/endpoints/targets", GetEndpointTargets(cfg))
	apiV1Router.Patch("/v1/endpoints/:key/target", SetEndpointTarget(cfg))
	apiV1Router.Delete("/v1/endpoints/:key/target", DeleteEndpointTarget(cfg))
	// Collector metric history: the counts behind the phones, firewall and
	// wireless rows, sampled over time. Raw for recent windows, hourly rollups
	// beyond; the response says which resolution it served.
	apiV1Router.Get("/v1/history/:key", GetMetricHistory)
	// Jira service-desk metrics, refreshed by the background jira poller.
	apiV1Router.Get("/v1/jira/metrics", GetJiraMetrics)
	// Jira ticket drill-down: fetches one issue's detail on demand.
	apiV1Router.Get("/v1/jira/issue/:key", GetJiraIssue)
	// Jira live stream (SSE): pushes a fresh snapshot on every poll.
	apiV1Router.Get("/v1/jira/live", JiraLive)
	// Jira Kanban: the agile boards themselves (columns, WIP limits, cards).
	// Per-assignee counts for the team dashboard; cached, ?refresh=1 to force.
	apiV1Router.Get("/v1/jira/breakdown", GetJiraBreakdown)
	apiV1Router.Get("/v1/jira/boards", GetJiraBoards)
	apiV1Router.Get("/v1/jira/board/:id", GetJiraBoard)
	apiV1Router.Get("/v1/jira/board/:id/live", JiraBoardLive)
	// SPA
	app.Get("/", SinglePageApplication(cfg.UI))
	app.Get("/endpoints/:key", SinglePageApplication(cfg.UI))
	app.Get("/suites/:key", SinglePageApplication(cfg.UI))
	app.Get("/sites/:name", SinglePageApplication(cfg.UI))
	app.Get("/jira", SinglePageApplication(cfg.UI))
	app.Get("/settings", SinglePageApplication(cfg.UI))
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
	// These used to sit behind the site-wide security middleware, which is why
	// they are registered down here rather than with the rest of the v1 table.
	// Nothing depends on that position now — the static filesystem above calls
	// Next() on a miss — so they are open like everything else, and moving them
	// up would be a safe tidy-up rather than a behaviour change.
	apiV1Router.Get("/v1/endpoints/statuses", EndpointStatuses(cfg))
	apiV1Router.Get("/v1/endpoints/:key/statuses", EndpointStatus(cfg))
	// Force ping: runs one out-of-band check now instead of waiting out the
	// endpoint's interval (the "Force ping" button on the endpoint drill-in).
	apiV1Router.Post("/v1/endpoints/:key/check", ForceEndpointCheck(cfg))
	apiV1Router.Get("/v1/suites/statuses", SuiteStatuses(cfg))
	apiV1Router.Get("/v1/suites/:key/statuses", SuiteStatus(cfg))
	// Live status stream (SSE) — a single broadcaster pushes the same snapshot
	// to every connected client so all screens stay in sync without refreshing.
	apiV1Router.Get("/v1/live", newSSEHub().Handler)
	// LL-Telemetry is disabled: the console and its proxy are no longer routed,
	// so /api/v1/telemetry/* is a 404. api/telemetry.go and its vendored console
	// asset stay in the tree (that file's init() prepares the embedded HTML and
	// must keep compiling). Re-enabling means these five registrations, the
	// app.Get("/ll-telemetry", ...) SPA deep link removed from the block above,
	// and the frontend route and header button.
	return app
}
