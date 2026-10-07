package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/TwiN/gatus/v5/sentinelone"
	"github.com/gofiber/fiber/v2"
)

// GetS1Metrics returns the latest cached SentinelOne snapshot polled by the
// background poller. Always 200: the payload's `configured`/`ok` fields tell
// the UI whether SentinelOne is set up and whether the last refresh succeeded.
func GetS1Metrics(c *fiber.Ctx) error {
	return c.Status(200).JSON(sentinelone.GetSnapshot())
}

// GetS1Threat fetches one threat's detail on demand for the drill-down panel
// (full threat record plus its activity timeline).
func GetS1Threat(c *fiber.Ctx) error {
	id := c.Params("id")
	if id == "" {
		return c.Status(400).JSON(fiber.Map{"error": "threat id is required"})
	}
	// Generous: this API's latency has been measured spiking to 24s for a
	// single call, so a conventional timeout here would fail on a slow console
	// rather than on a real problem.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	return c.Status(200).JSON(sentinelone.FetchThreat(ctx, id))
}

// S1Live streams SentinelOne snapshots to the browser over SSE, so an open
// dashboard updates the moment a poll completes rather than waiting on its own
// timer.
func S1Live(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	ch := sentinelone.Subscribe()
	initial, _ := json.Marshal(sentinelone.GetSnapshot())
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer sentinelone.Unsubscribe(ch)
		writeEvent := func(payload []byte) bool {
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return false
			}
			return w.Flush() == nil
		}
		if initial != nil && !writeEvent(initial) {
			return
		}
		heartbeat := time.NewTicker(20 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case msg, ok := <-ch:
				if !ok || !writeEvent(msg) {
					return
				}
			case <-heartbeat.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
				if w.Flush() != nil {
					return
				}
			}
		}
	})
	return nil
}
