package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

// LL-Telemetry integration.
//
// Gatus serves the LL-Telemetry operations console itself and reverse-proxies
// the console's API calls to the telemetry FastAPI. Two things make that safe:
//
//  1. The telemetry API has NO authentication of its own — in the real
//     deployment Caddy is the entire boundary. Here Gatus is, so every
//     telemetry route sits behind telemetryGate below. The gate is deliberately
//     independent of cfg.Security: Gatus's own security block is all-or-nothing
//     across /api, and switching it on would put the SSE stream behind a login
//     and break unattended wallboards. Registering under protectedAPIRouter as
//     well means telemetry picks up that second layer for free if it is ever
//     enabled.
//
//  2. The proxy is a deny-by-default allowlist, not a pass-through. Notably
//     POST /runs (ingest) is NOT proxied — field scripts post to the telemetry
//     host directly with their own llk_ keys, and exposing ingest here would let
//     anyone past the gate forge runs.
//
// The console itself is vendored byte-identical to upstream LL-Telemetry so it
// stays syncable with a plain diff; the only change is a serve-time rewrite of
// its API base, asserted at startup.

//go:embed assets/telemetry-console.html
var telemetryConsoleRaw string

const (
	// The console hardcodes its API base. We repoint it at the proxy prefix
	// rather than forking the file.
	consoleAPIOriginal  = "const API='/api/v1'"
	consoleAPIRewritten = "const API='/api/v1/telemetry'"

	telemetryUpstreamPrefix = "/api/v1"
	telemetryBodyLimit      = 1 << 16 // 64 KiB is ample for a key label
	telemetryResponseLimit  = 16 << 20

	// Distinguishes the gate's "not configured" 503 from the upstream's "the
	// database is down" 503, which the proxy would otherwise pass through
	// indistinguishably.
	telemetryNotConfiguredHeader = "X-Telemetry-Not-Configured"

	// Whole-request budget, shared across both hops of a DELETE. Must stay
	// under the 15s server WriteTimeout in controller/.
	telemetryRequestBudget = 12 * time.Second

	telemetryAuthCacheTTL = 30 * time.Second

	// Session cookie, so operators sign in through the console's own login
	// screen instead of a native browser dialog. The path scopes it to the
	// telemetry routes; nothing else on the origin ever receives it.
	telemetrySessionCookie = "lltel_session"
	telemetrySessionPath   = "/api/v1/telemetry"
	telemetrySessionTTL    = 12 * time.Hour

	// Failed sign-ins tolerated per source address before the gate stops
	// answering. A login form is far easier to grind than a browser dialog.
	telemetryMaxFailures  = 10
	telemetryFailureReset = 5 * time.Minute
)

var (
	telemetryConsoleHTML string
	telemetryConsoleCSP  string

	telemetryConfigOnce sync.Once
	telemetryConfigured telemetryConfig

	telemetryHTTPClient = &http.Client{
		// Comfortably under the 15s server WriteTimeout in controller/, so a
		// slow upstream surfaces as a clean 504 rather than a severed response.
		Timeout: 12 * time.Second,
	}

	// Sentinels so the caller can tell "this key may not be deleted" apart from
	// "the upstream could not be reached". Without them every pre-delete failure
	// looked like a 409 Conflict.
	errTelemetryKeyActive  = errors.New("revoke this key before deleting it: deleting an active key breaks ingest for every field script stamped with it")
	errTelemetryKeyUnknown = errors.New("no such telemetry key")

	telemetryAuthCacheMu sync.RWMutex
	telemetryAuthCache   = map[[32]byte]time.Time{}

	telemetrySessionsMu sync.Mutex
	telemetrySessions   = map[string]telemetrySession{}

	telemetryFailuresMu sync.Mutex
	telemetryFailures   = map[string]telemetryFailureCount{}
)

type telemetrySession struct {
	user   string
	expiry time.Time
}

type telemetryFailureCount struct {
	count int
	since time.Time
}

// newTelemetrySession mints a session token and sweeps expired ones while it
// holds the lock, so the map cannot grow without bound.
func newTelemetrySession(user string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	telemetrySessionsMu.Lock()
	for existing, session := range telemetrySessions {
		if now.After(session.expiry) {
			delete(telemetrySessions, existing)
		}
	}
	telemetrySessions[token] = telemetrySession{user: user, expiry: now.Add(telemetrySessionTTL)}
	telemetrySessionsMu.Unlock()
	return token, nil
}

func telemetrySessionUser(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	telemetrySessionsMu.Lock()
	defer telemetrySessionsMu.Unlock()
	session, ok := telemetrySessions[token]
	if !ok {
		return "", false
	}
	if time.Now().After(session.expiry) {
		delete(telemetrySessions, token)
		return "", false
	}
	return session.user, true
}

func dropTelemetrySession(token string) {
	if token == "" {
		return
	}
	telemetrySessionsMu.Lock()
	delete(telemetrySessions, token)
	telemetrySessionsMu.Unlock()
}

// telemetryThrottled reports whether this address has burned through its
// sign-in attempts. The window resets on its own, so a locked-out operator is
// never locked out for longer than telemetryFailureReset.
func telemetryThrottled(address string) bool {
	telemetryFailuresMu.Lock()
	defer telemetryFailuresMu.Unlock()
	record, ok := telemetryFailures[address]
	if !ok {
		return false
	}
	if time.Since(record.since) > telemetryFailureReset {
		delete(telemetryFailures, address)
		return false
	}
	return record.count >= telemetryMaxFailures
}

func telemetryRecordFailure(address string) {
	telemetryFailuresMu.Lock()
	defer telemetryFailuresMu.Unlock()
	// Sweep while the lock is already held: without this the map grows one
	// permanent entry per attacking address.
	for existing, record := range telemetryFailures {
		if time.Since(record.since) > telemetryFailureReset {
			delete(telemetryFailures, existing)
		}
	}
	record, ok := telemetryFailures[address]
	if !ok {
		telemetryFailures[address] = telemetryFailureCount{count: 1, since: time.Now()}
		return
	}
	record.count++
	// Sliding window: each failure re-anchors it, so a paced attacker cannot
	// escape with a one-second lockout while a fumbling operator eats five
	// minutes.
	record.since = time.Now()
	telemetryFailures[address] = record
}

func telemetryClearFailures(address string) {
	telemetryFailuresMu.Lock()
	delete(telemetryFailures, address)
	telemetryFailuresMu.Unlock()
}

// TelemetryLogin exchanges credentials for a session cookie. It exists so the
// console can present its own sign-in screen: a native basic-auth dialog inside
// the console's iframe is both ugly and unreliable.
func TelemetryLogin(c *fiber.Ctx) error {
	cfg := telemetryCfg()
	if !cfg.configured() {
		c.Set(telemetryNotConfiguredHeader, "1")
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "LL-Telemetry is not configured on this Gatus instance",
		})
	}
	address := c.IP()
	if telemetryThrottled(address) {
		logr.Warnf("[api.TelemetryLogin] too many failed sign-ins from %s", address)
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error": "Too many failed sign-ins. Wait a few minutes and try again.",
		})
	}
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Send a user and password."})
	}
	if !cfg.authenticate(strings.TrimSpace(body.User), body.Password) {
		telemetryRecordFailure(address)
		logr.Warnf("[api.TelemetryLogin] rejected sign-in for %q from %s", strings.TrimSpace(body.User), address)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Those credentials were not accepted.",
		})
	}
	// Drop any session this browser already held, so a second sign-in does not
	// leave the first one alive and unreachable for its full 12 hours.
	dropTelemetrySession(c.Cookies(telemetrySessionCookie))
	token, err := newTelemetrySession(strings.TrimSpace(body.User))
	if err != nil {
		logr.Errorf("[api.TelemetryLogin] could not mint a session: %s", err.Error())
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not start a session."})
	}
	telemetryClearFailures(address)
	c.Cookie(&fiber.Cookie{
		Name:     telemetrySessionCookie,
		Value:    token,
		Path:     telemetrySessionPath,
		HTTPOnly: true,
		SameSite: "Strict",
		Secure:   c.Protocol() == "https",
		MaxAge:   int(telemetrySessionTTL.Seconds()),
	})
	logr.Infof("[api.TelemetryLogin] %q signed in from %s", strings.TrimSpace(body.User), address)
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"user": strings.TrimSpace(body.User)})
}

// TelemetryLogout drops the session and clears the cookie.
func TelemetryLogout(c *fiber.Ctx) error {
	dropTelemetrySession(c.Cookies(telemetrySessionCookie))
	c.Cookie(&fiber.Cookie{
		Name:     telemetrySessionCookie,
		Value:    "",
		Path:     telemetrySessionPath,
		HTTPOnly: true,
		SameSite: "Strict",
		Secure:   c.Protocol() == "https",
		Expires:  time.Now().Add(-time.Hour),
		MaxAge:   -1,
	})
	return c.SendStatus(fiber.StatusNoContent)
}

func init() {
	// Fail loudly at startup rather than silently serving a console pointed at
	// Gatus's own /api/v1, which would render empty panels forever.
	if n := strings.Count(telemetryConsoleRaw, consoleAPIOriginal); n != 1 {
		panic(fmt.Sprintf(
			"api/assets/telemetry-console.html: expected exactly one occurrence of %q but found %d. "+
				"The vendored LL-Telemetry console changed shape and its API base can no longer be rewritten safely.",
			consoleAPIOriginal, n,
		))
	}
	telemetryConsoleHTML = strings.Replace(telemetryConsoleRaw, consoleAPIOriginal, consoleAPIRewritten, 1)
	telemetryConsoleCSP = buildTelemetryCSP(telemetryConsoleHTML)
}

// buildTelemetryCSP locks the console down as far as its own markup allows.
//
// script-src is a SHA-256 hash of the single inline <script>, with no
// 'unsafe-inline'. That is safe to keep strict because the console assigns
// handlers as JS properties (el.onclick = ...) rather than HTML on* attributes,
// and uses no eval, no new Function and no javascript: URLs.
//
// style-src has to allow 'unsafe-inline': the console renders 12 inline
// style="..." attributes, and a hash does not authorise style attributes (they
// are governed by style-src-attr, which falls back to style-src). Hashing the
// <style> block instead would leave those attributes blocked and the page
// visibly broken. The security cost is small; CSS is not the vector here.
//
// connect-src 'self' is the load-bearing directive: even a stored XSS carried
// in a machine transcript cannot exfiltrate anything off-origin.
func buildTelemetryCSP(doc string) string {
	directives := []string{
		"default-src 'none'",
		"script-src " + inlineHash(doc, "<script>", "</script>"),
		"style-src 'unsafe-inline'",
		"connect-src 'self'",
		"img-src 'self' data:",
		"frame-ancestors 'self'",
		"base-uri 'none'",
		"form-action 'none'",
	}
	return strings.Join(directives, "; ")
}

func inlineHash(doc, open, close string) string {
	// Exactly one block, or the hash silently covers only the first and the
	// rest are blocked at runtime. This is the trap a future upstream resync
	// would otherwise walk into.
	if n := strings.Count(doc, open); n != 1 {
		panic(fmt.Sprintf(
			"api/assets/telemetry-console.html: expected exactly one %s block but found %d; the CSP hash would cover only the first",
			open, n,
		))
	}
	start := strings.Index(doc, open)
	if start < 0 {
		panic("api/assets/telemetry-console.html: missing " + open + " block; cannot build a CSP hash")
	}
	start += len(open)
	end := strings.Index(doc[start:], close)
	if end < 0 {
		panic("api/assets/telemetry-console.html: unterminated " + open + " block; cannot build a CSP hash")
	}
	sum := sha256.Sum256([]byte(doc[start : start+end]))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// --- Configuration ---------------------------------------------------------

type telemetryConfig struct {
	upstream      string // base URL of the telemetry API, e.g. http://lltel-api:8080
	upstreamToken string // optional bearer, if the upstream ever grows its own auth
	uiUser        string
	uiBcryptHash  []byte
	uiPassword    string // plaintext fallback; bcrypt is preferred
}

func telemetryEnvOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func telemetryCfg() telemetryConfig {
	telemetryConfigOnce.Do(func() {
		cfg := telemetryConfig{
			upstream:      strings.TrimRight(telemetryEnvOr("TELEMETRY_UPSTREAM_URL", "http://lltel-api:8080"), "/"),
			upstreamToken: strings.TrimSpace(os.Getenv("TELEMETRY_UPSTREAM_TOKEN")),
			uiUser:        strings.TrimSpace(os.Getenv("TELEMETRY_UI_USER")),
			uiPassword:    os.Getenv("TELEMETRY_UI_PASSWORD"),
		}
		if hash := strings.TrimSpace(os.Getenv("TELEMETRY_UI_PASSWORD_BCRYPT")); hash != "" {
			cfg.uiBcryptHash = []byte(hash)
		}
		telemetryConfigured = cfg
		if !cfg.configured() {
			logr.Info("[api.telemetryCfg] LL-Telemetry is not configured (set TELEMETRY_UI_USER and TELEMETRY_UI_PASSWORD_BCRYPT or TELEMETRY_UI_PASSWORD) — the telemetry console will show a not-configured notice")
		} else {
			logr.Infof("[api.telemetryCfg] Proxying LL-Telemetry at %s for user %q", cfg.upstream, cfg.uiUser)
		}
	})
	return telemetryConfigured
}

// configured reports whether telemetry can be served. It requires credentials:
// the upstream has no auth of its own, so an unconfigured Gatus must fail
// closed rather than expose machine transcripts and ingest-key management.
func (c telemetryConfig) configured() bool {
	return c.upstream != "" && c.uiUser != "" && (len(c.uiBcryptHash) > 0 || c.uiPassword != "")
}

// authenticate deliberately checks the password even when the username is
// already wrong. Returning early would skip the entire bcrypt work factor,
// turning response time into a username oracle that is trivially measurable
// over a LAN.
func (c telemetryConfig) authenticate(user, password string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(c.uiUser)) == 1
	var passwordOK bool
	if len(c.uiBcryptHash) > 0 {
		passwordOK = bcrypt.CompareHashAndPassword(c.uiBcryptHash, []byte(password)) == nil
	} else {
		passwordOK = subtle.ConstantTimeCompare([]byte(password), []byte(c.uiPassword)) == 1
	}
	return userOK && passwordOK
}

// telemetrySameOriginWrite guards the state-changing routes against CSRF.
//
// The gate authenticates with Basic auth, and Basic credentials have no
// SameSite: the browser attaches them to cross-site-initiated requests to this
// origin. A form POST to /keys/{id}/revoke is a "simple" request, so it is not
// preflighted, and revoking a key kills ingest for every field script stamped
// with it. Nothing can be read back cross-origin, but the destructive half is
// real.
//
// A request with neither header is not from a browser (curl, scripts), so it is
// allowed; every browser that can mount this attack sends at least one.
func telemetrySameOriginWrite(c *fiber.Ctx) bool {
	if c.Method() == fiber.MethodGet || c.Method() == fiber.MethodHead {
		return true
	}
	if site := c.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	if origin := c.Get(fiber.HeaderOrigin); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return parsed.Host == string(c.Request().Host())
	}
	return true
}

// --- Gate ------------------------------------------------------------------

// TelemetryGate authenticates every telemetry route. When telemetry is not
// configured it serves a notice for the console and refuses everything else,
// so an unconfigured deployment exposes no data rather than defaulting open.
func TelemetryGate(c *fiber.Ctx) error {
	cfg := telemetryCfg()
	if !cfg.configured() {
		if strings.TrimSuffix(c.Path(), "/") == telemetrySessionPath+"/console" {
			c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
			return c.Status(fiber.StatusOK).SendString(telemetryNotConfiguredHTML)
		}
		// Marked so the UI can tell this apart from an upstream 503 (the
		// telemetry API returns 503 when MariaDB is down); otherwise a database
		// outage renders the "set these env vars" card.
		c.Set(telemetryNotConfiguredHeader, "1")
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "LL-Telemetry is not configured on this Gatus instance",
		})
	}
	if !telemetrySameOriginWrite(c) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "cross-site telemetry writes are refused",
		})
	}
	// Signing in and out cannot require a session. Matched exactly rather than
	// by suffix: a suffix test would let a crafted path such as
	// /api/v1/telemetry/runs/x/telemetry/session skip authentication and reach
	// the proxy handler. The allowlist would still refuse it, but the gate must
	// not be the thing that lets it through.
	if strings.TrimSuffix(c.Path(), "/") == telemetrySessionPath+"/session" {
		return c.Next()
	}
	// A session cookie is the normal path: the console signs in through its own
	// screen. Basic auth still works for curl and scripts.
	if user, ok := telemetrySessionUser(c.Cookies(telemetrySessionCookie)); ok {
		c.Locals("telemetryUser", user)
		return c.Next()
	}
	// Basic auth is the fallback for scripts, and it is the door an attacker
	// would actually use: it is reachable on every GET, which short-circuits
	// the same-origin check above. Throttle it on the same counter as the form,
	// or the form's limit only guards the door nobody has to knock on. Every
	// wrong password also pays a full bcrypt derivation, so this doubles as the
	// CPU-exhaustion defence.
	//
	// Only requests that actually carried credentials count: the SPA probes
	// /health without any on every page load, and counting those would lock an
	// operator out after ten reloads.
	header := c.Get(fiber.HeaderAuthorization)
	address := c.IP()
	if header != "" && telemetryThrottled(address) {
		logr.Warnf("[api.TelemetryGate] too many failed credentials from %s", address)
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error": "Too many failed sign-ins. Wait a few minutes and try again.",
		})
	}
	user, password, ok := parseBasicAuthHeader(header)
	if !ok || !telemetryAuthorized(cfg, header, user, password) {
		if header != "" {
			telemetryRecordFailure(address)
		}
		// Deliberately no WWW-Authenticate: it would make the browser throw a
		// native dialog inside the console's iframe. The SPA renders a proper
		// sign-in screen on 401 instead.
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Sign in to reach the telemetry console.",
		})
	}
	telemetryClearFailures(address)
	c.Locals("telemetryUser", user)
	return c.Next()
}

// telemetryAuthorized wraps authenticate with a short-lived cache of successful
// credentials. Without it every request pays a full bcrypt derivation: the
// console polls three endpoints every five seconds, and an unauthenticated
// caller could force the work factor at will on a :8080-published service.
// Only successes are cached, so a wrong password never gets cheaper.
func telemetryAuthorized(cfg telemetryConfig, header, user, password string) bool {
	sum := sha256.Sum256([]byte(header))
	telemetryAuthCacheMu.RLock()
	expiry, cached := telemetryAuthCache[sum]
	telemetryAuthCacheMu.RUnlock()
	if cached && time.Now().Before(expiry) {
		return true
	}
	if !cfg.authenticate(user, password) {
		return false
	}
	telemetryAuthCacheMu.Lock()
	// Bounded so a flood of distinct credentials cannot grow it without limit.
	if len(telemetryAuthCache) > 64 {
		telemetryAuthCache = map[[32]byte]time.Time{}
	}
	telemetryAuthCache[sum] = time.Now().Add(telemetryAuthCacheTTL)
	telemetryAuthCacheMu.Unlock()
	return true
}

func parseBasicAuthHeader(header string) (user, password string, ok bool) {
	const prefix = "Basic "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header[len(prefix):]))
	if err != nil {
		return "", "", false
	}
	user, password, found := strings.Cut(string(decoded), ":")
	if !found {
		return "", "", false
	}
	return user, password, true
}

// --- Console ---------------------------------------------------------------

// TelemetryConsole serves the vendored LL-Telemetry operations console. It is
// registered before the proxy wildcard so /telemetry/console is not swallowed
// by it.
func TelemetryConsole(c *fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	c.Set("Content-Security-Policy", telemetryConsoleCSP)
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Referrer-Policy", "no-referrer")
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Status(fiber.StatusOK).SendString(telemetryConsoleHTML)
}

const telemetryNotConfiguredHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>LL-Telemetry not configured</title>
<style>
html,body{height:100%;margin:0}
body{background:#15171c;color:#e6e8ec;font:13px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
display:flex;align-items:center;justify-content:center;padding:24px}
.card{max-width:560px;border:1px solid #2b2f38;border-radius:2px;background:#1b1e25;padding:20px}
h1{font-size:15px;margin:0 0 10px;letter-spacing:.02em}
p{margin:0 0 10px;color:#a7adb9}
code{font:12px ui-monospace,"Cascadia Mono",Consolas,monospace;color:#e6e8ec;background:#242832;padding:1px 5px;border-radius:2px}
ul{margin:0;padding-left:18px;color:#a7adb9}li{margin:3px 0}
</style></head>
<body><div class="card">
<h1>LL-Telemetry is not configured</h1>
<p>The telemetry console is gated behind its own credentials, and none are set on this Gatus instance. Nothing is being proxied.</p>
<p>Set these in <code>.env</code> and restart Gatus:</p>
<ul>
<li><code>TELEMETRY_UI_USER</code></li>
<li><code>TELEMETRY_UI_PASSWORD_BCRYPT</code> (preferred) or <code>TELEMETRY_UI_PASSWORD</code></li>
<li><code>TELEMETRY_UPSTREAM_URL</code> (defaults to <code>http://lltel-api:8080</code>)</li>
</ul>
</div></body></html>`

// --- Proxy -----------------------------------------------------------------

// telemetryRoute is one allowlisted upstream call. A "*" segment matches
// exactly one path segment.
type telemetryRoute struct {
	method   string
	segments []string
}

// The complete set of upstream calls the console is permitted to make.
// POST /runs is deliberately absent: ingest belongs to the field scripts and
// their llk_ keys, not to anyone who can reach this console.
var telemetryAllowlist = []telemetryRoute{
	{fiber.MethodGet, []string{"health"}},
	{fiber.MethodGet, []string{"stats"}},
	{fiber.MethodGet, []string{"timeline"}},
	{fiber.MethodGet, []string{"runs"}},
	{fiber.MethodGet, []string{"runs", "*"}},
	{fiber.MethodGet, []string{"keys"}},
	{fiber.MethodPost, []string{"keys"}},
	{fiber.MethodPost, []string{"keys", "*", "revoke"}},
	{fiber.MethodPost, []string{"keys", "*", "rotate"}},
	{fiber.MethodDelete, []string{"keys", "*"}},
}

// telemetryUpstreamPath validates the requested sub-path against the allowlist
// and returns the upstream path to call.
//
// Any '%' is rejected outright. No allowlisted path needs percent-encoding, so
// refusing it kills traversal, encoded-slash and null-byte tricks in one rule
// with no decode-ordering subtleties.
func telemetryUpstreamPath(method, rest string) (string, bool) {
	if strings.ContainsRune(rest, '%') {
		return "", false
	}
	if !strings.HasPrefix(rest, "/") {
		rest = "/" + rest
	}
	if rest != path.Clean(rest) {
		return "", false
	}
	for _, r := range rest {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '/':
		default:
			return "", false
		}
	}
	segments := strings.Split(strings.TrimPrefix(rest, "/"), "/")
	for _, route := range telemetryAllowlist {
		if route.method != method || len(route.segments) != len(segments) {
			continue
		}
		matched := true
		for i, want := range route.segments {
			if want == "*" {
				if segments[i] == "" {
					matched = false
					break
				}
				continue
			}
			if segments[i] != want {
				matched = false
				break
			}
		}
		if matched {
			return telemetryUpstreamPrefix + rest, true
		}
	}
	return "", false
}

// TelemetryProxy forwards an allowlisted console call to the telemetry API.
//
// The outbound request is built from scratch with an empty header map. That is
// deliberate: forwarding X-Forwarded-For would let a caller both reset the
// upstream's per-IP ingest rate-limit bucket and forge runs.site, because
// client_ip() upstream trusts the first XFF entry with no trusted-proxy check.
func TelemetryProxy(c *fiber.Ctx) error {
	cfg := telemetryCfg()
	rest := c.Params("*")
	upstreamPath, allowed := telemetryUpstreamPath(c.Method(), rest)
	if !allowed {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "this telemetry endpoint is not available through Gatus",
		})
	}
	// Deleting an ingest key breaks every field script stamped with that batch,
	// so require it to be revoked first. The console only offers delete on
	// revoked keys; this enforces that server-side rather than trusting the UI.
	// One budget for the whole request, shared across both hops of a DELETE.
	// Giving each hop its own 12s would allow 24s total and blow through the
	// 15s server WriteTimeout, severing the response mid-flight.
	ctx, cancel := context.WithTimeout(c.UserContext(), telemetryRequestBudget)
	defer cancel()
	if c.Method() == fiber.MethodDelete {
		id := strings.TrimPrefix(strings.TrimPrefix(rest, "/"), "keys/")
		switch err := telemetryRequireRevoked(ctx, cfg, id); {
		case err == nil:
		case errors.Is(err, errTelemetryKeyActive):
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
		case errors.Is(err, errTelemetryKeyUnknown):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
		default:
			// An unreachable upstream is not a conflict.
			logr.Warnf("[api.TelemetryProxy] could not verify key %q before deleting: %s", id, err.Error())
			return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "the telemetry service is unreachable"})
		}
	}
	target := cfg.upstream + upstreamPath
	if raw := string(c.Request().URI().QueryString()); raw != "" {
		// The query bypasses the path's charset check, so screen it for
		// anything that could break out of the request line.
		if strings.ContainsAny(raw, " \t\r\n") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid telemetry request"})
		}
		target += "?" + raw
	}
	parsed, err := url.Parse(target)
	if err != nil {
		logr.Warnf("[api.TelemetryProxy] could not build upstream URL for %s %s: %s", c.Method(), upstreamPath, err.Error())
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid telemetry request"})
	}
	var body io.Reader
	if raw := c.Body(); len(raw) > 0 {
		if len(raw) > telemetryBodyLimit {
			return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "request body too large"})
		}
		body = strings.NewReader(string(raw))
	}
	request, err := http.NewRequestWithContext(ctx, c.Method(), parsed.String(), body)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid telemetry request"})
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if cfg.upstreamToken != "" {
		request.Header.Set("Authorization", "Bearer "+cfg.upstreamToken)
	}
	// Key mutations are audited. Bodies are never logged: mint and rotate
	// return the plaintext secret.
	if c.Method() != fiber.MethodGet {
		logr.Infof("[api.TelemetryProxy] key mutation %s %s by %v", c.Method(), upstreamPath, c.Locals("telemetryUser"))
	}
	response, err := telemetryHTTPClient.Do(request)
	if err != nil {
		logr.Warnf("[api.TelemetryProxy] upstream %s %s failed: %s", c.Method(), upstreamPath, err.Error())
		// Distinguish "too slow" from "not there": /runs?q= does an unindexed
		// LIKE scan over a MEDIUMTEXT column and is the first thing that will
		// exceed the 12s budget.
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return c.Status(fiber.StatusGatewayTimeout).JSON(fiber.Map{
				"error": "the telemetry service took too long to respond",
			})
		}
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error": "the telemetry service is unreachable",
		})
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, telemetryResponseLimit+1))
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "the telemetry service response could not be read"})
	}
	if len(payload) > telemetryResponseLimit {
		// Serving a truncated body as 200 would hand the console invalid JSON.
		logr.Warnf("[api.TelemetryProxy] upstream %s %s exceeded the %d byte response limit", c.Method(), upstreamPath, telemetryResponseLimit)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error": "the telemetry response was too large; narrow the range or lower the page size",
		})
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Never echo the upstream body: FastAPI puts raw exception text in
		// `detail`, which would land in the console's error panel.
		logr.Warnf("[api.TelemetryProxy] upstream %s %s returned %d", c.Method(), upstreamPath, response.StatusCode)
		status := response.StatusCode
		if status >= 500 {
			// Collapse every upstream 5xx to 502. Passing a raw 503 through
			// would be indistinguishable from the gate's "not configured" 503.
			status = fiber.StatusBadGateway
		}
		return c.Status(status).JSON(fiber.Map{
			"error": telemetryStatusMessage(response.StatusCode),
		})
	}
	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = fiber.MIMEApplicationJSON
	}
	c.Set(fiber.HeaderContentType, contentType)
	// The upstream's Content-Type is echoed and transcripts are attacker-supplied
	// through the direct ingest path, so pin sniffing off.
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Status(response.StatusCode).Send(payload)
}

// telemetryRequireRevoked refuses to delete an ingest key that is still active.
// It returns errTelemetryKeyActive or errTelemetryKeyUnknown for decisions about
// the key itself, and any other error for a failure to reach the upstream, so
// the caller can pick the right status code.
func telemetryRequireRevoked(ctx context.Context, cfg telemetryConfig, id string) error {
	if id == "" {
		return errTelemetryKeyUnknown
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.upstream+telemetryUpstreamPrefix+"/keys", nil)
	if err != nil {
		return fmt.Errorf("building the key-state request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if cfg.upstreamToken != "" {
		request.Header.Set("Authorization", "Bearer "+cfg.upstreamToken)
	}
	response, err := telemetryHTTPClient.Do(request)
	if err != nil {
		return fmt.Errorf("listing keys: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("listing keys returned %d", response.StatusCode)
	}
	// GET /keys returns {"keys":[...]}, not a bare array.
	var payload struct {
		Keys []struct {
			ID      json.Number `json:"id"`
			Revoked bool        `json:"revoked"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, telemetryResponseLimit)).Decode(&payload); err != nil {
		return fmt.Errorf("decoding the key list: %w", err)
	}
	for _, key := range payload.Keys {
		if key.ID.String() == id {
			if !key.Revoked {
				return errTelemetryKeyActive
			}
			return nil
		}
	}
	return errTelemetryKeyUnknown
}

func telemetryStatusMessage(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "the telemetry service rejected the request"
	case http.StatusUnauthorized, http.StatusForbidden:
		return "Gatus is not authorized to read the telemetry service"
	case http.StatusNotFound:
		return "not found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusTooManyRequests:
		return "the telemetry service is rate limiting requests"
	default:
		if status >= 500 {
			return "the telemetry service returned an error"
		}
		return "the telemetry request could not be completed"
	}
}
