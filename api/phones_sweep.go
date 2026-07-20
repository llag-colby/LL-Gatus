package api

import (
	"sync"

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
