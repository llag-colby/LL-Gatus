# State

**Version:** 1.8.0
**Active task:** none (login removal + telemetry disable complete)

## What just landed

The dashboard has no sign-in. Accounts, roles, sessions and the login UI are
unwired, and the LL-Telemetry console is unrouted. Both bodies of code are still
in the tree, dormant, so either can be switched back on without rewriting it.

See `.tk/planning/HISTORY.md` 1.8.0 for the file-by-file account.

## Load-bearing decisions

- **Nothing was deleted.** `auth/`, `security/`, `api/telemetry.go`,
  `api/assets/telemetry-console.html`, `LoginDialog.vue`, `UserMenu.vue`,
  `TelemetryConsole.vue`, `docker-compose.telemetry.yml` and
  `docs/ll-telemetry.md` all remain. They are unreferenced, not removed.
- **`api/telemetry.go` must stay while its asset does.** Its `init()` rewrites
  the embedded console's API base and panics if the expected string is not found
  exactly once, and the `//go:embed` is compile-time. Deleting one without the
  other breaks the build or the boot.
- **`can()` returns a constant `true`.** It stays exported as the one hook a
  future gate would need, and because returning true is what makes the app
  behave exactly like a deployment that never had accounts.
- **The collector push token is untouched.** It is machine auth checked inside
  the handlers, was never part of the login, and is now the only credential the
  server checks.
- **Six writes are open on purpose.** Monitoring pause, phone exclusions, phone
  thresholds, unifi settings, sweep request, force-check. None can move to the
  browser: the collectors and the watchdog read that state server-side.
- **`cfg.Security` is still parsed.** The config field and its validation stay
  so the upstream config contract and its tests are unchanged; only
  `ApplySecurityMiddleware` is no longer installed. README says so in place.

## Open items

- `/data/auth.db` (plus `-shm`/`-wal`) is now an orphan on this box and on prod.
  Nothing creates, reads or deletes it. Safe to remove by hand whenever.
- `.env` still carries `TELEMETRY_*` and `LL_*` keys. They are inert now that the
  gate is unrouted. `.env.bak.20260915-132627` and `.env.bak.cullman.144439`
  contain a plaintext telemetry password and are worth scrubbing.
- `config/ui`'s `LoginSubtitle` field survives for upstream parity. It is no
  longer injected into `window.config` and nothing renders it.
- `api/config.go` still returns `oidc` and `authenticated`. Both are vestigial
  with no middleware installed; kept at the upstream shape for any external
  consumer, and commented as such.
- The UI was verified by API probe and by grepping the served bundle, not in a
  browser: Chrome could not reach the local port from the automation extension.
