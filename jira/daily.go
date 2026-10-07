package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TwiN/logr"
)

// The IT Daily Snapshot: the executive view of the service desk, modelled on
// the sales daily-snapshot page the leadership team already reads.
//
// Period switching (today / yesterday / month to date), four headline KPIs
// each graded against a target, and the same figures broken down by location
// and by technician so the ranked panels and the summary table can be drawn
// from one payload.
//
// WHY ONE QUERY COVERS THREE PERIODS. Today and yesterday are both inside
// month-to-date, so the resolved tickets are fetched ONCE from the earlier of
// (start of month, start of yesterday) and the three periods are derived in
// Go. Querying each period separately would triple the page count for data
// that is a strict subset of what the month query already returned.
//
// WHERE THE TARGETS COME FROM. IT has no sales forecast, so:
//
//   - volume targets are last month's daily average, pro-rated (the service
//     desk's own recent behaviour is the only honest baseline available);
//   - the SLA target is a percentage of tickets meeting the goal already
//     configured in Jira, defaulting to 95%;
//   - the response and resolution targets are last month's medians, so
//     "on target" means "no worse than last month".
//
// All of them are overridable by environment variable, because a real target
// agreed with the business beats a derived one.

// Bucket is one metric set, used for a whole period and for each row of a
// breakdown.
type Bucket struct {
	Closed int `json:"closed"`
	Opened int `json:"opened"`
	// Medians, not means: the distribution runs from seconds to weeks and a
	// mean describes nobody. Means are carried too because the sales page the
	// C-suite reads is phrased as an average, and refusing to show one would
	// just invite the question.
	FRTMedianMs int64 `json:"frtMedianMs"`
	FRTAvgMs    int64 `json:"frtAvgMs"`
	ResMedianMs int64 `json:"resMedianMs"`
	ResAvgMs    int64 `json:"resAvgMs"`
	SLAMet      int   `json:"slaMet"`
	SLABreach   int   `json:"slaBreach"`
	Measured    int   `json:"measured"`
}

// SLAPct is the share of measured tickets that met their resolution goal.
func (b Bucket) SLAPct() float64 {
	total := b.SLAMet + b.SLABreach
	if total == 0 {
		return 0
	}
	return (float64(b.SLAMet) / float64(total)) * 100
}

// Row is one location or one technician within a period.
type Row struct {
	Name      string `json:"name"`
	AccountID string `json:"accountId,omitempty"`
	Bucket    `json:"bucket"`
	SLAPctVal float64 `json:"slaPct"`
}

// Period is one selectable window.
type Period struct {
	ID         string `json:"id"`    // today | yesterday | mtd
	Label      string `json:"label"` // Today | Yesterday | Month to date
	From       string `json:"from"`  // YYYY-MM-DD
	To         string `json:"to"`    // YYYY-MM-DD, exclusive
	Bucket     `json:"bucket"`
	SLAPctVal  float64 `json:"slaPct"`
	ByLocation []Row   `json:"byLocation"`
	ByTech     []Row   `json:"byTech"`
}

// Targets are what the status colours are graded against.
type Targets struct {
	// ClosedPerDay is the volume target for a single day.
	ClosedPerDay float64 `json:"closedPerDay"`
	// ClosedMonth is the month's target, ClosedPerDay times the month's days.
	ClosedMonth float64 `json:"closedMonth"`
	FRTMs       int64   `json:"frtMs"`
	ResMs       int64   `json:"resMs"`
	SLAPct      float64 `json:"slaPct"`
	// Source says where each target came from, so the page can be honest about
	// a derived number rather than presenting it as an agreed goal.
	Source string `json:"source"`
}

// DailyProject is one project's snapshot.
type DailyProject struct {
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	Periods  []Period `json:"periods"`
	Targets  Targets  `json:"targets"`
	Baseline Bucket   `json:"baseline"` // last month, in full
	// BaselineDays is the number of days last month, for the pro-rating.
	BaselineDays int    `json:"baselineDays"`
	Error        string `json:"error,omitempty"`
	// NoLocation counts tickets in the window with no Location set. Shown
	// because it is a data-quality fact the desk can act on, not a bug here.
	NoLocation int `json:"noLocation"`
}

// Daily is the payload served at /api/v1/jira/daily.
type Daily struct {
	Configured bool   `json:"configured"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
	Computing  bool   `json:"computing,omitempty"`
	Stale      bool   `json:"stale,omitempty"`
	// MonthDay / MonthDays drive the "5 of 27 days" counter.
	MonthDay  int            `json:"monthDay"`
	MonthDays int            `json:"monthDays"`
	Today     string         `json:"today"` // YYYY-MM-DD, the desk's own date
	Projects  []DailyProject `json:"projects"`
}

// The Location custom field. Overridable because field ids differ per
// instance; this is the id on this one, and it is populated on roughly two
// thirds of tickets.
func locationFieldID() string { return envOr("JIRA_LOCATION_FIELD", "customfield_10090") }

const dailyCap = 4000

// A longer TTL than the breakdown: this pass pages a month and a half of
// resolved tickets, so recomputing it every three minutes would be
// disproportionate traffic for a figure that moves slowly.
const dailyTTL = 5 * time.Minute
const dailyRetryGap = 45 * time.Second
const dailyPassTimeout = 5 * time.Minute

var (
	dailyMu      sync.Mutex
	dailyCache   Daily
	dailyAt      time.Time
	dailyTriedAt time.Time
	dailyRunning bool
)

// GetDaily answers from cache immediately and recomputes behind the request,
// for the same reason as GetBreakdown: the server's WriteTimeout is 15s and a
// cold pass takes far longer.
func GetDaily(force bool) Daily {
	cfg := loadConfig()
	if !cfg.configured() {
		return Daily{Error: "Jira is not configured"}
	}
	dailyMu.Lock()
	defer dailyMu.Unlock()

	fresh := dailyCache.OK && time.Since(dailyAt) < dailyTTL
	mayAttempt := force || time.Since(dailyTriedAt) >= dailyRetryGap
	if (!fresh || force) && !dailyRunning && mayAttempt {
		dailyRunning = true
		dailyTriedAt = time.Now()
		go runDailyPass()
	}
	out := dailyCache
	out.Configured = true
	out.Computing = dailyRunning
	out.Stale = out.OK && !fresh
	return out
}

func runDailyPass() {
	ctx, cancel := context.WithTimeout(context.Background(), dailyPassTimeout)
	defer cancel()
	computed := computeDaily(ctx)

	dailyMu.Lock()
	defer dailyMu.Unlock()
	dailyRunning = false
	if computed.OK || !dailyCache.OK {
		dailyCache = computed
		if computed.OK {
			dailyAt = time.Now()
		}
		return
	}
	dailyCache.Error = computed.Error
}

// dailyIssue is one resolved ticket as this pass reads it.
type dailyIssue struct {
	assignee  string
	accountID string
	location  string
	created   time.Time
	resolved  time.Time
	frtMs     int64
	frtOK     bool
	resMs     int64
	resOK     bool
	breached  bool
}

func computeDaily(ctx context.Context) Daily {
	cfg := loadConfig()
	now := time.Now()
	out := Daily{
		UpdatedAt: now.UTC().Format(time.RFC3339),
		Today:     now.Format("2006-01-02"),
		MonthDay:  now.Day(),
		MonthDays: daysInMonth(now),
	}
	if !cfg.configured() {
		out.Error = "Jira is not configured"
		return out
	}
	out.Configured = true

	names := map[string]string{}
	for _, p := range GetSnapshot().Projects {
		names[p.Key] = p.Name
	}
	client := newClient(cfg)

	var mu sync.Mutex
	var wg sync.WaitGroup
	projects := make([]DailyProject, len(cfg.projects))
	anyOK := false
	firstErr := ""

	for i, key := range cfg.projects {
		wg.Add(1)
		go func(idx int, projectKey string) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logr.Errorf("[jira.daily] panic for %s: %v", projectKey, r)
				}
			}()
			dp, err := client.dailyProject(ctx, projectKey, now)
			name := names[projectKey]
			if name == "" {
				name = projectKey
			}
			dp.Key = projectKey
			dp.Name = name
			mu.Lock()
			projects[idx] = dp
			if err == nil {
				anyOK = true
			} else if firstErr == "" {
				firstErr = err.Error()
			}
			mu.Unlock()
		}(i, key)
	}
	wg.Wait()

	out.Projects = projects
	out.OK = anyOK
	if !anyOK {
		out.Error = firstErr
		if out.Error == "" {
			out.Error = "no project could be read"
		}
	}
	return out
}

func (c *client) dailyProject(ctx context.Context, key string, now time.Time) (DailyProject, error) {
	dp := DailyProject{}
	frtID, resID := slaFieldIDs()
	locID := locationFieldID()
	fields := []string{"assignee", "created", "resolutiondate", frtID, resID, locID}

	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterdayStart := todayStart.AddDate(0, 0, -1)
	tomorrowStart := todayStart.AddDate(0, 0, 1)

	// One fetch covering every period: the earlier of month start and
	// yesterday, so the 1st of the month still has yesterday's data.
	from := monthStart
	if yesterdayStart.Before(from) {
		from = yesterdayStart
	}

	issues, truncated, err := c.dailySearch(ctx, fmt.Sprintf(
		`project = %s AND resolutiondate >= "%s" ORDER BY resolutiondate DESC`,
		quote(key), from.Format("2006-01-02")), fields, dailyCap)
	if err != nil {
		dp.Error = err.Error()
		return dp, err
	}
	if truncated {
		logr.Warnf("[jira.daily] %s hit the fetch cap of %d resolved tickets", key, dailyCap)
	}

	// Last month, in full, for the baseline.
	lastStart := monthStart.AddDate(0, -1, 0)
	baseIssues, _, err := c.dailySearch(ctx, fmt.Sprintf(
		`project = %s AND resolutiondate >= "%s" AND resolutiondate < "%s"`,
		quote(key), lastStart.Format("2006-01-02"), monthStart.Format("2006-01-02")),
		fields, dailyCap)
	if err != nil {
		// A missing baseline costs the targets, not the whole snapshot.
		logr.Warnf("[jira.daily] %s baseline: %s", key, err.Error())
	}

	windows := []struct {
		id, label string
		from, to  time.Time
	}{
		{"today", "Today", todayStart, tomorrowStart},
		{"yesterday", "Yesterday", yesterdayStart, todayStart},
		{"mtd", "Month to date", monthStart, tomorrowStart},
	}

	for _, w := range windows {
		p := Period{
			ID: w.id, Label: w.label,
			From: w.from.Format("2006-01-02"),
			To:   w.to.Format("2006-01-02"),
		}
		inWindow := make([]dailyIssue, 0, len(issues))
		for _, iss := range issues {
			if !iss.resolved.Before(w.from) && iss.resolved.Before(w.to) {
				inWindow = append(inWindow, iss)
			}
		}
		p.Bucket = bucketOf(inWindow)
		p.SLAPctVal = round1(p.Bucket.SLAPct())
		p.ByLocation = rowsBy(inWindow, func(i dailyIssue) (string, string) {
			if strings.TrimSpace(i.location) == "" {
				return "No location set", ""
			}
			return i.location, ""
		})
		p.ByTech = rowsBy(inWindow, func(i dailyIssue) (string, string) {
			return i.assignee, i.accountID
		})
		// Opened in the window is a different predicate, so it is its own
		// cheap count rather than being derived from the resolved set.
		if n, cErr := c.countApprox(ctx, fmt.Sprintf(
			`project = %s AND created >= "%s" AND created < "%s"`,
			quote(key), w.from.Format("2006-01-02"), w.to.Format("2006-01-02"))); cErr == nil {
			p.Bucket.Opened = n
		}
		if w.id == "mtd" {
			for _, iss := range inWindow {
				if strings.TrimSpace(iss.location) == "" {
					dp.NoLocation++
				}
			}
		}
		dp.Periods = append(dp.Periods, p)
	}

	dp.Baseline = bucketOf(baseIssues)
	dp.BaselineDays = daysInMonth(lastStart)
	dp.Targets = targetsFrom(dp.Baseline, dp.BaselineDays, daysInMonth(now))
	return dp, nil
}

func bucketOf(issues []dailyIssue) Bucket {
	b := Bucket{Closed: len(issues)}
	var frt, res []int64
	for _, i := range issues {
		if i.frtOK {
			frt = append(frt, i.frtMs)
		}
		if i.resOK {
			res = append(res, i.resMs)
			b.Measured++
			if i.breached {
				b.SLABreach++
			} else {
				b.SLAMet++
			}
		}
	}
	b.FRTMedianMs = percentile(frt, 50)
	b.FRTAvgMs = meanOf(frt)
	b.ResMedianMs = percentile(res, 50)
	b.ResAvgMs = meanOf(res)
	return b
}

func rowsBy(issues []dailyIssue, keyOf func(dailyIssue) (string, string)) []Row {
	groups := map[string]*EditableRow{}
	for _, i := range issues {
		name, accountID := keyOf(i)
		if strings.TrimSpace(name) == "" {
			name = "Unassigned"
		}
		gk := accountID
		if gk == "" {
			gk = "name:" + name
		}
		g := groups[gk]
		if g == nil {
			g = &EditableRow{Name: name, AccountID: accountID}
			groups[gk] = g
		}
		g.Issues = append(g.Issues, i)
	}
	out := make([]Row, 0, len(groups))
	for _, g := range groups {
		b := bucketOf(g.Issues)
		out = append(out, Row{
			Name: g.Name, AccountID: g.AccountID,
			Bucket: b, SLAPctVal: round1(b.SLAPct()),
		})
	}
	// Most closed first, name as the tie-break so the order is stable between
	// passes - and the ranked panels all share this order.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Closed != out[j].Closed {
			return out[i].Closed > out[j].Closed
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// EditableRow is the mutable accumulator behind a Row.
type EditableRow struct {
	Name      string
	AccountID string
	Issues    []dailyIssue
}

// targetsFrom derives the grading targets. See the package comment for why
// each one is what it is.
func targetsFrom(base Bucket, baseDays, thisMonthDays int) Targets {
	t := Targets{Source: "last month's own numbers"}
	if baseDays > 0 && base.Closed > 0 {
		t.ClosedPerDay = round1(float64(base.Closed) / float64(baseDays))
	}
	t.ClosedMonth = round1(t.ClosedPerDay * float64(thisMonthDays))
	t.FRTMs = base.FRTMedianMs
	t.ResMs = base.ResMedianMs
	t.SLAPct = 95

	// Explicit targets win over derived ones.
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("JIRA_TARGET_CLOSED_PER_DAY")), 64); err == nil && v > 0 {
		t.ClosedPerDay = v
		t.ClosedMonth = round1(v * float64(thisMonthDays))
		t.Source = "configured"
	}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("JIRA_TARGET_FRT_MINUTES"))); err == nil && v > 0 {
		t.FRTMs = int64(v) * 60000
		t.Source = "configured"
	}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("JIRA_TARGET_RESOLVE_MINUTES"))); err == nil && v > 0 {
		t.ResMs = int64(v) * 60000
		t.Source = "configured"
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("JIRA_TARGET_SLA_PCT")), 64); err == nil && v > 0 {
		t.SLAPct = v
		t.Source = "configured"
	}
	return t
}

// dailySearch pages a JQL search and flattens each issue, decoding the SLA and
// Location custom fields by id.
func (c *client) dailySearch(ctx context.Context, jql string, fields []string, cap int) ([]dailyIssue, bool, error) {
	var all []dailyIssue
	frtID, resID := slaFieldIDs()
	locID := locationFieldID()
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
				Fields map[string]json.RawMessage `json:"fields"`
			} `json:"issues"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.do(ctx, "POST", "/rest/api/3/search/jql", body, &page); err != nil {
			return all, false, err
		}
		for _, iss := range page.Issues {
			row := dailyIssue{}
			if a := decodeAssignee(iss.Fields["assignee"]); a != nil {
				row.assignee, row.accountID = a.DisplayName, a.AccountID
			}
			if row.assignee == "" {
				row.assignee = "Unassigned"
			}
			row.location = decodeOptionValue(iss.Fields[locID])
			row.created, _ = parseJiraTime(decodeString(iss.Fields["created"]))
			row.resolved, _ = parseJiraTime(decodeString(iss.Fields["resolutiondate"]))
			if ms, breached, ok := decodeSLA(iss.Fields[frtID]); ok {
				row.frtMs, row.frtOK = ms, true
				_ = breached
			}
			if ms, breached, ok := decodeSLA(iss.Fields[resID]); ok {
				row.resMs, row.resOK, row.breached = ms, true, breached
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

func meanOf(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	var sum int64
	for _, v := range values {
		sum += v
	}
	return sum / int64(len(values))
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

func daysInMonth(t time.Time) int {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
}

// --- decoding fields addressed by id ------------------------------------
// The SLA and Location fields are custom fields, so their JSON keys are ids
// rather than names and they arrive as raw messages. Each decoder tolerates a
// null or an unexpected shape by returning a zero value: a ticket with an odd
// custom field should not fail the whole snapshot.

func decodeString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

type assigneeField struct {
	DisplayName string `json:"displayName"`
	AccountID   string `json:"accountId"`
}

func decodeAssignee(raw json.RawMessage) *assigneeField {
	if len(raw) == 0 {
		return nil
	}
	var a assigneeField
	if err := json.Unmarshal(raw, &a); err != nil || strings.TrimSpace(a.DisplayName) == "" {
		return nil
	}
	return &a
}

// decodeOptionValue reads a single-select custom field, whose shape is
// {"value": "Hoover"}. Also handles an array of options by taking the first.
func decodeOptionValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var one struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &one); err == nil && one.Value != "" {
		return one.Value
	}
	var many []struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &many); err == nil && len(many) > 0 {
		return many[0].Value
	}
	return ""
}

// decodeSLA reads a JSM SLA field and returns the LAST completed cycle: a
// ticket resolved, reopened and resolved again has several, and the one that
// describes how it finished is the latest.
func decodeSLA(raw json.RawMessage) (elapsedMs int64, breached bool, ok bool) {
	if len(raw) == 0 {
		return 0, false, false
	}
	var f slaField
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false, false
	}
	return f.last()
}
