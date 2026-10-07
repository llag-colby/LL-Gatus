package jira

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TwiN/logr"
)

// Per-assignee ticket counts over a set of time windows, which is the shape a
// team dashboard needs and the one thing the regular snapshot cannot give.
//
// Why this is separate from the poller's Snapshot: that payload carries the
// OPEN issues for each project, so anything about work that is already
// finished (completed today, this month, last month) is not in it and cannot
// be derived from it. A ticket raised and closed this morning never appears.
//
// Why the aggregation happens here and not in Jira: the Jira search API has no
// group-by. The only ways to get a count per assignee are one query per
// assignee, which means knowing the roster first and issuing dozens of
// requests, or one query per window fetching just the assignee field and
// counting locally. The second is far cheaper, so that is what this does: the
// request asks for exactly one field, so even a thousand resolved tickets is a
// small response.
//
// It is cached because the expensive window (last month, often four figures)
// pages at a hundred issues a request, and a wall display reloading the page
// must not mean twenty more calls to Jira.

// AssigneeCount is one row of a window: who, and how many.
//
// AccountID travels with the name so the UI can deep-link the row into Jira's
// issue navigator. It has to be the id and not the display name: `assignee =
// "Some Name"` is not a reliable JQL form on Jira Cloud, where the supported
// one is the account id. Empty for the Unassigned row.
type AssigneeCount struct {
	Name      string `json:"name"`
	AccountID string `json:"accountId,omitempty"`
	Count     int    `json:"count"`
}

// BreakdownWindow is one bento box: a labelled time window and its rows.
type BreakdownWindow struct {
	ID    string          `json:"id"`
	Label string          `json:"label"`
	Total int             `json:"total"`
	Rows  []AssigneeCount `json:"rows"`
	// JQL is handed to the browser so a row can link to the same question in
	// Jira. It is the window's query verbatim, which also means the numbers on
	// screen are checkable against Jira rather than being asserted.
	JQL string `json:"jql,omitempty"`
	// Truncated is set when the window hit the fetch cap, so the UI can say the
	// number is a floor rather than quietly showing a wrong total.
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

// BreakdownProject groups every window for one Jira project.
type BreakdownProject struct {
	Key     string            `json:"key"`
	Name    string            `json:"name"`
	Windows []BreakdownWindow `json:"windows"`
}

// Breakdown is the full payload served at /api/v1/jira/breakdown.
type Breakdown struct {
	Configured bool   `json:"configured"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
	Stale      bool   `json:"stale,omitempty"`
	// Computing is set while a pass is running. The first request returns with
	// this true and no projects, which is a different thing from a failure and
	// the UI has to tell them apart.
	Computing bool               `json:"computing,omitempty"`
	Projects  []BreakdownProject `json:"projects"`
}

// windowSpec is a window's identity and the JQL that selects it. %s is the
// project key.
//
// resolutiondate rather than status: a ticket can sit in a Done category
// without ever having been resolved (moved, duplicated), and "completed" on a
// team board means work that actually finished, with a date attached.
//
// startOfDay/startOfMonth are evaluated by Jira in the account's own timezone,
// which is the right one: "today" has to mean today for the people in the
// counts, not UTC.
var breakdownWindows = []struct {
	ID    string
	Label string
	JQL   string
}{
	// %s takes the quoted project key, the same way the poller builds its own
	// queries, so "Open" here counts exactly what the Overview tab counts.
	{"open", "Open", `project = %s AND statusCategory != Done`},
	{"createdToday", "Submitted today", `project = %s AND created >= startOfDay()`},
	{"resolvedToday", "Completed today", `project = %s AND resolutiondate >= startOfDay()`},
	{"resolvedMonth", "Completed this month", `project = %s AND resolutiondate >= startOfMonth()`},
	{"resolvedLastMonth", "Completed last month",
		`project = %s AND resolutiondate >= startOfMonth(-1) AND resolutiondate < startOfMonth()`},
}

// Enough for a busy month. Past this the window reports Truncated rather than
// paging forever against a mis-typed JQL.
const breakdownCap = 4000

// How long a computed breakdown is served before it is recomputed. Long enough
// that a wallboard refresh is free, short enough that "completed today" moves
// during a working day.
const breakdownTTL = 3 * time.Minute

// Minimum gap between attempts when the last one failed, so an unreachable
// Jira is not re-dialled on every page poll.
const breakdownRetryGap = 30 * time.Second

// Ceiling on one pass. Generous because the last-month window can page several
// thousand issues; it exists to stop a wedged pass from blocking all later
// ones forever, since only one runs at a time.
const breakdownPassTimeout = 4 * time.Minute

var (
	breakdownMu      sync.Mutex
	breakdownCache   Breakdown
	breakdownAt      time.Time // last SUCCESSFUL pass
	breakdownTriedAt time.Time // last attempt, successful or not
	breakdownRunning bool
)

// GetBreakdown answers immediately from cache and recomputes in the background
// when the cache has expired. It never blocks on Jira.
//
// This is not an optimisation, it is a correctness requirement: the HTTP server
// runs with a 15s WriteTimeout (controller/controller.go), and a cold pass over
// five windows per project takes considerably longer than that. A handler that
// waited for Jira would have its response severed mid-flight, which presents as
// a truncated body or a dropped connection rather than as a timeout, and is a
// genuinely confusing thing to debug.
//
// So the first request returns Computing with no projects, and the page polls
// until the numbers land. Only one pass runs at a time, so three wall displays
// refreshing together cost Jira one pass rather than three.
func GetBreakdown(force bool) Breakdown {
	cfg := loadConfig()
	if !cfg.configured() {
		return Breakdown{Error: "Jira is not configured"}
	}

	breakdownMu.Lock()
	defer breakdownMu.Unlock()

	fresh := breakdownCache.OK && time.Since(breakdownAt) < breakdownTTL
	// A human pressing refresh bypasses the retry gap; a page poll does not.
	mayAttempt := force || time.Since(breakdownTriedAt) >= breakdownRetryGap
	if (!fresh || force) && !breakdownRunning && mayAttempt {
		breakdownRunning = true
		breakdownTriedAt = time.Now()
		go runBreakdownPass()
	}

	out := breakdownCache
	out.Configured = true
	out.Computing = breakdownRunning
	out.Stale = out.OK && !fresh
	return out
}

// runBreakdownPass computes one breakdown and stores it. It takes its own
// context: the request that triggered it has long since been answered, so the
// request context would already be cancelled.
func runBreakdownPass() {
	ctx, cancel := context.WithTimeout(context.Background(), breakdownPassTimeout)
	defer cancel()
	computed := computeBreakdown(ctx)

	breakdownMu.Lock()
	defer breakdownMu.Unlock()
	breakdownRunning = false
	// A failed pass must not erase a good one: an expired-but-real breakdown
	// beats an empty page with an error on it. The error is still carried over
	// so the page can say the last attempt failed.
	if computed.OK || !breakdownCache.OK {
		breakdownCache = computed
		if computed.OK {
			breakdownAt = time.Now()
		}
		return
	}
	breakdownCache.Error = computed.Error
}

func computeBreakdown(ctx context.Context) Breakdown {
	cfg := loadConfig()
	out := Breakdown{UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	if !cfg.configured() {
		out.Error = "Jira is not configured"
		return out
	}
	out.Configured = true

	// Project display names come from the poller's snapshot so this does not
	// spend a request per project asking Jira what it already knows.
	names := map[string]string{}
	for _, p := range GetSnapshot().Projects {
		names[p.Key] = p.Name
	}

	client := newClient(cfg)

	type job struct {
		projectIndex int
		windowIndex  int
	}
	projects := make([]BreakdownProject, 0, len(cfg.projects))
	for _, key := range cfg.projects {
		name := names[key]
		if name == "" {
			name = key
		}
		projects = append(projects, BreakdownProject{
			Key:     key,
			Name:    name,
			Windows: make([]BreakdownWindow, len(breakdownWindows)),
		})
	}

	jobs := make([]job, 0, len(projects)*len(breakdownWindows))
	for pi := range projects {
		for wi := range breakdownWindows {
			jobs = append(jobs, job{pi, wi})
		}
	}

	// Bounded fan-out. Windows are independent, and running them serially made
	// a two-project board take the better part of a minute.
	const workers = 4
	var wg sync.WaitGroup
	var mu sync.Mutex
	anyOK := false
	firstErr := ""
	queue := make(chan job)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range queue {
				spec := breakdownWindows[j.windowIndex]
				projectKey := projects[j.projectIndex].Key
				jql := fmt.Sprintf(spec.JQL, quote(projectKey))
				window := BreakdownWindow{ID: spec.ID, Label: spec.Label, JQL: jql}

				assignees, truncated, err := client.assigneesFor(ctx, jql, breakdownCap)
				if err != nil {
					window.Error = err.Error()
					logr.Warnf("[jira.Breakdown] %s %s: %s", projectKey, spec.ID, err.Error())
				} else {
					window.Rows = tally(assignees)
					window.Total = len(assignees)
					window.Truncated = truncated
				}

				mu.Lock()
				projects[j.projectIndex].Windows[j.windowIndex] = window
				if err == nil {
					anyOK = true
				} else if firstErr == "" {
					firstErr = err.Error()
				}
				mu.Unlock()
			}
		}()
	}
	for _, j := range jobs {
		queue <- j
	}
	close(queue)
	wg.Wait()

	out.Projects = projects
	out.OK = anyOK
	if !anyOK {
		out.Error = firstErr
		if out.Error == "" {
			out.Error = "no windows could be read"
		}
	}
	return out
}

// assignee is one issue's assignee as the counter sees it.
type assignee struct {
	name      string
	accountID string
}

// tally groups assignees, unassigned included, and sorts by count with the name
// as the tie-break so the order is stable between refreshes.
//
// Grouped by account id where there is one, because two people can share a
// display name and must not be merged into one row. Falls back to the name for
// the Unassigned bucket, which has no id.
func tally(seen []assignee) []AssigneeCount {
	counts := map[string]int{}
	identity := map[string]assignee{}
	for _, a := range seen {
		group := a.accountID
		if group == "" {
			group = "name:" + a.name
		}
		counts[group]++
		identity[group] = a
	}
	rows := make([]AssigneeCount, 0, len(counts))
	for group, count := range counts {
		who := identity[group]
		rows = append(rows, AssigneeCount{Name: who.name, AccountID: who.accountID, Count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

// assigneesFor pages a JQL search asking for ONE field and returns the display
// name of each match's assignee.
//
// Deliberately not searchAll: that requests the full issue field set, which for
// a thousand resolved tickets is megabytes of summaries, statuses and SLA data
// this never looks at. Asking for assignee alone keeps a four figure window to
// a handful of small pages.
func (c *client) assigneesFor(ctx context.Context, jql string, cap int) ([]assignee, bool, error) {
	var out []assignee
	token := ""
	for len(out) < cap {
		pageSize := 100
		if remaining := cap - len(out); remaining < pageSize {
			pageSize = remaining
		}
		body := map[string]any{
			"jql":        jql,
			"maxResults": pageSize,
			"fields":     []string{"assignee"},
		}
		if token != "" {
			body["nextPageToken"] = token
		}
		var page struct {
			Issues []struct {
				Fields struct {
					Assignee *struct {
						DisplayName string `json:"displayName"`
						AccountID   string `json:"accountId"`
					} `json:"assignee"`
				} `json:"fields"`
			} `json:"issues"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.do(ctx, "POST", "/rest/api/3/search/jql", body, &page); err != nil {
			return out, false, err
		}
		for _, issue := range page.Issues {
			who := assignee{name: "Unassigned"}
			// Unassigned is a real row on a team board, not a gap: it is
			// usually the one that needs acting on.
			if a := issue.Fields.Assignee; a != nil && strings.TrimSpace(a.DisplayName) != "" {
				who = assignee{name: strings.TrimSpace(a.DisplayName), accountID: a.AccountID}
			}
			out = append(out, who)
		}
		if page.NextPageToken == "" || len(page.Issues) == 0 {
			return out, false, nil
		}
		token = page.NextPageToken
	}
	return out, true, nil
}
