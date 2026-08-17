package api

import (
	"errors"
	"strings"
	"time"

	"github.com/TwiN/gatus/v5/config"
	"github.com/TwiN/gatus/v5/config/endpoint"
	"github.com/TwiN/gatus/v5/metrics"
	"github.com/TwiN/gatus/v5/monitoring"
	"github.com/TwiN/gatus/v5/storage/store"
	"github.com/TwiN/gatus/v5/storage/store/common"
	"github.com/TwiN/gatus/v5/watchdog"
	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

func CreateExternalEndpointResult(cfg *config.Config) fiber.Handler {
	extraLabels := cfg.GetUniqueExtraMetricLabels()
	return func(c *fiber.Ctx) error {
		// Check if the success query parameter is present
		success, exists := c.Queries()["success"]
		if !exists || (success != "true" && success != "false") {
			return c.Status(400).SendString("missing or invalid success query parameter")
		}
		// Check if the authorization bearer token header is correct
		authorizationHeader := string(c.Request().Header.Peek("Authorization"))
		if !strings.HasPrefix(authorizationHeader, "Bearer ") {
			return c.Status(401).SendString("invalid Authorization header")
		}
		token := strings.TrimSpace(strings.TrimPrefix(authorizationHeader, "Bearer "))
		if len(token) == 0 {
			return c.Status(401).SendString("bearer token must not be empty")
		}
		key := c.Params("key")
		externalEndpoint := cfg.GetExternalEndpointByKey(key)
		if externalEndpoint == nil {
			logr.Errorf("[api.CreateExternalEndpointResult] External endpoint with key=%s not found", key)
			return c.Status(404).SendString("not found")
		}
		if externalEndpoint.Token != token {
			logr.Errorf("[api.CreateExternalEndpointResult] Invalid token for external endpoint with key=%s", key)
			return c.Status(401).SendString("invalid token")
		}
		// If monitoring is paused for this endpoint, drop the pushed result on the
		// floor: it isn't stored, alerted on, or counted in metrics, and the existing
		// history is left untouched so un-pausing resumes the same timeline. We still
		// answer 200 — an error status would make every collector log a warning on
		// every sweep, which is noise, not information.
		if monitoring.IsPaused(key) {
			logr.Debugf("[api.CreateExternalEndpointResult] Monitoring paused; discarding result for external endpoint with key=%s", key)
			return c.Status(200).SendString("OK (monitoring paused)")
		}
		// Persist the result in the storage
		result := &endpoint.Result{
			Timestamp: time.Now(),
			Success:   c.QueryBool("success"),
			Errors:    []string{},
		}
		if len(c.Query("duration")) > 0 {
			parsedDuration, err := time.ParseDuration(c.Query("duration"))
			if err != nil {
				logr.Errorf("[api.CreateExternalEndpointResult] Invalid duration from string=%s with error: %s", c.Query("duration"), err.Error())
				return c.Status(400).SendString("invalid duration: " + err.Error())
			}
			result.Duration = parsedDuration
		}
		// Record the pushed reason even on a SUCCESSFUL result. Our collectors work in
		// three states — healthy / degraded / down — but an external result only
		// carries a bool, and they deliberately report degraded as success=true so a
		// partial problem doesn't fire a down alert. Without this, "1 of 2 WAN uplinks
		// down" was stored as a bare pass and the dashboard painted it green. A
		// successful result carrying errors now means "passed, with a warning", which
		// the UI renders amber. Alerting keys off Success alone, so this does not turn
		// warnings into alerts.
		if errorFromQuery := c.Query("error"); len(errorFromQuery) > 0 {
			result.AddError(errorFromQuery)
		}
		convertedEndpoint := externalEndpoint.ToEndpoint()
		if err := store.Get().InsertEndpointResult(convertedEndpoint, result); err != nil {
			if errors.Is(err, common.ErrEndpointNotFound) {
				return c.Status(404).SendString(err.Error())
			}
			logr.Errorf("[api.CreateExternalEndpointResult] Failed to insert result in storage: %s", err.Error())
			return c.Status(500).SendString(err.Error())
		}
		logr.Infof("[api.CreateExternalEndpointResult] Successfully inserted result for external endpoint with key=%s and success=%s", c.Params("key"), success)
		inEndpointMaintenanceWindow := false
		for _, maintenanceWindow := range externalEndpoint.MaintenanceWindows {
			if maintenanceWindow.IsUnderMaintenance() {
				logr.Debug("[api.CreateExternalEndpointResult] Under endpoint maintenance window")
				inEndpointMaintenanceWindow = true
			}
		}
		// Check if an alert should be triggered or resolved
		if !cfg.Maintenance.IsUnderMaintenance() && !inEndpointMaintenanceWindow {
			watchdog.HandleAlerting(convertedEndpoint, result, cfg.Alerting)
			externalEndpoint.NumberOfSuccessesInARow = convertedEndpoint.NumberOfSuccessesInARow
			externalEndpoint.NumberOfFailuresInARow = convertedEndpoint.NumberOfFailuresInARow
		} else {
			logr.Debug("[api.CreateExternalEndpointResult] Not handling alerting because currently in the maintenance window")
		}
		if cfg.Metrics {
			metrics.PublishMetricsForEndpoint(convertedEndpoint, result, extraLabels)
		}
		// Return the result
		return c.Status(200).SendString("")
	}
}
