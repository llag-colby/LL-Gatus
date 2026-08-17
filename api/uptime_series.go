package api

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/TwiN/gatus/v5/storage/store"
	"github.com/TwiN/gatus/v5/storage/store/common"
	"github.com/gofiber/fiber/v2"
)

const (
	// uptimeSeriesSupportedRanges is the list of values accepted by the range query parameter, in the order in which
	// they're advertised to the client when an unsupported value is provided
	uptimeSeriesSupportedRanges = "1h, 6h, 24h, 7d, 30d"

	// resolutionHour is returned when every bucket in the series is hourly
	resolutionHour = "hour"

	// resolutionDay is returned when every bucket in the series is daily
	resolutionDay = "day"

	// resolutionMixed is returned when the series contains both hourly and daily buckets, which happens on ranges
	// long enough to reach entries that have been merged into daily entries
	resolutionMixed = "mixed"
)

// GetUptimeSeries returns the uptime of an endpoint over the requested range, one point per stored uptime bucket
//
// Uptime buckets are hourly for roughly the last 48 hours and daily beyond that, because the storage layer merges
// older hourly entries into daily entries. The series is therefore not guaranteed to have a constant resolution,
// which is why the resolution is returned alongside the data instead of being inferred from the range.
func GetUptimeSeries(c *fiber.Ctx) error {
	requestedRange := c.Query("range")
	now := time.Now()
	var from time.Time
	switch requestedRange {
	// Buckets are keyed by the timestamp at which they start, which means the bucket that a range starts in the
	// middle of starts before the range does and would be filtered out, silently shortening the window by up to one
	// bucket. To avoid that, the beginning of the range is floored to the width of the buckets it will land on:
	// the hour for ranges recent enough to be entirely hourly, and the day for ranges old enough to have been merged
	// into daily entries. This is the same allowance that api/raw.go makes for 1h, generalized to every range.
	case "1h":
		from = now.Truncate(time.Hour).Add(-time.Hour)
	case "6h":
		from = now.Truncate(time.Hour).Add(-6 * time.Hour)
	case "24h":
		from = now.Truncate(time.Hour).Add(-24 * time.Hour)
	case "7d":
		from = floorAtDay(now.Add(-7 * 24 * time.Hour))
	case "30d":
		from = floorAtDay(now.Add(-30 * 24 * time.Hour))
	default:
		return c.Status(400).JSON(fiber.Map{"error": "Ranges supported: " + uptimeSeriesSupportedRanges})
	}
	key, err := url.QueryUnescape(c.Params("key"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid key encoding"})
	}
	uptimeBuckets, err := store.Get().GetUptimeBucketsByKey(key, from, now)
	if err != nil {
		if errors.Is(err, common.ErrEndpointNotFound) {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		if errors.Is(err, common.ErrInvalidTimeRange) {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	// An endpoint that exists but has no bucket yet is a normal state, not an error, so it returns empty arrays
	timestamps := make([]int64, 0, len(uptimeBuckets))
	values := make([]interface{}, 0, len(uptimeBuckets))
	executions := make([]int, 0, len(uptimeBuckets))
	hasHourlyBucket, hasDailyBucket := false, false
	for _, uptimeBucket := range uptimeBuckets {
		timestamps = append(timestamps, uptimeBucket.Timestamp*1000)
		if uptimeBucket.TotalExecutions > 0 {
			values = append(values, float64(uptimeBucket.SuccessfulExecutions)/float64(uptimeBucket.TotalExecutions))
		} else {
			// A bucket without execution has no uptime to report, so it's returned as null to let the chart draw a
			// gap rather than an outage that never happened
			values = append(values, nil)
		}
		executions = append(executions, uptimeBucket.TotalExecutions)
		if isFlooredAtDay(uptimeBucket.Timestamp) {
			hasDailyBucket = true
		} else {
			hasHourlyBucket = true
		}
	}
	return c.Status(http.StatusOK).JSON(fiber.Map{
		"key":        key,
		"range":      requestedRange,
		"resolution": resolutionOf(hasHourlyBucket, hasDailyBucket),
		"timestamps": timestamps,
		"values":     values,
		"executions": executions,
	})
}

// resolutionOf returns the resolution of a series based on the buckets it actually contains
//
// An empty series has no resolution to derive, in which case the resolution of the buckets that would have been
// stored is returned, which is hourly.
func resolutionOf(hasHourlyBucket, hasDailyBucket bool) string {
	if hasHourlyBucket && hasDailyBucket {
		return resolutionMixed
	}
	if hasDailyBucket {
		return resolutionDay
	}
	return resolutionHour
}

// isFlooredAtDay returns whether a bucket timestamp is aligned on a day rather than on an hour, which is how a
// bucket that has been merged into a daily entry is distinguished from an hourly one
//
// The merge floors timestamps in the local location, so the comparison is made in the local location as well. Note
// that an hourly bucket that happens to start at midnight is indistinguishable from a daily bucket, which only
// matters for the hour following midnight and only affects the reported resolution, not the values.
func isFlooredAtDay(timestamp int64) bool {
	t := time.Unix(timestamp, 0)
	return t.Equal(floorAtDay(t))
}

// floorAtDay returns the beginning of the day during which the given time takes place, in the same way that the
// merge of hourly uptime entries into daily uptime entries does
func floorAtDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
