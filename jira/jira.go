// Package jira polls the Jira Cloud REST API on a fixed interval and caches a
// rich, multi-project snapshot of service-desk / project metrics for the /jira
// dashboard: per-project KPIs, breakdowns by type / priority / status, a 14-day
// created-vs-resolved trend, average resolution time, a best-effort SLA-breach
// count, and the current open-ticket list.
//
// Gatus itself does the polling (we hold the Jira API token). A background
// goroutine authenticates with HTTP Basic (email:token). Authentication is
// verified up front via /rest/api/3/myself, because Jira Cloud does NOT 401 on
// search/count with bad credentials, it silently returns empty anonymous
// results, which would otherwise masquerade as a healthy but all-zero board.
//
// Everything is environment-driven; nothing polls until JIRA_BASE_URL,
// JIRA_EMAIL and JIRA_API_TOKEN are all set. Set JIRA_DEMO=1 to serve synthetic
// data (useful for previewing the UI without live credentials).
package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TwiN/logr"
)

// NameCount is a labeled tally (used for by-type / by-priority / by-status).
type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// DayPoint is one day in the created-vs-resolved trend.
type DayPoint struct {
	Date     string `json:"date"` // YYYY-MM-DD
	Created  int    `json:"created"`
	Resolved int    `json:"resolved"`
}

// Issue is a single row in the open-tickets table.
type Issue struct {
	Key         string `json:"key"`
	Summary     string `json:"summary"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Category    string `json:"category"` // status category: new | indeterminate | done
	Priority    string `json:"priority"`
	Assignee    string `json:"assignee"`
	Created     string `json:"created"`
	SLABreached bool   `json:"slaBreached"`
	// Live SLA timing for the soonest-breaching ongoing metric on this ticket.
	SLAName        string `json:"slaName,omitempty"`
	SLABreachEpoch int64  `json:"slaBreachEpoch,omitempty"` // absolute breach time (ms since epoch)
	SLARemainingMs int64  `json:"slaRemainingMs"`           // at poll time; negative = overdue
	SLAActive      bool   `json:"slaActive"`                // clock running now (not paused, within calendar hours)
	SLAPaused      bool   `json:"slaPaused,omitempty"`
	SLAFriendly    string `json:"slaFriendly,omitempty"`
}

// Project is the full metric bundle for one Jira project.
type Project struct {
	Key                string      `json:"key"`
	Name               string      `json:"name"`
	TotalOpen          int         `json:"totalOpen"`
	Unassigned         int         `json:"unassigned"`
	CreatedToday       int         `json:"createdToday"`
	ResolvedToday      int         `json:"resolvedToday"`
	CreatedLast7d      int         `json:"createdLast7d"`
	ResolvedLast7d     int         `json:"resolvedLast7d"`
	AvgResolutionHours float64     `json:"avgResolutionHours"`
	SLABreached        int         `json:"slaBreached"` // -1 = not measured
	ByType             []NameCount `json:"byType"`
	ByPriority         []NameCount `json:"byPriority"`
	ByStatus           []NameCount `json:"byStatus"`
	Trend              []DayPoint  `json:"trend"`
	Issues             []Issue     `json:"issues"`
}

// Snapshot is the full payload served at /api/v1/jira/metrics.
type Snapshot struct {
	Configured bool      `json:"configured"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	Demo       bool      `json:"demo"`
	Status     string    `json:"status"` // healthy | degraded | down | unknown
	UpdatedAt  string    `json:"updatedAt,omitempty"`
	BaseURL    string    `json:"baseUrl,omitempty"`
	Account    string    `json:"account,omitempty"`
	Projects   []Project `json:"projects"`
}

var (
	storeMu sync.RWMutex
	store   = Snapshot{Configured: false, Status: "unknown"}

	subsMu sync.Mutex
	subs   = map[chan []byte]struct{}{}
)

// GetSnapshot returns the latest cached snapshot (safe for concurrent reads).
func GetSnapshot() Snapshot {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return store
}

func setSnapshot(s Snapshot) {
	storeMu.Lock()
	store = s
	storeMu.Unlock()
	// Push to any live (SSE) subscribers so every open dashboard updates the
	// moment a poll completes, without waiting for its own refresh timer.
	if data, err := json.Marshal(s); err == nil {
		broadcast(data)
	}
}

// Subscribe registers a live-update channel; call Unsubscribe when done.
func Subscribe() chan []byte {
	ch := make(chan []byte, 4)
	subsMu.Lock()
	subs[ch] = struct{}{}
	subsMu.Unlock()
	return ch
}

// Unsubscribe removes and closes a live-update channel.
func Unsubscribe(ch chan []byte) {
	subsMu.Lock()
	if _, ok := subs[ch]; ok {
		delete(subs, ch)
		close(ch)
	}
	subsMu.Unlock()
}

func broadcast(data []byte) {
	subsMu.Lock()
	for ch := range subs {
		select {
		case ch <- data:
		default: // never let one slow client stall the poller
		}
	}
	subsMu.Unlock()
}

// --- Configuration ---------------------------------------------------------

type config struct {
	baseURL      string
	email        string
	token        string
	projects     []string
	trendDays    int
	maxIssues    int // hard cap on issues fetched per query (pagination bound)
	pollInterval time.Duration
	slaMax       int // max open tickets to check per project for SLA breaches
	demo         bool
	// projects that should have SLA data pulled (Jira Service Management).
	// Defaults to the first project (typically the service desk).
	slaProjects map[string]bool
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func loadConfig() config {
	poll := 30
	if n, err := strconv.Atoi(os.Getenv("JIRA_POLL_SECONDS")); err == nil && n >= 15 {
		poll = n
	}
	trend := 14
	if n, err := strconv.Atoi(os.Getenv("JIRA_TREND_DAYS")); err == nil && n >= 5 && n <= 60 {
		trend = n
	}
	maxIssues := 500
	if n, err := strconv.Atoi(os.Getenv("JIRA_MAX_ISSUES")); err == nil && n >= 50 {
		maxIssues = n
	}
	slaMax := 60
	if n, err := strconv.Atoi(os.Getenv("JIRA_SLA_MAX")); err == nil && n >= 0 {
		slaMax = n
	}
	// Project list: JIRA_PROJECTS=LLSM,LLIP (falls back to legacy JIRA_PROJECT).
	raw := envOr("JIRA_PROJECTS", envOr("JIRA_PROJECT", "LLSM,LLIP"))
	var projects []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			projects = append(projects, p)
		}
	}
	slaProjects := map[string]bool{}
	if sp := strings.TrimSpace(os.Getenv("JIRA_SLA_PROJECTS")); sp != "" {
		for _, p := range strings.Split(sp, ",") {
			if p = strings.TrimSpace(p); p != "" {
				slaProjects[p] = true
			}
		}
	} else if len(projects) > 0 {
		slaProjects[projects[0]] = true // default: the first (service-desk) project
	}
	return config{
		baseURL:      strings.TrimRight(strings.TrimSpace(os.Getenv("JIRA_BASE_URL")), "/"),
		email:        strings.TrimSpace(os.Getenv("JIRA_EMAIL")),
		token:        strings.TrimSpace(os.Getenv("JIRA_API_TOKEN")),
		projects:     projects,
		trendDays:    trend,
		maxIssues:    maxIssues,
		pollInterval: time.Duration(poll) * time.Second,
		slaMax:       slaMax,
		demo:         os.Getenv("JIRA_DEMO") == "1",
		slaProjects:  slaProjects,
	}
}

func (c config) configured() bool {
	return c.demo || (c.baseURL != "" && c.email != "" && c.token != "")
}

// --- Poller ----------------------------------------------------------------

// StartPoller launches the background polling loop. Safe to call unconditionally.
func StartPoller() {
	cfg := loadConfig()
	if cfg.demo {
		logr.Info("[jira.StartPoller] JIRA_DEMO=1 — serving synthetic data")
		setSnapshot(demoSnapshot(cfg))
		return
	}
	if !cfg.configured() {
		logr.Info("[jira.StartPoller] Jira is not configured (set JIRA_BASE_URL, JIRA_EMAIL, JIRA_API_TOKEN) — the /jira page will show a not-configured state")
		setSnapshot(Snapshot{Configured: false, Status: "unknown"})
		return
	}
	logr.Infof("[jira.StartPoller] Polling %s projects=%s every %s", cfg.baseURL, strings.Join(cfg.projects, ","), cfg.pollInterval)
	go func() {
		poll(cfg)
		ticker := time.NewTicker(cfg.pollInterval)
		defer ticker.Stop()
		for range ticker.C {
			func() {
				defer func() {
					if r := recover(); r != nil {
						logr.Errorf("[jira.poll] recovered from panic: %v", r)
					}
				}()
				poll(cfg)
			}()
		}
	}()
}

func poll(cfg config) {
	cl := newClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Verify authentication first (see package doc for why this matters).
	account, err := cl.me(ctx)
	if err != nil {
		setSnapshot(Snapshot{
			Configured: true,
			OK:         false,
			Status:     "down",
			BaseURL:    cfg.baseURL,
			UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
			Error:      "authentication failed — verify JIRA_EMAIL + JIRA_API_TOKEN (the token must belong to that account): " + err.Error(),
		})
		logr.Warnf("[jira.poll] authentication check failed: %s", err.Error())
		return
	}

	var projects []Project
	var firstErr error
	degraded := false
	for _, key := range cfg.projects {
		pm, perr := cl.project(ctx, cfg, key)
		if perr != nil && firstErr == nil {
			firstErr = perr
		}
		if pm.SLABreached > 0 {
			degraded = true
		}
		projects = append(projects, pm)
	}

	snap := Snapshot{
		Configured: true,
		BaseURL:    cfg.baseURL,
		Account:    account,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		Projects:   projects,
	}
	if firstErr != nil {
		snap.OK = false
		snap.Status = "down"
		snap.Error = firstErr.Error()
		logr.Warnf("[jira.poll] refresh had errors: %s", firstErr.Error())
	} else {
		snap.OK = true
		if degraded {
			snap.Status = "degraded"
		} else {
			snap.Status = "healthy"
		}
	}
	setSnapshot(snap)
}

// --- Per-project computation ----------------------------------------------

func (c *client) project(ctx context.Context, cfg config, key string) (Project, error) {
	pm := Project{Key: key, Name: key, SLABreached: -1}
	var firstErr error

	if name, err := c.projectName(ctx, key); err == nil && name != "" {
		pm.Name = name
	}

	if n, err := c.countApprox(ctx, fmt.Sprintf(`project = %s AND statusCategory != Done`, quote(key))); err == nil {
		pm.TotalOpen = n
	} else {
		firstErr = err
	}

	// Open issues drive the table + the type/priority/status breakdowns.
	open, err := c.searchAll(ctx, fmt.Sprintf(`project = %s AND statusCategory != Done ORDER BY created DESC`, quote(key)), cfg.maxIssues)
	if err != nil && firstErr == nil {
		firstErr = err
	}
	typeAgg, prioAgg, statusAgg := map[string]int{}, map[string]int{}, map[string]int{}
	for _, ri := range open {
		iss := ri.toIssue()
		if iss.Assignee == "" {
			pm.Unassigned++
		}
		typeAgg[orDash(iss.Type)]++
		prioAgg[orDash(iss.Priority)]++
		statusAgg[orDash(iss.Status)]++
	}
	pm.ByType = topCounts(typeAgg, 8)
	pm.ByPriority = orderedPriority(prioAgg)
	pm.ByStatus = topCounts(statusAgg, 8)
	// Table: cap the rows we ship to the browser.
	for i, ri := range open {
		if i >= 60 {
			break
		}
		pm.Issues = append(pm.Issues, ri.toIssue())
	}

	// Trend + throughput windows come from created / resolved over trendDays.
	created, err := c.searchAll(ctx, fmt.Sprintf(`project = %s AND created >= -%dd`, quote(key), cfg.trendDays), cfg.maxIssues)
	if err != nil && firstErr == nil {
		firstErr = err
	}
	resolved, err := c.searchAll(ctx, fmt.Sprintf(`project = %s AND resolutiondate >= -%dd`, quote(key), cfg.trendDays), cfg.maxIssues)
	if err != nil && firstErr == nil {
		firstErr = err
	}
	pm.Trend = buildTrend(cfg.trendDays, created, resolved)
	pm.CreatedToday, pm.CreatedLast7d = windowCounts(created, func(ri rawIssue) string { return ri.Fields.Created })
	pm.ResolvedToday, pm.ResolvedLast7d = windowCounts(resolved, func(ri rawIssue) string { return ri.Fields.ResolutionDate })
	pm.AvgResolutionHours = averageResolutionHours(resolved)

	// SLA (service-desk projects only): enrich each open ticket with its
	// soonest-breaching ongoing SLA for the live countdown, and total breaches.
	if cfg.slaMax > 0 && cfg.slaProjects[key] && len(pm.Issues) > 0 {
		if breached, measured := c.enrichSLAs(ctx, pm.Issues, cfg.slaMax); measured {
			pm.SLABreached = breached
		}
	}
	return pm, firstErr
}

// --- Jira REST client ------------------------------------------------------

type client struct {
	cfg  config
	auth string
	http *http.Client
}

func newClient(cfg config) *client {
	return &client{
		cfg:  cfg,
		auth: "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.email+":"+cfg.token)),
		http: &http.Client{Timeout: 25 * time.Second},
	}
}

func (c *client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s -> %d: %s", method, path, resp.StatusCode, snippet(data))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return err
		}
	}
	return nil
}

// me verifies authentication and returns the display name of the account.
func (c *client) me(ctx context.Context) (string, error) {
	var out struct {
		DisplayName  string `json:"displayName"`
		EmailAddress string `json:"emailAddress"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/myself", nil, &out); err != nil {
		return "", err
	}
	if out.DisplayName != "" {
		return out.DisplayName, nil
	}
	return out.EmailAddress, nil
}

func (c *client) projectName(ctx context.Context, key string) (string, error) {
	var out struct {
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/project/"+key, nil, &out); err != nil {
		return "", err
	}
	return out.Name, nil
}

// countApprox returns the approximate number of issues matching a JQL query.
func (c *client) countApprox(ctx context.Context, jql string) (int, error) {
	var out struct {
		Count int `json:"count"`
	}
	if err := c.do(ctx, http.MethodPost, "/rest/api/3/search/approximate-count", map[string]string{"jql": jql}, &out); err != nil {
		return 0, err
	}
	return out.Count, nil
}

type rawIssue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary        string `json:"summary"`
		Created        string `json:"created"`
		ResolutionDate string `json:"resolutiondate"`
		Status         *struct {
			Name           string `json:"name"`
			StatusCategory struct {
				Key string `json:"key"`
			} `json:"statusCategory"`
		} `json:"status"`
		IssueType *struct {
			Name string `json:"name"`
		} `json:"issuetype"`
		Priority *struct {
			Name string `json:"name"`
		} `json:"priority"`
		Assignee *struct {
			DisplayName string `json:"displayName"`
		} `json:"assignee"`
	} `json:"fields"`
}

func (ri rawIssue) toIssue() Issue {
	iss := Issue{Key: ri.Key, Summary: ri.Fields.Summary, Created: ri.Fields.Created}
	if ri.Fields.Status != nil {
		iss.Status = ri.Fields.Status.Name
		iss.Category = ri.Fields.Status.StatusCategory.Key
	}
	if ri.Fields.IssueType != nil {
		iss.Type = ri.Fields.IssueType.Name
	}
	if ri.Fields.Priority != nil {
		iss.Priority = ri.Fields.Priority.Name
	}
	if ri.Fields.Assignee != nil {
		iss.Assignee = ri.Fields.Assignee.DisplayName
	}
	return iss
}

var issueFields = []string{"summary", "status", "issuetype", "priority", "assignee", "created", "resolutiondate"}

// searchAll runs an enhanced JQL search, paginating via nextPageToken up to cap.
func (c *client) searchAll(ctx context.Context, jql string, cap int) ([]rawIssue, error) {
	var all []rawIssue
	token := ""
	for len(all) < cap {
		pageSize := 100
		if remaining := cap - len(all); remaining < pageSize {
			pageSize = remaining
		}
		reqBody := map[string]any{"jql": jql, "maxResults": pageSize, "fields": issueFields}
		if token != "" {
			reqBody["nextPageToken"] = token
		}
		var out struct {
			Issues        []rawIssue `json:"issues"`
			NextPageToken string     `json:"nextPageToken"`
		}
		if err := c.do(ctx, http.MethodPost, "/rest/api/3/search/jql", reqBody, &out); err != nil {
			return all, err
		}
		all = append(all, out.Issues...)
		if out.NextPageToken == "" || len(out.Issues) == 0 {
			break
		}
		token = out.NextPageToken
	}
	return all, nil
}

// enrichSLAs loops the open tickets (capped) and, for each, pulls its SLA cycles.
// It flags breached tickets (returning the total count) and attaches the
// soonest-breaching ongoing SLA to the Issue so the UI can render a live
// countdown. Returns (breachedCount, measured); measured=false means the SLA API
// was unusable (e.g. not a service desk) so the metric should read "not measured".
func (c *client) enrichSLAs(ctx context.Context, issues []Issue, max int) (int, bool) {
	breached, measured := 0, false
	for i := range issues {
		if i >= max {
			break
		}
		var out struct {
			Values []struct {
				Name         string `json:"name"`
				OngoingCycle *struct {
					Breached            bool `json:"breached"`
					Paused              bool `json:"paused"`
					WithinCalendarHours bool `json:"withinCalendarHours"`
					BreachTime          struct {
						EpochMillis int64 `json:"epochMillis"`
					} `json:"breachTime"`
					RemainingTime struct {
						Millis   int64  `json:"millis"`
						Friendly string `json:"friendly"`
					} `json:"remainingTime"`
				} `json:"ongoingCycle"`
			} `json:"values"`
		}
		if err := c.do(ctx, http.MethodGet, "/rest/servicedeskapi/request/"+issues[i].Key+"/sla", nil, &out); err != nil {
			if !measured {
				return 0, false // SLA data unavailable for this project
			}
			continue
		}
		measured = true
		anyBreached := false
		haveBest := false
		var bestRemaining int64
		for _, v := range out.Values {
			oc := v.OngoingCycle
			if oc == nil {
				continue
			}
			if oc.Breached {
				anyBreached = true
			}
			// The most urgent metric = smallest remaining time (most overdue, or
			// closest to breach). That's the one the UI counts down.
			if !haveBest || oc.RemainingTime.Millis < bestRemaining {
				haveBest = true
				bestRemaining = oc.RemainingTime.Millis
				issues[i].SLAName = v.Name
				issues[i].SLABreachEpoch = oc.BreachTime.EpochMillis
				issues[i].SLARemainingMs = oc.RemainingTime.Millis
				issues[i].SLAActive = !oc.Paused && oc.WithinCalendarHours
				issues[i].SLAPaused = oc.Paused
				issues[i].SLAFriendly = oc.RemainingTime.Friendly
			}
		}
		if anyBreached {
			issues[i].SLABreached = true
			breached++
		}
	}
	return breached, measured
}

// --- Aggregation helpers ---------------------------------------------------

func orDash(s string) string {
	if s == "" {
		return "None"
	}
	return s
}

// topCounts returns the n largest tallies, descending, with the remainder folded
// into an "Other" bucket.
func topCounts(m map[string]int, n int) []NameCount {
	out := make([]NameCount, 0, len(m))
	for k, v := range m {
		out = append(out, NameCount{Name: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) <= n {
		return out
	}
	other := 0
	for _, nc := range out[n:] {
		other += nc.Count
	}
	out = append(out[:n:n], NameCount{Name: "Other", Count: other})
	return out
}

var priorityOrder = []string{"Highest", "High", "Medium", "Low", "Lowest"}

// orderedPriority returns priority tallies in severity order, appending any
// non-standard priority names after the known ones.
func orderedPriority(m map[string]int) []NameCount {
	var out []NameCount
	seen := map[string]bool{}
	for _, p := range priorityOrder {
		if v, ok := m[p]; ok {
			out = append(out, NameCount{Name: p, Count: v})
			seen[p] = true
		}
	}
	var extra []NameCount
	for k, v := range m {
		if !seen[k] {
			extra = append(extra, NameCount{Name: k, Count: v})
		}
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].Count > extra[j].Count })
	return append(out, extra...)
}

// buildTrend produces a contiguous day-by-day created/resolved series covering
// the last `days` days (oldest first), in the server's local time zone.
func buildTrend(days int, created, resolved []rawIssue) []DayPoint {
	idx := map[string]int{}
	series := make([]DayPoint, days)
	now := time.Now()
	for i := 0; i < days; i++ {
		d := now.AddDate(0, 0, -(days - 1 - i))
		key := d.Format("2006-01-02")
		series[i] = DayPoint{Date: key}
		idx[key] = i
	}
	bucket := func(issues []rawIssue, field func(rawIssue) string, add func(*DayPoint)) {
		for _, ri := range issues {
			if t, ok := parseJiraTime(field(ri)); ok {
				if i, ok := idx[t.Local().Format("2006-01-02")]; ok {
					add(&series[i])
				}
			}
		}
	}
	bucket(created, func(ri rawIssue) string { return ri.Fields.Created }, func(p *DayPoint) { p.Created++ })
	bucket(resolved, func(ri rawIssue) string { return ri.Fields.ResolutionDate }, func(p *DayPoint) { p.Resolved++ })
	return series
}

// windowCounts returns (today, last7d) counts based on a timestamp accessor.
func windowCounts(issues []rawIssue, field func(rawIssue) string) (int, int) {
	now := time.Now()
	startToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start7d := startToday.AddDate(0, 0, -6)
	today, week := 0, 0
	for _, ri := range issues {
		if t, ok := parseJiraTime(field(ri)); ok {
			lt := t.Local()
			if !lt.Before(startToday) {
				today++
			}
			if !lt.Before(start7d) {
				week++
			}
		}
	}
	return today, week
}

func averageResolutionHours(issues []rawIssue) float64 {
	var total time.Duration
	var n int
	for _, ri := range issues {
		created, ok1 := parseJiraTime(ri.Fields.Created)
		resolved, ok2 := parseJiraTime(ri.Fields.ResolutionDate)
		if ok1 && ok2 && resolved.After(created) {
			total += resolved.Sub(created)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return math.Round(float64(total)/float64(n)/float64(time.Hour)*10) / 10
}

func parseJiraTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func quote(v string) string {
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
