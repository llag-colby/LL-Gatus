package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TwiN/gatus/v5/config"
	"github.com/TwiN/gatus/v5/config/ui"
	"github.com/TwiN/gatus/v5/storage"
	"github.com/gofiber/fiber/v2"
)

func TestNew(t *testing.T) {
	type Scenario struct {
		Name         string
		Method       string // defaults to GET
		Path         string
		ExpectedCode int
		Gzip         bool
	}
	scenarios := []Scenario{
		{
			Name:         "health",
			Path:         "/health",
			ExpectedCode: fiber.StatusOK,
		},
		{
			Name:         "custom.css",
			Path:         "/css/custom.css",
			ExpectedCode: fiber.StatusOK,
		},
		{
			Name:         "custom.css-gzipped",
			Path:         "/css/custom.css",
			ExpectedCode: fiber.StatusOK,
			Gzip:         true,
		},
		{
			Name:         "metrics",
			Path:         "/metrics",
			ExpectedCode: fiber.StatusOK,
		},
		{
			Name:         "favicon.ico",
			Path:         "/favicon.ico",
			ExpectedCode: fiber.StatusOK,
		},
		{
			Name:         "app.js",
			Path:         "/js/app.js",
			ExpectedCode: fiber.StatusOK,
		},
		{
			Name:         "app.js-gzipped",
			Path:         "/js/app.js",
			ExpectedCode: fiber.StatusOK,
			Gzip:         true,
		},
		{
			Name:         "chunk-vendors.js",
			Path:         "/js/chunk-vendors.js",
			ExpectedCode: fiber.StatusOK,
		},
		{
			Name:         "chunk-vendors.js-gzipped",
			Path:         "/js/chunk-vendors.js",
			ExpectedCode: fiber.StatusOK,
			Gzip:         true,
		},
		{
			Name:         "index",
			Path:         "/",
			ExpectedCode: fiber.StatusOK,
		},
		{
			Name:         "index-html-redirect",
			Path:         "/index.html",
			ExpectedCode: fiber.StatusMovedPermanently,
		},
		{
			Name:         "config-should-always-return-200",
			Path:         "/api/v1/config",
			ExpectedCode: fiber.StatusOK,
		},
		// There is no sign-in. Statuses used to sit behind the site-wide security
		// middleware and answer 401 to an anonymous caller; every caller is
		// anonymous now, so it has to answer normally.
		{
			Name:         "endpoints-statuses-are-open",
			Path:         "/api/v1/endpoints/statuses",
			ExpectedCode: fiber.StatusOK,
		},
		// The routes that made up the login are gone, not merely unenforced. A
		// 200 from any of these would mean the accounts API came back.
		{
			Name:         "auth-me-is-gone",
			Path:         "/api/v1/auth/me",
			ExpectedCode: fiber.StatusNotFound,
		},
		{
			Name:         "auth-login-is-gone",
			Method:       "POST",
			Path:         "/api/v1/auth/login",
			ExpectedCode: fiber.StatusNotFound,
		},
		{
			Name:         "users-is-gone",
			Path:         "/api/v1/users",
			ExpectedCode: fiber.StatusNotFound,
		},
		// Telemetry is disabled: unrouted, so 404 rather than the gate's 401/503.
		{
			Name:         "telemetry-console-is-gone",
			Path:         "/api/v1/telemetry/console",
			ExpectedCode: fiber.StatusNotFound,
		},
		{
			Name:         "telemetry-proxy-is-gone",
			Path:         "/api/v1/telemetry/health",
			ExpectedCode: fiber.StatusNotFound,
		},
		// The SPA deep link went with it, while the other deep links stayed.
		{
			Name:         "ll-telemetry-spa-route-is-gone",
			Path:         "/ll-telemetry",
			ExpectedCode: fiber.StatusNotFound,
		},
		{
			Name:         "settings-spa-route-still-serves",
			Path:         "/settings",
			ExpectedCode: fiber.StatusOK,
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			cfg := &config.Config{Metrics: true, UI: &ui.Config{}, Storage: &storage.Config{}}
			api := New(cfg)
			router := api.Router()
			method := scenario.Method
			if method == "" {
				method = "GET"
			}
			request := httptest.NewRequest(method, scenario.Path, http.NoBody)
			if scenario.Gzip {
				request.Header.Set("Accept-Encoding", "gzip")
			}
			response, err := router.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != scenario.ExpectedCode {
				t.Errorf("%s %s should have returned %d, but returned %d instead", request.Method, request.URL, scenario.ExpectedCode, response.StatusCode)
			}
		})
	}
}
