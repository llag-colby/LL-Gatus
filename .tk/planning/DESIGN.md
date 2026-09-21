# Service desk wall board - design brief

Command: /tk:build heavy + /tk:design heavy
Brief: "redo the jira page. remove the coffee colored shit. make it more of a
monitoring page. i need to go to that page and display it on a monitor."

## Decisions taken with the user
- Use mode: wall TV that is still clickable. Big type, glanceable, but list
  rows and the ticket drawer stay live.
- Lead signals: ALL FOUR - SLA breach pressure, unassigned, open backlog and
  today's throughput, and newest tickets as they land. All four must be on the
  one screen, not behind a toggle.
- Kanban stays as its own tab.

## Purpose
A NOC board for a service desk. Read from ten feet away it must answer:
are we breaching, is anything unowned, are we keeping up, what just landed.
Walk up to it and it becomes a working queue.

## Tone: industrial / operations instrument
Airport FIDS board and a network operations panel, not a product dashboard.
- Chrome is achromatic. Colour is reserved for signal, so any colour on the
  page means something is happening.
- Zones divided by hairlines rather than a scatter of floating rounded cards.
  The page reads as one instrument, not six widgets.
- Every machine-generated value (keys, clocks, counts) is monospace with
  tabular numerals. Every human value (summaries, names) is sans.
- Character comes from scale and tracking contrast: huge tight numerals against
  tiny wide-tracked uppercase labels.

### Typography note (deliberate tradeoff)
No webfont. This page is served from an embedded Go binary onto an office wall
and must render correctly with no internet; the build container has no vendored
font assets. The distinction is carried by the type SYSTEM (weight, tracking and
scale contrast, mono for all machine values) rather than by a typeface. If a
custom face is wanted later, vendoring IBM Plex Mono as woff2 is the move.

## Colour: kill the coffee, adopt the app's own signal palette
The old page invented a warm sepia set (#e0a458 gold, #b08968 / #a3907a /
#9c6f5e / #c2a878 browns, #ef6b53 coral, #5aa06b sage) that existed nowhere
else and re-themed with nothing.

New tokens live in index.css on :root so the teleported ticket drawer can read
them too, and they DERIVE from the dashboard's user-themeable status colours:

    --j-crit -> var(--status-down)       breached
    --j-warn -> var(--status-degraded)   breaching soon
    --j-ok   -> var(--status-up)         resolved / healthy
    --j-info  cool cyan                  new / inbound / live  (its own idea)
    --j-idle  cool slate                 no signal

Consequence: the service desk board now re-themes with the palette picker like
every other page, and the type-mix ramp is a cool analytic ramp (cyan, indigo,
teal, violet, slate) instead of the coffee ramp.

## Layout - four bands, one screen
1. COMMAND RAIL - identity, project switcher, health pill, live feed dot, clock.
   Collapses to a status strip in fullscreen (no back link, no buttons).
2. KPI RAIL - Open / SLA breached / Unassigned / Avg resolve, plus a 14-day
   FLOW cell: diverging bars, created above the axis, resolved below.
3. SLA HORIZON - the signature instrument. One time axis from BREACHED to
   LATER, six bands, every open SLA ticket plotted as a tick sized by priority.
   The swarm crowding the red edge is the thing you see from across the room.
4. THREE LIVE COLUMNS - AT RISK (live countdowns) / UNASSIGNED / JUST IN.
   Headers carry the true count so a clipped body never lies.

## Tabs
Overview (the wall) | Queue (dense sortable table, search, breakdowns) | Kanban.
The old list/board toggle inside Overview is gone - it duplicated Kanban.
Editorial split: Overview carries signals, Queue carries analysis and work.

## The one memorable detail
The SLA horizon. Nothing else on this dashboard plots a population against a
deadline axis, and it turns "3 breached" into a picture of pressure.

## Backend fields newly put to work
snapshot.status (health pill), snapshot.account + baseUrl (provenance and deep
links), DayPoint.date (a real axis on the flow chart), issue.slaName and
issue.slaFriendly (which clock is ticking, in Jira's own business-hours
phrasing). project.slaBreached === -1 is handled as "not measured" rather than
being rendered as a number.

## Wall mode
.fs-active .jira-view clamps to 100vh with container-type:size, and all inner
sizing is in cqw/cqh, so the board is identical on 1080p and 4K. This is the
first .fs-active rule for a detail page; index.css previously scoped one-screen
clamping to .home-view only.
