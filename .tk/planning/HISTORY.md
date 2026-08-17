# History

## 1.2.0 — Telemetry sign-in screen, console fixes, site mapping

Replaced the browser's native basic-auth dialog with a proper sign-in screen,
fixed the ribbon brush and the console header, made the console full-bleed, and
expanded `SITE_MAP`.

**Sign-in.** `POST/DELETE /api/v1/telemetry/session` mints and drops an
in-memory session behind an `HttpOnly`, `SameSite=Strict` cookie scoped to
`/api/v1/telemetry`. The gate no longer sends `WWW-Authenticate`, so no native
dialog can appear inside the console's iframe; Basic auth is retained for
scripts. The login panel is built as an event row from the console's own river:
live clock, source label, and a severity chip carrying the auth state, with a
full-width severity wash on failure per DESIGN.md.

**Brush.** It was rebuilding up to 250 river rows on every `pointermove`, and had
no `pointercancel` or window-level `pointerup`, so releasing outside the ribbon
left the drag stuck on. Now rAF-throttled, committed once on release, cancellable
with Escape, anchored on absolute time so live refresh cannot remap it mid-drag,
and `touch-action:none` so it works on touch.

**Header.** Was `--panel`, one step lighter than its own content, with every
control outlined in the strongest border token and five type sizes in 46px. Moved
to `--bg` so the chrome recedes, hairline borders, controls raised to `--panel`.

### Review outcome

Four reviewers: 5 blockers, 1 HIGH security finding, all fixed.

- **HIGH:** the new login throttle covered only the form. Basic auth is reachable
  on every GET and short-circuits the same-origin check, so the limit guarded the
  one door an attacker never has to use, and every wrong password still paid a
  full bcrypt. Both paths now share the counter; credential-less requests are not
  counted, because the SPA probes `/health` on every page load.
- **CSS collision:** the sticky header and each site's 4px sparkbar both used
  `class="bar"`. `min-height:50px` is not overridden by `height:4px`, so every
  sparkbar would have rendered 50px tall. Renamed to `.sbar`.
- `renderRibbon(RIB)` threw while `RIB` was still null (during load, and forever
  if the API was down), which also stopped the river re-filtering on release.
- `telemetryFailures` was never swept; the lockout window did not slide.
- An auth bypass I caught before the reviewers: the session exemption used
  `strings.HasSuffix`, so `/api/v1/telemetry/runs/x/telemetry/session` skipped
  the gate. Only the allowlist stopped it reaching the upstream. Now exact.

### Corrected after review

`SITE_MAP` gained all 12 sites' WAN egress IPs from `config.yaml` (verified
digit-by-digit, zero transcription errors) — but **that layer is inert**: the
telemetry service is internal-only and field PCs reach it over the tunnel, so a
public egress IP never appears in either input. The eight sites without a LAN
subnet still resolve as unmapped. The comment and docs now say so plainly rather
than implying the problem is solved.

The `Bramlett / Decatur` → `Decatur` rename was reverted: 61 existing rows carry
the old name and `/api/v1/stats` does `GROUP BY site`, so it would have split
Decatur into two partial tiles.

## 1.1.0 — LL-Telemetry console integration

Added `/ll-telemetry`: the LL-Telemetry operations console, served by Gatus and
reached from a satellite-dish button in the header.

**New:** `api/telemetry.go`, `api/assets/telemetry-console.html`,
`web/app/src/views/TelemetryConsole.vue`, `docker-compose.telemetry.yml`,
`collector/seed_telemetry.py`, `docs/ll-telemetry.md`.
**Changed:** `api/api.go` (route group + SPA deep link), `App.vue` (button),
`router/index.js` (route), `.env` / `.env.example` (`TELEMETRY_*`, `LL_*`),
rebuilt `web/static/`.

Eight explorer agents mapped the two codebases, five architects designed it, and
three reviewers checked the result.

### Decisions that changed the design mid-flight

- The user's initial choice was to gate telemetry behind Gatus's own `security:`
  block. Exploration found that is all-or-nothing across `/api` and would break
  unattended wallboards via the `/api/v1/live` EventSource stream. Replaced with
  a telemetry-only gate after putting the trade-off back to the user.
- The console was going to live in `web/app/public/`. That output lands in
  `web/static/`, which fiberfs serves *before* the security middleware, so it
  would have been world-readable. Moved to `api/assets/` with its own embed.
- A full Vue port of the console was rejected: 619 lines with hand-drawn SVG,
  brush interaction and its own OKLCH theme, and it would have meant maintaining
  two consoles.

### Bugs found and fixed

- **LL-Telemetry (upstream, branch `fix/keys-panel-and-schema-mount`):**
  `dashboard/index.html` called an undefined `fmt()`, breaking the entire Keys
  panel; and both compose files mounted only `01-schema.sql`, so `02-apikeys.sql`
  never ran — which 500s `GET /api/v1/runs` itself, not just the key endpoints.
- **Own bug, caught by runtime testing:** the delete guard decoded `GET /keys`
  as a bare array when it returns `{"keys":[...]}`, so every key delete was
  refused, even after revoke.
- **Own bug, caught before shipping:** the first CSP hashed the inline `<style>`
  block, but a hash does not authorise inline `style` attributes (governed by
  `style-src-attr`), and the console renders 12 of them. Relaxed `style-src` to
  `'unsafe-inline'` while keeping the script hash strict.

### Claims checked and found false

- A reviewing agent reported `web/static/` was empty and the build broken. It
  was intact; the agent had observed a Vue build mid-flight, which clears
  `outputDir` before repopulating it.
- An agent justified a design choice on `.gitignore:30` saying "repo is public".
  LL-Gatus is private; LL-Telemetry is the public one.
