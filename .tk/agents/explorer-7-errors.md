# Explorer 7 — Error handling, resilience & edge-case patterns

Scope: the custom features bolted onto this Gatus fork (`jira/`, `api/jira.go`,
`api/unifi_inventory.go`, `api/phones_inventory.go`, `monitoring/`, and their Vue
counterparts), read as a house style for a new **LL-Telemetry** integration.

There are **two distinct architectures** in this fork, and LL-Telemetry must pick one:

| | **Pull** (Jira) | **Push** (UniFi / Phones) |
|---|---|---|
| Who calls whom | Gatus → third-party API | Collector → Gatus |
| Files | `jira/jira.go`, `jira/board.go`, `jira/issue.go`, `api/jira.go` | `api/unifi_inventory.go`, `api/phones_inventory.go` |
| Failure surface | upstream down/401/slow → poller | collector stops posting → data goes stale |
| Cache | yes, mandatory (poller + TTL hubs) | the store *is* the cache |

If LL-Telemetry's server is polled by Gatus, copy the **Jira** patterns. If a
collector pushes to Gatus, copy the **UniFi** patterns (much simpler).

---

## 1. Backend error handling

### 1.1 The house error-response shape: **envelope, not HTTP status**

The dominant convention is: **always return 200 and put the failure in the payload**.
The UI is a wall dashboard — an HTTP error blanks a panel, an envelope lets it
render "last known good + why it's stale".

`jira/jira.go:90-99` — the canonical envelope:

```go
// Snapshot is the full payload served at /api/v1/jira/metrics.
type Snapshot struct {
	Configured bool      `json:"configured"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	Status     string    `json:"status"` // healthy | degraded | down | unknown
	UpdatedAt  string    `json:"updatedAt,omitempty"`
	BaseURL    string    `json:"baseUrl,omitempty"`
	Account    string    `json:"account,omitempty"`
	Projects   []Project `json:"projects"`
}
```

`jira/board.go:92-113` repeats the same four leading fields on both board payloads:

```go
type BoardSnapshot struct {
	Configured     bool          `json:"configured"`
	OK             bool          `json:"ok"`
	Error          string        `json:"error,omitempty"`
	...
}

type BoardListResult struct {
	Configured bool       `json:"configured"`
	OK         bool       `json:"ok"`
	Error      string     `json:"error,omitempty"`
	Boards     []BoardRef `json:"boards"`
}
```

**The tri-state is deliberate and is the thing to copy:**
- `configured:false` → "not set up yet" (a setup notice, not an error)
- `configured:true, ok:false` → "set up, but we can't reach it" (+ `error` string)
- `configured:true, ok:true` → data; `error` may *still* be non-empty = partial data

### 1.2 Handlers: who returns what

`api/jira.go` — three of the five handlers never return a non-2xx for upstream failure.
The comments state the rule explicitly:

`api/jira.go:15-20`:
```go
// GetJiraMetrics returns the latest cached Jira snapshot polled by the background
// jira poller. Always 200: the payload's `configured`/`ok` fields tell the UI
// whether Jira is set up and whether the last refresh succeeded.
func GetJiraMetrics(c *fiber.Ctx) error {
	return c.Status(200).JSON(jira.GetSnapshot())
}
```

`api/jira.go:43-54` — 400 for a *caller* error, 200 for an *upstream* error:
```go
// GetJiraBoard returns one board: its columns exactly as configured in Jira
// (order, status mapping, WIP constraints) with the current cards placed in them.
// Always 200 on a valid id; the payload's `ok`/`error` carry the failure.
func GetJiraBoard(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil || id <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "board id must be a positive integer"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return c.Status(200).JSON(jira.GetBoard(ctx, id))
}
```

**The one exception** — `GetJiraIssue` is a *synchronous, on-demand, single-item*
fetch with no cache behind it, so it does propagate upstream failure as **502**
(`api/jira.go:24-33`):
```go
	detail, err := jira.FetchIssue(ctx, key)
	if err != nil {
		return c.Status(502).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(200).JSON(detail)
```

**Rule to carry into LL-Telemetry:** cached/aggregate reads → 200 + envelope.
Uncached on-demand single-item reads → `502 {"error": "..."}`. Bad params → `400 {"error": "..."}`.

Note the minor inconsistency to be aware of: the ad-hoc failure shape is
`fiber.Map{"error": ...}` (`api/jira.go:30,49,62`; `api/unifi_inventory.go:98`;
`api/phones_inventory.go:96`) whereas push handlers use **plain text**
`c.Status(401).SendString("invalid token")`. Both are in use; JSON `{"error":...}`
is the better default for a new feature.

### 1.3 The four failure modes, concretely

**Upstream down / slow** — `jira/jira.go:417-424`, single `do()` chokepoint:
```go
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s -> %d: %s", method, path, resp.StatusCode, snippet(data))
	}
```
Note `io.LimitReader(resp.Body, 8<<20)` — an **8 MB read cap** so a hostile/broken
upstream can't OOM the process. Copy this.

**Non-2xx (incl. 401)** — the error string embeds method, path, status and a
truncated body. `snippet()` at `jira/jira.go:745-751` bounds it to 300 chars:
```go
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
```
That whole string ends up in `Snapshot.Error` and is rendered verbatim in a `<pre>`
in the UI. **Caution for LL-Telemetry: never let a token/secret reach this path** —
the body snippet is shown to any browser hitting the unauthenticated dashboard.

**Malformed JSON** — `jira/jira.go:426-430`:
```go
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return err
		}
	}
```
Two edge cases handled implicitly: an **empty 200 body is not an error** (`len(data) > 0`
guard), and every nested optional object is a **pointer with a nil check** rather than
a value, so a field Jira omits doesn't panic. See `jira/jira.go:494-510`:
```go
func (ri rawIssue) toIssue() Issue {
	iss := Issue{Key: ri.Key, Summary: ri.Fields.Summary, Created: ri.Fields.Created}
	if ri.Fields.Status != nil {
		iss.Status = ri.Fields.Status.Name
		iss.Category = ri.Fields.Status.StatusCategory.Key
	}
	if ri.Fields.IssueType != nil {
		iss.Type = ri.Fields.IssueType.Name
	}
	...
```
This nil-guard-every-optional-pointer style is used in all four decoders
(`rawIssue`, `boardRawIssue.toCard()` at `board.go:364-392`, `FetchIssue` at
`issue.go:97-112`). It is the house style for "upstream sent us less than we hoped".

**401 specifically** gets its own up-front probe, and the package doc explains why —
`jira/jira.go:7-11`:
```go
// Gatus itself does the polling (we hold the Jira API token). A background
// goroutine authenticates with HTTP Basic (email:token). Authentication is
// verified up front via /rest/api/3/myself, because Jira Cloud does NOT 401 on
// search/count with bad credentials, it silently returns empty anonymous
// results, which would otherwise masquerade as a healthy but all-zero board.
```
Implemented at `jira/jira.go:265-278` — auth failure short-circuits the whole poll
and produces a **remediation-shaped error message**, not a raw one:
```go
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
```
**Lesson for LL-Telemetry: verify that "empty" and "unauthorized" are distinguishable.**
If the telemetry server returns 200-with-empty-array for a bad token, add the same
kind of identity probe.

### 1.4 Partial failure: `firstErr` accumulation

Nothing aborts a multi-step refresh on the first error. The pattern is "collect the
first error, keep going, ship what you have". `jira/jira.go:280-315`:
```go
	var projects []Project
	var firstErr error
	degraded := false
	for _, key := range cfg.projects {
		pm, perr := cl.project(ctx, cfg, key)
		if perr != nil && firstErr == nil {
			firstErr = perr
		}
		...
	}
```
Repeated at `jira/jira.go:319-381` (per-project, six independent sub-queries each
folding into one `firstErr`) and `jira/board.go:188-232`.

The subtlest and most important instance — `jira/board.go:502-506, 575-579`. Cards
loaded but something else failed is `ok:true` **with** an error string:
```go
	inflight, trunc, err := cl.boardIssues(ctx, id, "statusCategory != Done", maxCards)
	if err != nil && len(inflight) == 0 {
		snap.Error = "couldn't read board cards: " + err.Error()
		return snap
	}
	...
	snap.OK = true
	if err != nil {
		snap.Error = err.Error() // partial data: cards loaded, something else hiccuped
	}
	return snap
```
`err != nil && len(x) == 0` — **"only fail if you got nothing"** — appears three times
(`board.go:165` in `ListBoards`, `board.go:228` in `boards`, `board.go:503`). It is the
single most characteristic idiom in this codebase. The frontend has a matching branch
for it (see §5.3).

### 1.5 Best-effort sub-fetches that swallow errors entirely

Where a field is decoration rather than substance, the error is dropped and a nil
slice returned. `jira/issue.go:114-117`:
```go
	// SLA metrics (service desk only; best-effort, never fatal).
	d.SLAs = cl.fetchSLAs(ctx, key)
	// Recent comments (best-effort).
	d.Comments = cl.fetchComments(ctx, key)
```
`jira/issue.go:133-135` and `159-162`:
```go
	if err := c.do(ctx, http.MethodGet, "/rest/servicedeskapi/request/"+url.PathEscape(key)+"/sla", nil, &out); err != nil {
		return nil
	}
```

### 1.6 Push-side (UniFi) error handling

`api/unifi_inventory.go:44-89` is the whole contract — auth, validate, pause-check, store:
```go
		key := c.Params("key")
		externalEndpoint := cfg.GetExternalEndpointByKey(key)
		if externalEndpoint == nil {
			return c.Status(404).SendString("not found")
		}
		authorizationHeader := string(c.Request().Header.Peek("Authorization"))
		if !strings.HasPrefix(authorizationHeader, "Bearer ") {
			return c.Status(401).SendString("invalid Authorization header")
		}
		token := strings.TrimSpace(strings.TrimPrefix(authorizationHeader, "Bearer "))
		if len(token) == 0 || externalEndpoint.Token != token {
			return c.Status(401).SendString("invalid token")
		}
		var payload struct { ... }
		if err := json.Unmarshal(c.Body(), &payload); err != nil || payload.Kind == "" {
			return c.Status(400).SendString(`invalid body: expected {"kind":"firewall|wireless", ...}`)
		}
```
Two things to copy:
- **auth reuses the external-endpoint's push token** — no new secret to manage
- **the 400 message tells the collector the expected shape**, verbatim

And the deliberate 200-on-a-no-op, `api/unifi_inventory.go:69-75`:
```go
		// Paused means paused: freeze the snapshot too, so the drill-in doesn't show
		// live AP and WAN state on a page that says monitoring is stopped. 200 for
		// the same reason the result push returns 200 — a non-200 would make the
		// collector warn on every sweep.
		if monitoring.IsPaused(key) {
			return c.Status(200).SendString("OK (monitoring paused)")
		}
```
**Rule: don't return an error status for a condition the client can't fix** — it just
generates noise in the collector's log every cycle.

`storedUniFi` (`api/unifi_inventory.go:33-40`) keeps the interesting parts as
`json.RawMessage`:
```go
type storedUniFi struct {
	UpdatedAt string          `json:"updatedAt"`
	Kind      string          `json:"kind,omitempty"`   // firewall | wireless
	Status    string          `json:"status,omitempty"` // healthy | degraded | down
	Site      string          `json:"site,omitempty"`
	Counts    json.RawMessage `json:"counts,omitempty"`
	Detail    json.RawMessage `json:"detail,omitempty"` // everything else, verbatim
}
```
Only the fields Go actually reasons about are typed; the rest passes through
opaquely. This means **a collector-side schema change never requires a Go change**
— a very good property for a new telemetry feed. Strongly recommended.

`UpdatedAt` is stamped **server-side** at store time, not taken from the payload —
so a collector with a wrong clock can't make data look fresh.

---

## 2. Timeouts and retries

### 2.1 Timeouts — a two-layer scheme

**Layer 1, transport** — one shared client timeout, `jira/jira.go:391-397`:
```go
func newClient(cfg config) *client {
	return &client{
		cfg:  cfg,
		auth: "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.email+":"+cfg.token)),
		http: &http.Client{Timeout: 25 * time.Second},
	}
}
```
This is the **only** `http.Client{Timeout:...}` in the custom code (verified by grep
across `jira/` and `api/`).

**Layer 2, per-operation `context.WithTimeout`** — bounds the *whole* multi-request
operation, since one operation makes dozens of paginated calls:

| Operation | Timeout | Location |
|---|---|---|
| HTTP transport (per request) | 25s | `jira/jira.go:395` |
| Metrics poll (whole cycle, N projects) | 90s | `jira/jira.go:262` |
| Board fetch, background poller | 60s | `jira/board.go:733` |
| Board fetch, HTTP handler | 60s | `api/jira.go:51` |
| Board list, HTTP handler | 25s | `api/jira.go:38` |
| Single issue drill-down | 20s | `api/jira.go:26` |

Note the handler-scoped ones derive from `context.Background()`, **not** `c.Context()`
— so a browser that disconnects does not cancel the upstream work (which is correct
here, since the result populates a shared cache the next viewer will use).

`ctx` is threaded through every client method (`do`, `me`, `searchAll`, `boardIssues`,
`statuses`, `enrichSLAs`) — `jira/jira.go:408`:
```go
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.baseURL+path, reader)
```

### 2.2 Retries / backoff / circuit breaker: **none exist**

Grepping `jira/ api/jira.go api/unifi_inventory.go api/phones_inventory.go` for
`retry|Retry|backoff|Backoff|circuit` returns **zero hits**.

The deliberate substitute is: **a fixed-interval poller + a last-known-good cache**.
A failed poll is simply not retried — the next tick (default 30s metrics / 20s board)
*is* the retry, and the previous good snapshot stays served in the meantime. This is a
sound design for a dashboard and LL-Telemetry should follow it rather than adding
retry loops inside a poll cycle.

There is no circuit breaker; nothing tracks consecutive failures or backs off when the
upstream is hard-down. **This is the clearest gap** — a dead upstream is polled at full
rate forever. If LL-Telemetry's server is fragile, consider a consecutive-failure
counter that stretches the interval; it would be a genuine improvement, not a
deviation from house style.

### 2.3 Pagination bounds (a real edge case)

Every paginating loop is bounded so a bad `nextPageToken` or a lying `total` can't spin
forever. `jira/jira.go:515-541`:
```go
func (c *client) searchAll(ctx context.Context, jql string, cap int) ([]rawIssue, error) {
	var all []rawIssue
	token := ""
	for len(all) < cap {
		...
		if err := c.do(ctx, http.MethodPost, "/rest/api/3/search/jql", reqBody, &out); err != nil {
			return all, err   // <- partial results returned WITH the error
		}
		all = append(all, out.Issues...)
		if out.NextPageToken == "" || len(out.Issues) == 0 {
			break
		}
		token = out.NextPageToken
	}
	return all, nil
}
```
Note `return all, err` — **partial page results are returned alongside the error**, which
is what makes the `err != nil && len(x) == 0` idiom in §1.4 work.

`boardPage` uses a hard loop bound instead (`jira/board.go:236`): `for startAt := 0; startAt < 200; startAt += 50`.

`boardIssues` (`jira/board.go:400-435`) tracks a `truncated` flag rather than silently
dropping data, and the UI surfaces it as a "capped" chip.

---

## 3. Caching

Three distinct cache flavours, all in-memory, none persisted.

### 3.1 Poller-fed cache (no TTL — always serve the last poll)

`jira/jira.go:101-125`:
```go
var (
	storeMu sync.RWMutex
	store   = Snapshot{Configured: false, Status: "unknown"}
	...
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
```
**Cache miss + upstream failure:** the zero value `Snapshot{Configured:false, Status:"unknown"}`
is served — which the UI renders as the *setup* notice, not an error. Sensible, but note
the subtlety: **on a failed first poll, `setSnapshot` overwrites the store with an
`ok:false` snapshot**, so "never succeeded" and "succeeded once then broke" look the same
apart from `UpdatedAt` being present in the latter. LL-Telemetry can improve on this by
retaining `Projects`/data from the last good poll when a later poll fails — currently the
Jira poller **discards the previous good data on failure** (`jira/jira.go:268` builds a
fresh `Snapshot` with no `Projects`). The UI compensates by keeping its own last-good copy
client-side (§5.5), but the server does not.

### 3.2 Mutex + timestamp TTL cache (the classic)

`jira/board.go:143-171` — board list, 5-minute TTL:
```go
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
```
Two details worth copying: the **error is cached too** (so a hard-down upstream isn't
hammered once per request), and a partial success **clears** the cached error.

Caveat: the mutex is held across the network call, so concurrent first-requests serialise
rather than stampede — crude but effective single-flight.

### 3.3 Stale-beats-nothing fallback (the best pattern in the codebase)

`jira/board.go:279-304` — status catalogue, 10-minute TTL:
```go
func (c *client) statuses(ctx context.Context) (map[string]statusMeta, error) {
	statusMu.Lock()
	defer statusMu.Unlock()
	if statusVal != nil && time.Since(statusAt) < 10*time.Minute {
		return statusVal, nil
	}
	...
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/status", nil, &raw); err != nil {
		if statusVal != nil {
			return statusVal, nil // stale beats nothing
		}
		return nil, err
	}
	m := make(map[string]statusMeta, len(raw))
	...
	statusVal, statusAt = m, time.Now()
	return m, nil
}
```
**"stale beats nothing"** — on refresh failure, expired data is returned as if fresh.
This is exactly the degradation behaviour LL-Telemetry wants. **Cache miss + upstream
failure** is the only path that returns an error.

### 3.4 Per-entity hub cache with freshness check

`jira/board.go:635-677`:
```go
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
```
Note `(h.snap.OK || h.snap.Error != "")` — **a cached *error* counts as fresh**, so a
failing board isn't re-fetched on every request either.

`CachedBoard` (`jira/board.go:766-771`) is the read-only, never-fetch accessor used to
seed the SSE stream instantly:
```go
func CachedBoard(id int) BoardSnapshot {
	h := hubFor(id)
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.snap
}
```

### 3.5 Push-side "cache"

For UniFi/Phones the store *is* the cache and there is **no TTL and no eviction**
(`api/unifi_inventory.go:25-27`):
```go
// Snapshots are ephemeral: the collector re-reports every sweep, and nothing is
// persisted. A key that stops reporting simply goes stale, which the UI shows
// rather than hiding.
```
Staleness is communicated purely by the `updatedAt` field and left to the UI to judge.
Cache miss is a **404 with an explanatory message** (`api/unifi_inventory.go:97-99`):
```go
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "no UniFi snapshot reported yet"})
	}
```

### 3.6 Bulk-read endpoint (a performance pattern worth copying)

`api/unifi_inventory.go:103-115` — one request for the whole page instead of N:
```go
// GetUniFiSnapshots returns every reported snapshot keyed by endpoint key. The
// dashboard calls this once per refresh to fill the trailing metrics on every
// card's Firewall and Wireless rows — one request for the whole page instead of
// two per location.
func GetUniFiSnapshots(c *fiber.Ctx) error {
	unifiMu.RLock()
	out := make(map[string]storedUniFi, len(unifiStore))
	for k, v := range unifiStore {
		out[k] = v
	}
	unifiMu.RUnlock()
	return c.Status(200).JSON(out)
}
```
The map is **copied under the read lock** and serialised outside it. LL-Telemetry
should expose an equivalent `GET /api/v1/telemetry` bulk route if per-card data is needed.

---

## 4. Concurrency

All handlers are concurrency-safe; every shared map/struct is behind a mutex. There is
**no `sync.Map` anywhere** — the house style is an explicit `sync.RWMutex` beside the
data it guards, with a comment.

### 4.1 RWMutex + package-level store

`api/unifi_inventory.go:28-31`:
```go
var (
	unifiMu    sync.RWMutex
	unifiStore = make(map[string]storedUniFi)
)
```
Writes take `Lock()` (`:76-85`), reads take `RLock()` (`:94-96`, `:108-113`). Identical
in `api/phones_inventory.go:25-30` and `monitoring/monitoring.go:24-28`.

**Critical-section discipline:** the lock is released *before* the response is written.
`api/unifi_inventory.go:76-87`:
```go
		unifiMu.Lock()
		unifiStore[key] = storedUniFi{ ... }
		unifiMu.Unlock()
		logr.Infof("[api.SetUniFiSnapshot] Stored %s snapshot for key=%s status=%s", payload.Kind, key, payload.Status)
		return c.Status(200).SendString("OK")
```

### 4.2 Snapshot-by-value (avoids leaking a reference under lock)

`GetSnapshot()` (`jira/jira.go:110-114`) returns `Snapshot` **by value**, not a pointer.
The struct contains slices, so this is a shallow copy — safe only because the poller
always builds a *brand-new* `Snapshot` and never mutates the stored one in place
(`jira/jira.go:294-314`). **LL-Telemetry must preserve this invariant**: replace, never
mutate.

### 4.3 Broadcast without blocking (the non-negotiable SSE pattern)

`jira/jira.go:146-155`:
```go
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
```
Buffered channel + non-blocking send + drop-on-full. Channels are `make(chan []byte, 4)`
(`jira/jira.go:129`, `board.go:744`).

`boardHub.store` (`jira/board.go:679-701`) is the refined version — it copies the
subscriber list under the lock, then **marshals and sends outside the lock**:
```go
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
```
Prefer this variant for LL-Telemetry.

Unsubscribe is idempotent, guarding the double-close (`jira/jira.go:137-144`, `board.go:754-763`):
```go
func Unsubscribe(ch chan []byte) {
	subsMu.Lock()
	if _, ok := subs[ch]; ok {
		delete(subs, ch)
		close(ch)
	}
	subsMu.Unlock()
}
```
And the handler always unsubscribes via `defer` inside the stream writer
(`api/jira.go:70-71`): `defer jira.UnsubscribeBoard(id, ch)`.

### 4.4 Background refresh goroutines

Two kinds.

**Always-on, started once** — `jira/jira.go:235-258`:
```go
func StartPoller() {
	cfg := loadConfig()
	if !cfg.configured() {
		logr.Info("[jira.StartPoller] Jira is not configured (...) — the /jira page will show a not-configured state")
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
```
Three things to copy verbatim:
1. **`poll(cfg)` runs once immediately**, then the ticker — no cold-start delay.
2. **`recover()` inside a closure per tick** — a panic kills one cycle, not the poller.
3. Unconfigured → log + set the not-configured snapshot + return (no goroutine at all).

**On-demand, self-terminating** — `jira/board.go:704-739`. This is the most
sophisticated pattern in the fork and the right model if LL-Telemetry has per-entity
detail views:
```go
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
```
The `h.polling` flag is a **check-and-set under lock**, making `ensurePoller` safe to
call from every request path (`GetBoard`, `SubscribeBoard`) without spawning duplicates.
Idle detection = no subscribers AND no access for 2 minutes → the goroutine returns and
resets the flag so it can be restarted later.

### 4.5 Known concurrency gaps

- `hubs` map (`jira/board.go:646`) **grows without bound** — one `*boardHub` per board id
  ever requested, never deleted. Small and bounded in practice; a telemetry feed keyed by
  something high-cardinality (device id, session id) would leak. Bound it.
- `unifiStore` / `phonesInventoryStore` similarly **never evict** a key that stops
  reporting or is removed from config.
- `loadConfig()` re-reads `os.Getenv` on **every** `ListBoards`/`fetchBoard`/`FetchIssue`
  call (`board.go:154,440`; `issue.go:46`) — cheap, and it's what makes env changes visible
  without restart, but it means the poller goroutine holds a *stale* `cfg` captured at
  `StartPoller` time (`jira.go:236,244,254`) while handlers see fresh env. Inconsistent but
  harmless today.

---

## 5. Frontend error / loading / empty states

**There is no shared error/loading component.** Each view re-implements the states with a
shared *CSS class vocabulary*: `.notice`, `.notice-error`, `.notice-title`, `.notice-body`,
`.err-pre`, `.partial`. Duplicated per-file in `<style scoped>` blocks.

`web/app/src/views/JiraDetails.vue:394-399` — the canonical styles:
```css
.notice { border: 1px dashed hsl(var(--border)); border-radius: 12px; padding: 1.5rem; }
.notice-error { border-style: solid; border-color: hsl(var(--destructive) / 0.4); background: hsl(var(--destructive) / 0.05); }
.notice-title { font-weight: 700; margin-bottom: 0.35rem; }
.notice-body { font-size: 0.875rem; color: hsl(var(--muted-foreground)); }
.notice code { font-family: ui-monospace, monospace; background: hsl(var(--muted) / 0.6); padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.85em; }
.err-pre { font-size: 12px; white-space: pre-wrap; word-break: break-word; color: hsl(var(--destructive)); ... }
```
Dashed border = informational/empty. Solid + destructive tint = error.

**Creating a shared `<StateNotice>` component would be a defensible improvement**, but
matching the existing class names is the minimum bar.

### 5.1 The `loaded` guard (avoids a flash of "not configured")

`JiraDetails.vue:185-187, 196`:
```js
const loading = ref(false)
const loaded = ref(false)
...
const snapshot = ref({ configured: false, ok: false, status: 'unknown', projects: [] })
```
Every state branch is gated on `loaded` so the initial (falsy) default never renders as
a real state — `JiraDetails.vue:37-44`:
```html
      <div v-if="loaded && !snapshot.configured" class="notice">
        <div class="notice-title">Jira isn’t connected yet</div>
        <p class="notice-body">Set <code>JIRA_BASE_URL</code>, <code>JIRA_EMAIL</code> and <code>JIRA_API_TOKEN</code> in <code>.env</code>, then <code>docker compose up -d</code>.</p>
      </div>
      <div v-else-if="loaded && snapshot.configured && !snapshot.ok" class="notice notice-error">
        <div class="notice-title flex items-center gap-2"><AlertTriangle class="h-4 w-4" /> Can’t reach Jira</div>
        <pre class="err-pre">{{ snapshot.error }}</pre>
      </div>
```
Note the not-configured copy is **actionable**: it names the exact env vars and the command.
The error copy dumps the raw server error into a `<pre>`.

### 5.2 The full state ladder

`JiraKanban.vue:43-66` is the most complete example — **five** ordered states before the
happy path:
```html
    <!-- States -->
    <div v-if="loading && !board.columns.length" class="notice">
      <div class="notice-title">Loading the board…</div>
      <p class="notice-body">Reading the column configuration and cards from Jira.</p>
    </div>
    <div v-else-if="boardsError" class="notice notice-error">
      <div class="notice-title flex items-center gap-2"><AlertTriangle class="h-4 w-4" /> Can’t list boards</div>
      <pre class="err-pre">{{ boardsError }}</pre>
    </div>
    <div v-else-if="!boards.length" class="notice">
      <div class="notice-title">No agile boards on these projects</div>
      <p class="notice-body">
        The account can’t see a board for {{ projectHint }}. Create a Kanban board in Jira, or point
        <code>JIRA_PROJECTS</code> at a project that has one.
      </p>
    </div>
    <div v-else-if="board.error && !board.columns.length" class="notice notice-error">
      <div class="notice-title flex items-center gap-2"><AlertTriangle class="h-4 w-4" /> Can’t read this board</div>
      <pre class="err-pre">{{ board.error }}</pre>
    </div>
    <div v-else-if="!board.columns.length" class="notice">
      <div class="notice-title">This board has no columns</div>
      <p class="notice-body">Add columns to the board in Jira and they’ll show up here.</p>
    </div>
```
The ordering is the lesson: **loading → error → empty-because-nothing-exists →
error-for-this-entity → empty-because-misconfigured → data**. And critically,
`loading && !board.columns.length` — the spinner only replaces the page when there's
nothing to show; a refresh over existing data spins the icon instead
(`JiraKanban.vue:37-39`):
```html
        <Button variant="ghost" size="icon" class="h-9 w-9" @click="fetchBoard" data-tooltip="Refresh board" data-tip-pos="bottom">
          <RefreshCw class="h-5 w-5" :class="{ 'animate-spin': loading }" />
        </Button>
```

### 5.3 The partial-failure banner (mirrors §1.4 server-side)

`JiraKanban.vue:69-72` — data present *and* an error → show both:
```html
    <template v-else>
      <div v-if="board.error" class="partial">
        <AlertTriangle class="h-3.5 w-3.5" /> Showing the last good read — {{ board.error }}
      </div>
```
This is the single most important frontend pattern for LL-Telemetry: **never choose
between showing data and showing the error.**

### 5.4 Empty states at every granularity

Per-column (`JiraKanban.vue:113`) — distinguishes "empty" from "filtered to nothing":
```html
              <div v-if="!col.cards.length" class="kc-empty">{{ filter ? 'Nothing matches the filter' : 'Empty' }}</div>
```
Per-project (`JiraDetails.vue:161-164`):
```html
        <div v-else class="notice">
          <div class="notice-title">No open tickets in {{ proj.key }}</div>
          <p class="notice-body">Nothing is currently in an un-done status for this project.</p>
        </div>
```
Per-field — the em-dash fallback is used everywhere for a missing scalar:
`{{ it.summary || '—' }}`, `{{ it.assignee || 'Unassigned' }}` (`JiraDetails.vue:133-139`),
and in computed props (`JiraDetails.vue:201-209`):
```js
const avgResolution = computed(() => {
  const h = proj.value?.avgResolutionHours || 0
  if (!h) return '—'
  return h >= 48 ? (h / 24).toFixed(1) + 'd' : h.toFixed(1) + 'h'
})
const slaValue = computed(() => {
  const s = proj.value?.slaBreached
  return (s === undefined || s === null || s < 0) ? '—' : String(s)
})
```
`slaValue` decodes the server's **`-1` = "not measured"** sentinel (`jira/jira.go:81`,
`:320`) into `'—'` — distinct from `0`, which means "measured, none breached".
**LL-Telemetry should use the same sentinel convention for un-measurable metrics.**

### 5.5 Client-side last-known-good: `/* keep last */`

Every fetch swallows its error and keeps the previous state. `JiraDetails.vue:345-351`:
```js
const fetchMetrics = async () => {
  loading.value = true
  try {
    const res = await fetch('/api/v1/jira/metrics', { cache: 'no-store' })
    if (res.ok) applySnapshot(await res.json())
  } catch (e) { /* keep last */ } finally { loading.value = false }
}
```
`JiraKanban.vue:265-272`:
```js
const fetchBoard = async () => {
  if (!boardId.value) return
  loading.value = true
  try {
    const res = await fetch(`/api/v1/jira/board/${boardId.value}`, { cache: 'no-store' })
    if (res.ok) applyBoard(await res.json())
  } catch (e) { /* keep the last good board */ } finally { loading.value = false }
}
```
Three invariants: `{ cache: 'no-store' }` on every call; `if (res.ok)` guard so a non-2xx
never overwrites good state with an error body; `finally` always clears `loading`.

`store.js:120-127` — same for the bulk UniFi poll:
```js
export const unifiSnapshots = ref({})
export async function refreshUniFiSnapshots() {
  try {
    const response = await fetch('/api/v1/unifi', { cache: 'no-store' })
    if (response.ok) unifiSnapshots.value = await response.json()
  } catch (e) {
    // non-fatal — keep the last snapshot rather than blanking the rows
  }
}
refreshUniFiSnapshots()
setInterval(refreshUniFiSnapshots, 20000)
```
Repeated at `store.js:143-153` (`// non-fatal — keep the switches we already know about`)
and `store.js:190-201` (`// non-fatal — fall back to local clock`).

`store.js` is the place for **cross-card shared data with a module-level interval** —
LL-Telemetry data needed by many cards belongs here, not in the component
(`store.js:114-118`):
```js
// --- UniFi snapshots (the Firewall and Wireless rows) ---
// An external-endpoint can only carry a pass/fail, so the collector pushes the
// detail behind those two rows to a side channel. One request serves every card
// on the page, which is why it lives here rather than inside LocationCard.
```

### 5.6 SSE with polling fallback

`JiraDetails.vue:353-373`:
```js
let es = null
const connectLive = () => {
  try {
    es = new EventSource('/api/v1/jira/live')
    es.onmessage = (e) => { try { applySnapshot(JSON.parse(e.data)); live.value = true } catch (err) { /* ignore */ } }
    es.onerror = () => { live.value = false }
  } catch (e) { live.value = false }
}

let tick = null, fallback = null
onMounted(() => {
  fetchMetrics()
  connectLive()
  tick = setInterval(() => { now.value = Date.now() }, 1000)          // drives SLA countdowns
  fallback = setInterval(() => { if (!live.value) fetchMetrics() }, 30000) // if SSE drops
})
onUnmounted(() => {
  if (es) es.close()
  if (tick) clearInterval(tick)
  if (fallback) clearInterval(fallback)
})
```
Four layers of defence: outer `try` around `EventSource` construction; inner `try` around
`JSON.parse` so **one malformed frame doesn't kill the stream**; `onerror` flips `live`
false; a 30s poll that only fires when `live` is false. `onUnmounted` tears down all three
handles — no leaks.

`JiraKanban.vue:274-290` adds a stricter frame guard and closes any prior stream:
```js
const connectLive = () => {
  if (es) { es.close(); es = null }
  live.value = false
  if (!boardId.value) return
  try {
    es = new EventSource(`/api/v1/jira/board/${boardId.value}/live`)
    es.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data)
        if (data && (data.columns || data.error)) applyBoard(data)
        live.value = true
      } catch (err) { /* ignore a malformed frame */ }
    }
    es.onerror = () => { live.value = false }
  } catch (e) { live.value = false }
}
```
The `live` flag is also a **UI affordance**, not just internal state — `JiraDetails.vue:20`:
```html
          <span v-if="tab === 'overview'" class="live-ind" :class="{ on: live }"><span class="ldot"></span>{{ live ? 'live' : updatedLabel }}</span>
```
When live it says "live"; when not, it falls back to a relative timestamp
(`JiraDetails.vue:320-323`) so the user can judge staleness themselves:
```js
const updatedLabel = computed(() => {
  if (!snapshot.value.updatedAt) return 'never'
  try { return 'updated ' + generatePrettyTimeAgo(new Date(snapshot.value.updatedAt)) } catch (e) { return '—' }
})
```
Note `'never'` for a missing timestamp and a `try/catch` around date parsing.

### 5.7 Error handling that surfaces *when there is no error object*

`JiraKanban.vue:214-225` — distinguishes "our server answered with a failure" from
"our server didn't answer":
```js
const fetchBoards = async () => {
  try {
    const res = await fetch('/api/v1/jira/boards', { cache: 'no-store' })
    const data = await res.json()
    boardsError.value = data.ok === false ? (data.error || 'Jira rejected the board listing') : ''
    boards.value = data.boards || []
    const ids = boards.value.map(b => b.id)
    if (!ids.includes(boardId.value) && ids.length) boardId.value = ids[0]
  } catch (e) {
    boardsError.value = 'The board listing request failed. Is Gatus still reachable?'
  }
}
```
`data.ok === false` (strict) reads the envelope; `|| 'Jira rejected...'` covers an
envelope with no error text; the `catch` produces a *different, human* message about
Gatus itself. Also note `boards.value = data.boards || []` — **every array from the
server is `|| []`-guarded**, because Go marshals a nil slice as `null`, not `[]`.
This appears throughout: `JiraDetails.vue:198,261,332`; `JiraKanban.vue:246,312,315`;
`LocationCard.vue:165,178,185`. **Non-negotiable for a Go-backed feature.**

`applyBoard` defends the same way with a defaults-spread (`JiraKanban.vue:262`):
```js
  board.value = { columns: [], unmapped: [], total: 0, ...data }
```

### 5.8 LocationCard: semantic degradation (the domain-specific gem)

`LocationCard.vue:170-193` — the fork's most considered edge case. "No data" is
**not** the same as "failing", and painting it red would fake an outage:
```js
// "Nothing reported" is a distinct failure from "reported and failing": a site
// with 0 phones registered, or a UniFi console we can't read, has no health
// signal at all, and painting that red makes it look like an outage. Both
// collectors flag it with a fixed error prefix — "no phones reporting" and
// "no unifi reporting" — and it renders BLACK. Keep this in step with the
// prefixes in collector/phone_collector.py and collector/unifi_collector.py.
const NOT_REPORTING = /^no (phones|unifi) reporting\b/i
const isNotReporting = (result) =>
  !!result && !result.success && (result.errors || []).some((e) => NOT_REPORTING.test(e))

// A collector that reports three states (healthy / degraded / down) pushes
// degraded as a PASS carrying its reason, so a partial problem doesn't fire a
// down alert. A successful result with errors therefore means "passed, with a
// warning" — 1 of 2 WAN uplinks down, 4 of 23 APs offline — and reads AMBER.
// Painting it green would hide exactly the problems this dashboard exists for.
const isWarning = (result) => !!result && result.success && (result.errors || []).length > 0

const endpointRowCells = (endpoint) => {
  const padded = padResults(endpoint)
  return padded.map((result) => {
    if (!result) return { token: 'none', result: null }
    if (result.success) return { token: isWarning(result) ? 'amber' : 'green', result }
    return { token: isNotReporting(result) ? 'nodata' : 'red', result }
  })
}
```
**Four tokens, not two: `none` (no check yet) / `green` / `amber` (pass-with-warning) /
`red` / `nodata` (nothing reporting).** If LL-Telemetry has a "can't tell" state, it must
get its own token — do not collapse it into red. Note the maintenance hazard the comment
flags: the contract is a **magic string prefix** shared between Python and JS with nothing
enforcing it.

The rollup respects it too (`LocationCard.vue:212-217`):
```js
    const up = slice.filter((r) => r.success && r.duration)
    if (up.length === 0) {
      // Everything in this slice failed — but if the ONLY thing that failed was
      // a not-reporting feed, there is no outage to call red.
      const token = slice.every(isNotReporting) ? 'nodata' : 'red'
      cells.push({ token, result: slice[0] })
      continue
    }
```

Graceful fallback when the side channel hasn't reported (`LocationCard.vue:256-291`):
```js
// Firewall / Wireless read from the UniFi side channel, which carries counts an
// external-endpoint's pass/fail cannot. Falls back to latency until the
// collector has reported.
const unifiCounts = (endpoint) => {
  const snap = endpoint ? unifiSnapshots.value[endpoint.key] : null
  return snap && snap.counts ? snap.counts : null
}
...
const firewallMetric = (endpoint) => {
  const c = unifiCounts(endpoint)
  if (!c || c.wansTotal == null) return latencyOf(endpoint)
  ...
```
`if (!c || c.wansTotal == null) return latencyOf(endpoint)` — **when the enriched source
is missing, degrade to the basic source rather than showing nothing.** Note `== null`
(loose) to catch both null and undefined while allowing `0`.

Paused endpoints render a reason, not a blank (`LocationCard.vue:320-324`):
```js
      // A paused row shows why it is quiet instead of a number nobody is watching.
      value: paused ? 'paused' : metric.value,
      valueBad: paused ? false : metric.bad,
      valueSub: paused ? '' : (metric.sub || ''),
```

### 5.9 Optimistic update with rollback + toast

`store.js:155-179` — the only place the user is *told* about a failure:
```js
// Flips one endpoint. Optimistic so the toggle feels instant, reverted if the
// server disagrees.
export async function setMonitored(key, monitored) {
  if (!key) return
  const before = new Set(monitoringDisabled.value)
  const next = new Set(before)
  monitored ? next.delete(key) : next.add(key)
  monitoringDisabled.value = next
  try {
    const response = await fetch(`/api/v1/monitoring/${encodeURIComponent(key)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ monitored: !!monitored }),
    })
    if (!response.ok) throw new Error(String(response.status))
    const data = await response.json()
    const confirmed = new Set(monitoringDisabled.value)
    data.monitored ? confirmed.delete(key) : confirmed.add(key)
    monitoringDisabled.value = confirmed
    addToast(`${monitored ? 'Resumed' : 'Paused'} monitoring for ${key}`, monitored ? 'success' : 'info')
  } catch (e) {
    monitoringDisabled.value = before
    addToast(`Couldn't change monitoring for ${key}`, 'error')
  }
}
```
**The rule the codebase follows: passive/background reads fail silently and keep the
last value; user-initiated writes roll back and toast.** Toast API is `addToast(message, type, timeout)`
from `store.js:35-40`, types `info | success | error`.

`store.js:57-64` — even `localStorage` reads are guarded:
```js
function loadStatusColors() {
  try {
    const saved = JSON.parse(localStorage.getItem('gatus:colors') || '{}')
    return { ...STATUS_COLOR_DEFAULTS, ...saved }
  } catch (e) {
    return { ...STATUS_COLOR_DEFAULTS }
  }
}
```
And `fromConfig` (`store.js:3-9`) defends against **unreplaced Go template placeholders**:
```js
// Reads a value from window.config, ignoring unreplaced Go template placeholders.
function fromConfig(key) {
  if (typeof window === 'undefined' || !window.config) return null
  const value = window.config[key]
  if (!value || (typeof value === 'string' && value.startsWith('{{'))) return null
  return value
}
```

---

## 6. Startup and hot-reload — **the biggest risk area**

### 6.1 The reload cycle

`main.go:228-254`:
```go
func listenToConfigurationFileChanges(cfg *config.Config) {
	for {
		time.Sleep(30 * time.Second)
		if cfg.HasLoadedConfigurationBeenModified() {
			logr.Info("[main.listenToConfigurationFileChanges] Configuration file has been modified")
			stop(cfg)
			time.Sleep(time.Second) // Wait a bit to make sure everything is done.
			save()
			updatedConfig, err := loadConfiguration()
			if err != nil {
				if cfg.SkipInvalidConfigUpdate {
					logr.Errorf("[main.listenToConfigurationFileChanges] Failed to load new configuration: %s", err.Error())
					logr.Error("[main.listenToConfigurationFileChanges] The configuration file was updated, but it is not valid. The old configuration will continue being used.")
					cfg.UpdateLastFileModTime()
					continue
				} else {
					panic(err)
				}
			}
			store.Get().Close()
			initializeStorage(updatedConfig)
			start(updatedConfig)
			return
		}
	}
}
```
The watcher goroutine **returns** after calling `start(updatedConfig)`, which itself
launches a fresh watcher (`main.go:57`) — so the count stays at one. That part is fine.

### 6.2 `start` / `stop` are asymmetric — Jira leaks

`main.go:52-65`:
```go
func start(cfg *config.Config) {
	go controller.Handle(cfg)
	metrics.InitializePrometheusMetrics(cfg, nil)
	watchdog.Monitor(cfg)
	jira.StartPoller() // background Jira service-desk metrics (no-op unless configured)
	go listenToConfigurationFileChanges(cfg)
}

func stop(cfg *config.Config) {
	watchdog.Shutdown(cfg)
	controller.Shutdown()
	metrics.UnregisterPrometheusMetrics()
	closeTunnels(cfg)
}
```
**`jira.StartPoller()` is in `start` but has no counterpart in `stop`.** There is no
`jira.StopPoller`. Consequences on every config hot-reload:

1. **A second metrics poller goroutine is spawned.** `StartPoller` has no
   already-running guard (`jira/jira.go:235-258`) — no `sync.Once`, no `started` flag.
   Each reload adds another `for range ticker.C` loop. N reloads → N pollers, N× the
   Jira API request rate, all racing to `setSnapshot` (safe, but wasteful and each
   `broadcast` fires N times).
2. **The old poller keeps its stale `cfg`** — captured by value at `StartPoller`
   (`jira/jira.go:236`) and closed over (`:244, :254`). It cannot be stopped and will
   never see new env.
3. **Board hub goroutines survive**, which is *mostly* fine — `ensurePoller` is
   idempotent via `h.polling`, and hubs self-terminate after 2 minutes idle
   (`jira/board.go:717-726`). But `hubs` and the module-level caches
   (`boardListVal/At/Err`, `statusVal/At`) are **package-level and never reset**, so a
   reload does not invalidate them.
4. **SSE subscribers survive** the reload (`subs` at `jira/jira.go:106`) — arguably
   correct, since browsers stay connected.
5. **`unifiStore` / `phonesInventoryStore` survive** — snapshots for endpoint keys
   removed from the new config are never cleaned up. Compare `initializeStorage`
   (`main.go:118-136`), which *does* delete endpoint statuses whose endpoints no longer
   exist. The custom stores have no equivalent reconciliation.

### 6.3 What LL-Telemetry must do

- **Add a `StopX()` and call it from `stop(cfg)`** — a `context.CancelFunc` or a `stop chan struct{}`
  selected on alongside `ticker.C` in the poll loop.
- **Guard `StartX()` against double-start** — a package-level `started bool` under a mutex,
  or accept a context and let the old one cancel.
- **Reset or reconcile package caches on reload**, and **evict store keys** no longer in
  `cfg` (mirroring `main.go:118-136`).
- **Keep the "no-op unless configured" property** (`jira/jira.go:237-241`) so an
  unconfigured feature starts nothing at all.
- Note the SIGTERM path (`main.go:41-47`) calls `stop(cfg)` then `save()` — anything needing
  a flush-on-shutdown must hook `stop`. LL-Telemetry state persisted like
  `monitoring.persist()` (write-through on every change, `monitoring/monitoring.go:53-65`)
  needs no shutdown hook, which is why that pattern is preferable.

### 6.4 Lazy-load-on-first-use for anything touching `/data`

`monitoring/monitoring.go:16-19`:
```go
// State is persisted to /data (mounted, gitignored) so it survives updates, and
// lazily loaded on first use because /data is only guaranteed to exist at
// runtime.
```
`ensureLoaded()` (`monitoring/monitoring.go:36-53`) is called at the top of every public
function, guarded by a `loaded bool`, and treats a missing/corrupt file as the safe default:
```go
// ensureLoaded reads the persisted state once. A missing or corrupt file simply
// means "nothing is paused" — monitoring everything is the safe default, so
// there's no error to report to the caller.
func ensureLoaded() {
	mu.Lock()
	defer mu.Unlock()
	if loaded {
		return
	}
	loaded = true
	if b, err := os.ReadFile(pausedPath); err == nil {
		var f monitoringFile
		if json.Unmarshal(b, &f) == nil {
			for _, key := range f.Paused {
				paused[key] = true
			}
		}
	}
}
```
`loaded = true` is set **before** the read, so a failing read isn't retried on every call.
Identical in `api/phones_inventory.go:100-112` (`ensureExclusionsLoaded`).

Also note the import-cycle constraint documented at `monitoring/monitoring.go:21-22`:
```go
// This package must not import api or watchdog — both of them import it, and
// api already imports watchdog, so either direction would be an import cycle.
```
**A new `lltelemetry` package should likewise be a leaf** that `api` imports, importing
nothing from `api`/`watchdog`.

### 6.5 Route registration edge case

`api/api.go:104-105, 112-120` — static routes must be registered **before** parameterised
ones, and the code says so twice:
```go
	// Force-sweep: static route registered BEFORE the :key GET so it isn't
	// swallowed by :key="sweep-pending". Collector claims pending sweeps here.
	unprotectedAPIRouter.Get("/v1/phones/sweep-pending", ClaimPhonesSweeps)
...
	// Static route first so it isn't swallowed by :key.
	unprotectedAPIRouter.Get("/v1/unifi", GetUniFiSnapshots)
	unprotectedAPIRouter.Post("/v1/unifi/:key", SetUniFiSnapshot(cfg))
	unprotectedAPIRouter.Get("/v1/unifi/:key", GetUniFiSnapshot)
```

**All custom routes are registered on `unprotectedAPIRouter`** (`api/api.go:100-132`) —
i.e. no auth even when `cfg.Security` is set. Justified at `api/phones_inventory.go:133-135`
as "internal LAN tool, consistent with the rest of the API". LL-Telemetry read routes should
follow suit for consistency; push routes carry their own bearer token.

**SSE must be excluded from compression** (`api/api.go:66-73`):
```go
	app.Use(compress.New(compress.Config{
		// Never compress the live SSE streams — they must stream unbuffered.
		Next: func(c *fiber.Ctx) bool {
			p := c.Path()
			return strings.HasPrefix(p, "/api/v1/live") || p == "/api/v1/jira/live" ||
				(strings.HasPrefix(p, "/api/v1/jira/board/") && strings.HasSuffix(p, "/live"))
		},
	}))
```
**This is a hard-coded path list — a new LL-Telemetry SSE route must be added here or it
will buffer and appear dead.** Plus the four required headers (`api/jira.go:64-67`):
```go
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")
```
And a 20s heartbeat to keep proxies from timing out (`api/jira.go:81, 89-95`):
```go
			heartbeat := time.NewTicker(20 * time.Second)
			defer heartbeat.Stop()
			...
			case <-heartbeat.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
				if w.Flush() != nil {
					return
				}
```
Every write is checked and returns on failure, which is what terminates the goroutine when
the browser goes away.

A new SPA route also needs registering (`api/api.go:138`): `app.Get("/jira", SinglePageApplication(cfg.UI))`.

---

## 7. Logging convention

Package: **`github.com/TwiN/logr`** (not stdlib `log`), configured once in
`main.go:73-86` from `GATUS_LOG_LEVEL`, defaulting to `LevelInfo`.

**Format: `logr.<Level>f("[package.Function] Message", args...)`** — the bracketed
`package.Function` prefix is universal.

Complete inventory across the custom features:
```
jira/board.go:524    logr.Warnf("[jira.fetchBoard] board %d: done column unavailable: %s", id, derr.Error())
jira/board.go:724    logr.Debugf("[jira.board] board %d idle — stopping poller", id)
jira/board.go:730    logr.Errorf("[jira.board] board %d poll recovered from panic: %v", id, r)
jira/jira.go:238     logr.Info("[jira.StartPoller] Jira is not configured (set JIRA_BASE_URL, JIRA_EMAIL, JIRA_API_TOKEN) — the /jira page will show a not-configured state")
jira/jira.go:242     logr.Infof("[jira.StartPoller] Polling %s projects=%s every %s", cfg.baseURL, strings.Join(cfg.projects, ","), cfg.pollInterval)
jira/jira.go:251     logr.Errorf("[jira.poll] recovered from panic: %v", r)
jira/jira.go:276     logr.Warnf("[jira.poll] authentication check failed: %s", err.Error())
jira/jira.go:305     logr.Warnf("[jira.poll] refresh had errors: %s", firstErr.Error())
api/unifi_inventory.go:86    logr.Infof("[api.SetUniFiSnapshot] Stored %s snapshot for key=%s status=%s", payload.Kind, key, payload.Status)
api/phones_inventory.go:83   logr.Infof("[api.SetPhonesInventory] Stored inventory for key=%s status=%s", key, payload.Status)
api/phones_inventory.go:118  logr.Errorf("[api.persistExclusions] could not write %s: %s", exclusionsPath, err.Error())
api/monitoring.go:43         logr.Infof("[api.SetMonitoringForKey] Set monitored=%v for key=%s", body.Monitored, key)
api/api.go:33/37             logr.Warnf("[api.New] nil web config passed as parameter. ...")
api/api.go:51                logr.Errorf("[api.ErrorHandler] %s", err.Error())
```

Level semantics as actually practised:

| Level | Used for |
|---|---|
| `Debugf` | high-frequency lifecycle noise (poller idle/stop) |
| `Info`/`Infof` | startup config summary; a successful state-changing write |
| `Warnf` | **an upstream failure that the envelope already reports to the UI** |
| `Errorf` | recovered panics; local I/O failures (can't write `/data`) |

**The key judgement to copy: an unreachable third-party API is `Warnf`, not `Errorf`.**
It's expected, it's already visible in the UI, and it recurs every poll interval — logging
it at Error would flood the log. `Errorf` is reserved for *our* bugs and *our* disk.

Other conventions: always `err.Error()` (never `%v` on an error) — except for `recover()`
values, which use `%v`; identifying context (`key=`, `board %d`, `status=`) is included in
the message; no logging in hot read paths (`GetSnapshot`, `GetUniFiSnapshots`, `IsPaused`
log nothing).

Fiber has a global error handler catching anything a handler returns (`api/api.go:50-53`),
plus `recover.New()` middleware (`api/api.go:65`) — so a handler panic returns 500 rather
than killing the process.

---

## 8. Existing tests

### 8.1 Coverage: **zero for every custom feature**

```
$ ls jira/*_test.go
No such file or directory

$ grep -rl "jira\|unifi\|phones" --include="*_test.go" .
(no results)
```

`api/*_test.go` contains only upstream Gatus tests: `api_test.go`, `badge_test.go`,
`chart_test.go`, `config_test.go`, `endpoint_status_test.go`, `external_endpoint_test.go`,
`raw_test.go`, `spa_test.go`, `suite_status_test.go`, `util_test.go`.

**Not tested:** `api/jira.go`, `api/unifi_inventory.go`, `api/phones_inventory.go`,
`api/phones_settings.go`, `api/phones_sweep.go`, `api/monitoring.go`, `api/endpoint_check.go`,
all of `jira/`, all of `monitoring/`.

Note `api/api_test.go` `TestNew` (the route smoke test) does **not** cover any custom route —
adding LL-Telemetry scenarios there is the cheapest possible first test.

### 8.2 Available test utilities

There is **no shared helper file** (no `testutil.go`, no `TestMain`). Each test file is
self-contained on stdlib + Fiber's built-in test client.

**The house test pattern — table-driven scenarios + `router.Test(request)`**
(`api/api_test.go:14-131`), which needs **no `net/http/httptest.Server`**; Fiber's
`*fiber.App.Test` dispatches an `*http.Request` directly:
```go
func TestNew(t *testing.T) {
	type Scenario struct {
		Name         string
		Path         string
		ExpectedCode int
		Gzip         bool
		WithSecurity bool
	}
	scenarios := []Scenario{
		{
			Name:         "health",
			Path:         "/health",
			ExpectedCode: fiber.StatusOK,
		},
		...
	}
	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			cfg := &config.Config{Metrics: true, UI: &ui.Config{}}
			if scenario.WithSecurity {
				cfg.Security = &security.Config{ ... }
			}
			api := New(cfg)
			router := api.Router()
			request := httptest.NewRequest("GET", scenario.Path, http.NoBody)
			if scenario.Gzip {
				request.Header.Set("Accept-Encoding", "gzip")
			}
			response, err := router.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != scenario.ExpectedCode {
				t.Errorf("%s %s should have returned %d, but returned %d instead", request.Method, request.URL, scenario.ExpectedCode, response.StatusCode)
			}
		})
	}
}
```
Idioms: `t.Run(scenario.Name, ...)`; `httptest.NewRequest` + `http.NoBody`; `t.Fatal` for
setup failure, `t.Errorf` for assertion failure; long descriptive error messages; **plain
stdlib `testing`, no testify/assert anywhere**.

`api/external_endpoint_test.go` is the closest analogue for a **push** endpoint
(token auth + JSON body + `config.Config` fixture) — read it before writing LL-Telemetry
push tests.

### 8.3 Testability obstacles LL-Telemetry should design around

The Jira package is hard to test as written, and a new feature can cheaply avoid this:

1. **Config comes straight from `os.Getenv`** inside `loadConfig()` (`jira/jira.go:180-226`),
   called from deep inside `fetchBoard`/`FetchIssue`/`ListBoards`. Requires `t.Setenv` and
   forbids parallel tests. → Accept a config struct, with `loadConfig()` only at the edge.
2. **Base URL is env-only** (`jira/jira.go:216`), so pointing the client at an
   `httptest.Server` means setting `JIRA_BASE_URL`. That *does* work — an
   `httptest.NewServer` returning canned JSON/401/malformed bodies is the natural way
   to test `do()`. Make the equivalent injectable and this becomes trivial.
3. **Caches are package-level globals with no reset hook** (`boardListVal/At/Err`,
   `statusVal/At`, `hubs`, `store`) — tests leak into each other. → Provide an unexported
   `resetCachesForTest()` or make the cache a struct.
4. **`newClient` hard-codes its `http.Client`** (`jira/jira.go:395`) — no transport
   injection, so timeout/retry behaviour can't be exercised. → Make it a field.
5. **Time is `time.Now()` throughout** (`buildTrend`, `windowCounts`, `nowRFC3339`,
   freshness checks) — TTL and staleness logic can't be tested deterministically. →
   Inject a clock, or at minimum keep pure functions pure.

Pure, already-testable helpers that show the good shape and are worth mirroring:
`topCounts`, `orderedPriority`, `buildTrend`, `windowCounts`, `averageResolutionHours`,
`parseJiraTime`, `quote`, `snippet` (`jira/jira.go:610-751`); `cardUrgency`, `priorityRank`,
`dominant` (`jira/board.go:596-623`). All take inputs and return outputs with no globals.

---

## 9. Checklist for LL-Telemetry

**Backend**
- [ ] Envelope: `{configured, ok, error, status, updatedAt, ...data}` — 200 for cached reads, 502 for uncached on-demand, 400 for bad params
- [ ] `err != nil && len(data) == 0` → hard fail; otherwise ship partial data **with** the error string
- [ ] `http.Client{Timeout: 25s}` + per-operation `context.WithTimeout`, `http.NewRequestWithContext` everywhere
- [ ] `io.LimitReader(resp.Body, 8<<20)`; treat empty-200 as success; nil-check every optional pointer
- [ ] Verify auth up-front if "empty" and "unauthorized" are indistinguishable upstream
- [ ] No retries inside a cycle — the next tick is the retry
- [ ] Cache with TTL + **"stale beats nothing"** on refresh failure; cache errors too
- [ ] Consider retaining last-good data on a failed poll (Jira doesn't — improve on it)
- [ ] Consider a consecutive-failure backoff (nothing in the fork has one)
- [ ] `sync.RWMutex` beside the data; replace snapshots, never mutate; copy maps under `RLock`
- [ ] Poller: immediate first run, `recover()` per tick, no-op when unconfigured
- [ ] SSE: buffered chan (4) + non-blocking send + drop; marshal outside the lock; idempotent unsubscribe
- [ ] **`StopTelemetry()` wired into `main.go:stop()` + double-start guard** ← the gap Jira has
- [ ] Evict store keys not in the reloaded config; bound any per-entity hub map
- [ ] Leaf package — must not import `api`/`watchdog`
- [ ] Register static routes before `:param`; add any SSE path to the compress `Next` list; four SSE headers + 20s heartbeat
- [ ] Never let a secret reach `Snapshot.Error` (rendered verbatim in the browser)

**Frontend**
- [ ] `.notice` / `.notice-error` / `.notice-title` / `.notice-body` / `.err-pre` / `.partial` classes
- [ ] `loaded` guard; state ladder: loading → error → empty → per-entity error → per-entity empty → data
- [ ] `loading && !data.length` for the full-page spinner; icon spin on refresh-over-data
- [ ] **`v-if="x.error"` partial banner over live data** — never choose between data and error
- [ ] `{ cache: 'no-store' }`, `if (res.ok)` guard, `catch { /* keep last */ }`, `finally { loading = false }`
- [ ] `|| []` on every server array (Go nils marshal to `null`); `|| '—'` on every scalar
- [ ] `-1` sentinel for "not measured", rendered `'—'`, distinct from `0`
- [ ] SSE + `if (!live) fetch()` 30s fallback; inner `try` around `JSON.parse`; teardown in `onUnmounted`
- [ ] `live ? 'live' : updatedLabel` staleness affordance
- [ ] Distinct token for "can't tell" — do not collapse into red
- [ ] Fall back to the basic source when the enriched source is absent
- [ ] Cross-card data + `setInterval` in `store.js`; per-view data in the component
- [ ] Silent-keep-last for background reads; rollback + `addToast(..., 'error')` for user writes

**Tests**
- [ ] Nothing custom is tested today — any test is net-new coverage
- [ ] Add scenarios to `api/api_test.go:TestNew` (cheapest first step)
- [ ] Table-driven + `api.New(cfg).Router().Test(httptest.NewRequest(...))`, stdlib `testing` only
- [ ] `httptest.NewServer` for upstream 200/401/malformed/timeout cases
- [ ] Design for testability: injectable config, base URL, HTTP client and clock; a cache-reset hook
