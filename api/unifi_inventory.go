package api

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/TwiN/gatus/v5/config"
	"github.com/TwiN/gatus/v5/monitoring"
	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

// UniFi snapshots pushed by collector/unifi_collector.py, the same side-channel
// pattern the phones collector uses: the external-endpoint itself only carries a
// pass/fail bar, so the rich detail (per-WAN state, gateway health, AP list,
// client counts) lands here.
//
// Two kinds share this store, distinguished by the payload's `kind`:
//
//	firewall — the site gateway and its WAN uplinks (the "Firewall" row)
//	wireless — the site's access points and clients (the "Wireless" row)
//
// Snapshots are ephemeral: the collector re-reports every sweep, and nothing is
// persisted. A key that stops reporting simply goes stale, which the UI shows
// rather than hiding.
var (
	unifiMu    sync.RWMutex
	unifiStore = make(map[string]storedUniFi)
)

type storedUniFi struct {
	UpdatedAt string          `json:"updatedAt"`
	Kind      string          `json:"kind,omitempty"`   // firewall | wireless
	Status    string          `json:"status,omitempty"` // healthy | degraded | down
	Site      string          `json:"site,omitempty"`
	Counts    json.RawMessage `json:"counts,omitempty"`
	Detail    json.RawMessage `json:"detail,omitempty"` // everything else, verbatim
}

// SetUniFiSnapshot receives one site's UniFi snapshot. Auth reuses the push token
// configured on that external-endpoint, exactly like the phones inventory.
func SetUniFiSnapshot(cfg *config.Config) fiber.Handler {
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
			Kind   string          `json:"kind"`
			Status string          `json:"status"`
			Site   string          `json:"site"`
			Counts json.RawMessage `json:"counts"`
			Detail json.RawMessage `json:"detail"`
		}
		if err := json.Unmarshal(c.Body(), &payload); err != nil || payload.Kind == "" {
			return c.Status(400).SendString(`invalid body: expected {"kind":"firewall|wireless", ...}`)
		}
		// Paused means paused: freeze the snapshot too, so the drill-in doesn't show
		// live AP and WAN state on a page that says monitoring is stopped. 200 for
		// the same reason the result push returns 200 — a non-200 would make the
		// collector warn on every sweep.
		if monitoring.IsPaused(key) {
			return c.Status(200).SendString("OK (monitoring paused)")
		}
		unifiMu.Lock()
		unifiStore[key] = storedUniFi{
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			Kind:      payload.Kind,
			Status:    payload.Status,
			Site:      payload.Site,
			Counts:    payload.Counts,
			Detail:    payload.Detail,
		}
		unifiMu.Unlock()
		// Persist the counts for the charts. After the pause guard on purpose: a
		// paused endpoint records nothing, so its history has a gap rather than a
		// flat line implying it was still being measured.
		recordCounts(key, payload.Counts)
		logr.Infof("[api.SetUniFiSnapshot] Stored %s snapshot for key=%s status=%s", payload.Kind, key, payload.Status)
		return c.Status(200).SendString("OK")
	}
}

// GetUniFiSnapshot returns the last snapshot reported for one endpoint key.
func GetUniFiSnapshot(c *fiber.Ctx) error {
	key := c.Params("key")
	unifiMu.RLock()
	snap, ok := unifiStore[key]
	unifiMu.RUnlock()
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "no UniFi snapshot reported yet"})
	}
	return c.Status(200).JSON(snap)
}

// GetUniFiSnapshots returns every reported snapshot keyed by endpoint key. The
// dashboard calls this once per refresh to fill the trailing metrics on every
// card's Firewall and Wireless rows — one request for the whole page instead of
// two per location.
func GetUniFiSnapshots(c *fiber.Ctx) error {
	unifiMu.RLock()
	out := make(map[string]storedUniFi, len(unifiStore))
	for k, v := range unifiStore {
		out[k] = v
	}
	unifiMu.RUnlock()
	return c.Status(200).JSON(out)
}
