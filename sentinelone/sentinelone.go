// Package sentinelone polls the SentinelOne management API on a fixed interval
// and caches a snapshot of the threat estate for the /s1 dashboard: headline
// incident counts, the open threat queue, breakdowns by analyst verdict,
// confidence, classification and mitigation state, a created-per-day trend,
// and endpoint agent health.
//
// SentinelOne has no concept of a "ticket". The thing an analyst works through
// is a THREAT, and it carries the three fields that make it ticket-like: an
// incident status (unresolved / in_progress / resolved), an analyst verdict
// (undefined / suspicious / true_positive / false_positive) and a mitigation
// state. "Unresolved or in progress" is treated as the open queue, the direct
// analogue of a service desk's open tickets.
//
// Gatus does the polling because it holds the API token. Authentication is
// verified up front against /system/info: unlike Jira Cloud, SentinelOne
// returns a clean 401 on a bad token, so a failure is unambiguous and is
// reported rather than being allowed to masquerade as an empty, healthy estate.
//
// MEASURED PROPERTIES OF THIS API, all established against the live console
// rather than assumed, because several of them are surprising:
//
//   - countOnly=true returns a ~60 byte body whose pagination.totalItems
//     respects every filter. So each count on this page costs one tiny request
//     and NOTHING has to page thousands of threats to be counted.
//   - limit=1000 passes validation and then truncates the response mid-stream
//     (IncompleteRead). The usable ceiling is 500; this package uses 100.
//   - Latency ranges from 0.2s to 24s for the same query. A conventional 10s
//     or 30s HTTP timeout produces false failures, so the client allows 90s.
//   - There are no rate-limit headers and no 429s, so there is no signal to
//     back off against. Requests are therefore kept few and bounded.
//   - Unknown query parameters 400, so a filter cannot be silently ignored -
//     with one exception: `classifications` accepts anything and returns an
//     empty result for a typo. This package never filters on it.
//   - /threats/{id} is a 404. A single threat is fetched with /threats?ids=.
//   - Site and group are single-valued in this tenant (1 site, 1 group, 956
//     agents), so grouping by site is degenerate and the estate is grouped by
//     endpoint, OS and machine type instead.
//
// Everything is environment-driven; nothing polls until S1_BASE_URL and
// S1_API_TOKEN are both set. There is no synthetic mode - every number the
// dashboard shows came from SentinelOne.
package sentinelone

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// --- Public types ----------------------------------------------------------

// NameCount is a labeled tally, used for every breakdown.
type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// TrendPoint is one day of the created trend.
type TrendPoint struct {
	Date    string `json:"date"` // YYYY-MM-DD
	Created int    `json:"created"`
}

// Threat is one threat flattened into the fields a dashboard row needs. The
// raw API nests these across threatInfo / agentDetectionInfo /
// agentRealtimeInfo; flattening here keeps that shape out of the frontend.
type Threat struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Path           string   `json:"path,omitempty"`
	Classification string   `json:"classification,omitempty"`
	Verdict        string   `json:"verdict,omitempty"`
	VerdictLabel   string   `json:"verdictLabel,omitempty"`
	Confidence     string   `json:"confidence,omitempty"`
	Status         string   `json:"status,omitempty"`
	StatusLabel    string   `json:"statusLabel,omitempty"`
	Mitigation     string   `json:"mitigation,omitempty"`
	MitigationLbl  string   `json:"mitigationLabel,omitempty"`
	DetectionType  string   `json:"detectionType,omitempty"`
	Engines        []string `json:"engines,omitempty"`
	Publisher      string   `json:"publisher,omitempty"`
	SHA1           string   `json:"sha1,omitempty"`
	Storyline      string   `json:"storyline,omitempty"`
	Endpoint       string   `json:"endpoint,omitempty"`
	OS             string   `json:"os,omitempty"`
	MachineType    string   `json:"machineType,omitempty"`
	Domain         string   `json:"domain,omitempty"`
	Site           string   `json:"site,omitempty"`
	Group          string   `json:"group,omitempty"`
	AgentVersion   string   `json:"agentVersion,omitempty"`
	AgentInfected  bool     `json:"agentInfected,omitempty"`
	CreatedAt      string   `json:"createdAt,omitempty"`
	IdentifiedAt   string   `json:"identifiedAt,omitempty"`
	UpdatedAt      string   `json:"updatedAt,omitempty"`
	AutoResolved   bool     `json:"autoResolved,omitempty"`
	PendingActions bool     `json:"pendingActions,omitempty"`
	RebootRequired bool     `json:"rebootRequired,omitempty"`
	FailedActions  bool     `json:"failedActions,omitempty"`
	// FailedMitigations lists "action/status" pairs that did not succeed, e.g.
	// "quarantine/failed". A threat the console believes it handled but whose
	// quarantine actually failed is the single most important row on the page.
	FailedMitigations []string `json:"failedMitigations,omitempty"`
	// NeedsAction is the combination actually worth chasing: unmitigated and
	// not written off as benign or a false positive.
	NeedsAction bool `json:"needsAction,omitempty"`
}

// ThreatCounts is the headline row, from the console's own summary endpoint so
// the numbers match what an analyst sees when they log in.
type ThreatCounts struct {
	Total                 int `json:"total"`
	NotResolved           int `json:"notResolved"`
	InProgress            int `json:"inProgress"`
	Resolved              int `json:"resolved"`
	MaliciousNotResolved  int `json:"maliciousNotResolved"`
	SuspiciousNotResolved int `json:"suspiciousNotResolved"`
	NotMitigated          int `json:"notMitigated"`
	NewToday              int `json:"newToday"`
	NewThisWeek           int `json:"newThisWeek"`
	// NeedsAction counts OPEN threats that are not mitigated and have not been
	// written off as benign or a false positive: the ones where something may
	// still be sitting on a machine. It is the number that matters most, and
	// it is computed from the queue rather than from the estate summary.
	NeedsAction int `json:"needsAction"`
}

// AgentCounts is endpoint fleet health, from /private/agents/summary - the
// whole panel in one request.
type AgentCounts struct {
	Total          int `json:"total"`
	Online         int `json:"online"`
	Offline        int `json:"offline"`
	Infected       int `json:"infected"`
	UpToDate       int `json:"upToDate"`
	OutOfDate      int `json:"outOfDate"`
	Decommissioned int `json:"decommissioned"`
	// Licences come from /sites and are the one number that bites without
	// warning: detections keep working until the count runs out.
	ActiveLicenses int `json:"activeLicenses"`
	TotalLicenses  int `json:"totalLicenses"`
}

// Snapshot is the whole payload served at /api/v1/s1/metrics.
type Snapshot struct {
	Configured bool   `json:"configured"`
	OK         bool   `json:"ok"`
	Status     string `json:"status"` // healthy | degraded | down | unknown
	BaseURL    string `json:"baseUrl,omitempty"`
	Console    string `json:"console,omitempty"`
	Account    string `json:"account,omitempty"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
	Error      string `json:"error,omitempty"`

	Threats ThreatCounts `json:"threats"`
	Agents  AgentCounts  `json:"agents"`

	// Facets over the WHOLE estate, each from one countOnly request.
	AllVerdict        []NameCount `json:"allVerdict"`
	AllConfidence     []NameCount `json:"allConfidence"`
	AllClassification []NameCount `json:"allClassification"`
	AllMitigation     []NameCount `json:"allMitigation"`

	// Facets over the OPEN queue, tallied from the rows already fetched so
	// they are guaranteed consistent with the list on screen.
	OpenVerdict        []NameCount `json:"openVerdict"`
	OpenClassification []NameCount `json:"openClassification"`
	OpenMitigation     []NameCount `json:"openMitigation"`
	ByEndpoint         []NameCount `json:"byEndpoint"`
	ByOS               []NameCount `json:"byOs"`
	ByMachineType      []NameCount `json:"byMachineType"`
	ByAction           []NameCount `json:"byAction"`

	Trend []TrendPoint `json:"trend"`

	// Open is the working queue: unresolved and in-progress threats, newest
	// first. Resolved threats are not carried; there are thousands and the
	// counts above already say how many.
	Open          []Threat `json:"open"`
	OpenTruncated bool     `json:"openTruncated,omitempty"`

	// SingleSite records that this tenant has only one site and group, which
	// is why there is no per-site breakdown. Without it, a missing panel looks
	// like a bug rather than a fact about the estate.
	SingleSite bool `json:"singleSite,omitempty"`
}

// --- Snapshot store + live subscribers ------------------------------------

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

// --- Configuration --------------------------------------------------------

type config struct {
	baseURL      string
	token        string
	pollInterval time.Duration
	trendDays    int
	maxOpen      int
}

func loadConfig() config {
	poll := 60
	if n, err := strconv.Atoi(os.Getenv("S1_POLL_SECONDS")); err == nil && n >= 30 {
		poll = n
	}
	trend := 14
	if n, err := strconv.Atoi(os.Getenv("S1_TREND_DAYS")); err == nil && n >= 5 && n <= 60 {
		trend = n
	}
	maxOpen := 1000
	if n, err := strconv.Atoi(os.Getenv("S1_MAX_OPEN")); err == nil && n >= 100 {
		maxOpen = n
	}
	return config{
		// The token's own `sub` claim names a mgmt-<id> host, but that is NOT
		// necessarily the tenant's console, and pointing at the wrong one
		// returns a perfectly formatted 401 that reads exactly like a bad
		// token. This must be the URL you log into, e.g.
		// https://usea1-example.sentinelone.net
		baseURL:      strings.TrimRight(strings.TrimSpace(os.Getenv("S1_BASE_URL")), "/"),
		token:        strings.TrimSpace(os.Getenv("S1_API_TOKEN")),
		pollInterval: time.Duration(poll) * time.Second,
		trendDays:    trend,
		maxOpen:      maxOpen,
	}
}

func (c config) configured() bool {
	return c.baseURL != "" && c.token != ""
}

// --- Client ---------------------------------------------------------------

type client struct {
	cfg  config
	http *http.Client
}

func newClient(cfg config) *client {
	return &client{
		cfg: cfg,
		// 90s, not the usual 10-30s: the same query on this API has been
		// measured at anywhere from 0.2s to 24s, and a short timeout turns
		// ordinary slowness into a reported outage.
		http: &http.Client{Timeout: 90 * time.Second},
	}
}

func (c *client) get(ctx context.Context, path string, query url.Values, out any) error {
	full := c.cfg.baseURL + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return err
	}
	// SentinelOne's scheme is "ApiToken", not "Bearer".
	req.Header.Set("Authorization", "ApiToken "+c.cfg.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 24<<20))
	if err != nil {
		// Reading the body can fail partway through on large pages, which is
		// why page sizes here stay well under the documented maximum.
		return fmt.Errorf("%s: truncated response: %w", path, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("401 unauthorized — check S1_API_TOKEN, and that S1_BASE_URL is the console you log into (currently %s)", c.cfg.baseURL)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s returned %d: %s", path, resp.StatusCode, snippet(body))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s returned unparseable JSON: %w", path, err)
	}
	return nil
}

// count runs a countOnly query and returns pagination.totalItems. This is the
// cheap primitive the whole page is built on: a ~60 byte response that honours
// every filter, so no count requires paging threat bodies.
func (c *client) count(ctx context.Context, q url.Values) (int, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("countOnly", "true")
	var out struct {
		Pagination struct {
			TotalItems int `json:"totalItems"`
		} `json:"pagination"`
	}
	if err := c.get(ctx, "/web/api/v2.1/threats", q, &out); err != nil {
		return 0, err
	}
	return out.Pagination.TotalItems, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// --- Poller ---------------------------------------------------------------

// StartPoller launches the background polling loop. Safe to call unconditionally.
func StartPoller() {
	cfg := loadConfig()
	if !cfg.configured() {
		logr.Info("[s1.StartPoller] SentinelOne is not configured (set S1_BASE_URL, S1_API_TOKEN) — the /s1 page will show a not-configured state")
		setSnapshot(Snapshot{Configured: false, Status: "unknown"})
		return
	}
	logr.Infof("[s1.StartPoller] Polling %s every %s", cfg.baseURL, cfg.pollInterval)
	go func() {
		poll(cfg)
		ticker := time.NewTicker(cfg.pollInterval)
		defer ticker.Stop()
		for range ticker.C {
			func() {
				defer func() {
					if r := recover(); r != nil {
						logr.Errorf("[s1.poll] recovered from panic: %v", r)
					}
				}()
				poll(cfg)
			}()
		}
	}()
}

func poll(cfg config) {
	cl := newClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Verify auth first so a bad token or the wrong console reads as exactly
	// that, rather than as an estate with nothing in it.
	if err := cl.ping(ctx); err != nil {
		setSnapshot(Snapshot{
			Configured: true,
			OK:         false,
			Status:     "down",
			BaseURL:    cfg.baseURL,
			UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
			Error:      err.Error(),
		})
		logr.Warnf("[s1.poll] authentication/reachability check failed: %s", err.Error())
		return
	}

	snap := Snapshot{
		Configured: true,
		BaseURL:    cfg.baseURL,
		Console:    consoleName(cfg.baseURL),
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	var mu sync.Mutex
	var firstErr error
	note := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}

	// Everything below is independent, and this API has latency spikes to 24s,
	// so the calls run concurrently with a small bound. Few enough requests
	// that self-throttling is unnecessary - there are no rate-limit headers to
	// react to, so the mitigation is simply not to be greedy.
	//
	// IMPORTANT: every goroutine writes into its OWN local, and the snapshot is
	// assembled once after the pool drains. Nothing here writes a field of a
	// struct that another goroutine assigns wholesale. An earlier version had
	// one goroutine doing `snap.Agents = summary` while another did
	// `snap.Agents.ActiveLicenses = n`, and whichever finished last won - which
	// presented as licences reading "0 of 0" while the API plainly returned
	// 956 of 1000. The same shape silently affected the two "new threats"
	// counts, which were correct only by luck of scheduling.
	var (
		counts      ThreatCounts
		agents      AgentCounts
		singleSite  bool
		licActive   int
		licTotal    int
		newToday    int
		newThisWeek int
		needsAction int
		trendPts    []TrendPoint
		openQueue   []Threat
		openTrunc   bool
		facets      = map[string][]NameCount{}
	)

	run := newPool(6)

	run.do(func() {
		got, err := cl.threatSummary(ctx)
		note(err)
		mu.Lock()
		counts = got
		mu.Unlock()
	})
	run.do(func() {
		got, err := cl.agentSummary(ctx)
		note(err)
		mu.Lock()
		agents = got
		mu.Unlock()
	})
	run.do(func() {
		single, active, total, err := cl.siteInfo(ctx)
		note(err)
		mu.Lock()
		singleSite, licActive, licTotal = single, active, total
		mu.Unlock()
	})
	run.do(func() {
		n, err := cl.count(ctx, url.Values{"createdAt__gte": {s1Time(startOfLocalDay(0))}})
		note(err)
		mu.Lock()
		newToday = n
		mu.Unlock()
	})
	run.do(func() {
		n, err := cl.count(ctx, url.Values{"createdAt__gte": {s1Time(startOfLocalDay(6))}})
		note(err)
		mu.Lock()
		newThisWeek = n
		mu.Unlock()
	})

	// Estate-wide facets, one tiny request each.
	facet := func(key, param string, values []string) {
		run.do(func() {
			rows := make([]NameCount, 0, len(values))
			for _, v := range values {
				n, err := cl.count(ctx, url.Values{param: {v}})
				note(err)
				rows = append(rows, NameCount{Name: prettyEnum(v), Count: n})
			}
			sortTally(rows)
			mu.Lock()
			facets[key] = rows
			mu.Unlock()
		})
	}
	facet("verdict", "analystVerdicts", verdictValues)
	facet("confidence", "confidenceLevels", confidenceValues)
	facet("classification", "classifications", classificationValues)
	facet("mitigation", "mitigationStatuses", mitigationValues)

	run.do(func() {
		got, err := cl.trend(ctx, cfg)
		note(err)
		mu.Lock()
		trendPts = got
		mu.Unlock()
	})
	run.do(func() {
		got, truncated, err := cl.openThreats(ctx, cfg)
		note(err)
		mu.Lock()
		openQueue, openTrunc = got, truncated
		mu.Unlock()
	})

	run.wait()

	// Assembled here, single-threaded, so the composition is obvious and no
	// field can be clobbered by a sibling.
	snap.Threats = counts
	snap.Threats.NewToday = newToday
	snap.Threats.NewThisWeek = newThisWeek
	snap.Agents = agents
	snap.Agents.ActiveLicenses = licActive
	snap.Agents.TotalLicenses = licTotal
	snap.SingleSite = singleSite
	snap.Trend = trendPts
	snap.Open = openQueue
	snap.OpenTruncated = openTrunc
	snap.AllVerdict = facets["verdict"]
	snap.AllConfidence = facets["confidence"]
	snap.AllClassification = facets["classification"]
	snap.AllMitigation = facets["mitigation"]
	needsAction = applyOpenBreakdowns(&snap, openQueue)
	snap.Threats.NeedsAction = needsAction

	if firstErr != nil {
		snap.OK = false
		snap.Status = "down"
		snap.Error = firstErr.Error()
		logr.Warnf("[s1.poll] refresh had errors: %s", firstErr.Error())
		setSnapshot(snap)
		return
	}
	snap.OK = true
	snap.Status = healthOf(snap)
	setSnapshot(snap)
}

// pool is a tiny bounded worker helper.
type pool struct {
	sem chan struct{}
	wg  sync.WaitGroup
}

func newPool(n int) *pool { return &pool{sem: make(chan struct{}, n)} }

func (p *pool) do(fn func()) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.sem <- struct{}{}
		defer func() { <-p.sem }()
		defer func() {
			if r := recover(); r != nil {
				logr.Errorf("[s1.pool] recovered from panic: %v", r)
			}
		}()
		fn()
	}()
}

func (p *pool) wait() { p.wg.Wait() }

// The enum values in use, as observed in the live console. Held as constants
// because `classifications` is the one filter this API does NOT validate: a
// typo there returns 200 with an empty result rather than an error, so a
// misspelling would quietly read as zero threats of that kind.
var (
	verdictValues        = []string{"true_positive", "false_positive", "suspicious", "undefined"}
	confidenceValues     = []string{"malicious", "suspicious"}
	classificationValues = []string{"General", "Malware", "Ransomware"}
	mitigationValues     = []string{"mitigated", "not_mitigated", "marked_as_benign"}
)

func (c *client) ping(ctx context.Context) error {
	var out struct {
		Data struct {
			LatestAgentVersion string `json:"latestAgentVersion"`
		} `json:"data"`
	}
	return c.get(ctx, "/web/api/v2.1/system/info", nil, &out)
}

// consoleName is the subdomain an analyst recognises ("usea1-example").
func consoleName(base string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	if i := strings.Index(host, "."); i > 0 {
		host = host[:i]
	}
	return host
}

func (c *client) threatSummary(ctx context.Context) (ThreatCounts, error) {
	var out struct {
		Data struct {
			Total                 int `json:"total"`
			NotResolved           int `json:"notResolved"`
			InProgress            int `json:"inProgress"`
			Resolved              int `json:"resolved"`
			MaliciousNotResolved  int `json:"maliciousNotResolved"`
			SuspiciousNotResolved int `json:"suspiciousNotResolved"`
			NotMitigated          int `json:"notMitigated"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/web/api/v2.1/private/threats/summary", nil, &out); err != nil {
		return ThreatCounts{}, err
	}
	d := out.Data
	return ThreatCounts{
		Total:                 d.Total,
		NotResolved:           d.NotResolved,
		InProgress:            d.InProgress,
		Resolved:              d.Resolved,
		MaliciousNotResolved:  d.MaliciousNotResolved,
		SuspiciousNotResolved: d.SuspiciousNotResolved,
		NotMitigated:          d.NotMitigated,
	}, nil
}

// agentSummary reads the whole fleet panel in one request.
func (c *client) agentSummary(ctx context.Context) (AgentCounts, error) {
	var out struct {
		Data struct {
			Total          int `json:"total"`
			Online         int `json:"online"`
			Infected       int `json:"infected"`
			UpToDate       int `json:"upToDate"`
			OutOfDate      int `json:"outOfDate"`
			Decommissioned int `json:"decommissioned"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/web/api/v2.1/private/agents/summary", nil, &out); err != nil {
		return AgentCounts{}, err
	}
	d := out.Data
	return AgentCounts{
		Total:          d.Total,
		Online:         d.Online,
		Offline:        max0(d.Total - d.Online),
		Infected:       d.Infected,
		UpToDate:       d.UpToDate,
		OutOfDate:      d.OutOfDate,
		Decommissioned: d.Decommissioned,
	}, nil
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// siteInfo reports whether the tenant has a single site (which is why there is
// no per-site breakdown) and the licence position.
//
// Note: the /sites payload also carries a registrationToken per site. It is
// deliberately not read or stored here - it is a credential that would enrol
// new agents, and it has no business in a dashboard snapshot.
func (c *client) siteInfo(ctx context.Context) (single bool, active, total int, err error) {
	var out struct {
		Data struct {
			Sites []struct {
				Name           string `json:"name"`
				ActiveLicenses int    `json:"activeLicenses"`
				TotalLicenses  int    `json:"totalLicenses"`
			} `json:"sites"`
			AllSites struct {
				ActiveLicenses int `json:"activeLicenses"`
				TotalLicenses  int `json:"totalLicenses"`
			} `json:"allSites"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/web/api/v2.1/sites", url.Values{"limit": {"100"}}, &out); err != nil {
		return false, 0, 0, err
	}
	// The licence counts are on each SITE, not on allSites: allSites reports
	// activeLicenses as 0 even when the site itself reports 956, so reading
	// the summary object gives a confidently wrong "0 of 1000 in use".
	for _, site := range out.Data.Sites {
		active += site.ActiveLicenses
		total += site.TotalLicenses
	}
	if n := out.Data.AllSites.TotalLicenses; n > 0 {
		total = n
	}
	return len(out.Data.Sites) <= 1, active, total, nil
}

// healthOf grades the estate.
//
// Deliberately graded on what is actually unsafe rather than on what is merely
// unread: an infected endpoint or a threat that was never mitigated is a
// problem, while an unresolved-but-mitigated threat is triage work. Treating
// the triage backlog as an outage would leave this page permanently red and
// therefore useless.
func healthOf(s Snapshot) string {
	switch {
	case s.Agents.Infected > 0:
		return "down"
	// NeedsAction and failed mitigations are computed from the OPEN queue.
	// The summary's estate-wide notMitigated is deliberately not used here: it
	// counts resolved threats too, and an analyst resolving something as
	// not-needing-mitigation is a decision, not a fault. Grading on it left
	// this page permanently amber on an estate where every open threat was
	// already contained.
	case s.Threats.NeedsAction > 0 || hasFailedMitigation(s.Open):
		return "degraded"
	case s.Threats.MaliciousNotResolved > 0:
		return "degraded"
	default:
		return "healthy"
	}
}

func hasFailedMitigation(open []Threat) bool {
	for _, t := range open {
		if len(t.FailedMitigations) > 0 {
			return true
		}
	}
	return false
}

// rawThreat mirrors only the parts of the API object this package reads.
type rawThreat struct {
	ID         string `json:"id"`
	ThreatInfo struct {
		ThreatName                string   `json:"threatName"`
		FilePath                  string   `json:"filePath"`
		Classification            string   `json:"classification"`
		AnalystVerdict            string   `json:"analystVerdict"`
		AnalystVerdictDescription string   `json:"analystVerdictDescription"`
		ConfidenceLevel           string   `json:"confidenceLevel"`
		IncidentStatus            string   `json:"incidentStatus"`
		IncidentStatusDescription string   `json:"incidentStatusDescription"`
		MitigationStatus          string   `json:"mitigationStatus"`
		MitigationStatusDesc      string   `json:"mitigationStatusDescription"`
		DetectionType             string   `json:"detectionType"`
		Engines                   []string `json:"engines"`
		PublisherName             string   `json:"publisherName"`
		SHA1                      string   `json:"sha1"`
		Storyline                 string   `json:"storyline"`
		CreatedAt                 string   `json:"createdAt"`
		IdentifiedAt              string   `json:"identifiedAt"`
		UpdatedAt                 string   `json:"updatedAt"`
		AutomaticallyResolved     bool     `json:"automaticallyResolved"`
		PendingActions            bool     `json:"pendingActions"`
		RebootRequired            bool     `json:"rebootRequired"`
		FailedActions             bool     `json:"failedActions"`
	} `json:"threatInfo"`
	// Per-action mitigation rows: (action, status) pairs such as
	// quarantine/failed, which the scalar mitigationStatus above does not show.
	MitigationStatus []struct {
		Action string `json:"action"`
		Status string `json:"status"`
	} `json:"mitigationStatus"`
	AgentRealtimeInfo struct {
		AgentComputerName string `json:"agentComputerName"`
		AgentOSName       string `json:"agentOsName"`
		AgentMachineType  string `json:"agentMachineType"`
		AgentDomain       string `json:"agentDomain"`
		AgentVersion      string `json:"agentVersion"`
		AgentInfected     bool   `json:"agentInfected"`
		SiteName          string `json:"siteName"`
		GroupName         string `json:"groupName"`
	} `json:"agentRealtimeInfo"`
	AgentDetectionInfo struct {
		AgentOSName  string `json:"agentOsName"`
		AgentDomain  string `json:"agentDomain"`
		AgentVersion string `json:"agentVersion"`
		SiteName     string `json:"siteName"`
		GroupName    string `json:"groupName"`
	} `json:"agentDetectionInfo"`
}

// flatten prefers the realtime agent fields and falls back to the
// detection-time ones. They differ when a machine has been renamed or moved
// since the detection, and the realtime value is the one that helps you find
// the machine today. Note the computer name exists ONLY on the realtime block.
func (r rawThreat) flatten() Threat {
	pick := func(now, then string) string {
		if strings.TrimSpace(now) != "" {
			return now
		}
		return then
	}
	ti := r.ThreatInfo
	t := Threat{
		ID:             r.ID,
		Name:           ti.ThreatName,
		Path:           ti.FilePath,
		Classification: ti.Classification,
		Verdict:        ti.AnalystVerdict,
		VerdictLabel:   ti.AnalystVerdictDescription,
		Confidence:     ti.ConfidenceLevel,
		Status:         ti.IncidentStatus,
		StatusLabel:    ti.IncidentStatusDescription,
		Mitigation:     ti.MitigationStatus,
		MitigationLbl:  ti.MitigationStatusDesc,
		DetectionType:  ti.DetectionType,
		Engines:        ti.Engines,
		Publisher:      ti.PublisherName,
		SHA1:           ti.SHA1,
		Storyline:      ti.Storyline,
		CreatedAt:      ti.CreatedAt,
		IdentifiedAt:   ti.IdentifiedAt,
		UpdatedAt:      ti.UpdatedAt,
		AutoResolved:   ti.AutomaticallyResolved,
		PendingActions: ti.PendingActions,
		RebootRequired: ti.RebootRequired,
		FailedActions:  ti.FailedActions,
		Endpoint:       r.AgentRealtimeInfo.AgentComputerName,
		MachineType:    r.AgentRealtimeInfo.AgentMachineType,
		AgentInfected:  r.AgentRealtimeInfo.AgentInfected,
		OS:             pick(r.AgentRealtimeInfo.AgentOSName, r.AgentDetectionInfo.AgentOSName),
		Domain:         pick(r.AgentRealtimeInfo.AgentDomain, r.AgentDetectionInfo.AgentDomain),
		AgentVersion:   pick(r.AgentRealtimeInfo.AgentVersion, r.AgentDetectionInfo.AgentVersion),
		Site:           pick(r.AgentRealtimeInfo.SiteName, r.AgentDetectionInfo.SiteName),
		Group:          pick(r.AgentRealtimeInfo.GroupName, r.AgentDetectionInfo.GroupName),
	}
	for _, m := range r.MitigationStatus {
		if m.Status != "" && m.Status != "success" {
			t.FailedMitigations = append(t.FailedMitigations, m.Action+"/"+m.Status)
		}
	}
	t.NeedsAction = ti.MitigationStatus != "mitigated" &&
		ti.MitigationStatus != "marked_as_benign" &&
		ti.AnalystVerdict != "false_positive"
	return t
}

// openThreats fetches the working queue: everything not resolved, newest first.
//
// Page size 100. The documented maximum is 1000, but limit=1000 passes
// validation and then truncates the response mid-stream, so it is not used.
func (c *client) openThreats(ctx context.Context, cfg config) ([]Threat, bool, error) {
	var out []Threat
	cursor := ""
	for len(out) < cfg.maxOpen {
		pageSize := 100
		if remaining := cfg.maxOpen - len(out); remaining < pageSize {
			pageSize = remaining
		}
		q := url.Values{}
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("sortBy", "createdAt")
		q.Set("sortOrder", "desc")
		q.Set("incidentStatuses", "unresolved,in_progress")
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page struct {
			Data       []rawThreat `json:"data"`
			Pagination struct {
				NextCursor string `json:"nextCursor"`
			} `json:"pagination"`
		}
		if err := c.get(ctx, "/web/api/v2.1/threats", q, &page); err != nil {
			return out, false, err
		}
		for _, r := range page.Data {
			out = append(out, r.flatten())
		}
		if page.Pagination.NextCursor == "" || len(page.Data) == 0 {
			return out, false, nil
		}
		cursor = page.Pagination.NextCursor
	}
	return out, true, nil
}

// applyOpenBreakdowns tallies the open queue from rows already fetched, so the
// facets cannot disagree with the list shown beside them.
// Returns the needs-action count rather than writing it into s.Threats, which
// is assembled by the caller. Called single-threaded, after the pool drains.
func applyOpenBreakdowns(s *Snapshot, open []Threat) int {
	verdict := map[string]int{}
	classification := map[string]int{}
	mitigation := map[string]int{}
	endpoint := map[string]int{}
	osName := map[string]int{}
	machine := map[string]int{}
	action := map[string]int{}

	needsAction := 0
	for _, t := range open {
		if t.NeedsAction {
			needsAction++
		}
		verdict[labelOr(t.VerdictLabel, t.Verdict, "Undefined")]++
		classification[labelOr("", t.Classification, "Unclassified")]++
		mitigation[labelOr(t.MitigationLbl, t.Mitigation, "Unknown")]++
		osName[labelOr("", t.OS, "Unknown OS")]++
		machine[labelOr("", t.MachineType, "Unknown")]++
		if t.Endpoint != "" {
			endpoint[t.Endpoint]++
		}
		for _, a := range t.FailedMitigations {
			action[a]++
		}
	}
	s.OpenVerdict = rank(verdict, 0)
	s.OpenClassification = rank(classification, 0)
	s.OpenMitigation = rank(mitigation, 0)
	s.ByOS = rank(osName, 8)
	s.ByMachineType = rank(machine, 0)
	s.ByAction = rank(action, 0)
	// Only the worst offenders: a list of 900 machines with one detection each
	// is not information.
	s.ByEndpoint = rank(endpoint, 12)
	return needsAction
}

func labelOr(label, raw, fallback string) string {
	if strings.TrimSpace(label) != "" {
		return label
	}
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	return prettyEnum(raw)
}

// prettyEnum turns "true_positive" into "True positive". The API's own
// *Description fields are used where they exist; this is for the raw enums.
func prettyEnum(v string) string {
	if v == "" {
		return ""
	}
	s := strings.ReplaceAll(v, "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

func sortTally(rows []NameCount) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Name < rows[j].Name
	})
}

// rank sorts a tally by count, name as the tie-break so the order is stable
// between polls. limit > 0 keeps only the top N.
func rank(m map[string]int, limit int) []NameCount {
	out := make([]NameCount, 0, len(m))
	for name, count := range m {
		out = append(out, NameCount{Name: name, Count: count})
	}
	sortTally(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// startOfLocalDay returns midnight, n days ago, in the server's local zone.
func startOfLocalDay(daysAgo int) time.Time {
	now := time.Now().AddDate(0, 0, -daysAgo)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

// s1Time formats a timestamp the way this API demands. A bare date is rejected
// ("Not a valid datetime"), so the full microsecond form in UTC is used.
func s1Time(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000Z")
}

// trend counts threats created per day, one countOnly request per day.
//
// Paging the threats themselves and bucketing locally would cost dozens of
// multi-megabyte pages; this is N tiny requests instead, and each one is the
// console's own count rather than something derived here.
func (c *client) trend(ctx context.Context, cfg config) ([]TrendPoint, error) {
	out := make([]TrendPoint, 0, cfg.trendDays)
	var firstErr error
	for i := cfg.trendDays - 1; i >= 0; i-- {
		start := startOfLocalDay(i)
		end := start.AddDate(0, 0, 1)
		n, err := c.count(ctx, url.Values{
			"createdAt__gte": {s1Time(start)},
			"createdAt__lt":  {s1Time(end)},
		})
		if err != nil && firstErr == nil {
			firstErr = err
		}
		out = append(out, TrendPoint{Date: start.Format("2006-01-02"), Created: n})
	}
	return out, firstErr
}

// --- On-demand threat detail ----------------------------------------------

// ThreatDetail is one threat plus its activity timeline, fetched on demand for
// the drill-in panel rather than carried in every snapshot.
type ThreatDetail struct {
	OK       bool           `json:"ok"`
	Error    string         `json:"error,omitempty"`
	Threat   Threat         `json:"threat"`
	Timeline []TimelineItem `json:"timeline"`
}

// TimelineItem is one activity entry: this API's analogue of a ticket comment.
// Notes exist as an endpoint but are empty in this tenant, whereas the timeline
// is populated, so the timeline is what the drill-in shows.
type TimelineItem struct {
	At        string `json:"at,omitempty"`
	Primary   string `json:"primary"`
	Secondary string `json:"secondary,omitempty"`
}

// FetchThreat returns one threat with its timeline.
func FetchThreat(ctx context.Context, id string) ThreatDetail {
	cfg := loadConfig()
	if !cfg.configured() {
		return ThreatDetail{Error: "SentinelOne is not configured"}
	}
	cl := newClient(cfg)

	// There is no GET /threats/{id} - that path 404s. A single threat is
	// fetched by filtering the collection.
	var page struct {
		Data []rawThreat `json:"data"`
	}
	if err := cl.get(ctx, "/web/api/v2.1/threats", url.Values{"ids": {id}}, &page); err != nil {
		return ThreatDetail{Error: err.Error()}
	}
	if len(page.Data) == 0 {
		return ThreatDetail{Error: "threat " + id + " not found"}
	}
	out := ThreatDetail{OK: true, Threat: page.Data[0].flatten()}

	// The timeline is a nice-to-have: a threat whose timeline cannot be read is
	// still worth showing, so this failure is not fatal.
	var tl struct {
		Data []struct {
			CreatedAt            string `json:"createdAt"`
			PrimaryDescription   string `json:"primaryDescription"`
			SecondaryDescription string `json:"secondaryDescription"`
		} `json:"data"`
	}
	if err := cl.get(ctx, "/web/api/v2.1/threats/"+url.PathEscape(id)+"/timeline",
		url.Values{"limit": {"50"}}, &tl); err == nil {
		for _, e := range tl.Data {
			out.Timeline = append(out.Timeline, TimelineItem{
				At:        e.CreatedAt,
				Primary:   e.PrimaryDescription,
				Secondary: e.SecondaryDescription,
			})
		}
	}
	return out
}
