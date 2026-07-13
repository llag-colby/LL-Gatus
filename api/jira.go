package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/TwiN/gatus/v5/jira"
	"github.com/gofiber/fiber/v2"
)

// GetJiraMetrics returns the latest cached Jira snapshot polled by the background
// jira poller. Always 200: the payload's `configured`/`ok` fields tell the UI
// whether Jira is set up and whether the last refresh succeeded.
func GetJiraMetrics(c *fiber.Ctx) error {
	return c.Status(200).JSON(jira.GetSnapshot())
}

// GetJiraIssue fetches a single ticket's detail on demand for the drill-down
// panel (description, reporter, SLA remaining time, recent comments).
func GetJiraIssue(c *fiber.Ctx) error {
	key := c.Params("key")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	detail, err := jira.FetchIssue(ctx, key)
	if err != nil {
		return c.Status(502).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(200).JSON(detail)
}

// JiraLive streams Jira snapshots to the browser over SSE. The poller pushes a
// new snapshot on every refresh, so open dashboards update the instant a poll
// completes (new tickets, status/comment changes, SLA movement) rather than
// waiting on their own timer.
func JiraLive(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	ch := jira.Subscribe()
	initial, _ := json.Marshal(jira.GetSnapshot())
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer jira.Unsubscribe(ch)
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
