# Threat model: Gatus → LL-Telemetry console integration (pre-build)

Date: 2026-08-17
Scope: planned Gatus button + view that proxies/embeds the LL-Telemetry operations console.
Status: **integration should NOT be built as described until items P0-1..P0-4 are satisfied.**

---

## Executive summary

The proposed integration collapses two authentication domains into one, and the weaker
domain is the one that wins.

Three findings drive everything else:

1. **Gatus has no authentication enabled in this deployment.** `config.yaml` has no
   `security:` block, so `cfg.Security == nil` and the security middleware is never
   installed (`api/api.go:167-175`). The container publishes `8080:8080` directly with no
   reverse proxy (`docker-compose.yml:11-12`). Gatus is open to anyone who can reach it.
2. **The LL-Telemetry API enforces authentication on exactly one route.** Only
   `POST /api/v1/runs` checks a credential in application code (`telemetry/api/main.py:215-218`).
   Every read route *and every API-key management route* — create, list, revoke, rotate,
   delete — has zero application-level auth. Caddy is the **sole** enforcement point.
3. Therefore proxying telemetry through Gatus takes data currently behind basic auth and
   makes it readable by anyone who can reach Gatus. If the proxy targets `api:8080`
   directly (the natural choice on a Docker network) it also exposes **ingest-key
   management** to the same anonymous population.

The stored-XSS vector the task flagged as "worth verifying" was investigated in depth and
is **largely a false alarm** — the transcript renderer escapes correctly. Details in §6.

---

## 1. LL-Telemetry's stated security model

Source: `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\docs\SECURITY.md`

The document is accurate and unusually well-reasoned. Precise summary:

- **Posture** (lines 3-5): internal-only. No public DNS, no NAT, no firewall publish.
  Reachable from all sites over existing VPN tunnels. Explicitly assumes the network is
  *semi-trusted* and that USB-distributed field scripts *will* leak.
- **Trust boundaries** (lines 9-15):
  | Boundary | Control |
  |---|---|
  | Transport | Internal-CA TLS terminated at Caddy; SANs cover FQDN + `10.15.102.8` |
  | Write path | `POST /api/v1/runs`, ingest Bearer token, nothing else |
  | Read path | `GET /api/v1/runs*`, `/api/v1/stats*`, **separate** basic-auth credential |
  | Other `/api/` | Caddy 405 |
  | Size | Caddy caps body 2 MB; API truncates `log` to 300k chars |
- **Leaked ingest token blast radius** (lines 17-22): write-only. Can insert junk rows.
  Cannot read data, cannot reach `/stats`, cannot move laterally, exposes no other
  credential. Per-source-IP rate limiting bounds junk rate; `run_id` uniqueness makes
  replay a no-op.
- **Ingest keys** (lines 24-40): per-rollout-batch keys, format `llk_<prefix>_<secret>`.
  Server stores **SHA-256 hash** + plaintext prefix + label, so a DB dump yields no usable
  key. Every run records the key's label for attribution. Create/list/revoke/rotate/delete
  from the console **Keys** panel; those `/api/v1/keys*` endpoints "sit behind the dashboard
  credential at Caddy and are only reachable through it; the API is internal-only." Secret
  shown **once**, at creation. Revoke is instant and scoped to one batch. Legacy shared
  `LL_INGEST_TOKEN` still works as `shared-legacy` fallback during migration.
- **Endpoint data minimization** (lines 42-47): before a log leaves the PC the reporting
  block scrubs `password`, `passwd`, `pwd`, `secret`, `token`, `apikey`, `api_key`, `psk`
  patterns plus `-Password` / `/pass:` argument forms.
- **TLS-bypass tradeoff** (lines 49-57): a freshly imaged PC has not applied GPO and does
  not trust the internal CA, so its first report would fail validation. The block retries
  once over an unvalidated channel and flags the row `tls_bypassed = 1`. Judged acceptable
  given internal/VPN-only + write-only token; the dashboard lists affected machines.
  Remediation: bake the LL root CA into the golden image, set `$LLT_AllowInsecure = $false`.
- **Server hardening** (lines 59-66): `.env` is `chmod 600`; certs in `caddy/certs/`, never
  committed; UFW allows only 443/tcp and 22/tcp from `10.0.0.0/8`, port 80 closed; response
  headers `X-Content-Type-Options`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`,
  `-Server`; nightly `mariadb-dump`.

### Where the doc drifts from the implementation

Two discrepancies, both worth knowing before building against the doc:

- **Doc line 13 omits `/api/v1/timeline`.** The Caddyfile read matcher is
  `path /api/v1/runs* /api/v1/timeline*` (`telemetry/caddy/Caddyfile:38-41`) and there is a
  live `@app.get("/api/v1/timeline")` (`main.py:441`). The read surface is larger than the
  doc's table states.
- **Doc line 32's claim "only reachable through it" is a Caddy-topology fact, not an
  application property.** It holds only while Caddy is the sole path to `api:8080`. The
  planned Gatus proxy is precisely a second path. See §2 and §3 — this is the single most
  load-bearing assumption the integration would break.

---

## 2. Auth boundary risk — blast radius

### 2a. Does Gatus have auth? Yes, as a capability. No, as a deployment.

Gatus ships a complete security package — `security/basic.go` (bcrypt basic auth),
`security/oidc.go`, `security/sessions.go`, `security/config.go`. Enforcement is wired in
`api/api.go`:

```go
// api/api.go:163-175
protectedAPIRouter := apiRouter.Group("/")
if cfg.Security != nil {
    if err := cfg.Security.RegisterHandlers(app); err != nil { panic(err) }
    if err := cfg.Security.ApplySecurityMiddleware(protectedAPIRouter); err != nil { panic(err) }
}
```

The middleware is installed **only when `cfg.Security != nil`**.

### 2b. Is this deployment protected? No.

`config.yaml` top-level keys are exactly: `storage:` (1), `ui:` (11), `endpoints:` (30),
`external-endpoints:` (173). **There is no `security:` block anywhere in the file.**
A case-insensitive grep for `security` in `config.yaml` returns no match.

Consequences:
- `cfg.Security == nil` → `ApplySecurityMiddleware` never runs → the "PROTECTED ROUTES"
  under `api/api.go:176-186` (`/v1/endpoints/statuses`, `/v1/live` SSE, force-ping, etc.)
  are **not actually protected**.
- `api/config.go:20-23`: `isAuthenticated := true // Default to true if no security config
  is set` — the frontend is told it is authenticated, so no login UI appears.
- `docker-compose.yml:11-12` publishes `"8080:8080"` with no TLS and no fronting proxy.
  `.env` (`docker-compose.yml:16-17`) is injected but contains no auth material for Gatus
  itself.

Additional hardening gap found while confirming the above: the static file server is
mounted with **directory browsing enabled** — `api/api.go:159-163`,
`fiberfs.New(fiberfs.Config{Root: ..., Index: "index.html", Browse: true})`. Harmless for
the current embedded SPA bundle, but it becomes a real disclosure risk the moment console
assets are served from the Gatus origin (§6).

### 2c. Answering the critical question

> Would proxying make previously-authenticated telemetry data readable by anyone who can
> reach Gatus?

**Yes. Unambiguously, and with no mitigating control anywhere in the current design.**

The chain: anonymous user reaches Gatus:8080 → hits the new telemetry proxy route → Gatus
attaches the server-side stored basic-auth credential → Caddy accepts it → full read access
to `/api/v1/runs*`, `/api/v1/stats*`, `/api/v1/timeline*`.

The proxy converts a *credential the user must possess* into a *credential the server
supplies on their behalf*. Unless the proxy route is itself gated, the basic-auth boundary
described in `SECURITY.md:13` ceases to exist.

**What that discloses.** This is not thin data. Per `main.py:317-319` and the drawer
renderer, a reader obtains, fleet-wide: hostname, serial, manufacturer, model, OS + build,
AD domain, **internal IP**, **MAC**, site, technician identity (`tech`), and the **full
PowerShell transcript** (`log`, up to 300k chars). Endpoint scrubbing (`SECURITY.md:42-47`)
is pattern-based on a known keyword list and is a mitigation, not a guarantee — it is not
designed to withstand an anonymous reader with unlimited query access and a full-text
search parameter (`q` matches against `log`, `main.py:306-312`). Effectively this is an
anonymous asset inventory plus operational transcript archive for the whole estate.

### 2d. The much worse variant: proxying to `api:8080` directly

If the Gatus proxy targets the FastAPI container directly rather than going through Caddy
— which is the *natural* implementation on a shared Docker network, and which conveniently
sidesteps the whole TLS problem in §4 — then **no authentication exists at any layer**.

Verified route-by-route in `telemetry/api/main.py`. Only one route reads a credential:

```python
# main.py:214-218
@app.post("/api/v1/runs")
def ingest(run: Run, request: Request, authorization: str = Header(default="")):
    if not authorization.startswith("Bearer "):
        ...
```

Every other route has **no auth dependency, no `Depends(...)`, no header check**:
`/api/v1/health` (203), `/api/v1/runs` (284), `/api/v1/runs/{run_id}` (340),
`/api/v1/stats` (358), `/api/v1/timeline` (441), `/api/v1/keys` GET (499),
`/api/v1/keys` POST (516), `/api/v1/keys/{id}/revoke` (535),
`/api/v1/keys/{id}/rotate` (548), `/api/v1/keys/{id}` DELETE (570).

The Caddyfile is the entire access-control system. A direct-to-API proxy bypasses it
completely and exposes read **and full key management** to anonymous users.

**This design option must be rejected outright.** See §7.

---

## 3. The Keys panel

**Assessment: must be omitted from the embedded view. Gating is not sufficient, and
"acceptable as-is" is not defensible.**

Reasoning, in order of weight:

1. **The Keys panel is pure client-side UI over unauthenticated server endpoints.**
   `renderKeys` / `keyCreate` / `keyAction` (`telemetry/dashboard/index.html:583-613`) are
   presentation only. The endpoints they call are anonymous at the application layer
   (§2d). Hiding the panel in the embedded HTML therefore hides a *button*, not a
   capability — if the Gatus proxy forwards `/api/v1/keys*`, anyone can `curl` it through
   Gatus whether or not the panel renders. **Omission must be enforced at the proxy path
   allowlist, not in the UI.**

2. **These are state-changing operations reached through a GET-only-shaped integration.**
   `apiSend` (`index.html:577-581`) issues `POST /keys`, `POST /keys/{id}/revoke`,
   `POST /keys/{id}/rotate`, `DELETE /keys/{id}`. The plan describes Gatus proxying *read*
   requests; a proxy that forwards these is well outside the stated design.

3. **Consequence of reachability is estate-wide, not local.** `DELETE /api/v1/keys/{id}`
   (`main.py:570-577`) is a hard row delete with no revocation grace and no confirmation
   server-side. Deleting the active batch key silently breaks ingest for every field
   script stamped with it — a fleet-wide telemetry outage triggered by one anonymous
   request. `rotate` (548) does the same with an extra step. This is a denial-of-service
   primitive against the monitoring system, exposed to whoever can reach Gatus.

4. **`POST /api/v1/keys` mints a live ingest credential and returns the plaintext**
   (`main.py:516-531`, secret in the 201 body). Anonymous reachability means anonymous
   minting of valid write credentials — which then lets the attacker forge telemetry
   attributed to a label of their choosing, corrupting the very audit trail
   (`SECURITY.md:28-29`) that makes the per-batch key scheme worth having.

5. **The blast-radius argument in `SECURITY.md:17-22` is quietly invalidated.** That
   analysis holds because ingest keys are write-only *and* obtaining one requires the
   dashboard credential. Remove the second condition and "what a leaked ingest token buys
   an attacker" stops being the right question — the attacker no longer needs to leak one.

6. **CSRF, even if Gatus later gets auth.** Gatus has no CSRF middleware (no
   `middleware/csrf` import in `api/api.go`) and, with basic auth or a session cookie,
   browsers attach credentials automatically. A proxied `POST /keys` reachable from an
   authenticated admin's browser is cross-site forgeable from any page they visit.

**Verdict:** the proxy must expose a hardcoded allowlist of read paths only. `/api/v1/keys*`
must be rejected by default-deny, and key management must remain reachable **only** via
direct Caddy access to `telemetry.longlewis.local` with the dashboard credential.

### Incidental bug found in the Keys panel

`telemetry/dashboard/index.html:590` calls `fmt(k.created_utc)`:

```js
<td>${k.created_utc?fmt(k.created_utc):''}</td>
```

**`fmt` is never defined.** The only similarly-named function is `fmtTime` (line 336). The
only three occurrences of the token `fmt` in the file are the definition of `fmtTime`
(336), its use at 471, and this call at 590.

Since every persisted key has a non-null `created_utc` (`main.py:524-527` always inserts
`now`), the ternary always takes the truthy branch and throws `ReferenceError: fmt is not
defined` inside `renderKeys`. The throw propagates to `loadKeys`'s catch
(`index.html:605`), which replaces the panel body with an error div. **Net effect: the v0.4
Keys panel cannot list any existing key.** Not a security hole by itself — it is a
correctness bug in LL-Telemetry, unrelated to Gatus — but it means the panel is currently
untested against real data, which lowers confidence in it as an embedding target and
reinforces the "omit it" recommendation.

---

## 4. TLS options, ranked

The telemetry server presents a cert from an internal CA for `telemetry.longlewis.local`
(`Caddyfile:10`, `tls /certs/telemetry.crt /certs/telemetry.key`; SANs cover the FQDN and
`10.15.102.8` per `SECURITY.md:11`). Go's `net/http` will reject it with
`x509: certificate signed by unknown authority` unless the CA is in the trust pool.

**Ranked best → worst:**

| # | Option | Risk | Assessment |
|---|---|---|---|
| 1 | Mount the LL root CA into the Gatus container and load it into `tls.Config.RootCAs` via `x509.NewCertPool()` + `AppendCertsFromPEM` | Low | **Correct answer.** Preserves authentication *and* confidentiality of the hop. Needs a read-only bind mount of the CA PEM in `docker-compose.yml` and a scoped `http.Client` for telemetry only. Do not mutate `SystemCertPool` globally. |
| 2 | Add the CA to the image's system trust store at build time (`update-ca-certificates`) | Low-moderate | Works and needs no Go changes, but the trust decision becomes invisible in code and applies process-wide to every outbound Gatus client, including user-configured monitoring targets. Prefer #1 for blast-radius containment. |
| 3 | Plain HTTP to `api:8080` over the Docker network | **Unacceptable** | Ranked here only for completeness. Confidentiality loss on the hop is the *lesser* problem — the fatal one is that it bypasses Caddy, which is the only component enforcing authentication (§2d). Rejected in §7. |
| 4 | `InsecureSkipVerify: true` | High | Disables authentication of the server identity; any host that can win the name/route becomes the telemetry server and harvests the forwarded basic-auth credential. Gains nothing over #1 — the CA is available and mounting it is a two-line change. |

Note that #3 and #4 fail differently and #3 is worse despite "sounding safer": `InsecureSkipVerify`
still traverses Caddy and so still requires a credential, whereas plain HTTP to the API
container requires none.

### Does Gatus already do any of these?

Yes — `InsecureSkipVerify` appears throughout the codebase, but the distinction matters and
cuts against reusing the pattern:

**User-controlled, opt-in, defaults false** (legitimate; these are monitoring-target knobs):
- `client/config.go:215-216` — `InsecureSkipVerify: c.Insecure`
- `client/client.go:193-194`, `213-214`, `418-419` — all `config.Insecure`
- `client/grpc.go:28` — `cfg.Insecure`
- `client/config.go:341` — `configureTLS(tlsConfig *tls.Config, c TLSConfig)`

**Hardcoded `true`** (pre-existing weaknesses, upstream, not introduced by this fork):
- `alerting/provider/email/email.go:137` — `d.TLSConfig = &tls.Config{InsecureSkipVerify: true}`
- `alerting/provider/gitea/gitea.go:68` — `TLSClientConfig: &tls.Config{InsecureSkipVerify: true}`

**There is no existing CA-bundle-loading code in Gatus** — no `x509.NewCertPool` and no
`RootCAs` assignment anywhere in the non-test Go sources. Option #1 is new code, roughly
15 lines.

The two hardcoded-`true` sites are a tempting precedent (*"we already do this"*). They
should not be treated as one. Both are outbound to third-party alerting services where the
existing behaviour is an upstream bug; neither carries a credential that unlocks the entire
fleet's inventory. Do not extend the pattern to telemetry.

---

## 5. Secret handling in Gatus

**Current pattern (sound, and the integration should follow it):** secrets live in `.env`,
are injected as container env vars via `env_file` (`docker-compose.yml:16-17`), and are
read either through `${VAR}` expansion in `config.yaml` or `os.Getenv` in Go.

`.env` is present and git-ignored. Key names only (**values not read or reproduced**):
`JIRA_API_TOKEN`, `JIRA_BASE_URL`, `JIRA_EMAIL`, `JIRA_PROJECTS`, `PHONES_PUSH_TOKEN`,
`WILDIX_API_KEY`, `PHONES_IVORY_TOWER_TOKEN`, `PHONES_ALABASTER_TOKEN`,
`PHONES_BESSEMER_TOKEN`, `PHONES_CULLMAN_TOKEN`, `PHONES_FLORENCE_TOKEN`,
`PHONES_HOOVER_TOKEN`, `PHONES_MUSCLE_SHOALS_TOKEN`, `PHONES_PRATTVILLE_TOKEN`,
`PHONES_TUSCUMBIA_TOKEN`, `PHONES_DECATUR_TOKEN`, `PHONES_PUSH_TOKEN`, `UNIFI_API_KEY`.

The closest precedent is Jira — a third-party service credential read server-side only:
- `jira/jira.go:216-218` — `baseURL` / `email` / `token` from `JIRA_BASE_URL`,
  `JIRA_EMAIL`, `JIRA_API_TOKEN`
- `jira/jira.go:174` — `strings.TrimSpace(os.Getenv(key))` helper

Telemetry credentials should follow exactly this shape: `LLT_BASE_URL`, `LLT_DASH_USER`,
`LLT_DASH_PASS`, `LLT_CA_FILE` — env only, never in `config.yaml`, never in a handler
response.

### Verified: no secret currently leaks to the frontend

Checked concretely, not assumed.

**`window.config`** — every read in the Vue source, exhaustively:
- `web/app/src/App.vue:247` `logo`, `:251` `header`, `:255` `buttons`, `:259` `loginSubtitle`
- `web/app/src/components/Pagination.vue:52-53` `maximumNumberOfResults`
- `web/app/src/store.js:3-6` generic reader that discards unreplaced Go template placeholders

All six are UI presentation values from the `ui:` config block. No credential is templated
into the page.

**`/api/v1/config` response** — `api/config.go:26-35` marshals a map containing exactly
three keys: `oidc` (bool), `authenticated` (bool), `announcements` (slice). The
`securityConfig` field is used only to derive those two booleans (`config.go:18-23`) and is
never serialized. **No secret exposure.**

**Caveat for the new work:** the leak-free record is a property of the current handlers,
not an enforced invariant. A telemetry proxy that returns upstream error bodies verbatim
can leak the credential or internal URLs — e.g. `apiSend` surfaces
`` `${method} ${path} → ${r.status}` `` (`index.html:579`) and the API returns
`detail=f"db unavailable: {e}"` on `/health` (`main.py:210`), which is a raw DB exception
string including connection details. The proxy must normalize upstream errors to a bare
status code and must never echo request headers back.

---

## 6. Embedding risks

### 6a. Stored XSS via transcript — investigated in depth, **largely a false alarm**

The task flagged this as "a real stored-XSS vector worth verifying." I verified it and the
claim does not hold for the transcript path.

`telemetry/dashboard/index.html:326` defines the escaper:
```js
const esc=s=>String(s??'').replace(/[&<>"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
```

The transcript renderer at line 511 applies it **per line**:
```js
const logHtml=lines.length&&r.log?lines.map(ln=>{const s=logSeverity(ln);
  return `<div class="ln" data-sev="${s}"><span class="lv">${s||''}</span><span class="tx">${esc(ln||' ')}</span></div>`;}).join(''):'…';
```
`ln` — the attacker-controlled transcript content — is escaped. `s` comes from
`logSeverity` (`:497-501`), which returns one of four hardcoded literals. **Not injectable.**

Every other sink handling ingest-controlled data is likewise escaped: the detail key/value
renderer `kv` (`:509`), `riverRow`'s `site` / `hostname` / `script` / `result` / `run_id` /
`details` (`:466`, `:470-476`), `renderSites` (`:376-377`), `fillSelect` (`:461`), and the
Keys panel's `label` / `prefix` / `secret` (`:586`, `:589`).

**Residual gaps — real but currently not exploitable:**

- **`esc` does not escape the single quote `'`.** Safe *today* only because every
  interpolated attribute in the file uses double quotes (`:461`, `:470`, `:376`, `:511`).
  This is a latent footgun: one future single-quoted attribute turns into an injection.
- **`riverRow:469` interpolates two fields without escaping** —
  `exit=${r.exit_code??'—'}` and `${r.duration_sec??0}s`. Not exploitable because Pydantic
  coerces both to integers at ingest: `exit_code: int | None = None` (`main.py:183`),
  `duration_sec: int | None = None` (`main.py:186`); a non-integer payload is rejected 422
  before reaching the DB. **Defence depends entirely on the server-side model** — relocating
  this UI without that model, or relaxing those types, reintroduces the hole.
- **`:471` `fmtTime(r.received_utc)`** is unescaped, but `fmtTime` (`:336`) parses through
  `new Date()` and emits only zero-padded numerics. Not injectable.

**Also checked and clean:** SQL construction in `main.py:296-320` is fully parameterized —
column names come from a fixed literal tuple, all values are `%s` placeholders, including
the `q` full-text search (`:306-312`) and the `results` IN-list (`:302-305`). No SQL
injection.

**Conclusion:** the console's escaping is competent. But note *why* it is currently safe —
one un-escaped integer path is protected by a Pydantic type annotation in a different
repository, and `esc`'s missing `'` is protected by a quoting convention. Both are
one-commit-away regressions in a codebase whose Keys panel already ships a
`ReferenceError` (§3). **Same-origin embedding removes the blast-radius margin that makes
those acceptable risks**, which is the substance of 6c.

### 6b. `X-Frame-Options: DENY` blocks the iframe approach outright

`Caddyfile:82-87` sets, globally across all handlers:
```
header {
    X-Content-Type-Options nosniff
    X-Frame-Options DENY
    Referrer-Policy no-referrer
    -Server
}
```

`DENY` blocks framing by *any* origin, including Gatus. **The iframe design does not work
today.** Making it work means weakening the telemetry server's headers — deliberately
removing a control listed in the hardening checklist (`SECURITY.md:64-65`) — for the
benefit of an unauthenticated consumer. If framing is ever chosen, `DENY` must become a
narrowly-scoped `frame-ancestors` CSP naming the exact Gatus origin, never a blanket
removal, and only after Gatus itself is authenticated.

A second, independent reason the iframe fails: `api()` and `apiSend`
(`index.html:333`, `:577-581`) send **no `Authorization` header**. The console relies
entirely on *ambient* browser basic-auth credentials — Caddy 401s, the browser prompts, then
replays the credential on subsequent same-origin requests. Framed inside Gatus, those
requests are cross-origin to `telemetry.longlewis.local`; the user would face a basic-auth
prompt inside an iframe (a credential-phishing pattern browsers increasingly block), and
`fetch` without `credentials: 'include'` plus the absent CORS headers would fail regardless.

### 6c. Same-origin hosting converts console XSS into Gatus compromise

If the console HTML is served from the Gatus origin instead, the isolation that currently
contains §6a's residual gaps disappears. `X-Frame-Options: DENY` and
`Referrer-Policy: no-referrer` are set by *Caddy*, not by Gatus — Gatus sets no security
headers at all (no `helmet` middleware and no manual header writes in `api/api.go`), so
console content rehosted on the Gatus origin inherits none of them.

Under same-origin hosting, any future escaping regression would execute with full authority
over the Gatus origin: read/modify the dashboard, drive the state-changing endpoints listed
in §2 (`POST /v1/endpoints/:key/check`, `SetMonitoringForKey`, `SetPhonesExclusion`,
`RequestPhonesSweep`), and reach the proxied telemetry API *with the server-side credential
automatically attached*. That is the payoff that makes a low-probability escaping bug worth
designing against.

Compounding this: `Browse: true` on the static file server (`api/api.go:159-163`) would
expose a directory listing of any console asset tree mounted there.

### 6d. Ingest → console is a genuine cross-trust-boundary path

Independent of current escaping quality, note the shape of the threat: a **write-only,
deliberately widely-distributed, assumed-to-leak** credential (`SECURITY.md:5`, `:26-38`)
controls content that renders in a **read-privileged admin console**. `POST /api/v1/runs`
accepts `hostname`, `site`, `tech`, `script`, `details` (free-form `dict`, `main.py:197`)
and `log` (`:198`). Anyone holding any batch key — by design, every field technician with a
USB stick — can write content into the admin console's DOM. The current escaping handles
it; the architectural adjacency is what justifies strict CSP as defence-in-depth rather
than relying on `esc` alone.

---

## 7. Prioritized security requirements

### P0 — blocking; integration must not ship without these

- **P0-1. Gate the telemetry view behind Gatus authentication.** Add a `security:` block to
  `config.yaml` (`security.basic` with `password-bcrypt-base64`, or `security.oidc`) and
  register every telemetry route on `protectedAPIRouter` — i.e. **after** the
  `ApplySecurityMiddleware` call at `api/api.go:167-175`. Route ordering is load-bearing
  and the file says so explicitly at `:165`.
  Note the fork's own habit runs the other way: *every* custom route added so far — phones,
  UniFi, Jira, monitoring — sits on `unprotectedAPIRouter` (`api/api.go:83-131`). Copying
  the nearest existing example produces an unauthenticated route by default. This is the
  single most likely way the integration goes wrong.
- **P0-2. Never proxy to `api:8080` directly.** All traffic must traverse Caddy at
  `https://telemetry.longlewis.local`, because Caddy is the only component that
  authenticates read and key-management routes (`main.py` has no auth on any route except
  `POST /api/v1/runs` at `:215-218`).
- **P0-3. Default-deny path allowlist in the proxy.** Permit only
  `GET /api/v1/runs`, `GET /api/v1/runs/{id}`, `GET /api/v1/stats`, `GET /api/v1/timeline`.
  Reject `/api/v1/keys*` and all non-GET methods with a hardcoded check — not a regex
  denylist, not a UI-level omission. Do not accept a caller-supplied upstream path or host.
- **P0-4. Trust the internal CA properly.** Mount the LL root CA read-only and build a
  telemetry-scoped `http.Client` with `RootCAs` (§4 option 1). No `InsecureSkipVerify` on
  this path, and no plaintext HTTP.

### P1 — required before exposing the view to users

- **P1-1. Credentials from env only**, following `jira/jira.go:216-218`. Add
  `LLT_BASE_URL` / `LLT_DASH_USER` / `LLT_DASH_PASS` / `LLT_CA_FILE` to `.env` and
  `.env.example` (names only in the latter). Never in `config.yaml`, never in a response body.
- **P1-2. Normalize upstream errors.** Return a bare status code. Never forward upstream
  bodies (`main.py:210` returns raw DB exception text), never echo request headers,
  never surface the proxy target URL to the client.
- **P1-3. Set security headers on Gatus responses** — `Content-Security-Policy`,
  `X-Content-Type-Options: nosniff`, `X-Frame-Options`/`frame-ancestors`,
  `Referrer-Policy`. Gatus currently sets none, and any rehosted console content would
  inherit Caddy's protections not at all (§6c).
- **P1-4. Set `Browse: false`** at `api/api.go:159-163` if any console asset directory is
  mounted on the static file server.
- **P1-5. Add CSRF protection** to Gatus once auth exists — no CSRF middleware is present
  today, and several existing unprotected routes are state-changing (§3 item 6).
- **P1-6. Rate-limit and cap the proxy.** Bound `limit`/`days` server-side (upstream allows
  `limit` ≤ 1000 and `days` ≤ 365, `main.py:286`/`:294`) and cap response size — a single
  proxied request can pull 1000 rows each carrying up to 300k chars of transcript.

### P2 — hardening / follow-up

- **P2-1. Separate read credential for Gatus.** Issue a distinct dashboard credential for
  the proxy rather than reusing the human operators' — Caddy currently supports a single
  `{$LL_DASH_USER}`/`{$LL_DASH_HASH}` pair (`Caddyfile:43-46`), so this needs an upstream
  change. Enables independent revocation without locking out staff.
- **P2-2. Consider defence-in-depth auth inside the telemetry API.** The current
  Caddy-only model is fragile precisely because a second consumer is being added; an
  application-layer check on read + keys routes would make `SECURITY.md:32`'s "only
  reachable through it" true by construction rather than by topology.
- **P2-3. Fix `fmt` → `fmtTime`** at `telemetry/dashboard/index.html:590` (§3) — upstream
  bug, currently breaks the Keys panel entirely.
- **P2-4. Harden `esc`** to also escape `'` and `` ` `` (`index.html:326`), and escape
  `exit_code`/`duration_sec` at `:469` so the UI is not silently dependent on Pydantic
  types in another repo (§6a).
- **P2-5. Update `docs/SECURITY.md:13`** to include `/api/v1/timeline*` in the read-path row.
- **P2-6. Log proxy access** with the authenticated Gatus principal, so telemetry reads
  performed through Gatus remain attributable — Caddy's log will show only the Gatus
  service identity.

### Design options to reject outright

1. **Proxy directly to `api:8080`, bypassing Caddy.** Zero authentication on reads and on
   full key management (`main.py`, every route but `:215`). Non-negotiable.
2. **`InsecureSkipVerify: true` on the telemetry client.** The CA is available; mounting it
   is a two-line change. The existing hardcoded-`true` sites
   (`alerting/provider/email/email.go:137`, `alerting/provider/gitea/gitea.go:68`) are
   upstream bugs, not precedent.
3. **Proxying `/api/v1/keys*` in any form, gated or not.** Anonymous or CSRF-driven key
   deletion is a fleet-wide ingest outage; key creation mints valid write credentials and
   corrupts the label-attribution audit trail.
4. **Shipping the telemetry view on `unprotectedAPIRouter`** — i.e. matching the existing
   phones/UniFi/Jira pattern — while `config.yaml` has no `security:` block.
5. **Removing or blanket-weakening `X-Frame-Options: DENY`** on the telemetry server
   (`Caddyfile:84`) to enable iframing. If framing is genuinely needed, use a narrowly
   scoped `frame-ancestors` naming the exact Gatus origin, and only after P0-1.
6. **Forwarding the raw console HTML from the Gatus origin without CSP.** Collapses the
   origin boundary that currently contains the residual escaping gaps in §6a.

---

## Appendix — evidence index

| Claim | Location |
|---|---|
| Security middleware only when `cfg.Security != nil` | `Gatus/api/api.go:167-175` |
| Custom routes all unprotected | `Gatus/api/api.go:83-131` |
| Protected route list | `Gatus/api/api.go:176-186` |
| Static FS directory browsing enabled | `Gatus/api/api.go:159-163` |
| No `security:` block | `Gatus/config.yaml` (keys: 1, 11, 30, 173) |
| `isAuthenticated` defaults true | `Gatus/api/config.go:20-23` |
| Config response has no secrets | `Gatus/api/config.go:26-35` |
| Port published, no proxy | `Gatus/docker-compose.yml:11-12` |
| Env-var credential pattern | `Gatus/jira/jira.go:174`, `:216-218` |
| `window.config` reads (all) | `Gatus/web/app/src/App.vue:247,251,255,259`; `components/Pagination.vue:52-53`; `store.js:3-6` |
| Hardcoded `InsecureSkipVerify` | `Gatus/alerting/provider/email/email.go:137`; `gitea/gitea.go:68` |
| User-controlled `Insecure` | `Gatus/client/config.go:215-216,341`; `client/client.go:193,213,418`; `client/grpc.go:28` |
| No CA-pool code exists | no `x509.NewCertPool` / `RootCAs` in non-test Go sources |
| Only ingest route authenticates | `LL-Telemetry/telemetry/api/main.py:214-218` |
| Keys routes unauthenticated | `main.py:499,516,535,548,570` |
| `DELETE` is a hard delete | `main.py:570-577` |
| Create returns plaintext secret | `main.py:516-531` |
| Pydantic int coercion | `main.py:183,186` |
| `details` free-form dict, `log` str | `main.py:197-198` |
| SQL parameterized | `main.py:296-320` |
| Caddy is sole auth enforcement | `telemetry/caddy/Caddyfile:38-68` |
| `X-Frame-Options: DENY` global | `telemetry/caddy/Caddyfile:82-87` |
| `timeline` in read matcher | `telemetry/caddy/Caddyfile:38-41` |
| `esc` misses `'` | `telemetry/dashboard/index.html:326` |
| Transcript escaped | `telemetry/dashboard/index.html:511` |
| Unescaped ints | `telemetry/dashboard/index.html:469` |
| No `Authorization` header (ambient auth) | `telemetry/dashboard/index.html:333,577-581` |
| `fmt` undefined | `telemetry/dashboard/index.html:590` |
