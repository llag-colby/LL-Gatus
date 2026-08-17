# State

**Version:** 1.2.0
**Active task:** none (LL-Telemetry integration + sign-in screen complete, uncommitted)

## Open decision for the user

Eight of twelve sites still resolve as **unmapped**. The WAN-egress layer added
to `SITE_MAP` cannot fire: telemetry is internal-only and field PCs reach it over
the tunnel. Only LAN subnets work, and only four are known. Getting the remaining
eight subnets is a two-minute change and the only real fix.

## What just landed

The LL-Telemetry operations console, reachable from a satellite-dish button in
the Gatus header at `/ll-telemetry`.

Shape: Gatus serves the vendored console HTML and reverse-proxies its API calls
to the telemetry FastAPI. A same-origin iframe isolates the console's OKLCH
design system from Tailwind. See `docs/ll-telemetry.md` for the full guide.

## Load-bearing decisions

- **Telemetry has its own auth gate**, separate from Gatus's `security:` block.
  Gatus's built-in security is all-or-nothing across `/api`; enabling it would
  put `/api/v1/live` behind a login, and EventSource cannot carry a basic
  credential, so unattended wallboards would silently reconnect-loop forever.
  `TelemetryGate` gates only telemetry and leaves every existing screen alone.
- **Fails closed.** No credentials configured means nothing is proxied. The
  upstream FastAPI has no auth of its own, so defaulting open would expose every
  machine transcript and the ingest-key endpoints.
- **`POST /runs` is not proxied.** Ingest stays with the field scripts and their
  `llk_` keys.
- **The console is vendored byte-identical** to upstream; its API base is
  rewritten at serve time, with a startup assertion that panics if the expected
  string is not found exactly once.
- **The local telemetry stack is a separate compose file**, not the main one and
  not `docker-compose.override.yml` (which would auto-load and break the prod
  box, where `../LL-Telemetry` does not exist).

## Review outcome

Three reviewers (bugs, security, conventions) produced 2 blockers, 1 real
exploitable security finding, and ~20 smaller items. All fixed and re-verified.
The security finding was CSRF on the state-changing proxy routes: Basic
credentials have no SameSite, and `POST /keys/{id}/revoke` is a simple request,
so a cross-site form could have killed ingest fleet-wide. Now requires
`Sec-Fetch-Site: same-origin` or a matching `Origin` on any non-GET.

`api/telemetry_test.go` covers the allowlist (including the lowercase-method
bypass the reviewer could not rule out locally), basic-auth parsing, fail-closed
config, and the startup console/CSP assertions. `go vet ./api/...` is clean.

## Verified at runtime

Auth 401 without credentials / 200 with; console 200; `POST /runs` via proxy
403; traversal (`%2e%2e`, literal `../`, encoded slash) all 403; key mint →
revoke → delete lifecycle including the active-key delete refusal; CSP script
hash independently recomputed and matched; run data readable through the
proxy.

**Not verified:** visual rendering in a real browser. Signing in triggers
a credential prompt the browser-automation session could not complete safely.
Needs a human eyeball.

## Open items

- Uncommitted. The working tree also holds substantial unrelated pre-existing
  work (monitoring pause, UniFi views, uptime series, metric history).
- LL-Telemetry upstream fixes sit on branch `fix/keys-panel-and-schema-mount`,
  committed but not pushed.
- `llag-colby/LL-Telemetry` is a PUBLIC GitHub repo containing real internal
  CIDRs, the telemetry hostname/IP, and the security model. Worth making private.
- `api/api.go:161` mounts static files with `Browse: true`; a missing deep link
  yields a directory listing. Pre-existing, not touched.
- `.gitignore:30` claims "repo is public" - LL-Gatus is actually private.
- `.env` defines `PHONES_PUSH_TOKEN` twice.
