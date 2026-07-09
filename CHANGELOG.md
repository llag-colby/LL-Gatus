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
