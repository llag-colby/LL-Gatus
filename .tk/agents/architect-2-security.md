# architect-2-security: SECURITY-FIRST architecture for LL-Telemetry inside Gatus

APPROACH: Security-first. Deny by default at every layer, fail closed on misconfiguration,
and make the dangerous operations (ingest-key mutation, especially hard DELETE) require a
deliberate, attributable, logged act rather than one click.

The four decided points are taken as given. Everything below is the safest correct version
of *that* design, not an argument against it.

---

## 0. The threat model this design is actually defending against

Three attackers, in descending order of likelihood:

1. **Anyone on the LAN.** Gatus today is a wide-open `8080:8080` with no `security:` block.
   Bolting a console that reads full PowerShell transcripts, serials, MACs, AD domain and
   technician identity onto that origin, and that can delete ingest keys, turns a read-only
   status board into a credentialed admin surface. This is the primary risk and it is
   created by the integration itself.
2. **A field machine holding an ingest key.** Every string in `runs` (`hostname`, `script`,
   `tech`, `site`, `details`, and 300 KB of `log`) is attacker-controlled input written by
   whoever holds a `llk_` token. Once the console is same-origin with Gatus, a stored XSS in
   any of those fields executes against the Gatus session, which now controls key
   mint/rotate/revoke/DELETE. That is an ingest-key to full-console privilege escalation.
3. **A container or process that lands on the docker network.** `lltel-api` has zero
   application-level auth on every read route and all five key routes
   (`main.py:499,516,535,548,570`). Caddy is the only boundary in prod. Move the stack next
   to Gatus without adding auth to the API and the compose network becomes the boundary,
   which is a much weaker one.

The design has to close all three. Auth (section 1) closes #1, CSP + escaping (section 7)
closes #2, upstream token + network scoping (sections 3 and 5) close #3.

---

## 1. THE AUTH DESIGN

### 1.1 Recommendation

**Ship Basic now. Move to OIDC before the Keys panel is considered production-trusted.**

Rationale, concretely:

| | Basic | OIDC |
|---|---|---|
| External dependency | none | needs Entra/Okta reachable from the Gatus box |
| Iframe subresource auth | works (see 1.4) | works (see 1.4) |
| Session expiry | never (browser caches for tab lifetime) | 8 h default, `sessions.SetWithTTL` |
| Logout / revocation | none, short of closing the browser | revoke the session, revoke the IdP grant |
| **Identity for the key audit log** | **one shared username** | **per-user `sub`** |
| Credential on the wire | base64, replayed on every request | opaque UUID cookie |

The deciding factor is the last-but-one row. `DELETE /api/v1/keys/{id}` is a hard delete
that breaks ingest for every field script stamped with that batch key. An audit record
that says `basic:admin` is not an audit record, it is a timestamp. Under OIDC the same
record says `oidc:cwest@longlewis.com`.

So: Basic unblocks the build and is a genuine improvement over the current nothing. But
treat "Keys panel + Basic" as a temporary state, and make the audit log record `basic:` vs
`oidc:` explicitly so the gap is visible in the log itself rather than assumed.

### 1.2 `config.yaml`

```yaml
security:
  basic:
    username: "llops"
    # base64url(bcrypt hash). MUST be base64url with padding: security/config.go:72
    # uses base64.URLEncoding, not StdEncoding. A '+' or '/' from standard base64
    # makes DecodeString error, which is returned from ApplySecurityMiddleware and
    # panicked at api.go:173. That is a loud failure, which is the correct behaviour.
    password-bcrypt-base64: ${GATUS_BASIC_BCRYPT_B64}
```

And the OIDC end-state, for reference:

```yaml
security:
  oidc:
    issuer-url: "https://login.microsoftonline.com/<tenant-id>/v2.0"
    redirect-url: "https://gatus.longlewis.local/authorization-code/callback"  # suffix is validated
    client-id: ${GATUS_OIDC_CLIENT_ID}
    client-secret: ${GATUS_OIDC_CLIENT_SECRET}
    scopes: ["openid", "email"]
    allowed-subjects:
      - "cwest@longlewis.com"
    session-ttl: 8h
```

`allowed-subjects` is not optional here. `oidc.go:122-127`: an empty list means **every
subject in the tenant** is allowed. For a console that can delete ingest keys, populate it.

### 1.3 Generating the hash and supplying the credential

The hash never goes in `config.yaml` literally, for two reasons: it is a credential, and
`config.go:287` runs `os.ExpandEnv` over the whole YAML before parsing, so a raw bcrypt
hash's `$2y$12$...` would be mangled into nothing. The base64url wrapper exists precisely
to avoid `$`, and the env indirection keeps it out of git.

```bash
# 1. bcrypt, cost 12, no username prefix
docker run --rm httpd:2.4-alpine htpasswd -bnBC 12 "" 'THE-PASSWORD' | tr -d ':\n'
#    -> $2y$12$8kQ....   (x/crypto/bcrypt's decodeVersion accepts any minor, $2y is fine)

# 2. base64url, WITH padding (URLEncoding, not RawURLEncoding)
docker run --rm httpd:2.4-alpine htpasswd -bnBC 12 "" 'THE-PASSWORD' \
  | tr -d ':\n' \
  | python -c "import base64,sys; print(base64.urlsafe_b64encode(sys.stdin.buffer.read()).decode())"
```

Put the result in `.env` (already gitignored, already `env_file:`d into the gatus service):

```dotenv
GATUS_BASIC_BCRYPT_B64=JDJ5JDEyJDhrUS4uLg==
```

`docker-compose.yml` already has `env_file: - .env` on the `gatus` service, so
`os.ExpandEnv` picks it up with no compose change.

**Fail-closed check, verified.** If `GATUS_BASIC_BCRYPT_B64` is unset, `os.ExpandEnv`
substitutes empty string, `BasicConfig.isValid()` returns false (`basic.go:15` requires both
fields non-empty), `ValidateSecurityConfig` returns `ErrInvalidSecurityConfig`
(`config.go:579-581`), and Gatus refuses to start. Good. That is the behaviour we want and
it should be asserted by a test so nobody "fixes" it later.

**Latent defect to patch anyway** (`security/config.go:79-84`):

```go
Authorizer: func(username, password string) bool {
    if len(c.Basic.PasswordBcryptHashBase64Encoded) > 0 {
        if username != c.Basic.Username || bcrypt.CompareHashAndPassword(...) != nil {
            return false
        }
    }
    return true          // <-- empty hash accepts ANY username and ANY password
},
```

Today only the YAML validator stands between this and a silent total bypass. Given we are
about to put key deletion behind it, make the middleware itself fail closed:

```go
} else if c.Basic != nil {
    if len(c.Basic.PasswordBcryptHashBase64Encoded) == 0 {
        return errors.New("security.basic: password-bcrypt-base64 is required")
    }
    ...
    Authorizer: func(username, password string) bool {
        if subtle.ConstantTimeCompare([]byte(username), []byte(c.Basic.Username)) != 1 {
            // still run bcrypt so a wrong username and a wrong password cost the same
            _ = bcrypt.CompareHashAndPassword(decodedBcryptHash, []byte(password))
            return false
        }
        return bcrypt.CompareHashAndPassword(decodedBcryptHash, []byte(password)) == nil
    },
```

### 1.4 How the iframe's subresource fetches stay authenticated

This is the part that decides whether the whole design works, so it is worth being precise.

The iframe is **same-origin** (`/telemetry/console` on the Gatus origin), and the console's
own fetches go to `/api/v1/telemetry/*` on that same origin. So:

**Basic.** After the browser has been challenged once for the origin, it preemptively
attaches `Authorization: Basic ...` to every subsequent same-origin request, including
requests issued from inside a same-origin iframe. `fetch()` default `credentials:
"same-origin"` does not suppress this. So the subresource fetches are authenticated with no
code change and no second prompt. Confirmed by the way the console already works behind
Caddy's `basic_auth`, which is the identical mechanism.

**OIDC.** The gate reads the `gatus_session` cookie (`config.go:57-63`), set with
`Path: "/"` and `SameSite=Strict` (`oidc.go:143-149`). A request from a same-origin iframe
to the same origin is same-site by definition, so Strict does not block it. Cookie is sent,
gate passes. Also works with no code change, provided the console's fetches use
`credentials: "same-origin"` (the default) rather than `"omit"`. They do.

**Where each scheme breaks: the auth challenge must never happen inside the iframe.**

This is the real failure mode, and it is shared by both schemes.

- *Basic:* Gatus's SPA shell at `/` is unprotected (`api.go:134`), so a user can be sitting
  on the dashboard having never been challenged. They click the telemetry button, the
  iframe requests `/telemetry/console`, gets a 401 with `WWW-Authenticate: Basic`, and
  Chrome's handling of auth dialogs originating from a subframe is inconsistent and
  restricted. Best case an ugly dialog inside the iframe; worst case a silent blank frame.
  Then, separately, once the document is loaded, if the credential is ever dropped the
  console's `fetch` gets a bare 401 which `api()` at `index.html:333` turns into
  `"/runs → 401"` rendered as an error string, with no re-auth path at all.
- *OIDC:* worse. A 401 from the g8 gate is JSON, not a redirect. And if you make it a
  redirect to `/oidc/login`, that bounces to the IdP, which sends `X-Frame-Options: DENY`
  and blanks the iframe.

**Mandated mitigation, both schemes.** The Vue view probes before it mounts the iframe, and
any challenge is escalated to a top-level navigation:

```js
// web/app/src/views/TelemetryConsole.vue
const state = ref('probing');   // probing | ready | needs-auth | down
onMounted(async () => {
  try {
    const r = await fetch('/api/v1/telemetry/health', { credentials: 'same-origin' });
    if (r.status === 401 || r.status === 403) { state.value = 'needs-auth'; return; }
    state.value = r.ok ? 'ready' : 'down';
  } catch { state.value = 'down'; }
});
// needs-auth renders a button, NOT an iframe:
//   Basic -> window.location.href = '/telemetry/console?return=/telemetry'
//   OIDC  -> window.location.href = '/oidc/login'
// Both are top-level navigations, so the dialog / IdP redirect happens in the top frame.
```

And the console itself must escalate mid-session expiry the same way. One addition to
`index.html` (this is the only functional change the console needs beyond the API base):

```js
async function api(p){
  const r = await fetch(API+p,{headers:{accept:'application/json'},credentials:'same-origin'});
  if(r.status===401||r.status===403){ parent.postMessage({type:'lltel-auth-required'},location.origin); throw new Error('session expired'); }
  if(!r.ok) throw new Error(p+' → '+r.status);
  return r.json();
}
```

with the Vue parent listening for that message (validating `event.origin === location.origin`)
and flipping to `needs-auth`. Under Basic this fires ~never; under OIDC it fires every 8 h,
which is exactly when you want it.

**Verdict: neither scheme double-prompts, and neither breaks, provided the challenge is
never allowed to originate inside the iframe.** That is a hard requirement, not a polish item.

### 1.5 Positional protection: where the routes must be registered

`api/api.go` protects by *position*: `protectedAPIRouter` (line 167) gets the middleware,
`unprotectedAPIRouter` (line 86) does not, and both are `apiRouter.Group("/")`. So:

- Every telemetry API route goes on `protectedAPIRouter`, registered **after** line 175.
- The console *document* needs a non-`/api` URL for the iframe, so it needs its own
  protected group. Register it before the `fiberfs` catch-all at line 158 to avoid relying
  on the filesystem middleware's not-found fallthrough.

```go
// api/api.go, inserted after the SPA routes (~line 138)

// Telemetry console. FAIL CLOSED: if there is no security config, none of this
// is registered at all. An unauthenticated telemetry console is worse than no
// telemetry console.
if cfg.Security == nil {
    logr.Warnf("[api.createRouter] telemetry console DISABLED: no `security:` block in config")
} else {
    tp, err := NewTelemetryProxy(cfg)
    if err != nil {
        panic(err)   // bad TELEMETRY_API_BASE or missing LL_CONSOLE_TOKEN
    }
    consoleRouter := app.Group("/telemetry")
    if err := cfg.Security.ApplySecurityMiddleware(consoleRouter); err != nil {
        panic(err)
    }
    consoleRouter.Get("/console", TelemetryConsole)   // the iframe document
    a.telemetry = tp                                   // wired onto protectedAPIRouter below
}
...
// and, after line 175, on protectedAPIRouter:
if a.telemetry != nil {
    protectedAPIRouter.Get("/v1/telemetry/*", a.telemetry.Handle)
    protectedAPIRouter.Post("/v1/telemetry/*", a.telemetry.Handle)
    protectedAPIRouter.Delete("/v1/telemetry/*", a.telemetry.Handle)
}
// SPA route so the Vue view deep-links:
app.Get("/telemetry", SinglePageApplication(cfg.UI))
```

Note that `ApplySecurityMiddleware` is called twice, once per group. Under Basic that is two
independent `basicauth.New` instances, which is harmless. Under OIDC it builds a second
`g8.Gate` and overwrites `c.gate`, which is also harmless because both gates read the same
`sessions` store, but `IsAuthenticated` then reflects the second gate. Acceptable; note it
so nobody is surprised.

---

## 2. PROXY ALLOWLIST DESIGN

### 2.1 Rejected outright: `httputil.ReverseProxy`

A `ReverseProxy` with a `Director` is the obvious implementation and it is the wrong one.
Its default behaviour forwards the inbound header set (including the Gatus `Authorization`
and `Cookie`), copies the inbound path with no validation, follows the transport's redirect
policy, and streams upstream headers back. Every one of those is a hole here. Build the
outbound `http.Request` from scratch.

### 2.2 The allowlist

```go
// api/telemetry.go

type queryRule struct {
    Pattern *regexp.Regexp // anchored value pattern
    MaxLen  int
}

type allowedRoute struct {
    Method   string
    Pattern  *regexp.Regexp        // anchored, matched against the CLEANED path
    Query    map[string]queryRule  // params not listed here are DROPPED, not passed
    MaxBody  int                   // 0 = body must be absent
    Mutation bool                  // key mutations: extra gate, audit, rate limit
}

var reRunID = `[0-9a-fA-F][0-9a-fA-F-]{0,35}`
var reKeyID = `[1-9][0-9]{0,8}`

var telemetryRoutes = []allowedRoute{
 {"GET",    regexp.MustCompile(`^/api/v1/health$`),                        nil, 0, false},
 {"GET",    regexp.MustCompile(`^/api/v1/runs$`),                          runsQuery, 0, false},
 {"GET",    regexp.MustCompile(`^/api/v1/runs/`+reRunID+`$`),              nil, 0, false},
 {"GET",    regexp.MustCompile(`^/api/v1/stats$`),                         daysQuery, 0, false},
 {"GET",    regexp.MustCompile(`^/api/v1/timeline$`),                      timelineQuery, 0, false},
 {"GET",    regexp.MustCompile(`^/api/v1/keys$`),                          nil, 0, false},
 {"POST",   regexp.MustCompile(`^/api/v1/keys$`),                          nil, 4096, true},
 {"POST",   regexp.MustCompile(`^/api/v1/keys/`+reKeyID+`/revoke$`),       nil, 0, true},
 {"POST",   regexp.MustCompile(`^/api/v1/keys/`+reKeyID+`/rotate$`),       nil, 0, true},
 {"DELETE", regexp.MustCompile(`^/api/v1/keys/`+reKeyID+`$`),              nil, 0, true},
}

var runsQuery = map[string]queryRule{
  "limit":    {regexp.MustCompile(`^[0-9]{1,4}$`), 4},
  "offset":   {regexp.MustCompile(`^[0-9]{1,7}$`), 7},
  "days":     {regexp.MustCompile(`^[0-9]{1,3}$`), 3},
  "script":   {regexp.MustCompile(`^[A-Za-z0-9._ -]{1,64}$`), 64},
  "result":   {regexp.MustCompile(`^[A-Z_]{1,32}$`), 32},
  "results":  {regexp.MustCompile(`^[A-Z_,]{1,128}$`), 128},
  "site":     {regexp.MustCompile(`^[A-Za-z0-9./ -]{1,48}$`), 48},
  "hostname": {regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`), 64},
  "q":        {regexp.MustCompile(`^[^\x00-\x1f%]{1,120}$`), 120},
}
```

**`POST /api/v1/runs` is deliberately absent and must stay absent.** Proxying ingest would
(a) let anyone with a Gatus session forge telemetry, (b) require the proxy to accept
arbitrary 2 MB bodies and a client-supplied `Authorization` header, and (c) put an ingest
credential path through the console origin. Ingest stays direct to the telemetry API via
Caddy on the prod box. This is a hard no.

### 2.3 Path handling: traversal, encoded slashes, smuggling

Deny by default, on a normalized string, with a charset so narrow that the classic bypasses
have nothing to work with.

```go
func (p *telemetryProxy) resolvePath(c *fiber.Ctx) (string, *allowedRoute, error) {
    raw := c.Params("*")               // safe to retain: fiber Config.Immutable = true (api.go:56)

    // 1. Reject percent-encoding entirely. No allowlisted path needs it, so this
    //    kills %2e%2e%2f, %2f, %5c, %00, %0d%0a in one rule with no decode-order bugs.
    if strings.ContainsRune(raw, '%') {
        return "", nil, errDenied
    }
    // 2. Reject anything outside the alphabet the allowlist can match.
    for i := 0; i < len(raw); i++ {
        ch := raw[i]
        ok := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
              (ch >= '0' && ch <= '9') || ch == '/' || ch == '-' || ch == '_' || ch == '.'
        if !ok {
            return "", nil, errDenied      // kills \ : @ ? # space CR LF NUL and all UTF-8
        }
    }
    // 3. Structural rejects. '..' anywhere, empty segments, leading dot-segments.
    if strings.Contains(raw, "..") || strings.Contains(raw, "//") {
        return "", nil, errDenied
    }
    full := "/api/v1/" + strings.TrimPrefix(raw, "/")
    // 4. path.Clean must be an identity transform. If it changes anything, the input
    //    was not already canonical, so refuse rather than proxy the cleaned version.
    if path.Clean(full) != full {
        return "", nil, errDenied
    }
    // 5. Allowlist match.
    for i := range telemetryRoutes {
        r := &telemetryRoutes[i]
        if r.Method == c.Method() && r.Pattern.MatchString(full) {
            return full, r, nil
        }
    }
    return "", nil, errDenied
}
```

Rule 1 plus rule 2 together mean an absolute URL (`http://evil/`), an encoded slash, a
scheme-relative `//evil/`, a backslash, a CR/LF header-injection attempt, and a null byte
are all rejected before any parsing happens. Rule 4 means we never "helpfully" normalize on
the attacker's behalf, which is the source of most proxy-normalization divergences.

**Building the outbound URL** uses structured fields, never string concatenation:

```go
u := &url.URL{
    Scheme:   p.base.Scheme,       // fixed at startup
    Host:     p.base.Host,         // fixed at startup, e.g. "lltel-api:8080"
    Path:     upstreamPath,        // already proven to be canonical and in-alphabet
    RawQuery: filtered.Encode(),   // rebuilt from the allowlist, never passed through
}
```

Nothing user-supplied ever reaches `Scheme` or `Host`, so this proxy cannot be turned into
an SSRF pivot even if the path validation were bypassed.

### 2.4 Header, body, and response hygiene

```go
req, _ := http.NewRequestWithContext(ctx, route.Method, u.String(), bodyReader)
req.Header = http.Header{}                                  // start empty. Nothing inherited.
req.Header.Set("Accept", "application/json")
req.Header.Set("User-Agent", "gatus-telemetry-proxy/1")
req.Header.Set(consoleTokenHeader, p.token.reveal())        // section 3
if len(body) > 0 { req.Header.Set("Content-Type", "application/json") }
req.Host = p.base.Host
```

- **Nothing is forwarded from the client.** No `Cookie`, no `Authorization`, no
  `X-Forwarded-*`, no `Transfer-Encoding`, no `Connection`, no `Upgrade`. The Gatus Basic
  credential therefore cannot reach the telemetry API even by accident, and there is no
  hop-by-hop header passthrough for a smuggling gadget to ride on.
- **Bodies are reconstructed, not relayed.** Only `POST /keys` has one. Read at most 4 KB,
  `json.Unmarshal` into `struct{ Label string \`json:"label"\` }` with
  `DisallowUnknownFields`, validate `1 <= len(label) <= 96` and a printable-ASCII charset,
  then re-marshal. Arbitrary JSON never reaches FastAPI.
- **Redirects are never followed:**
  `Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}`,
  `Timeout: 15 * time.Second`.
- **Responses are re-headed, not copied.** Take the status code and the body; set our own
  `Content-Type: application/json`, `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`.
  Cap the body at 8 MB (`io.LimitReader`) since `GET /runs/{id}` can carry a 300 KB
  transcript and `/runs?limit=1000` is large.
- **Non-2xx bodies are dropped, not relayed.** See section 3.3.

---

## 3. CREDENTIAL HANDLING

### 3.1 Add auth to the telemetry API. Do not rely on the network alone.

`main.py` has no auth on any read route or any of the five key routes. If the only thing
standing between `lltel-api` and a key delete is "you have to be on the compose network",
then a compromised `phone-collector` or `unifi-collector` (both of which run
`python:3-slim` with bind-mounted scripts) owns the key store. Add a shared secret.

```python
# telemetry/api/main.py, near the top
import hmac
CONSOLE_TOKEN = os.environ.get("LL_CONSOLE_TOKEN", "")

@app.middleware("http")
async def require_console_token(request: Request, call_next):
    path, method = request.url.path, request.method
    # Ingest keeps its own Bearer-key auth and is NOT behind the console token.
    if path == "/api/v1/runs" and method == "POST":
        return await call_next(request)
    if path == "/api/v1/health" and method == "GET":
        return await call_next(request)
    if not CONSOLE_TOKEN:                       # fail closed, never open
        return JSONResponse(status_code=503, content={"detail": "console auth not configured"})
    if not hmac.compare_digest(request.headers.get("x-ll-console-token", ""), CONSOLE_TOKEN):
        return JSONResponse(status_code=401, content={"detail": "unauthorized"})
    return await call_next(request)
```

While in this file, fix the ingest-key comparison at `main.py:102`. It is currently
`hash_key(token) == row["key_hash"]`, a plain `==` on an unsalted sha256 hex string:

```python
if row and not row["revoked"] and hmac.compare_digest(hash_key(token), row["key_hash"]):
```

(The unsalted sha256 is defensible for a 48-hex-char random token, which has no precomputation
surface. The non-constant-time compare is not, and the fix is one line.)

### 3.2 Where the secret lives

One value, `LL_CONSOLE_TOKEN`, generated once with `openssl rand -hex 32`, stored in the
Gatus repo's existing gitignored `.env`, and injected as an env var into exactly two
containers. It is never in `config.yaml`, never in an image layer, never in a volume, never
in a URL.

```dotenv
# Gatus/.env
LL_CONSOLE_TOKEN=<openssl rand -hex 32>
TELEMETRY_API_BASE=http://lltel-api:8080
```

Gatus reads it at startup in `NewTelemetryProxy` and fails to start if it is empty or
shorter than 32 chars. Fail closed on both ends.

### 3.3 The concrete guarantee that it never leaks

Four independent mechanisms. Any one of them failing still leaves three.

**(a) A type that cannot be accidentally stringified.**

```go
// api/telemetry_secret.go
type secret string

func (secret) String() string                { return "[redacted]" }
func (secret) GoString() string              { return "[redacted]" }
func (secret) MarshalJSON() ([]byte, error)  { return []byte(`"[redacted]"`), nil }
func (secret) MarshalText() ([]byte, error)  { return []byte("[redacted]"), nil }
func (s secret) reveal() string              { return string(s) }
```

`fmt.Sprintf("%v"/"%s"/"%+v"/"%#v")`, `json.Marshal`, `logr.Errorf`, and any struct that
embeds it all produce `[redacted]`. `reveal()` is unexported and has **exactly one call
site**, the `req.Header.Set` in 2.4. Enforce that with a test:

```go
func TestSecretRevealHasExactlyOneCallSite(t *testing.T) {
    // grep the package source; fail if `.reveal()` appears more than once
}
```

**(b) The token is only ever a request header, never a URL component.** Go's `url.Error`,
returned from every `client.Do` failure, embeds the URL. Since the token is never in the
URL, transport errors cannot carry it. Rule: no query-param auth, ever.

**(c) Upstream response bodies are never echoed.** This is the direct fix for the Jira
precedent, where `api/jira.go:30` does
`c.Status(502).JSON(fiber.Map{"error": err.Error()})` and the frontend renders it in a
`<pre>`. FastAPI's own error strings are hostile to that pattern:
`main.py:273` raises `detail=f"write failed: {e}"` (raw DB exception),
`main.py:211` raises `detail=f"db unavailable: {e}"` (connection string fragments).
Replace passthrough with a static table:

```go
var upstreamMessage = map[int]string{
    400: "telemetry rejected the request",
    401: "telemetry upstream authentication failed",
    403: "telemetry upstream refused the request",
    404: "not found",
    409: "conflict",
    422: "invalid input",
    429: "telemetry upstream rate limited",
    500: "telemetry upstream error",
    503: "telemetry upstream unavailable",
}

func (p *telemetryProxy) fail(c *fiber.Ctx, status int, ref string) error {
    msg, ok := upstreamMessage[status]
    if !ok { msg = "telemetry upstream error" }
    c.Set("Cache-Control", "no-store")
    return c.Status(status).JSON(fiber.Map{"error": msg, "ref": ref})
}
```

`ref` is 8 random hex chars, printed alongside the real (redacted) detail in the Gatus log,
so an operator can correlate without the browser ever seeing upstream text.

**(d) A redaction pass on everything this package logs, and a blanket no-log rule on
`/keys` bodies.**

```go
var (
    reIngestKey = regexp.MustCompile(`llk_[0-9a-fA-F]{8}_[0-9a-fA-F]{48}`)
)

func (p *telemetryProxy) redact(s string) string {
    s = reIngestKey.ReplaceAllString(s, "llk_[redacted]")
    if t := p.token.reveal(); len(t) >= 8 {
        s = strings.ReplaceAll(s, t, "[redacted]")
    }
    return s
}
```

And the hard rule, because `POST /keys` and `POST /keys/{id}/rotate` return a freshly minted
`secret` in a 201 body: **the proxy never logs a response body for any `/keys` route, at any
log level, success or failure.** For those routes it logs status code and `ref` only. The
minted secret goes upstream-to-browser and nowhere else, with `Cache-Control: no-store` set
on the response.

---

## 4. KEYS PANEL HARDENING

DELETE stays, as decided. Here is the safest proxied form of it.

### 4.1 Server-enforced revoke-then-delete. Yes, downgrade.

The console UI already only offers `delete` on rows where `k.revoked` is true
(`index.html:594`), but the UI is not a security boundary and the proxy route is directly
reachable by any authenticated session or any XSS on the origin. Move that invariant to the
server:

Before proxying `DELETE /api/v1/keys/{id}`, Gatus performs a precondition `GET
/api/v1/keys` upstream and:

1. **404** if `{id}` is not present.
2. **409 `"key must be revoked before it can be deleted"`** if `revoked == false`.
   This is the downgrade. Deleting an *active* batch key is the failure mode that breaks
   ingest for every field script stamped with it, and it becomes structurally impossible in
   one request.
3. **409 `"key was revoked less than 24h ago; rotate instead, or wait out the grace window"`**
   if `revoked_utc` is within `graceAfterRevoke` (default 24 h, configurable via
   `TELEMETRY_KEY_DELETE_GRACE`). Rationale: revoke is the recoverable state. Once the key
   row is gone you have lost the label, the prefix, the `use_count`, and any ability to
   correlate historical `runs.key_label` back to a real key. The grace window makes
   "revoke, notice ingest broke, rotate instead" a viable recovery, which hard delete
   forecloses.
4. **409** if `use_count > 0 && last_used_utc` is within 30 days, **unless** the request
   also carries `X-LL-Force: yes-break-ingest`. A key that field scripts used last week is
   a key that field scripts are still stamped with.

Rules 2 and 3 are non-overridable. Rule 4 is overridable and the override is audited as
`"forced": true`.

### 4.2 Confirmation requirements

**Typed-label confirmation for DELETE.** The UI presents the key's `label` and requires the
operator to type it exactly. The typed value is sent as a header:

```
X-LL-Confirm: 2026-Q3 USB
```

Gatus compares it against the label it just read in the precondition GET. Mismatch is a
`422` and is audited as a denial. This is the "type the repository name to delete it"
pattern and it exists for exactly this reason: it makes a delete impossible to perform
accidentally, and it makes a *scripted* delete (from XSS) require a prior read of `/keys`,
which is itself a logged event that shows up in the audit trail immediately before the
deletion.

`revoke` and `rotate` require `X-LL-Confirm` set to the key's **prefix** (`llk_1a2b3c4d`),
which is visible in the table. Lower friction, same anti-blind-request property.

**CSRF.** All mutations are same-origin fetches carrying a custom header
(`X-LL-Confirm`, plus `Content-Type: application/json` on create), which a cross-site HTML
form cannot set, so they always trigger a preflight and are inherently CSRF-safe. Belt and
braces, the mutation gate additionally:

```go
if o := c.Get("Origin"); o != "" && o != expectedOrigin(c) {
    return p.deny(c, "cross-origin mutation refused")
}
if sfs := c.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" {
    return p.deny(c, "cross-site mutation refused")
}
```

**Clickjacking.** Gatus must send `Content-Security-Policy: frame-ancestors 'self'` and
`X-Frame-Options: SAMEORIGIN` on all responses, so no external page can frame the Gatus
origin and bait a click onto a delete button. Note that Caddy's current
`X-Frame-Options: DENY` (Caddyfile line 84) would break the same-origin iframe, which is one
more reason Caddy leaves the local stack (section 5).

**Rate limit.** In-memory sliding window, 10 key mutations per 5 minutes per principal.
A legitimate operator mints one key and revokes one key. A script deleting every key trips
this on the eleventh and the audit log shows the burst.

### 4.3 Audit log

Append-only JSONL at `/data/telemetry-audit.log` (the `./data` bind mount already exists on
the gatus service), opened `O_APPEND|O_CREATE|O_WRONLY` with a mutex, `fsync` after each
write, and a strongly typed record so a secret cannot be smuggled in by a `map[string]any`:

```go
type keyAuditEvent struct {
    TS         time.Time `json:"ts"`
    Ref        string    `json:"ref"`
    Phase      string    `json:"phase"`      // "intent" | "ok" | "denied" | "upstream_error"
    Actor      string    `json:"actor"`      // "oidc:cwest@longlewis.com" | "basic:llops"
    AuthScheme string    `json:"auth_scheme"`// "oidc" | "basic"  <- makes the Basic gap visible
    IP         string    `json:"ip"`
    UserAgent  string    `json:"user_agent"`
    Action     string    `json:"action"`     // key.list | key.create | key.rotate | key.revoke | key.delete
    KeyID      int       `json:"key_id,omitempty"`
    KeyPrefix  string    `json:"key_prefix,omitempty"`
    KeyLabel   string    `json:"key_label,omitempty"`
    Confirmed  bool      `json:"confirmed"`
    Forced     bool      `json:"forced"`
    Status     int       `json:"status,omitempty"`
    Reason     string    `json:"reason,omitempty"`
}

// Struct only. No map, no `any`, no interface{}. There is no field a minted
// secret could occupy, which is the structural guarantee.
func (a *auditLog) Write(e keyAuditEvent)
```

**Every key mutation writes two records:** an `intent` before the upstream call and an
`ok` / `denied` / `upstream_error` after. Two-phase matters because a delete that succeeds
upstream and then fails to return still happened, and only the `intent` record proves it.

`key.list` (`GET /keys`) is also audited, at `phase:"ok"` only. It is the reconnaissance
step for any scripted key attack and it is cheap to record.

**Never written:** the `secret` field of a create/rotate response, the `key_hash`, the
console token, any request or response body.

Getting `Actor` requires one small addition to `security`, because `sessions` is unexported:

```go
// security/config.go
func (c *Config) Principal(ctx *fiber.Ctx) (scheme, actor string) {
    if c.gate != nil {
        req, err := adaptor.ConvertRequest(ctx, false)
        if err != nil { return "oidc", "oidc:unknown" }
        if v, ok := sessions.Get(c.gate.ExtractTokenFromRequest(req)); ok {
            if sub, ok := v.(string); ok && sub != "" { return "oidc", "oidc:" + sub }
        }
        return "oidc", "oidc:unknown"
    }
    if c.Basic != nil { return "basic", "basic:" + c.Basic.Username }
    return "none", "anonymous"
}
```

### 4.4 What the mint response is allowed to do

`POST /keys` and `POST /keys/{id}/rotate` return `{"secret": "llk_..."}` and that has to
reach the browser. Constraints: `Cache-Control: no-store, no-cache, must-revalidate` and
`Pragma: no-cache` on the response; no logging of the body at any level; the console keeps
it in a JS local only, never in `sessionStorage`/`localStorage` (it currently does not, and
must not start); and the redaction regex in 3.3(d) catches it if it ever appears in a log
line by some other path.

---

## 5. NETWORK ISOLATION

### 5.1 Caddy is removed from the local stack

Gatus is now the boundary. Keeping Caddy locally would: publish `443:443` and `80:80` on the
host, add a second independent auth configuration to keep in sync, and send
`X-Frame-Options: DENY` (Caddyfile:84) which breaks the same-origin iframe. Drop it.
`telemetry/docker-compose.yml` and `telemetry/caddy/Caddyfile` stay untouched for the prod
box, which still needs Caddy for ingest TLS.

**Consequence, stated plainly: the local stack does not accept ingest.** No field script
posts to it. If ingest is ever wanted locally, add Caddy back for that one route only, and
still do not route ingest through the Gatus proxy.

### 5.2 Compose

Two internal networks, so `lltel-api` can reach the DB and Gatus can reach the API, and
nothing else in either direction. Neither telemetry container publishes a port and neither
has a route off the host.

```yaml
# Gatus/docker-compose.yml
services:
  gatus:
    # ...existing...
    ports:
      - "127.0.0.1:8080:8080"   # see 5.3
    networks: [default, lltel-edge]

  lltel-db:
    image: mariadb:11.4
    container_name: lltel-db
    restart: unless-stopped
    environment:
      MARIADB_ROOT_PASSWORD: ${LL_DB_ROOT_PASS}
      MARIADB_DATABASE: lltelemetry
      MARIADB_USER: lltel
      MARIADB_PASSWORD: ${LL_DB_PASS}
    volumes:
      - ../LL-Telemetry/telemetry/db/01-schema.sql:/docker-entrypoint-initdb.d/01-schema.sql:ro
      - ../LL-Telemetry/telemetry/db/02-apikeys.sql:/docker-entrypoint-initdb.d/02-apikeys.sql:ro
      - lltel-data:/var/lib/mysql
    healthcheck:
      test: ["CMD","healthcheck.sh","--connect","--innodb_initialized"]
      interval: 10s
      timeout: 5s
      retries: 12
      start_period: 60s
    networks: [lltel-data]            # DB is on ONE internal net. Not reachable from gatus.
    security_opt: [no-new-privileges:true]
    # NO ports:

  lltel-api:
    build: ../LL-Telemetry/telemetry/api
    container_name: lltel-api
    restart: unless-stopped
    environment:
      LL_CONSOLE_TOKEN: ${LL_CONSOLE_TOKEN}   # section 3
      LL_INGEST_TOKEN: ${LL_INGEST_TOKEN}
      LL_DB_HOST: lltel-db
      LL_DB_USER: lltel
      LL_DB_PASS: ${LL_DB_PASS}
      LL_DB_NAME: lltelemetry
    depends_on:
      lltel-db: {condition: service_healthy}
    networks: [lltel-edge, lltel-data]
    security_opt: [no-new-privileges:true]
    cap_drop: [ALL]
    read_only: true
    tmpfs: [/tmp]
    # NO ports: not even 127.0.0.1. Gatus reaches it by service name on lltel-edge.

networks:
  default: {}          # gatus's existing outbound net (ICMP, Jira, UniFi cloud)
  lltel-edge:
    internal: true     # gatus <-> lltel-api only, no gateway, no egress
  lltel-data:
    internal: true     # lltel-api <-> lltel-db only

volumes:
  lltel-data:
```

Properties this buys, each checkable:

- `docker port lltel-api` returns nothing. There is no host-reachable socket for the
  telemetry API. Not on `0.0.0.0`, not on `127.0.0.1`.
- `phone-collector` and `unifi-collector` sit on `default` only, so a compromise of either
  cannot reach `lltel-api` at all, let alone `/api/v1/keys`.
- `lltel-api` is on two `internal: true` networks and therefore has **no route to the
  internet**. If it is ever popped, it cannot exfiltrate directly.
- `lltel-db` is only on `lltel-data`, so Gatus itself cannot reach MariaDB. Compromise of
  Gatus still has to go through the API's allowlist and console token.

**Is Caddy still needed locally? No.** Its three jobs here are TLS, basic auth, and path
gating. Gatus now does auth and path gating better (typed allowlist vs. Caddy path globs
like `/api/v1/runs*`, which would also match `/api/v1/runsanything`). TLS is section 5.3.

### 5.3 The port that *does* still need attention: Gatus's own

`docker-compose.yml` publishes `8080:8080` on all interfaces with no TLS. Adding Basic auth
to that means the credential crosses the LAN base64-encoded on every single request, and the
`gatus_session` cookie has no `Secure` flag anywhere in `oidc.go:143-149`.

Pick one, but pick one explicitly:

- **(a) Preferred.** Bind `127.0.0.1:8080:8080` and front Gatus with a TLS terminator on the
  host (the existing Caddy config and the `telemetry.crt`/`.key` pair are a template),
  setting `X-Frame-Options: SAMEORIGIN` and HSTS. Wall-mounted kiosks reach it over HTTPS.
- **(b) Accept it,** on a trusted management VLAN, with the risk written down: Basic
  credentials and session cookies are readable by anyone who can sniff that segment, and the
  Keys panel sits behind those credentials.

OIDC is only meaningfully safe under (a). Note also that if you go OIDC over plain HTTP, the
session cookie has no `Secure` attribute, which is a defect in Gatus worth a one-line patch:
add `Secure: true` when the redirect URL is `https://`.

---

## 6. THE `fiberfs Browse: true` EXPOSURE

Two separate findings from `api.go:158-162`.

**6.1 `Browse: true` should be `false`.** It enables directory listing over the embedded FS.
Today that only enumerates `web/static/{css,js,img}`, which is public SPA build output, so
the direct impact is low. But it is a free hardening win, it removes a reconnaissance
primitive, and it becomes materially dangerous the moment anyone drops a file into
`web/static/` thinking it is private. Set it to `false`. One character.

**6.2 The important part: nothing in `web/static/` is authenticated, so the console must
not live there.** The `fiberfs` mount is `app.Use("/")` registered at line 158, before the
security middleware is installed at line 172. Anything under that mount is served to
unauthenticated callers, unconditionally.

**Does it matter that the console HTML is unauthenticated?** It depends entirely on where
you put it, and the answer is: it must not be, so make it not be.

- The console HTML by itself contains no data. Every byte of telemetry arrives via `fetch`
  to `/api/v1/telemetry/*`, which is protected. So an unauthenticated console document
  would leak no records.
- But it is not harmless. It discloses the full API surface including every key-management
  route and the exact request shapes, it is a ready-made attacker UI for anyone who later
  finds an auth gap, it hands out the internal architecture for free, and its `SITE_MAP`-
  derived site names and result vocabulary are mild internal disclosure. More practically,
  it makes the "is the user authenticated?" question ambiguous: an unauthenticated user gets
  a fully rendered console that then throws 401s everywhere, which is a confusing and
  support-generating state.

**Mandated:** the console is served by the dedicated protected `/telemetry/console` handler
in section 1.5, from a `//go:embed` blob compiled into the binary, not from `web/static/`
and not from a bind mount. The handler sets the CSP from section 7. A test should assert
that `GET /telemetry/console` with no credentials returns 401 and that no file matching
`*console*.html` exists under `web/static/`.

---

## 7. RESIDUAL XSS REVIEW

### 7.1 Why it matters more now

Today an XSS in the console runs on the telemetry origin, where the worst outcome is
stealing a read-mostly Basic credential for a dashboard on an isolated box. On the Gatus
origin it runs with the Gatus session, and that session now reaches:

- `POST /api/v1/telemetry/keys` (mint a key for the attacker)
- `DELETE /api/v1/telemetry/keys/{id}` (destroy every batch key)
- `POST /api/v1/endpoints/:key/check` and the whole protected Gatus API
- every telemetry record: serials, MACs, AD domain, technician names, transcripts

And the injection vector is *cheap*: any machine holding an ingest key can write arbitrary
strings into `hostname`, `script`, `tech`, `site`, `details`, and `log`. So the attacker
does not need to compromise Gatus. They need one field script's token, which is exactly the
credential the Keys panel exists to manage. That is a circular dependency worth naming.

### 7.2 The two specific findings

**`esc()` at `index.html:326` does not escape `'`.**

```js
const esc=s=>String(s??'').replace(/[&<>"]/g,c=>({...}[c]));
```

Audit of every current call site: all attribute interpolations use double quotes
(`data-site="${esc(...)}"` line 376, `data-id="${esc(r.run_id)}"` line 470,
`data-host="${esc(o.hostname)}"` line 449, `value="${esc(v)}"` line 461). So the missing `'`
is **not currently exploitable**.

**Verdict: fix it anyway.** It is exploitable the instant anyone writes a single-quoted
attribute, which is a normal thing to do and which no reviewer would flag as security-
relevant. The fix is one character class and carries zero behavioural risk:

```js
const esc=s=>String(s??'').replace(/[&<>"'`]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;','`':'&#96;'}[c]));
```

(Backtick included because it is an attribute delimiter in some legacy parsers and because
these are template literals, where a stray backtick is a live hazard during editing.)

**`riverRow` at `index.html:469` interpolates `exit_code` and `duration_sec` unescaped.**

```js
const msg=`${esc(phrase)} <span class="k">·</span> exit=${r.exit_code??'—'} <span class="k">·</span> ${r.duration_sec??0}s...`;
```

Both are int/decimal columns in `db/01-schema.sql` and Pydantic coerces them at ingest, so
they cannot carry `<` today. **Verdict: fix it anyway**, for one reason that is not fussiness:
that invariant lives in a schema file in a different repository, is enforced by a type
annotation two systems away, and is defended by absolutely nothing in the console. A future
`exit_code: str | None` to capture `"TIMEOUT"`, or a JSON re-serialization that stringifies
numbers, silently turns this into stored XSS on the Gatus origin. Wrap both in `esc()`.

**Three more the review should sweep while it is in there** (same class, same cost):

- `index.html:588` `<tr data-id="${k.id}">` and `:592` `<td>${k.use_count}</td>` are raw
  ints from `api_keys`. Same argument, wrap them.
- `index.html:511` the transcript renderer puts `logSeverity(ln)` into `data-sev="${s}"`
  raw. Confirm `logSeverity` returns only values from a fixed constant set; if it can ever
  return a substring of the line, that is a live XSS against 300 KB of fully attacker-
  controlled PowerShell transcript. This one is worth actually checking, not assuming.
- `index.html:467` `r.result.toLowerCase()` will throw on a null `result`, breaking the
  whole river render. Availability, not XSS, but it is one bad row away.

### 7.3 The control that actually closes the class: CSP

Escaping is a per-site fix and the console has roughly forty interpolation sites. A
hash-based CSP on the console document makes the entire class unexploitable regardless of
whether any individual `esc()` is right. This is the MUST; 7.2 is defence in depth.

The console is a single file with one inline `<script>` and one inline `<style>`, so compute
their sha256 at build time and emit:

```go
func TelemetryConsole(c *fiber.Ctx) error {
    c.Set("Content-Security-Policy",
        "default-src 'none'; "+
        "script-src 'sha256-"+consoleScriptHash+"'; "+
        "style-src 'sha256-"+consoleStyleHash+"'; "+
        "img-src 'self' data:; "+
        "connect-src 'self'; "+
        "frame-ancestors 'self'; "+
        "base-uri 'none'; "+
        "form-action 'none'")
    c.Set("X-Content-Type-Options", "nosniff")
    c.Set("Referrer-Policy", "no-referrer")
    c.Set("Cache-Control", "no-store")
    c.Type("html", "utf-8")
    return c.Send(consoleHTML)   // //go:embed
}
```

No `'unsafe-inline'`, no `'unsafe-eval'`. An injected `<script>`, an `onerror=`, a
`javascript:` href, and an injected `<style>` are all inert. `connect-src 'self'` means even
a successful script injection cannot exfiltrate to an external host. If maintaining the hash
is annoying, split the script into a separate embedded file served from the same protected
group and use `script-src 'self'`; do not reach for `'unsafe-inline'`.

**Honest residual risk.** `sandbox="allow-scripts allow-same-origin"` on the iframe is *not*
a mitigation, since that combination is equivalent to no sandbox. Because the decided design
is a same-origin iframe, an XSS that does get through the CSP has full Gatus origin access.
The only structural alternative would be a second origin (different port or hostname) with
an explicit token handoff, which the decision excludes. CSP is therefore load-bearing here,
not optional, and it should be treated as a release blocker rather than a hardening task.

---

## 8. PRIORITIZED REQUIREMENTS

### MUST (do not ship the integration without these)

1. `security:` block present and validated. **Telemetry routes are registered only when
   `cfg.Security != nil`** (section 1.5). No security config means no telemetry, not
   unauthenticated telemetry.
2. Console document served from a protected route, `//go:embed`ed, **never** placed in
   `web/static/`.
3. `fiberfs Browse: false`.
4. Typed method+path allowlist, deny by default. Outbound request built from scratch, no
   `httputil.ReverseProxy`, no inbound headers forwarded, redirects not followed.
5. Path rejection rules: no `%`, restricted alphabet, no `..`, no `//`,
   `path.Clean` must be identity. Upstream `Scheme`/`Host` fixed at startup.
6. `POST /api/v1/runs` (ingest) is **not** proxied. Ever.
7. `LL_CONSOLE_TOKEN` required on every non-ingest telemetry API route, compared with
   `hmac.compare_digest`, fail closed if unset.
8. `secret` type with redacting `String`/`MarshalJSON`, single `reveal()` call site.
9. Upstream bodies never echoed into error envelopes. Static status-to-message table plus a
   `ref` correlation id. No response body logged for any `/keys` route.
10. `lltel-api` and `lltel-db` publish **no ports**; both telemetry networks are
    `internal: true`; the DB is unreachable from Gatus.
11. Hash-based CSP on the console document with no `'unsafe-inline'`, plus
    `frame-ancestors 'self'` and `X-Frame-Options: SAMEORIGIN` on Gatus responses.
12. Server-enforced revoke-before-delete and the 24 h post-revoke grace window.
13. Typed-label `X-LL-Confirm` on DELETE, prefix confirmation on revoke/rotate, both
    verified server-side against a precondition read.
14. Two-phase append-only audit log for every key mutation, strongly typed, with actor,
    auth scheme, IP, and user agent.
15. Auth challenge never originates inside the iframe: probe-then-top-level-navigation in
    the Vue view, `postMessage` escalation from the console on 401.
16. Decide and document the Gatus TLS/exposure question (section 5.3). Basic auth over
    cleartext on `0.0.0.0:8080` with a key-deletion console behind it is not a default to
    drift into.

### SHOULD

17. `esc()` extended to `'` and `` ` ``; `exit_code`, `duration_sec`, `k.id`, `k.use_count`
    wrapped in `esc()`; `logSeverity` output verified constant.
18. `hmac.compare_digest` in `check_ingest_key` (`main.py:102`).
19. `ApplySecurityMiddleware` patched to error on an empty bcrypt hash rather than accepting
    everything, and to compare the username in constant time.
20. `allowed-subjects` populated if OIDC (empty means the whole tenant).
21. Mutation rate limit, 10 per 5 min per principal.
22. `Secure: true` on the `gatus_session` cookie when the redirect URL is HTTPS.
23. `security_opt: no-new-privileges`, `cap_drop: ALL`, `read_only` on `lltel-api`.
24. Response size cap (8 MB) and a 15 s upstream timeout.
25. Tests: unauthenticated `GET /telemetry/console` returns 401; a table of ~30 hostile paths
    (`..%2f`, `%2e%2e/`, `//evil`, `\..\`, `keys/1/../../runs`, absolute URLs, CR/LF) all
    return 403 without an upstream call; the `reveal()` call-site count test.
26. Retention/rotation on `telemetry-audit.log` so it cannot fill `/data` and take the
    SQLite store down with it.

### NICE

27. Move to OIDC and retire the shared Basic credential, so the audit log carries real
    identities.
28. Split the console's inline script into a separate embedded file and use
    `script-src 'self'`, removing the build-time hash step.
29. Surface the audit log read-only in the Gatus UI, so key mutations are visible without
    shelling into the container.
30. `Report-To`/`report-uri` on the CSP so blocked injections are observable rather than
    silent.
31. Redact `serial`, `mac`, and `ad_domain` in the console list view behind a "reveal"
    toggle. They are rarely needed at a glance and their presence raises the value of the
    console as a target.

### REJECTED OUTRIGHT

- **Proxying ingest (`POST /api/v1/runs`) through Gatus.** Not with an allowlist, not with a
  size cap, not "just for local testing".
- **Registering any telemetry route when `cfg.Security == nil`.** Fail closed, always.
- **`httputil.ReverseProxy` with a `Director`.** The entire safety property is the allowlist
  and the from-scratch request; a Director undoes both.
- **Echoing upstream response bodies into error envelopes**, the current `api/jira.go:30`
  pattern. FastAPI's `detail=f"write failed: {e}"` and `detail=f"db unavailable: {e}"` put
  raw exception text one `<pre>` away from the browser.
- **Putting the console under `web/static/`**, or serving it from any route registered
  before the security middleware.
- **Leaving `Browse: true`.**
- **`sandbox="allow-scripts allow-same-origin"`** presented as an XSS mitigation. It is not
  one, and claiming it is would hide the residual risk in 7.3.
- **Auth by network position alone** on the telemetry API. The compose network already hosts
  two containers running bind-mounted Python from the working tree.
- **Any variant where the auth challenge happens inside the iframe.**
- **Shipping the Keys panel to more than a small named operator group while Basic is the
  scheme.** The panel is in scope, as decided; what is rejected is pretending a shared
  `basic:llops` line in an audit log constitutes attribution for a destructive operation.

---

## RECOMMENDATION

**Yes, build it, with the MUST list as the definition of done.**

The decided shape (local compose, Go allowlist proxy, same-origin iframe, full Keys panel)
is workable and can be made genuinely safe. The three things that would make it unsafe, and
that the MUST list exists to prevent, are: registering telemetry routes while Gatus still has
no auth at all; passing paths, headers, or upstream bodies through instead of reconstructing
them; and letting the same-origin iframe inherit a stored-XSS surface without a CSP.

The one thing worth pushing back on even though it is decided: hard DELETE. It is kept, as
instructed, but it is downgraded to *revoke, wait out a grace window, type the label, then
delete*, because the stated failure mode (ingest silently breaks for every field script
stamped with that batch key) is unrecoverable and invisible until someone notices a site has
stopped reporting. If any single requirement in this document gets cut for schedule, it
should not be that one.
