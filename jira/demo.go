package jira

import (
	"math"
	"time"
)

// demoSnapshot returns a realistic synthetic snapshot for previewing the /jira
// dashboard without live Jira credentials (enabled with JIRA_DEMO=1). The shape
// matches exactly what the live poller produces.
func demoSnapshot(cfg config) Snapshot {
	days := cfg.trendDays
	if days == 0 {
		days = 14
	}
	sm := demoServiceDesk(days)
	ip := demoProjects(days)
	return Snapshot{
		Configured: true,
		OK:         true,
		Demo:       true,
		Status:     "degraded", // SLA breaches present in the demo
		BaseURL:    "https://longlewis.atlassian.net",
		Account:    "Demo Mode",
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		Projects:   []Project{sm, ip},
	}
}

// deterministic-ish pseudo values (no rand needed) so the preview is stable.
func wobble(i, base, amp int) int {
	v := base + int(float64(amp)*math.Sin(float64(i)*0.9)) + (i*7)%5 - 2
	if v < 0 {
		v = 0
	}
	return v
}

func demoTrend(days, cBase, rBase int) []DayPoint {
	now := time.Now()
	out := make([]DayPoint, days)
	for i := 0; i < days; i++ {
		d := now.AddDate(0, 0, -(days - 1 - i))
		out[i] = DayPoint{
			Date:     d.Format("2006-01-02"),
			Created:  wobble(i, cBase, 4),
			Resolved: wobble(i+2, rBase, 4),
		}
	}
	return out
}

func demoServiceDesk(days int) Project {
	now := time.Now()
	ago := func(h int) string { return now.Add(-time.Duration(h) * time.Hour).Format("2006-01-02T15:04:05.000-0700") }
	issues := []Issue{
		{Key: "LLSM-4821", Summary: "Showroom POS terminal won't print invoices", Type: "Incident", Status: "In Progress", Category: "indeterminate", Priority: "High", Assignee: "Dana Reeves", Created: ago(5), SLABreached: true},
		{Key: "LLSM-4820", Summary: "New hire laptop setup — Parts dept", Type: "Service Request", Status: "Waiting for support", Category: "new", Priority: "Medium", Assignee: "", Created: ago(9)},
		{Key: "LLSM-4818", Summary: "VPN drops every ~10 minutes at Cullman", Type: "Incident", Status: "Escalated", Category: "indeterminate", Priority: "Highest", Assignee: "Marcus Hill", Created: ago(21), SLABreached: true},
		{Key: "LLSM-4815", Summary: "Request: shared mailbox for service-loaner", Type: "Service Request", Status: "Waiting for customer", Category: "indeterminate", Priority: "Low", Assignee: "Dana Reeves", Created: ago(28)},
		{Key: "LLSM-4810", Summary: "Recurring DMS sync failure overnight", Type: "Problem", Status: "Under investigation", Category: "indeterminate", Priority: "High", Assignee: "Marcus Hill", Created: ago(46)},
		{Key: "LLSM-4807", Summary: "Printer offline — F&I office Alabaster", Type: "Incident", Status: "Waiting for support", Category: "new", Priority: "Medium", Assignee: "", Created: ago(52)},
		{Key: "LLSM-4802", Summary: "Access request: DealerTrack for new advisor", Type: "Service Request", Status: "In Progress", Category: "indeterminate", Priority: "Low", Assignee: "Dana Reeves", Created: ago(70)},
		{Key: "LLSM-4799", Summary: "Phones down in Tuscumbia service lane", Type: "Incident", Status: "Waiting for support", Category: "new", Priority: "High", Assignee: "", Created: ago(80), SLABreached: true},
	}
	return Project{
		Key: "LLSM", Name: "IT Service Management", TotalOpen: 34, Unassigned: 7,
		CreatedToday: 6, ResolvedToday: 5, CreatedLast7d: 41, ResolvedLast7d: 38,
		AvgResolutionHours: 19.4, SLABreached: 3,
		ByType: []NameCount{{"Incident", 15}, {"Service Request", 14}, {"Problem", 5}},
		ByPriority: []NameCount{{"Highest", 2}, {"High", 9}, {"Medium", 15}, {"Low", 8}},
		ByStatus: []NameCount{{"Waiting for support", 11}, {"In Progress", 9}, {"Waiting for customer", 8}, {"Escalated", 4}, {"Under investigation", 2}},
		Trend:  demoTrend(days, 6, 5),
		Issues: issues,
	}
}

func demoProjects(days int) Project {
	now := time.Now()
	ago := func(h int) string { return now.Add(-time.Duration(h) * time.Hour).Format("2006-01-02T15:04:05.000-0700") }
	issues := []Issue{
		{Key: "LLIP-112", Summary: "Roll out new DMS across all 9 rooftops", Type: "Epic", Status: "In Progress", Category: "indeterminate", Priority: "High", Assignee: "Priya Nair", Created: ago(220)},
		{Key: "LLIP-108", Summary: "Wi-Fi refresh — Florence & Muscle Shoals", Type: "Story", Status: "In Progress", Category: "indeterminate", Priority: "Medium", Assignee: "Alex Monroe", Created: ago(140)},
		{Key: "LLIP-104", Summary: "Migrate file shares to SharePoint", Type: "Task", Status: "To Do", Category: "new", Priority: "Medium", Assignee: "", Created: ago(96)},
		{Key: "LLIP-101", Summary: "Standardize showroom digital signage", Type: "Story", Status: "To Do", Category: "new", Priority: "Low", Assignee: "Priya Nair", Created: ago(60)},
	}
	return Project{
		Key: "LLIP", Name: "IT Projects", TotalOpen: 12, Unassigned: 3,
		CreatedToday: 1, ResolvedToday: 0, CreatedLast7d: 4, ResolvedLast7d: 3,
		AvgResolutionHours: 96.5, SLABreached: -1,
		ByType:     []NameCount{{"Story", 6}, {"Task", 4}, {"Epic", 2}},
		ByPriority: []NameCount{{"High", 3}, {"Medium", 6}, {"Low", 3}},
		ByStatus:   []NameCount{{"To Do", 7}, {"In Progress", 5}},
		Trend:      demoTrend(days, 1, 1),
		Issues:     issues,
	}
}
