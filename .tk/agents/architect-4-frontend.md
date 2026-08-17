# Architect 4 — Frontend + Console-Serving Layer

Telemetry console inside Gatus. Same-origin iframe, Gatus serves the console HTML,
Gatus reverse-proxies `/api/v1/telemetry/*`, everything gated behind Gatus auth.

**Chosen URLs (proposed, used consistently below):**

| Thing | URL | Registered where |
|---|---|---|
| SPA page (Vue view) | `/telemetry` | `api/api.go` SPA block + `router/index.js` |
| Console HTML (iframe `src`) | `/api/v1/telemetry/console` | `protectedAPIRouter`, **before** the proxy wildcard |
| Telemetry data | `/api/v1/telemetry/*` | `protectedAPIRouter` (backend architect owns this) |

---

## 1. How the console HTML is stored and served

### The options

**(a) `web/app/public/telemetry/console.html`**

Survives `npm run build` (Vue CLI copies `public/` verbatim into `outputDir`), lands in
`web/static/telemetry/console.html`, gets picked up by the existing `//go:embed static` in
`web/static.go`. Zero new Go code.

Rejected, three reasons:

1. **It is served by the unprotected static handler.** `app.Use("/", fiberfs.New(...))` at
   `api/api.go:158` is registered *before* the security middleware block at line 168. Anything
   under `web/static/` is public by construction. The brief says telemetry is gated behind Gatus
   auth; serving the console from the public static tree directly contradicts that. The console
   HTML leaks the API surface (`/keys`, `/runs`, `/stats`), the estate's naming, and the ingest-key
   UI copy to anyone who can reach the host.
2. **Re-syncing from upstream requires a container npm build.** Copy the file, then spin up
   `node:20-alpine`, `npm ci` (~90s), `npm run build`, commit a regenerated `app.js`/`chunk-vendors.js`
   that has nothing to do with the console. A one-file upstream sync should not produce a
   thousand-line bundle diff.
3. It still lives inside `outputDir`. It survives *today* because `public/` is copied after the
   clean, but that ordering is a Vue CLI implementation detail sitting one config change away from
   deleting the file.

**(c) Go route reading the file from disk at runtime**

Rejected outright. The `Dockerfile` final stage is `FROM scratch` — it copies exactly the binary,
`config.yaml`, and the CA bundle. There is no filesystem to read from. This would require a new
bind mount in `docker-compose.yml` and would make the image non-self-contained for no benefit.

### (b) Recommended — separate `//go:embed` in the `api` package

```
api/assets/telemetry-console.html   <- vendored copy of LL-Telemetry index.html
api/telemetry_console.go            <- //go:embed + handler
```

Why this wins on every axis:

- **`npm run build` cannot touch it.** The file is not under `web/static` and not under
  `web/app/public`. `vue-cli-service build` cleans `outputDir` (`../static`) and has no idea
  `api/assets/` exists. The "Vue CLI destroyed my file" failure mode is structurally impossible,
  not merely avoided by convention.
- **`go:embed` still picks it up**, via its own directive in the `api` package. `web/static.go`'s
  `//go:embed static` is untouched. Two embeds, two independent lifecycles.
- **Re-syncing from upstream is `cp` + `docker compose build`.** No node, no npm, no bundle diff.
  The only files in the commit are the console and (if anything) the sync note.
- **It can be served from the protected router.** `protectedAPIRouter` is `apiRouter.Group("/")`
  (i.e. `/api/`) with the security middleware applied at `api/api.go:172`. Registering the console
  as `protectedAPIRouter.Get("/v1/telemetry/console", ...)` puts the HTML behind *exactly the same*
  auth as the data it displays — one gate, not two that can drift apart.
- **Serve-time transformation becomes possible**, which is what makes item 2 below clean.

**On the URL being under `/api`:** serving an HTML document from `/api/v1/telemetry/console` reads
slightly odd, but it is a purely internal iframe `src` that no human types. The alternative —
`app.Get("/telemetry/console.html", ...)` — requires a second call to
`cfg.Security.ApplySecurityMiddleware()` on a second router group, which is more code, a second
place to get auth wrong, and a second thing to forget when the security config changes. One gate.

**Current-state note, stated honestly:** `config.yaml` has no `security:` block, so `cfg.Security`
is `nil` today and `protectedAPIRouter` is presently open. Registering there is still correct —
it means telemetry is gated the moment Gatus auth is turned on, with no follow-up change. It does
*not* mean telemetry is gated right now. If gating must be real before Gatus auth exists, that is
a `config.yaml` change (add `security:`), not a frontend change.

### `api/telemetry_console.go` (complete, copy-paste)

```go
package api

import (
	_ "embed"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// The vendored LL-Telemetry operations console. This is a byte-for-byte copy of
// LL-Telemetry/telemetry/dashboard/index.html — do NOT hand-edit it. Fixes go
// upstream first, then get copied down. See api/assets/TELEMETRY-CONSOLE-SYNC.md.
//
//go:embed assets/telemetry-console.html
var telemetryConsoleUpstream string

// The console's only Gatus-specific difference: upstream talks to /api/v1 on the
// telemetry host, here it talks to Gatus's authenticated reverse proxy. Rewritten
// at start-up instead of edited in place so the vendored file stays diffable
// against upstream with a plain `diff`.
const (
	telemetryConsoleUpstreamAPIBase = "const API='/api/v1';"
	telemetryConsoleGatusAPIBase    = "const API='/api/v1/telemetry';"
)

// TelemetryConsole serves the vendored console with its API base rewritten.
// Registered on the protected router, so it inherits Gatus's auth.
//
// It panics if the API-base line isn't found exactly once. That is deliberate:
// a silent miss would serve a console that hammers Gatus's own /api/v1 and
// renders six empty panels with no clue why. Fail at boot, not in the browser.
func TelemetryConsole() fiber.Handler {
	if n := strings.Count(telemetryConsoleUpstream, telemetryConsoleUpstreamAPIBase); n != 1 {
		panic("api: vendored telemetry console contains " + string(rune('0'+n)) +
			" occurrences of `" + telemetryConsoleUpstreamAPIBase + "`, expected exactly 1 — " +
			"re-sync from LL-Telemetry and check that the API base line is unchanged")
	}
	html := strings.Replace(telemetryConsoleUpstream, telemetryConsoleUpstreamAPIBase, telemetryConsoleGatusAPIBase, 1)
	return func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/html; charset=utf-8")
		c.Set("Cache-Control", "no-cache")
		// Gatus may frame it. Nobody else may.
		c.Set("X-Frame-Options", "SAMEORIGIN")
		c.Set("Content-Security-Policy", "frame-ancestors 'self'")
		c.Set("X-Content-Type-Options", "nosniff")
		return c.SendString(html)
	}
}
```

The `panic` fires during `createRouter`, which is already the established failure style in this
file (`api/api.go:170`, `:173`).

### Registration in `api/api.go` — ORDER IS LOAD-BEARING

The console route must be registered **before** the proxy's wildcard, or `/v1/telemetry/*` swallows
`/v1/telemetry/console` and the iframe gets JSON. This repo has already been bitten by exactly this
twice — see the comments at `api/api.go:103-105` (`/v1/phones/sweep-pending` before `/v1/phones/:key`)
and `:112-114` (`/v1/monitoring` before `/v1/monitoring/:key`). Same trap, third time.

Add at the end of the protected block (after `api/api.go:185`):

```go
	// LL-Telemetry operations console. The console HTML is vendored and served by
	// Gatus itself; its data comes from the reverse proxy below. Both live on the
	// protected router so telemetry inherits Gatus auth.
	// Static route registered BEFORE the proxy wildcard so it isn't swallowed
	// (same reason as /v1/phones/sweep-pending above).
	protectedAPIRouter.Get("/v1/telemetry/console", TelemetryConsole())
	// ... backend architect's proxy registration goes HERE, after the line above ...
```

---

## 2. Getting `API` to `/api/v1/telemetry` without forking the file

Console line 318 is the single source of truth: `const API='/api/v1';`. Both fetch call sites
(line 333 `api()`, line 578 `apiSend()`) build on it, so one substitution covers GET, POST and DELETE.

**`<base href>` injection — does not work. Reject.** `<base>` only affects the resolution of
*relative* URLs. `API` is root-relative (`/api/v1`), and `fetch('/api/v1/stats')` resolves against
the document origin, ignoring `<base>` entirely. Making it work would require first editing the
console to use a relative base — i.e. the edit we were trying to avoid, plus a `<base>` tag, plus
a subtle trailing-slash footgun.

**Single-line patch to the vendored file — reject.** It works, but it means every upstream sync is
`cp` followed by "remember to re-apply the one-liner." That is exactly how a vendored copy becomes
an unmaintainable fork: the first time someone forgets, the console silently queries Gatus's own
`/api/v1/stats` (404) and the panels come up empty with no error that names the cause.

**Serve-time string replacement — recommended.** Implemented above. Properties:

- The vendored file on disk is **byte-identical to upstream**. Syncing is `cp`, and drift is
  detectable with `diff api/assets/telemetry-console.html ../LL-Telemetry/telemetry/dashboard/index.html`
  returning empty. That is the whole maintainability argument.
- The brittleness of exact-string matching (upstream reformats `const API = '/api/v1'` with spaces
  and the match silently fails) is neutralised by the `Count(...) != 1` assertion. Silent breakage
  becomes a loud boot panic naming the file and the expected string. This is the trade that makes
  string replacement acceptable rather than sloppy.
- Cost is one `strings.Replace` on ~28KB, executed once at router construction. Not per request.

**Add `api/assets/TELEMETRY-CONSOLE-SYNC.md`** so the contract survives the author:

```markdown
# Vendored telemetry console — sync procedure

Source of truth: LL-Telemetry/telemetry/dashboard/index.html
This copy MUST stay byte-identical to it. Do not hand-edit.

To sync:
  1. Fix/change the console UPSTREAM in LL-Telemetry.
  2. cp ../LL-Telemetry/telemetry/dashboard/index.html api/assets/telemetry-console.html
  3. docker compose build   (no npm needed — this file is not part of the Vue bundle)
  4. Verify: `diff` against upstream returns nothing.

Gatus applies exactly ONE transformation, at start-up, in api/telemetry_console.go:
  const API='/api/v1';  ->  const API='/api/v1/telemetry';
If upstream reformats that line, Gatus panics at boot with a message naming it.
Fix the constant in telemetry_console.go — do NOT edit this HTML.

Known upstream divergence (deliberately NOT patched here):
  The 1h/6h/24h range buttons all send days=1 and applyRiver() never filters by
  range, so "1h" still lists 24h of events. Fix belongs upstream (an `hours`
  query param on /runs). See architect-4 item 7.
```

---

## 3. The Vue view — `web/app/src/views/TelemetryConsole.vue`

### Layout reasoning

- The Gatus header is `sticky top-0` and **`flex-wrap`** (`App.vue:13`), so its height is *not*
  constant — it grows when the controls wrap at narrow widths, and it's `display:none` under
  `.fs-active` (`index.css:270-273`). A hardcoded `calc(100vh - 64px)` breaks in both cases.
  The view measures its own `getBoundingClientRect().top` and re-measures via a `ResizeObserver`
  on the `<header>` element.
- **No double scrollbars.** While this view is mounted it adds `body.telemetry-open`, which sets
  `overflow:hidden` on the body and hides the Gatus footer. The iframe fills exactly the space from
  the header's bottom edge to the viewport bottom, and the console's own document is the only
  scroll context. (Gatus's global `*{scrollbar-width:none}` at `index.css:239-247` doesn't cross
  the iframe boundary, so the console keeps its real scrollbars — which is what it was designed for.)
- **No extra toolbar.** Gatus's header + a Gatus sub-toolbar + the console's own 46px bar would be
  three stacked bars. Back-to-dashboard already exists on the header logo. Reload lives in the error
  state, where it's actually needed.
- **Error detection.** An iframe's `onload` fires for 404/500/proxy-error pages too, so it can't be
  used as a health signal. The view pre-flights `GET /api/v1/telemetry/stats?days=1` (the same call
  `refresh()` makes at console line 542) and only mounts the iframe on success. A separate 15s
  watchdog catches the case where the HTML itself never loads.
- **`announcements` prop is declared** even though unused: `App.vue:111` passes it to every routed
  component, and an undeclared array prop would fall through as a stringified DOM attribute.
- Gatus's `router-view` uses `<transition mode="out-in">`, so unmount completes before the next
  view mounts — the body class is always cleaned up before anything else renders.

### Complete source

```vue
<template>
  <div ref="hostEl" class="telemetry-view" :style="{ height: hostHeight }">
    <!-- LOADING -->
    <div v-if="state === 'probing' || (state === 'ready' && !frameLoaded)" class="tele-overlay">
      <Loading size="lg" />
      <p class="tele-note">Connecting to LL&#8209;Telemetry&hellip;</p>
    </div>

    <!-- ERROR -->
    <div v-else-if="state === 'error'" class="tele-overlay">
      <div class="tele-card">
        <div class="tele-card-head">
          <AlertTriangle class="h-5 w-5 text-destructive shrink-0" />
          <h2 class="text-lg font-semibold leading-none tracking-tight">Telemetry is unreachable</h2>
        </div>
        <p class="tele-card-body">{{ errorHint }}</p>
        <pre class="tele-err">{{ errorDetail }}</pre>
        <div class="tele-card-actions">
          <Button variant="outline" size="sm" @click="probe" :disabled="retrying">
            <RefreshCw class="h-4 w-4 mr-2" :class="{ 'animate-spin': retrying }" />
            Try again
          </Button>
          <router-link
            to="/"
            class="inline-flex items-center justify-center h-9 px-4 rounded-md text-sm font-medium text-muted-foreground hover:bg-accent hover:text-accent-foreground transition-colors"
          >
            Back to dashboard
          </router-link>
        </div>
      </div>
    </div>

    <!-- CONSOLE -->
    <iframe
      v-if="state === 'ready'"
      :key="frameKey"
      ref="frameEl"
      class="tele-frame"
      :class="{ 'is-loaded': frameLoaded }"
      src="/api/v1/telemetry/console"
      title="LL-Telemetry operations console"
      sandbox="allow-scripts allow-same-origin"
      allow="clipboard-write"
      referrerpolicy="no-referrer"
      @load="onFrameLoad"
    ></iframe>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { AlertTriangle, RefreshCw } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import Loading from '@/components/Loading.vue'

// App.vue passes this to every routed component (App.vue:111). Declared so it
// doesn't fall through onto the root <div> as a stringified attribute.
defineProps({
  announcements: { type: Array, default: () => [] },
})

const PROBE_URL = '/api/v1/telemetry/stats?days=1'
const PROBE_TIMEOUT_MS = 10000
const FRAME_TIMEOUT_MS = 15000

const state = ref('probing')     // 'probing' | 'ready' | 'error'
const frameLoaded = ref(false)
const frameKey = ref(0)          // bumped to force a real iframe reload on retry
const retrying = ref(false)
const errorDetail = ref('')
const errorHint = ref('')

const hostEl = ref(null)
const frameEl = ref(null)
let frameTimer = null
let headerObserver = null

/* ---------------- sizing ----------------
   The header is sticky, flex-wrap (so its height varies with width) and hidden
   entirely in fullscreen. Measure rather than assume. */
const hostHeight = ref('70vh')
const measure = () => {
  if (!hostEl.value) return
  const top = hostEl.value.getBoundingClientRect().top
  hostHeight.value = `${Math.max(320, Math.round(window.innerHeight - top))}px`
}

/* ---------------- health probe ----------------
   An iframe fires `load` for 404s and proxy errors alike, so it can't tell us
   whether the telemetry stack is actually up. Ask the API directly first — this
   is the same call the console itself makes on boot (console line 542). */
const probe = async () => {
  retrying.value = state.value === 'error'
  state.value = 'probing'
  frameLoaded.value = false
  errorDetail.value = ''
  errorHint.value = ''
  clearTimeout(frameTimer)

  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), PROBE_TIMEOUT_MS)
  try {
    const res = await fetch(PROBE_URL, {
      signal: controller.signal,
      credentials: 'include',
      cache: 'no-store',
      headers: { accept: 'application/json' },
    })
    if (res.status === 401 || res.status === 403) {
      throw new Error(`GET ${PROBE_URL} → ${res.status} — not authorised`)
    }
    if (!res.ok) {
      throw new Error(`GET ${PROBE_URL} → ${res.status} ${res.statusText || ''}`.trim())
    }
    await res.json()   // a 200 that isn't JSON means we proxied to the wrong place
    frameKey.value++
    state.value = 'ready'
    frameTimer = setTimeout(() => {
      if (!frameLoaded.value) {
        errorHint.value = 'The telemetry API answered, but the console page never finished loading.'
        errorDetail.value = `GET /api/v1/telemetry/console did not load within ${FRAME_TIMEOUT_MS / 1000}s`
        state.value = 'error'
      }
    }, FRAME_TIMEOUT_MS)
  } catch (e) {
    if (e.name === 'AbortError') {
      errorHint.value = 'The telemetry API did not answer in time. The stack may be starting up, or the proxy target may be wrong.'
      errorDetail.value = `GET ${PROBE_URL} timed out after ${PROBE_TIMEOUT_MS / 1000}s`
    } else if (/401|403/.test(e.message)) {
      errorHint.value = 'Gatus refused the telemetry request. Your session may have expired — reload the page and sign in again.'
      errorDetail.value = e.message
    } else {
      errorHint.value = 'Gatus proxies this console to the LL-Telemetry API, and that call failed. The console would only render empty panels, so it is not shown.'
      errorDetail.value = e.message || String(e)
    }
    state.value = 'error'
  } finally {
    clearTimeout(timeout)
    retrying.value = false
  }
}

const onFrameLoad = () => {
  clearTimeout(frameTimer)
  frameLoaded.value = true
  measure()
}

onMounted(() => {
  // Body owns no scroll while the console is up: the iframe document is the only
  // scroll context, and the Gatus footer would otherwise push the page taller
  // than the viewport and produce a second scrollbar.
  document.body.classList.add('telemetry-open')
  measure()
  probe()
  window.addEventListener('resize', measure)
  document.addEventListener('fullscreenchange', measure)
  // The header wraps at narrow widths and vanishes in fullscreen — both change
  // where this view starts.
  const header = document.querySelector('header')
  if (header && 'ResizeObserver' in window) {
    headerObserver = new ResizeObserver(measure)
    headerObserver.observe(header)
  }
})

onUnmounted(() => {
  document.body.classList.remove('telemetry-open')
  window.removeEventListener('resize', measure)
  document.removeEventListener('fullscreenchange', measure)
  headerObserver?.disconnect()
  headerObserver = null
  clearTimeout(frameTimer)
  // Note: the console's uncleared 5s live-tail setInterval (console line 559)
  // dies with the iframe document when this view unmounts. Nothing to clean up.
})
</script>

<style scoped>
.telemetry-view {
  position: relative;
  width: 100%;
  overflow: hidden;
  background: hsl(var(--background));
}

.tele-frame {
  display: block;
  width: 100%;
  height: 100%;
  border: 0;
  opacity: 0;
  transition: opacity var(--dur-2, 180ms) ease;
}
.tele-frame.is-loaded {
  opacity: 1;
}

.tele-overlay {
  position: absolute;
  inset: 0;
  z-index: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 1rem;
  padding: 1.5rem;
  background: hsl(var(--background));
}

.tele-note {
  font-size: 0.875rem;
  color: hsl(var(--muted-foreground));
}

.tele-card {
  width: 100%;
  max-width: 34rem;
  padding: 1.25rem 1.5rem 1.5rem;
  border: 1px solid hsl(var(--border));
  border-radius: var(--radius, 0.5rem);
  background: hsl(var(--card));
}
.tele-card-head {
  display: flex;
  align-items: center;
  gap: 0.625rem;
  margin-bottom: 0.75rem;
}
.tele-card-body {
  font-size: 0.875rem;
  line-height: 1.55;
  color: hsl(var(--muted-foreground));
}
.tele-err {
  margin-top: 0.875rem;
  padding: 0.625rem 0.75rem;
  border-radius: calc(var(--radius, 0.5rem) - 2px);
  background: hsl(var(--muted));
  color: hsl(var(--foreground));
  font-family: ui-monospace, "Cascadia Mono", Consolas, monospace;
  font-size: 0.75rem;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}
.tele-card-actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-top: 1.125rem;
}
</style>

<style>
/* Deliberately NOT scoped — these target elements outside this component.
   Gated on a body class that is added on mount and removed on unmount, so the
   rules are inert on every other route even though the CSS is always present
   in the bundle. */
body.telemetry-open {
  overflow: hidden;
}
body.telemetry-open footer {
  display: none !important;
}
</style>
```

**Why the console's `html,body{height:100%}` is satisfied:** the iframe element has a definite
pixel height (`.telemetry-view` is `height: <measured>px`, `.tele-frame` is `height:100%`), so the
framed document's viewport has a real height and `height:100%` resolves. The console's `.wrap`
overflows that and the iframe document scrolls internally — which is the intended behaviour of a
619-line dashboard.

---

## 4. The header button

**Icon: `SatelliteDish`** from `lucide-vue-next` (already a dependency, `^0.539.0`). Reasoning:
the other header glyphs are `Volume2`/`VolumeX`, `Maximize`/`Minimize`, `RefreshCw` — all thin
geometric outlines. `Activity` (the obvious "telemetry" pick) is a pulse line that reads as
"monitoring", which describes the entire rest of the dashboard and therefore distinguishes nothing.
`SatelliteDish` has a distinct asymmetric silhouette at 20px and reads unambiguously as
"field machines reporting in". Fallback if the team prefers something flatter: `Radio`.

**Critically, it is an inline `<svg>`, not an `<img>`.** The `header img { height: 38px !important }`
and `html.dark header img { filter: brightness(0) invert(1) }` rules from `config.yaml`'s
`ui.custom-css` (lines 22 and 24) cannot match it, so it needs none of the background-image `<span>`
workaround the Jira button requires. It inherits `currentColor` and themes correctly for free.

**Placement:** immediately after the Jira `<router-link>`, i.e. last in the icon cluster that closes
at `App.vue:88`. Nothing above it moves, so existing muscle memory for the Jira button is intact.

### Edit 1 — insert after `App.vue:87` (the Jira `</router-link>`), before the closing `</div>` on line 88

```html
                <router-link
                  to="/telemetry"
                  class="inline-flex items-center justify-center h-9 w-9 rounded-md hover:bg-accent transition-colors"
                  data-tooltip="Telemetry console"
                  data-tip-pos="bottom"
                  aria-label="Telemetry console"
                >
                  <!-- A lucide SVG, not an <img>: the header's custom-css rules
                       (`header img { height: 38px !important }` and the dark-mode
                       `filter: brightness(0) invert(1)`) only match <img>, so this
                       needs none of the background-image workaround the Jira icon
                       above requires. -->
                  <SatelliteDish class="h-5 w-5" />
                </router-link>
```

### Edit 2 — `App.vue:180`, add the import

```js
import { LogIn, Maximize, Minimize, RefreshCw, SatelliteDish, Volume2, VolumeX } from 'lucide-vue-next'
```

(`<script setup>` auto-exposes it to the template — no `components:` registration needed.)

---

## 5. Router + server-side route

### `web/app/src/router/index.js`

Import, after line 6:

```js
import TelemetryConsole from '@/views/TelemetryConsole';
```

Route, appended to the `routes` array (after the `/jira` entry that ends at line 35 — add a comma
to that entry's closing brace):

```js
    {
        // LL-Telemetry operations console, hosted in a same-origin iframe. The
        // console HTML and its data both come from /api/v1/telemetry/* so they
        // sit behind Gatus auth together.
        path: '/telemetry',
        name: 'Telemetry',
        component: TelemetryConsole
    }
```

### `api/api.go` — the SPA mirror

`createWebHistory` with no catch-all means every SPA path needs its own server route or a deep
link / F5 on `/telemetry` returns 404. Add after `api/api.go:138`:

```go
	app.Get("/telemetry", SinglePageApplication(cfg.UI))
```

This must stay in the SPA block (before the `fiberfs` catch-all at line 158), alongside the other
four mirrored routes.

**Verified non-collision:** `app.Get("/telemetry")` is an exact-match route, so `/api/v1/telemetry/console`
and `/api/v1/telemetry/stats` are unaffected — they're under `/api` and matched by the
`apiRouter` group registered at line 82.

---

## 6. Iframe hardening

```html
sandbox="allow-scripts allow-same-origin"
allow="clipboard-write"
referrerpolicy="no-referrer"
title="LL-Telemetry operations console"
```

**What the console needs, checked against its source:**

| Capability | Needed? | Evidence |
|---|---|---|
| `allow-scripts` | **Required** | The entire console is the `<script>` at lines 317-617. |
| `allow-same-origin` | **Required** | See below. |
| `allow-forms` | No | Zero `<form>` elements; the key-label input is bare (line 585) and submits via a `keydown` Enter handler (line 601). |
| `allow-modals` | No | No `alert`/`confirm`/`prompt` anywhere. |
| `allow-popups` | No | No `window.open`, no `target="_blank"`. |
| `allow-downloads` | No | No download paths. |
| `allow-top-navigation*` | No | Uses no `location` or `history` (verified). Omitting these is a genuine win: even a future upstream mistake can't navigate Gatus away. |
| `allow-pointer-lock` | No | The ribbon brush uses `setPointerCapture` (line 418), which needs no sandbox token. |

`keydown` handling (lines 573-574) and all dynamic DOM construction need no sandbox token —
they're plain scripting, covered by `allow-scripts`.

**`allow-same-origin` is required. Confirmed, and here is the implication you must accept.**

Without it the framed document gets an opaque, unique origin. Two things break, both fatally:
`fetch('/api/v1/telemetry/stats')` becomes a *cross-origin* request from a `null` origin, so the
browser omits Gatus's ambient session cookie and the request is refused; and the response would be
CORS-blocked regardless (Gatus sets `Access-Control-Allow-Origin` only for `http://localhost:8081`
under `ENVIRONMENT=dev`, `api/api.go:58-63`). Ambient-auth reuse is the entire premise of the
same-origin-iframe decision, so `allow-same-origin` is not negotiable.

**Implication:** `sandbox="allow-scripts allow-same-origin"` on a **same-origin** document is
functionally equivalent to no sandbox at all. The framed document shares Gatus's origin, so its
script can reach `parent.document`, read Gatus's cookies and `localStorage`, and can even remove
its own `sandbox` attribute from the parent DOM and reload itself unsandboxed. This is a documented
property of the sandbox attribute, not a Gatus quirk.

So state the real security posture plainly rather than pretending the attribute is a boundary:

- The sandbox here buys **defence against categories we've excluded** — top-level navigation,
  popups, downloads, modals, plugins. Those are real (a hijacked console can't redirect the whole
  Gatus tab to a phishing page). It buys **nothing** against script running inside the frame.
- The actual control is that **we vendor the file and review it**. It has been verified to make
  zero external requests, and syncing it is a deliberate `cp` + code review + rebuild, never an
  automatic pull. That is the trust decision; the sandbox attribute is hygiene layered on top.
- The `Content-Security-Policy: frame-ancestors 'self'` and `X-Frame-Options: SAMEORIGIN` headers
  set by `TelemetryConsole()` are the more meaningful control: they stop any *other* site from
  framing the authenticated console and reading it through a logged-in user's session.

`allow="clipboard-write"` — the Keys panel's Copy button uses `navigator.clipboard.writeText`
(console line 602). Permissions-Policy defaults `clipboard-write` to `self`, so a same-origin frame
inherits it, but declaring it explicitly costs nothing and survives a future top-level policy header.

`referrerpolicy="no-referrer"` — the console makes zero external requests, so there is nothing to
leak either way; this is belt-and-braces against a future upstream change adding an outbound asset.

`title` is required for screen readers — an untitled iframe is announced as "frame".

---

## 7. Console bug fixes to apply while vendoring

**Both fixes go UPSTREAM in `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\dashboard\index.html` first**, then get vendored. This is what keeps `api/assets/telemetry-console.html`
byte-identical to upstream and preserves the whole maintainability argument from item 2.

### 7a. `fmt` ReferenceError — FIX, mandatory

Console line 590 calls `fmt(k.created_utc)`. No `fmt` exists — only `fmtTime` (line 336). Every key
row throws, `renderKeys` aborts mid-`map`, and the caller's `catch` in `loadKeys` (line 605) paints
the whole Keys panel as an error box. Since the brief includes the Keys panel, this is a blocker,
not a polish item.

`fmtTime` is the wrong substitute: it renders `HH:MM:SS.mmm`, which is useless for a "Created"
column on a key that may be months old. Add a date formatter — 1 new line + 1 changed line:

**Insert after line 337** (`rel()`), next to the other formatters:

```js
function fmtDate(iso){if(!iso)return'—';const d=new Date(iso);return `${d.getFullYear()}-${pad2(d.getMonth()+1)}-${pad2(d.getDate())}`;}
```

**Change line 590** from:

```js
    <td>${k.created_utc?fmt(k.created_utc):''}</td>
```

to:

```js
    <td>${fmtDate(k.created_utc)}</td>
```

(`pad2` is already defined at line 327; the `?:` guard moves inside the helper, matching how
`rel()` handles its own null case at line 337.)

### 7b. Range-filter quirk — DEFER, do not patch while vendoring

`RANGES` (line 324) gives `1h`, `6h` and `24h` all `days:1`, `loadRiver` sends only `days`
(line 529), and `applyRiver` filters by severity and brush but never by range (lines 478-481).
So "1h" lists 24 hours of events. Real, and confusing.

**My call: leave it.** Three reasons:

1. **It is not a crash.** Unlike 7a it degrades a filter's precision; nothing errors and no data is
   wrong, just over-inclusive. It does not block shipping the console inside Gatus.
2. **The obvious client-side patch creates a worse inconsistency.** Adding
   `rows = rows.filter(r => new Date(r.received_utc).getTime() >= Date.now() - RANGES[S.range].hours*3600e3)`
   to `applyRiver` makes the footer read "12 shown" beside "4,310 in range" (line 491, fed by the
   server's `days`-scoped total), and breaks pagination outright: "older ›" would fetch page 2 of a
   24h result set and then filter almost all of it away, so paging through a 1h view yields mostly
   empty pages. The stats/charts/ribbon above would still show 24h, since they're driven by
   `days`/`hours` server-side.
3. **The correct fix is server-side and upstream** — an `hours` param on `/runs` so `days` stops
   being the only window control, which then makes `total`, pagination and the panels all agree.
   That belongs in the LL-Telemetry API, not in a Gatus vendoring commit, and doing it here would
   be exactly the fork we're trying not to create.

Record it in `TELEMETRY-CONSOLE-SYNC.md` (text included in item 2) so it's a known, tracked
divergence rather than a surprise. If someone insists on the cosmetic fix before the API grows an
`hours` param, do it upstream *and* change line 490's `rcount` text and disable the pager when
`S.range` is sub-daily — not the one-line filter alone.

---

## 8. Frontend build + commit sequence (no local node)

### The trap that will eat the build

`vue.config.js` sets `outputDir: '../static'` — **relative to `web/app`**. Mounting only
`web/app` into the container makes `../static` resolve to `/static` *inside the container*, so the
entire build is written to the container's ephemeral layer and vanishes on `--rm`. The command
appears to succeed and `web/static/` is untouched.

**Mount `web/`, not `web/app/`.**

### Sequence

```powershell
# 0. From the repo root.
cd C:\Users\colby.west\Desktop\Projects\Gatus

# 1. Vendor the console (AFTER applying the 7a fix upstream).
mkdir api\assets -Force
copy ..\LL-Telemetry\telemetry\dashboard\index.html api\assets\telemetry-console.html
# Sanity: byte-identical to upstream.
fc.exe api\assets\telemetry-console.html ..\LL-Telemetry\telemetry\dashboard\index.html

# 2. Frontend edits (no build yet):
#    - web/app/src/views/TelemetryConsole.vue   (new, item 3)
#    - web/app/src/App.vue                      (2 edits, item 4)
#    - web/app/src/router/index.js              (2 edits, item 5)

# 3. Build the SPA in a container. NOTE THE MOUNT: web -> /web, cwd /web/app,
#    so outputDir '../static' lands in the REAL web/static.
docker run --rm -v "${PWD}\web:/web" -w /web/app node:20-alpine `
  sh -c "npm ci && npm run build"

# 4. Confirm the bundle actually changed. If web/static is clean here, the
#    build wrote to the wrong place — recheck the mount in step 3.
git status --short web/static
#   expect: M web/static/js/app.js  (and index.html / css if touched)

# 5. Go edits:
#    - api/telemetry_console.go                 (new, item 1)
#    - api/assets/TELEMETRY-CONSOLE-SYNC.md     (new, item 2)
#    - api/api.go                               (2 lines: SPA route + console route
#                                                registered BEFORE the proxy wildcard)

# 6. Build + run locally. A missing/renamed API-base line panics here, loudly.
docker compose build
docker compose up -d
docker compose logs --tail=40 gatus

# 7. Verify in the browser at http://localhost:8080
#    - header shows the dish icon after the Jira icon
#    - clicking it routes to /telemetry and the console renders
#    - F5 ON /telemetry returns the SPA, not a 404   <- proves the api.go SPA line
#    - KEYS button opens the drawer and rows render (proves the 7a fix)
#    - devtools Network: console requests go to /api/v1/telemetry/*, no 404s
#    - only ONE scrollbar (the console's), no Gatus footer visible
#    - narrow the window until the header wraps: the iframe still ends at the
#      viewport bottom

# 8. Commit. web/static is COMMITTED and go:embed'd — a frontend change is not
#    live until the built bundle is in the commit.
git add web/app/src web/static api/telemetry_console.go api/assets api/api.go
git commit
```

`node_modules` lands in `web/app/node_modules` (mounted, so it persists and makes the next
`npm ci` faster) and is already covered by `.gitignore`'s bare `node_modules` rule. `npm ci` is
safe — `web/app/package-lock.json` exists.

**Do not push to prod.** Per the standing deploy rule, this sequence stops at the local
`docker compose` + commit; deployment to production is the user's own step.

### Files touched, complete list

| File | Change |
|---|---|
| `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\views\TelemetryConsole.vue` | new — iframe host (item 3) |
| `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\App.vue` | import + header `<router-link>` (item 4) |
| `C:\Users\colby.west\Desktop\Projects\Gatus\web\app\src\router\index.js` | import + `/telemetry` route (item 5) |
| `C:\Users\colby.west\Desktop\Projects\Gatus\api\telemetry_console.go` | new — embed + rewrite + handler (item 1) |
| `C:\Users\colby.west\Desktop\Projects\Gatus\api\assets\telemetry-console.html` | new — vendored console |
| `C:\Users\colby.west\Desktop\Projects\Gatus\api\assets\TELEMETRY-CONSOLE-SYNC.md` | new — sync contract (item 2) |
| `C:\Users\colby.west\Desktop\Projects\Gatus\api\api.go` | SPA route + console route (items 1, 5) |
| `C:\Users\colby.west\Desktop\Projects\Gatus\web\static\**` | regenerated bundle, committed |
| `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\dashboard\index.html` | `fmtDate` fix (item 7a) |

Not touched: `config.yaml` (no custom-css change needed — the button is an SVG),
`web/app/public/` (nothing added there by design), `web/app/vue.config.js`, `Dockerfile`,
`web/static.go`.

---

## Handoff notes for the backend architect

1. Register the proxy **after** `protectedAPIRouter.Get("/v1/telemetry/console", TelemetryConsole())`.
   A `/v1/telemetry/*` wildcard registered first swallows the console route and the iframe receives
   JSON. Third occurrence of this exact trap in this file (see `api/api.go:103-105`, `:112-114`).
2. The proxy must forward **GET, POST and DELETE** — the Keys panel uses all three
   (`apiSend`, console lines 577-581, 606-611).
3. Paths the console actually calls, relative to `/api/v1/telemetry`:
   `/stats?days=N`, `/timeline?hours=N&buckets=N`, `/runs?days&limit&offset[&q&script&site]`,
   `/runs/:id`, `/keys`, `POST /keys`, `POST /keys/:id/revoke`, `POST /keys/:id/rotate`,
   `DELETE /keys/:id`.
4. The Vue view pre-flights `GET /api/v1/telemetry/stats?days=1` and requires a JSON body on 200.
   Make sure the proxy passes the upstream content type through rather than wrapping responses.
5. Prefer a proxy that returns a **non-2xx on upstream failure** (502/504) rather than an empty 200 —
   the view's error state keys off status, and a 200 with an HTML error body will trip the
   `res.json()` guard into a confusing parse error instead of a clean "unreachable" message.
