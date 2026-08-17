# Explorer 1 — Jira service-desk monitor, end-to-end trace

Template for building the new **LL-Telemetry** feature (dashboard button → embedded console page).
Every path below is repo-relative to `C:\Users\colby.west\Desktop\Projects\Gatus`.

---

## 0. TL;DR — the shape of the feature

```
main.go:56  jira.StartPoller()          background goroutine, env-driven, no-op if unconfigured
   |
jira/  (own top-level Go package, NOT under config/)
   jira.go    config from env + HTTP client + poll loop + in-memory Snapshot + SSE broadcaster
   issue.go   on-demand single-ticket detail
   board.go   agile boards: list cache, per-board hub (cache + SSE fan-out + idle shutdown)
   |
api/jira.go   5 thin fiber handlers that just marshal what the jira package holds
   |
api/api.go    routes registered in the UNPROTECTED group (before ApplySecurityMiddleware)
              + compress-middleware exclusion for the SSE paths
              + `app.Get("/jira", SinglePageApplication(cfg.UI))` SPA deep-link fallback
   |
web/app/src/router/index.js   route '/jira' -> JiraDetails
web/app/src/App.vue           header <router-link to="/jira"> button (always visible)
web/app/src/views/JiraDetails.vue     the page (fetch + EventSource)
web/app/src/components/JiraKanban.vue + JiraTicketPanel.vue
web/app/src/assets/jira.png
web/static/**  (built bundle, committed, go:embed'd)
docs/jira-monitor.md
```

Key architectural facts:
- **No `config/jira` package. No `ValidateAndSetDefaults`. Nothing in `config.yaml`.** Jira is 100% environment-variable driven, read directly with `os.Getenv` inside `jira.loadConfig()`.
- **No `window.config` involvement.** The Jira button is unconditional — there is no feature flag. `window.config` only carries the stock Gatus UI fields.
- **No `store.js` involvement.** `web/app/src/store.js` has zero Jira state; each Jira view owns its own `ref`s. (UniFi *does* use the store — see §7 for that alternative pattern.)

---

## 1. Backend

### 1.1 `main.go`

| Line | Code |
|------|------|
| 12 | `"github.com/TwiN/gatus/v5/jira"` import |
| 56 | `jira.StartPoller() // background Jira service-desk metrics (no-op unless configured)` — inside `func start(cfg *config.Config)` (lines 52-58), after `watchdog.Monitor(cfg)`, before `go listenToConfigurationFileChanges(cfg)` |

Note `start()` is re-invoked by `listenToConfigurationFileChanges` on config-file change (main.go:250), so `StartPoller` can be called more than once in a process lifetime — the Jira poller does not guard against a double-start (a latent bug worth not copying verbatim).

### 1.2 `jira/jira.go` (752 lines) — poller + store + SSE hub

| Lines | Contents |
|-------|----------|
| 1-16 | Package doc (explains why auth is verified up-front) |
| 37-99 | JSON payload types: `NameCount`, `DayPoint`, `Issue`, `Project`, `Snapshot` |
| 101-107 | Package state: `storeMu sync.RWMutex` + `store Snapshot`; `subsMu sync.Mutex` + `subs map[chan []byte]struct{}` |
| 109-114 | `GetSnapshot() Snapshot` — RLock'd copy |
| 116-125 | `setSnapshot(s)` — write + `broadcast(json)` |
| 127-134 | `Subscribe() chan []byte` (buffer 4) |
| 136-144 | `Unsubscribe(ch)` |
| 146-155 | `broadcast(data)` — non-blocking `select { case ch <- data: default: }` so a slow client never stalls the poller |
| 159-171 | `type config struct` (unexported) — all fields lowercase |
| 173-178 | `envOr(key, def)` helper |
| 180-226 | `loadConfig()` — reads every `JIRA_*` env var, applies floors/defaults |
| 228-230 | `func (c config) configured() bool` — `baseURL != "" && email != "" && token != ""` |
| 234-258 | `StartPoller()` — logs + returns early when unconfigured; otherwise `go func(){ poll once; ticker loop with per-tick panic recover }` |
| 260-315 | `poll(cfg)` — 90s ctx, `cl.me()` auth check first, per-project loop, builds `Snapshot`, sets `Status` healthy/degraded/down |
| 319-381 | `(*client).project(...)` — per-project metric computation |
| 385-397 | `type client` + `newClient(cfg)` — `Basic base64(email:token)`, 25s `http.Client` |
| 399-432 | `(*client).do(ctx, method, path, body, out)` — the single request helper; 8MB read limit; non-2xx → error with a 300-char body snippet |
| 435-447 | `me()` — `GET /rest/api/3/myself` |
| 449-457 | `projectName()` |
| 460-468 | `countApprox()` — `POST /rest/api/3/search/approximate-count` |
| 470-512 | `rawIssue` + `toIssue()` + `issueFields` |
| 515-541 | `searchAll()` — `POST /rest/api/3/search/jql`, paginates via `nextPageToken` |
| 548-608 | `enrichSLAs()` — `GET /rest/servicedeskapi/request/{key}/sla` per ticket |
| 612-751 | Aggregation helpers: `orDash`, `topCounts`, `priorityOrder`/`orderedPriority`, `buildTrend`, `windowCounts`, `averageResolutionHours`, `parseJiraTime`, `quote`, `snippet` |

Polling/caching behavior:
- Snapshot poll: every `JIRA_POLL_SECONDS` (default 30, min 15). One global snapshot for all projects.
- Reads are lock-free-ish (`RWMutex`), always return the *last good* snapshot; failures set `OK:false` + `Error` rather than dropping data.

### 1.3 `jira/issue.go` (169 lines) — on-demand detail, no cache

| Lines | Contents |
|-------|----------|
| 10-42 | `SLAInfo`, `Comment`, `IssueDetail` types |
| 45-119 | `FetchIssue(ctx, key)` — `loadConfig()` per call (not cached!), errors if `!configured()`, `GET /rest/api/3/issue/{key}?expand=renderedFields&fields=...` |
| 121-147 | `fetchSLAs()` — best-effort, returns nil on error |
| 149-168 | `fetchComments()` — last 3 comments, `expand=renderedBody` |

### 1.4 `jira/board.go` (772 lines) — on-demand per-board hubs

| Lines | Contents |
|-------|----------|
| 1-22 | File doc (documents the two deliberate deviations: windowed done column, card cap) |
| 43-113 | `BoardRef`, `BoardCard`, `BoardColumn`, `BoardSnapshot`, `BoardListResult` |
| 117-139 | `boardPollInterval()` (20s min 10), `boardMaxCards()` (400 min 50), `boardDoneDays()` (14) |
| 143-149 | Board-list cache vars, `boardListTTL = 5 * time.Minute` |
| 153-178 | `ListBoards(ctx) BoardListResult` — TTL-cached |
| 188-232 | `(*client).boards()` — per-project + one unfiltered pass, merged by id (the unfiltered pass is load-bearing for the renamed LLSM→LLIT project) |
| 234-264 | `boardPage()` — `/rest/agile/1.0/board` paging |
| 268-304 | `statuses()` — status catalogue cached 10min, falls back to stale on error |
| 308-362 | `boardConfiguration` + `boardRawIssue` types |
| 400-435 | `boardIssues()` — `/rest/agile/1.0/board/{id}/issue` paging with truncation flag |
| 439-580 | `fetchBoard(ctx, id)` — configuration → columns → in-flight cards → done cards → placement → sort |
| 584-594 | `slaIndex()` — reuses the metrics snapshot's SLA fields so cards get countdowns for free |
| 596-631 | `cardUrgency`, `priorityRank`, `dominant`, `cachedBoardRefs`, `nowRFC3339` |
| 635-658 | `boardHub` struct + `hubs map[int]*boardHub` + `hubFor(id)` |
| 662-677 | `GetBoard(ctx, id)` — returns cache if fresh, else synchronous fetch; always `ensurePoller` |
| 679-701 | `(*boardHub).store(snap)` — save + fan out to subscribers |
| 704-739 | `ensurePoller(id)` — starts a goroutine that stops itself once `len(subs)==0 && time.Since(lastAccess) > 2*time.Minute` |
| 742-771 | `SubscribeBoard`, `UnsubscribeBoard`, `CachedBoard` |

**This idle-shutdown hub is the single most reusable idea for LL-Telemetry** if telemetry is expensive to poll: nothing polls until someone looks.

### 1.5 `api/jira.go` (144 lines) — handlers

| Lines | Handler | Returns |
|-------|---------|---------|
| 18-20 | `GetJiraMetrics` | `200 jira.GetSnapshot()` — **always 200**; `configured`/`ok` carry state |
| 24-33 | `GetJiraIssue` | 20s ctx; `200 *IssueDetail` or `502 {"error": ...}` |
| 37-41 | `GetJiraBoards` | 25s ctx; `200 BoardListResult` |
| 46-54 | `GetJiraBoard` | validates `:id` → `400 {"error":"board id must be a positive integer"}`; 60s ctx; `200 BoardSnapshot` |
| 59-100 | `JiraBoardLive` | SSE for one board |
| 106-143 | `JiraLive` | SSE for the metrics snapshot |

The SSE handler idiom (mirror this verbatim):

```go
c.Set("Content-Type", "text/event-stream")
c.Set("Cache-Control", "no-cache")
c.Set("Connection", "keep-alive")
c.Set("X-Accel-Buffering", "no")
ch := jira.Subscribe()
initial, _ := json.Marshal(jira.GetSnapshot())
c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
    defer jira.Unsubscribe(ch)
    writeEvent := func(payload []byte) bool {
        if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
            return false
        }
        return w.Flush() == nil
    }
    if initial != nil && !writeEvent(initial) {
        return
    }
    heartbeat := time.NewTicker(20 * time.Second)
    defer heartbeat.Stop()
    for {
        select {
        case msg, ok := <-ch:
            if !ok || !writeEvent(msg) {
                return
            }
        case <-heartbeat.C:
            if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
                return
            }
            if w.Flush() != nil {
                return
            }
        }
    }
})
return nil
```

### 1.6 `api/api.go` — route registration (THE wiring point)

Order inside `createRouter(cfg)` (lines 48-187):

1. **line 66-73** — compress middleware with an SSE exclusion `Next`:
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
2. **line 82** `apiRouter := app.Group("/api")`
3. **line 86** `unprotectedAPIRouter := apiRouter.Group("/")` — everything here is **UNAUTHENTICATED**
4. **lines 123-132** — the Jira block, verbatim:
```go
// Jira service-desk metrics, refreshed by the background jira poller.
unprotectedAPIRouter.Get("/v1/jira/metrics", GetJiraMetrics)
// Jira ticket drill-down: fetches one issue's detail on demand.
unprotectedAPIRouter.Get("/v1/jira/issue/:key", GetJiraIssue)
// Jira live stream (SSE): pushes a fresh snapshot on every poll.
unprotectedAPIRouter.Get("/v1/jira/live", JiraLive)
// Jira Kanban: the agile boards themselves (columns, WIP limits, cards).
unprotectedAPIRouter.Get("/v1/jira/boards", GetJiraBoards)
unprotectedAPIRouter.Get("/v1/jira/board/:id", GetJiraBoard)
unprotectedAPIRouter.Get("/v1/jira/board/:id/live", JiraBoardLive)
```
5. **lines 133-138** — SPA deep-link fallbacks (a hard refresh on `/jira` must serve index.html):
```go
// SPA
app.Get("/", SinglePageApplication(cfg.UI))
app.Get("/endpoints/:key", SinglePageApplication(cfg.UI))
app.Get("/suites/:key", SinglePageApplication(cfg.UI))
app.Get("/sites/:name", SinglePageApplication(cfg.UI))
app.Get("/jira", SinglePageApplication(cfg.UI))
```
6. lines 140-162 — health, custom CSS, redirect, static filesystem
7. **lines 163-175** — `protectedAPIRouter` + `cfg.Security.ApplySecurityMiddleware(...)`
8. lines 176-185 — the protected routes (`/v1/endpoints/statuses`, `/v1/live`, …)

> **Answer to "before or after ApplySecurityMiddleware?": BEFORE.** All six Jira routes sit in the unprotected group at lines 124-132, ~40 lines above the `ApplySecurityMiddleware` call at line 172. If LL-Telemetry must require auth, register it on `protectedAPIRouter` after line 175 instead — but note the front-end fetch/EventSource code below assumes no auth.

Static-route-before-param ordering matters in this router: see the comments at api.go:103-105 and 118-119 (`/v1/phones/sweep-pending` and `/v1/unifi` are registered before their `:key` siblings). `/v1/jira/boards` (line 130) vs `/v1/jira/board/:id` (line 131) are distinct paths so no conflict, but keep the habit.

---

## 2. Config layer

**There is no config layer.** Confirmed:
- `ls config/` → `announcement, config.go, connectivity, endpoint, gontext, key, maintenance, remote, suite, tunneling, ui, util.go, web` — **no `jira` package**, so no `ValidateAndSetDefaults`, no yaml struct, no validation test.
- `grep -ni jira config.yaml` → **zero matches**.
- `grep -ni jira .env.example` → **zero matches** (a real gap — the Jira keys are undocumented in the example file even though `docs/jira-monitor.md` says they live in `.env`).
- `.env` (gitignored, local only) contains: `JIRA_API_TOKEN`, `JIRA_BASE_URL`, `JIRA_EMAIL`, `JIRA_PROJECTS`.
- `docker-compose.yml:16-17` — the `gatus` service gets `env_file: - .env`. That single line is the entire injection path. `docker-compose.yml:14-15` also sets `TZ=America/Chicago` (matters: `buildTrend`/`windowCounts` use server-local time).
- `.gitignore:31-33` — `.env`, `.env.*`, `!.env.example`.
- `env-merge.sh` (repo root) — appends only missing keys into a prod `.env` without disturbing existing values.

Full env-var surface (defaults from `jira/jira.go:180-226` and `jira/board.go:117-139`):

| Var | Required | Default | Floor/clamp |
|-----|----------|---------|-------------|
| `JIRA_BASE_URL` | yes | — | trailing `/` trimmed |
| `JIRA_EMAIL` | yes | — | |
| `JIRA_API_TOKEN` | yes | — | |
| `JIRA_PROJECTS` (fallback `JIRA_PROJECT`) | no | `LLSM,LLIP` | comma-split, trimmed |
| `JIRA_POLL_SECONDS` | no | 30 | min 15 |
| `JIRA_TREND_DAYS` | no | 14 | 5..60 |
| `JIRA_MAX_ISSUES` | no | 500 | min 50 |
| `JIRA_SLA_MAX` | no | 60 | min 0 |
| `JIRA_SLA_PROJECTS` | no | first project | |
| `JIRA_BOARD_POLL_SECONDS` | no | 20 | min 10 |
| `JIRA_BOARD_MAX_CARDS` | no | 400 | min 50 |
| `JIRA_BOARD_DONE_DAYS` | no | 14 | min 1 |

---

## 3. Frontend

### 3.1 `web/app/src/router/index.js` (44 lines) — the whole file

```js
import JiraDetails from '@/views/JiraDetails';   // line 5
...
    {
        path: '/jira',          // lines 31-35
        name: 'Jira',
        component: JiraDetails
    }
```
Router uses `createWebHistory(process.env.BASE_URL)` (line 39) — hence the Go-side SPA fallback at api.go:138.

### 3.2 `web/app/src/App.vue` (353 lines) — the button

Markup, lines **75-87**, inside the icon-button cluster (`<div class="flex items-center gap-1">`, line 41) that also holds SimulatePanel / sound / fullscreen / refresh:

```html
<router-link
  to="/jira"
  class="inline-flex items-center justify-center h-9 w-9 rounded-md hover:bg-accent transition-colors"
  data-tooltip="Jira monitor"
  data-tip-pos="bottom"
  aria-label="Jira monitor"
>
  <!-- Rendered as a background-image span (not an <img>) so the
       header's custom-css rule for the wordmark logo
       (`header img { filter: brightness(0) invert(1) }`) doesn't
       repaint this icon into a solid white box. -->
  <span class="jira-ico" :style="{ backgroundImage: `url(${jiraIcon})` }"></span>
</router-link>
```

- line **189**: `import jiraIcon from '@/assets/jira.png'`
- lines **342-353**: scoped style
```css
.jira-ico {
  display: inline-block;
  width: 20px;
  height: 20px;
  background-size: contain;
  background-repeat: no-repeat;
  background-position: center;
}
```
- The button is **unconditional** — no `v-if`, no feature flag, no config gate.
- Tooltips come from the global `data-tooltip` / `data-tip-pos` attributes installed by `installTooltips()` (App.vue:316, `@/utils/tooltip`). Use the same attributes on the LL-Telemetry button; do **not** roll a title attribute.
- If your LL-Telemetry icon is a lucide glyph instead of a PNG, use a plain `<Button variant="ghost" size="icon" class="h-9 w-9">` wrapper like lines 43-74 and skip the background-image hack entirely — the hack exists only because `config.yaml:22-24` custom-css repaints every `<img>` inside `<header>`.

### 3.3 `web/app/src/views/JiraDetails.vue` (509 lines)

| Lines | Contents |
|-------|----------|
| 2-3 | Root: `<div class="dashboard-container detail-page bg-background">` → `<div class="w-full px-4 sm:px-6 py-4 space-y-5 jira-panel">` |
| 6-25 | Toolbar: back `<router-link to="/">` with `<ArrowLeft>`, icon mark, title, live indicator, refresh button |
| 28-35 | Tab bar (Overview / Kanban) |
| 37-44 | Not-configured notice + error notice (reads `snapshot.configured` / `snapshot.ok` / `snapshot.error`) |
| 47 | `<JiraKanban v-else-if="tab === 'kanban' && snapshot.configured" @open="openKey = $event" />` |
| 49-165 | Overview: project switcher, KPI rail, breakdown panels, sparkline, list/board views |
| 168 | `<JiraTicketPanel :issue-key="openKey" @close="openKey = ''" />` |
| 173-179 | Imports — `lucide-vue-next` icons, `@/components/ui/button`, `@/utils/time`'s `generatePrettyTimeAgo`, the PNG, the two child components |
| 185-196 | Local state refs (loading/loaded/live/search/tab/view/selectedKey/openKey/now/snapshot) |
| 190-191 | Tab persisted to `localStorage['gatus.jira.tab']` |
| 198-231 | Computed derivations (projects, KPIs, segment bars, sparkline points) |
| 234-258 | Live SLA countdown formatting |
| 260-305 | Filter/sort/board-grouping |
| 329-343 | `applySnapshot(data)` + new-ticket flash set |
| 345-351 | **`fetchMetrics()`** |
| 353-360 | **`connectLive()`** |
| 362-373 | `onMounted` / `onUnmounted` |
| 376-509 | Scoped CSS (all local, uses `hsl(var(--border))` etc. shadcn tokens) |

The data-fetch idiom to mirror verbatim:

```js
const snapshot = ref({ configured: false, ok: false, status: 'unknown', projects: [] })

const fetchMetrics = async () => {
  loading.value = true
  try {
    const res = await fetch('/api/v1/jira/metrics', { cache: 'no-store' })
    if (res.ok) applySnapshot(await res.json())
  } catch (e) { /* keep last */ } finally { loading.value = false }
}

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

Note the house style: relative URLs (`/api/v1/...`), `{ cache: 'no-store' }`, swallow errors and keep the last good payload, and a polling fallback whenever SSE is used.

### 3.4 `web/app/src/components/JiraKanban.vue` (611 lines)

Self-contained child; emits only `open` (a ticket key). Notable:
- `defineEmits(['open'])`
- localStorage keys `gatus.jira.boardId`, `gatus.jira.swimlane`
- `fetchBoards()` → `/api/v1/jira/boards`; `fetchBoard()` → `/api/v1/jira/board/${boardId}`; `connectLive()` → `new EventSource('/api/v1/jira/board/${boardId}/live')`
- `applyBoard(data)` diffs card placement to flash new/moved cards
- `watch(boardId, ...)` tears down and re-opens the EventSource on board change — the pattern to copy if LL-Telemetry has a selectable target/console.

### 3.5 `web/app/src/components/JiraTicketPanel.vue` (220 lines)

Slide-over drill-down. Props `{ issueKey }`, emits `close`. `load(key, refresh)` hits `/api/v1/jira/issue/${encodeURIComponent(key)}`; quiet 15s in-place refresh while open; `Escape` closes; description HTML sanitized with **DOMPurify** (`FORBID_TAGS: ['img','style']`, `FORBID_ATTR: ['style']`). If LL-Telemetry embeds third-party HTML, copy this sanitization; if it embeds a third-party *console* via iframe, this file is not the right template.

### 3.6 `web/app/src/store.js` (204 lines) — NOT used by Jira

Contains `controls`, `requestRefresh`, toasts, sound, status colors, simulations, `isFullscreen`, `dashboardView`, **UniFi snapshots** (lines 114-129), **monitoring pause switches** (131-182), server-time sync (185-203). Its `fromConfig(key)` helper (lines 3-9) is the canonical way to read `window.config` while ignoring unreplaced `{{ ... }}` Go template placeholders — **use this if LL-Telemetry needs a config-driven flag or URL**:

```js
function fromConfig(key) {
  if (typeof window === 'undefined' || !window.config) return null
  const value = window.config[key]
  if (!value || (typeof value === 'string' && value.startsWith('{{'))) return null
  return value
}
```

The UniFi block is the template to copy if LL-Telemetry data should be shared across views rather than owned by one page:

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

---

## 4. `window.config` injection — how it works, and why Jira ignores it

Path: `config.yaml` `ui:` block → `config/ui.Config` struct → `ui.ViewData{UI, Theme}` → Go `html/template` executed over the embedded `index.html`.

- `config/ui/ui.go:37-57` — the `Config` struct (`title`, `description`, `logo`, `link`, `favicon`, `buttons`, `custom-css`, `dark-mode`, `default-sort-by`, `default-filter-by`, `login-subtitle`, plus non-configurable `MaximumNumberOfResults`).
- `config/ui/ui.go:167-173` — `ValidateAndSetDefaults` renders the template once at boot to prove it parses.
- `config/ui/ui.go:175-178` — `type ViewData struct { UI *Config; Theme string }`.
- `api/spa.go:13-41` — `SinglePageApplication(uiConfig)` builds `ViewData`, resolves theme from the `theme` cookie else `uiConfig.IsDarkMode()`, `template.ParseFS(static.FileSystem, static.IndexPath)`, `t.Execute(c, vd)`.
- `web/web/static.go` — `//go:embed static`, `RootPath = "static"`, `IndexPath = "static/index.html"`.
- **`web/app/public/index.html:6`** (source) and **`web/static/index.html:1`** (built) carry the injection line:
```html
<script>window.config = {logo: "{{ .UI.Logo }}", header: "{{ .UI.Header }}", dashboardHeading: "{{ .UI.DashboardHeading }}", dashboardSubheading: "{{ .UI.DashboardSubheading }}", link: "{{ .UI.Link }}", buttons: [], maximumNumberOfResults: "{{ .UI.MaximumNumberOfResults }}", defaultSortBy: "{{ .UI.DefaultSortBy }}", defaultFilterBy: "{{ .UI.DefaultFilterBy }}", loginSubtitle: "{{ .UI.LoginSubtitle }}"};{{- range .UI.Buttons}}window.config.buttons.push({name:"{{ .Name }}",link:"{{ .Link }}"});{{end}}</script>
```
- Consumers: `App.vue:246-260` (`logo`, `header`, `buttons`, `loginSubtitle`, each comparing against the literal `'{{ .UI.Logo }}'` placeholder in dev) and `store.js:11` (`defaultSortBy`).
- The separate runtime endpoint `GET /api/v1/config` (`api/config.go`) returns only `{oidc, authenticated, announcements}` — **not** a place to add feature flags without also touching `App.vue:263-276`.

**Jira uses none of this.** There is no `jiraEnabled` flag; the button always renders and the page self-reports "Jira isn't connected yet" (JiraDetails.vue:37-40) when `snapshot.configured === false`. **Recommend copying that pattern for LL-Telemetry** (always-visible button + honest in-page unconfigured state) rather than adding a `window.config` flag — a flag would mean touching 4 extra files (`config/ui/ui.go`, `web/app/public/index.html`, `web/static/index.html`, and a consumer).

---

## 5. Docs — `docs/jira-monitor.md` (183 lines)

Structure worth cloning for `docs/ll-telemetry.md`:
1. Title + one-paragraph pitch + how to reach it (line 8: "Reach it from the Jira icon in the top-right of the header, or at `/jira`")
2. "What it shows" bullet list
3. "Architecture" with an ASCII data-flow diagram (lines 40-45)
4. **Backend files** table, **Frontend files** table, **HTTP endpoints (added)** table, upstream API list
5. Configuration table (var / required / default / notes) + how to get credentials
6. **Build & deploy** (lines 120-144) — critical, see §6
7. **Gotchas & troubleshooting** — four real ones, including the header-icon white-box and the SSE/gzip rule
8. Stale section: lines 174-183 say the real board isn't wired up yet — it now is (`jira/board.go` + `JiraKanban.vue`), and the doc's file tables omit `jira/board.go`, `api/jira.go`'s board handlers, and `JiraKanban.vue`. Don't inherit that drift.

---

## 6. Build & deploy constraint (non-negotiable)

The `Dockerfile` compiles Go only; it never runs `npm`. `web/app/vue.config.js` writes to `outputDir: '../static'` with `filenameHashing: false`, and `web/static/**` is **committed** and `go:embed`'d. Therefore any frontend change is a two-step:

```bash
cd web/app && npm run build        # regenerates web/static/
cd ../.. && docker compose up -d --build gatus
```

(Consistent with the current `git status`, which shows `web/static/js/app.js` and `chunk-vendors.js` modified alongside the `.vue` sources.)

Dev server: `npm run serve` on :8081 proxying `^/api|^/css|^/oicd` to `http://localhost:8080` (vue.config.js), with the Go side enabling CORS for `http://localhost:8081` when `ENVIRONMENT=dev` (`api/api.go:58-63`).

Deploy rule from project memory: **do not push to prod** — edit local docker only; the user deploys.

---

## 7. THE CHECKLIST — files to touch for LL-Telemetry

### Required (the "6 locations", in dependency order)

| # | File | Change |
|---|------|--------|
| 1 | `telemetry/telemetry.go` **(new package)** | Mirror `jira/jira.go`: exported payload structs w/ json tags, `sync.RWMutex`-guarded store, `GetSnapshot()`, `setSnapshot()`, `Subscribe`/`Unsubscribe`/`broadcast`, unexported `config` + `loadConfig()` from `LLT_*`/`TELEMETRY_*` env, `configured() bool`, `StartPoller()` with per-tick panic recover. Split into extra files (`issue.go`/`board.go` equivalents) only if there are on-demand or per-target sub-resources. |
| 2 | `main.go:56-57` | Add `telemetry.StartPoller()` next to `jira.StartPoller()` inside `start()`, plus the import at line ~13. |
| 3 | `api/telemetry.go` **(new)** | Handlers: `GetTelemetry` (always 200, `configured`/`ok` in payload), any drill-down handler, `TelemetryLive` (copy the SSE block from `api/jira.go:106-143` verbatim). |
| 4 | `api/api.go` | (a) extend the compress `Next` at **lines 68-72** with the new SSE path; (b) register routes in the **unprotected** group after **line 132**; (c) add `app.Get("/telemetry", SinglePageApplication(cfg.UI))` after **line 138**. |
| 5 | `web/app/src/router/index.js` | Import the view (after line 6) and add the route object after **line 35**. |
| 6 | `web/app/src/App.vue` | Add the `<router-link to="/telemetry">` button after **line 87**; icon import after line 189 (only if a PNG); scoped style after line 353 (only if a PNG). |
| 7 | `web/app/src/views/TelemetryDetails.vue` **(new)** | Copy the skeleton of `JiraDetails.vue`: `dashboard-container detail-page bg-background` root, back-arrow toolbar, live indicator, refresh button, unconfigured/error notices, `fetchX` + `connectLive` + `onMounted`/`onUnmounted`. |
| 8 | `docs/ll-telemetry.md` **(new)** | Clone the structure of `docs/jira-monitor.md`. |
| 9 | `.env` (local, gitignored) | Add the telemetry vars. |
| 10 | `.env.example` | Add commented placeholders — **and while you're there, add the missing `JIRA_*` block**. |
| 11 | `web/static/**` | Regenerate via `cd web/app && npm run build`, and commit the diff. |

### Conditional

| Condition | Extra files |
|-----------|-------------|
| Data must be shared across views | `web/app/src/store.js` — add an exported `ref` + `refreshX()` + `setInterval`, mirroring the UniFi block at lines 114-129 |
| Icon is a PNG/SVG asset | `web/app/src/assets/<name>.png` |
| Sub-components (console frame, picker, drill-in panel) | `web/app/src/components/Telemetry*.vue` |
| Route must require auth | register on `protectedAPIRouter` after `api/api.go:175` instead — and expect the browser `fetch`/`EventSource` to need credentials |
| A `window.config` feature flag is genuinely required | `config/ui/ui.go` (struct + default), `web/app/public/index.html:6`, `web/static/index.html:1`, and the consumer in `App.vue`/`store.js`. **Recommended: skip this.** |
| Extra compose service (a collector) | `docker-compose.yml` — see the `phone-collector`/`unifi-collector` blocks at lines ~20-60 for the `env_file: - .env` + `GATUS_PUSH_BASE=http://gatus:8080` pattern |

### Explicitly NOT needed
- `config/` — no new package, no `ValidateAndSetDefaults`, no yaml. (Unless you deliberately choose yaml over env, which would be a departure from the Jira template.)
- `config.yaml` — no entry.
- `api/config.go` — no change.
- `api/spa.go`, `config/ui/ui.go`, `web/*/index.html` — no change, given the no-flag recommendation.

---

## 8. Jira-specific things that do NOT transfer

1. **Auth-check-first (`/rest/api/3/myself`)** — exists only because Jira Cloud silently returns anonymous-empty results instead of 401. Keep the *idea* (fail loudly rather than showing a healthy all-zero board) only if your upstream has the same failure mode.
2. **HTTP Basic `base64(email:token)`** — Atlassian-specific. Most telemetry APIs use a bearer token or an API key header.
3. **Everything about SLA** — `enrichSLAs`, `SLARemainingMs`/`SLAActive`/`SLAPaused`, the ticking countdown (`now` ref + `remainingOf`), `cardUrgency`, `slaSortKey`, `slaIndex()`. All service-desk-only.
4. **JQL construction + `quote()`** — Jira query language.
5. **`nextPageToken` pagination + the "classic `/search` was removed in 2025" note** — Jira-API-version-specific.
6. **`/rest/agile/1.0` board configuration, WIP constraints, the done-column windowing and sub-filter fallback, the renamed-project unfiltered-pass workaround** — all board semantics; irrelevant to an embedded console.
7. **The background-image `<span>` icon hack** — a workaround for `config.yaml:22-24`'s `html.dark header img { filter: brightness(0) invert(1) }`. Only needed if your button uses an `<img>`/asset. A lucide SVG icon sidesteps it.
8. **DOMPurify sanitization in `JiraTicketPanel.vue`** — needed because Jira returns rendered HTML. Only relevant if you render untrusted upstream HTML.
9. **`buildTrend`/`windowCounts` local-time bucketing** — coupled to `TZ=America/Chicago` in docker-compose.
10. **Two-project switcher + `JIRA_PROJECTS` / `JIRA_SLA_PROJECTS`** — Jira's tenancy model.
11. **The LLSM→LLIT rename workaround** (jira/board.go:180-187) — a Long Lewis-specific data quirk (see memory note).

---

## 9. 13 key files to read

1. `api/api.go` — route registration order, compress exclusion, SPA fallback (lines 66-73, 123-138, 163-175)
2. `api/jira.go` — handler shapes + the SSE stream writer (all 144 lines)
3. `jira/jira.go` — poller, env config, in-memory store, SSE hub (lines 101-258 are the reusable core)
4. `jira/board.go` — on-demand hub with idle shutdown (lines 635-771)
5. `jira/issue.go` — on-demand detail handler pattern (lines 45-119)
6. `main.go` — lines 52-58 (`start()`)
7. `web/app/src/router/index.js` — whole file (44 lines)
8. `web/app/src/App.vue` — lines 41-88 (button cluster), 189, 342-353
9. `web/app/src/views/JiraDetails.vue` — lines 1-50 + 172-200 + 325-374 (page shell + fetch/SSE lifecycle)
10. `web/app/src/components/JiraKanban.vue` — child-component + selectable-target SSE pattern
11. `web/app/src/store.js` — lines 1-11 (`fromConfig`) and 114-129 (UniFi shared-state template)
12. `api/spa.go` + `config/ui/ui.go` (lines 37-57, 167-178) + `web/app/public/index.html:6` — the `window.config` injection chain
13. `docs/jira-monitor.md` — the doc template, and `docker-compose.yml:1-20` + `.env.example` for the config plumbing
