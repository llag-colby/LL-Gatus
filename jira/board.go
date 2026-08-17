package jira

// Kanban board support. Where the rest of this package computes our own metrics
// from JQL searches, this file mirrors an actual Jira *agile board*: the columns
// come from the board's own configuration (name, order, status mapping and WIP
// constraints), and the cards come from the board's own filter. What you see
// here is what you'd see in Jira, not a re-derived approximation.
//
// Two deliberate deviations, both surfaced in the payload so the UI can label
// them honestly:
//
//   - The done column is windowed. Jira restricts its last column with the
//     board's sub-filter; an unfiltered fetch of every issue ever completed is
//     unbounded, so we ask for the sub-filter AND statusCategory = Done, and
//     fall back to "resolved in the last N days" when the sub-filter can't be
//     combined. DoneWindowDays reports the window that was applied.
//   - Cards are capped (JIRA_BOARD_MAX_CARDS). Truncated says whether the cap
//     was hit.
//
// Boards are polled on demand: a hub spins up the first time a board is
// requested, pushes every refresh to its SSE subscribers, and shuts itself down
// once nothing has watched it for a couple of minutes. Idle boards cost nothing.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TwiN/logr"
)

// --- Types -----------------------------------------------------------------

// BoardRef identifies one agile board visible to the configured account.
type BoardRef struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"` // kanban | scrum | simple
	ProjectKey  string `json:"projectKey,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
}

// BoardCard is one card on the board.
type BoardCard struct {
	Key      string   `json:"key"`
	Summary  string   `json:"summary"`
	Type     string   `json:"type"`
	Status   string   `json:"status"`
	StatusID string   `json:"statusId"`
	Category string   `json:"category"` // new | indeterminate | done
	Priority string   `json:"priority"`
	Assignee string   `json:"assignee"`
	Reporter string   `json:"reporter"`
	Created  string   `json:"created"`
	Updated  string   `json:"updated"`
	Due      string   `json:"due,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	EpicKey  string   `json:"epicKey,omitempty"`
	Epic     string   `json:"epic,omitempty"`

	// SLA fields are copied from the metrics snapshot when the same ticket is
	// present there (service-desk projects), so cards get a live countdown
	// without a per-card SLA request.
	SLABreached    bool   `json:"slaBreached,omitempty"`
	SLAName        string `json:"slaName,omitempty"`
	SLABreachEpoch int64  `json:"slaBreachEpoch,omitempty"`
	SLARemainingMs int64  `json:"slaRemainingMs,omitempty"`
	SLAActive      bool   `json:"slaActive,omitempty"`
	SLAPaused      bool   `json:"slaPaused,omitempty"`
}

// BoardColumn is one column of the board, exactly as configured in Jira.
type BoardColumn struct {
	Name     string      `json:"name"`
	Statuses []string    `json:"statuses"`         // status names mapped to this column
	Category string      `json:"category"`         // dominant status category
	Min      int         `json:"min,omitempty"`    // WIP constraint (0 = unset)
	Max      int         `json:"max,omitempty"`    // WIP constraint (0 = unset)
	IsDone   bool        `json:"isDone,omitempty"` // the board's completion column
	Cards    []BoardCard `json:"cards"`
}

// BoardSnapshot is the payload served at /api/v1/jira/board/:id.
type BoardSnapshot struct {
	Configured     bool          `json:"configured"`
	OK             bool          `json:"ok"`
	Error          string        `json:"error,omitempty"`
	Board          BoardRef      `json:"board"`
	ConstraintType string        `json:"constraintType,omitempty"` // issueCount | issueCountExclSubs | none
	Columns        []BoardColumn `json:"columns"`
	Unmapped       []BoardCard   `json:"unmapped,omitempty"` // statuses not on any column
	Total          int           `json:"total"`
	DoneWindowDays int           `json:"doneWindowDays,omitempty"`
	Truncated      bool          `json:"truncated,omitempty"`
	UpdatedAt      string        `json:"updatedAt,omitempty"`
	BaseURL        string        `json:"baseUrl,omitempty"`
}

// BoardListResult is the payload served at /api/v1/jira/boards.
type BoardListResult struct {
	Configured bool       `json:"configured"`
	OK         bool       `json:"ok"`
	Error      string     `json:"error,omitempty"`
	Boards     []BoardRef `json:"boards"`
}

// --- Config ----------------------------------------------------------------

func boardPollInterval() time.Duration {
	secs := 20
	if n, err := strconv.Atoi(os.Getenv("JIRA_BOARD_POLL_SECONDS")); err == nil && n >= 10 {
		secs = n
	}
	return time.Duration(secs) * time.Second
}

func boardMaxCards() int {
	n, err := strconv.Atoi(os.Getenv("JIRA_BOARD_MAX_CARDS"))
	if err != nil || n < 50 {
		return 400
	}
	return n
}

func boardDoneDays() int {
	n, err := strconv.Atoi(os.Getenv("JIRA_BOARD_DONE_DAYS"))
	if err != nil || n < 1 {
		return 14
	}
	return n
}

// --- Board list (cached) ---------------------------------------------------

var (
	boardListMu  sync.Mutex
	boardListVal []BoardRef
	boardListAt  time.Time
	boardListErr error
	boardListTTL = 5 * time.Minute
)

// ListBoards returns the agile boards for the configured projects, cached for a
// few minutes (board configuration changes far more slowly than ticket state).
func ListBoards(ctx context.Context) BoardListResult {
	cfg := loadConfig()
	if !cfg.configured() {
		return BoardListResult{Configured: false}
	}
	boardListMu.Lock()
	defer boardListMu.Unlock()
	if time.Since(boardListAt) < boardListTTL && (boardListVal != nil || boardListErr != nil) {
		return boardListResult(boardListVal, boardListErr)
	}
	boards, err := newClient(cfg).boards(ctx, cfg)
	boardListAt = time.Now()
	if err != nil && len(boards) == 0 {
		boardListErr, boardListVal = err, nil
	} else {
		boardListErr, boardListVal = nil, boards
	}
	return boardListResult(boardListVal, boardListErr)
}

func boardListResult(boards []BoardRef, err error) BoardListResult {
	if err != nil {
		return BoardListResult{Configured: true, OK: false, Error: err.Error()}
	}
	return BoardListResult{Configured: true, OK: true, Boards: boards}
}

// boards lists every board the account can see: once per configured project,
// plus an unfiltered pass, merged by id.
//
// The unfiltered pass is not a fallback, it is load-bearing. projectKeyOrId does
// not honour a project's *previous* key, so a renamed project (JIRA_PROJECTS can
// easily still carry the old one) returns zero boards from the filtered query
// while the board is sitting right there in the unfiltered list. Filtering first
// is still worth doing: it is what attaches the project name to each board.
func (c *client) boards(ctx context.Context, cfg config) ([]BoardRef, error) {
	var all []BoardRef
	seen := map[int]bool{}
	var firstErr error
	collect := func(extra string) {
		page, err := c.boardPage(ctx, extra)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		for _, b := range page {
			if !seen[b.ID] {
				seen[b.ID] = true
				all = append(all, b)
			}
		}
	}
	for _, key := range cfg.projects {
		collect("&projectKeyOrId=" + url.QueryEscape(key))
	}
	collect("")
	// Kanban first (that's what this view is for), then by project, then name.
	rank := func(t string) int {
		switch strings.ToLower(t) {
		case "kanban":
			return 0
		case "simple":
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if r1, r2 := rank(all[i].Type), rank(all[j].Type); r1 != r2 {
			return r1 < r2
		}
		if all[i].ProjectKey != all[j].ProjectKey {
			return all[i].ProjectKey < all[j].ProjectKey
		}
		return all[i].Name < all[j].Name
	})
	if len(all) == 0 {
		return nil, firstErr
	}
	return all, nil
}

func (c *client) boardPage(ctx context.Context, extra string) ([]BoardRef, error) {
	var out []BoardRef
	for startAt := 0; startAt < 200; startAt += 50 {
		var page struct {
			IsLast bool `json:"isLast"`
			Values []struct {
				ID       int    `json:"id"`
				Name     string `json:"name"`
				Type     string `json:"type"`
				Location struct {
					ProjectKey  string `json:"projectKey"`
					ProjectName string `json:"projectName"`
				} `json:"location"`
			} `json:"values"`
		}
		path := fmt.Sprintf("/rest/agile/1.0/board?maxResults=50&startAt=%d%s", startAt, extra)
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return out, err
		}
		for _, v := range page.Values {
			out = append(out, BoardRef{
				ID: v.ID, Name: v.Name, Type: v.Type,
				ProjectKey: v.Location.ProjectKey, ProjectName: v.Location.ProjectName,
			})
		}
		if page.IsLast || len(page.Values) == 0 {
			break
		}
	}
	return out, nil
}

// --- Status catalogue (id -> name/category), cached ------------------------

type statusMeta struct {
	Name     string
	Category string
}

var (
	statusMu  sync.Mutex
	statusVal map[string]statusMeta
	statusAt  time.Time
)

func (c *client) statuses(ctx context.Context) (map[string]statusMeta, error) {
	statusMu.Lock()
	defer statusMu.Unlock()
	if statusVal != nil && time.Since(statusAt) < 10*time.Minute {
		return statusVal, nil
	}
	var raw []struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		StatusCategory struct {
			Key string `json:"key"`
		} `json:"statusCategory"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/status", nil, &raw); err != nil {
		if statusVal != nil {
			return statusVal, nil // stale beats nothing
		}
		return nil, err
	}
	m := make(map[string]statusMeta, len(raw))
	for _, s := range raw {
		m[s.ID] = statusMeta{Name: s.Name, Category: s.StatusCategory.Key}
	}
	statusVal, statusAt = m, time.Now()
	return m, nil
}

// --- Board fetch -----------------------------------------------------------

type boardConfiguration struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	ColumnConf struct {
		ConstraintType string `json:"constraintType"`
		Columns        []struct {
			Name     string `json:"name"`
			Min      *int   `json:"min"`
			Max      *int   `json:"max"`
			Statuses []struct {
				ID string `json:"id"`
			} `json:"statuses"`
		} `json:"columns"`
	} `json:"columnConfig"`
	SubQuery struct {
		Query string `json:"query"`
	} `json:"subQuery"`
}

type boardRawIssue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string   `json:"summary"`
		Created string   `json:"created"`
		Updated string   `json:"updated"`
		DueDate string   `json:"duedate"`
		Labels  []string `json:"labels"`
		Status  *struct {
			ID             string `json:"id"`
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
		Parent *struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
			} `json:"fields"`
		} `json:"parent"`
	} `json:"fields"`
}

func (ri boardRawIssue) toCard() BoardCard {
	c := BoardCard{
		Key: ri.Key, Summary: ri.Fields.Summary,
		Created: ri.Fields.Created, Updated: ri.Fields.Updated,
		Due: ri.Fields.DueDate, Labels: ri.Fields.Labels,
	}
	if ri.Fields.Status != nil {
		c.Status = ri.Fields.Status.Name
		c.StatusID = ri.Fields.Status.ID
		c.Category = ri.Fields.Status.StatusCategory.Key
	}
	if ri.Fields.IssueType != nil {
		c.Type = ri.Fields.IssueType.Name
	}
	if ri.Fields.Priority != nil {
		c.Priority = ri.Fields.Priority.Name
	}
	if ri.Fields.Assignee != nil {
		c.Assignee = ri.Fields.Assignee.DisplayName
	}
	if ri.Fields.Reporter != nil {
		c.Reporter = ri.Fields.Reporter.DisplayName
	}
	if ri.Fields.Parent != nil {
		c.EpicKey = ri.Fields.Parent.Key
		c.Epic = ri.Fields.Parent.Fields.Summary
	}
	return c
}

var boardFields = strings.Join([]string{
	"summary", "status", "issuetype", "priority", "assignee", "reporter",
	"created", "updated", "duedate", "labels", "parent",
}, ",")

// boardIssues pages through /board/{id}/issue for a JQL slice of the board.
func (c *client) boardIssues(ctx context.Context, id int, jql string, cap int) ([]boardRawIssue, bool, error) {
	var all []boardRawIssue
	truncated := false
	for startAt := 0; ; {
		pageSize := 100
		if remaining := cap - len(all); remaining < pageSize {
			pageSize = remaining
		}
		if pageSize <= 0 {
			truncated = true
			break
		}
		path := fmt.Sprintf("/rest/agile/1.0/board/%d/issue?startAt=%d&maxResults=%d&fields=%s",
			id, startAt, pageSize, url.QueryEscape(boardFields))
		if jql != "" {
			path += "&jql=" + url.QueryEscape(jql)
		}
		var page struct {
			Total      int             `json:"total"`
			MaxResults int             `json:"maxResults"`
			Issues     []boardRawIssue `json:"issues"`
		}
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return all, truncated, err
		}
		all = append(all, page.Issues...)
		startAt += len(page.Issues)
		if len(page.Issues) == 0 || startAt >= page.Total {
			if page.Total > len(all) {
				truncated = true
			}
			break
		}
	}
	return all, truncated, nil
}

// fetchBoard builds a full board snapshot: configuration, then cards, then the
// status→column mapping, then SLA enrichment from the metrics snapshot.
func fetchBoard(ctx context.Context, id int) BoardSnapshot {
	cfg := loadConfig()
	snap := BoardSnapshot{Configured: true, BaseURL: cfg.baseURL, UpdatedAt: nowRFC3339()}
	if !cfg.configured() {
		snap.Configured = false
		return snap
	}
	cl := newClient(cfg)

	var conf boardConfiguration
	if err := cl.do(ctx, http.MethodGet, fmt.Sprintf("/rest/agile/1.0/board/%d/configuration", id), nil, &conf); err != nil {
		snap.Error = "couldn't read board " + strconv.Itoa(id) + " configuration: " + err.Error()
		return snap
	}
	snap.Board = BoardRef{ID: id, Name: conf.Name, Type: conf.Type}
	snap.ConstraintType = conf.ColumnConf.ConstraintType
	// Fill in the project from the cached board list (configuration omits it).
	for _, b := range cachedBoardRefs() {
		if b.ID == id {
			snap.Board.ProjectKey, snap.Board.ProjectName = b.ProjectKey, b.ProjectName
			if snap.Board.Name == "" {
				snap.Board.Name = b.Name
			}
			break
		}
	}

	statusByID, err := cl.statuses(ctx)
	if err != nil {
		snap.Error = "couldn't read the status catalogue: " + err.Error()
		return snap
	}

	// Columns, in board order. columnOf maps a status id to its column index.
	columnOf := map[string]int{}
	for i, col := range conf.ColumnConf.Columns {
		bc := BoardColumn{Name: col.Name, Cards: []BoardCard{}}
		if col.Min != nil {
			bc.Min = *col.Min
		}
		if col.Max != nil {
			bc.Max = *col.Max
		}
		catTally := map[string]int{}
		for _, s := range col.Statuses {
			columnOf[s.ID] = i
			if meta, ok := statusByID[s.ID]; ok {
				bc.Statuses = append(bc.Statuses, meta.Name)
				catTally[meta.Category]++
			}
		}
		bc.Category = dominant(catTally)
		snap.Columns = append(snap.Columns, bc)
	}
	// Jira treats the right-most column as the completion column.
	if n := len(snap.Columns); n > 0 {
		snap.Columns[n-1].IsDone = true
	}

	maxCards := boardMaxCards()
	doneDays := boardDoneDays()

	// In-flight cards: everything the board's filter matches that isn't done.
	inflight, trunc, err := cl.boardIssues(ctx, id, "statusCategory != Done", maxCards)
	if err != nil && len(inflight) == 0 {
		snap.Error = "couldn't read board cards: " + err.Error()
		return snap
	}
	snap.Truncated = trunc

	// Done cards: the board's own sub-filter where it can be combined, else a
	// recency window. Either way the done column stays bounded.
	doneJQL := fmt.Sprintf("statusCategory = Done AND updated >= -%dd", doneDays)
	if q := strings.TrimSpace(conf.SubQuery.Query); q != "" {
		doneJQL = "(" + q + ") AND statusCategory = Done"
	}
	doneCap := maxCards / 2
	done, dtrunc, derr := cl.boardIssues(ctx, id, doneJQL, doneCap)
	if derr != nil {
		// The sub-filter can reference fields this board doesn't have; fall back
		// to the plain recency window before giving up on the done column.
		doneDays = boardDoneDays()
		done, dtrunc, derr = cl.boardIssues(ctx, id,
			fmt.Sprintf("statusCategory = Done AND updated >= -%dd", doneDays), doneCap)
		if derr != nil {
			logr.Warnf("[jira.fetchBoard] board %d: done column unavailable: %s", id, derr.Error())
			done, dtrunc = nil, false
		}
		snap.DoneWindowDays = doneDays
	} else if strings.TrimSpace(conf.SubQuery.Query) == "" {
		snap.DoneWindowDays = doneDays
	}
	if dtrunc {
		snap.Truncated = true
	}

	// Place every card in its column.
	sla := slaIndex()
	place := func(raw []boardRawIssue) {
		for _, ri := range raw {
			card := ri.toCard()
			if s, ok := sla[card.Key]; ok {
				card.SLABreached = s.SLABreached
				card.SLAName = s.SLAName
				card.SLABreachEpoch = s.SLABreachEpoch
				card.SLARemainingMs = s.SLARemainingMs
				card.SLAActive = s.SLAActive
				card.SLAPaused = s.SLAPaused
			}
			snap.Total++
			if idx, ok := columnOf[card.StatusID]; ok && idx < len(snap.Columns) {
				snap.Columns[idx].Cards = append(snap.Columns[idx].Cards, card)
			} else {
				snap.Unmapped = append(snap.Unmapped, card)
			}
		}
	}
	place(inflight)
	place(done)

	// Within a column: SLA urgency first, then priority, then newest.
	for i := range snap.Columns {
		cards := snap.Columns[i].Cards
		sort.SliceStable(cards, func(a, b int) bool {
			ka, kb := cardUrgency(cards[a]), cardUrgency(cards[b])
			if ka != kb {
				return ka < kb
			}
			pa, pb := priorityRank(cards[a].Priority), priorityRank(cards[b].Priority)
			if pa != pb {
				return pa < pb
			}
			return cards[a].Updated > cards[b].Updated
		})
	}

	snap.OK = true
	if err != nil {
		snap.Error = err.Error() // partial data: cards loaded, something else hiccuped
	}
	return snap
}

// slaIndex pulls the SLA fields the metrics poller already fetched, keyed by
// ticket, so board cards get a live countdown for free.
func slaIndex() map[string]Issue {
	out := map[string]Issue{}
	for _, p := range GetSnapshot().Projects {
		for _, iss := range p.Issues {
			if iss.SLAName != "" || iss.SLABreached {
				out[iss.Key] = iss
			}
		}
	}
	return out
}

func cardUrgency(c BoardCard) int64 {
	if c.SLABreached {
		return -1e14 + c.SLARemainingMs
	}
	if c.SLAName != "" {
		return c.SLARemainingMs
	}
	return 1e14
}

func priorityRank(p string) int {
	for i, name := range priorityOrder {
		if strings.EqualFold(name, p) {
			return i
		}
	}
	return len(priorityOrder)
}

func dominant(tally map[string]int) string {
	best, bestN := "", 0
	for k, v := range tally {
		if v > bestN || (v == bestN && k < best) {
			best, bestN = k, v
		}
	}
	return best
}

func cachedBoardRefs() []BoardRef {
	boardListMu.Lock()
	defer boardListMu.Unlock()
	return boardListVal
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// --- On-demand board hubs (cache + SSE fan-out) ----------------------------

type boardHub struct {
	mu         sync.RWMutex
	snap       BoardSnapshot
	fetchedAt  time.Time
	subs       map[chan []byte]struct{}
	polling    bool
	lastAccess time.Time
}

var (
	hubsMu sync.Mutex
	hubs   = map[int]*boardHub{}
)

func hubFor(id int) *boardHub {
	hubsMu.Lock()
	defer hubsMu.Unlock()
	h, ok := hubs[id]
	if !ok {
		h = &boardHub{subs: map[chan []byte]struct{}{}}
		hubs[id] = h
	}
	return h
}

// GetBoard returns the board, refreshing synchronously when the cache is cold or
// stale, and makes sure a poller is running for it.
func GetBoard(ctx context.Context, id int) BoardSnapshot {
	h := hubFor(id)
	h.mu.Lock()
	h.lastAccess = time.Now()
	fresh := h.fetchedAt.After(time.Now().Add(-boardPollInterval())) && (h.snap.OK || h.snap.Error != "")
	snap := h.snap
	h.mu.Unlock()
	if fresh {
		h.ensurePoller(id)
		return snap
	}
	snap = fetchBoard(ctx, id)
	h.store(snap)
	h.ensurePoller(id)
	return snap
}

func (h *boardHub) store(snap BoardSnapshot) {
	h.mu.Lock()
	h.snap = snap
	h.fetchedAt = time.Now()
	subs := make([]chan []byte, 0, len(h.subs))
	for ch := range h.subs {
		subs = append(subs, ch)
	}
	h.mu.Unlock()
	if len(subs) == 0 {
		return
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return
	}
	for _, ch := range subs {
		select {
		case ch <- data:
		default: // never let one slow client stall the poller
		}
	}
}

// ensurePoller starts the refresh loop for a board if it isn't already running.
func (h *boardHub) ensurePoller(id int) {
	h.mu.Lock()
	if h.polling {
		h.mu.Unlock()
		return
	}
	h.polling = true
	h.mu.Unlock()
	go func() {
		interval := boardPollInterval()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			h.mu.RLock()
			idle := len(h.subs) == 0 && time.Since(h.lastAccess) > 2*time.Minute
			h.mu.RUnlock()
			if idle {
				h.mu.Lock()
				h.polling = false
				h.mu.Unlock()
				logr.Debugf("[jira.board] board %d idle — stopping poller", id)
				return
			}
			func() {
				defer func() {
					if r := recover(); r != nil {
						logr.Errorf("[jira.board] board %d poll recovered from panic: %v", id, r)
					}
				}()
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				h.store(fetchBoard(ctx, id))
			}()
		}
	}()
}

// SubscribeBoard registers a live-update channel for one board.
func SubscribeBoard(id int) chan []byte {
	h := hubFor(id)
	ch := make(chan []byte, 4)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.lastAccess = time.Now()
	h.mu.Unlock()
	h.ensurePoller(id)
	return ch
}

// UnsubscribeBoard removes and closes a live-update channel.
func UnsubscribeBoard(id int, ch chan []byte) {
	h := hubFor(id)
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.lastAccess = time.Now()
	h.mu.Unlock()
}

// CachedBoard returns the last stored snapshot for a board without fetching.
func CachedBoard(id int) BoardSnapshot {
	h := hubFor(id)
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.snap
}
