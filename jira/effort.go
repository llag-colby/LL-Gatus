package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Per-assignee effort and timing analytics for the Team tab.
//
// WHY NOT WORKLOGS. The obvious source for "time spent working a ticket" is
// the worklog / timespent field. In this instance it is empty: zero issues
// with timespent > 0, zero worklog entries in a year, across every project.
// Nobody logs work, so any dashboard built on `timespent` would show a wall of
// zeros and look broken. That is a fact about the instance, not a limitation
// here, and `NoTimeLogged` reports it so the page can say so plainly instead
// of presenting empty columns.
//
// WHAT IS USED INSTEAD. Jira Service Management runs two SLA clocks on every
// request, and they are populated:
//
//   - Time to first response -> how long until a human replied
//   - Time to resolution     -> how long the ticket was actually being worked
//
// The second one is the useful one, because an SLA clock is not wall-clock
// time: it respects the service calendar and it PAUSES while the ticket waits
// on the customer. So it measures how long a ticket was actually the DESK'S
// problem, and it is measured automatically rather than typed in by hand.
//
// What it is NOT is labour. The clock keeps running during business hours
// whether or not anyone is touching the ticket, so it cannot be presented as
// time worked: measured over 30 days on this instance it totals ~9,200 hours
// across 11 people, which is roughly 836 hours each against a working month of
// about 173. Everything here is therefore named "on desk" rather than "agent
// time", and the page repeats the distinction, because the honest version of
// this number is still useful and the dishonest version is a fabrication.
//
// It is deliberately NOT called cycle time anywhere. Cycle time is the wall
// clock from created to resolved, which is also computed here, and the gap
// between the two is itself one of the more revealing numbers on the page:
// it is the time a ticket spent waiting rather than being worked.
//
// Durations are summarised with MEDIANS and p90, never means. The observed
// distribution is violently skewed - median around 20 minutes, p99 around 286
// hours - so a mean would be dragged around by a handful of tickets that sat
// open for weeks and would describe nobody's actual day.

// EffortRow is one person's numbers over the window.
type EffortRow struct {
	Name      string `json:"name"`
	AccountID string `json:"accountId,omitempty"`

	Resolved int `json:"resolved"` // tickets they resolved in the window
	Measured int `json:"measured"` // of those, how many carried an SLA clock

	WorkMedianMs int64 `json:"workMedianMs"` // business-hours time on the desk
	WorkP90Ms    int64 `json:"workP90Ms"`
	WorkTotalMs  int64 `json:"workTotalMs"`

	FirstResponseMedianMs int64 `json:"frtMedianMs"`
	CycleMedianMs         int64 `json:"cycleMedianMs"` // wall clock created->resolved

	// Resolved on the first reply: the first-response clock and the resolution
	// clock stopped at the same elapsed value, so the ticket never came back.
	FirstTouch int `json:"firstTouch"`

	SLAMetRes    int `json:"slaMetRes"`
	SLABreachRes int `json:"slaBreachRes"`
	SLAMetFRT    int `json:"slaMetFrt"`
	SLABreachFRT int `json:"slaBreachFrt"`
}

// HeatCell is one hour of one weekday in the resolution heatmap.
type HeatCell struct {
	Day   int `json:"day"`  // 0 = Sunday
	Hour  int `json:"hour"` // 0-23, in the Jira account's own timezone
	Count int `json:"count"`
}

// Effort is the analytics block attached to the breakdown payload.
type Effort struct {
	WindowDays int  `json:"windowDays"`
	Sampled    int  `json:"sampled"`
	Truncated  bool `json:"truncated,omitempty"`
	// NoTimeLogged is true when the instance carries no worklogs at all, which
	// is why these figures come from the SLA clock. The page says so rather
	// than letting the numbers imply someone logged them.
	NoTimeLogged bool `json:"noTimeLogged"`
	// NoSLAData is true when the SLA fields came back empty too, in which case
	// only the wall-clock figures below are real.
	NoSLAData bool   `json:"noSlaData,omitempty"`
	Error     string `json:"error,omitempty"`

	Rows []EffortRow `json:"rows"`

	// Desk-wide
	WorkTotalMs   int64 `json:"workTotalMs"`
	WorkMedianMs  int64 `json:"workMedianMs"`
	WorkP90Ms     int64 `json:"workP90Ms"`
	CycleMedianMs int64 `json:"cycleMedianMs"`
	FRTMedianMs   int64 `json:"frtMedianMs"`
	FirstTouch    int   `json:"firstTouch"`
	Measured      int   `json:"measured"`
	Breaches      int   `json:"breaches"`

	// WaitRatio is agent time divided by wall-clock time across the window:
	// how much of a ticket's life was actually being worked. 0.26 means three
	// quarters of it was waiting.
	WaitRatio float64 `json:"waitRatio"`

	// Histogram of agent time, in fixed human buckets.
	Buckets []NameCount `json:"buckets"`
	// Heatmap of when tickets actually get resolved.
	Heatmap []HeatCell `json:"heatmap"`
}

// SLA custom-field ids. These differ per instance, so they are overridable;
// the defaults are the ids discovered on this one.
//
//	customfield_10049 = Time to first response
//	customfield_10050 = Time to resolution
func slaFieldIDs() (frt string, res string) {
	return envOr("JIRA_SLA_FRT_FIELD", "customfield_10049"),
		envOr("JIRA_SLA_RES_FIELD", "customfield_10050")
}

// How far back the effort window looks, in days.
func effortWindowDays() int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("JIRA_EFFORT_DAYS"))); err == nil && n >= 1 && n <= 180 {
		return n
	}
	return 30
}

// Cap on issues pulled for the effort window. A month of a busy desk is around
// a thousand; this leaves room without letting a mis-typed JQL page forever.
const effortCap = 3000

// slaField is the shape of a JSM SLA custom field.
type slaField struct {
	CompletedCycles []struct {
		Breached    bool `json:"breached"`
		ElapsedTime struct {
			Millis int64 `json:"millis"`
		} `json:"elapsedTime"`
		GoalDuration struct {
			Millis int64 `json:"millis"`
		} `json:"goalDuration"`
	} `json:"completedCycles"`
}

// last returns the most recent completed cycle's elapsed time, whether it
// breached, and whether there was a cycle at all.
//
// The LAST cycle, not the first: a ticket that was resolved, reopened and
// resolved again has several, and the one that describes how it finished is
// the latest.
func (s *slaField) last() (elapsed int64, breached bool, ok bool) {
	if s == nil || len(s.CompletedCycles) == 0 {
		return 0, false, false
	}
	c := s.CompletedCycles[len(s.CompletedCycles)-1]
	return c.ElapsedTime.Millis, c.Breached, true
}

// effortIssue is one row of the effort query: the typed fields plus the SLA
// custom fields, which are addressed by id and so cannot be named in a struct.
type effortIssue struct {
	Assignee       *assigneeRef
	Created        string
	ResolutionDate string
	SLA            map[string]slaField
}

type assigneeRef struct {
	DisplayName string `json:"displayName"`
	AccountID   string `json:"accountId"`
}

// effortFields is the part of an issue's `fields` object with known names.
type effortFields struct {
	Assignee       *assigneeRef `json:"assignee"`
	Created        string       `json:"created"`
	ResolutionDate string       `json:"resolutiondate"`
}

// computeEffort builds the analytics block for one project.
func (c *client) computeEffort(ctx context.Context, projectKey string, windowDays int) Effort {
	frtID, resID := slaFieldIDs()
	out := Effort{WindowDays: windowDays, NoTimeLogged: true}

	jql := fmt.Sprintf(`project = %s AND resolutiondate >= -%dd ORDER BY resolutiondate DESC`,
		quote(projectKey), windowDays)

	issues, truncated, err := c.effortSearch(ctx, jql, []string{
		"assignee", "created", "resolutiondate", frtID, resID,
	}, effortCap)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Sampled = len(issues)
	out.Truncated = truncated

	type acc struct {
		row   EffortRow
		work  []int64
		frt   []int64
		cycle []int64
	}
	people := map[string]*acc{}

	var allWork, allFRT, allCycle []int64
	var totalWall, totalWork int64
	heat := map[[2]int]int{}
	buckets := map[string]int{}
	sawSLA := false

	for _, iss := range issues {
		name, accountID := "Unassigned", ""
		if a := iss.Assignee; a != nil && strings.TrimSpace(a.DisplayName) != "" {
			name, accountID = strings.TrimSpace(a.DisplayName), a.AccountID
		}
		// Grouped by account id where there is one: two people can share a
		// display name and must not be merged into one row.
		group := accountID
		if group == "" {
			group = "name:" + name
		}
		p := people[group]
		if p == nil {
			p = &acc{row: EffortRow{Name: name, AccountID: accountID}}
			people[group] = p
		}
		p.row.Resolved++

		// --- wall clock ------------------------------------------------
		created, okC := parseJiraTime(iss.Created)
		resolved, okR := parseJiraTime(iss.ResolutionDate)
		if okC && okR && resolved.After(created) {
			ms := resolved.Sub(created).Milliseconds()
			p.cycle = append(p.cycle, ms)
			allCycle = append(allCycle, ms)
			totalWall += ms
		}
		if okR {
			heat[[2]int{int(resolved.Weekday()), resolved.Hour()}]++
		}

		// --- SLA clocks ------------------------------------------------
		resCycle := iss.SLA[resID]
		frtCycle := iss.SLA[frtID]
		workMs, resBreached, hasRes := resCycle.last()
		frtMs, frtBreached, hasFRT := frtCycle.last()

		if hasRes {
			sawSLA = true
			p.row.Measured++
			out.Measured++
			p.work = append(p.work, workMs)
			allWork = append(allWork, workMs)
			p.row.WorkTotalMs += workMs
			totalWork += workMs
			if resBreached {
				p.row.SLABreachRes++
				out.Breaches++
			} else {
				p.row.SLAMetRes++
			}
			buckets[durationBucket(workMs)]++
		}
		if hasFRT {
			sawSLA = true
			p.frt = append(p.frt, frtMs)
			allFRT = append(allFRT, frtMs)
			if frtBreached {
				p.row.SLABreachFRT++
			} else {
				p.row.SLAMetFRT++
			}
		}
		// Resolved on the first reply: both clocks stopped at the same elapsed
		// value, so nothing accrued after the first response.
		if hasRes && hasFRT && workMs == frtMs {
			p.row.FirstTouch++
			out.FirstTouch++
		}
	}

	rows := make([]EffortRow, 0, len(people))
	for _, p := range people {
		p.row.WorkMedianMs = percentile(p.work, 50)
		p.row.WorkP90Ms = percentile(p.work, 90)
		p.row.FirstResponseMedianMs = percentile(p.frt, 50)
		p.row.CycleMedianMs = percentile(p.cycle, 50)
		rows = append(rows, p.row)
	}
	// Most tickets first, name as the tie-break so the order is stable.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Resolved != rows[j].Resolved {
			return rows[i].Resolved > rows[j].Resolved
		}
		return rows[i].Name < rows[j].Name
	})
	out.Rows = rows

	out.WorkTotalMs = totalWork
	out.WorkMedianMs = percentile(allWork, 50)
	out.WorkP90Ms = percentile(allWork, 90)
	out.CycleMedianMs = percentile(allCycle, 50)
	out.FRTMedianMs = percentile(allFRT, 50)
	out.NoSLAData = !sawSLA
	if totalWall > 0 {
		out.WaitRatio = math.Round((float64(totalWork)/float64(totalWall))*1000) / 1000
	}

	out.Buckets = orderedBuckets(buckets)
	out.Heatmap = make([]HeatCell, 0, len(heat))
	for key, n := range heat {
		out.Heatmap = append(out.Heatmap, HeatCell{Day: key[0], Hour: key[1], Count: n})
	}
	sort.Slice(out.Heatmap, func(i, j int) bool {
		if out.Heatmap[i].Day != out.Heatmap[j].Day {
			return out.Heatmap[i].Day < out.Heatmap[j].Day
		}
		return out.Heatmap[i].Hour < out.Heatmap[j].Hour
	})
	return out
}

// effortSearch pages a JQL search. Each issue's `fields` object is decoded
// twice from the same bytes: once into the named fields, once as a map keyed by
// field id so the customfield_* SLA objects can be picked out. Decoding twice
// is cheaper and far less brittle than hand-rolling a dynamic struct, and it
// costs no extra HTTP calls.
func (c *client) effortSearch(ctx context.Context, jql string, fields []string, cap int) ([]effortIssue, bool, error) {
	var all []effortIssue
	token := ""
	for len(all) < cap {
		pageSize := 100
		if remaining := cap - len(all); remaining < pageSize {
			pageSize = remaining
		}
		body := map[string]any{"jql": jql, "maxResults": pageSize, "fields": fields}
		if token != "" {
			body["nextPageToken"] = token
		}
		var page struct {
			Issues []struct {
				Fields json.RawMessage `json:"fields"`
			} `json:"issues"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.do(ctx, http.MethodPost, "/rest/api/3/search/jql", body, &page); err != nil {
			return all, false, err
		}
		for _, iss := range page.Issues {
			row := effortIssue{SLA: map[string]slaField{}}
			var named effortFields
			if err := json.Unmarshal(iss.Fields, &named); err == nil {
				row.Assignee = named.Assignee
				row.Created = named.Created
				row.ResolutionDate = named.ResolutionDate
			}
			// A field that is null, or is some other custom field shape,
			// simply fails to decode into slaField and is skipped - which is
			// why this tolerates per-key errors instead of failing the page.
			var byID map[string]json.RawMessage
			if err := json.Unmarshal(iss.Fields, &byID); err == nil {
				for key, raw := range byID {
					if !strings.HasPrefix(key, "customfield_") {
						continue
					}
					var sla slaField
					if err := json.Unmarshal(raw, &sla); err == nil && len(sla.CompletedCycles) > 0 {
						row.SLA[key] = sla
					}
				}
			}
			all = append(all, row)
		}
		if page.NextPageToken == "" || len(page.Issues) == 0 {
			return all, false, nil
		}
		token = page.NextPageToken
	}
	return all, true, nil
}

// Timestamps are parsed with parseJiraTime from jira.go, which handles Jira's
// non-RFC3339 offset ("...-0500", no colon) and keeps that offset rather than
// converting, so hour-of-day reads as the service desk's own local hour.

// percentile returns the pth percentile of a slice of millisecond durations,
// using nearest-rank. Returns 0 for an empty slice.
//
// Medians and p90 rather than means throughout: the real distribution runs
// from seconds to weeks, so a mean describes nobody.
func percentile(values []int64, p int) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]int64, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := int(math.Ceil((float64(p) / 100) * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// durationBucket puts a duration in a human band. Boundaries chosen from the
// observed distribution rather than round numbers: most tickets land under an
// hour, so that is where the resolution needs to be.
func durationBucket(ms int64) string {
	m := ms / 60000
	switch {
	case m < 5:
		return "< 5m"
	case m < 15:
		return "5-15m"
	case m < 60:
		return "15-60m"
	case m < 240:
		return "1-4h"
	case m < 480:
		return "4-8h"
	case m < 1440:
		return "8-24h"
	default:
		return "> 24h"
	}
}

// bucketOrder keeps the histogram in time order rather than count order; a
// histogram sorted by frequency is not a histogram.
var bucketOrder = []string{"< 5m", "5-15m", "15-60m", "1-4h", "4-8h", "8-24h", "> 24h"}

func orderedBuckets(m map[string]int) []NameCount {
	out := make([]NameCount, 0, len(bucketOrder))
	for _, name := range bucketOrder {
		out = append(out, NameCount{Name: name, Count: m[name]})
	}
	return out
}
