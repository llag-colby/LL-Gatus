package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/TwiN/gatus/v5/history"
	"github.com/gofiber/fiber/v2"
)

// Metric history for the collector side-channels. The phones and UniFi snapshots
// are ephemeral by design (the collector re-reports every sweep and only the last
// one is kept), which is fine for "what is happening now" but leaves no way to
// answer "how many phones were offline last night". The history package persists
// the numeric counts out of those pushes; this file is the write hook shared by
// both collectors, and the read endpoint the charts call.

// recordCounts persists every numeric field of a collector's `counts` object.
//
// Fields are extracted generically rather than by name, so that any count a
// collector starts sending is charted with no change here. Only JSON numbers are
// kept: nulls, strings and booleans are skipped, which matters because wireless
// reports txRetryPct as null when the controller does not expose it, and charting
// that as 0 would draw a dip that never happened.
func recordCounts(endpointKey string, counts json.RawMessage) {
	if len(counts) == 0 {
		return
	}
	var decoded map[string]any
	if err := json.Unmarshal(counts, &decoded); err != nil {
		return // counts is optional and free-form; a non-object simply has nothing to record
	}
	metrics := make(map[string]float64, len(decoded))
	for name, value := range decoded {
		if number, ok := value.(float64); ok {
			metrics[name] = number
		}
	}
	history.Record(endpointKey, metrics, time.Now())
}

// GetMetricHistory returns the recorded history of every metric known for one
// endpoint key, at whichever resolution covers the requested range.
//
// An endpoint with nothing recorded yet returns 200 with an empty series map
// rather than 404: a collector that has only just started pushing is a normal
// state the UI renders as an empty chart, not an error it has to special-case.
func GetMetricHistory(c *fiber.Ctx) error {
	now := time.Now()
	var from time.Time
	switch c.Query("range") {
	case "30d":
		from = now.Add(-30 * 24 * time.Hour)
	case "7d":
		from = now.Add(-7 * 24 * time.Hour)
	case "24h":
		from = now.Add(-24 * time.Hour)
	case "6h":
		from = now.Add(-6 * time.Hour)
	case "1h":
		from = now.Add(-1 * time.Hour)
	default:
		return c.Status(400).SendString("Ranges supported: 1h, 6h, 24h, 7d, 30d")
	}
	key, err := url.QueryUnescape(c.Params("key"))
	if err != nil {
		return c.Status(400).SendString("invalid key encoding")
	}
	series, resolution := history.Query(key, from, now)
	return c.Status(http.StatusOK).JSON(fiber.Map{
		"key":        key,
		"range":      c.Query("range"),
		"resolution": resolution,
		"from":       from.UnixMilli(),
		"to":         now.UnixMilli(),
		"series":     series,
	})
}
