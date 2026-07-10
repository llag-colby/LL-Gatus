package api

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

// Phone health thresholds — how many MONITORED phones offline flips a location
// to degraded / down. A global default plus optional per-site (per phones key)
// overrides. Persisted to /data (survives updates), editable live from the UI,
// and read by the collector each sweep.

type phoneThresholds struct {
	DegradedAt int `json:"degradedAt"`
	DownAt     int `json:"downAt"`
}

var (
	phSettingsMu       sync.RWMutex
	phSettingsGlobal   = phoneThresholds{DegradedAt: 2, DownAt: 10}
	phSettingsOverride = map[string]phoneThresholds{}
	phSettingsLoaded   bool
)

const phonesSettingsPath = "/data/phones_settings.json"

type phonesSettingsFile struct {
	Global    phoneThresholds            `json:"global"`
	Overrides map[string]phoneThresholds `json:"overrides"`
}

func ensurePhonesSettingsLoaded() {
	phSettingsMu.Lock()
	defer phSettingsMu.Unlock()
	if phSettingsLoaded {
		return
	}
	phSettingsLoaded = true
	if b, err := os.ReadFile(phonesSettingsPath); err == nil {
		var f phonesSettingsFile
		if json.Unmarshal(b, &f) == nil {
			if f.Global.DegradedAt > 0 {
				phSettingsGlobal = f.Global
			}
			if f.Overrides != nil {
				phSettingsOverride = f.Overrides
			}
		}
	}
}

func persistPhonesSettings() {
	// caller holds phSettingsMu
	f := phonesSettingsFile{Global: phSettingsGlobal, Overrides: phSettingsOverride}
	if b, err := json.MarshalIndent(f, "", "  "); err == nil {
		if err := os.WriteFile(phonesSettingsPath, b, 0o644); err != nil {
			logr.Errorf("[api.persistPhonesSettings] write %s: %s", phonesSettingsPath, err.Error())
		}
	}
}

func effectiveThresholds(key string) (phoneThresholds, *phoneThresholds) {
	if ov, ok := phSettingsOverride[key]; ok {
		o := ov
		return ov, &o
	}
	return phSettingsGlobal, nil
}

func clampThresholds(t phoneThresholds) phoneThresholds {
	if t.DegradedAt < 1 {
		t.DegradedAt = 1
	}
	if t.DownAt < t.DegradedAt {
		t.DownAt = t.DegradedAt
	}
	return t
}

func settingsResponse(key string) fiber.Map {
	eff, ov := effectiveThresholds(key)
	resp := fiber.Map{"effective": eff, "global": phSettingsGlobal, "override": nil}
	if ov != nil {
		resp["override"] = *ov
	}
	return resp
}

// GetPhonesSettings returns effective thresholds for a key + the global default
// and any site override. Read by the collector each sweep and by the UI.
func GetPhonesSettings(c *fiber.Ctx) error {
	ensurePhonesSettingsLoaded()
	phSettingsMu.RLock()
	resp := settingsResponse(c.Params("key"))
	phSettingsMu.RUnlock()
	return c.Status(200).JSON(resp)
}

// SetPhonesSettings updates thresholds. Body:
//
//	{"scope":"global"|"site","degradedAt":2,"downAt":10,"clear":false}
//
// scope=global edits the default; scope=site edits this key's override
// (clear=true removes the override so it falls back to global). Unauthenticated
// (internal LAN tool), persisted to /data.
func SetPhonesSettings(c *fiber.Ctx) error {
	ensurePhonesSettingsLoaded()
	key := c.Params("key")
	var body struct {
		Scope      string `json:"scope"`
		DegradedAt int    `json:"degradedAt"`
		DownAt     int    `json:"downAt"`
		Clear      bool   `json:"clear"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return c.Status(400).SendString("invalid body")
	}
	phSettingsMu.Lock()
	if body.Scope == "global" {
		phSettingsGlobal = clampThresholds(phoneThresholds{DegradedAt: body.DegradedAt, DownAt: body.DownAt})
	} else { // site override
		if body.Clear {
			delete(phSettingsOverride, key)
		} else {
			phSettingsOverride[key] = clampThresholds(phoneThresholds{DegradedAt: body.DegradedAt, DownAt: body.DownAt})
		}
	}
	persistPhonesSettings()
	resp := settingsResponse(key)
	phSettingsMu.Unlock()
	return c.Status(200).JSON(resp)
}
