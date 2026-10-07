package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTallyGroupsByAccountIDNotDisplayName(t *testing.T) {
	// Two different people really can share a display name. Grouping on the
	// name would merge them into one row and silently halve the roster.
	rows := tally([]assignee{
		{name: "Chris Lee", accountID: "acct-1"},
		{name: "Chris Lee", accountID: "acct-2"},
		{name: "Chris Lee", accountID: "acct-1"},
	})
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows for 2 distinct accounts, got %d: %+v", len(rows), rows)
	}
	if rows[0].Count != 2 || rows[0].AccountID != "acct-1" {
		t.Errorf("expected acct-1 with 2 first, got %+v", rows[0])
	}
	if rows[1].Count != 1 || rows[1].AccountID != "acct-2" {
		t.Errorf("expected acct-2 with 1 second, got %+v", rows[1])
	}
}

func TestTallyKeepsUnassignedSeparate(t *testing.T) {
	// Unassigned has no account id. It must not collapse into whichever
	// id-less row happened to come first, and it must survive as its own row.
	rows := tally([]assignee{
		{name: "Unassigned"},
		{name: "Dana Fox", accountID: "acct-9"},
		{name: "Unassigned"},
	})
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(rows), rows)
	}
	var unassigned *AssigneeCount
	for i := range rows {
		if rows[i].Name == "Unassigned" {
			unassigned = &rows[i]
		}
	}
	if unassigned == nil {
		t.Fatal("Unassigned row was dropped")
	}
	if unassigned.Count != 2 {
		t.Errorf("Unassigned count = %d, want 2", unassigned.Count)
	}
	if unassigned.AccountID != "" {
		t.Errorf("Unassigned should carry no account id, got %q", unassigned.AccountID)
	}
}

func TestTallySortIsStableOnTies(t *testing.T) {
	// A wallboard refreshes every minute. If equal counts came back in map
	// order the rows would visibly shuffle, so the name breaks the tie.
	for i := 0; i < 20; i++ {
		rows := tally([]assignee{
			{name: "Zoe", accountID: "z"},
			{name: "Amy", accountID: "a"},
			{name: "Mel", accountID: "m"},
		})
		got := []string{rows[0].Name, rows[1].Name, rows[2].Name}
		want := []string{"Amy", "Mel", "Zoe"}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("pass %d: order %v, want %v", i, got, want)
			}
		}
	}
}

// fakeJira serves paged search responses so pagination and the cap can be
// tested without a Jira instance.
func fakeJira(t *testing.T, pages [][]string, lastToken string) (*client, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Fields        []string `json:"fields"`
			MaxResults    int      `json:"maxResults"`
			NextPageToken string   `json:"nextPageToken"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		// The whole point of this query is that it asks for one field.
		if len(body.Fields) != 1 || body.Fields[0] != "assignee" {
			t.Errorf("expected fields=[assignee], got %v", body.Fields)
		}
		idx := 0
		if body.NextPageToken != "" {
			if _, err := fmt.Sscanf(body.NextPageToken, "p%d", &idx); err != nil {
				t.Errorf("bad token %q", body.NextPageToken)
			}
		}
		calls++
		issues := []map[string]any{}
		if idx < len(pages) {
			for _, name := range pages[idx] {
				issues = append(issues, map[string]any{
					"fields": map[string]any{"assignee": map[string]any{"displayName": name, "accountId": "id-" + name}},
				})
			}
		}
		token := fmt.Sprintf("p%d", idx+1)
		if idx+1 >= len(pages) {
			token = lastToken
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"issues": issues, "nextPageToken": token})
	}))
	t.Cleanup(srv.Close)
	c := newClient(config{baseURL: srv.URL, email: "e", token: "t"})
	return c, &calls
}

func TestAssigneesForPagesUntilTokenIsEmpty(t *testing.T) {
	c, calls := fakeJira(t, [][]string{
		{"Amy", "Bob"},
		{"Amy", "Cat"},
	}, "")
	got, truncated, err := c.assigneesFor(context.Background(), "project = X", 4000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if truncated {
		t.Error("should not report truncated when the token ran out")
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 assignees across 2 pages, got %d (%d calls)", len(got), *calls)
	}
	rows := tally(got)
	if rows[0].Name != "Amy" || rows[0].Count != 2 {
		t.Errorf("expected Amy with 2 on top, got %+v", rows[0])
	}
}

func TestAssigneesForStopsAtCapAndSaysSo(t *testing.T) {
	// A Jira that keeps handing back a token forever: without the cap this
	// loops until the context dies, and without the flag the total silently
	// reads as if it were complete.
	pages := make([][]string, 50)
	for i := range pages {
		pages[i] = []string{"Amy", "Bob"}
	}
	c, _ := fakeJira(t, pages, "never-ends")
	got, truncated, err := c.assigneesFor(context.Background(), "project = X", 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !truncated {
		t.Error("expected truncated=true when the cap is reached")
	}
	if len(got) != 6 {
		t.Errorf("expected exactly the cap (6), got %d", len(got))
	}
}

func TestAssigneesForTreatsMissingAssigneeAsUnassigned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"issues":[
			{"fields":{"assignee":null}},
			{"fields":{"assignee":{"displayName":"  ","accountId":"blank"}}},
			{"fields":{}}
		],"nextPageToken":""}`))
	}))
	defer srv.Close()
	c := newClient(config{baseURL: srv.URL, email: "e", token: "t"})
	got, _, err := c.assigneesFor(context.Background(), "project = X", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(got))
	}
	for i, a := range got {
		if a.name != "Unassigned" || a.accountID != "" {
			t.Errorf("row %d: got %+v, want the Unassigned sentinel", i, a)
		}
	}
}

func TestBreakdownWindowJQLQuotesTheProjectKey(t *testing.T) {
	// The templates take a pre-quoted key. A bare %s substitution would
	// produce `project = LLSM` here and `project = "LLSM"` in the poller,
	// which is how two counts of the same thing start to disagree.
	for _, w := range breakdownWindows {
		jql := fmt.Sprintf(w.JQL, quote("LLSM"))
		if !strings.Contains(jql, `project = "LLSM"`) {
			t.Errorf("window %s: %q does not carry a quoted project key", w.ID, jql)
		}
		if strings.Contains(jql, "%!") {
			t.Errorf("window %s: bad format verb in %q", w.ID, jql)
		}
	}
}

func TestBreakdownLastMonthWindowIsBounded(t *testing.T) {
	// "Completed last month" has to exclude this month, or it becomes
	// "everything since the start of last month" and double counts MTD.
	var jql string
	for _, w := range breakdownWindows {
		if w.ID == "resolvedLastMonth" {
			jql = w.JQL
		}
	}
	if jql == "" {
		t.Fatal("resolvedLastMonth window is missing")
	}
	if !strings.Contains(jql, "startOfMonth(-1)") || !strings.Contains(jql, "< startOfMonth()") {
		t.Errorf("last-month window is not bounded on both ends: %q", jql)
	}
}
