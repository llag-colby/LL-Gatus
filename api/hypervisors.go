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

// Hyper-V host snapshots pushed by collector/hv_collector.py, the same
// side-channel pattern the phones, UniFi and SMB collectors use: the
// external-endpoint carries only a pass/fail bar, so everything that makes the
// drill-in worth opening lands here.
//
// This payload is much larger than its siblings because a hypervisor is the one
// thing on this dashboard where "is it up" is the least interesting question.
// What an operator actually needs is CPU and memory headroom, how full every
// volume is, and which guests are running, all in one place. Detail is stored
// verbatim as json.RawMessage so the collector can add fields without a Go
// change; only the fields this server actually reasons about are typed.
//
// Snapshots are ephemeral: the collector re-reports every sweep and nothing is
// persisted. A key that stops reporting goes stale, which the UI shows rather
// than hiding.
var (
	hvMu    sync.RWMutex
	hvStore = make(map[string]storedHV)
)

type storedHV struct {
	UpdatedAt string          `json:"updatedAt"`
	Status    string          `json:"status,omitempty"` // healthy | degraded | down
	Host      string          `json:"host,omitempty"`   // MS-HV01
	Site      string          `json:"site,omitempty"`
	Address   string          `json:"address,omitempty"` // the address that answered
	Counts    json.RawMessage `json:"counts,omitempty"`  // numeric, charted via recordCounts
	Detail    json.RawMessage `json:"detail,omitempty"`  // everything else, verbatim
}

// SetHVSnapshot receives one hypervisor's snapshot. Auth reuses the push token
// configured on that external-endpoint, exactly like the other collectors.
func SetHVSnapshot(cfg *config.Config) fiber.Handler {
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
			Status  string          `json:"status"`
			Host    string          `json:"host"`
			Site    string          `json:"site"`
			Address string          `json:"address"`
			Counts  json.RawMessage `json:"counts"`
			Detail  json.RawMessage `json:"detail"`
		}
		if err := json.Unmarshal(c.Body(), &payload); err != nil || payload.Status == "" {
			return c.Status(400).SendString(`invalid body: expected {"status":"healthy|degraded|down", ...}`)
		}
		// Paused means paused: freeze the snapshot too, so the drill-in doesn't
		// show live CPU and VM state on a page that says monitoring is stopped.
		// 200 for the same reason the result push returns 200, so the collector
		// does not warn on every sweep.
		if monitoring.IsPaused(key) {
			return c.Status(200).SendString("OK (monitoring paused)")
		}
		hvMu.Lock()
		hvStore[key] = storedHV{
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			Status:    payload.Status,
			Host:      payload.Host,
			Site:      payload.Site,
			Address:   payload.Address,
			Counts:    payload.Counts,
			Detail:    payload.Detail,
		}
		hvMu.Unlock()
		// Persist the numeric counts for the charts (CPU load, memory used,
		// volume fullness, VM counts). After the pause guard on purpose: a paused
		// endpoint records nothing, so its history shows a gap rather than a flat
		// line implying it was still being measured.
		recordCounts(key, payload.Counts)
		logr.Infof("[api.SetHVSnapshot] Stored hypervisor snapshot for key=%s host=%s status=%s", key, payload.Host, payload.Status)
		return c.Status(200).SendString("OK")
	}
}

// GetHVSnapshot returns the last snapshot reported for one hypervisor key.
func GetHVSnapshot(c *fiber.Ctx) error {
	key := c.Params("key")
	hvMu.RLock()
	snap, ok := hvStore[key]
	hvMu.RUnlock()
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "no hypervisor snapshot reported yet"})
	}
	return c.Status(200).JSON(snap)
}

// GetHVSnapshots returns every reported snapshot keyed by endpoint key. The
// fleet view and the drill-in's sibling list call this once instead of making
// one request per host.
func GetHVSnapshots(c *fiber.Ctx) error {
	hvMu.RLock()
	out := make(map[string]storedHV, len(hvStore))
	for k, v := range hvStore {
		out[k] = v
	}
	hvMu.RUnlock()
	return c.Status(200).JSON(out)
}
