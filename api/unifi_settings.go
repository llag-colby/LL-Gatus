package api

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

// How many WAN uplinks a site is EXPECTED to have carrying traffic. A global
// default plus optional per-site (per firewall key) overrides, persisted to
// /data, editable live from the UI and read by the collector each sweep.
//
// This exists because a gateway reports every physical WAN port it has, whether
// or not anything is plugged into it. Counting those as configured uplinks puts
// a site on a permanent "1 of 2 WAN uplinks down" amber that nobody can clear.
//
// ExpectedWANs == 0 means AUTO: expect exactly the uplinks that are currently
// plugged in, so an empty port is not an outage. Set it to a number to assert
// "this site must have N uplinks carrying traffic" and have the row go amber
// when one of them goes dark, including a WAN that was unplugged entirely.
//
// Deliberately NOT a learned baseline. An earlier version of the phones
// collector ratcheted a high-water mark upward and had no way back down: one
// busy afternoon raised the bar permanently and a site that legitimately shrank
// alarmed forever. It was replaced by operator-set thresholds polled each
// sweep, and this mirrors that.

type unifiUplinks struct {
	ExpectedWANs int `json:"expectedWans"`
}

var (
	unSettingsMu       sync.RWMutex
	unSettingsGlobal   = unifiUplinks{ExpectedWANs: 0}
	unSettingsOverride = map[string]unifiUplinks{}
	unSettingsLoaded   bool
)

const unifiSettingsPath = "/data/unifi_settings.json"

type unifiSettingsFile struct {
	Global    unifiUplinks            `json:"global"`
	Overrides map[string]unifiUplinks `json:"overrides"`
}

func ensureUniFiSettingsLoaded() {
	unSettingsMu.Lock()
	defer unSettingsMu.Unlock()
	if unSettingsLoaded {
		return
	}
	unSettingsLoaded = true
	if b, err := os.ReadFile(unifiSettingsPath); err == nil {
		var f unifiSettingsFile
		if json.Unmarshal(b, &f) == nil {
			// 0 is a meaningful value here (auto), so unlike the phones
			// thresholds there is no ">0" guard on adopting the stored global.
			unSettingsGlobal = f.Global
			if f.Overrides != nil {
				unSettingsOverride = f.Overrides
			}
		}
	}
}

func persistUniFiSettings() {
	// caller holds unSettingsMu
	f := unifiSettingsFile{Global: unSettingsGlobal, Overrides: unSettingsOverride}
	if b, err := json.MarshalIndent(f, "", "  "); err == nil {
		if err := os.WriteFile(unifiSettingsPath, b, 0o644); err != nil {
			logr.Errorf("[api.persistUniFiSettings] write %s: %s", unifiSettingsPath, err.Error())
		}
	}
}

func effectiveUplinks(key string) (unifiUplinks, *unifiUplinks) {
	if ov, ok := unSettingsOverride[key]; ok {
		o := ov
		return ov, &o
	}
	return unSettingsGlobal, nil
}

func clampUplinks(u unifiUplinks) unifiUplinks {
	if u.ExpectedWANs < 0 {
		u.ExpectedWANs = 0
	}
	// A gateway with more than four WAN ports does not exist in this estate, and
	// a typo of 40 would park the row on a permanent red.
	if u.ExpectedWANs > 4 {
		u.ExpectedWANs = 4
	}
	return u
}

func unifiSettingsResponse(key string) fiber.Map {
	eff, ov := effectiveUplinks(key)
	resp := fiber.Map{"effective": eff, "global": unSettingsGlobal, "override": nil}
	if ov != nil {
		resp["override"] = *ov
	}
	return resp
}

// GetUniFiSettings returns the effective expected-uplink count for a key plus
// the global default and any site override. Read by the collector each sweep
// and by the UI. Unauthenticated read, matching the other UniFi read routes.
func GetUniFiSettings(c *fiber.Ctx) error {
	ensureUniFiSettingsLoaded()
	unSettingsMu.RLock()
	resp := unifiSettingsResponse(c.Params("key"))
	unSettingsMu.RUnlock()
	return c.Status(200).JSON(resp)
}

// SetUniFiSettings updates the expected uplink count. Body:
//
//	{"scope":"global"|"site","expectedWans":2,"clear":false}
//
// scope=global edits the default; scope=site edits this key's override
// (clear=true removes the override so it falls back to global). Operator-gated
// at the route, persisted to /data.
func SetUniFiSettings(c *fiber.Ctx) error {
	ensureUniFiSettingsLoaded()
	key := c.Params("key")
	var body struct {
		Scope        string `json:"scope"`
		ExpectedWANs int    `json:"expectedWans"`
		Clear        bool   `json:"clear"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return c.Status(400).SendString("invalid body")
	}
	unSettingsMu.Lock()
	if body.Scope == "global" {
		unSettingsGlobal = clampUplinks(unifiUplinks{ExpectedWANs: body.ExpectedWANs})
	} else { // site override
		if body.Clear {
			delete(unSettingsOverride, key)
		} else {
			unSettingsOverride[key] = clampUplinks(unifiUplinks{ExpectedWANs: body.ExpectedWANs})
		}
	}
	persistUniFiSettings()
	resp := unifiSettingsResponse(key)
	unSettingsMu.Unlock()
	return c.Status(200).JSON(resp)
}
