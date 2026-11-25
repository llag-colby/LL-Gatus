package api

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

// Wireless health rules, operator-set from the wireless drill-in and read by
// unifi-collector each sweep. Two independent things live here:
//
//   - Thresholds: how many COUNTED access points offline make a site degraded
//     or down, and the tx-retry level that reads as degraded. A global default
//     plus optional per-site (per wireless key) overrides, the same shape as the
//     phones thresholds.
//   - Ignored APs: radios an operator has counted out of a site's health, by
//     MAC (or by name when the snapshot carried no MAC). Per site only; there is
//     no global ignore list. An ignored AP is still listed and still shown, it
//     just stops moving the row's status.
//
// Persisted to /data so a setting survives updates and restarts.

type wirelessThresholds struct {
	// DegradedAt: this many counted APs offline (or more) is degraded. >= 1.
	DegradedAt int `json:"degradedAt"`
	// DownAt: this many counted APs offline (or more) is down. 0 means only
	// when every counted AP is offline, which is the behaviour before this
	// setting existed.
	DownAt int `json:"downAt"`
	// TxRetryPct: site tx retry above this is degraded. 0 turns the check off.
	TxRetryPct float64 `json:"txRetryPct"`
}

var defaultWirelessThresholds = wirelessThresholds{DegradedAt: 1, DownAt: 0, TxRetryPct: 20}

var (
	wlSettingsMu       sync.RWMutex
	wlSettingsGlobal   = defaultWirelessThresholds
	wlSettingsOverride = map[string]wirelessThresholds{}
	wlIgnored          = map[string][]string{}
	wlSettingsLoaded   bool
)

// A var rather than a const so tests can point it at a temp dir.
var wirelessSettingsPath = "/data/unifi_wireless_settings.json"

type wirelessSettingsFile struct {
	Global    wirelessThresholds            `json:"global"`
	Overrides map[string]wirelessThresholds `json:"overrides"`
	Ignored   map[string][]string           `json:"ignored"`
}

func ensureWirelessSettingsLoaded() {
	wlSettingsMu.Lock()
	defer wlSettingsMu.Unlock()
	if wlSettingsLoaded {
		return
	}
	wlSettingsLoaded = true
	b, err := os.ReadFile(wirelessSettingsPath)
	if err != nil {
		return
	}
	var f wirelessSettingsFile
	if json.Unmarshal(b, &f) != nil {
		logr.Errorf("[api.ensureWirelessSettingsLoaded] %s does not parse; using defaults", wirelessSettingsPath)
		return
	}
	// Clamp on the way in too: the file is hand-editable.
	wlSettingsGlobal = clampWireless(f.Global)
	for k, v := range f.Overrides {
		wlSettingsOverride[k] = clampWireless(v)
	}
	for k, v := range f.Ignored {
		if ids := normaliseIDs(v); len(ids) > 0 {
			wlIgnored[k] = ids
		}
	}
}

func persistWirelessSettings() {
	// caller holds wlSettingsMu
	f := wirelessSettingsFile{Global: wlSettingsGlobal, Overrides: wlSettingsOverride, Ignored: wlIgnored}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		logr.Errorf("[api.persistWirelessSettings] marshal: %s", err.Error())
		return
	}
	// Sibling + rename, so a crash mid-write cannot truncate the file and
	// silently drop every override and ignore.
	tmp := wirelessSettingsPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		logr.Errorf("[api.persistWirelessSettings] write %s: %s", tmp, err.Error())
		return
	}
	if err := os.Rename(tmp, wirelessSettingsPath); err != nil {
		logr.Errorf("[api.persistWirelessSettings] rename %s: %s", wirelessSettingsPath, err.Error())
	}
}

func clampWireless(t wirelessThresholds) wirelessThresholds {
	if t.DegradedAt < 1 {
		t.DegradedAt = 1
	}
	if t.DownAt < 0 {
		t.DownAt = 0
	}
	// A down threshold below the degraded one would skip amber entirely.
	if t.DownAt != 0 && t.DownAt < t.DegradedAt {
		t.DownAt = t.DegradedAt
	}
	if t.TxRetryPct < 0 {
		t.TxRetryPct = 0
	}
	if t.TxRetryPct > 100 {
		t.TxRetryPct = 100
	}
	return t
}

// IDs are MACs (or names), compared case-insensitively, so store them lowered,
// trimmed, de-duplicated and sorted. Sorted keeps the file diff-stable.
func normaliseIDs(ids []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func wirelessSettingsResponse(key string) fiber.Map {
	// caller holds wlSettingsMu (read or write)
	eff := wlSettingsGlobal
	var override interface{}
	if ov, ok := wlSettingsOverride[key]; ok {
		eff = ov
		override = ov
	}
	ignored := wlIgnored[key]
	if ignored == nil {
		ignored = []string{}
	}
	return fiber.Map{
		"effective": eff,
		"global":    wlSettingsGlobal,
		"override":  override,
		"ignored":   append([]string{}, ignored...),
	}
}

// GetWirelessSettings returns the effective thresholds for a wireless key, the
// global default, any site override and the site's ignored APs. Read by the
// collector each sweep and by the wireless drill-in.
func GetWirelessSettings(c *fiber.Ctx) error {
	ensureWirelessSettingsLoaded()
	wlSettingsMu.RLock()
	resp := wirelessSettingsResponse(c.Params("key"))
	wlSettingsMu.RUnlock()
	return c.Status(200).JSON(resp)
}

// SetWirelessSettings updates thresholds. Body:
//
//	{"scope":"global"|"site","degradedAt":1,"downAt":0,"txRetryPct":20,"clear":false}
//
// scope=global edits the default; scope=site edits this key's override, and
// clear=true removes it so the site falls back to the global default.
func SetWirelessSettings(c *fiber.Ctx) error {
	ensureWirelessSettingsLoaded()
	key := c.Params("key")
	var body struct {
		Scope      string  `json:"scope"`
		DegradedAt int     `json:"degradedAt"`
		DownAt     int     `json:"downAt"`
		TxRetryPct float64 `json:"txRetryPct"`
		Clear      bool    `json:"clear"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return c.Status(400).SendString("invalid body")
	}
	t := clampWireless(wirelessThresholds{DegradedAt: body.DegradedAt, DownAt: body.DownAt, TxRetryPct: body.TxRetryPct})
	wlSettingsMu.Lock()
	switch {
	case body.Scope == "global":
		wlSettingsGlobal = t
	case body.Clear:
		delete(wlSettingsOverride, key)
	default:
		wlSettingsOverride[key] = t
	}
	persistWirelessSettings()
	resp := wirelessSettingsResponse(key)
	wlSettingsMu.Unlock()
	return c.Status(200).JSON(resp)
}

// SetWirelessIgnored counts one AP in or out of a site's health. Body:
//
//	{"id":"a1:b2:c3:d4:e5:f6","ignored":true}
//
// id is the AP's MAC, or its name when the snapshot carries no MAC.
func SetWirelessIgnored(c *fiber.Ctx) error {
	ensureWirelessSettingsLoaded()
	key := c.Params("key")
	var body struct {
		ID      string `json:"id"`
		Ignored bool   `json:"ignored"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil || strings.TrimSpace(body.ID) == "" {
		return c.Status(400).SendString(`invalid body: expected {"id":"<mac or name>","ignored":true|false}`)
	}
	id := strings.ToLower(strings.TrimSpace(body.ID))
	wlSettingsMu.Lock()
	list := []string{}
	for _, existing := range wlIgnored[key] {
		if existing != id {
			list = append(list, existing)
		}
	}
	if body.Ignored {
		list = append(list, id)
	}
	if list = normaliseIDs(list); len(list) > 0 {
		wlIgnored[key] = list
	} else {
		delete(wlIgnored, key)
	}
	persistWirelessSettings()
	resp := wirelessSettingsResponse(key)
	wlSettingsMu.Unlock()
	return c.Status(200).JSON(resp)
}
