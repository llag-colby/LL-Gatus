# Security review — telemetry form login + session cookie

Scope: `api/telemetry.go`, `web/app/src/views/TelemetryConsole.vue`, with route wiring from
`api/api.go:231-235` and the deployment shape from `docker-compose.yml`.

Context that sets severity: this gate is the only authentication in front of an API that has
none of its own, covering both reads (hostnames, serials, MACs, AD domain, internal IPs,
technician names, full PowerShell transcripts) and ingest-key mint/rotate/revoke/DELETE.
Gatus is published on `:8080` over plain HTTP with no TLS terminator (`docker-compose.yml`
`ports: "8080:8080"`, no proxy service in the file).

Verdict on the eight questions: **one HIGH, two MEDIUM (one of them latent), the rest either
sound or low.** Three of the concerns in the brief are refuted outright — the token error path,
the login-CSRF exposure, and the "session survives a credential change" premise.

---

## F1 [HIGH] The throttle covers the form only — the retained Basic fallback is unthrottled

`api/telemetry.go:464-473` (gate's Basic branch) vs `api/telemetry.go:219-225` (login's throttle).

`telemetryThrottled` / `telemetryRecordFailure` are called from `TelemetryLogin` and nowhere
else. `TelemetryGate`'s Basic-auth branch calls `telemetryAuthorized` → `cfg.authenticate`
with no counter, no backoff and no lockout.

**Exploit path.** Skip the form entirely:

```
for p in $(cat wordlist); do
  curl -s -o /dev/null -w '%{http_code}\n' -u llops:$p \
    http://gatus:8080/api/v1/telemetry/health
done
```

A `GET` short-circuits `telemetrySameOriginWrite` at `:413` before any header is inspected, so
no `Sec-Fetch-Site` or `Origin` is needed. `telemetryThrottled` is never consulted on this
path. Attempts are unlimited regardless of how many the form has already burned.

**Second effect — the bcrypt DoS the code tries to prevent.** The comment at `:478-482`
identifies exactly this risk and the cache at `:483-502` only closes half of it: `:491-493`
returns before the cache is written, so **only successes are cached** (as `:482` states). Every
wrong password therefore pays a full bcrypt derivation. At cost 10 that is ~50-100ms of CPU per
guess; a few dozen parallel connections saturate every core on the box, and Gatus's own check
scheduler shares that CPU, so the monitoring goes soft while the attack runs. This is
unauthenticated and pre-auth.

**Worse on the plaintext path.** If `TELEMETRY_UI_PASSWORD` is used instead of the bcrypt hash,
`:396` is a `subtle.ConstantTimeCompare` with no work factor at all — guess rate is bounded only
by the network. `.env.example:21` ships `TELEMETRY_UI_PASSWORD_BCRYPT=` **empty**, so the
plaintext fallback at `.env.example:23` is the path of least resistance for whoever fills it in.

**This is a regression in coverage, not a new bug.** `.tk/agents/reviewer-2-security.md`
finding #3 flagged "no throttling on the only authentication boundary". The new code adds a
throttle that makes it look addressed while leaving the cheaper channel wide open.

**Fix.** Hoist the throttle into `TelemetryGate` so it wraps both credential paths, not just the
form:

```go
// in TelemetryGate, immediately before the Basic branch at :464
address := c.IP()
if telemetryThrottled(address) {
    return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "Too many failed sign-ins."})
}
header := c.Get(fiber.HeaderAuthorization)
user, password, ok := parseBasicAuthHeader(header)
if !ok || !telemetryAuthorized(cfg, header, user, password) {
    telemetryRecordFailure(address)   // <- currently missing
    return c.Status(fiber.StatusUnauthorized).JSON(...)
}
telemetryClearFailures(address)
```

Then `TelemetryLogin` can keep its own check or defer to the gate's. Separately: delete
`TELEMETRY_UI_PASSWORD` from `.env.example` and make the bcrypt hash mandatory, so the
no-work-factor path cannot be selected by accident.

**Throttle key — not attacker-controlled, verified.** `api/api.go:49-57` sets no `ProxyHeader`
and no `EnableTrustedProxyCheck`, so Fiber's `c.IP()` returns the real TCP peer
(`fasthttp.RemoteIP()`) and ignores `X-Forwarded-For`. A remote attacker cannot forge the bucket
key or use it to lock out an operator from a different address. Two residual notes: (a) requests
originating on the Docker host itself collapse to the bridge gateway IP and share one bucket
with each other; (b) `telemetryRecordFailure` at `:194-199` never extends `since`, so lockout is
capped at `telemetryFailureReset` (5 min) — correct, and it means the throttle cannot be turned
into a persistent denial of service against a legitimate operator. Both are fine as designed.

---

## F2 [MEDIUM] The session cookie crosses the LAN in cleartext, and `Secure` is derived from an attacker-supplied header

`api/telemetry.go:252` (`Secure: c.Protocol() == "https"`), same at `:268` on logout.

**In this deployment `Secure` is always false.** `docker-compose.yml` publishes `8080:8080` with
no TLS anywhere in the file, so `c.Protocol()` returns `"http"` and the flag is off.

**Concrete risk.** The cookie is a 12-hour bearer token (`:80`) for an API that exposes machine
transcripts and ingest-key mint/rotate/revoke/DELETE, and it is transmitted on every request the
console makes — which per the comment at `:480-481` is three endpoints every five seconds. Anyone
with a passive position on the same L2 segment (span port, ARP spoof, shared corporate Wi-Fi,
compromised switch) captures it within seconds of an operator opening the console and replays it
for 12 hours from any host. `HttpOnly` and `SameSite=Strict` are irrelevant to a network
attacker; they defend against script and cross-site, not against the wire.

**Is this a regression from Basic auth?** Basic was equally cleartext, so the exposure channel is
not new. What is new is that the captured artifact is now a **long-lived, independently
replayable token** rather than credentials that at least an operator could rotate — and there is
no way to revoke it short of restarting Gatus (see F6). Net: not a regression in confidentiality,
a regression in blast radius per capture.

**Second defect — the flag is influenced by the client.** With `EnableTrustedProxyCheck` unset
(`api/api.go:49-57`), Fiber v2's `IsProxyTrusted()` returns true, so `c.Protocol()` honours a
client-supplied `X-Forwarded-Proto`. Any caller can send `X-Forwarded-Proto: https` and get
`Secure: true` on their own cookie. The practical effect is self-inflicted (the browser then
refuses to store a `Secure` cookie delivered over `http://`, so login silently fails — see F8),
not an escalation. But a security flag should never be a function of an untrusted request header.

**Fix.** Either terminate TLS in front of Gatus, or set the flag from explicit configuration:

```go
Secure: telemetryCookieSecure,   // from TELEMETRY_COOKIE_SECURE, default false, documented
```

If cleartext has to stay for now, drop `telemetrySessionTTL` (`:80`) well below 12h so a
captured token has a short life, and say plainly in `docs/ll-telemetry.md` that the console must
not be used across an untrusted network.

---

## F3 [MEDIUM, latent] The session-path exemption is a suffix match, not a route match

`api/telemetry.go:455`: `if strings.HasSuffix(c.Path(), "/telemetry/session") { return c.Next() }`

This returns `c.Next()` with **no authentication of any kind**. The question asked was whether a
path can end in that string without being the login/logout route. It can — the exemption fires on
any path ending in those 18 characters, and the wildcard at `api/api.go:235`
(`telemetryRouter.All("/*", TelemetryProxy)`) is registered to catch exactly such paths.

**I traced it end to end: it is not exploitable today.** Recording why, because every step is
load-bearing:

1. Fiber v2.52.13's `c.Path()` returns `URI().PathOriginal()` — the raw bytes from the request
   line. `UnescapePath` is not set in `api/api.go:49-57`, so it defaults false and `%2f` is never
   decoded into a `/`. Percent-encoding cannot forge the suffix.
2. A path equal to `/api/v1/telemetry/session` hits `Post`/`Delete("/session")` — intended.
   Anything **longer** ending in the suffix falls through to `All("/*", TelemetryProxy)`.
3. `telemetryUpstreamPath` (`:588-630`) then requires the last segment to be one of
   `health|stats|timeline|runs|keys|revoke|rotate`, or to be the single `*` of `runs/*` or
   `keys/*`. Matching the suffix requires second-to-last == `telemetry` and last == `session`;
   no allowlist entry can produce that shape. Result: 403 at `:643`, no upstream call.

**So the allowlist — not the gate — is what stops unauthenticated access on these paths.** That
is one commit away from critical. Add any allowlist entry with a wildcard in the penultimate
position (`GET runs/*/*`, or any future `GET {resource}/{id}` where the caller picks both
segments) and `GET /api/v1/telemetry/telemetry/session` reaches the upstream with no credentials
at all. Nothing in the code or comments records that the allowlist is holding this up.

**The same mismatch already misfires in the safe direction, which proves the check disagrees with
the router.** `CaseSensitive` and `StrictRouting` are unset in `api/api.go:49-57`, so Fiber routes
on a lowercased, trailing-slash-trimmed path while `HasSuffix` is case- and slash-sensitive.
`POST /api/v1/telemetry/session/` and `POST /api/v1/telemetry/SESSION` both route to
`TelemetryLogin` but fail the suffix test and get 401 — a login endpoint reachable only when
already authenticated. Harmless, but it is the identical bug pointing the other way.

**Fix (robust).** Stop exempting inside the gate; register the unauthenticated routes outside the
gated group so no string comparison decides who skips authentication:

```go
// api/api.go — before the gated group
protectedAPIRouter.Post("/v1/telemetry/session", TelemetryLogin)
protectedAPIRouter.Delete("/v1/telemetry/session", TelemetryLogout)
telemetryRouter := protectedAPIRouter.Group("/v1/telemetry", TelemetryGate)
```

`TelemetryLogin` already carries its own not-configured check (`:212-218`); give both handlers
the `telemetrySameOriginWrite` call so the CSRF property in F7 is preserved. If the gate must
keep the exemption, make it exact and method-bound:

```go
if c.Path() == telemetrySessionPath+"/session" &&
   (c.Method() == fiber.MethodPost || c.Method() == fiber.MethodDelete) {
    return c.Next()
}
```

The same suffix pattern at `:437` (`"/telemetry/console"`) deserves the same treatment; it only
serves a static notice, so it is informational.

---

## F4 [LOW] Session token generation and handling — verified sound, no action needed

`api/telemetry.go:129-145`.

- **Strength.** 32 bytes from `crypto/rand` = 256 bits, `base64.RawURLEncoding` = 43 chars. Ample.
- **Error path is safe, with two independent guards.** `:131-133` returns `("", err)`;
  `TelemetryLogin` returns 500 at `:243` **before** reaching `c.Cookie(...)` at `:246`, so a
  failed draw can never set a cookie. Even if an empty value reached a cookie by some other
  route, `telemetrySessionUser` rejects `""` at `:148-150` before touching the map. No empty or
  partially-populated token is ever usable. **Refutes the concern in the brief.**
- **Never logged.** `:221`, `:235`, `:242`, `:255` log the address and username only.
  `TelemetryProxy:704` logs `c.Locals("telemetryUser")`, the username set at `:461`. No statement
  in the file takes the token, the cookie value, or the raw `Authorization` header. Confirmed.
- Nit, not a finding: the lookup at `:153` is a plain map index and so not constant-time. Against
  a 256-bit random token with no partial-match oracle this is not exploitable — noted only so it
  is not "discovered" as a bug later.

---

## F5 [LOW] `SameSite=Strict` is the correct choice here — verified, no change

The console is served by Gatus itself at `/api/v1/telemetry/console` and framed by the SPA
(`TelemetryConsole.vue:109-114`), so the iframe is **same-origin**. Its fetches are same-site and
carry the cookie; `Strict` blocks only cross-site requests, which is precisely the desired
behaviour. `frame-ancestors 'self'` at `:312` keeps it that way.

One consequence worth stating rather than fixing: `Strict` withholds the cookie on a top-level
cross-site *navigation* into `/ll-telemetry` (following a link from a wiki or Teams). This does
not break anything, because the SPA's `probe()` XHR (`TelemetryConsole.vue:172`) is same-site and
does carry it, so the operator lands on the console rather than the login form. Cookie
`Path=/api/v1/telemetry` (`:79`, `:249`) correctly covers login, logout, the console document and
every proxied fetch, and nothing else on the origin ever receives it.

---

## F6 [LOW] Session lifecycle — correct, except there is no revocation

`api/telemetry.go:129-171`.

- **No fixation.** The token is minted only after `cfg.authenticate` succeeds (`:233` → `:240`).
  There is no pre-auth session and no path by which a caller supplies their own token — the value
  is always server-generated. Confirmed clean.
- **Logout invalidates server-side**, not just in the browser: `:261` → `dropTelemetrySession`
  deletes the map entry at `:164-171`. The SPA's `DELETE` (`TelemetryConsole.vue:224`) is a
  same-origin `fetch`, which defaults to `credentials: 'same-origin'`, so the cookie rides along
  and the correct session is dropped. Correct.
- **Expiry is enforced on read** at `:157-160`, with deletion; the sweep at `:137-141` bounds map
  growth. Correct.
- **Credential change — the brief's premise is refuted.** `telemetryCfg()` is `sync.Once`-guarded
  (`:359`) over `os.Getenv`, so the credentials cannot change in a running process. A `.env` edit
  therefore requires a container restart (`docker-compose.yml` `env_file`), and the session map at
  `:111` is process-local memory that a restart wipes. There is **no** window in which a session
  minted under the old password survives the new one. Nothing to fix.
- **The real gap is the inverse: sessions cannot be revoked.** If an operator's laptop is lost, or
  a token is sniffed off the wire per F2, the only remedy is restarting Gatus — which also drops
  every SSE wallboard, the exact outcome the design notes at `api/api.go:222-227` were written to
  avoid. Either document "restart Gatus to revoke telemetry sessions" in `docs/ll-telemetry.md`,
  or add a small admin action that clears `telemetrySessions`.
- **12h is long for an absolute TTL with no idle timeout and no rotation** (`:80`). An unlocked
  browser on an operator's desk holds a live session to transcripts and key management for a full
  working day. 2-4h with re-auth would cost little given the login screen is now pleasant.

---

## F7 [LOW] CSRF — ordering confirmed correct; no login CSRF, no forced logout

**Ordering is right, and it is what the code does.** `telemetrySameOriginWrite` runs at `:449`,
**before** the session-path exemption at `:455`. Both `POST /session` and `DELETE /session` are
therefore covered by the CSRF check. Confirmed as claimed.

- **Login CSRF: blocked.** An attacker page doing a cross-site form POST or
  `fetch('/api/v1/telemetry/session', {method:'POST'})` produces `Sec-Fetch-Site: cross-site`,
  which fails `:417` and gets 403 at `:450`. On a browser too old to send `Sec-Fetch-*`
  (Safari < 16.4), the `Origin` fallback at `:419-425` catches it, since every such browser sends
  `Origin` on cross-origin POSTs. `Origin: null` (sandboxed iframe, `data:` URL) parses to an
  empty `Host` and is refused. An attacker cannot plant their own session in a victim's browser.
- **Forced logout: blocked.** `DELETE` cannot be issued by an HTML form, and a cross-origin
  `fetch` DELETE is preflighted. The `OPTIONS` preflight is itself non-GET, so it hits `:449`,
  is refused, and returns no CORS headers — the real DELETE never fires.
- **Residual, deliberate, acceptable:** `telemetrySameOriginWrite` returns true when neither
  header is present (`:426`) and for `Sec-Fetch-Site: none` (`:417`). Both are documented at
  `:410-411` and neither is producible from a cross-site browser context. The only clients they
  exempt are non-browsers, which must still supply credentials. No change needed.
- Note for whoever maintains this: the comment at `:403-406` justifies the check on the grounds
  that "Basic credentials have no SameSite". Still true, but the cookie is now the primary
  credential and it *is* `SameSite=Strict`, so this check is now belt-and-braces for sessions and
  load-bearing only for the Basic fallback. Keep it — F1 shows the Basic fallback is very much
  still live.

---

## F8 [LOW] The Vue view leaks nothing and cannot be XSS'd; two small robustness issues

`web/app/src/views/TelemetryConsole.vue`.

- **Password handling is clean — refutes the concern.** `v-model="password"` on a
  `type="password"` input (`:38-45`) binds a plain `ref` (`:138`). It is sent only in a JSON
  **request body** on POST (`:197-201`) — never in a URL or query string. There is no
  `localStorage`, `sessionStorage`, or `document.cookie` access anywhere in the file, and no
  `console.*` call at all, so it cannot reach a log. It is cleared on success (`:204`) and on
  sign-out (`:227`). Not clearing it on failure is correct UX (lets the operator fix a typo) and
  costs nothing, since the value is already in the DOM node either way.
- **Error rendering is safe — refutes the concern.**
  `<p v-if="error" class="tel-error">{{ error }}</p>` (`:47`) is mustache interpolation, which Vue
  escapes; there is no `v-html` anywhere in the file. So even though `error` is assigned
  server-supplied `payload.error` (`:212`), markup cannot be injected. Belt-and-braces: every
  error string the gate can emit (`:216`, `:223`, `:231`, `:237`, `:243`, `:451`, `:471`) is a
  compile-time constant with no attacker-influenced substitution — unlike `TelemetryProxy`, which
  deliberately refuses to echo upstream bodies (`:735-736`).
- **[LOW] `probe()` treats every unrecognised status as `ready`** (`:169-190`, else-branch at
  `:183-186`). A 403 from the same-origin check or a 429 from the throttle renders the console
  iframe, so the operator sees an empty, silently-failing console with no explanation. Add
  explicit 403 and 429 branches.
- **[LOW] `signIn` never confirms the cookie was stored.** On `response.ok` it sets a 450ms timer
  (`:206`) and re-probes. If the browser refused the cookie, `probe()` returns 401 and the user
  bounces back to the login form having just been told they succeeded — an unexplained loop. This
  is exactly the failure mode F2's `X-Forwarded-Proto` issue produces. Surface a distinct message
  when a successful login is followed by a 401 probe.

---

## F9 [INFO] Removing `WWW-Authenticate` weakened nothing

`api/telemetry.go:467-472`.

Dropping the challenge header removes only the browser's native dialog. It does not change what
the server *accepts*: `parseBasicAuthHeader` (`:504-518`) still parses any `Authorization: Basic`
sent preemptively, which is what `curl -u` does by default. The scripted fallback is intact
(F1 demonstrates it rather too well).

If anything it is a marginal improvement: with no challenge, a browser never caches Basic
credentials for the origin and never auto-attaches them to later requests — which removes the
precise "Basic credentials have no SameSite" exposure that motivated the CSRF check at `:403-406`.

The only casualty is a client that waits for a 401 challenge before authenticating
(`curl --anyauth`, HTTP libraries configured for lazy auth). That is functional, not security —
worth one line in `docs/ll-telemetry.md` telling script authors to send credentials preemptively.

---

## Priority

1. **F1** — add the throttle and failure recording to the Basic branch of `TelemetryGate`, and
   drop `TELEMETRY_UI_PASSWORD` from `.env.example`. This is the finding that matters.
2. **F3** — move login/logout out of the gated group so no suffix comparison decides who skips
   authentication. Not exploitable today; the allowlist is the only thing holding it, and nothing
   records that.
3. **F2** — TLS in front of Gatus, or a config-driven `Secure` flag plus a shorter TTL.
4. F6 (revocation story, TTL), F8 (`probe()` status handling) as cleanup.
