package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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

// GetJiraDaily returns the IT Daily Snapshot: today / yesterday / month-to-date
// KPIs graded against targets, broken down by location and by technician.
//
// Answers at once from cache. The pass pages a month and a half of resolved
// tickets, which is far longer than the server's 15s WriteTimeout allows, so
// it computes in the background; ?refresh=1 forces a recompute.
func GetJiraDaily(c *fiber.Ctx) error {
	return c.Status(200).JSON(jira.GetDaily(c.Query("refresh") == "1"))
}

// GetJiraBreakdown returns per-assignee ticket counts across five time windows
// for the team dashboard. The result is cached in the jira package, so a wall
// display refreshing this page does not re-page the whole of last month out of
// Jira; ?refresh=1 forces a recompute.
//
// Always 200: the payload's configured/ok/error fields carry the failure, which
// keeps the UI from having to handle an HTTP error path of its own.
func GetJiraBreakdown(c *fiber.Ctx) error {
	// Returns at once, always. A cold pass over five windows per project takes
	// far longer than the server's 15s WriteTimeout, so the counting happens in
	// the background and this hands back whatever is cached, with `computing`
	// set while a pass is running.
	return c.Status(200).JSON(jira.GetBreakdown(c.Query("refresh") == "1"))
}

// GetJiraBoards lists the agile boards the configured account can see, so the
// Kanban tab can offer a board picker grouped by project.
func GetJiraBoards(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return c.Status(200).JSON(jira.ListBoards(ctx))
}

// GetJiraBoard returns one board: its columns exactly as configured in Jira
// (order, status mapping, WIP constraints) with the current cards placed in them.
// Always 200 on a valid id; the payload's `ok`/`error` carry the failure.
func GetJiraBoard(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil || id <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "board id must be a positive integer"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return c.Status(200).JSON(jira.GetBoard(ctx, id))
}

// JiraBoardLive streams one board over SSE. Subscribing starts an on-demand
// poller for that board; it shuts down once nothing has watched it for a while,
// so boards nobody is looking at cost nothing.
func JiraBoardLive(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil || id <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "board id must be a positive integer"})
	}
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	ch := jira.SubscribeBoard(id)
	initial, _ := json.Marshal(jira.CachedBoard(id))
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer jira.UnsubscribeBoard(id, ch)
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
