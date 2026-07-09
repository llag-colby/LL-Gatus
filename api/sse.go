package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/TwiN/gatus/v5/storage/store"
	"github.com/TwiN/gatus/v5/storage/store/common/paging"
	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

const (
	// How often the broadcaster reads the latest statuses and (if changed)
	// pushes them to every connected client. Checks run on their own interval;
	// this only bounds how quickly a change reaches the screens.
	sseBroadcastInterval = 3 * time.Second
	// Number of results per endpoint/suite to include (matches the frontend).
	sseResultsPerPage = 50
	// Heartbeat keeps the connection alive and lets us detect disconnects.
	sseHeartbeatInterval = 20 * time.Second
)

// liveSnapshot is the payload pushed to every client. Same shape the REST
// endpoints return, combined so all screens update atomically and in sync.
type liveSnapshot struct {
	Endpoints interface{} `json:"endpoints"`
	Suites    interface{} `json:"suites"`
}

// sseHub is a single broadcaster shared by all connected clients, so every
// screen receives the exact same snapshot at the same moment.
type sseHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
	latest  []byte
}

func newSSEHub() *sseHub {
	h := &sseHub{clients: make(map[chan []byte]struct{})}
	go h.run()
	return h
}

func buildSnapshot() ([]byte, error) {
	endpoints, err := store.Get().GetAllEndpointStatuses(paging.NewEndpointStatusParams().WithResults(1, sseResultsPerPage))
	if err != nil {
		return nil, err
	}
	suites, err := store.Get().GetAllSuiteStatuses(paging.NewSuiteStatusParams().WithPagination(1, sseResultsPerPage))
	if err != nil {
		return nil, err
	}
	return json.Marshal(liveSnapshot{Endpoints: endpoints, Suites: suites})
}

func (h *sseHub) run() {
	if data, err := buildSnapshot(); err == nil {
		h.mu.Lock()
		h.latest = data
		h.mu.Unlock()
	}
	ticker := time.NewTicker(sseBroadcastInterval)
	defer ticker.Stop()
	for range ticker.C {
		data, err := buildSnapshot()
		if err != nil {
			logr.Errorf("[api.sseHub] Failed to build snapshot: %s", err.Error())
			continue
		}
		h.mu.Lock()
		changed := !bytes.Equal(data, h.latest)
		h.latest = data
		if changed {
			for ch := range h.clients {
				// Non-blocking send: never let one slow client stall the hub.
				select {
				case ch <- data:
				default:
				}
			}
		}
		h.mu.Unlock()
	}
}

func (h *sseHub) register(ch chan []byte) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[ch] = struct{}{}
	return h.latest
}

func (h *sseHub) unregister(ch chan []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

// Handler streams live status snapshots to the client over SSE.
func (h *sseHub) Handler(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	ch := make(chan []byte, 8)
	latest := h.register(ch)
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer h.unregister(ch)
		writeEvent := func(payload []byte) bool {
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return false
			}
			return w.Flush() == nil
		}
		// Send the current snapshot immediately so a new screen is instantly in sync.
		if latest != nil && !writeEvent(latest) {
			return
		}
		heartbeat := time.NewTicker(sseHeartbeatInterval)
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
