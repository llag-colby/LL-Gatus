package api

import (
	"sync"
	"time"

	"github.com/TwiN/gatus/v5/config"
	"github.com/TwiN/gatus/v5/config/endpoint"
	"github.com/TwiN/gatus/v5/monitoring"
	"github.com/TwiN/gatus/v5/watchdog"
	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

// forceCheckCooldown is the minimum delay between two forced checks of the same
// endpoint. The button exists to answer "is it back up NOW?", not to let a held
// mouse button turn the dashboard into a ping flood.
const forceCheckCooldown = 3 * time.Second

var (
	forceCheckMu   sync.Mutex
	forceCheckLast = map[string]time.Time{}
	// Keys with a check currently in flight. A second request for the same key
	// returns 409 instead of queueing behind the first one (the watchdog
	// semaphore would serialize them anyway, holding the HTTP request open).
	forceCheckInFlight = map[string]bool{}
)

// claimForceCheck reserves the right to run a forced check for key. The returned
// release func MUST be called when the check finishes (it is nil when the claim
// was refused).
func claimForceCheck(key string) (retryAfter time.Duration, inFlight bool, release func()) {
	forceCheckMu.Lock()
	defer forceCheckMu.Unlock()
	if forceCheckInFlight[key] {
		return 0, true, nil
	}
	if elapsed := time.Since(forceCheckLast[key]); elapsed < forceCheckCooldown {
		return forceCheckCooldown - elapsed, false, nil
	}
	forceCheckInFlight[key] = true
	return 0, false, func() {
		forceCheckMu.Lock()
		delete(forceCheckInFlight, key)
		forceCheckLast[key] = time.Now()
		forceCheckMu.Unlock()
	}
}

// ForceEndpointCheck runs an immediate, out-of-band check of a single endpoint
// and returns its result, instead of waiting out the endpoint's interval.
//
// The result is persisted and alerted on like any scheduled check, so the
// drill-in only has to re-fetch its statuses afterwards to show it.
func ForceEndpointCheck(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		key := c.Params("key")
		var target *endpoint.Endpoint
		for _, ep := range cfg.Endpoints {
			if ep.Key() == key {
				target = ep
				break
			}
		}
		if target == nil {
			// Push-based endpoints (phones) are never probed by Gatus — the
			// collector owns them, and the phones drill-in has its own
			// force-sweep button for exactly this.
			for _, ee := range cfg.ExternalEndpoints {
				if ee.Key() == key {
					return c.Status(400).JSON(fiber.Map{"error": "this endpoint is push-based; Gatus cannot probe it directly"})
				}
			}
			return c.Status(404).JSON(fiber.Map{"error": "endpoint not found"})
		}
		if !target.IsEnabled() {
			return c.Status(400).JSON(fiber.Map{"error": "endpoint is disabled"})
		}
		// Paused endpoints are refused up front. Without this the check falls through
		// to the watchdog's own pause guard, which returns ErrCheckSkipped — whose
		// message blames shutdown or connectivity and would send someone chasing a
		// problem that doesn't exist.
		if monitoring.IsPaused(key) {
			return c.Status(409).JSON(fiber.Map{"error": "monitoring is paused for this endpoint; resume it to run a check"})
		}
		retryAfter, inFlight, release := claimForceCheck(key)
		if inFlight {
			return c.Status(409).JSON(fiber.Map{"error": "a check is already running for this endpoint"})
		}
		if release == nil {
			c.Set("Retry-After", "1")
			return c.Status(429).JSON(fiber.Map{
				"error":        "checked too recently; try again shortly",
				"retryAfterMs": retryAfter.Milliseconds(),
			})
		}
		defer release()
		logr.Infof("[api.ForceEndpointCheck] Forcing check of group=%s; endpoint=%s; key=%s", target.Group, target.Name, key)
		result, err := watchdog.ExecuteEndpointNow(target, cfg)
		if err != nil {
			return c.Status(503).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(200).JSON(fiber.Map{
			"key":        key,
			"success":    result.Success,
			"durationMs": result.Duration.Milliseconds(),
			"hostname":   result.Hostname,
			"ip":         result.IP,
			"errors":     result.Errors,
			"timestamp":  result.Timestamp,
		})
	}
}
