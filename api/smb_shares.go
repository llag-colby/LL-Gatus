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

// SMB share snapshots pushed by collector/smb_collector.py, the same
// side-channel pattern the phones and UniFi collectors use: the
// external-endpoint itself only carries a pass/fail bar, so the detail that
// makes the drill-in worth opening — whether the tree actually mounted, whether
// the root listed, free space, the account used — lands here.
//
// Why this exists at all: Gatus has no SMB client, so the only thing it could
// check natively was tcp/445 on the file server. That proves the service is
// listening and nothing else. A share can be unshared, ACL'd shut, full, or
// replaced by a DFS referral that goes nowhere while port 445 stays wide open,
// which is exactly the outage people actually hit. This collector connects to
// the share, lists its root and reads its free space, so the row means "the
// share works" instead of "the server answers".
//
// Snapshots are ephemeral: the collector re-reports every sweep and nothing is
// persisted. A key that stops reporting goes stale, which the UI shows rather
// than hiding.
var (
	smbMu    sync.RWMutex
	smbStore = make(map[string]storedSMB)
)

type storedSMB struct {
	UpdatedAt string          `json:"updatedAt"`
	Status    string          `json:"status,omitempty"` // healthy | degraded | down
	Drive     string          `json:"drive,omitempty"`  // "L:"
	Path      string          `json:"path,omitempty"`   // \\rr-fs01\Company Hub
	Counts    json.RawMessage `json:"counts,omitempty"`
	Detail    json.RawMessage `json:"detail,omitempty"` // everything else, verbatim
}

// SetSMBSnapshot receives one share's snapshot. Auth reuses the push token
// configured on that external-endpoint, exactly like the UniFi inventory.
func SetSMBSnapshot(cfg *config.Config) fiber.Handler {
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
			Status string          `json:"status"`
			Drive  string          `json:"drive"`
			Path   string          `json:"path"`
			Counts json.RawMessage `json:"counts"`
			Detail json.RawMessage `json:"detail"`
		}
		if err := json.Unmarshal(c.Body(), &payload); err != nil || payload.Status == "" {
			return c.Status(400).SendString(`invalid body: expected {"status":"healthy|degraded|down", ...}`)
		}
		// Paused means paused: freeze the snapshot too, so the drill-in doesn't
		// show live share state on a page that says monitoring is stopped. 200
		// for the same reason the result push returns 200 — a non-200 would make
		// the collector warn on every sweep.
		if monitoring.IsPaused(key) {
			return c.Status(200).SendString("OK (monitoring paused)")
		}
		smbMu.Lock()
		smbStore[key] = storedSMB{
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			Status:    payload.Status,
			Drive:     payload.Drive,
			Path:      payload.Path,
			Counts:    payload.Counts,
			Detail:    payload.Detail,
		}
		smbMu.Unlock()
		// Persist the counts for the charts (free space, listing latency, entry
		// count). After the pause guard on purpose: a paused endpoint records
		// nothing, so its history has a gap rather than a flat line implying it
		// was still being measured.
		recordCounts(key, payload.Counts)
		logr.Infof("[api.SetSMBSnapshot] Stored share snapshot for key=%s status=%s path=%s", key, payload.Status, payload.Path)
		return c.Status(200).SendString("OK")
	}
}

// GetSMBSnapshot returns the last snapshot reported for one endpoint key.
func GetSMBSnapshot(c *fiber.Ctx) error {
	key := c.Params("key")
	smbMu.RLock()
	snap, ok := smbStore[key]
	smbMu.RUnlock()
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "no SMB snapshot reported yet"})
	}
	return c.Status(200).JSON(snap)
}

// GetSMBSnapshots returns every reported snapshot keyed by endpoint key. The
// drill-in calls this once to fill the sibling list, instead of one request per
// share.
func GetSMBSnapshots(c *fiber.Ctx) error {
	smbMu.RLock()
	out := make(map[string]storedSMB, len(smbStore))
	for k, v := range smbStore {
		out[k] = v
	}
	smbMu.RUnlock()
	return c.Status(200).JSON(out)
}
