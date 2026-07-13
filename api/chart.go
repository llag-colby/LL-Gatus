package api

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/TwiN/gatus/v5/storage/store"
	"github.com/TwiN/gatus/v5/storage/store/common"
	"github.com/TwiN/gatus/v5/storage/store/common/paging"
	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
	"github.com/wcharczuk/go-chart/v2"
	"github.com/wcharczuk/go-chart/v2/drawing"
)

const timeFormat = "3:04PM"

var (
	gridStyle = chart.Style{
		StrokeColor: drawing.Color{R: 119, G: 119, B: 119, A: 40},
		StrokeWidth: 1.0,
	}
	axisStyle = chart.Style{
		FontColor: drawing.Color{R: 119, G: 119, B: 119, A: 255},
	}
	transparentStyle = chart.Style{
		FillColor: drawing.Color{R: 255, G: 255, B: 255, A: 0},
	}
)

func ResponseTimeChart(c *fiber.Ctx) error {
	duration := c.Params("duration")
	chartTimestampFormatter := chart.TimeValueFormatterWithFormat(timeFormat)
	var from time.Time
	switch duration {
	case "30d":
		from = time.Now().Truncate(time.Hour).Add(-30 * 24 * time.Hour)
		chartTimestampFormatter = chart.TimeDateValueFormatter
	case "7d":
		from = time.Now().Truncate(time.Hour).Add(-7 * 24 * time.Hour)
	case "24h":
		from = time.Now().Truncate(time.Hour).Add(-24 * time.Hour)
	default:
		return c.Status(400).SendString("Durations supported: 30d, 7d, 24h")
	}
	key, err := url.QueryUnescape(c.Params("key"))
	if err != nil {
		return c.Status(400).SendString("invalid key encoding")
	}
	hourlyAverageResponseTime, err := store.Get().GetHourlyAverageResponseTimeByKey(key, from, time.Now())
	if err != nil {
		if errors.Is(err, common.ErrEndpointNotFound) {
			return c.Status(404).SendString(err.Error())
		} else if errors.Is(err, common.ErrInvalidTimeRange) {
			return c.Status(400).SendString(err.Error())
		}
		return c.Status(500).SendString(err.Error())
	}
	if len(hourlyAverageResponseTime) == 0 {
		return c.Status(204).SendString("")
	}
	series := chart.TimeSeries{
		Name: "Average response time per hour",
		Style: chart.Style{
			StrokeWidth: 1.5,
			DotWidth:    2.0,
		},
	}
	keys := make([]int, 0, len(hourlyAverageResponseTime))
	earliestTimestamp := int64(0)
	for hourlyTimestamp := range hourlyAverageResponseTime {
		keys = append(keys, int(hourlyTimestamp))
		if earliestTimestamp == 0 || hourlyTimestamp < earliestTimestamp {
			earliestTimestamp = hourlyTimestamp
		}
	}
	for earliestTimestamp > from.Unix() {
		earliestTimestamp -= int64(time.Hour.Seconds())
		keys = append(keys, int(earliestTimestamp))
	}
	sort.Ints(keys)
	var maxAverageResponseTime float64
	for _, key := range keys {
		averageResponseTime := float64(hourlyAverageResponseTime[int64(key)])
		if maxAverageResponseTime < averageResponseTime {
			maxAverageResponseTime = averageResponseTime
		}
		series.XValues = append(series.XValues, time.Unix(int64(key), 0))
		series.YValues = append(series.YValues, averageResponseTime)
	}
	graph := chart.Chart{
		Canvas:     transparentStyle,
		Background: transparentStyle,
		Width:      1280,
		Height:     300,
		XAxis: chart.XAxis{
			ValueFormatter: chartTimestampFormatter,
			GridMajorStyle: gridStyle,
			GridMinorStyle: gridStyle,
			Style:          axisStyle,
			NameStyle:      axisStyle,
		},
		YAxis: chart.YAxis{
			Name:           "Average response time",
			GridMajorStyle: gridStyle,
			GridMinorStyle: gridStyle,
			Style:          axisStyle,
			NameStyle:      axisStyle,
			Range: &chart.ContinuousRange{
				Min: 0,
				Max: math.Ceil(maxAverageResponseTime * 1.25),
			},
		},
		Series: []chart.Series{series},
	}
	c.Set("Content-Type", "image/svg+xml")
	c.Set("Cache-Control", "no-cache, no-store")
	c.Set("Expires", "0")
	c.Status(http.StatusOK)
	if err := graph.Render(chart.SVG, c); err != nil {
		logr.Errorf("[api.ResponseTimeChart] Failed to render response time chart: %s", err.Error())
		return c.Status(500).SendString(err.Error())
	}
	return nil
}

func ResponseTimeHistory(c *fiber.Ctx) error {
	duration := c.Params("duration")
	var from time.Time
	// Short windows return every individual check (raw), so the chart shows one
	// dot per ping instead of a couple of hourly averages. Long windows stay
	// hourly-averaged: raw isn't retained that far back and would be tens of
	// thousands of points.
	raw := false
	switch duration {
	case "30d":
		from = time.Now().Truncate(time.Hour).Add(-30 * 24 * time.Hour)
	case "7d":
		from = time.Now().Truncate(time.Hour).Add(-7 * 24 * time.Hour)
	case "2d":
		from = time.Now().Add(-2 * 24 * time.Hour)
		raw = true
	case "24h":
		from = time.Now().Add(-24 * time.Hour)
		raw = true
	case "16h":
		from = time.Now().Add(-16 * time.Hour)
		raw = true
	case "5h":
		from = time.Now().Add(-5 * time.Hour)
		raw = true
	case "1h":
		from = time.Now().Add(-1 * time.Hour)
		raw = true
	default:
		return c.Status(400).SendString("Durations supported: 30d, 7d, 2d, 24h, 16h, 5h, 1h")
	}
	endpointKey, err := url.QueryUnescape(c.Params("key"))
	if err != nil {
		return c.Status(400).SendString("invalid key encoding")
	}
	if raw {
		return responseTimeRawHistory(c, endpointKey, from)
	}
	hourlyAverageResponseTime, err := store.Get().GetHourlyAverageResponseTimeByKey(endpointKey, from, time.Now())
	if err != nil {
		if errors.Is(err, common.ErrEndpointNotFound) {
			return c.Status(404).SendString(err.Error())
		}
		if errors.Is(err, common.ErrInvalidTimeRange) {
			return c.Status(400).SendString(err.Error())
		}
		return c.Status(500).SendString(err.Error())
	}
	if len(hourlyAverageResponseTime) == 0 {
		return c.Status(200).JSON(map[string]interface{}{
			"timestamps": []int64{},
			"values":     []int{},
		})
	}
	hourlyTimestamps := make([]int, 0, len(hourlyAverageResponseTime))
	earliestTimestamp := int64(0)
	for hourlyTimestamp := range hourlyAverageResponseTime {
		hourlyTimestamps = append(hourlyTimestamps, int(hourlyTimestamp))
		if earliestTimestamp == 0 || hourlyTimestamp < earliestTimestamp {
			earliestTimestamp = hourlyTimestamp
		}
	}
	for earliestTimestamp > from.Unix() {
		earliestTimestamp -= int64(time.Hour.Seconds())
		hourlyTimestamps = append(hourlyTimestamps, int(earliestTimestamp))
	}
	sort.Ints(hourlyTimestamps)
	timestamps := make([]int64, 0, len(hourlyTimestamps))
	values := make([]int, 0, len(hourlyTimestamps))
	for _, hourlyTimestamp := range hourlyTimestamps {
		timestamp := int64(hourlyTimestamp)
		averageResponseTime := hourlyAverageResponseTime[timestamp]
		timestamps = append(timestamps, timestamp*1000)
		values = append(values, averageResponseTime)
	}
	return c.Status(http.StatusOK).JSON(map[string]interface{}{
		"timestamps": timestamps,
		"values":     values,
	})
}

// responseTimeRawHistory returns per-check (not hourly-averaged) response times
// within [from, now]. This is what makes short ranges show every individual ping
// instead of a couple of hourly points. Failed checks are emitted as null so the
// chart renders them as a gap, matching the live view. The number of points is
// bounded by storage.maximum-number-of-results.
func responseTimeRawHistory(c *fiber.Ctx, endpointKey string, from time.Time) error {
	// 4000 comfortably covers the widest raw window (2 days at a 60s interval =
	// 2880 checks); the store returns at most maximum-number-of-results anyway.
	status, err := store.Get().GetEndpointStatusByKey(endpointKey, paging.NewEndpointStatusParams().WithResults(1, 4000))
	if err != nil {
		if errors.Is(err, common.ErrEndpointNotFound) {
			return c.Status(404).SendString(err.Error())
		}
		if errors.Is(err, common.ErrInvalidTimeRange) {
			return c.Status(400).SendString(err.Error())
		}
		return c.Status(500).SendString(err.Error())
	}
	timestamps := make([]int64, 0)
	values := make([]interface{}, 0)
	if status != nil {
		results := status.Results
		// Oldest to newest, so the line reads left to right.
		sort.Slice(results, func(i, j int) bool {
			return results[i].Timestamp.Before(results[j].Timestamp)
		})
		for _, r := range results {
			if r.Timestamp.Before(from) {
				continue
			}
			timestamps = append(timestamps, r.Timestamp.UnixMilli())
			if r.Success && r.Duration > 0 {
				values = append(values, r.Duration.Milliseconds())
			} else {
				values = append(values, nil) // failed check → gap
			}
		}
	}
	return c.Status(http.StatusOK).JSON(map[string]interface{}{
		"timestamps": timestamps,
		"values":     values,
	})
}
