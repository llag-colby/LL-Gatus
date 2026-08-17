# Reviewer 3 - Convention & Integration Fit (LL-Telemetry)

Scope: `api/telemetry.go`, `api/assets/telemetry-console.html`, `web/app/src/views/TelemetryConsole.vue`,
`web/app/src/App.vue` (new router-link), `web/app/src/router/index.js` (new route),
`docker-compose.telemetry.yml`, `collector/seed_telemetry.py`, `docs/ll-telemetry.md`, `.env.example`.

Baselines read: `jira/jira.go`, `api/jira.go`, `api/api.go`, `api/unifi_inventory.go`,
`collector/unifi_collector.py`, `collector/phone_collector.py`, `web/app/src/views/JiraDetails.vue`,
`FirewallDetails.vue`, `WirelessDetails.vue`, `PhoneDetails.vue`, `SiteOverview.vue`,
`web/app/src/index.css`, `.tk/RULES.md`.

Only findings at >=80% confidence are listed.

---

## BLOCKERS

None. Nothing here is wrong enough to hold the merge.

---

## FINDINGS

### 1. Log prefix is a package name, not `[package.Function]`
CONFIDENCE: 90%
FILE: api/telemetry.go
LINE: 162, 164

Both lines log `[api.telemetry]`. The house prefix is `[package.Function]`:
`[jira.StartPoller]` (jira/jira.go:238), `[jira.poll]` (jira/jira.go:276),
`[api.SetUniFiSnapshot]` (api/unifi_inventory.go:90), `[api.New]` (api/api.go:33) - and
`[api.TelemetryProxy]` three lines down in this very file (399, 412, 395).

FIX: `[api.telemetry]` -> `[api.telemetryCfg]` on both lines.

---

### 2. `telemetryRequireRevoked` swallows every error, and all of them become 409
CONFIDENCE: 85%
FILE: api/telemetry.go
LINE: 427-463 (raised at 361-363)

Three genuinely different failures - request construction, upstream unreachable, undecodable
response - collapse into the identical opaque string `"could not verify the key state before
deleting"`, and nothing is logged. Everywhere else in this file and in `jira/jira.go` an
upstream failure is logged at Warn with the underlying error attached (telemetry.go:399, 412;
jira/jira.go:276, 305).

Worse, line 362 maps *all* of them to `fiber.StatusConflict`. So "the telemetry API is down"
reaches the console as a 409, next to the genuine 409 ("revoke this key first"). The rest of the
proxy correctly uses 502 for an unreachable upstream.

FIX: log `logr.Warnf("[api.telemetryRequireRevoked] could not read /keys: %s", err.Error())` on
each transport/decode failure, and distinguish the cases at the call site - e.g. return a sentinel
`errTelemetryKeyActive` and map only that to 409, everything else to `fiber.StatusBadGateway`.

---

### 3. Outbound request built without a context, unlike the rest of the file
CONFIDENCE: 90%
FILE: api/telemetry.go
LINE: 431

`http.NewRequest(http.MethodGet, ...)`. `TelemetryProxy` at line 381 uses
`http.NewRequestWithContext(c.UserContext(), ...)`. This pre-flight call is not tied to the
client's cancellation, so an abandoned DELETE still holds a 12s upstream call open.

FIX: give `telemetryRequireRevoked` a `ctx context.Context` first parameter, pass
`c.UserContext()` from line 361, and use `http.NewRequestWithContext`.

---

### 4. The documented 504 never happens
CONFIDENCE: 85%
FILE: api/telemetry.go
LINE: 69-71 (and 399-402)

The comment on `telemetryHTTPClient` says the 12s timeout is set "so a slow upstream surfaces as a
clean 504 rather than a severed response". It does not: every `telemetryHTTPClient.Do` error,
timeout included, returns `fiber.StatusBadGateway` (502) at line 400.

The drift propagates - `web/app/src/views/TelemetryConsole.vue:90` branches on
`response.status === 502 || response.status === 504`, and the 504 arm is unreachable.

FIX: pick one. Either return `fiber.StatusGatewayTimeout` when
`errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err)`, or correct the comment and drop
the dead 504 test in the Vue.

---

### 5. Empty/error states bypass the `.notice` vocabulary
CONFIDENCE: 85%
FILE: web/app/src/views/TelemetryConsole.vue
LINE: 9-35

The `unconfigured` and `unreachable` panels hand-roll Tailwind
(`max-w-2xl rounded-md border border-border bg-card p-5` + `<h2 class="text-sm font-semibold">`).
Every other view with these states uses the shared `.notice` / `.notice-title` / `.notice-body` /
`.notice-error` vocabulary with a scoped style block: JiraDetails.vue:37-44 and 394-398,
FirewallDetails.vue:578-585, WirelessDetails.vue:745-747, JiraKanban.vue.

The result reads visibly different from every other failure panel in the app - different border
(solid vs dashed), radius (6px vs 12px) and title weight.

FIX: `<div class="notice">` for unconfigured, `<div class="notice notice-error">` for unreachable,
with `.notice-title` / `.notice-body` inside, and copy the `.notice*` scoped CSS block from
JiraDetails.vue:394-398.

---

### 6. Root element does not match the detail-view container
CONFIDENCE: 85%
FILE: web/app/src/views/TelemetryConsole.vue
LINE: 2

`<div class="w-full">`. Every other detail view roots at
`<div class="dashboard-container detail-page bg-background">` (FirewallDetails, WirelessDetails,
PhoneDetails; SiteOverview adds `site-overview`). `.dashboard-container` and `.detail-page` carry
no rules of their own today - they are the hooks `index.css` scopes fullscreen behaviour against
(see the comment at index.css:286) - so a view that omits them is invisible to any future
detail-page rule.

FIX: `<div class="dashboard-container detail-page bg-background">`.

---

### 7. No back-to-dashboard affordance
CONFIDENCE: 85%
FILE: web/app/src/views/TelemetryConsole.vue
LINE: 1-49

Every drill-in view opens with an `ArrowLeft` router-link to `/` plus an `<h1>`:
JiraDetails.vue:8-11, FirewallDetails.vue:10, PhoneDetails.vue:10, SiteOverview.vue:10,
EndpointDetails.vue:12. This view has none, and the iframe fills the viewport edge to edge, so the
only exits are the browser back button and the header logo.

FIX: add the standard toolbar row above the iframe - `<router-link to="/" data-tooltip="Back to
dashboard" data-tip-pos="bottom"><ArrowLeft class="h-5 w-5" /></router-link>` plus a title - and
subtract its height from `frameStyle` the same way the header is subtracted.

---

### 8. Console frame is short by one header in fullscreen wall mode
CONFIDENCE: 85%
FILE: web/app/src/views/TelemetryConsole.vue
LINE: 74-79

`index.css:252` sets `.fs-active header { display: none !important }`. In fullscreen
`header.offsetHeight` is therefore `0`, the `header.offsetHeight > 0` guard rejects it, and
`headerHeight` stays at the stale 65. The iframe renders `calc(100vh - 65px)` inside a `<main>`
that is `height: 100vh` (index.css:279), leaving a 65px dead band.

There is also no `fullscreenchange` listener; App.vue:337 registers one for exactly this reason.

FIX:
```js
const measureHeader = () => {
  const header = document.querySelector('header')
  headerHeight.value = header ? header.offsetHeight : 0
}
```
and add `document.addEventListener('fullscreenchange', measureHeader)` in `onMounted` with the
matching `removeEventListener` in the teardown hook.

---

### 9. Seeder does not load `.env`, unlike both other collectors
CONFIDENCE: 80%
FILE: collector/seed_telemetry.py
LINE: 29-32

`BASE`, `TOKEN`, `RUN_COUNT`, `SPAN_DAYS` come straight from `os.environ` at import time.
`unifi_collector.py:124` and `phone_collector.py:131` both define an identical `load_dotenv()`
that walks `./.env` then `../.env`, and call it as the first statement of `main()`.

Inside the compose profile this is harmless - `docker-compose.telemetry.yml:82-85` injects the
values. But running `python3 collector/seed_telemetry.py` from the host, which is exactly how the
other two collectors are run one-shot, silently seeds with an empty `LL_INGEST_TOKEN`.

FIX: copy the `load_dotenv()` helper verbatim, call it first in `main()`, and read the four
settings inside `main()` rather than at module scope.

---

### 10. Dead branch and redundant exception tuple in the seeder
CONFIDENCE: 85%
FILE: collector/seed_telemetry.py
LINE: 211-212 (also 143)

`urllib.request.urlopen` raises `HTTPError` for any non-2xx/3xx and follows 3xx, so `post()` can
only ever return a 2xx status. The `else: failed += 1` arm at 211-212 is unreachable - every
rejection lands in the `except urllib.error.HTTPError` handler instead.

Line 143 `except (urllib.error.URLError, urllib.error.HTTPError, OSError)` is also redundant:
`HTTPError` subclasses `URLError`, which subclasses `OSError`, so the tuple reduces to `OSError`.

FIX: drop the `else` arm at 211-212 (or keep it with a comment saying it is defensive), and
collapse line 143 to `except OSError:`.

---

### 11. Doc drift: the console is 622 lines, not 619
CONFIDENCE: 90%
FILE: docs/ll-telemetry.md
LINE: 34

"The console is one self-contained 619-line file". `api/assets/telemetry-console.html` is 622
lines. Since the doc's whole thesis is that the file is vendored byte-identical and drift is a
`diff`, a stale line count in the doc is the one number that will keep going stale.

FIX: drop the number - "one self-contained single-file console" carries the same argument.

---

### 12. Literal default secrets in the compose file
CONFIDENCE: 80%
FILE: docker-compose.telemetry.yml
LINE: 31, 34, 64

`${LL_DB_ROOT_PASS:-lltelroot}`, `${LL_DB_PASS:-lltel}` (twice), and
`${LL_INGEST_TOKEN:-local-dev-ingest-token}`. `.tk/RULES.md:6` is unconditional: "No hardcoded
secrets or API keys - secrets come from `.env` / env vars only." `.env.example:30-32` already
declares all three, so the fallbacks are not needed for a documented first run.

Mitigating: this stack is local-dev-only, MariaDB is on an `internal: true` network, and
`lltel-api` publishes no ports. This is the letter of the rule rather than a live exposure - but
it is the only mechanical rules violation in the changeset.

FIX: drop the `:-default` suffixes so `docker compose` fails loudly on a missing value, and note in
`docs/ll-telemetry.md` that the three `LL_*` vars must be set before the stack will come up.

---

### 13. Seeder env vars are undocumented
CONFIDENCE: 85%
FILE: docs/ll-telemetry.md
LINE: 162-176 ("Seeding demo data")

`seed_telemetry.py:29-32` reads `LLTEL_BASE`, `LL_INGEST_TOKEN`, `SEED_RUNS` (default 400) and
`SEED_DAYS` (default 3). The doc says "a few hundred runs" and names none of them, so there is no
documented way to change the volume or the span.

FIX: add a two-line note under the seed command listing `SEED_RUNS` and `SEED_DAYS` with their
defaults.

---

### 14. `close` shadows the builtin
CONFIDENCE: 80%
FILE: api/telemetry.go
LINE: 118

`func inlineHash(doc, open, close string) string` shadows the predeclared `close`. Harmless here,
but it is the kind of thing a linter flags and no other function in the package does it.

FIX: rename the parameters to `openTag` / `closeTag`.

---

### 15. Import brace spacing and lifecycle hook differ from the house
CONFIDENCE: 80%
FILE: web/app/src/views/TelemetryConsole.vue
LINE: 52-53, 106

Lines 52-53 use `{ref, onMounted, ...}` and `{type: Array, default: () => []}` with no inner
spaces; line 54 in the same file uses `{ Button }` with spaces, as do App.vue:194 and
JiraDetails.vue:173-179. Line 106 uses `onBeforeUnmount`; every other view and App.vue use
`onUnmounted`.

FIX: space the braces on 52-53 and 57, and switch to `onUnmounted` for consistency with
JiraDetails.vue:369 and App.vue:344.

---

## VERIFIED CLEAN

These were checked against the code and are correct. Recorded so they are not re-litigated.

**Q1 - logging levels.** No misuse. Unreachable upstream is `logr.Warnf` (telemetry.go:399),
non-2xx upstream is `logr.Warnf` (412), key mutations are `logr.Infof` (395), configuration state
is `logr.Info`/`Infof` (162, 164) - the same shape as `jira/jira.go:238/242/276/305`. Nothing
third-party is logged at Error. Only the prefix (finding 1) is off.

**Q2 - error-response shape.** Defensible and consistent. The proxy is a synchronous fetch, so it
returns real status codes (403/409/413/502), matching `GetJiraIssue`'s 502 (api/jira.go:30) and
`GetUniFiSnapshot`'s 404 (api/unifi_inventory.go:102). The always-200-with-`ok`/`error` shape in
`GetJiraMetrics` and `GetJiraBoard` exists because those serve a *cached* snapshot that can carry
a `configured`/`ok` flag; telemetry caches nothing, so there is no payload to hang the flag on.
The 503-when-unconfigured at telemetry.go:199 is the deliberate signal `TelemetryConsole.vue:88`
branches on. Finding 2 is the one place this breaks down.

**Q3 - view conventions, other than findings 5-8 and 15.** `Loading` from
`@/components/Loading.vue` and `{ Button }` from `@/components/ui/button` match JiraDetails.vue:175
and App.vue:194/201. The `announcements` prop is declared (`{type: Array, default: () => []}`),
matching Home.vue:118 - App.vue:124 passes it to every routed component, and declaring it prevents
it landing as a stringified DOM attribute. No provide/inject, so RULES.md:22 holds. On RULES.md:21
(`dark:` classes): the view uses the CSS-variable tokens `bg-card`, `border-border`,
`text-muted-foreground`, `bg-background`, which is what every view except SuiteDetails.vue now
does - it is the current house practice, not a deviation.

**Q4 - header button.** Exact parity with the Jira router-link two lines above (App.vue:75-87):
same `inline-flex items-center justify-center h-9 w-9 rounded-md hover:bg-accent transition-colors`,
same `data-tooltip` + `data-tip-pos="bottom"`, same `aria-label` mirroring the tooltip text. The
`SatelliteDish` import is correctly added to the existing alphabetised lucide import at App.vue:193.
The comment explaining why this one can be an inline SVG (the `header img` custom-CSS filter only
matches `<img>`) is accurate.

**Q5 - seeder style, other than findings 9-10.** Stdlib only (`json`, `os`, `random`, `sys`,
`time`, `urllib.*`, `uuid`, `datetime`). No emojis. Module docstring, `#!/usr/bin/env python3` and
`if __name__ == "__main__": sys.exit(main())` all match the other two collectors. Every network
call is inside a handler; the 429 back-off at 215-221 is a real retry loop, not a swallow; failures
are counted and reported on exit code.

**Q6 - RULES.md sweep.** No TODO/FIXME/placeholder. No `alert(`/`confirm(`/`prompt(` - verified in
both `TelemetryConsole.vue` and all 622 lines of the vendored console. No emojis (the console's two
`✕` are U+2715 dingbats inside vendored markup). No unhandled async: `probe()` wraps its `fetch` in
try/catch and sets `unreachable` on throw. Em dashes appear only in Go comments and one `logr.Info`
string (telemetry.go:162), exactly mirroring `jira/jira.go:238` - not UI copy. Route ordering
(RULES.md:17-18) is correct: telemetry registers under `protectedAPIRouter` *after*
`ApplySecurityMiddleware` (api/api.go:181 then 205-207), and `/console` registers before the
`/*` wildcard. `web/static/` was rebuilt, not hand-edited (RULES.md:14) - `web/static/js/app.js`
contains both `ll-telemetry` and `Field-script telemetry`.

**Q7 - doc accuracy.** Verified against code, all correct: the allowlist table matches
`telemetryAllowlist` (telemetry.go:279-290) entry for entry; that allowlist in turn covers exactly
the five paths the console actually calls (`/stats?days=`, `/timeline?hours=&buckets=`, `/runs?`,
`/runs/{id}`, `/keys`) plus the four key mutations (`POST /keys`, `/keys/{id}/revoke`,
`/keys/{id}/rotate`, `DELETE /keys/{id}`) - no over-grant and no missing grant. `POST /runs` is
absent as documented. The `%`-rejection (298-301), never-forward-XFF (empty header map, 385-391),
never-echo-upstream-body (409-416) and 12s timeout (71) claims are all accurate. Every CSP claim
checks out: the file has exactly 12 `style="` attributes, zero `on*` handler attributes, zero
`eval`/`new Function`/`javascript:`, and its two `fetch` calls send no `Authorization` header.
`const API='/api/v1'` occurs exactly once, so the startup assertion at telemetry.go:78 is live.
The env-var table matches `telemetryCfg()` including "bcrypt wins if both are set" (177-185). The
`/ll-telemetry` SPA deep link exists at api/api.go:147. `lltel-api` publishes no ports and
`lltel-internal` is `internal: true`. Only findings 11 and 13 are drift.

**Q8 - dead code in telemetry.go.** None. Every import is used (`path` at 305, `url` at 369,
`subtle` at 178/184, `bcrypt` at 182, `sync` at 65, `_ "embed"` for line 47). Every constant is
used: `consoleAPIOriginal` (78, 80, 85), `consoleAPIRewritten` (85), `telemetryUpstreamPrefix`
(336, 431), `telemetryBodyLimit` (376), `telemetryResponseLimit` (405, 451). Every struct field is
read. `golang.org/x/crypto` was already a direct dependency (go.mod:30) so no `go mod tidy` was
needed, and this repo vendors nothing.

---

APPROVED: yes
BLOCKERS: 0
SUGGESTIONS: 15
