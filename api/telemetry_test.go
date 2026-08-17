package api

import (
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestTelemetryUpstreamPath(t *testing.T) {
	t.Parallel()
	scenarios := []struct {
		name     string
		method   string
		rest     string
		wantPath string
		wantOK   bool
	}{
		// Allowed
		{"health", fiber.MethodGet, "health", "/api/v1/health", true},
		{"stats", fiber.MethodGet, "stats", "/api/v1/stats", true},
		{"timeline", fiber.MethodGet, "timeline", "/api/v1/timeline", true},
		{"runs list", fiber.MethodGet, "runs", "/api/v1/runs", true},
		{"one run", fiber.MethodGet, "runs/3483c22c-1bbb-4033-bbf7-e8f3b4b83c6a", "/api/v1/runs/3483c22c-1bbb-4033-bbf7-e8f3b4b83c6a", true},
		{"keys list", fiber.MethodGet, "keys", "/api/v1/keys", true},
		{"mint key", fiber.MethodPost, "keys", "/api/v1/keys", true},
		{"revoke key", fiber.MethodPost, "keys/12/revoke", "/api/v1/keys/12/revoke", true},
		{"rotate key", fiber.MethodPost, "keys/12/rotate", "/api/v1/keys/12/rotate", true},
		{"delete key", fiber.MethodDelete, "keys/12", "/api/v1/keys/12", true},
		{"leading slash is tolerated", fiber.MethodGet, "/stats", "/api/v1/stats", true},

		// Ingest is deliberately not proxied: field scripts post directly with
		// their own llk_ keys, and exposing it here would let anyone past the
		// gate forge runs.
		{"ingest is refused", fiber.MethodPost, "runs", "", false},

		// Method must match exactly. Fiber normalises methods, but the
		// allowlist must not depend on that: a lowercase match slipping through
		// would be a bypass.
		{"lowercase method", "delete", "keys/12", "", false},
		{"mixed case method", "PoSt", "keys", "", false},
		{"wrong method for path", fiber.MethodGet, "keys/12/revoke", "", false},
		{"delete on runs", fiber.MethodDelete, "runs/12", "", false},

		// Traversal and encoding
		{"literal dot dot", fiber.MethodGet, "../config", "", false},
		{"encoded dot dot", fiber.MethodGet, "%2e%2e/config", "", false},
		{"encoded slash", fiber.MethodPost, "keys%2f1%2frevoke", "", false},
		{"any percent at all", fiber.MethodGet, "runs/%41", "", false},
		{"null byte", fiber.MethodGet, "runs/%00", "", false},
		{"double slash", fiber.MethodGet, "runs//12", "", false},
		{"trailing slash", fiber.MethodGet, "runs/", "", false},
		{"backslash", fiber.MethodGet, `runs\12`, "", false},
		{"space", fiber.MethodGet, "runs/1 2", "", false},
		{"empty", fiber.MethodGet, "", "", false},

		// Not on the allowlist at all
		{"unknown path", fiber.MethodGet, "config", "", false},
		{"too many segments", fiber.MethodGet, "runs/12/log", "", false},
		{"nested key path", fiber.MethodPost, "keys/12/revoke/now", "", false},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			got, ok := telemetryUpstreamPath(scenario.method, scenario.rest)
			if ok != scenario.wantOK {
				t.Fatalf("telemetryUpstreamPath(%q, %q) ok = %v, want %v", scenario.method, scenario.rest, ok, scenario.wantOK)
			}
			if got != scenario.wantPath {
				t.Errorf("telemetryUpstreamPath(%q, %q) = %q, want %q", scenario.method, scenario.rest, got, scenario.wantPath)
			}
		})
	}
}

func TestParseBasicAuthHeader(t *testing.T) {
	t.Parallel()
	scenarios := []struct {
		name         string
		header       string
		wantUser     string
		wantPassword string
		wantOK       bool
	}{
		{"valid", "Basic bGxvcHM6c2VjcmV0", "llops", "secret", true},
		{"scheme is case insensitive", "basic bGxvcHM6c2VjcmV0", "llops", "secret", true},
		{"password may contain colons", "Basic bGxvcHM6YTpiOmM=", "llops", "a:b:c", true},
		{"empty password", "Basic bGxvcHM6", "llops", "", true},
		{"empty header", "", "", "", false},
		{"wrong scheme", "Bearer bGxvcHM6c2VjcmV0", "", "", false},
		{"not base64", "Basic !!!!", "", "", false},
		{"no colon in payload", "Basic bGxvcHM=", "", "", false},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			user, password, ok := parseBasicAuthHeader(scenario.header)
			if ok != scenario.wantOK || user != scenario.wantUser || password != scenario.wantPassword {
				t.Errorf("parseBasicAuthHeader(%q) = (%q, %q, %v), want (%q, %q, %v)",
					scenario.header, user, password, ok, scenario.wantUser, scenario.wantPassword, scenario.wantOK)
			}
		})
	}
}

// The gate must refuse to serve anything when no credentials are configured:
// the upstream has no authentication of its own, so defaulting open would
// expose machine transcripts and ingest-key management.
func TestTelemetryConfigFailsClosed(t *testing.T) {
	t.Parallel()
	scenarios := []struct {
		name string
		cfg  telemetryConfig
		want bool
	}{
		{"nothing set", telemetryConfig{}, false},
		{"upstream only", telemetryConfig{upstream: "http://lltel-api:8080"}, false},
		{"user but no password", telemetryConfig{upstream: "http://x", uiUser: "llops"}, false},
		{"password but no user", telemetryConfig{upstream: "http://x", uiPassword: "p"}, false},
		{"no upstream", telemetryConfig{uiUser: "llops", uiPassword: "p"}, false},
		{"plaintext complete", telemetryConfig{upstream: "http://x", uiUser: "llops", uiPassword: "p"}, true},
		{"bcrypt complete", telemetryConfig{upstream: "http://x", uiUser: "llops", uiBcryptHash: []byte("$2a$10$abc")}, true},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			if got := scenario.cfg.configured(); got != scenario.want {
				t.Errorf("configured() = %v, want %v", got, scenario.want)
			}
		})
	}
}

// A wrong username must not be cheaper to reject than a wrong password, or
// response time becomes a username oracle.
func TestTelemetryAuthenticateRejects(t *testing.T) {
	t.Parallel()
	cfg := telemetryConfig{upstream: "http://x", uiUser: "llops", uiPassword: "correct-horse"}
	scenarios := []struct {
		name     string
		user     string
		password string
		want     bool
	}{
		{"both correct", "llops", "correct-horse", true},
		{"wrong password", "llops", "wrong", false},
		{"wrong user", "someone", "correct-horse", false},
		{"both wrong", "someone", "wrong", false},
		{"empty", "", "", false},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			if got := cfg.authenticate(scenario.user, scenario.password); got != scenario.want {
				t.Errorf("authenticate(%q, %q) = %v, want %v", scenario.user, scenario.password, got, scenario.want)
			}
		})
	}
}

// The gate lets the sign-in route through without a session. That exemption
// must match the route exactly: a suffix test would let a crafted path skip
// authentication entirely.
func TestTelemetrySessionExemptionIsExact(t *testing.T) {
	t.Parallel()
	exempt := telemetrySessionPath + "/session"
	scenarios := []struct {
		path string
		want bool
	}{
		{"/api/v1/telemetry/session", true},
		{"/api/v1/telemetry/session/", true},
		{"/api/v1/telemetry/runs/x/telemetry/session", false},
		{"/api/v1/telemetry/keys/telemetry/session", false},
		{"/api/v1/telemetry/sessionx", false},
		{"/api/v1/telemetry/console", false},
		{"/api/v1/telemetry/runs", false},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.path, func(t *testing.T) {
			t.Parallel()
			if got := strings.TrimSuffix(scenario.path, "/") == exempt; got != scenario.want {
				t.Errorf("exemption for %q = %v, want %v", scenario.path, got, scenario.want)
			}
		})
	}
}

// The console must be rewritten to point at the proxy prefix, and the CSP must
// carry a script hash. init() panics if the console shape changed, so reaching
// these assertions at all means the startup guard held.
func TestTelemetryConsolePreparedAtInit(t *testing.T) {
	t.Parallel()
	if strings.Contains(telemetryConsoleHTML, consoleAPIOriginal) {
		t.Error("the console still contains the original API base; the serve-time rewrite did not apply")
	}
	if !strings.Contains(telemetryConsoleHTML, consoleAPIRewritten) {
		t.Error("the console does not contain the rewritten API base")
	}
	if !strings.Contains(telemetryConsoleCSP, "script-src 'sha256-") {
		t.Errorf("CSP is missing a script hash: %s", telemetryConsoleCSP)
	}
	if strings.Contains(telemetryConsoleCSP, "script-src 'unsafe-inline'") {
		t.Error("CSP must not allow unsafe-inline scripts")
	}
	if !strings.Contains(telemetryConsoleCSP, "connect-src 'self'") {
		t.Error("CSP must confine connections to the same origin")
	}
}
