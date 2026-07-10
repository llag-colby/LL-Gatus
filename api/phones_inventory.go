package api

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/TwiN/gatus/v5/config"
	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

// Phone inventory pushed by the external collector (collector/phone_collector.py).
// The Gatus external-endpoint only stores a single pass/fail; this side channel
// holds the rich per-phone table shown on the phones drill-in page.
//
// Ephemeral on purpose: the collector re-reports every sweep (15-45s), so losing
// this on a restart is harmless — it repopulates within one sweep.
var (
	phonesInventoryMu    sync.RWMutex
	phonesInventoryStore = make(map[string]storedInventory)
)

type storedInventory struct {
	UpdatedAt string          `json:"updatedAt"`
	Phones    json.RawMessage `json:"phones"`
}

// SetPhonesInventory receives the full per-phone inventory for a phones
// external-endpoint. Auth reuses that endpoint's configured push token, so the
// collector uses the same PHONES_PUSH_TOKEN it already holds.
func SetPhonesInventory(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
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
		var payload struct {
			Phones json.RawMessage `json:"phones"`
		}
		if err := json.Unmarshal(c.Body(), &payload); err != nil || len(payload.Phones) == 0 {
			return c.Status(400).SendString(`invalid body: expected {"phones": [...]}`)
		}
		phonesInventoryMu.Lock()
		phonesInventoryStore[key] = storedInventory{
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			Phones:    payload.Phones,
		}
		phonesInventoryMu.Unlock()
		logr.Infof("[api.SetPhonesInventory] Stored phone inventory for key=%s", key)
		return c.Status(200).SendString("OK")
	}
}

// GetPhonesInventory returns the last inventory reported for a phones endpoint.
func GetPhonesInventory(c *fiber.Ctx) error {
	key := c.Params("key")
	phonesInventoryMu.RLock()
	inv, ok := phonesInventoryStore[key]
	phonesInventoryMu.RUnlock()
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "no inventory reported yet"})
	}
	return c.Status(200).JSON(inv)
}
