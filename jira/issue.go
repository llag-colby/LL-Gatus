package jira

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// SLAInfo is one SLA metric on a ticket (e.g. "Time to first response").
type SLAInfo struct {
	Name      string `json:"name"`
	Breached  bool   `json:"breached"`
	Remaining string `json:"remaining"` // human-friendly, e.g. "2h 14m" or "-1h 5m" (overdue)
	Ongoing   bool   `json:"ongoing"`
}

// Comment is one recent comment (rendered to HTML by Jira).
type Comment struct {
	Author  string `json:"author"`
	HTML    string `json:"html"`
	Created string `json:"created"`
}

// IssueDetail is the on-demand drill-down payload for a single ticket.
type IssueDetail struct {
	Key             string    `json:"key"`
	Summary         string    `json:"summary"`
	Type            string    `json:"type"`
	Status          string    `json:"status"`
	Category        string    `json:"category"`
	Priority        string    `json:"priority"`
	Assignee        string    `json:"assignee"`
	Reporter        string    `json:"reporter"`
	Created         string    `json:"created"`
	Updated         string    `json:"updated"`
	Labels          []string  `json:"labels"`
	DescriptionHTML string    `json:"descriptionHtml"`
	URL             string    `json:"url"`
	SLAs            []SLAInfo `json:"slas"`
	Comments        []Comment `json:"comments"`
	Demo            bool      `json:"demo"`
}

// FetchIssue loads a single ticket's detail on demand (used by the drill-down).
func FetchIssue(ctx context.Context, key string) (*IssueDetail, error) {
	cfg := loadConfig()
	if cfg.demo {
		return demoIssueDetail(key), nil
	}
	if !cfg.configured() {
		return nil, fmt.Errorf("jira is not configured")
	}
	cl := newClient(cfg)

	var raw struct {
		Key    string `json:"key"`
		Fields struct {
			Summary   string   `json:"summary"`
			Created   string   `json:"created"`
			Updated   string   `json:"updated"`
			Labels    []string `json:"labels"`
			Status    *struct {
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
			Reporter *struct {
				DisplayName string `json:"displayName"`
			} `json:"reporter"`
		} `json:"fields"`
		RenderedFields struct {
			Description string `json:"description"`
		} `json:"renderedFields"`
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) +
		"?expand=renderedFields&fields=summary,status,issuetype,priority,assignee,reporter,created,updated,labels,description"
	if err := cl.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}

	d := &IssueDetail{
		Key:             raw.Key,
		Summary:         raw.Fields.Summary,
		Created:         raw.Fields.Created,
		Updated:         raw.Fields.Updated,
		Labels:          raw.Fields.Labels,
		DescriptionHTML: raw.RenderedFields.Description,
		URL:             cfg.baseURL + "/browse/" + key,
	}
	if raw.Fields.Status != nil {
		d.Status = raw.Fields.Status.Name
		d.Category = raw.Fields.Status.StatusCategory.Key
	}
	if raw.Fields.IssueType != nil {
		d.Type = raw.Fields.IssueType.Name
	}
	if raw.Fields.Priority != nil {
		d.Priority = raw.Fields.Priority.Name
	}
	if raw.Fields.Assignee != nil {
		d.Assignee = raw.Fields.Assignee.DisplayName
	}
	if raw.Fields.Reporter != nil {
		d.Reporter = raw.Fields.Reporter.DisplayName
	}

	// SLA metrics (service desk only; best-effort, never fatal).
	d.SLAs = cl.fetchSLAs(ctx, key)
	// Recent comments (best-effort).
	d.Comments = cl.fetchComments(ctx, key)
	return d, nil
}

func (c *client) fetchSLAs(ctx context.Context, key string) []SLAInfo {
	var out struct {
		Values []struct {
			Name         string `json:"name"`
			OngoingCycle *struct {
				Breached      bool `json:"breached"`
				RemainingTime struct {
					Friendly string `json:"friendly"`
				} `json:"remainingTime"`
			} `json:"ongoingCycle"`
		} `json:"values"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/servicedeskapi/request/"+url.PathEscape(key)+"/sla", nil, &out); err != nil {
		return nil
	}
	var slas []SLAInfo
	for _, v := range out.Values {
		s := SLAInfo{Name: v.Name}
		if v.OngoingCycle != nil {
			s.Ongoing = true
			s.Breached = v.OngoingCycle.Breached
			s.Remaining = v.OngoingCycle.RemainingTime.Friendly
		}
		slas = append(slas, s)
	}
	return slas
}

func (c *client) fetchComments(ctx context.Context, key string) []Comment {
	var out struct {
		Comments []struct {
			Author struct {
				DisplayName string `json:"displayName"`
			} `json:"author"`
			RenderedBody string `json:"renderedBody"`
			Created      string `json:"created"`
		} `json:"comments"`
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/comment?maxResults=3&orderBy=-created&expand=renderedBody"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil
	}
	var comments []Comment
	for _, cm := range out.Comments {
		comments = append(comments, Comment{Author: cm.Author.DisplayName, HTML: cm.RenderedBody, Created: cm.Created})
	}
	return comments
}

// demoIssueDetail returns synthetic detail for the demo tickets.
func demoIssueDetail(key string) *IssueDetail {
	now := time.Now()
	ago := func(h int) string { return now.Add(-time.Duration(h) * time.Hour).Format("2006-01-02T15:04:05.000-0700") }
	return &IssueDetail{
		Key: key, Summary: "Showroom POS terminal won't print invoices", Type: "Incident",
		Status: "In Progress", Category: "indeterminate", Priority: "High",
		Assignee: "Dana Reeves", Reporter: "Front Desk", Created: ago(5), Updated: ago(1),
		Labels: []string{"pos", "alabaster"},
		DescriptionHTML: "<p>The invoice printer attached to the showroom POS stopped printing after the last Windows update. " +
			"Restarting the spooler works for one job then fails again.</p><p><b>Steps tried:</b> reinstalled driver, cleared queue.</p>",
		URL:  "https://longlewis.atlassian.net/browse/" + key,
		SLAs: []SLAInfo{{Name: "Time to first response", Breached: true, Remaining: "-1h 20m", Ongoing: true}, {Name: "Time to resolution", Breached: false, Remaining: "3h 40m", Ongoing: true}},
		Comments: []Comment{
			{Author: "Dana Reeves", HTML: "<p>Swapping the printer with a spare, will confirm shortly.</p>", Created: ago(1)},
			{Author: "Front Desk", HTML: "<p>Still down as of this morning.</p>", Created: ago(3)},
		},
		Demo: true,
	}
}
