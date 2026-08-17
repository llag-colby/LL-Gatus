package api

import (
	"sync"

	"github.com/TwiN/gatus/v5/monitoring"
	"github.com/gofiber/fiber/v2"
)

// Force-sweep requests. The phones drill-in can ask the collector to run an
// immediate sweep instead of waiting out its jittered loop. The UI POSTs to
// /v1/phones/:key/sweep; the collector claims pending requests each short poll
// (GET /v1/phones/sweep-pending, which clears them) and, if any are pending,
// sweeps right away. Purely in-memory — a missed request just means the next
// scheduled sweep picks things up anyway.
var (
	sweepMu      sync.Mutex
	sweepPending = map[string]bool{}
)

// RequestPhonesSweep marks a phones endpoint for an immediate sweep. The
// collector re-reports every location each sweep, so any pending key triggers a
// full sweep; the key is mainly a UI-facing acknowledgement.
func RequestPhonesSweep(c *fiber.Ctx) error {
	key := c.Params("key")
	// Refuse while monitoring is paused. The sweep would run, but its inventory
	// and result pushes are both discarded for a paused key, so the UI would say
	// "sweep requested" and then nothing would ever change on screen.
	if monitoring.IsPaused(key) {
		return c.Status(409).JSON(fiber.Map{"error": "monitoring is paused for this endpoint; resume it to sweep"})
	}
	sweepMu.Lock()
	sweepPending[key] = true
	sweepMu.Unlock()
	return c.Status(200).JSON(fiber.Map{"ok": true, "key": key})
}

// ClaimPhonesSweeps returns the pending sweep keys and clears the set. Called by
// the collector each poll; a non-empty result makes it sweep now.
func ClaimPhonesSweeps(c *fiber.Ctx) error {
	sweepMu.Lock()
	pending := make([]string, 0, len(sweepPending))
	for k := range sweepPending {
		pending = append(pending, k)
	}
	sweepPending = map[string]bool{}
	sweepMu.Unlock()
	return c.Status(200).JSON(fiber.Map{"pending": pending})
}
