# Reviewer 4 — session auth in `api/telemetry.go`

Scope: the newly added session store, per-IP failure throttle, `TelemetryLogin`/`TelemetryLogout`,
and the rewritten `TelemetryGate`. Proxy/allowlist code reviewed only where the gate depends on it.

Verdict: **APPROVED: no** — 2 blockers, 4 suggestions.

---

## BLOCKER 1 — the Basic-auth path in the gate is not throttled at all

**FILE:** `api/telemetry.go`
**LINE:** 464-473 (and 483-501)
**CONFIDENCE:** 95%

`telemetryThrottled` / `telemetryRecordFailure` are called **only** from `TelemetryLogin`
(lines 220, 234). The gate's Basic-auth fallback records nothing and checks nothing:

```go
header := c.Get(fiber.HeaderAuthorization)
user, password, ok := parseBasicAuthHeader(header)
if !ok || !telemetryAuthorized(cfg, header, user, password) {
    return c.Status(fiber.StatusUnauthorized).JSON(...)   // no throttle, no counter
}
```

Why it matters:

1. **The throttle is decorative.** An attacker never touches `POST /session`; they grind
   `Authorization: Basic` against `GET /api/v1/telemetry/health` at unlimited rate. The 10-per-5-min
   limit protects the one door nobody has to use.
2. **It is also an unauthenticated CPU-exhaustion vector.** `telemetryAuthorized` caches only
   successes (deliberately, per the comment at 478-482), so every wrong password pays a full bcrypt
   derivation. With no throttle, an unauthenticated caller pins the CPU on a directly published
   `:8080` (docker-compose.yml maps `8080:8080`). The comment at line 481 identifies exactly this
   risk and then leaves the path unguarded.
3. **The docs already claim this is implemented.** `docs/ll-telemetry.md:88-90`: "Failed sign-ins are
   counted per source address: 10 within 5 minutes and *the gate* returns 429." The gate never
   returns 429.

**FIX:** move the throttle into `TelemetryGate`, around the Basic branch:

```go
address := c.IP()
header := c.Get(fiber.HeaderAuthorization)
if header != "" && telemetryThrottled(address) {
    return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "..."})
}
user, password, ok := parseBasicAuthHeader(header)
if !ok || !telemetryAuthorized(cfg, header, user, password) {
    if header != "" {
        telemetryRecordFailure(address)
    }
    return c.Status(fiber.StatusUnauthorized).JSON(...)
}
telemetryClearFailures(address)
```

Guard on `header != ""` deliberately: the SPA's `probe()` (`TelemetryConsole.vue:172`) fetches
`/health` with **no** credentials on every page load and refocus to detect the login state. Counting
credential-less requests as failures would lock the operator out after 10 page loads.

---

## BLOCKER 2 — `telemetryFailures` is never swept and grows without bound

**FILE:** `api/telemetry.go`
**LINE:** 113-114, 190-200
**CONFIDENCE:** 90%

Entries leave the map in exactly two places: `telemetryThrottled` deletes **that one address** if it
is re-checked after the window expires (183-186), and `telemetryClearFailures` deletes on a
successful login (202-206). Nothing sweeps. An address that fails once and never returns keeps its
entry for the lifetime of the process.

`newTelemetrySession` sweeps on every mint (137-141) and `telemetryAuthCache` is hard-capped at 64
(496-498), so the omission here looks like an oversight rather than a decision. An attacker rotating
source addresses — trivial from an IPv6 /64, and each address only needs one failed request — adds a
permanent map entry per address. This is unbounded memory growth reachable pre-authentication.
Blocker 1 makes it worse: once the gate records failures too, every unauthenticated probe from a new
address allocates.

**FIX:** sweep inside `telemetryRecordFailure` while the lock is already held, mirroring
`newTelemetrySession`:

```go
func telemetryRecordFailure(address string) {
    telemetryFailuresMu.Lock()
    defer telemetryFailuresMu.Unlock()
    now := time.Now()
    for addr, r := range telemetryFailures {
        if now.Sub(r.since) > telemetryFailureReset {
            delete(telemetryFailures, addr)
        }
    }
    record, ok := telemetryFailures[address]
    if !ok || now.Sub(record.since) > telemetryFailureReset {
        telemetryFailures[address] = telemetryFailureCount{count: 1, since: now}
        return
    }
    record.count++
    record.since = now // see suggestion 2
    telemetryFailures[address] = record
}
```

A hard cap (drop the map when `len > N`, as the auth cache does) is a reasonable belt-and-braces
addition, but the sweep alone bounds it at "addresses seen in the last 5 minutes".

---

## SUGGESTION 1 — `c.IP()` collapses to one bucket behind any reverse proxy

**FILE:** `api/telemetry.go:219` (with `api/api.go:49-57`)
**CONFIDENCE:** 85% on the mechanism

`fiber.New` in `api/api.go` sets `ErrorHandler`, `ReadBufferSize`, `Network`, `Immutable` — no
`ProxyHeader`, no `EnableTrustedProxyCheck`, no `TrustedProxies`. In fiber v2.52, `Ctx.IP()` returns
`c.fasthttp.RemoteIP()` unless `ProxyHeader` is configured. So the throttle key is the **socket
peer**, not the client.

- **Today** (docker-compose publishes `8080:8080`, no proxy in the compose file) the client IP is
  preserved and the throttle behaves as intended.
- **Behind Caddy/nginx/traefik** — where TLS terminates, which the `Secure` flag at line 252 and the
  docs' TLS language both anticipate — every request carries the proxy's address. All operators then
  share one bucket: any 10 bad sign-ins from anywhere on the internet lock out *everyone* for up to
  5 minutes, and conversely one legitimate login clears the attacker's counter (line 245). The
  throttle flips from a defence to a self-DoS.

Not a bug in the code as written; it is a hidden coupling to deployment topology that will fail
silently the day a proxy is added.

**FIX:** either configure fiber for the real topology —

```go
app := fiber.New(fiber.Config{
    ...
    ProxyHeader:             fiber.HeaderXForwardedFor,
    EnableTrustedProxyCheck: true,
    TrustedProxies:          []string{"172.16.0.0/12"}, // the proxy only
})
```

— or make the throttle topology-independent by keying on the submitted username as well as the
address, so a shared source address cannot lock out a different operator. Do **not** set
`ProxyHeader` without `EnableTrustedProxyCheck` + `TrustedProxies`: that hands the throttle key to
the client and makes the limit bypassable with a header.

---

## SUGGESTION 2 — the lockout window is anchored to the first failure, so the penalty is arbitrarily short

**FILE:** `api/telemetry.go:194-199` vs `183-187`
**CONFIDENCE:** 85%

`since` is written once, when a window opens, and never advanced (`record.count++` at 198 leaves
`since` alone). Both the expiry check in `telemetryRecordFailure` (194) and the one in
`telemetryThrottled` (183) measure from that first failure.

Traced, as requested: 9 failures at t0 → `{count: 9, since: t0}`, not throttled (9 < 10). 10th
failure → `{count: 10, since: t0}` → throttled. So the lockout **is** reachable, and it **does**
release: at t0+5m01s `telemetryThrottled` deletes the entry and returns false. Correct on both
counts.

The weakness is the duration. The penalty is `5m − (time from 1st to 10th failure)`. An attacker who
paces the 10th attempt to land at t0+4m59s is locked out for one second, then gets a fresh window of
10. A victim who fumbles their password ten times in ten seconds eats the full five minutes. The
punishment is inverted: the patient attacker pays nothing, the flustered operator pays everything.

Secondary: `telemetryThrottled` (line 220) and `telemetryRecordFailure` (line 234) are two separate
lock acquisitions around a bcrypt call — a check-then-act. N concurrent requests can all pass the
check while `count == 9`. Bcrypt's cost limits the practical damage, but the cap is soft.

**FIX:** advance `since` on every recorded failure (sliding window), so the lockout is a real 5
minutes from the *last* attempt — one line, shown in the Blocker 2 patch above. If the concurrent
burst matters, fold check-and-record into a single function that holds the lock across both.

---

## SUGGESTION 3 — logout revokes only the presented token; earlier sessions stay live for 12h

**FILE:** `api/telemetry.go:240-245`, `260-273`
**CONFIDENCE:** 85%

Answering the question directly: **logout does invalidate server-side.** `dropTelemetrySession`
(164-171) deletes the map entry, and the gate's only acceptance path is a map hit
(`telemetrySessionUser`, 147-162), so a stale cookie for a dropped or expired token cannot
authenticate. There is no session fixation on the mint path either — the token is 256 bits from
`crypto/rand` and no caller-supplied value is ever accepted. That part is correct.

The gap is that `TelemetryLogin` mints a new token (240) **without dropping the one in the inbound
cookie**. Sign in twice and there are two live server-side sessions; the browser only remembers the
second, because the `Set-Cookie` overwrites it. The first is now unreachable by the operator but
valid for its full 12 hours, and "Sign out" cannot revoke it because logout only knows the cookie it
was handed. Any copy of that earlier token — a cookie jar on a shared machine, a proxy log from a
plain-HTTP hop — outlives the logout that was supposed to kill it.

**FIX:** drop the presented token before minting:

```go
dropTelemetrySession(c.Cookies(telemetrySessionCookie))
token, err := newTelemetrySession(user)
```

If you want "sign out everywhere", store sessions keyed by token with the user recorded (already the
case) and have logout delete every entry whose `user` matches.

---

## SUGGESTION 4 — the path-suffix auth skip is fail-closed in one direction and one allowlist entry from fail-open in the other

**FILE:** `api/telemetry.go:455`
**CONFIDENCE:** 90% on the behaviour; the "not exploitable today" is a traced conclusion

```go
if strings.HasSuffix(c.Path(), "/telemetry/session") {
    return c.Next()
}
```

**Direction A — legitimate requests that fail closed.** `api/api.go:49` sets neither `CaseSensitive`
nor `StrictRouting`, so fiber's defaults apply: routing matches case-insensitively and ignores
trailing slashes, using an internal lowercased/trimmed `detectionPath`. `c.Path()` returns the
*original* path, untouched. Result: `POST /api/v1/telemetry/session/` and
`POST /api/v1/telemetry/SESSION` both route to `TelemetryLogin`, but both fail the suffix check —
so the gate demands a session in order to reach the endpoint that creates a session. Any client that
appends a trailing slash gets a permanent 401 with no way in. The SPA doesn't do this, so it is
latent, but it is a real 401-on-login for hand-written curl and any client library that normalises
with a trailing slash.

**Direction B — requests that skip auth.** *Any* path ending in `/telemetry/session` skips
authentication and falls through to `telemetryRouter.All("/*", TelemetryProxy)`
(`api/api.go:235`). `GET /api/v1/telemetry/runs/telemetry/session` reaches `TelemetryProxy`
completely unauthenticated.

I traced whether this is currently exploitable and concluded **it is not**: `telemetryUpstreamPath`
(588-630) requires an exact segment-count match against the allowlist, and no allowlisted route has
`telemetry` / `session` as its last two segments (2-segment routes are `runs/*` and `keys/*`;
3-segment routes end in `revoke` or `rotate`), so every such request dies at the 403. Percent-encoded
variants (`/api/v1/telemetry/keys%2f..%2ftelemetry/session`) also reach the proxy unauthenticated and
are stopped by the `%` rejection at 589. Dot-segment variants are normalised before matching.

So the allowlist is the only thing standing between a suffix match and an unauthenticated upstream
call. That is a single future allowlist entry — say `GET /telemetry/session` for some unrelated
upstream feature, or any 2-segment route whose wildcard could be `session` — away from a real
bypass, and the dependency is invisible from either file.

**FIX:** match the exact path instead of a suffix, which fixes both directions at once:

```go
if strings.EqualFold(strings.TrimRight(c.Path(), "/"), telemetrySessionPath+"/session") {
    return c.Next()
}
```

Cleaner still: register the two session routes outside the gated group and have them call
`telemetrySameOriginWrite` and `cfg.configured()` themselves — `TelemetryLogin` already duplicates
the configured check at 212-218; `TelemetryLogout` would need both added.

---

## Checked and clean

- **Lock discipline — no races found.** Every acquisition pairs with exactly one release on every
  path. `newTelemetrySession` (136/143) and `dropTelemetrySession` (168/170) and
  `telemetryClearFailures` (203/205) use bare `Unlock` but contain no early return, no panic-prone
  call, and no branch between lock and unlock. `telemetrySessionUser` (151), `telemetryThrottled`
  (177) and `telemetryRecordFailure` (191) use `defer`. Deleting from a map while ranging over it
  (137-141) is defined behaviour in Go. **`telemetryThrottled` does hold the correct lock
  (`telemetryFailuresMu`) for its delete at 184** — the two maps are never held simultaneously, so
  there is no lock-ordering hazard either.
  - One hardening note: `recover.New()` is installed at `api/api.go:65`. A panic inside a
    bare-`Unlock` critical section would be recovered into a 500 and leave the mutex locked forever,
    deadlocking all telemetry auth until restart. Nothing in those three regions can realistically
    panic, but `defer` costs nothing — use it for all five.
- **Session map growth is bounded.** `newTelemetrySession` sweeps under the lock on every mint
  (137-141), and entries are only created by a *successful* login, so the map is bounded by
  successful logins in a 12h window. A single login followed by silence leaves one stale entry until
  the next mint — harmless.
- **No secrets in logs.** Every `logr` call in the new code checked: 221 (address), 235 (attempted
  username + address), 242 (`rand.Read` error), 255 (username + address). The password is never
  formatted, the session token is never formatted, and `TelemetryProxy:704` logs
  `c.Locals("telemetryUser")` (a username) rather than the token. One soft spot worth knowing: line
  235 logs the submitted username verbatim, so an operator who types their password into the
  Operator field puts it in the log. Not worth changing.
- **Removing `WWW-Authenticate` does not break `curl -u`.** curl sends `Authorization: Basic`
  preemptively on the first request; it only waits for a challenge with `--anyauth`, `--digest`,
  `--negotiate` or `--ntlm`. The documented command at `docs/ll-telemetry.md:86` still works. Two
  consequences worth accepting knowingly: `curl --anyauth -u ...` now 401s forever, and a direct
  browser navigation to `/api/v1/telemetry/console` renders raw JSON instead of prompting. Both are
  intended per the comment at 467-469.
- **Cookie clearing on logout works.** `MaxAge: -1` is not `> 0`, so fasthttp falls through to the
  past `Expires` and emits a deletion cookie. Path/HttpOnly/SameSite match the login cookie, which
  is what the browser requires to overwrite it.

## One deployment note, not a code defect

`Secure: c.Protocol() == "https"` (252, 268). With no trusted-proxy configuration, fiber's
`IsProxyTrusted()` returns true by default, so `Protocol()` honours `X-Forwarded-Proto` from any
client — harmless, since it only affects the caller's own cookie. The real consequence is the
current topology: docker-compose publishes plain `8080`, so the session cookie is issued without
`Secure` and rides the LAN in cleartext for 12 hours. `HttpOnly` + `SameSite=Strict` + the
`Path` scoping are doing real work, but none of them help against a passive listener. Worth a line
in the docs if TLS is not going in front.
