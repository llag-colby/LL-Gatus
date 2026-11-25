package api

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func resetWirelessSettings(t *testing.T) {
	t.Helper()
	wirelessSettingsPath = filepath.Join(t.TempDir(), "unifi_wireless_settings.json")
	wlSettingsMu.Lock()
	wlSettingsGlobal = defaultWirelessThresholds
	wlSettingsOverride = map[string]wirelessThresholds{}
	wlIgnored = map[string][]string{}
	wlSettingsLoaded = false
	wlSettingsMu.Unlock()
}

func wirelessApp() *fiber.App {
	app := fiber.New()
	app.Get("/v1/unifi/:key/wireless-settings", GetWirelessSettings)
	app.Post("/v1/unifi/:key/wireless-settings", SetWirelessSettings)
	app.Post("/v1/unifi/:key/wireless-ignored", SetWirelessIgnored)
	return app
}

type wirelessResp struct {
	Effective wirelessThresholds  `json:"effective"`
	Global    wirelessThresholds  `json:"global"`
	Override  *wirelessThresholds `json:"override"`
	Ignored   []string            `json:"ignored"`
}

func callWireless(t *testing.T, app *fiber.App, method, path, body string) (int, wirelessResp) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out wirelessResp
	if res.StatusCode == 200 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
	}
	return res.StatusCode, out
}

func TestWirelessSettings_DefaultsOverrideAndClear(t *testing.T) {
	resetWirelessSettings(t)
	app := wirelessApp()
	const p = "/v1/unifi/wireless_decatur-kia/wireless-settings"

	_, r := callWireless(t, app, "GET", p, "")
	if r.Effective != defaultWirelessThresholds || r.Override != nil || len(r.Ignored) != 0 {
		t.Fatalf("defaults wrong: %+v", r)
	}

	_, r = callWireless(t, app, "POST", p, `{"scope":"site","degradedAt":2,"downAt":4,"txRetryPct":30}`)
	want := wirelessThresholds{DegradedAt: 2, DownAt: 4, TxRetryPct: 30}
	if r.Effective != want || r.Override == nil || r.Global != defaultWirelessThresholds {
		t.Fatalf("site override not applied: %+v", r)
	}

	// Another site still sees the global default.
	_, other := callWireless(t, app, "GET", "/v1/unifi/wireless_alabaster/wireless-settings", "")
	if other.Effective != defaultWirelessThresholds {
		t.Fatalf("override leaked to another site: %+v", other)
	}

	_, r = callWireless(t, app, "POST", p, `{"scope":"site","clear":true}`)
	if r.Override != nil || r.Effective != defaultWirelessThresholds {
		t.Fatalf("clear did not fall back to global: %+v", r)
	}
}

func TestWirelessSettings_Clamp(t *testing.T) {
	for _, tc := range []struct{ in, want wirelessThresholds }{
		{wirelessThresholds{0, 0, 20}, wirelessThresholds{1, 0, 20}},
		{wirelessThresholds{3, 2, 20}, wirelessThresholds{3, 3, 20}},
		{wirelessThresholds{1, -1, -5}, wirelessThresholds{1, 0, 0}},
		{wirelessThresholds{1, 0, 250}, wirelessThresholds{1, 0, 100}},
	} {
		if got := clampWireless(tc.in); got != tc.want {
			t.Errorf("clampWireless(%+v) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestWirelessIgnored_ToggleNormaliseAndPersist(t *testing.T) {
	resetWirelessSettings(t)
	app := wirelessApp()
	const p = "/v1/unifi/wireless_decatur-kia/wireless-ignored"

	if code, _ := callWireless(t, app, "POST", p, `{"id":"  ","ignored":true}`); code != 400 {
		t.Fatalf("blank id accepted: %d", code)
	}
	callWireless(t, app, "POST", p, `{"id":"AA:BB:CC:00:11:22","ignored":true}`)
	_, r := callWireless(t, app, "POST", p, `{"id":"aa:bb:cc:00:11:22","ignored":true}`)
	if len(r.Ignored) != 1 || r.Ignored[0] != "aa:bb:cc:00:11:22" {
		t.Fatalf("ignore not normalised/deduped: %+v", r.Ignored)
	}

	// Survives a reload from disk.
	if _, err := os.Stat(wirelessSettingsPath); err != nil {
		t.Fatalf("not persisted: %v", err)
	}
	wlSettingsMu.Lock()
	wlIgnored = map[string][]string{}
	wlSettingsLoaded = false
	wlSettingsMu.Unlock()
	_, r = callWireless(t, app, "GET", "/v1/unifi/wireless_decatur-kia/wireless-settings", "")
	if len(r.Ignored) != 1 {
		t.Fatalf("ignore lost on reload: %+v", r)
	}

	_, r = callWireless(t, app, "POST", p, `{"id":"AA:BB:CC:00:11:22","ignored":false}`)
	if len(r.Ignored) != 0 {
		t.Fatalf("un-ignore failed: %+v", r.Ignored)
	}
}
