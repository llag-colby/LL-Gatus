package api

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TwiN/gatus/v5/config"
	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

// Phone inventory pushed by the external collector (collector/phone_collector.py).
// The Gatus external-endpoint only stores a single pass/fail; this side channel
// holds the rich per-phone table + tri-state health shown on the drill-in.
//
// Inventory is ephemeral (the collector re-reports every sweep). Exclusions,
// however, are persisted to /data (mounted, gitignored) so they survive updates.
var (
	phonesInventoryMu    sync.RWMutex
	phonesInventoryStore = make(map[string]storedInventory)

	exclusionsMu     sync.RWMutex
	exclusionsData   = map[string][]string{}
	exclusionsLoaded bool
)

const exclusionsPath = "/data/phones_exclusions.json"

type storedInventory struct {
	UpdatedAt string          `json:"updatedAt"`
	Status    string          `json:"status,omitempty"`
	Counts    json.RawMessage `json:"counts,omitempty"`
	Phones    json.RawMessage `json:"phones"`
}

// --- Inventory (rich per-phone table) --------------------------------------

// SetPhonesInventory receives the full inventory for a phones external-endpoint.
// Auth reuses that endpoint's configured push token.
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
			Status string          `json:"status"`
			Counts json.RawMessage `json:"counts"`
		}
		if err := json.Unmarshal(c.Body(), &payload); err != nil || len(payload.Phones) == 0 {
			return c.Status(400).SendString(`invalid body: expected {"phones": [...]}`)
		}
		phonesInventoryMu.Lock()
		phonesInventoryStore[key] = storedInventory{
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			Status:    payload.Status,
			Counts:    payload.Counts,
			Phones:    payload.Phones,
		}
		phonesInventoryMu.Unlock()
		logr.Infof("[api.SetPhonesInventory] Stored inventory for key=%s status=%s", key, payload.Status)
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

// --- Exclusions (persisted; extensions ignored by health + marked in the UI) -

func ensureExclusionsLoaded() {
	exclusionsMu.Lock()
	defer exclusionsMu.Unlock()
	if exclusionsLoaded {
		return
	}
	exclusionsLoaded = true
	if b, err := os.ReadFile(exclusionsPath); err == nil {
		_ = json.Unmarshal(b, &exclusionsData)
	}
}

func persistExclusions() {
	// caller holds exclusionsMu
	if b, err := json.MarshalIndent(exclusionsData, "", "  "); err == nil {
		if err := os.WriteFile(exclusionsPath, b, 0o644); err != nil {
			logr.Errorf("[api.persistExclusions] could not write %s: %s", exclusionsPath, err.Error())
		}
	}
}

// GetPhonesExclusions returns the excluded extensions for a phones endpoint.
// Read by both the collector (each sweep) and the drill-in UI.
func GetPhonesExclusions(c *fiber.Ctx) error {
	ensureExclusionsLoaded()
	key := c.Params("key")
	exclusionsMu.RLock()
	list := append([]string{}, exclusionsData[key]...)
	exclusionsMu.RUnlock()
	sort.Strings(list)
	return c.Status(200).JSON(fiber.Map{"excluded": list})
}

// SetPhonesExclusion toggles one extension in/out of the exclusion list.
// Body: {"ext":"0150","excluded":true}. Unauthenticated (internal LAN tool,
// consistent with the rest of the API), state persisted to /data.
func SetPhonesExclusion(c *fiber.Ctx) error {
	ensureExclusionsLoaded()
	key := c.Params("key")
	var body struct {
		Ext      string `json:"ext"`
		Excluded bool   `json:"excluded"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil || body.Ext == "" {
		return c.Status(400).SendString(`invalid body: expected {"ext":"...","excluded":true|false}`)
	}
	exclusionsMu.Lock()
	set := map[string]bool{}
	for _, e := range exclusionsData[key] {
		set[e] = true
	}
	if body.Excluded {
		set[body.Ext] = true
	} else {
		delete(set, body.Ext)
	}
	list := make([]string, 0, len(set))
	for e := range set {
		list = append(list, e)
	}
	sort.Strings(list)
	exclusionsData[key] = list
	persistExclusions()
	exclusionsMu.Unlock()
	return c.Status(200).JSON(fiber.Map{"excluded": list})
}
