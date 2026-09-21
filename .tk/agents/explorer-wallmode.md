# Agent: explorer-wallmode
Task: map the fullscreen wall-display mechanism so a DETAIL page can be made wall-ready.
(Explorer ran read-only; findings persisted here by the parent.)

## Findings

### fs-active
- Applied only on `#global` in `web/app/src/App.vue:2`, bound to `isFullscreen`.
- Triggered by a header ghost button `App.vue:54-64`; handler `App.vue:203-209` calls
  `globalEl.requestFullscreen()` on the APP CONTAINER (not documentElement) so `<main>`
  stays an inner scroll context.
- `isFullscreen` lives at `store.js:101`, NOT persisted. No keyboard shortcut, no query param.
- Precedent for a non-Home view reacting to it: `TelemetryConsole.vue:91` `v-if="!isFullscreen"`.

### CSS hooks (index.css:322-527)
- `.fs-active header, .fs-active footer { display:none }`  (325)
- `.fs-active { height:100vh; overflow:hidden }`            (348)
- `.fs-active main { height:100vh; overflow-y:auto }`       (352)
- `.fs-active .home-view { height:100vh; overflow:hidden }` (360)  <- scoped to HOME only
- `.fs-active #settings` shrink pill                        (333)
- **There are zero `.fs-active .detail-page` rules.** That is the extension point.
- Established scaling technique: `container-type: size` on the card + `cqw`/`cqh` units
  (`index.css:389-392`), so a layout is identical on 1080p and 4K.
- Caveat: px only for values read back via `getComputedStyle` (cq units resolve empty).

### Class convention
`Home.vue:2` = `dashboard-container home-view`; every detail view = `dashboard-container detail-page`.

### Grid sizing precedent (Home.vue)
- `--fs-cols` computed `Home.vue:282-297`: brute-forces column count scoring
  `empty + aspectDiff*3 + orphan*6` against a 16:9 target.
- `--loc-max-rows` `Home.vue:273-280`: children report row counts up via `@rowcount`,
  `Math.max(4, ...counts)`; drives `clamp()` bar heights so every card agrees.
- ResizeObserver lives in the CHILD (`LocationCard.vue:605-651`), reading `--pill-w` back
  from CSS with `getComputedStyle`.

### store.js
- `applyStatusColors()` `store.js:66-72` sets `--status-up/--status-degraded/--status-down`
  on `:root` from `gatus:colors`. Defaults `#22c55e / #f59e0b / #ef4444` (`store.js:56`).
- `requestRefresh()` `store.js:28-30` dispatches window event `gatus:refresh`.
- Persisted per-screen keys use the `gatus:<kebab>` convention.
- Theme is a COOKIE (`theme`), handled in `Settings.vue:72,132-134`, not localStorage.

### Settings pill
- `components/Settings.vue`, `<div id="settings" class="fixed bottom-4 left-4 z-50">`.
- Mounted PER VIEW. Home/EndpointDetails/SuiteDetails/SiteOverview mount it.
  **JiraDetails does NOT** - so wall-mode Jira has no refresh/theme affordance today.
- Offers refresh interval (10/30/60/120/300/600s, default 300, key `gatus:refresh-interval`)
  and theme toggle. Emits `refreshData` to the parent view.

### Rotation / carousel
None exists anywhere in `web/app/src`. Would be new.

### Router
`web/app/src/router/index.js:33-37` -> path `/jira`, name `Jira`, component `JiraDetails`.
No route `meta` used anywhere in the app; no component reads `route.query`.

## Summary
Chrome hiding is already free on /jira. What is missing for a wall page is:
one-screen clamping for `.detail-page`, cq-based scaling of the Jira grids
(currently fixed px: `.kpi-rail` 411, `.thead`/`.trow` 455/461, `.insight-grid` 421),
a mounted Settings pill, and hiding the interactive toolbar under `isFullscreen`.
