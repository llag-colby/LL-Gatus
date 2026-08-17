# Security review — LL-Telemetry integration

Scope: `api/telemetry.go` (new), the `telemetryRouter` group + `/ll-telemetry` SPA line in
`api/api.go`, `web/app/src/views/TelemetryConsole.vue`, `docker-compose.telemetry.yml`,
`api/assets/telemetry-console.html` (vendored).

Threat model taken as given: Gatus has no auth of its own (`cfg.Security == nil`) and is
published on `:8080`; the telemetry FastAPI has no auth on any route and is never published;
`TelemetryGate` is the only boundary.

Verified against the real upstream at `../LL-Telemetry/telemetry/api/main.py` and fiber
`v2.52.13` semantics.

**Verdict: the boundary holds. One real exploitable finding (CSRF), three hardening items.
Eight of the nine questions asked are clean.**

---

## Findings

### 1. [MEDIUM] CSRF: any web page can revoke or rotate an ingest key

`api/telemetry.go:192-212` (gate), `api/telemetry.go:348` (proxy), `api/api.go:205-207`.

The gate authenticates with HTTP Basic and nothing else. Basic credentials are cached by the
browser per origin+realm and attached automatically to *any* request to that origin,
including cross-site-initiated ones — `SameSite` does not exist for HTTP auth. The comment at
`telemetry.go:206-207` names this behaviour as a design goal ("lets the browser cache the
credential for the whole origin"); it is also what makes the deputy confusable.

There is no Origin check, no `Sec-Fetch-*` check, no CSRF token, and no
custom-header requirement anywhere in the request path.

Concrete exploit — an operator has the console open (or has authenticated to it this browser
session) and visits any other page:

```html
<form id="f" method="POST" target="sink"
      action="http://gatus:8080/api/v1/telemetry/keys/3/revoke"></form>
<iframe name="sink" hidden></iframe>
<script>f.submit()</script>
```

- The form POST is a "simple request": no preflight, and the browser attaches the cached
  Basic credential because `/api/v1/telemetry/keys/3/revoke` sits under the authenticated
  path `/api/v1/telemetry/`.
- `TelemetryGate` sees a valid credential and calls `c.Next()`.
- `POST /keys/*/revoke` is on the allowlist (`telemetry.go:287`).
- `TelemetryProxy` sends a bodyless POST upstream; `revoke_key()` needs no body and
  `UPDATE api_keys SET revoked = 1` lands.
- **Ingest stops for every field script stamped with that key.** Key IDs are small
  sequential integers, so `for (i=1;i<50;i++)` revokes the whole table.

`POST /keys/{id}/rotate` is reachable the same way and is worse: it revokes the old key *and*
mints a replacement whose plaintext secret is returned in a response nobody reads — the key
is unrecoverable. `POST /keys` (mint) is also reachable; the proxy itself sets
`Content-Type: application/json` on the outbound request (`telemetry.go:387`), so the
attacker's content type is irrelevant upstream.

Not reachable this way: `DELETE /keys/{id}` (a non-simple method forces a preflight, which
fails with no CORS middleware), and no response body can be read cross-origin, so this is
destructive-only, not an exfiltration path.

**Fix** — in `TelemetryGate`, after authenticating, reject browser-initiated cross-site
writes:

```go
if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
    // Browsers always send Sec-Fetch-Site; a cross-site form POST sends "cross-site".
    // Non-browser clients (curl, scripts) send neither header and are unaffected.
    if site := c.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
        return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "cross-site request refused"})
    }
    if origin := c.Get(fiber.HeaderOrigin); origin != "" && origin != c.Protocol()+"://"+c.Hostname() {
        return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "cross-origin request refused"})
    }
}
```

The console's own calls are same-origin (it is served from Gatus and framed same-origin by
`TelemetryConsole.vue:63`), so nothing in the UI breaks. A belt-and-braces alternative is to
require a custom header (`X-LL-Console: 1`) which forces a preflight, but that needs a matching
edit in `apiSend()` at `telemetry-console.html:581` and diverges the vendored file.

---

### 2. [LOW] `authenticate()` early return is a genuine username oracle

`api/telemetry.go:177-185`.

```go
if subtle.ConstantTimeCompare([]byte(user), []byte(c.uiUser)) != 1 {
    return false          // returns in microseconds
}
if len(c.uiBcryptHash) > 0 {
    return bcrypt.CompareHashAndPassword(...) == nil   // ~60-100ms at cost 10
}
```

Yes, it is meaningful, and more so than the usual textbook case. The gap is not a
cache-timing sliver — it is the entire bcrypt work factor, ~5 orders of magnitude, trivially
measurable over a LAN with a single request. `subtle.ConstantTimeCompare` also returns 0
immediately when lengths differ, so the username *length* leaks too, which makes the search
cheap: guess length first, then confirm the exact string.

With bcrypt configured, the deployment has exactly one credential pair protecting machine
transcripts and key management. Learning the username for free halves it. (With the plaintext
fallback both branches are fast and the oracle disappears — the safer-looking config is the
one that leaks.)

Secondary effect: because bcrypt only runs on a username hit, an attacker who has learned the
username can also pin Gatus's CPU with concurrent unauthenticated requests.

**Fix** — always pay the same cost, and combine with `&`:

```go
func (c telemetryConfig) authenticate(user, password string) bool {
    userOK := subtle.ConstantTimeCompare([]byte(user), []byte(c.uiUser)) == 1
    var passOK bool
    if len(c.uiBcryptHash) > 0 {
        passOK = bcrypt.CompareHashAndPassword(c.uiBcryptHash, []byte(password)) == nil
    } else {
        passOK = subtle.ConstantTimeCompare([]byte(password), []byte(c.uiPassword)) == 1
    }
    return userOK && passOK
}
```

(`userOK && passOK` after both have been evaluated is fine — the branch is on the already
computed booleans, not on the comparison.) Hashing the username and password to fixed-length
digests before comparing would also close the length leak.

---

### 3. [LOW] No throttling on the only authentication boundary

`api/telemetry.go:203-209`.

The gate has no rate limit, no backoff, and no lockout. On the bcrypt path an attacker gets
roughly 10-50 guesses/sec/connection; on the `TELEMETRY_UI_PASSWORD` plaintext path
(`.env.example` offers it as a supported option) there is no work factor at all and the rate
is bounded only by the network. Gatus is published on `:8080` and the password is
operator-chosen.

**Fix** — put `github.com/gofiber/fiber/v2/middleware/limiter` in front of `TelemetryGate` on
the group (e.g. 10 requests/minute/IP for failed auth), or add a small per-IP failure counter
with exponential backoff inside the gate. Also worth dropping `TELEMETRY_UI_PASSWORD` from
`.env.example` and requiring the bcrypt form.

---

### 4. [LOW / latent] `esc()` does not escape `'`

`api/assets/telemetry-console.html:326`.

```js
const esc=s=>String(s??'').replace(/[&<>"]/g, ...)
```

Single quotes pass through. I checked every interpolation sink in the file — `data-site`
(:379), `data-id` (:473, :591), `data-script` (:446), `data-host` (:452), `<option value>`
(:464), `data-sev` (:514) — and all of them are double-quoted, so this is **not exploitable
today**. It is a landmine: one future single-quoted attribute in the vendored console turns
every ingested `hostname`/`site`/`script`/`tech`/`log` line into an injection point, and the
file is explicitly maintained as a byte-for-byte upstream vendor, so the review that would
catch it is a `diff`, not a read.

**Fix** — upstream `esc()` should include `'` → `&#39;`. Given the vendoring policy, push it
to LL-Telemetry rather than forking here; alternatively assert on it at startup the same way
`init()` already asserts on the API-base string (`telemetry.go:78-84`).

---

### 5. [INFO] The DELETE guard doubles the worst-case latency past the write timeout

`api/telemetry.go:360-364` + `:431-442`.

`telemetryRequireRevoked` issues its own upstream `GET /keys` with the same 12s client
timeout, then `TelemetryProxy` issues the DELETE. The comment at `telemetry.go:70-71` reasons
that 12s is "comfortably under the 15s server WriteTimeout" — true for one call, but a DELETE
can now take up to 24s and be severed mid-response. Reliability, not security. Also note
`telemetryRequireRevoked` uses `http.NewRequest` rather than
`http.NewRequestWithContext(c.UserContext(), ...)`, so it does not cancel when the client
disconnects.

---

### 6. [INFO] Upstream URL is logged verbatim

`api/telemetry.go:164`. `logr.Infof("... at %s for user %q", cfg.upstream, cfg.uiUser)`. If an
operator ever sets `TELEMETRY_UPSTREAM_URL=http://user:pass@host`, the userinfo lands in the
log. Same for the `url.Parse` failure path at `:371`, where `err.Error()` is a `*url.Error`
whose `.URL` is the un-redacted raw string. `TELEMETRY_UPSTREAM_TOKEN` is never affected — see
"verified clean" below. Redact with `parsed.Redacted()` if you care.

---

## Verified clean (asked about, refuted)

**1. Gate bypass — no.** Checked every path into `/api/v1/telemetry/*`:

- `protectedAPIRouter.Group("/v1/telemetry", TelemetryGate)` registers `TelemetryGate` as a
  fiber `Use` route at `/api/v1/telemetry`, *before* the two routes in the group. Any path
  that matches `/api/v1/telemetry/*` necessarily matches the `Use` prefix — the wildcard
  route's own prefix is a strict superset-free subset of it. There is no gap.
- Route ordering in `createRouter` is fine. `app.Use("/", fiberfs.New(...))` at `api.go:167`
  is registered *earlier*, but fiber's filesystem middleware calls `c.Next()` on
  `fs.ErrNotExist`, and nothing named `api/v1/telemetry/...` exists in the embedded
  `web/static` FS (confirmed: `telemetry-console.html` exists only under `api/assets/`, is
  `go:embed`-ed into package `api`, and is served solely by `TelemetryConsole`). This is the
  same mechanism every pre-existing protected route already relies on.
- `/console` is registered before `/*`, so it is not swallowed.
- `app.Get("/ll-telemetry", SinglePageApplication(...))` (`api.go:147`) serves the SPA shell
  only — no telemetry data crosses it. The Vue view fetches through the gated proxy.
- Case-insensitive routing (fiber default) means `/API/V1/TELEMETRY/keys` still matches the
  gate; it then fails the allowlist's lowercase literals and 403s. Fail-closed, no bypass.
- `//api/v1/telemetry/keys` matches neither the gate nor the wildcard (fiber uses the
  *original* path, not a normalised one) and falls through to static/404.
- `.All("/*")` accepts every method, but `telemetryUpstreamPath` compares the method against
  `fiber.MethodGet/Post/Delete` exactly, so HEAD/PUT/PATCH/OPTIONS/lowercase-anything 403.
  This also means the method handed to `http.NewRequestWithContext` is always one of three
  constants — no method injection into the upstream request line.

**2. Fail-closed — yes.** `configured()` (`:173`) requires `upstream != "" && uiUser != "" &&
(bcrypt || password)`. With `TELEMETRY_UI_USER` unset, `TelemetryGate` serves the static
not-configured HTML for `.../telemetry/console` and `503` for everything else, and
`TelemetryProxy` is never entered. A malformed `TELEMETRY_UI_PASSWORD_BCRYPT` (truncated,
plaintext pasted in) makes `bcrypt.CompareHashAndPassword` always error — a lockout, not an
opening. `TELEMETRY_UPSTREAM_URL=/` trims to `""` and also fails closed. `docker-compose.yml`
does pass `.env` into the gatus container (`env_file`), so the vars actually arrive.

**4. Allowlist — could not defeat it.** `telemetryUpstreamPath` (`:298`):

- `%` rejected before anything else, and fiber's `c.Params("*")` yields the *raw, undecoded*
  path (`UnescapePath` defaults false), so there is no decode-ordering window — encoded
  slashes, `%2e%2e`, `%00` all die at the first check.
- `rest != path.Clean(rest)` kills `..`, `.`, `//`, empty segments and trailing slashes.
  Correctly uses `path`, not `path/filepath`, so it is slash-only and Windows-independent.
- The charset loop ranges over *runes* and allows only `[A-Za-z0-9-_./]`. Backslash, `;`,
  `?`, `#`, NUL, control bytes, all Unicode (including full-width and homoglyph tricks) and
  invalid UTF-8 (which yields `RuneError`) hit `default:` and are rejected.
- Segment count is compared exactly, so no route can be extended or truncated.
- The query string is forwarded verbatim but cannot escape the path: a raw space or tab in
  the request line is rejected by fasthttp, and any control byte makes `url.Parse` error out
  to a 400 before the request is built. `#` merely truncates the query. Upstream query
  handling is fully parameterised (`list_runs` uses `%s` placeholders throughout), so nothing
  smuggles into SQL either.

**5. Credential leakage — none found.** `TELEMETRY_UPSTREAM_TOKEN` is written only to
`request.Header` (`:390`) and never read back, logged, or echoed. The upstream response body
is echoed only on 2xx; non-2xx bodies are replaced by `telemetryStatusMessage` (`:409-416`),
which is the right call given FastAPI puts raw exception text in `detail`. The
`telemetryHTTPClient.Do` error path logs a `*url.Error` whose URL is password-stripped by
`net/http`. Only `Content-Type` is copied from the upstream response — no `Set-Cookie`
passthrough. The UI password appears in no log line, no error, and no response. The audit log
at `:394-396` correctly logs method/path/user and never the body (mint and rotate return
plaintext secrets).

**6. SSRF — no.** `target` is `cfg.upstream` (env only) + `telemetryUpstreamPrefix` +
allowlist-validated `rest`. The host and scheme are fixed before any request-derived byte
appears, and the first request-derived byte is a `/`-prefixed path, so no `@`-userinfo or
scheme-confusion trick applies. The query string appends after `?` and cannot reach the
authority component.

**7. CSP — sound, and `style-src 'unsafe-inline'` is an acceptable trade here.** I checked
every directive against the actual markup:

- The script hash is computed over the *rewritten* document (`init()` rewrites first, then
  calls `buildTelemetryCSP`), over exactly the text between `<script>` and `</script>`, which
  is what the browser hashes. There is exactly one `<script` in the file and it is
  attribute-free, so `strings.Index(doc, "<script>")` cannot mismatch. The startup panics
  make drift impossible.
- Nothing in the console violates the policy: no `<link>`, no `<img>`, no `url()`, no
  `@import`, no `@font-face`, no forms, no `eval`/`new Function`, no `javascript:`, no HTML
  `on*` attributes (all handlers are assigned as JS properties). `fetch` is same-origin.
  `svg.innerHTML` at :413 is inline SVG, unaffected. The page will not break at runtime.
- `frame-ancestors 'self'` is correct for the same-origin iframe in `TelemetryConsole.vue`.
- The `'unsafe-inline'` style relaxation is not reachable by an attacker: all 12 inline
  `style` attributes interpolate either module constants (`SEV_COLOR`) or *arithmetic*
  (`okn/max*100`), and arithmetic on an attacker-supplied string coerces to `NaN`, not
  markup. Even a hypothetical CSS injection could not exfiltrate, since `background-image:
  url(https://evil)` is governed by `img-src 'self' data:`.
- `connect-src 'self'` is doing exactly the load-bearing job the comment claims.

**8. Stored XSS in the vendored console — none exploitable.** Traced every attacker-controlled
ingest field (`hostname`, `site`, `script`, `result`, `tech`, `serial`, `mac`, `ad_domain`,
`ip`, `details` keys and values, and the full `log` transcript) through `riverRow` (:467),
the drawer/`kv`/transcript renderer (:512-522), `fillSelect` (:464) and `renderKeys`
(:586-607). Every one is `esc()`-wrapped, and every sink is a text node or a double-quoted
attribute.

The unescaped interpolations I found — `${r.exit_code}` and `${r.duration_sec}` at :472,
`${k.id}` and `${k.use_count}` at :591/:595, `${secret.rotated_from}` at :589, and the
`renderStats` counters at :367 — are all server-typed integers, confirmed against
`main.py:183/186` (`exit_code: int | None`, `duration_sec: int | None`, Pydantic-validated at
ingest) and the `api_keys` schema. A string payload in `exit_code` is rejected at ingest, not
rendered. And even if one of these regressed, the hash-based `script-src` with no
`'unsafe-inline'` blocks both injected `<script>` blocks and injected `on*` handlers, so the
CSP is a real second layer rather than decoration.

**9. DELETE guard TOCTOU — not exploitable.** There is a window between the `GET /keys` check
(`:431`) and the proxied DELETE, but flipping a key from revoked back to active is the only
way to abuse it and **no un-revoke endpoint exists** upstream (`main.py` exposes list,
create, revoke, rotate, delete only) — `revoked` is a monotonic one-way flag. Nor is the
guard bypassable by input: the id is derived from the same validated `rest`, the allowlist
pins DELETE to exactly two segments, `json.Number.String()` compares the literal so `/keys/05`
does not match key `5`, and every failure mode (unreachable upstream, decode error, unknown
id) returns an error and 409s. `list_keys` coerces `revoked` to a real JSON bool
(`main.py:513`), so the `Revoked bool` decode does not silently fail either. Note it is a
guardrail against operator misclicks, not a security control — anyone past the gate can
revoke-then-delete in two calls.

---

## Notes, not findings

- `docker-compose.telemetry.yml` is correct on the points that matter: no `ports:` on
  `lltel-api`, DB on an `internal: true` network, and the file is deliberately not named
  `docker-compose.override.yml`. `LL_DB_PASS`/`LL_INGEST_TOKEN` have weak inline defaults
  (`:-lltel`, `:-local-dev-ingest-token`) but this file is dev-only and never deployed.
- Pre-existing, inherited by these routes: `api.go:58-63` enables CORS with
  `AllowCredentials: true` for `http://localhost:8081` when `ENVIRONMENT=dev`. Under that
  env var a page on localhost:8081 could *read* telemetry cross-origin. Nothing sets it in
  compose; worth keeping it that way.
- `telemetryResponseLimit` (16 MiB) silently truncates rather than erroring, which would
  surface to the console as a JSON parse failure. Cosmetic.
