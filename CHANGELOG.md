# LL-Gatus Changelog

Customized fork of [TwiN/gatus](https://github.com/TwiN/gatus) for Long Lewis.

> **How to tell if a deploy actually updated:** the running build's git SHA is
> shown in the footer ("· build `<sha>`") and at `GET /api/v1/version`.
> `./update.sh` prints it and warns if it doesn't match the latest commit.
> Legend: `+` added · `-` removed/changed · `~` fixed

## Unreleased
+ `update.sh` — one-command Ubuntu updater: force-syncs to origin/main (kills
  stale code), rebuilds, restarts, and verifies the running build.
+ Baked build version (git SHA) exposed at `/api/v1/version` and in the footer.
+ Faster Docker builds via BuildKit cache mounts (incremental rebuilds).
+ This changelog.

## Jira service-desk monitor (`/jira`)
See [docs/jira-monitor.md](docs/jira-monitor.md) for the full guide.
+ New `/jira` page: a live Jira Cloud service-desk dashboard reached from a Jira
  button in the header. Tabs per project (LLSM = IT Service Management, LLIP =
  IT Projects).
+ Backend `jira` poller (Go): authenticates with HTTP Basic (`JIRA_EMAIL` +
  `JIRA_API_TOKEN`), polls every 30s, caches a snapshot, and serves
  `/api/v1/jira/metrics`. Auth is verified via `/rest/api/3/myself` (bad creds
  otherwise return empty *anonymous* results, not 401).
+ Live updates via SSE (`/api/v1/jira/live`) — new tickets, status/comment
  changes and SLA movement reach every open screen the moment a poll lands.
+ Per-ticket **SLA countdowns** that tick client-side and escalate amber (<1h) /
  red (<15m) / "overdue"; tickets sort by urgency; new tickets flash on arrival.
+ **Drill-down** slide-over (`/api/v1/jira/issue/:key`): summary, assignee,
  reporter, labels, every SLA metric, full rendered description, recent comments;
  live-refreshes every 15s while open.
+ KPI rail, priority/type/status breakdowns, 14-day created-vs-resolved sparkline,
  sortable list columns, and a **Board** (Kanban-by-status) view.
+ `JIRA_DEMO=1` serves badged synthetic data for previewing without live creds.
+ `env-merge.sh` — append-only env merger (target is master) for adding the Jira
  keys to a box's existing `.env` without disturbing its values.
~ Header Jira icon rendered as a background-image span, not an `<img>`, so the
  wordmark `header img { filter: brightness(0) invert(1) }` custom CSS can't
  repaint it into a white box.

## Live updates
+ Live, 100%-synced updates via SSE (`/api/v1/live`): one server-side
  broadcaster pushes the same snapshot to every browser when data changes.
- Removed the independent 5-minute per-browser polling (caused drift/staleness).

## Dashboard & UI
+ Consolidated per-location cards (one box per site) with fixed rows:
  WAN 1, WAN 2, Phones, and an Overall Health row colored by latency.
+ Search / filter / sort moved into a full-width header.
+ Long Lewis logo; theme-aware favicon (black on light, white on dark).
+ Fullscreen wall-display mode: balanced grid that fills the screen (no orphan
  box), no scrollbars, drilled-in detail pages scroll.
+ Classy hover tooltips; logo click returns to the dashboard.
+ All timestamps shown in US Central time, 12-hour format.
~ Reduced status bars per row so the board reads calmly.
- Removed the status-badge dots and the redundant "last ping" timestamp.
