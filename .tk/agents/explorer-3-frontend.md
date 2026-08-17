# Explorer 3 — Frontend Architecture Map (for adding an "LL-Telemetry" button + view)

Root: `C:\Users\colby.west\Desktop\Projects\Gatus`
Frontend: `web/app/` (Vue 3 SPA, `<script setup>`, Vue CLI 5 + webpack, Tailwind 3) → builds to `web/static/` → `go:embed`'d by `web/static.go`.

---

## 1. Full inventory of `web/app/src/`

### Entry / shell
| File | Role |
|---|---|
| `web/app/src/main.js` | 6 lines. `createApp(App).use(router).mount('#app')`, imports `./index.css`. |
| `web/app/src/App.vue` | 354 lines. App shell: sticky header (logo, SearchBar, sound/fullscreen/refresh/**Jira** buttons, configured nav links), `<router-view>` with crossfade, footer, global Tooltip + ToastContainer, OIDC login screen. Fetches `/api/v1/config` + `/api/v1/version`. |
| `web/app/src/router/index.js` | 43 lines. Full router config (see §2). |
| `web/app/src/store.js` | 203 lines. Global reactive state + polling side-channels (see §4). |
| `web/app/src/index.css` | 448 lines. Tailwind directives, CSS custom props, motion system, tooltip CSS, fullscreen wall layout (see §5). |

### Views (`web/app/src/views/`)
| File | Lines | Role |
|---|---|---|
| `Home.vue` | 406 | Dashboard. Fetches `/api/v1/endpoints/statuses` + `/api/v1/suites/statuses`, subscribes to SSE `/api/v1/live`, groups endpoints into locations, renders `LocationCard`/`SuiteCard`, pagination, audio alerts, hosts `<Settings/>` pill. |
| `EndpointDetailRouter.vue` | 30 | Dispatcher for `/endpoints/:key` — picks PhoneDetails / FirewallDetails / WirelessDetails / EndpointDetails by key prefix (`phones_`, `firewall_`, `wireless_`). |
| `EndpointDetails.vue` | 505 | Generic endpoint drill-in: KPI strip, response-time chart, event list, CSV export, **Force ping** button. |
| `PhoneDetails.vue` | 500 | Phones drill-in: per-phone inventory table, exclusions, per-site settings, force sweep. |
| `FirewallDetails.vue` | 484 | UniFi firewall drill-in: WAN uplinks. |
| `WirelessDetails.vue` | 683 | UniFi wireless drill-in: AP fleet. |
| `SiteOverview.vue` | 791 | Whole-site drill-in (`/sites/:name`) — the "Overall" row on a location card. |
| `SuiteDetails.vue` | 323 | Suite drill-in (`/suites/:key`). |
| `JiraDetails.vue` | 509 | **The model to copy.** Jira monitor page at `/jira`: toolbar with back-arrow + mark + title, Overview/Kanban tabs, KPI rail, sparkline, sortable ticket list/board, SSE live stream. |

### Components (`web/app/src/components/`)
| File | Lines | Role |
|---|---|---|
| `LocationCard.vue` | 465 | A site card on the dashboard: WAN/Phones/UniFi/Overall rows of status bars; each row label is a `router-link` into a drill-in. Uses `useRouter()`. |
| `EndpointCard.vue` | 212 | Single-endpoint card (non-location layout). |
| `SuiteCard.vue` | 201 | Suite card. |
| `JiraKanban.vue` | 611 | Jira agile board tab (columns, WIP limits, cards); own fetch + SSE. |
| `JiraTicketPanel.vue` | 220 | Slide-in ticket detail panel (`/api/v1/jira/issue/:key`). |
| `CardSettingsMenu.vue` | 378 | Per-card settings dropdown. |
| `ResponseTimeChart.vue` | 398 | chart.js response-time chart. |
| `AnnouncementBanner.vue` | 307 / `PastAnnouncements.vue` | 215 | Announcements UI. |
| `Tooltip.vue` | 268 | Rich data-point tooltip (distinct from global `[data-tooltip]` bubbles). |
| `Settings.vue` | 189 | Fixed bottom-left pill: refresh interval + **dark-mode toggle** (writes `theme` cookie, toggles `html.dark`). Rendered **only from Home.vue** (line ~100). |
| `MonitorToggle.vue` | 140 | Pause/resume monitoring switch for an endpoint. |
| `SequentialFlowDiagram.vue` 125 / `FlowStep.vue` 132 / `StepDetailsModal.vue` 200 | Suite step visualization. |
| `SimulatePanel.vue` | 90 | Client-side outage simulator (header). |
| `SearchBar.vue` | 73 | Header search bound to `controls.searchQuery`. |
| `Pagination.vue` 71 / `Loading.vue` 85 / `ToastContainer.vue` 45 / `StatusBadge.vue` 43 / `Social.vue` 30 | Small shared UI. |
| `ui/button/Button.vue` (53), `ui/card/{Card,CardHeader,CardTitle,CardContent}.vue`, `ui/badge/Badge.vue`, `ui/input/Input.vue`, `ui/select/Select.vue` | shadcn-style primitives, each with an `index.js` barrel. |

### Utils (`web/app/src/utils/`)
`format.js` (16, ns→human duration) · `time.js` (75, `generatePrettyTimeAgo`) · `misc.js` (6, `combineClasses` = twMerge+clsx) · `sounds.js` (48, WebAudio chimes) · `markdown.js` (45, marked+DOMPurify) · `tooltip.js` (142, global `[data-tooltip]` fixed-position bubble engine, `installTooltips()` / `hideTooltip()`).

### Assets
`web/app/src/assets/jira.png` (header/toolbar Jira mark), `web/app/src/assets/logo.svg`.

---

## 2. Routing — `web/app/src/router/index.js` (VERBATIM, all 43 lines)

```js
import {createRouter, createWebHistory} from 'vue-router'
import Home from '@/views/Home'
import EndpointDetailRouter from "@/views/EndpointDetailRouter";
import SuiteDetails from '@/views/SuiteDetails';
import JiraDetails from '@/views/JiraDetails';
import SiteOverview from '@/views/SiteOverview';

const routes = [
    {
        path: '/',
        name: 'Home',
        component: Home
    },
    {
        path: '/endpoints/:key',
        name: 'EndpointDetails',
        component: EndpointDetailRouter,
    },
    {
        // Whole-site drill-in (the Overall row on a location card). Keyed by
        // endpoint `name`, which is what groups endpoints into a site.
        path: '/sites/:name',
        name: 'SiteOverview',
        component: SiteOverview,
    },
    {
        path: '/suites/:key',
        name: 'SuiteDetails',
        component: SuiteDetails
    },
    {
        path: '/jira',
        name: 'Jira',
        component: JiraDetails
    }
];

const router = createRouter({
    history: createWebHistory(process.env.BASE_URL),
    routes
});

export default router;
```

Notes:
- **HTML5 history mode** — every route must ALSO be registered server-side or a deep link / hard refresh 404s.
- Server-side SPA fallback list is `api/api.go:134-138`:
  ```go
  app.Get("/", SinglePageApplication(cfg.UI))
  app.Get("/endpoints/:key", SinglePageApplication(cfg.UI))
  app.Get("/suites/:key", SinglePageApplication(cfg.UI))
  app.Get("/sites/:name", SinglePageApplication(cfg.UI))
  app.Get("/jira", SinglePageApplication(cfg.UI))
  ```
  → **Adding `/ll-telemetry` requires a matching `app.Get("/ll-telemetry", SinglePageApplication(cfg.UI))` line here.** There is no catch-all; unmatched paths fall through to `fiberfs` static serving with `Browse: true`.
- `@` alias = `web/app/src` (Vue CLI default).
- No lazy loading — all views are statically imported.

---

## 3. The button pattern — ALL top-level nav buttons

### Where they live
| Button | File | Lines |
|---|---|---|
| Logo → `/` | `web/app/src/App.vue` | 15–35 |
| SearchBar | `web/app/src/App.vue` | 39 |
| SimulatePanel (outage sim) | `web/app/src/App.vue` | 42 |
| Sound toggle (Volume2/VolumeX) | `web/app/src/App.vue` | 43–53 |
| **Fullscreen** (Maximize/Minimize) | `web/app/src/App.vue` | 54–64 |
| Refresh data (RefreshCw) | `web/app/src/App.vue` | 65–74 |
| **Jira** → `/jira` | `web/app/src/App.vue` | **75–87** (+ scoped `.jira-ico` style at 342–354) |
| Configured external links (`window.config.buttons`) | `web/app/src/App.vue` | 91–101 |
| Refresh-interval + **dark-mode toggle** pill | `web/app/src/components/Settings.vue` | 1–53 (mounted from `Home.vue:100` — home page only) |
| **Force ping** (Zap) | `web/app/src/views/EndpointDetails.vue` | 32–36 (handler 401–425) |
| Export CSV / avg-vs-minmax / refresh | `web/app/src/views/EndpointDetails.vue` | 25–38 |
| Phones / Firewall / Wireless entry points | `web/app/src/components/LocationCard.vue` | row labels are `router-link`s to `/endpoints/:key`; dispatched by `EndpointDetailRouter.vue`. **There is no header button for Phones/UniFi.** |
| Jira page tabs (Overview / Kanban) | `web/app/src/views/JiraDetails.vue` | 28–35 |

### THE button to copy verbatim — `web/app/src/App.vue` lines 75–87

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

Supporting scoped style, `App.vue` lines 342–354:
```css
<style scoped>
.jira-ico {
  display: inline-block;
  width: 20px;
  height: 20px;
  background-size: contain;
  background-repeat: no-repeat;
  background-position: center;
}
</style>
```
Import at `App.vue:189`: `import jiraIcon from '@/assets/jira.png'`

### The icon-Button variant (for a lucide icon instead of an image) — `App.vue` 65–74
```html
                <Button
                  variant="ghost"
                  size="icon"
                  class="h-9 w-9"
                  @click="refreshData"
                  data-tooltip="Refresh data"
                  data-tip-pos="bottom"
                >
                  <RefreshCw class="h-5 w-5" />
                </Button>
```
`Button` = `@/components/ui/button` (cva). Variants: `default | destructive | outline | secondary | ghost | link`; sizes: `default | sm | lg | icon`. Base classes (`Button.vue:30`):
`inline-flex items-center justify-center whitespace-nowrap rounded-md text-sm font-medium ring-offset-background transition duration-150 ease-out active:scale-[0.96] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50`

**Recommendation for LL-Telemetry:** drop a `<router-link to="/ll-telemetry">` immediately after the Jira link (App.vue line 87), same class string, with a **lucide icon** (e.g. `Activity`, `Radio`, `Cpu`) rather than an `<img>` — see the custom-CSS `header img` trap in §5.

All buttons sit inside `<div class="flex items-center gap-1">` (App.vue:41–88), which is inside `<div class="flex items-center gap-2 sm:gap-3 flex-wrap justify-end grow">` (line 38).

---

## 4. `web/app/src/store.js` — state + fetch pattern

Not Vuex/Pinia — a plain ES module exporting `reactive`/`ref` singletons. **Import what you need directly:** `import { isFullscreen, addToast } from '@/store'`.

### What it holds
| Export | Lines | Purpose |
|---|---|---|
| `controls` (reactive) | 17–25 | `searchQuery, filterBy, sortBy, showOnlyFailing, showRecentFailures, groupByGroup, showAverageResponseTime`. Persisted to `localStorage['gatus:sort-by' / 'gatus:show-average-response-time']`. |
| `requestRefresh()` | 28–30 | Dispatches `window` CustomEvent `gatus:refresh`; Home.vue listens (Home.vue:397). |
| `toasts`, `addToast(msg, type, timeout)`, `removeToast(id)` | 33–44 | Bottom-right toasts, rendered by `ToastContainer`. |
| `soundEnabled`, `setSoundEnabled` | 47–51 | `localStorage['gatus:sound']`. |
| `statusColors`, `STATUS_COLOR_DEFAULTS`, `applyStatusColors/setStatusColor/resetStatusColors` | 56–83 | Writes `--status-up/-degraded/-down` onto `document.documentElement`. `localStorage['gatus:colors']`. |
| `simulations`, `setSimulation`, `clearSimulations`, `knownLocations` | 87–96 | Client-side outage simulator. |
| `isFullscreen` (ref) | 101 | Set by App.vue on `fullscreenchange`; read by Home.vue. |
| `dashboardView`, `setDashboardView` | 106–112 | `'vertical' \| 'horizontal'`, `localStorage['gatus:view']`. |
| `unifiSnapshots`, `refreshUniFiSnapshots()` | 119–129 | GET `/api/v1/unifi`, **polled every 20 s**. |
| `monitoringDisabled` (Set), `isMonitored`, `refreshMonitoring`, `setMonitored` | 137–182 | GET `/api/v1/monitoring` **every 30 s**; optimistic POST with revert + toast. |
| `now` (ref), server clock sync | 186–203 | `now` ticks every 1 s; GET `/api/v1/time` **every 60 s** to compute `serverOffset`. |
| `fromConfig(key)` helper | 4–9 | Reads `window.config[key]`, ignoring unreplaced `{{ ... }}` Go template placeholders. |

### The canonical fetch pattern (VERBATIM, store.js:119–129)
```js
// --- UniFi snapshots (the Firewall and Wireless rows) ---
// An external-endpoint can only carry a pass/fail, so the collector pushes the
// detail behind those two rows to a side channel. One request serves every card
// on the page, which is why it lives here rather than inside LocationCard.
// Keyed by endpoint key, e.g. "firewall_decatur-gmc".
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

Conventions: **native `fetch` only (no axios)**, `{ cache: 'no-store' }`, `credentials: 'include'` for protected routes, `if (response.ok)` guard, **swallow errors and keep the last good value** (never blank the UI), module-level `setInterval` at import time for store-level polling. Mutating POSTs use optimistic update + revert + `addToast`.

### Per-view fetch pattern (SSE with polling fallback) — `JiraDetails.vue:345–373`
```js
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
Home.vue mirrors this (`fetchData()` + `EventSource('/api/v1/live', { withCredentials: true })`, Home.vue:272–336, 393–405).

Server side: SSE routes are excluded from compression at `api/api.go:66–73` — **any new SSE endpoint must be added to that `Next` predicate**.

---

## 5. Styling — CRITICAL for embedding a dark OKLCH console

### Files
- `web/app/src/index.css` (448 lines) — the only global stylesheet.
- `web/app/tailwind.config.js` — Tailwind **3.x**, `darkMode: 'class'`, `content: ['./public/index.html', './src/**/*.{vue,js,ts,jsx,tsx}']`, no plugins.
- `web/app/postcss.config.js` — tailwindcss + autoprefixer.
- Per-component `<style scoped>` blocks (JiraDetails.vue:376+, JiraKanban.vue:451+, PhoneDetails.vue:421+, App.vue:342+).
- **`config.yaml` `ui.custom-css` (lines 19–28)** — served at `/css/custom.css`, `<link>`'d from `public/index.html:27`, handled by `api/api.go:146`.

### Design tokens
`index.css:5–50` defines shadcn HSL-triplet vars on `:root` and `:root.dark`:
`--background --foreground --card --card-foreground --popover --popover-foreground --primary --primary-foreground --secondary --secondary-foreground --muted --muted-foreground --accent --accent-foreground --destructive --destructive-foreground --border --input --ring --radius`

`tailwind.config.js:13–52` maps them as `hsl(var(--x))`. **They are raw HSL triplets (`222.2 84% 4.9%`), not colors** — you cannot put an OKLCH value into these vars; the wrapper is hardcoded `hsl(...)`. For an OKLCH console, define your own separate variables.

Also on `:root`: `--status-up/-degraded/-down` (index.css:70–74, overridden at runtime by `store.applyStatusColors`) and motion tokens `--ease-out-quart/-quint/-expo`, `--dur-1..4` (index.css:108–116).

### Dark mode
- Class-based on `<html>`: set inline by `public/index.html:8–16` (cookie `theme` → else `prefers-color-scheme`), server-rendered as `<html lang="en" class="{{ .Theme }}">` from `api/spa.go:13–41`, toggled by `components/Settings.vue`.
- Component code uses semantic classes (`bg-background`, `text-muted-foreground`, `border`) far more than `dark:` prefixes. Raw `dark:` appears sparsely; `index.css` uses `.dark .stbar-nodata { ... }` descendant selectors (lines 90–92).

### ⚠️ Leak analysis for an embedded console

**A real `<iframe>` is safe.** No `<iframe>` exists anywhere in `web/app/src/` today. An iframe gets its own document, so Tailwind preflight, the universal rules below, and `custom.css` do NOT cross into it. You would need to inject your own reset inside the frame. Caveats: (a) `html.dark` is not inherited — pass the theme in via query param / `postMessage` or hardcode dark; (b) set `color-scheme: dark` inside the frame; (c) the iframe element itself still receives the outer universal rules (border-color, scrollbar hiding) — give it explicit `border: 0` and sizing.

**Mounting third-party HTML directly into the DOM (`v-html`, `appendChild`, web component with open shadow… ) WILL be polluted.** The offenders:

1. `index.css:52–58` — a **universal border rule**:
   ```css
   @layer base {
     * { @apply border-border; }
     body { @apply bg-background text-foreground; }
   }
   ```
   Every element inherits the app's border color.
2. `index.css:239–247` — **global scrollbar suppression**, no scoping:
   ```css
   * { scrollbar-width: none; -ms-overflow-style: none; }
   *::-webkit-scrollbar { width: 0; height: 0; display: none; }
   ```
   A scrollable console mounted in-page loses its scrollbars entirely.
3. **Tailwind preflight** (`@tailwind base`, index.css:1) — global `margin: 0`, `border: 0 solid`, unstyled headings/lists, `img { display: block }`, etc.
4. `index.css:441–447` — `prefers-reduced-motion` kills all animation/transition durations globally (`*, *::before, *::after ... !important`).
5. **`config.yaml` `ui.custom-css`, served at `/css/custom.css` (loaded on every page, not just Home):**
   ```css
   header .w-12.h-12 { width: auto !important; }
   header img { height: 38px !important; width: auto !important; max-width: 260px; }
   html.dark header img { filter: brightness(0) invert(1); }
   header h1 { display: none !important; }
   #social { display: none !important; }
   ```
   → **This is exactly why the Jira nav button uses a background-image `<span>` and not an `<img>`** (see the comment at App.vue:82–85). Any `<img>` inside `<header>` gets forced to 38px tall and painted solid white in dark mode. Use a lucide component or a background-image span for the LL-Telemetry icon.
6. `index.css:249–419` — the `.fs-active` fullscreen block uses aggressive `!important` container-query rules scoped to `.location`, `.loc-*`, `header`, `footer`, `#settings`. If the telemetry view is meant to work in fullscreen, note `.fs-active header, .fs-active footer { display: none !important }` and `.fs-active main { height: 100vh; overflow-y: auto }`.

**Recommended isolation strategy, strongest first:** `<iframe srcdoc>` / same-origin iframe → shadow DOM with an explicit reset → last resort, an `.ll-telemetry` wrapper that re-declares `scrollbar-width: auto`, `*::-webkit-scrollbar { display: block; width: 8px }`, `border-color: initial`, and its own OKLCH token block.

---

## 6. Build

`web/app/package.json` scripts:
```json
"serve": "vue-cli-service serve --mode development",
"build": "vue-cli-service build --modern --mode production",
"lint":  "vue-cli-service lint"
```

`web/app/vue.config.js` (verbatim, minus header comment):
```js
module.exports = {
	filenameHashing: false,
	productionSourceMap: false,
	outputDir: '../static',
	publicPath: '/',
	devServer: {
		port: 8081,
		https: false,
		client: { webSocketURL:'auto://0.0.0.0/ws' },
		proxy: {
			'^/api|^/css|^/oicd': {
				target: "http://localhost:8080",
				changeOrigin: true,
				secure: false,
			}
		}
	}
}
```

- **Vue CLI 5 / webpack — NOT Vite.** Babel via `web/app/babel.config.js`.
- `filenameHashing: false` → deterministic output: `web/static/js/app.js`, `web/static/js/chunk-vendors.js`, `web/static/css/app.css`, `web/static/index.html`, `web/static/img/logo.svg`.
- `outputDir: '../static'` → build lands directly in `web/static/`, which `web/static.go` embeds:
  ```go
  //go:embed static
  FileSystem embed.FS
  ```
  `RootPath = "static"`, `IndexPath = "static/index.html"`.
- **`web/static/` is committed to git** (`web/app/.gitignore` only ignores `/dist` and `node_modules`) — current `git status` shows `web/static/css/app.css` and `web/static/js/*.js` as modified. A frontend change is not live in the Go binary until you rebuild AND commit these.
- `Makefile`:
  ```
  frontend-install:  npm --prefix web/app install
  frontend-build:    npm --prefix web/app run build
  frontend-dev:      npm --prefix web/app run serve
  ```
- Dev loop: `make run` (Go on :8080) + `make frontend-dev` (webpack on :8081, proxying `/api`, `/css`, `/oicd` → :8080). Note `--mode production` builds `index.html` from `web/app/public/index.html`, which contains Go template placeholders `{{ .UI.Logo }}` etc. that are rendered at request time by `api/spa.go`.

---

## 7. `window.config` and feature flags

Injected server-side in `web/app/public/index.html:6` (a Go `text/template` executed by `api/spa.go`):
```html
window.config = {logo: "{{ .UI.Logo }}", header: "{{ .UI.Header }}", dashboardHeading: "{{ .UI.DashboardHeading }}", dashboardSubheading: "{{ .UI.DashboardSubheading }}", link: "{{ .UI.Link }}", buttons: [], maximumNumberOfResults: "{{ .UI.MaximumNumberOfResults }}", defaultSortBy: "{{ .UI.DefaultSortBy }}", defaultFilterBy: "{{ .UI.DefaultFilterBy }}", loginSubtitle: "{{ .UI.LoginSubtitle }}"};{{- range .UI.Buttons}}window.config.buttons.push({name:"{{ .Name }}",link:"{{ .Link }}"});{{end}}
```

Read two ways:
- `store.js:4–9` `fromConfig(key)` — returns `null` if the value is missing or still an unreplaced `{{ ... }}` placeholder (happens when the file is served statically instead of through the SPA handler).
- `App.vue:246–260` — inline computed guards comparing against the literal placeholder string, e.g.
  ```js
  const logo = computed(() => window.config && window.config.logo && window.config.logo !== '{{ .UI.Logo }}' ? window.config.logo : "")
  const buttons = computed(() => window.config && window.config.buttons ? window.config.buttons : [])
  ```

Runtime config also comes from `GET /api/v1/config` (App.vue:263–276, refetched every 10 min) returning `{ oidc, authenticated, announcements }`. This gates the whole app: `v-else-if="!config || !config.oidc || config.authenticated"` (App.vue:9).

**Feature flags gating button visibility: NONE.** The Jira button is unconditionally rendered — there is no `ui.jira.enabled` flag; when Jira isn't configured, `JiraDetails.vue:37–40` shows an inline "Jira isn't connected yet" notice instead. The only conditional nav is `v-if="buttons && buttons.length"` for the external `ui.buttons` links (App.vue:91). So the LL-Telemetry button can be added unconditionally, with an in-view "not configured" notice, matching the Jira precedent.

---

## Checklist to add LL-Telemetry (derived)
1. `web/app/src/views/LLTelemetry.vue` — copy the JiraDetails toolbar/tab/notice skeleton.
2. `web/app/src/router/index.js` — import + `{ path: '/ll-telemetry', name: 'LLTelemetry', component: LLTelemetry }`.
3. `api/api.go` (~line 138) — `app.Get("/ll-telemetry", SinglePageApplication(cfg.UI))` for deep links.
4. `web/app/src/App.vue` line 87 — new `<router-link>` after the Jira one; **lucide icon, not `<img>`**.
5. If it streams: add the path to the compression `Next` predicate at `api/api.go:66-73`.
6. If it embeds a dark OKLCH console: use an iframe; do not rely on the app's HSL tokens; re-enable scrollbars inside.
7. `make frontend-build`, then commit `web/static/`.

---

## 8–15 key files to read

1. `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\router\index.js` — add the route here.
2. `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\App.vue` — nav button pattern (lines 75–87, 342–354).
3. `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\views\JiraDetails.vue` — the view template to clone (toolbar, tabs, SSE, scoped styles).
4. `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\store.js` — state + fetch/polling conventions.
5. `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\index.css` — global CSS, tokens, and the leak hazards.
6. `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\tailwind.config.js` — token→Tailwind mapping, `darkMode: 'class'`.
7. `C:\Users\colby.west\Desktop\Projects\Gatus\api\api.go` — SPA route registration + SSE compression exclusion.
8. `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\public\index.html` — `window.config` injection + theme bootstrap.
9. `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\vue.config.js` — build output → `web/static/`.
10. `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\components\ui\button\Button.vue` — cva variants for any button.
11. `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\views\EndpointDetails.vue` — Force-ping button + POST-with-toast pattern (lines 32–36, 397–425).
12. `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\views\Home.vue` — SSE + `gatus:refresh` listener + Settings pill host.
13. `C:\Users\colby.west\Desktop\Projects\Gatus\config.yaml` — `ui.custom-css` (lines 19–28), the `header img` trap.
14. `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\components\JiraKanban.vue` — sub-tab component with its own fetch + SSE.
15. `C:\Users\colby.west\Desktop\Projects\Gatus\api\spa.go` — theme cookie → `<html class>` server render.
