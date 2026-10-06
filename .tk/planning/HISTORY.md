# 1.11.0 - Real SMB share checks and a Hyper-V fleet (2026-10-06)

## SMB Shares

Started as four `tcp://host:445` endpoints, which is all Gatus can do natively.
Replaced with `collector/smb_collector.py` after the port check was shown to be
green through every failure mode that actually occurs: share unshared, ACL'd
shut, volume full, DFS referral dead. Three of the four letters also lived on
one server, so those rows were one signal with three labels.

The collector mounts each share as a read-only service account, lists its root
and reads free space, reporting three stages separately (connect+auth / open
share / read root) so a red row names the broken part. Degraded covers low free
space and a slow root listing, and is pushed as a pass carrying its reason.

Dropped S: (`\llfs01\HighSecurityHub`) on request: the service account is
denied Read there by design, so the row could only ever report access-denied.
Config, collector, frontend catalog and env docs all updated; `main.go` pruned
its stored history automatically on the next boot.

Found by testing against a live Samba container, not by reading docs:
  * sessions are cached per server, so shares 2..n never re-authenticated and a
    wrong password came back healthy. Fixed with a per-share `delete_session`.
  * a missing share raises a bare `SMBOSError` with `0xc0000225`, not
    `BadNetworkName`; a denied share `0xc0000022`, not `AccessDenied`; a bad
    password a generic `SMBException` with `STATUS_LOGON_FAILURE`. All three are
    now mapped to sentences an operator can act on.

## Hypervisors

Eleven hosts matching `*-HV##` pulled from the RMM device export. `HV-DC01` was
excluded: the HV there is the Hoover site prefix, not a role suffix, and it is
already a domain controller row.

`collector/hv_collector.py` runs a gzip-wrapped PowerShell inventory over WinRM
and reports OS, host hardware, CPU topology and load, memory, every volume, the
full guest list with state/vCPU/assigned/demand/uptime/heartbeat, Hyper-V host
paths, adapters, and a 24h System-log error count.

The load-bearing decision: liveness is a SEPARATE TCP probe, so a host whose
WinRM is disabled reports DEGRADED rather than down. Calling an alive host an
outage starts the wrong incident. Only a host that answers nothing is down.

Found against real hosts:
  * `pywinrm.run_ps` passes the script via `powershell -EncodedCommand`, which
    inflates UTF-16LE + base64 by ~2.7x against an 8191 character command line.
    The 4.8 KB script arrived as 12,880 characters and ONA-HV1 and ONA-HV2 both
    answered "The command line is too long". The script now travels gzipped
    inside a small bootstrap: 6,664 characters encoded.
  * ONA-HV1 answers on 10.15.101.230 (AD DNS), not the export's 10.15.102.25.
    Hosts therefore carry an ADDRESS LIST, tried in order, and the row reports
    which one answered. The working address is listed first because trying the
    stale one cost 9 seconds of timeouts per sweep.
  * probe budget is ports x addresses x timeout. Four ports at 4s across two
    addresses was 32s for one host, which made a full-outage sweep outrun its
    own 60s interval. Now three ports at 3s with one worker per host: 18s.

## Three bugs in the hypervisor collector, found after first deploy

### Port 135 removed, seven false outages

`PROBE_PORTS` was trimmed from four ports to three as a sweep-time optimisation,
dropping 135 on the reasoning that anything answering the RPC endpoint mapper
also answers 445. That is false on this estate. FL-HV01, HOOV-HV01/02 and
MS-HV01/03/04/06 answer 135 and NOTHING else: measured on all 12 of their
addresses, 445 / 5985 / 3389 all time out. Seven live hosts reported "host did
not answer on any address". 135 is back, second in the order.

### AMSI blocks a compressed script, and says "Access is denied"

The command-line fix from earlier in this release, gzip+base64 behind
`Invoke-Expression`, fits inside the 8191 character limit and is also exactly
the shape of a malicious loader. AMSI on ONA-HV1 refused it deterministically.
It surfaces as a WinRM `Access is denied` (wsmanfault 2147942405) raised from
`run_command`, with no mention of AMSI, so it reads as a host permissions
problem. It had worked for the first two sweeps, which made it look intermittent.

Bisected to prove it rather than guessed: the same gzip wrapper around a tiny
payload runs fine; a same-sized (6668 character) benign padded script runs fine;
`Invoke-Expression` alone runs fine; only the real base64 blob is refused, three
times out of three.

Replaced with three small plain-text scripts (`PS_PARTS`: system, storage,
hyperv), each emitting JSON for the keys it owns, merged in Python. Encoded
sizes 4544 / 2764 / 3420 against a 7600 ceiling.

### Leaked WinRM shells

`Session.run_ps` opens a shell per call and leaks it on any exception, since
`close_shell` never runs. Here the raising path is the normal case, so the leak
was unbounded: ONA-HV1 held 22 orphaned shells against a two hour IdleTimeout.
Now `Protocol` directly, one shell per host per sweep, closed in a `finally`,
with `cleanup_command` per part. Verified: five consecutive collects add zero
shells, against a stable baseline.

With all three fixed, no host reports down, ONA-HV1 returns full inventory
(51% CPU, 38% memory, 19 of 22 guests), and the remaining ten report exactly
what is wrong with them: eight need `Enable-PSRemoting`, two reject the
credentials.

## Drill-ins

`SmbShareDetails.vue` and `HypervisorDetails.vue`, routed by key suffix in
`EndpointDetailRouter` (SMB and HV put the kind in the endpoint NAME, so they
match on suffix where phones/firewall/wireless match on prefix).

Triage sections are gated on `failedStage`. The healthy-state variants were
removed at the user's request: advice rendered next to a green status pill is
noise and pushes the real data down the page.

Detail lands in side-channel stores (`api/smb_shares.go`, `api/hypervisors.go`)
shaped like the UniFi one, so `recordCounts` charts every numeric field with no
extra work.

## Latency baselines replace fixed thresholds

`LocationCard` coloured the Overall row with a hardcoded 100 ms good / 250 ms
warn. That was written when every row was a WAN ping. It made three kinds of row
permanently red while they were perfectly healthy:

  * SMB shares, which mount in ~280 ms (connect, auth, open, list, over a VPN)
  * UniFi Firewall and Wireless rows, which baseline at ~1600 ms
  * hypervisors, where a full WinRM inventory runs to 20 seconds

Replaced with a baseline derived from each ROW's own recent history: median of
its last 20 successful checks, amber at 1.6x that, red at 3x, with absolute
floors of 120 / 250 ms so fast rows keep their old behaviour and a 3x jump on a
27 ms ping does not read as a problem. Under 8 samples there is no baseline and
a pass is simply green.

Two decisions worth keeping:

  * PER ROW, not per card. The Hypervisors card holds a host whose inventory
    takes 24 s next to one refused in 300 ms; a card-wide number would call one
    of them broken whichever way it landed.
  * A RECENT window, not all history. These rows change what they measure: the
    SMB rows were a 30 ms port check in the morning and a 280 ms share mount by
    the afternoon. A baseline over all history stays anchored to the old era and
    paints the new one red, which is exactly the bug being fixed.

The Overall row now takes the healthiest token across its rows, lowest latency
breaking a tie, which preserves "best of the WANs" on a link card.

Verified against live data: every card reads green at its typical value, and
synthetic spikes still escalate (8x on a ping, 1.8x on a share, 2x on an
inventory all go amber; 20x, 4x and 4x go red).

## Deploy note

`docker compose down --remove-orphans` also removed `gatus-lltel-api` and
`gatus-lltel-db`, which belong to `docker-compose.telemetry.yml` and share the
project name. Restored with
`docker compose -f docker-compose.yml -f docker-compose.telemetry.yml up -d`.
Use plain `down` for this project, not `--remove-orphans`.

# History

## 1.8.0 - Login removed, telemetry disabled

The dashboard no longer has a sign-in. No login dialog, no user menu, no
accounts page, no roles, no session cookie. Every control that used to require
operator - pause monitoring, force ping, force sweep, phone exclusions, phone
thresholds - is available to anyone who can reach the port.

**Server.** `api/api.go` lost the `/v1/auth/*` routes, the `/v1/users` group,
the six per-route role gates and the protected/unprotected router split; the two
groups collapsed into one `apiV1Router`, and `cfg.Security`'s middleware is no
longer installed. `main.go` no longer opens `/data/auth.db` or calls
`EnsureAdmin`, so the database is not created and no bootstrap admin password is
generated. The `auth` and `security` packages stay in the tree, unwired, and
their tests still pass.

**LL-Telemetry disabled.** The five telemetry route registrations, the
`/ll-telemetry` SPA deep link, the router entry, the satellite-dish header
button and the view import are gone; `/api/v1/telemetry/*` answers 404.
`api/telemetry.go` and `api/assets/telemetry-console.html` stay put on purpose -
that file's `init()` prepares the embedded console and must keep compiling.
`docker-compose.telemetry.yml` and `docs/ll-telemetry.md` also stay, the doc now
carrying a DISABLED banner. The two local containers were stopped.

**Client.** `can()` is now a constant `true` and the module-load `refreshAuth()`
is gone. The role gates, disabled states and every "Sign in to ..." string came
out of `MonitorToggle`, `CardSettingsMenu`, `EndpointDetails` and `PhoneDetails`.
`SettingsView` was rewritten: the session readout, password form, clearance
matrix and user roster are gone, leaving the paused-checks list and a note that
preferences are per-browser. `LoginDialog.vue`, `UserMenu.vue` and
`TelemetryConsole.vue` remain on disk, unreferenced.

**Unchanged: the collectors.** Their per-endpoint `Bearer` push token was never
part of the login and is still the only credential the server checks.

**What this accepts.** Six endpoints are now unauthenticated writes: monitoring
pause, phone exclusions, phone thresholds, unifi settings, sweep request and
force-check. None could move client-side - the collectors and the watchdog read
them server-side. Force-check is an outbound-probe amplifier guarded only by its
3s per-key cooldown, and `GET /v1/phones/sweep-pending` was already an open
mutating GET.

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
- An auth bypass caught before review: the session exemption used
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
`docs/ll-telemetry.md`.
**Changed:** `api/api.go` (route group + SPA deep link), `App.vue` (button),
`router/index.js` (route), `.env` / `.env.example` (`TELEMETRY_*`, `LL_*`),
rebuilt `web/static/`.

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
- The delete guard decoded `GET /keys` as a bare array when it returns
  `{"keys":[...]}`, so every key delete was refused, even after revoke. Caught by
  runtime testing.
- The first CSP hashed the inline `<style>` block, but a hash does not authorise
  inline `style` attributes (governed by `style-src-attr`), and the console
  renders 12 of them. Relaxed `style-src` to `'unsafe-inline'` while keeping the
  script hash strict.

### Claims checked and found false

- A report that `web/static/` was empty and the build broken. It was intact; the
  observation caught a Vue build mid-flight, which clears `outputDir` before
  repopulating it.
- A design choice justified on `.gitignore:30` saying "repo is public".
  LL-Gatus is private; LL-Telemetry is the public one.

## 2026-09-21 - Service desk wall board (1.9.0 -> 1.10.0)
/tk:build heavy + /tk:design heavy. Rewrote web/app/src/views/JiraDetails.vue and
restyled JiraKanban.vue + JiraTicketPanel.vue.

- Removed the warm sepia palette (#e0a458 gold, #b08968/#a3907a/#9c6f5e/#c2a878
  browns, #ef6b53 coral, #5aa06b sage) from all three Jira files. Zero coffee
  hexes remain. The same literals still exist in FirewallDetails, WirelessDetails,
  SettingsView, MonitorToggle and CardSettingsMenu - deliberately out of scope.
- Added --j-* tokens to index.css on :root (on :root, not the page, because the
  ticket drawer teleports to body). crit/warn/ok derive from --status-down/
  -degraded/-up, so the board now re-themes with the palette picker.
- /jira is now three tabs: Overview (wall board), Queue (dense sortable table),
  Kanban. The old list/board toggle inside Overview is gone - it duplicated Kanban.
- Overview carries all four signals the user asked for on one screen: KPI rail
  with a 14-day diverging flow chart, an SLA horizon instrument, and three live
  columns (At risk / Unassigned / Just in).
- index.css gained the first `.fs-active .detail-page` rules in the codebase.
  container-type:size + cqw/cqh, same technique as the location cards.
- Newly used backend fields: snapshot.status, .account, .baseUrl, DayPoint.date,
  issue.slaName, and the slaBreached === -1 "not measured" sentinel.
