# Explorer 4 — LL-Telemetry Operations Console: exhaustive port analysis

**Target file:** `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\dashboard\index.html`
**Size:** 619 lines / 43,845 bytes / single file, zero build, zero dependencies
**Purpose of this doc:** everything needed to port or embed this dashboard into Gatus (Vue 3 + Go).

Supporting files read for endpoint/auth truth:
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\api\main.py` (578 lines, FastAPI, `APP_VERSION = "0.4"`)
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\caddy\Caddyfile` (production, 88 lines)
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\caddy\Caddyfile.local` (dev, 22 lines)
- `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\docker-compose.yml` (dashboard mounted read-only at `/srv/dashboard`, line 52)

---

## 1. File structure — section-by-section outline

| Lines | Section |
|---|---|
| 1–6 | Doctype, `<html lang="en">`, `<head>`, charset, viewport, `<title>LL-Telemetry — Operations Console</title>` |
| **7–241** | **`<style>` block (235 lines, ~38% of file)** |
| 8–30 | `:root` — 25 CSS custom properties, all OKLCH, plus `color-scheme: dark`, `--r`, `--mono`, `--sans` |
| 31–43 | Global reset + base: `*`, `html,body`, `body`, `.mono`, `.num`, `.micro`, `button`, `:focus-visible` |
| 45–71 | Top bar: `.bar`, `.brand`, `.grow`, `.live` (+ `@keyframes pulse` L59, reduced-motion L60), `.seg`, `.search`, `.health` |
| 73–74 | `.wrap` (page container, `max-width:1720px`), `.err` |
| 76–102 | Status band: `.band` (+ `@media max-width:1100px` L78), `.stats`, `.stat`, `.panel`, `.phd`, site board `.sites`/`.site` |
| 104–111 | Ribbon: `.ribbon-wrap`, `.ribbon`, `.rb-legend`, `.brush-note` |
| 113–130 | Charts: `.charts` (+ media query L115), `.cbody`, `.mix`, `.mixkey`, `.hbars`, `.hbar`, `.emptyc` |
| 132–181 | Event river: `.rhd`, `.chips`/`.chip` (6 severity variants L137–142), `.river`, `table.rv`, `.rchip` (L165–171), `.flag`, `.rfoot`, `.emptyr`, `.skel` (+ `@keyframes fresh` L156, `@keyframes sh` L181) |
| 183–211 | Drawer: `.scrim`, `.drawer`, `.dhd`, `.dbody`, `.sec`, `.kv`, `.log`/`.log .ln` |
| 213–240 | Keys panel: `.tbtn`, `.ksec`, `.krow`, `.kbtn`, `.ksecret`, `.kwarn`, `.kval`, `.kmeta`, `table.ktbl`, `.kstat`, `.kact`, `.khint` |
| 242–243 | `</head>`, `<body>` |
| **245–315** | **Markup (71 lines — the entire DOM is 71 lines; everything else is rendered by JS)** |
| 245–259 | Top bar: brand + `v0.4` tag, `#range` segmented control (5 buttons L248–252), `#live`, `#keysbtn`, `#q` search, `#hdot`/`#hstat` health |
| 261–311 | `.wrap` main content |
| 262 | `#err` error slot |
| 264–270 | `.band` → `#stats` (empty, JS-filled) + Sites panel `#sitesub` / `#sites` |
| 272–276 | Severity timeline panel: `#ribsub`, `#brushnote`, `#riblegend`, `<svg id="ribbon">` |
| 278–291 | Charts row: Result mix (`#mix`, `#mixkey`, `#mixsub`), By script (`#byscript`), Repeat offenders (`#offenders`) |
| 293–310 | Event stream panel: `#sevchips`, `#fscript`, `#fsite`, `#clearf`, `#river`, footer `#rcount`/`#prev`/`#next`/`#pinfo` |
| 313 | Run detail drawer (`#scrim` → `#dttl`, `#dclose`, `#dbody`) — one line |
| 315 | Keys drawer (`#kscrim` → `#kclose`, `#kbody`) — one line |
| **317–617** | **`<script>` block (301 lines) — plain classic script, no module, no defer** |
| 318–331 | Constants: `API`, `SEV`, `SEV_STACK`, `SEV_COLOR`, `SEV_LABEL`, `RESULT_PHRASE`, `RANGES`; helpers `sev`/`esc`/`pad2`; state object `S` (L329–331) |
| 333–338 | `api()` fetch wrapper (L333), `showErr`, `fmtTime`, `rel`, `shortScript` |
| 340–347 | `renderHealth(svc)` |
| 349–385 | `renderStats(st)` (350), `renderSites(st)` (367) |
| 387–427 | Ribbon: `RIB` (388), `renderRibbon` (389), `ribbonSpan`/`fracForTime`/`timeForFrac` (412–414), IIFE `brushSetup` (415–421), `updateBrushNote` (422) |
| 429–452 | `renderCharts(st)` — result mix, by-script, offenders |
| 454–461 | `renderChips()` (455), `fillSelect()` (460) |
| 463–494 | `riverRow(r)` (464), `applyRiver()` (478) |
| 496–522 | `logSeverity()` (497), `openDrawer(id)` (502), `closeDrawer()` (522) |
| 524–550 | `resetPage`, `setSearch`, `loadRiver()` (527), `refresh()` (537) |
| 552–574 | Control wiring: range buttons, live toggle, search debounce, selects, clear, pagination, drawer close, **document-level keydown (573–574)** |
| 576–614 | Keys: `apiSend()` (577), `keysErr`, `renderKeys` (583), `loadKeys` (605), `keyCreate` (606), `keyAction` (607), button wiring (612–614) |
| 616 | Boot: `renderChips();updateBrushNote();refresh();` |
| 618–619 | `</body></html>` |

---

## 2. Every API endpoint — complete inventory

**Base URL strategy:** `const API='/api/v1';` — **line 318**. A hardcoded root-relative path. Not configurable, no env, no `<base>`, no `data-` attribute, no `window.API` override. Every request is same-origin relative to whatever host serves the page.

**There are exactly TWO `fetch()` call sites in the file** — line 333 and line 578. All nine logical API calls funnel through these two wrappers.

### The two fetch wrappers

```js
// line 333  — all GETs
async function api(p){const r=await fetch(API+p,{headers:{accept:'application/json'}});if(!r.ok)throw new Error(p+' → '+r.status);return r.json();}

// line 578  — all mutations (POST/DELETE)
const r=await fetch(API+path,{method,headers:body?{'content-type':'application/json'}:{},body:body?JSON.stringify(body):undefined});
```

Note both omit `credentials:` (defaults to `same-origin`) and neither sets an `Authorization` header. See §3.

### Complete endpoint → purpose → consumer table

| # | Line | Method + path | Query params | Consumer panel | Response shape (from `main.py`) |
|---|---|---|---|---|---|
| 1 | **542** | `GET /api/v1/stats` | `days` (1–365; 1, 7, or 30) | **4 panels at once**: stat tiles, site board, all 3 charts, health dot | `{by_script_result:[{script,result,n}], by_site:[{site,n,failures}], repeat_offenders:[{hostname,repairs,last_seen,…}], tls_bypassed:[…], unmapped_subnets:[…], daily:[{d,n,failures}], totals:{total,hosts,offline_queued,tls_bypassed,avg_sec}, service:{version,last_received_utc,runs_last_hour}}` (main.py 358–438) |
| 2 | **542** | `GET /api/v1/timeline` | `hours` (1–2160), `buckets` (6–240) | Severity ribbon + legend + brush | `{hours,buckets,bucket_sec,results:[…],series:[{t:<epoch ms>,counts:{RESULT:n},total:n}]}` (main.py 441–483) |
| 3 | **533** | `GET /api/v1/runs` | `days`, `limit` (250), `offset`, `q?`, `script?`, `site?` | Event river table + pagination footer | `{count,total,limit,offset,runs:[{id,run_id,received_utc,script,script_version,result,exit_code,hostname,serial,model,os_build,ip,site,tech,duration_sec,queued_offline,tls_bypassed,key_label,details}]}` (main.py 284–337) |
| 4 | **507** | `GET /api/v1/runs/{run_id}` | — | Transcript drawer | `SELECT *` from `runs` — full row incl. `log` (up to 300,000 chars), `started_utc`, `finished_utc`, `manufacturer`, `os`, `ad_domain`, `mac`, `source_ip`. 404 if missing. (main.py 340–355) |
| 5 | **605** | `GET /api/v1/keys` | — | Keys panel table | `{keys:[{id,prefix,label,created_utc,last_used_utc,use_count,revoked:bool,revoked_utc}]}` (main.py 499–513) |
| 6 | **606** | `POST /api/v1/keys` | body `{label}` | Keys panel — create | **201** `{id,prefix,label,secret,created_utc}` — `secret` shown once, never again (main.py 516–532) |
| 7 | **608** | `POST /api/v1/keys/{id}/revoke` | — | Keys panel — revoke | `{revoked:true,id}`; 404 if absent/already revoked (main.py 535–545) |
| 8 | **609** | `POST /api/v1/keys/{id}/rotate` | — | Keys panel — rotate | **201** `{id,prefix,label,secret,rotated_from}` — revokes old, mints new with same label (main.py 548–567) |
| 9 | **610** | `DELETE /api/v1/keys/{id}` | — | Keys panel — delete (revoked rows only) | `{deleted:true,id}` (main.py 570–577) |

### Endpoints that exist server-side but the dashboard NEVER calls
- `GET /api/v1/health` (main.py 203–211) — unused. Health is derived from `stats.service` instead (`renderHealth`, line 341–347).
- `POST /api/v1/runs` (main.py 214–281) — ingest only; field scripts call it, not the dashboard.
- `GET /runs` supports `result`, `results` (CSV), `hostname` params (main.py 288–293) that the dashboard never sends — it does severity filtering **client-side** in `applyRiver()` (line 480).

### Query-param construction detail
`loadRiver()` (lines 527–536) builds `URLSearchParams` with `days`, `limit`, `offset` always, and conditionally `q`, `script`, `site`. `RANGES` (line 324) maps the 5 range buttons:

```js
{'1h':{hours:1,buckets:60,days:1}, '6h':{hours:6,buckets:72,days:1}, '24h':{hours:24,buckets:96,days:1},
 '7d':{hours:168,buckets:84,days:7}, '30d':{hours:720,buckets:90,days:30}}
```

**Behavioral quirk to preserve or fix on port:** `1h`, `6h`, and `24h` all send `days=1`. The ribbon narrows correctly (it uses `hours`), but the event river always fetches a full day and `applyRiver()` only filters by severity and brush — never by range hours. So selecting "1h" still lists 24 hours of events in the table.

---

## 3. Auth — exactly how it works

**The dashboard performs no authentication whatsoever in JavaScript.** There is no login form, no token in code, no `Authorization` header, no cookie handling, no `credentials:'include'`, no `localStorage`/`sessionStorage` (grep confirms zero hits).

Auth is entirely **Caddy HTTP Basic Auth via the browser's native prompt**, in production `Caddyfile`:

| Caddyfile lines | Matcher | Protection |
|---|---|---|
| 19–28 | `@ingest` — `POST /api/v1/runs` | No basic auth; Bearer token validated by FastAPI instead |
| 30–36 | `@health` — `GET /api/v1/health` | **Open, no auth** |
| 38–47 | `@read` — `GET /api/v1/runs*`, `/api/v1/timeline*` | `basic_auth {$LL_DASH_USER} {$LL_DASH_HASH}` |
| 49–58 | `@stats` — `GET /api/v1/stats*` | `basic_auth` |
| 60–68 | `@admin` — `/api/v1/keys*` (**all methods**) | `basic_auth` |
| 70–72 | any other `/api/*` | `405 method or path not allowed` |
| 74–80 | everything else → `file_server` from `/srv/dashboard` | `basic_auth` — **this is what protects index.html itself** |

Mechanism: the browser requests `index.html`, gets `401 WWW-Authenticate: Basic`, prompts the user, then **caches the credential for that origin+realm and auto-attaches `Authorization: Basic …` to every subsequent same-origin request** — including the `fetch()` calls at lines 333 and 578. The JS is entirely unaware this is happening.

**Critical consequences for the Gatus port:**
1. Same-origin is load-bearing. Move the JS to a different origin and every request 401s, because browser basic-auth caching is per-origin and `fetch` sends no explicit credential.
2. `main.py` has **no `CORSMiddleware`** (verified — no CORS import or setup anywhere in the 578 lines). Cross-origin browser calls from Gatus would be blocked by CORS even with auth solved.
3. The Keys admin surface has **no authorization beyond the same shared dashboard password** — anyone who can see the console can mint, rotate, and delete ingest keys. Note the comment at main.py 486–489: *"Reached only through Caddy behind the dashboard credential; the API is internal-only."* The FastAPI app itself does zero auth on `/keys`. If Gatus proxies to `api:8080` directly, **the Keys endpoints become completely unauthenticated**.
4. `Caddyfile.local` (dev) strips all auth and TLS — `handle /api/*` reverse-proxies with nothing in front (lines 10–17).

---

## 4. Polling / live tailing

- **No `EventSource`. No `WebSocket`. No long-polling.** Grep for `EventSource|WebSocket|XMLHttpRequest` returns zero hits. It is pure `fetch` polling.
- **One `setInterval`, line 559**, created only when LIVE is toggled on:
  ```js
  S.timer=setInterval(()=>{if(!document.getElementById('scrim').classList.contains('on'))refresh();},5000);
  ```
  **5-second interval.** Self-suppressing: skips the refresh while the run drawer is open (so reading a transcript isn't interrupted). Cleared at line 560 on toggle-off.
- Each tick calls `refresh()` (line 537) = **3 HTTP requests**: `/stats` + `/timeline` in `Promise.all` (line 542), then `await loadRiver()` → `/runs` (line 533). So live mode is ~36 requests/minute, and `/runs` pulls up to 250 full rows each time.
- `S.loading` guard at line 538 prevents overlapping refreshes.
- **`setTimeout` line 562** — 280 ms debounce on the search input.
- **`setTimeout` line 602** — 1200 ms cosmetic "Copied" label reset in the Keys panel.
- No `requestAnimationFrame` anywhere.
- Freshness highlight: `S.lastMax` (line 547) tracks the newest `received_utc` seen; `applyRiver()` line 488 adds `.fresh` to rows newer than that, driving `@keyframes fresh` (line 156).
- **Timer leak on port:** `S.timer` is never cleared on page unload or navigation. In a Vue SPA this must be cleared in `onUnmounted` or it polls forever after leaving the view.

---

## 5. UI panels — what each renders, needs, and is fed by

| Panel | Markup | Render fn | Data source | Notes |
|---|---|---|---|---|
| **Health indicator** (top bar) | 258 | `renderHealth` 341–347 | `stats.service` | Dot color by staleness: `<15min` ok, `<180min` warn, else bad. Text: `api v{version} · {runs_last_hour}/hr · last {rel}` |
| **Stat tiles** (6) | 265 | `renderStats` 350–366 | `stats.totals` + `stats.by_script_result` | events, machines, healthy, failed, repaired, tls bypassed. healthy/failed/repaired are **computed client-side** by summing `by_script_result` (lines 353–355) |
| **Site board** | 267–268 | `renderSites` 367–385 | `stats.by_site` | Auto-fill grid, min 148px. Each tile: name, run count, failure count, stacked ok/fail bar. `.dead` marker (red ●) when failures ≥ 50% of runs (line 375). **Click toggles `S.fsite` and refetches** (382–384) |
| **Severity ribbon / timeline** (signature panel) | 272–276 | `renderRibbon` 389–411 | `timeline.series` | Hand-built SVG, `viewBox 0 0 1000 78`, no chart library. Stacked bars per bucket in `SEV_STACK` order (crit on top). Each `<rect>` has a `<title>` tooltip. Legend auto-derived from severities present (393–395). |
| **Ribbon brush** | — | IIFE 415–421 + `updateBrushNote` 422–427 | client-side only | Pointer-events drag (`pointerdown`/`move`/`up` with `setPointerCapture`). Sets `S.brush={t0,t1}` in epoch ms, which filters the river client-side (line 481). Click without drag (<0.4% width) clears. **No refetch** — pure client filter. |
| **Result mix** | 279–282 | `renderCharts` 430–438 | `stats.by_script_result` | Horizontal 100% stacked bar, severities collapsed via `SEV` map, plus a 2-col key with counts |
| **By script** | 283–286 | `renderCharts` 440–445 | `stats.by_script_result` | Sorted horizontal bars; failure overlay drawn with `margin-top:-8px` (line 443). **Label click sets `S.fscript` and refetches** (445). `shortScript()` strips the `LL-` prefix |
| **Repeat offenders** | 287–290 | `renderCharts` 447–451 | `stats.repeat_offenders` (from DB view `v_repeat_offenders`) | Top 8 hosts by `repairs`. **Label click calls `setSearch(hostname)`** → refetch with `q=` (451) |
| **Severity chips** | 296 | `renderChips` 455–459 | client-side `S.sevOn` | 6 toggles: crit/error/warn/notice/ok/info. Pure client-side filter, no refetch (458 → `applyRiver`) |
| **Script/site selects** | 298–299 | `fillSelect` 460–461 | `stats.by_script_result` / `stats.by_site` | Options rebuilt every refresh, preserving current value; option[0] (`all …`) preserved via `outerHTML` |
| **Event river** | 302 | `riverRow` 464–477 + `applyRiver` 478–494 | `runs.runs` | Full `<table>` rebuilt on every render (innerHTML). Columns: Time (ms precision), Site, Host, Script, Result chip, Message. Message = phrase + exit code + duration + first 2 `details` entries + TLS/Q flags. Row background tinted for error/crit (152–153). Sticky `<thead>` (148). `max-height:min(58vh,720px)` (146) |
| **Pagination** | 303–309 | 491–493, 569–570 | `runs.total` | `limit:250`, offset ± 250. Calls `loadRiver()` only (not full `refresh()`) |
| **Search** | 256 | 562–563, 526 | server-side `q` param | 280 ms debounce. Server does `LIKE %q%` across hostname, script, site, tech, result, **and log body** (main.py 307–312). Escape clears |
| **Transcript drawer** | 313 | `openDrawer` 502–521 | `GET /runs/{id}` | Three `.kv` grids (Outcome / Machine / Details) + full log. `logSeverity()` (497–501) regex-classifies each log line into error/warn/ok for per-line tinting — nice touch, purely cosmetic |
| **Keys panel (v0.4)** | 315 | `renderKeys` 583–604 | `GET /keys` | Create row (label input, max 96 chars, Enter submits) + one-time secret reveal box with clipboard copy + table (Label, Prefix, Created, Last used, Uses, Status, Actions). Active rows get **rotate/revoke**; revoked rows get **delete** (line 594). Hint text references `tools\Build-FieldScripts.ps1 -Token <secret>` |

### 🐛 Bug found in the Keys panel — `fmt()` is undefined

**Line 590:**
```js
<td>${k.created_utc?fmt(k.created_utc):''}</td>
```

`fmt` is **never defined anywhere in the file** (grep confirms line 590 is its only occurrence). The available helpers are `fmtTime` (336) and `rel` (337). Since `create_key` in main.py always writes `created_utc`, and `list_keys` always returns it (main.py 509), **every key row throws `ReferenceError: fmt is not defined`**, which propagates out of `renderKeys` → caught by `loadKeys`'s catch (line 605) → the whole Keys panel renders as an error box instead of the table. The Keys panel is effectively broken for any non-empty key list. Fix on port: use `rel(k.created_utc)+' ago'` or a proper date formatter.

---

## 6. CSS analysis — embeddability risk

### Custom properties: yes, extensively
25 custom properties on `:root` (lines 8–30). **All colors are OKLCH** — `--bg: oklch(0.16 0.012 250)` etc., including alpha-slash forms like `oklch(0.70 0.132 236 / 0.14)`. OKLCH is fine in modern browsers (Chrome 111+, Safari 15.4+, Firefox 113+) and Gatus's Vue build won't touch them, but any PostCSS/autoprefixer pipeline that tries to downlevel colors could mangle them.

Additional raw OKLCH literals appear **outside** `:root` (not tokenized): lines 59 (keyframes), 149, 159, 166–171, 173–174, 180, 184, 187, 204, 231, 234. These would need collecting if you re-theme.

### Global selectors that will fight the host app — the leakage list

| Line | Selector | Damage if injected globally |
|---|---|---|
| **9** | `:root{color-scheme:dark}` | Forces dark scrollbars/form controls **document-wide**, including Gatus's own chrome |
| **31** | `*{box-sizing:border-box;margin:0;padding:0}` | **Worst offender.** Universal margin/padding reset nukes every host element's spacing. Tailwind Preflight does the box-sizing part but never zeroes padding on `*` |
| **32** | `html,body{height:100%}` | Forces host document height |
| **33–38** | `body{background;color;font-family;font-size:13px;line-height:1.35;font-variant-numeric:tabular-nums}` | Overrides host body background, text color, and drops base font to **13px** — every Tailwind `rem`-based size in Gatus shifts if the host relies on inherited sizing |
| **42** | `button{font-family:inherit;color:inherit;background:none;border:none;cursor:pointer}` | Unqualified element selector — **strips styling from every button in the host app** |
| **43** | `:focus-visible{outline:2px solid var(--brand);…}` | Unqualified — recolors focus rings app-wide |

### Class-name collision risk (both directions)
The class vocabulary is extremely short and generic. High-collision candidates against Gatus/Tailwind component CSS: `.bar`, `.brand`, `.panel`, `.stat`, `.stats`, `.site`, `.sites`, `.search`, `.health`, `.live`, `.seg`, `.wrap`, `.err`, `.band`, `.charts`, `.mix`, `.chip`, `.chips`, `.river`, `.log`, `.drawer`, `.scrim`, `.grow`, `.num`, `.mono`, `.micro`, `.sec`, `.kv`, `.flag`, `.track`, `.row`, `.n`, `.t`, `.s`, `.v`, `.l`, `.d`, `.k`, `.f`.

Note `.site .bar` (line 99, a 4px progress bar) vs `.bar` (line 46, the 46px sticky top bar) — the file already relies on descendant scoping to disambiguate its *own* names. Host classes will not be so careful.

**Reverse leakage (host → dashboard):** Tailwind Preflight sets `border:0 solid` on `*,::before,::after`. The dashboard always declares explicit `border:1px solid …` so borders survive, but any host rule on `table`, `th`, `td`, `input`, `select`, `button`, or `svg` will bleed into the console. Tailwind's `table{border-collapse:collapse}` is already what the file wants. The real risk is host `font-size`/`line-height` on `body` and a host `.panel`/`.bar`/`.search` component class.

### Layout assumptions hostile to embedding
- **Line 47:** `.bar{position:sticky;top:0;z-index:30}` — sticks to the nearest scroll container. Under a Gatus layout with its own fixed header, this bar will either overlap the host nav or stick to the wrong ancestor.
- **Line 184:** `.scrim{position:fixed;inset:0;z-index:40}` — both drawers cover the **entire viewport including Gatus's navigation**, not just the view. z-index 30/40 will fight any Gatus modal/dropdown layer.
- **Line 73:** `.wrap{max-width:1720px;margin:0 auto}` — assumes it owns the full page width.
- **Line 146:** `.river{max-height:min(58vh,720px)}` — `vh` units assume full-viewport ownership; inside a Gatus content column with its own header this will be too tall.
- **Line 186:** `.drawer{width:min(820px,94vw)}` — same `vw` assumption.

### Verdict on CSS
Every one of these is fixable, but the fix is mechanical and touches all 235 style lines: scope everything under a single wrapper (`.lltel-console { … }` with the tokens moved off `:root` onto that wrapper), delete or scope lines 31–43, and replace `vh`/`vw` with container-relative sizing. Nothing here needs a rewrite — it needs a namespace.

---

## 7. JS globals and top-level document assumptions

### Window pollution
Classic (non-module) script, so **all ~31 top-level `function` declarations become `window` properties**:
`api, showErr, fmtTime, rel, shortScript, renderHealth, renderStats, renderSites, renderRibbon, ribbonSpan, fracForTime, timeForFrac, updateBrushNote, renderCharts, renderChips, fillSelect, riverRow, applyRiver, logSeverity, openDrawer, closeDrawer, resetPage, setSearch, loadRiver, refresh, apiSend, keysErr, renderKeys, loadKeys, keyCreate, keyAction`.

Several are dangerously generic in a shared global namespace: **`api`, `refresh`, `rel`, `closeDrawer`, `setSearch`, `resetPage`**.

The `const`/`let` bindings (`API`, `SEV`, `SEV_STACK`, `SEV_COLOR`, `SEV_LABEL`, `RESULT_PHRASE`, `RANGES`, `sev`, `esc`, `pad2`, `S`, `RIB`, `qDeb`) live in the script-scope declarative record and do **not** attach to `window` — but they still collide with any other classic script declaring the same names.

Positive: no `var`, no IIFE-free mutation of built-ins, no prototype patching.

### Top-level document assumptions
- **58 `document.getElementById()` calls** and **7 `document.querySelectorAll()` calls** (lines 382, 445, 451, 458, 553, 554, 603). All are document-rooted, none scoped to a container element. **41 unique IDs** are assumed globally unique in the document. In a Vue app, IDs like `#q`, `#live`, `#next`, `#prev`, `#stats`, `#sites`, `#range`, `#search`, `#err`, `#log` are plausible collisions.
- **Document-level keyboard handler, lines 573–574** — the biggest embedding hazard:
  ```js
  document.addEventListener('keydown',e=>{
    if(e.key==='Escape'){closeDrawer();document.getElementById('kscrim').classList.remove('on');}
    if(e.key==='/'&&document.activeElement.tagName!=='INPUT'){e.preventDefault();document.getElementById('q').focus();}
  });
  ```
  - The `/` shortcut hijacks the slash key **globally**, and the guard only checks `tagName!=='INPUT'` — so typing `/` in a **`<textarea>` or any `contenteditable`** in the host app gets swallowed and refocuses the dashboard search. It also fires when the dashboard view isn't even visible, because the listener is never removed.
  - The `Escape` handler unconditionally closes both drawers and will swallow Escape from host modals.
  - Never detached → in a Vue SPA this listener survives navigation away from the view.
- **Does NOT touch:** `document.title` (set only via the static `<title>` tag, line 6, never read/written in JS), `location`, `history`, `localStorage`, `sessionStorage`, `document.cookie`. Grep confirms zero hits for all of these. **This is a big point in the port's favor** — no routing or URL-state coupling to unwind, and no persisted state to migrate.
- Only `navigator.*` use is `navigator.clipboard.writeText` at **line 602** (guarded by an `if(navigator.clipboard)` check). Note: `navigator.clipboard` is undefined on insecure origins, and in an iframe requires `allow="clipboard-write"`.
- All handlers use `.onclick =` / `.onchange =` assignment (single-handler, auto-replacing) rather than `addEventListener`, except the four `addEventListener` calls at 562, 563, 572, 573, 601, 614 and the three pointer handlers at 418–420. The `.onclick` style is actually convenient for re-render (no accumulating duplicates), but it means the dashboard silently clobbers any host handler bound the same way to the same element.

### Rendering approach
100% `innerHTML` string templating — no framework, no virtual DOM, no templates. XSS is handled by `esc()` (line 326), which escapes `& < > "` but **not `'`**. All attribute interpolations in the file use double quotes, so this is safe as written, but it is a fragile invariant to carry into a port. Note the log transcript (line 511) and `details` values (line 466) are user/machine-controlled content and are correctly escaped.

---

## 8. External requests — claim verified TRUE

Exhaustive grep of `index.html`:

| Check | Result |
|---|---|
| `https?://` anywhere | **0 matches** |
| `src=` or `href=` attributes | **0 matches** (no `<script src>`, no `<link>`, no `<img>`, no `<a href>`) |
| `url(` in CSS | **0 matches** (no background images, no `@font-face`, no CDN font import) |
| `@import` | 0 matches |
| `fetch` / `XMLHttpRequest` / `EventSource` / `WebSocket` / `importScripts` / dynamic `import()` | 2 `fetch(` only (lines 333, 578), both to the relative `/api/v1` base |
| Fonts | System stacks only — `--mono` (line 28) and `--sans` (line 29) list local families with generic fallbacks |
| Icons/images | None. The only graphics are the hand-built inline SVG ribbon and CSS shapes. The "icons" are literal text characters: `‹`, `›`, `✕`, `●`, `/` |

**Confirmed: fully self-contained, zero third-party requests, zero build dependencies.** This is a genuine single-file app — it will run from `file://` (though API calls would fail there).

---

## 9. Embeddability verdict

### Option A — iframe

**Blocked in production. Straightforward in local dev.**

| Blocker | Severity | Detail |
|---|---|---|
| **`X-Frame-Options: DENY`** | 🔴 **Hard blocker** | `Caddyfile` **line 84**, inside the site-wide `header {}` block (82–87), applied to *all* responses including `index.html`. The browser refuses to frame the page. Requires editing production Caddy config to `SAMEORIGIN` (only helps if Gatus is same-origin) or replacing with `Content-Security-Policy: frame-ancestors <gatus-origin>`. **Per the standing deploy rule, this is a prod config change the user must make — do not push it.** |
| **Basic-auth prompt inside a frame** | 🔴 High | Chrome blocks cross-origin subresource/iframe basic-auth prompts. Even with XFO relaxed, the framed page would 401 silently rather than prompting. Only workable if the user has already authenticated to that origin in a top-level tab. |
| **Cross-origin cookie/credential isolation** | 🟠 Medium | Gatus at a different origin means no shared credential state; third-party-cookie style partitioning applies to the auth cache too. |
| Document-level `keydown` inside iframe | 🟢 Low — *actually a benefit* | Frame boundary naturally sandboxes the `/` and `Escape` handlers. Same for CSS: an iframe is perfect isolation for lines 31–43. |
| `navigator.clipboard` in Keys panel | 🟠 Medium | Needs `allow="clipboard-write"` on the `<iframe>`. |
| Sizing | 🟠 Medium | `vh`/`vw` units (146, 186, 203) resolve against the **iframe** viewport, which is what you want — but the iframe needs an explicit height; there's no `postMessage` resize protocol. |

**Effort if XFO is relaxed: ~1 hour** (a Vue view containing one `<iframe>` + height management). **Effort including the auth/origin story: significant** — you'd want Gatus's Go backend to reverse-proxy `/api/v1/*` and serve `index.html` from the Gatus origin, injecting the basic-auth credential server-side. That reverse proxy is the actual work, and it's needed for Option B too.

### Option B — port into a Vue 3 component

**Very feasible. This is the recommended path.** The app is small, dependency-free, and has no routing/storage coupling. Realistic estimate: **1.5–3 days** for a faithful port.

Concrete blockers, in descending order of effort:

| # | Blocker | Lines | Fix |
|---|---|---|---|
| 1 | **41 hardcoded DOM IDs + 58 `getElementById` + 7 `document.querySelectorAll`** | throughout | The mechanical bulk of the work. Convert to `ref()`/reactive state and `v-for`. Or, as a fast intermediate step, keep the imperative code but scope every query to a component root ref. |
| 2 | **`innerHTML` string templating everywhere** | 364, 372, 394, 410, 437–438, 443, 448–450, 457, 461, 470–476, 483–485, 512–519, 584–598 | Convert to `v-for` + `v-if` templates. Once in Vue templates, `esc()` (326) becomes unnecessary — Vue escapes interpolations by default. Only the SVG ribbon (399–410) genuinely benefits from staying as generated markup. |
| 3 | **Global CSS lines 31–43 + `:root` tokens** | 8–43 | Scope tokens onto a wrapper class, delete/scope `*`, `html,body`, `body`, `button`, `:focus-visible`. Use `<style scoped>` — but note scoped styles do **not** apply to `innerHTML`-generated nodes, which is another reason to finish blocker #2 first. Consider CSS Modules or a `.lltel-` prefix instead. |
| 4 | **Document-level keydown listener** | 573–574 | Move to `onMounted`/`onUnmounted`, and widen the guard beyond `tagName!=='INPUT'` to include `TEXTAREA`, `SELECT`, and `isContentEditable`. |
| 5 | **`setInterval` never cleared** | 559–560 | `onUnmounted(() => clearInterval(S.timer))`. |
| 6 | **Hardcoded `const API='/api/v1'`** | 318 | Make it a prop/config. Gatus will almost certainly need a different prefix (e.g. `/api/v1/telemetry` proxied by Go). |
| 7 | **Cross-origin + auth + CORS** | — | The real architectural work: a Go reverse-proxy handler in Gatus that forwards `/api/v1/*` to the telemetry API and injects the basic-auth header server-side. Removes the browser prompt entirely and makes everything same-origin. **⚠️ Must also re-protect `/api/v1/keys*`** — those endpoints have zero auth in FastAPI and today rely entirely on Caddy. |
| 8 | **`fmt()` undefined** | 590 | Fix while porting (see §5). |
| 9 | Fixed/sticky positioning + `vh`/`vw` | 47, 73, 146, 184, 186, 203 | Rework for a content-column layout; teleport drawers to `<body>` (`<Teleport to="body">`) and align z-index with Gatus's layer scale (currently 30/40). |
| 10 | `1h`/`6h` river range mismatch | 324, 527–536 | Optional correctness fix: send an hours-based bound or filter client-side. |

**Factors that make the port unusually easy:**
- Zero external dependencies — nothing to vendor or license-check.
- No `location`/`history`/`localStorage`/`document.title` coupling — no routing or persistence to reconcile.
- All state already lives in **one plain object `S`** (lines 329–331), which maps almost 1:1 onto a Vue `reactive()` store.
- Only 2 fetch call sites — trivial to swap for a Gatus API client.
- The SVG ribbon is hand-rolled with no chart library, so it ports verbatim into a `<svg>` template with `v-for` over rects.
- Only 301 lines of JS total.

### Recommendation
Port to Vue (Option B), and do the Go reverse-proxy (blocker #7) first — it's a prerequisite for either option and is where the genuine risk lives. Attack the port in this order: reverse proxy → `S` → `reactive()` → template conversion panel by panel (stats → sites → charts → river → ribbon → drawers) → CSS scoping last.

---

## Appendix — key constants to carry over verbatim

```js
// line 319 — result → severity mapping
SEV={HEALTHY:'ok',OK:'ok',REPAIRED:'notice',NOT_JOINED:'warn',PARTIAL:'warn',
     CANCELLED:'warn',FAILED:'error',ERROR:'error',NO_DC:'crit'}

// line 320 — stacking/draw order, crit on top
SEV_STACK=['ok','info','notice','warn','error','crit']

// line 323 — human phrasing in the river message column
RESULT_PHRASE={HEALTHY:'domain trust healthy',OK:'completed clean',
  REPAIRED:'secure channel repaired',NOT_JOINED:'not domain-joined',
  NO_DC:'no domain controller reachable',FAILED:'run failed',
  ERROR:'unhandled error',PARTIAL:'completed with gaps',CANCELLED:'cancelled by tech'}
```

Server-side `RESULTS` set (main.py 41–44) is the authoritative enum: `HEALTHY, REPAIRED, FAILED, NOT_JOINED, NO_DC, OK, PARTIAL, ERROR, CANCELLED`. `SEV` covers all nine, so `sev()`'s `'info'` fallback (line 325) is currently dead code.
