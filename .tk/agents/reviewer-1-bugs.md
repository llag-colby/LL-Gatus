# LL-Telemetry integration — bug review

Scope: `api/telemetry.go`, the LL-Telemetry additions in `api/api.go`,
`collector/seed_telemetry.py`, `web/app/src/views/TelemetryConsole.vue`,
`docker-compose.telemetry.yml`.

Cross-checked against the real upstream at `../LL-Telemetry/telemetry/api/main.py`
and the vendored console at `api/assets/telemetry-console.html`.

**8 findings. 2 blockers, 4 should-fix, 2 minor.**

---

## F1 — BLOCKER — a telemetry DB outage renders as "not configured"

**FILE:** `web/app/src/views/TelemetryConsole.vue:88`
(with `api/telemetry.go:199` and `api/telemetry.go:409-415`)

`503` is overloaded. It is emitted by two completely different conditions and the
probe cannot tell them apart:

1. `TelemetryGate` returns `503` when credentials are unset (`api/telemetry.go:199`).
2. The upstream `/api/v1/health` raises `503` when MariaDB is unreachable:

   ```python
   # ../LL-Telemetry/telemetry/api/main.py:211
   raise HTTPException(status_code=503, detail=f"db unavailable: {e}")
   ```

   and `TelemetryProxy` passes the upstream status through verbatim
   (`api/telemetry.go:413`: `return c.Status(response.StatusCode).JSON(...)`).

So when the telemetry DB is down — the single most likely real-world failure, and
exactly the one the seeder's `wait_for_api()` exists to tolerate — the probe hits
branch 2, sets `state = 'unconfigured'`, and the operator is told:

> "LL-Telemetry isn't connected yet ... Set these in `.env` and restart Gatus:
> `TELEMETRY_UI_USER` ..."

The credentials are fine. The advice is wrong, and following it means restarting
Gatus for nothing while the actual fault (MariaDB) goes undiagnosed. The
`'unreachable'` state — which has the correct copy *and* a "Try again" button —
is the one that should fire here.

**FIX** — make the two 503s distinguishable. Cheapest is a marker header on the
gate's response:

```go
// api/telemetry.go, in TelemetryGate
c.Set("X-Telemetry-Configured", "false")
return c.Status(fiber.StatusServiceUnavailable).JSON(...)
```

```js
// TelemetryConsole.vue
if (response.status === 503 && response.headers.get('X-Telemetry-Configured') === 'false') {
  state.value = 'unconfigured'
} else if (response.status >= 502 && response.status <= 504) {
  state.value = 'unreachable'
} else { state.value = 'ready' }
```

Alternatively clamp upstream 5xx in the proxy so an upstream status can never
collide with a gate status:

```go
status := response.StatusCode
if status >= 500 { status = fiber.StatusBadGateway }
return c.Status(status).JSON(fiber.Map{"error": telemetryStatusMessage(response.StatusCode)})
```

---

## F2 — BLOCKER — DELETE can run ~24s against a 15s WriteTimeout

**FILE:** `api/telemetry.go:361` and `api/telemetry.go:431`

`telemetryRequireRevoked` builds its request with **`http.NewRequest`, not
`NewRequestWithContext`** (:431), so it inherits no deadline and no cancellation
from the inbound request. It then shares `telemetryHTTPClient`, whose `Timeout`
is 12s (:71). The forward that follows gets its *own* fresh 12s (:381/:397).

Worst case for one `DELETE /keys/{id}`:

```
telemetryRequireRevoked  GET /keys   → up to 12s
telemetryHTTPClient.Do   DELETE      → up to 12s
                                     ─────────
                                        24s
```

against `server.WriteTimeout = 15 * time.Second` (`controller/controller.go:23`).
Past 15s fasthttp's write deadline has already fired, so the handler's eventual
`c.Status(...).JSON(...)` never lands — the console sees a severed connection,
which is precisely the failure mode the 12s comment at :69-71 was written to
avoid. The comment's arithmetic is right for a single hop and wrong for the only
two-hop path in the file.

Secondary: because the precheck has no context, a caller who disconnects still
leaves the GET running to completion.

**FIX** — one budget for the whole handler, threaded through both hops:

```go
func TelemetryProxy(c *fiber.Ctx) error {
	cfg := telemetryCfg()
	ctx, cancel := context.WithTimeout(c.UserContext(), 12*time.Second)
	defer cancel()
	...
	if c.Method() == fiber.MethodDelete {
		if err := telemetryRequireRevoked(ctx, cfg, id); err != nil { ... }
	}
	request, err := http.NewRequestWithContext(ctx, c.Method(), parsed.String(), body)
```

```go
func telemetryRequireRevoked(ctx context.Context, cfg telemetryConfig, id string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.upstream+telemetryUpstreamPrefix+"/keys", nil)
```

**Related, same area:** the comment at :70 says a slow upstream "surfaces as a
clean 504", but a client timeout produces an error from `Do` and the code returns
**502** (`fiber.StatusBadGateway`, :400). Nothing in `telemetry.go` can ever emit
504, so the `response.status === 504` branch in `TelemetryConsole.vue:90` is
unreachable. Either return 504 on `errors.Is(err, context.DeadlineExceeded)` or
fix the comment.

---

## F3 — SHOULD FIX — `telemetryRequireRevoked` never checks the upstream status, and its failures surface as 409 Conflict

**FILE:** `api/telemetry.go:439-462` (mapped at `:362`)

There is no status check between `Do` and `Decode`. FastAPI error bodies are
`{"detail": "..."}`, which unmarshals **cleanly** into the anonymous struct —
`payload.Keys` is simply `nil`. The loop then finds nothing and returns:

```go
return fmt.Errorf("no such telemetry key")
```

which the caller turns into `409 Conflict`. So a `500` from `GET /keys` (e.g. the
missing-`api_keys`-table case the compose file's own comment at
`docker-compose.telemetry.yml:36-38` warns about), a `401`, or a `503` all tell
the operator **"no such telemetry key"** for a key that plainly exists in the UI
they are looking at.

The status-code choice is wrong too: every error from this function is mapped to
`409` at `:362`, but only one of the four is a conflict. "Could not verify the key
state" is an upstream failure (502/503), not a conflict.

**FIX:**

```go
response, err := telemetryHTTPClient.Do(request)
if err != nil {
	return errTelemetryVerify
}
defer response.Body.Close()
if response.StatusCode < 200 || response.StatusCode >= 300 {
	logr.Warnf("[api.TelemetryProxy] key-state precheck: upstream GET /keys returned %d", response.StatusCode)
	return errTelemetryVerify
}
```

and have the caller distinguish them, e.g. a sentinel:

```go
var errTelemetryVerify = errors.New("could not verify the key state before deleting")
var errTelemetryActive = errors.New("revoke this key before deleting it: ...")

if err := telemetryRequireRevoked(ctx, cfg, id); err != nil {
	status := fiber.StatusConflict
	if errors.Is(err, errTelemetryVerify) {
		status = fiber.StatusBadGateway
	}
	return c.Status(status).JSON(fiber.Map{"error": err.Error()})
}
```

---

## F4 — SHOULD FIX — bcrypt runs on every request, including unauthenticated ones

**FILE:** `api/telemetry.go:182` (reached from `TelemetryGate`, `:204`)

```go
return bcrypt.CompareHashAndPassword(c.uiBcryptHash, []byte(password)) == nil
```

Two problems, both from there being no verification cache:

1. **DoS amplification.** Anyone who can reach the port can force a full bcrypt
   derivation per request with no valid credential at all. At the common cost of
   10-12 that is ~60-300ms of CPU per request; a handful of concurrent
   connections saturates the box, and it takes the rest of Gatus down with it
   because they share the process.
2. **Steady-state cost on the happy path.** The gate is a `Use` on the whole
   `/api/v1/telemetry` prefix, and the console polls `/stats`, `/timeline` and
   `/runs`. Every one of those fetches pays bcrypt again.

`TELEMETRY_UI_PASSWORD_BCRYPT` is the documented *preferred* option
(`telemetry.go:262`, `TelemetryConsole.vue:19`), so this is the path most
deployments will take.

**FIX** — verify once per credential, then compare a cheap digest:

```go
var (
	telemetryAuthCache   = map[[32]byte]time.Time{}
	telemetryAuthCacheMu sync.Mutex
)

func (c telemetryConfig) authenticate(user, password string) bool {
	if subtle.ConstantTimeCompare([]byte(user), []byte(c.uiUser)) != 1 {
		return false
	}
	if len(c.uiBcryptHash) == 0 {
		return subtle.ConstantTimeCompare([]byte(password), []byte(c.uiPassword)) == 1
	}
	key := sha256.Sum256([]byte(user + "\x00" + password))
	telemetryAuthCacheMu.Lock()
	until, ok := telemetryAuthCache[key]
	telemetryAuthCacheMu.Unlock()
	if ok && time.Now().Before(until) {
		return true
	}
	if bcrypt.CompareHashAndPassword(c.uiBcryptHash, []byte(password)) != nil {
		return false
	}
	telemetryAuthCacheMu.Lock()
	telemetryAuthCache[key] = time.Now().Add(5 * time.Minute)
	telemetryAuthCacheMu.Unlock()
	return true
}
```

Only successes are cached, so a guessing attacker still pays bcrypt — pair it
with a small per-IP failure counter if that matters.

---

## F5 — SHOULD FIX — the seeder's 429 backoff is ~3.3x too short and ignores `Retry-After`

**FILE:** `collector/seed_telemetry.py:207-221`

The comment says "wait the window out", but the code cannot:

```python
for attempt in range(4):
    ...
    if e.code == 429 and attempt < 3:
        time.sleep(30)          # max 3 sleeps = 90s total
```

The upstream limiter is a **sliding** window, not a fixed one
(`../LL-Telemetry/telemetry/api/main.py:114-138`):

```python
RATE_MAX    = 300     # main.py:29
RATE_WINDOW = 300     # main.py:30
retry = int(hits[0] + RATE_WINDOW - now) + 1
```

The seeder bursts through 300 runs in a few seconds, so at the moment of the
first 429 `hits[0]` is only seconds old and `retry` is ≈ **300s**. 90s of total
backoff expires with the bucket still full, the run is counted as failed, and
every subsequent run repeats the same 90s-then-give-up cycle.

With the default `SEED_RUNS = 400` (`:31`) this is not a corner case, it is the
guaranteed outcome: ~300 accepted, ~100 failed, and the seed takes far longer
than it should while dropping a quarter of the data.

The upstream even hands you the correct number and the code throws it away —
`main.py:233` sets `headers={"Retry-After": str(retry)}`, available as
`e.headers.get("Retry-After")`.

**FIX:**

```python
except urllib.error.HTTPError as e:
    if e.code == 429 and attempt < 3:
        if not throttled:
            print("ingest rate limit reached, pacing to stay under it", flush=True)
            throttled = True
        try:
            delay = int(e.headers.get("Retry-After", "30"))
        except (TypeError, ValueError):
            delay = 30
        time.sleep(min(max(delay, 1) + 1, 330))
        continue
```

One full-window sleep is enough for the whole backlog: after 300s every hit has
aged out of the sliding window, so the remaining ~100 runs all go through.

**Also in this loop** — `seed_telemetry.py:210-213`, the `else: failed += 1`
branch is dead code. `urllib.request.urlopen` raises `HTTPError` for every status
outside 200-299, so `post()` can only ever return a 2xx and `if 200 <= status <
300` is always true. Harmless today, but it reads as live error handling and will
mislead the next reader. Drop it, or switch `post()` to a handler that returns
non-2xx instead of raising.

---

## F6 — SHOULD FIX — a truncated upstream response is served as a success

**FILE:** `api/telemetry.go:405`

```go
payload, err := io.ReadAll(io.LimitReader(response.Body, telemetryResponseLimit))
```

`io.LimitReader` returns `io.EOF` at the cap, not an error, so a response larger
than 16 MiB is silently cut mid-token and then handed to the console with the
upstream's `200` and `Content-Type: application/json`. The console's
`await r.json()` throws a bare `SyntaxError` and `keysErr` /the error panel shows
a JSON parse message with nothing pointing at the real cause.

`GET /runs` with a wide window and 300 000-char logs (`MAX_LOG_CHARS = 300000`,
`main.py:23`) is the realistic way to hit this.

**FIX** — read one byte past the cap and detect the overflow:

```go
payload, err := io.ReadAll(io.LimitReader(response.Body, telemetryResponseLimit+1))
if err != nil { ... }
if len(payload) > telemetryResponseLimit {
	logr.Warnf("[api.TelemetryProxy] upstream %s %s exceeded %d bytes", c.Method(), upstreamPath, telemetryResponseLimit)
	return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "the telemetry service response was too large"})
}
```

---

## F7 — MINOR — upstream `Content-Type` is echoed verbatim with no `nosniff`

**FILE:** `api/telemetry.go:417-423`

```go
contentType := response.Header.Get("Content-Type")
if contentType == "" { contentType = fiber.MIMEApplicationJSON }
c.Set(fiber.HeaderContentType, contentType)
```

Every allowlisted endpoint returns JSON, so there is no reason to trust the
upstream's declaration here. `TelemetryConsole` sets
`X-Content-Type-Options: nosniff` (:238) but the proxy sets none, and the console's
strict CSP (:104-116) governs only the console *document* — it does not apply to a
proxy response opened directly.

The content is not fully trusted: run transcripts arrive from field scripts
POSTing straight to the telemetry host with their own `llk_` keys, entirely
outside this gate. A `text/html` content type on that path would render
attacker-supplied transcript text as same-origin HTML on the Gatus origin.

**FIX** — pin it, since the allowlist guarantees JSON:

```go
c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
c.Set("X-Content-Type-Options", "nosniff")
c.Set(fiber.HeaderCacheControl, "no-store")
```

---

## F8 — MINOR — the query string bypasses the validation the path gets

**FILE:** `api/telemetry.go:366-368`

The path is validated hard — `%` rejected, `path.Clean` identity, a strict
character allowlist. The query string gets none of that:

```go
if raw := string(c.Request().URI().QueryString()); raw != "" {
	target += "?" + raw
}
```

`url.Parse` rejects ASCII control bytes, so CRLF injection is blocked. A literal
**space** (0x20) is not a control byte: it survives `url.Parse`, is stored raw in
`RawQuery`, and `URL.RequestURI()` writes it verbatim into the outbound request
line, producing a malformed HTTP request to uvicorn. Post-authentication only, so
impact is low, but it is an asymmetry that will look like an oversight later.

**FIX** — normalise through the parser instead of string concatenation:

```go
parsed, err := url.Parse(cfg.upstream + upstreamPath)
if err != nil { ... }
parsed.RawQuery = c.Request().URI().QueryArgs().String()
```

---

## F9 — MINOR / LATENT — the CSP hash silently covers only the first `<script>`

**FILE:** `api/telemetry.go:118-130` and `:75-87`

`inlineHash` takes `strings.Index(doc, "<script>")` — the first *literal* match —
and `init()` asserts only on the API-base string, never on the script count.

Verified correct today: `api/assets/telemetry-console.html` has exactly one
`<script>` and one `<style>`, and the rewrite at `:85` runs before
`buildTelemetryCSP` at `:86`, so the hash covers the rewritten body. The whole
point of the vendoring strategy, though, is that this file gets resynced from
upstream with a plain diff. A resync that adds a second inline script, or changes
the tag to `<script defer>`, produces either a hash for the wrong block or a
`panic` at a spot with no assertion explaining why — and the first of those is a
blank console with a console-only CSP violation, at startup, with no log line.

**FIX** — extend the existing `init()` guard, which already has exactly the right
shape:

```go
if n := strings.Count(telemetryConsoleRaw, "<script"); n != 1 {
	panic(fmt.Sprintf("api/assets/telemetry-console.html: expected exactly one <script> but found %d; "+
		"buildTelemetryCSP hashes only the first and the rest would be blocked at runtime", n))
}
```

---

# Checked and clean

These were called out for scrutiny and are **not** defects. Recorded so they are
not re-litigated.

**`telemetryCfg()` / `sync.Once` (`:149-168`) — safe.** `Once.Do` establishes a
happens-before edge between the write to `telemetryConfigured` inside `f` and the
return of *every* `Do` call, including concurrent ones that block on the first.
The read at `:167` is therefore ordered after the write on all goroutines. No
other code in the package touches `telemetryConfigured`. Not a race.

**`body != nil` (`:386`) — not the typed-nil trap.** `body` is declared as a bare
`io.Reader` and the only assignment (`:379`) stores a non-nil `*strings.Reader`. A
typed nil is never placed in the interface, so the check does what it reads as.
`Immutable: true` is irrelevant here as well: `c.Body()` returns a `[]byte` view
of the fasthttp buffer either way, and `strings.NewReader(string(raw))` copies it
before the buffer can be recycled.

**DELETE id extraction (`:361`) — correct today, fragile tomorrow.**
`strings.TrimPrefix(strings.TrimPrefix(rest, "/"), "keys/")` runs *after*
`telemetryUpstreamPath` has already returned `allowed`, and `{MethodDelete,
{"keys", "*"}}` is the only DELETE entry in the allowlist, so `rest` is
necessarily `keys/<id>` or `/keys/<id>`. It handles both the leading-slash and
no-leading-slash forms of `c.Params("*")`. If a second DELETE route is ever added
the second `TrimPrefix` no-ops and the full path is compared against key ids —
which fails closed with a 409, but confusingly. Worth deriving the id from the
matched segments (have `telemetryUpstreamPath` return them) rather than
re-parsing the string.

**Allowlist matcher (`:298-340`) — I could not get anything unintended through.**
`%` is rejected outright, so no percent-decoding ordering exists to exploit. The
`rest != path.Clean(rest)` identity check kills `//`, trailing slashes, `.` and
`..` in every position. The rune allowlist kills `\`, `;`, `@`, `?`, `#` and NUL.
Empty `*` segments are rejected explicitly (`:324`). Comparison is
case-sensitive, so `/Keys` *denies* rather than matches — deny-by-default, the
safe direction. `c.Params("*")` returns `""` for `/api/v1/telemetry`, which splits
to `[""]` and matches no single-segment route. Fiber's `UnescapePath` is not
enabled in `createRouter` (`api/api.go:49-57`), so params reach the matcher
percent-encoded and hit the `%` rejection. `POST /runs` is correctly absent.

One thing I could not verify from source in this environment: whether Fiber v2
rejects a non-canonically-cased method (`delete`) before dispatch, which is what
`route.method != method` at `:318` relies on. I believe it does — `methodInt`
returns -1 for unrecognised methods and the handler short-circuits — but it is
worth a table test pinning `telemetryUpstreamPath("delete", "keys/1")` to
`false`, since the consequence if the assumption is wrong is an allowlist bypass.

**Credential and body leakage (`:389-415`) — clean.** `cfg.upstreamToken` is set
as a header and never logged or echoed. `http.Client` errors are `*url.Error`
whose `URL` field has been through net/http's `stripPassword`, so credentials
embedded in `TELEMETRY_UPSTREAM_URL` do not reach the log at `:399`. Non-2xx
upstream bodies are replaced wholesale by `telemetryStatusMessage`, so FastAPI's
`detail` (which carries raw exception text — e.g. `f"db unavailable: {e}"` at
`main.py:211`) never reaches the browser. The comment at `:410-411` is accurate.
`upstreamPath` is character-restricted before it reaches any format string, so
log injection via newline is not possible either.

**Seeder `throttled` scoping (`:201`, `:217-219`) — correct.** Python scoping is
per-function, not per-block. `throttled` is bound at `:201` before both loops, so
the nested `throttled = True` rebinds that same local. No `UnboundLocalError`, and
the banner prints exactly once for the whole run.

**Seeder run counts (`:199-231`) — accurate.** Every iteration of the outer
`for _ in range(RUN_COUNT)` increments exactly one of `accepted`/`failed`: the
inner loop's only non-terminal path is the 429 `continue`, and its final attempt
(`attempt == 3`) falls through to `failed += 1; break`. So `accepted + failed ==
RUN_COUNT` always. `run_id` is a fresh `uuid4()` per run, so replay
de-duplication upstream cannot inflate `accepted` against what was actually
stored. `HTTPError` subclasses `URLError` subclasses `OSError`, and the `except`
clauses are ordered most-specific-first (`:215` before `:226`), so the 429 handler
is reachable. `RESULTS` weights sum to exactly 100.

**Vue lifecycle (`TelemetryConsole.vue:100-108`) — correct.** `measureHeader` is
the same function reference on both `addEventListener` and `removeEventListener`,
so the listener is genuinely removed. Setting a `ref` after unmount is harmless in
Vue 3, so the un-awaited `probe()` at `:103` needs no guard. The `frameEl` ref
(`:67`) is bound in the template but never read in script — dead, and safe to
delete. The `announcements` prop (`:56-58`) is declared but unused; it is passed
to every route component by `App.vue:124`, so declaring it is right, it just is
not rendered here.

**`docker-compose.telemetry.yml` — no defects found.** `lltel-api` explicitly
lists `default` alongside `lltel-internal`, so it does not lose the shared network
Gatus proxies over — the exact trap the header comment (lines 12-15) documents for
the `gatus` service. `lltel-db` is `internal: true` only. `lltel-seed` has
`depends_on: lltel-api` with no `condition`, but `seed_telemetry.py:136` polls
`/health` for up to 120s, which covers it. The `db/` directory mount (line 39)
with its comment about `02-apikeys.sql` is right — mounting only `01-schema.sql`
would leave `list_keys` (`main.py:500`) selecting from a nonexistent table.

**`api/api.go:205-207` — route ordering correct.** `Get("/console")` is registered
before `All("/*")`, so the console is not swallowed. A non-GET to `/console` falls
to the proxy and 403s on the allowlist, which is the right answer. The `Group`
prefix `/api/v1/telemetry` mounts `TelemetryGate` as a `Use`, which prefix-matches
*more* broadly than the proxy route — the safe direction; nothing routable sits
under the gate's prefix without passing through it.
