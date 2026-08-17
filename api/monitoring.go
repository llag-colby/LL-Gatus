package api

import (
	"encoding/json"

	"github.com/TwiN/gatus/v5/config"
	"github.com/TwiN/gatus/v5/monitoring"
	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

// Pause monitoring, per endpoint key. The state itself lives in the monitoring
// package (it's read by the watchdog too); these handlers are just the UI's way
// of reading and toggling it. Unauthenticated, consistent with the rest of this
// API (internal LAN tool).

// GetMonitoring returns every paused endpoint key.
//
// "paused" and "disabled" are ALIASES holding the same sorted list: "paused" is
// the name the backend uses, "disabled" is the name the frontend reads. Both are
// emitted on purpose — don't "clean up" either one.
func GetMonitoring(c *fiber.Ctx) error {
	list := monitoring.Paused()
	return c.Status(200).JSON(fiber.Map{"paused": list, "disabled": list})
}

// GetMonitoringForKey returns whether one endpoint is currently monitored.
func GetMonitoringForKey(c *fiber.Ctx) error {
	key := c.Params("key")
	return c.Status(200).JSON(fiber.Map{"key": key, "monitored": !monitoring.IsPaused(key)})
}

// SetMonitoringForKey pauses or resumes monitoring for one endpoint.
// Body: {"monitored":true|false}. State is persisted to /data.
//
// The key must belong to a configured endpoint. Without that check any string
// would be persisted, so a typo or a stale bookmark would grow
// /data/monitoring.json with keys that match nothing and never expire.
func SetMonitoringForKey(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		key := c.Params("key")
		var body struct {
			Monitored bool `json:"monitored"`
		}
		if err := json.Unmarshal(c.Body(), &body); err != nil {
			return c.Status(400).SendString(`invalid body: expected {"monitored":true|false}`)
		}
		if !isKnownEndpointKey(cfg, key) {
			return c.Status(404).JSON(fiber.Map{"error": "no configured endpoint with key " + key})
		}
		monitoring.SetPaused(key, !body.Monitored)
		logr.Infof("[api.SetMonitoringForKey] Set monitored=%v for key=%s", body.Monitored, key)
		return c.Status(200).JSON(fiber.Map{"key": key, "monitored": body.Monitored})
	}
}

// isKnownEndpointKey reports whether key names a configured endpoint, whether
// Gatus probes it itself or a collector pushes to it.
func isKnownEndpointKey(cfg *config.Config, key string) bool {
	if cfg == nil {
		return false
	}
	for _, ep := range cfg.Endpoints {
		if ep.Key() == key {
			return true
		}
	}
	return cfg.GetExternalEndpointByKey(key) != nil
}
