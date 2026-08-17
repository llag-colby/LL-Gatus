# Explorer 6 — Configuration, Secrets, Build & Deployment Map

Repo: `C:\Users\colby.west\Desktop\Projects\Gatus` (fork of TwiN/gatus v5, remote `https://github.com/llag-colby/LL-Gatus.git`, branch `main`).
Purpose: groundwork for an **LL-Telemetry** integration needing a telemetry server base URL + read credentials, possibly a new container.

> **STANDING RULE — LOCAL ONLY.** Edit local docker/compose config only. Never push/deploy to prod. See §9 for the exact local/prod boundary and the list of prod-targeting artifacts to avoid.

---

## 1. `config.yaml` (root) — structure

Path: `C:\Users\colby.west\Desktop\Projects\Gatus\config.yaml` (243 lines, currently modified vs HEAD).

**Important:** `config.yaml` is listed in `.gitignore:21` **but is still git-tracked** (`git ls-files config.yaml` → hit). Gitignore does not apply to already-tracked files, so edits to it DO show up in `git status` and WILL be committed if you `git add -A`.

### Sections present (only 4)

| Lines | Section | Contents |
|---|---|---|
| 1–9 | `storage:` | `type: sqlite`, `path: /data/data.db`, `maximum-number-of-results: 3000` |
| 11–28 | `ui:` | `title`, `header`, base64 inline SVG `logo`, `favicon` map, `custom-css` block |
| 30–162 | `endpoints:` | 22 ICMP WAN ping endpoints, `interval: 60s`, `conditions: ["[CONNECTED] == true"]` |
| 164–243 | `external-endpoints:` | 17 push receivers (Phones ×11, Firewall ×3, Wireless ×3) |

There is **no** `alerting:`, `security:`, `web:`, `maintenance:`, `remote:`, `connectivity:`, `tunneling:`, `announcements:`, or `suites:` section. And critically — **no `jira:` section**. Jira is 100% environment-variable driven (§6).

### Endpoint naming convention (load-bearing)

The dashboard groups **cards by endpoint `name`** and rows by `group`. So:
- `name: Muscle Shoals` + `group: WAN 1 (Comcast Fiber)` → the "WAN 1" row of the Muscle Shoals card.
- `name: Ivory Tower` + `group: Phones` → the "Phones" row of the Ivory Tower card.

Endpoint key = `slug(group) + "_" + slug(name)` — see `config/key/key.go:6-20`:

```go
func ConvertGroupAndNameToKey(groupName, name string) string {
	return sanitize(groupName) + "_" + sanitize(name)
}
```
`sanitize` lowercases and replaces `/ _ . , space # + &` with `-`. So `group=Firewall`, `name=Decatur GMC` → `firewall_decatur-gmc`.

### The custom features in `config.yaml`

**Phones** — `config.yaml:164-207`. Comment block at 164–172 explains the design. Eleven entries, all identical shape:
```yaml
external-endpoints:
  - name: Ivory Tower
    group: Phones
    token: "${PHONES_PUSH_TOKEN}"
```
Keys produced: `phones_ivory-tower`, `phones_alabaster`, … `phones_tuscumbia`.

**UniFi** — `config.yaml:209-243`. Comment block at 209–223. Six entries across two groups:
- `group: Firewall` → Alabaster, Decatur GMC, Decatur KIA (lines 224–232)
- `group: Wireless` → Decatur GMC, Decatur KIA, Ivory Tower (lines 235–242)

All six also use `token: "${PHONES_PUSH_TOKEN}"`. The comment at 221–223 documents this deliberately: *"The push token is deliberately PHONES_PUSH_TOKEN rather than a new secret: it is the same collector trust boundary, and every deployed box already has it set."*

**Jira** — absent from `config.yaml` entirely.

### CRITICAL operational note (repeated 3× in the file)

> `# NOTE: external-endpoints only (re)load on 'docker compose down && up -d'.`
> (`config.yaml:172`, `config.yaml:223`; also `docs/unifi-monitor.md:159` and `:256`)

A plain `docker compose restart` or `up -d --build` does **not** register new external endpoints. **LL-Telemetry must plan for a full down/up if it adds external-endpoint entries.**

### `config/` directory (two meanings — do not confuse)

1. **`config/` Go package** at repo root — the config code (§5).
2. **`config/config.yaml` inside the container** — the Dockerfile copies the root `config.yaml` to `./config/config.yaml` in the image (`Dockerfile:21`), matching `config.DefaultConfigurationFilePath = "config/config.yaml"` (`config/config.go:38`). There is **no** `config/config.yaml` on disk in the working tree; `.gitignore:20` ignores `config/config.yml`.

`config/` package contents: `config.go` (25 KB), `config_test.go` (89 KB), `util.go`, and sub-packages `announcement/ connectivity/ endpoint/ gontext/ key/ maintenance/ remote/ suite/ tunneling/ ui/ web/`.

---

## 2. `.env` and `.env.example` — every key

`.env` is at repo root, **untracked** (`.gitignore:30-33` — `.env`, `.env.*`, but `!.env.example`). Confirmed: `git ls-files .env` → not known to git. Values redacted below.

### `.env.example` (11 lines, tracked) — verbatim

```
# Copy to .env and fill in real values. .env is gitignored (never committed).
# On the prod box, create this ONCE — `update.sh` (git clean -fd) preserves it.

# Wildix global API key (reserved for future use)
WILDIX_API_KEY=wsk-v1-your-key-here

# Phones — Ivory Tower PBX access token (Wildix Simple Token; the collector reads the PBX with it)
PHONES_IVORY_TOWER_TOKEN=access_your-pbx-token-here

# Phones — shared PUSH token. Same value in config.yaml (external-endpoints) and the collector.
PHONES_PUSH_TOKEN=generate-with-openssl-rand-hex-20
```
`.env.example` is **badly out of date** — it lists 3 keys; the real `.env` has 17. It documents nothing about Jira or UniFi.

### Live `.env` — all keys, by line

| Line | Key | Feature | Purpose |
|---|---|---|---|
| 7 | `JIRA_API_TOKEN` | Jira | Atlassian API token (HTTP Basic password half) |
| 9 | `JIRA_BASE_URL` | Jira | e.g. `https://longlewis.atlassian.net`, no trailing slash |
| 11 | `JIRA_EMAIL` | Jira | Atlassian account email owning the token (Basic username half) |
| 13 | `JIRA_PROJECTS` | Jira | Comma-separated project keys |
| 15–18 | *(commented out)* | Jira | `JIRA_TYPE_INCIDENT`, `JIRA_TYPE_SERVICE_REQUEST`, `JIRA_TYPE_PROBLEM`, `JIRA_SLA_MAX` |
| 23 | `PHONES_PUSH_TOKEN` | Phones + UniFi | Shared push secret; `config.yaml` interpolates it into every external-endpoint token |
| 26 | `WILDIX_API_KEY` | Phones | Wildix global key — reserved, currently unused by any code |
| 29 | `PHONES_IVORY_TOWER_TOKEN` | Phones | Wildix "Simple Token" per PBX |
| 32 | `PHONES_ALABASTER_TOKEN` | Phones | " |
| 35 | `PHONES_BESSEMER_TOKEN` | Phones | " |
| 38 | `PHONES_CULLMAN_TOKEN` | Phones | " (see memory: Cullman's real PBX is `longlewiscu`) |
| 41 | `PHONES_FLORENCE_TOKEN` | Phones | " |
| 44 | `PHONES_HOOVER_TOKEN` | Phones | " |
| 47 | `PHONES_MUSCLE_SHOALS_TOKEN` | Phones | " |
| 50 | `PHONES_PRATTVILLE_TOKEN` | Phones | " |
| 53 | `PHONES_TUSCUMBIA_TOKEN` | Phones | " |
| 56 | `PHONES_PUSH_TOKEN` | Phones | **duplicate of line 23** (later wins in docker `env_file`) |
| 60 | `PHONES_DECATUR_TOKEN` | Phones | One token serves Decatur GMC + Decatur KIA |
| 65 | `UNIFI_API_KEY` | UniFi | Global Site Manager key from unifi.ui.com (32 chars) |

### Env vars read by code but NOT present in `.env` (defaults apply)

Jira: `JIRA_PROJECT` (legacy fallback), `JIRA_POLL_SECONDS`, `JIRA_TREND_DAYS`, `JIRA_MAX_ISSUES`, `JIRA_SLA_MAX`, `JIRA_SLA_PROJECTS`, `JIRA_BOARD_POLL_SECONDS`, `JIRA_BOARD_MAX_CARDS`, `JIRA_BOARD_DONE_DAYS`.
UniFi: `UNIFI_PUSH_TOKEN` (falls back to `PHONES_PUSH_TOKEN`), `UNIFI_DECATUR_API_KEY` (legacy fallback for `UNIFI_API_KEY`), `UNIFI_TX_RETRY_DEGRADED_PCT`.
Phones: `PHONES_DEGRADED_AT`, `PHONES_STATE_FILE`, `VERIFY_TLS`.
Collector-generic: `GATUS_PUSH_BASE`, `LOOP`, `SWEEP_MIN`, `SWEEP_MAX`, `SWEEP_POLL`.
Gatus core: `GATUS_CONFIG_PATH`, `GATUS_CONFIG_FILE` (deprecated), `GATUS_LOG_LEVEL`, `GATUS_DELAY_START_SECONDS`, `ENVIRONMENT`, `PORT`, `TZ`.

### Naming convention for custom vars — the rule to follow

```
<FEATURE>_<SCOPE>_<KIND>
```
- Feature prefix, screaming snake case: `JIRA_`, `PHONES_`, `UNIFI_`, `WILDIX_`.
- Connection basics named plainly: `_BASE_URL`, `_API_TOKEN`, `_API_KEY`, `_EMAIL`.
- Per-site secrets embed the site: `PHONES_<SITE>_TOKEN` (`PHONES_MUSCLE_SHOALS_TOKEN`).
- Tuning knobs are optional-with-default and read inline: `_POLL_SECONDS`, `_MAX_ISSUES`, `_SWEEP_MIN`, `_DEGRADED_PCT`.
- Push direction is distinguished from read direction: `*_PUSH_TOKEN` (collector→Gatus) vs `*_API_TOKEN`/`*_API_KEY` (Gatus/collector→vendor).

**Recommended LL-Telemetry keys:** `TELEMETRY_BASE_URL`, `TELEMETRY_API_TOKEN` (or `TELEMETRY_API_KEY`), optionally `TELEMETRY_EMAIL` if Basic, `TELEMETRY_POLL_SECONDS`. If it pushes into Gatus, reuse `PHONES_PUSH_TOKEN` (the established precedent) or add `TELEMETRY_PUSH_TOKEN` with a `PHONES_PUSH_TOKEN` fallback — exactly what `unifi_collector.py:408-409` does.

---

## 3. `docker-compose.yml` — three services

Path: `docker-compose.yml` (61 lines). **Three services**, one network (the implicit compose default bridge — no `networks:` block anywhere).

### Service 1: `gatus` (lines 2–19)
```yaml
  gatus:
    build:
      context: .
      args:
        GIT_SHA: ${GIT_SHA:-dev}   # stamped into the build; shown at /api/v1/version
    image: ll-gatus:local
    container_name: gatus
    restart: always
    ports:
      - "8080:8080"
    volumes:
      - ./data:/data
    environment:
      - TZ=America/Chicago
    env_file:
      - .env   # injects PHONES_PUSH_TOKEN etc. so Gatus can expand ${...} in config.yaml
    cap_add:
      - NET_RAW   # required for ICMP (WAN ping) checks
```
- Built locally from the root `Dockerfile`; image tag `ll-gatus:local` — **not pushed to any registry**.
- `env_file: .env` is what makes `${PHONES_PUSH_TOKEN}` in `config.yaml` resolve (via `os.ExpandEnv`, §5) **and** what feeds the Go Jira poller its `JIRA_*` vars.
- `NET_RAW` needed for ICMP.
- Only `./data` is bind-mounted — config.yaml is **baked into the image**, not mounted. Config changes therefore require `--build`.

### Service 2: `phone-collector` (lines 23–39)
```yaml
    image: python:3-slim
    container_name: phone-collector
    restart: always
    depends_on: [gatus]
    env_file: [.env]
    environment:
      - LOOP=1
      - GATUS_PUSH_BASE=http://gatus:8080
      - PYTHONUNBUFFERED=1
      - TZ=America/Chicago
    volumes:
      - ./collector:/collector
    working_dir: /collector
    command: ["python", "phone_collector.py"]
```

### Service 3: `unifi-collector` (lines 45–61)
Identical shape; `command: ["python", "unifi_collector.py"]`, same `./collector:/collector` mount, same `GATUS_PUSH_BASE=http://gatus:8080`.

### The sidecar-collector pattern (the template for LL-Telemetry)

Both collectors use **stock `python:3-slim` with the script bind-mounted** — no Dockerfile, no image build, no `pip install` (both scripts use only stdlib `urllib`/`json`). Adding an LL-Telemetry collector container is a ~16-line copy-paste block plus a `.py` file in `collector/`, with **zero build cost**. This is by far the lowest-friction integration path.

Notes:
- `depends_on` is start-order only, no healthcheck/condition.
- No `networks:`/`healthcheck:`/`logging:` blocks anywhere; service DNS name `gatus` resolves on the default compose network.
- `restart: always` on all three.

---

## 4. Build & deploy scripts

### `Dockerfile` (27 lines) — two-stage
- Stage 1 `golang:alpine AS builder`: `go.mod`/`go.sum` copied first for layer caching (`:8-9`); cache mounts for `/go/pkg/mod` and `/root/.cache/go-build` (`:14-15`).
- Build line 16: `CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w -X github.com/TwiN/gatus/v5/api.Version=${GIT_SHA}" -o gatus .` — `GIT_SHA` (ARG, line 12, default `dev`) is stamped into `api.Version`, served at `/api/v1/version`.
- Stage 2 `FROM scratch`: copies the binary, **`config.yaml` → `./config/config.yaml`** (line 21), and CA certs. Env defaults: `GATUS_CONFIG_PATH=""`, `GATUS_LOG_LEVEL="INFO"`, `PORT="8080"`. `ENTRYPOINT ["/gatus"]`.
- `scratch` base ⇒ **no shell, no python, no debug tooling in the gatus container.** Any new sidecar must be its own container.
- `CGO_ENABLED=0` with sqlite storage — Gatus uses a pure-Go sqlite driver.

### `.dockerignore` (7 lines)
`.examples`, `Dockerfile`, `.github`, `.idea`, `.git`, `web/app`, `*.db`, `testdata`.
Note: `.env` is **not** excluded, so it lands in the build context (though nothing in the Dockerfile copies it into the image beyond `COPY . ./` in the builder stage — **it does end up in the discarded builder layer**, not in the final `scratch` image). `collector/` is also not excluded and gets copied into the builder stage unnecessarily.

### `Makefile` (54 lines) — upstream-inherited, mostly unused here
- `make install` → `go build -v -o gatus .`
- `make run` → `ENVIRONMENT=dev GATUS_CONFIG_PATH=./config.yaml go run main.go` (line 9) — **this is how you run Gatus locally against the root `config.yaml` without Docker.** `ENVIRONMENT=dev` enables CORS for `localhost:8081` (`api/api.go:58-63`).
- `make test` → `go test ./... -cover`
- `make frontend-install` / `frontend-build` / `frontend-dev` → npm in `web/app`. **`make frontend-build` is mandatory after any `web/app/` change** (output `web/static/` is `//go:embed`-ed).
- ⚠️ **`docker-build` / `docker-run` targets (lines 28-37) are upstream leftovers** tagging `twinproduction/gatus:latest`. Do not use — they conflict with the compose-managed `ll-gatus:local` and the `gatus` container name.

### `DEPLOY.md` (36 lines) — **PROD-targeting document**
Describes deployment on an Ubuntu box: `git clone https://github.com/llag-colby/LL-Gatus.git`, `cd LL-Gatus`, `docker compose up -d --build`, expects `"Validated 14 endpoints"` (stale — now 22). Update flow documented as `git pull && docker compose up -d --build`. **This describes the prod box workflow — read for context, do not execute.**

### `update.sh` (88 lines, executable) — **PROD deploy script, DESTRUCTIVE, DO NOT RUN**
Sequence:
1. `:20` record `BEFORE=$(git rev-list --count HEAD)` — build number is the commit count.
2. `:26-28` **`git fetch --prune origin main` → `git reset --hard origin/main` → `git clean -fd`**. This obliterates all local uncommitted work. Every file in the current `git status` (modified `api/api.go`, `jira/*`, new `api/unifi_inventory.go`, `collector/unifi_collector.py`, `jira/board.go`, `web/app/src/components/JiraKanban.vue`) would be **destroyed**.
3. `:43-49` pre-flight: warns if `.env` missing. `.env` survives `git clean -fd` because it is gitignored.
4. `:53-61` removes any `gatus`/`phone-collector` container not labeled `com.docker.compose.project` (non-compose orphans). Note: **`unifi-collector` is not in this list** — an orphan-check gap.
5. `:66-67` `export GIT_SHA="$BUILD"` then `docker compose up -d --build --force-recreate --remove-orphans`.
6. `:72-77` polls `http://localhost:8080/health`, then reads `/api/v1/version` and compares against the git commit count; prints `DEPLOYED OK ✔` or a mismatch warning.

Note `--force-recreate` does recreate containers, so external-endpoints DO reload under `update.sh` — but a bare `docker compose up -d --build` (the DEPLOY.md path) does not reliably.

### `env-merge.sh` (52 lines, executable) — **safe, non-destructive; the intended way to add new keys**
`./env-merge.sh SOURCE TARGET`. TARGET is master: only keys absent from TARGET are appended. Existing values are never changed or reordered. Backs up TARGET to `$DST.bak.YYYYmmdd-HHMMSS` before writing (`:46-47`). Idempotent. Skips comments/blanks; only `KEY=VALUE` lines. Header comment example (`:13-14`) is `./env-merge.sh .env.incoming .env` **"on the prod box"** — the script itself is harmless locally, but note the intent.

**This is the correct mechanism to introduce `TELEMETRY_*` keys**: write a `.env.incoming` with the new keys and merge, rather than hand-editing `.env`.

---

## 5. `config/` Go package — sections, validation, ordering

### The `Config` struct — `config/config.go:63-128`

Each section is a pointer to a sub-package `Config` with a yaml tag:
```go
	Security     *security.Config       `yaml:"security,omitempty"`     // :87
	Alerting     *alerting.Config       `yaml:"alerting,omitempty"`     // :90
	Endpoints    []*endpoint.Endpoint   `yaml:"endpoints,omitempty"`    // :93
	ExternalEndpoints []*endpoint.ExternalEndpoint `yaml:"external-endpoints,omitempty"` // :96
	Suites       []*suite.Suite         `yaml:"suites,omitempty"`       // :99
	Storage      *storage.Config        `yaml:"storage,omitempty"`      // :102
	Web          *web.Config            `yaml:"web,omitempty"`          // :105
	UI           *ui.Config             `yaml:"ui,omitempty"`           // :108
	Maintenance  *maintenance.Config    `yaml:"maintenance,omitempty"`  // :111
	Remote       *remote.Config         `yaml:"remote,omitempty"`       // :115
	Connectivity *connectivity.Config   `yaml:"connectivity,omitempty"` // :118
	Tunneling    *tunneling.Config      `yaml:"tunneling,omitempty"`    // :121
	Announcements []*announcement.Announcement `yaml:"announcements,omitempty"` // :124
```
Private trailing fields `configPath`, `lastFileModTime` at `:126-127`.

### `LoadConfiguration` — `config/config.go:199-258`
Tries in order: `$GATUS_CONFIG_PATH`, then `config/config.yaml`, then `config/config.yml` (`:204`). If the path is a **directory**, every `.yml`/`.yaml` under it is deep-merged via `deepmerge.YAML` (`:221-236`, `walkConfigDir` at `:261-279`) — so LL-Telemetry config could optionally live in a separate YAML file if the deployment switched to a config directory. Currently it does not: the Dockerfile bakes a single `config/config.yaml`.

### **Env-var expansion in YAML — `config/config.go:282-289`** (verbatim)
```go
func parseAndValidateConfigBytes(yamlBytes []byte) (config *Config, err error) {
	// Replace $$ with __GATUS_LITERAL_DOLLAR_SIGN__ to prevent os.ExpandEnv from treating "$$" as if it was an
	// environment variable. This allows Gatus to support literal "$" in the configuration file.
	yamlBytes = []byte(strings.ReplaceAll(string(yamlBytes), "$$", "__GATUS_LITERAL_DOLLAR_SIGN__"))
	// Expand environment variables
	yamlBytes = []byte(os.ExpandEnv(string(yamlBytes)))
	// Replace __GATUS_LITERAL_DOLLAR_SIGN__ with "$" to restore the literal "$" in the configuration file
	yamlBytes = []byte(strings.ReplaceAll(string(yamlBytes), "__GATUS_LITERAL_DOLLAR_SIGN__", "$"))
```
**This is the whole `${VAR}`-in-config mechanism.** Any `${TELEMETRY_BASE_URL}` placed in `config.yaml` resolves from the gatus container's process env, which comes from `env_file: .env`. An unset var expands to the **empty string** silently — no error.

### Validation ordering — `config/config.go:294-344` (verbatim)
```go
	// Check if the configuration file at least has endpoints configured
	if config == nil || (len(config.Endpoints) == 0 && len(config.Suites) == 0) {
		err = ErrNoEndpointOrSuiteInConfig
	} else {
		// XXX: Remove this in v6.0.0
		if config.Debug {
			logr.Warn("WARNING: The 'debug' configuration has been deprecated and will be removed in v6.0.0")
			logr.Warn("WARNING: Please use the GATUS_LOG_LEVEL environment variable instead")
		}
		// XXX: End of v6.0.0 removals
		ValidateAlertingConfig(config.Alerting, config.Endpoints, config.ExternalEndpoints)
		if err := ValidateSecurityConfig(config); err != nil {
			return nil, err
		}
		if err := ValidateEndpointsConfig(config); err != nil {
			return nil, err
		}
		if err := ValidateWebConfig(config); err != nil {
			return nil, err
		}
		if err := ValidateUIConfig(config); err != nil {
			return nil, err
		}
		if err := ValidateMaintenanceConfig(config); err != nil {
			return nil, err
		}
		if err := ValidateStorageConfig(config); err != nil {
			return nil, err
		}
		if err := ValidateRemoteConfig(config); err != nil {
			return nil, err
		}
		if err := ValidateConnectivityConfig(config); err != nil {
			return nil, err
		}
		if err := ValidateTunnelingConfig(config); err != nil {
			return nil, err
		}
		if err := ValidateAnnouncementsConfig(config); err != nil {
			return nil, err
		}
		if err := ValidateSuitesConfig(config); err != nil {
			return nil, err
		}
		if err := ValidateUniqueKeys(config); err != nil {
			return nil, err
		}
		ValidateAndSetConcurrencyDefaults(config)
		// Cross-config changes
		config.UI.MaximumNumberOfResults = config.Storage.MaximumNumberOfResults
	}
	return
}
```

**Hard ordering constraints (documented in-code):**
1. `ValidateAlertingConfig` **must precede** `ValidateEndpointsConfig` — provider default alerts must be parsed before endpoint defaults are set (`config/config.go:589-590`, also `AGENTS.md:19`).
2. `ValidateTunnelingConfig` **must follow** `ValidateEndpointsConfig` and `ValidateSuitesConfig` — it resolves tunnel refs in their client configs (`config/config.go:355-357`). *(Note: in the current source it actually runs at `:329`, **before** `ValidateSuitesConfig` at `:335` — a latent inconsistency between the comment and the call order. Not our problem, but don't be misled by it.)*
3. `ValidateUniqueKeys` runs last among the `Validate*` calls (`:338`) — it checks endpoint/external-endpoint/suite key collisions across all sections.
4. `config.UI.MaximumNumberOfResults = config.Storage.MaximumNumberOfResults` (`:343`) is a cross-section fixup and must be after both UI and Storage validation.
5. **Early-exit trap (`:295`):** if there are zero `endpoints` AND zero `suites`, it returns `ErrNoEndpointOrSuiteInConfig` and **no `Validate*` runs at all**. A telemetry-only config would never validate.

### Sub-package pattern — `ValidateAndSetDefaults()`

Two shapes exist. Simplest, `config/connectivity/connectivity.go:11-33`:
```go
var (
	ErrInvalidInterval  = errors.New("connectivity.checker.interval must be 5s or higher")
	ErrInvalidDNSTarget = errors.New("connectivity.checker.target must be suffixed with :53")
)

// Config is the configuration for the connectivity checker.
type Config struct {
	Checker *Checker `yaml:"checker,omitempty"`
}

func (c *Config) ValidateAndSetDefaults() error {
	if c.Checker != nil {
		if c.Checker.Interval == 0 {
			c.Checker.Interval = 60 * time.Second
		} else if c.Checker.Interval < 5*time.Second {
			return ErrInvalidInterval
		}
		if !strings.HasSuffix(c.Checker.Target, ":53") {
			return ErrInvalidDNSTarget
		}
	}
	return nil
}
```
And the nil-guarded wrapper in the parent, `config/config.go:348-353`:
```go
func ValidateConnectivityConfig(config *Config) error {
	if config.Connectivity != nil {
		return config.Connectivity.ValidateAndSetDefaults()
	}
	return nil
}
```
`config/remote/remote.go:24-37` is the same idea with a default-filling branch (`c.ClientConfig = client.GetDefaultConfig()`).

**Errors are package-level `var Err... = errors.New(...)` sentinels. Invalid config is FATAL — `main.go:31-34` panics on load error** (`AGENTS.md:20`).

### ▶ Exact steps to add a `telemetry:` config section

Only do this if LL-Telemetry needs *structured* YAML config. If it only needs a URL + credential, the env-var route (§6) is simpler and matches the Jira precedent.

1. **Create** `config/telemetry/telemetry.go`:
   ```go
   package telemetry

   import "errors"

   var ErrMissingBaseURL = errors.New("telemetry.base-url must be set")

   type Config struct {
       BaseURL  string `yaml:"base-url"`
       Username string `yaml:"username,omitempty"`
       Password string `yaml:"password,omitempty"`
       Interval time.Duration `yaml:"interval,omitempty"`
   }

   func (c *Config) ValidateAndSetDefaults() error {
       if c.Interval == 0 { c.Interval = 60 * time.Second }
       if len(c.BaseURL) == 0 { return ErrMissingBaseURL }
       return nil
   }
   ```
   Plus `config/telemetry/telemetry_test.go` (`AGENTS.md:78` — config structs should have tests).

2. **Import** — add `"github.com/TwiN/gatus/v5/config/telemetry"` to the import block at `config/config.go:19-27` (alphabetical: after `suite`, before `tunneling`).

3. **Add the field** — `config/config.go`, insert after line 121 (`Tunneling`), before line 124 (`Announcements`):
   ```go
   	// Telemetry is the configuration for the LL-Telemetry integration
   	Telemetry *telemetry.Config `yaml:"telemetry,omitempty"`
   ```

4. **Add the wrapper** — new func alongside the others (e.g. after `ValidateConnectivityConfig` at `config/config.go:353`):
   ```go
   func ValidateTelemetryConfig(config *Config) error {
   	if config.Telemetry != nil {
   		return config.Telemetry.ValidateAndSetDefaults()
   	}
   	return nil
   }
   ```

5. **Register the call** — `config/config.go`, insert **between line 334 and line 335** (i.e. after the `ValidateAnnouncementsConfig` block, before `ValidateSuitesConfig`), or anywhere after `ValidateEndpointsConfig` and before `ValidateUniqueKeys` at `:338`:
   ```go
   			if err := ValidateTelemetryConfig(config); err != nil {
   				return nil, err
   			}
   ```
   No ordering dependency unless telemetry references endpoints (if it does, it must come after `ValidateEndpointsConfig` at `:308`).

6. **Env expansion is free** — `${TELEMETRY_API_TOKEN}` in the YAML resolves automatically via `:287`. Secrets stay in `.env`; only the placeholder goes in tracked `config.yaml`.

7. **Startup hook** — if it needs a background poller, add `telemetry.StartPoller()` to `main.go:52-58` `start()`, next to `jira.StartPoller()` at `main.go:56`. ⚠️ `start()` is re-invoked by the hot-reload loop (`main.go:228-253` → `:250`), so the poller must be idempotent / not leak goroutines on reload — the Jira poller currently is *not* (it spawns an unbounded goroutine each time).

---

## 6. How custom features get secrets — the concrete trace

### ▶ Trace A: Jira (env-var only, no config struct) — **the recommended pattern for LL-Telemetry**

```
.env:7,9,11,13
  JIRA_API_TOKEN / JIRA_BASE_URL / JIRA_EMAIL / JIRA_PROJECTS
        │
        ▼  docker-compose.yml:16-17   env_file: - .env   (gatus service)
container process env
        │
        ▼  jira/jira.go:180-226       loadConfig() reads os.Getenv directly
jira.config struct (private, package-level, NOT config.Config)
        │
        ▼  jira/jira.go:228-230       configured() gate
        ▼  jira/jira.go:235-258       StartPoller()  ← called from main.go:56
        ▼  jira/jira.go:260+          poll(cfg) → HTTP Basic(email:token) to baseURL
        ▼  api/api.go:124-132         handlers serve the cached snapshot
```

**Verbatim, `jira/jira.go:159-178`** — the struct + helper:
```go
type config struct {
	baseURL      string
	email        string
	token        string
	projects     []string
	trendDays    int
	maxIssues    int // hard cap on issues fetched per query (pagination bound)
	pollInterval time.Duration
	slaMax       int // max open tickets to check per project for SLA breaches
	// projects that should have SLA data pulled (Jira Service Management).
	// Defaults to the first project (typically the service desk).
	slaProjects map[string]bool
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
```

**Verbatim, `jira/jira.go:180-230`** — the reading pattern (the thing to copy):
```go
func loadConfig() config {
	poll := 30
	if n, err := strconv.Atoi(os.Getenv("JIRA_POLL_SECONDS")); err == nil && n >= 15 {
		poll = n
	}
	trend := 14
	if n, err := strconv.Atoi(os.Getenv("JIRA_TREND_DAYS")); err == nil && n >= 5 && n <= 60 {
		trend = n
	}
	maxIssues := 500
	if n, err := strconv.Atoi(os.Getenv("JIRA_MAX_ISSUES")); err == nil && n >= 50 {
		maxIssues = n
	}
	slaMax := 60
	if n, err := strconv.Atoi(os.Getenv("JIRA_SLA_MAX")); err == nil && n >= 0 {
		slaMax = n
	}
	// Project list: JIRA_PROJECTS=LLSM,LLIP (falls back to legacy JIRA_PROJECT).
	raw := envOr("JIRA_PROJECTS", envOr("JIRA_PROJECT", "LLSM,LLIP"))
	var projects []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			projects = append(projects, p)
		}
	}
	...
	return config{
		baseURL:      strings.TrimRight(strings.TrimSpace(os.Getenv("JIRA_BASE_URL")), "/"),
		email:        strings.TrimSpace(os.Getenv("JIRA_EMAIL")),
		token:        strings.TrimSpace(os.Getenv("JIRA_API_TOKEN")),
		projects:     projects,
		trendDays:    trend,
		maxIssues:    maxIssues,
		pollInterval: time.Duration(poll) * time.Second,
		slaMax:       slaMax,
		slaProjects:  slaProjects,
	}
}

func (c config) configured() bool {
	return c.baseURL != "" && c.email != "" && c.token != ""
}
```

**The idioms worth copying verbatim for LL-Telemetry:**
- Secrets and base URL: `strings.TrimSpace(os.Getenv(...))`; base URL additionally `strings.TrimRight(..., "/")` (`:216`).
- Numeric knobs: `strconv.Atoi(os.Getenv(...))` guarded with `err == nil && n >= <floor>`, falling back to a literal default. Floors are enforced, so a bad value can't produce a hot loop.
- A `configured() bool` gate over the required trio.
- **Graceful no-op when unconfigured** — `jira/jira.go:236-241`:
  ```go
  	cfg := loadConfig()
  	if !cfg.configured() {
  		logr.Info("[jira.StartPoller] Jira is not configured (set JIRA_BASE_URL, JIRA_EMAIL, JIRA_API_TOKEN) — the /jira page will show a not-configured state")
  		setSnapshot(Snapshot{Configured: false, Status: "unknown"})
  		return
  	}
  ```
  Missing telemetry credentials must likewise degrade to a "not configured" UI state, **never** panic — because `main.go:31-34` panics only on config-file errors, and the Jira poller deliberately stays out of that path.
- Panic-recovery inside the ticker loop, `jira/jira.go:248-255`.
- Same pattern repeated in `jira/board.go:117-140` (`JIRA_BOARD_POLL_SECONDS` / `_MAX_CARDS` / `_DONE_DAYS`).

**Key takeaway: no custom feature in this repo has ever added a `config.Config` section.** Jira reads env directly in its own package; phones/UniFi carry a token through `external-endpoints` in `config.yaml`.

### ▶ Trace B: UniFi (collector-side, env → vendor API + push back)

```
.env:65  UNIFI_API_KEY
   │
   ▼  docker-compose.yml:51-52   env_file: .env  (unifi-collector service)
   ▼  collector/unifi_collector.py:406-410
   ▼  X-API-KEY header → https://api.ui.com     (read credentials)
   ▼  Bearer PHONES_PUSH_TOKEN → http://gatus:8080  (write back)
```
`collector/unifi_collector.py:406-410` — note the **fallback-chain idiom**:
```python
    api_key = (os.environ.get("UNIFI_API_KEY")
               or os.environ.get("UNIFI_DECATUR_API_KEY") or "").strip()
    push_token = (os.environ.get("UNIFI_PUSH_TOKEN")
                  or os.environ.get("PHONES_PUSH_TOKEN") or "").strip()
    base = os.environ.get("GATUS_PUSH_BASE", "http://localhost:8080")
```
`collector/unifi_collector.py:137-143` — the vendor read:
```python
def api(path, key):
    req = urllib.request.Request(API_BASE + path,
                                 headers={"X-API-KEY": key, "Accept": "application/json"})
    with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT) as resp:
        if resp.getcode() != 200:
            raise ValueError(f"{path} -> HTTP {resp.getcode()}")
        return json.loads(resp.read())
```

---

## 7. `collector/` Python collectors

Directory: `collector/` — `phone_collector.py` (19 KB, exec), `unifi_collector.py` (18.8 KB, exec), `README.md`, `.phones_state.json` (gitignored runtime state, `.gitignore:36`).

### How they run
**Primary: compose sidecar containers** (§3). `image: python:3-slim`, `LOOP=1`, script bind-mounted from `./collector`. `collector/README.md:21-34` calls this "recommended — no Python on the host".

**Alternatives supported by the scripts themselves:**
- One-shot: `python3 phone_collector.py` → single sweep, exit. Schedulable by cron.
- Daemon: `LOOP=1 python3 <script>.py` under systemd.
- Cron was the original design (`config.yaml:165` still says *"run by cron"*, and `collector/README.md:7` draws `cron ──> phone_collector.py`) — **stale comments; it is a container now.**

Loop cadence (jittered to avoid thundering herd):
- phones: `SWEEP_MIN=15`, `SWEEP_MAX=45` (`phone_collector.py:428-432`), plus a `SWEEP_POLL=2`s poll of the force-sweep queue.
- unifi: `SWEEP_MIN=30`, `SWEEP_MAX=60` (`unifi_collector.py:443-445`).

### Dependencies: **stdlib only**
Both import only `json, os, random, sys, time, urllib.error, urllib.parse, urllib.request` (+`ssl` in phones). No `requests`, no `pip install`, no `requirements.txt`. This is why bare `python:3-slim` works.

### `.env` auto-loading (host-run mode) — `unifi_collector.py:124-134`, mirrored at `phone_collector.py:~132-142`
```python
def load_dotenv():
    here = os.path.dirname(os.path.abspath(__file__))
    for path in (os.path.join(here, ".env"), os.path.join(here, "..", ".env")):
        if os.path.isfile(path):
            with open(path, "r", encoding="utf-8") as fh:
                for raw in fh:
                    line = raw.strip()
                    if line and not line.startswith("#") and "=" in line:
                        k, v = line.split("=", 1)
                        os.environ.setdefault(k.strip(), v.strip())
            return
```
Uses `setdefault`, so real env (from `env_file`) always wins over the file. Checks `collector/.env` then repo-root `.env`.

### Authentication
- **Phones → Wildix PBX:** per-site `Authorization: Bearer <PHONES_<SITE>_TOKEN>` over HTTPS. Site table is `LOCATIONS` in `phone_collector.py:44-120`, each entry carrying `pbx` host, `key`, and a `token_env` naming its env var (e.g. `"token_env": "PHONES_MUSCLE_SHOALS_TOKEN"` at `:105`). Read at `phone_collector.py:322`: `key, token = loc["key"], os.environ.get(loc["token_env"], "")`. Optional `VERIFY_TLS=0` escape hatch at `:145`.
- **UniFi → api.ui.com:** global `X-API-KEY` header, one key for all consoles (`unifi_collector.py:19-26, 137-143`). Site table is `SITES` at `unifi_collector.py:77-107`.
- **Both → Gatus:** `Authorization: Bearer <push token>`.

### How they push into Gatus (two channels per sweep)

**Channel 1 — pass/fail into the external endpoint** (drives the status bar, history, alerting):
`phone_collector.py:309-317`:
```python
def push_result(base, key, success, error, duration_ms, push_token):
    ...
    http(f"{base}/api/v1/endpoints/{key}/external?{urlencode(q)}", push_token, method="POST")
```
`unifi_collector.py:400`:
```python
        push(f"{base}/api/v1/endpoints/{key}/external?{urllib.parse.urlencode(q)}", push_token)
```

**Channel 2 — rich detail side-channel** (drives the drill-in views; **in-memory only, never persisted**):
`phone_collector.py:304-306`:
```python
def push_inventory(base, key, phones, status, counts, push_token):
    ...
    http(f"{base}/api/v1/phones/{key}", push_token, method="POST", body=body)
```
`unifi_collector.py:390`:
```python
        push(f"{base}/api/v1/unifi/{key}", push_token, body)
```

**Three-state trick:** collectors model healthy/degraded/down, but an external result carries only a bool. They report degraded as `success=true` **with an `error=` query param**, so it doesn't fire a down alert but renders amber. Documented at `api/external_endpoint.go:69-76`.

### Adding a site (the documented extension procedure)
`collector/README.md:41-46` for phones; `config.yaml:218-219` for UniFi: *"Adding one is two entries here plus a line in the collector's SITES list."*

### ▶ The LL-Telemetry precedent
This is a proven, low-friction template for an external data source:
1. Write `collector/telemetry_collector.py` (stdlib only, `load_dotenv()`, `LOOP`/`SWEEP_MIN`/`SWEEP_MAX`, `GATUS_PUSH_BASE`, a `SITES`/`TARGETS` table).
2. Add a ~16-line `telemetry-collector` service to `docker-compose.yml` (copy the `unifi-collector` block, lines 45–61).
3. Add `external-endpoints:` entries to `config.yaml` with `token: "${PHONES_PUSH_TOKEN}"` (or a new `${TELEMETRY_PUSH_TOKEN}`).
4. Add `TELEMETRY_BASE_URL` / `TELEMETRY_API_TOKEN` to `.env` via `env-merge.sh`.
5. If rich detail is needed, add `api/telemetry_inventory.go` modeled on `api/unifi_inventory.go` + two route registrations in `api/api.go`.
6. `docker compose down && docker compose up -d --build` (down/up required for external-endpoints).

**No Go build is required for steps 1–4** — a pure-collector integration touches zero Go code.

---

## 8. External endpoints — the push API

**Yes.** This config uses it heavily: 17 external endpoints across Phones / Firewall / Wireless (§1).

### Config shape — `config/endpoint/external_endpoint.go:24-38`
```go
type ExternalEndpoint struct {
	Enabled *bool  `yaml:"enabled,omitempty"`
	Name    string `yaml:"name"`
	Group   string `yaml:"group,omitempty"`
	// Token is the bearer token that must be provided through the Authorization header to push results to the endpoint
	Token   string `yaml:"token,omitempty"`
	Alerts  []*alert.Alert `yaml:"alerts,omitempty"`
	...
}
```

### Route — `api/api.go:99`
```go
	// This endpoint requires authz with bearer token, so technically it is protected
	unprotectedAPIRouter.Post("/v1/endpoints/:key/external", CreateExternalEndpointResult(cfg))
```
Registered on the **unprotected** router (before `ApplySecurityMiddleware` at `api/api.go:172`) because it does its own bearer-token check. This matters: the same trick is used by all the custom side-channels.

### Handler — `api/external_endpoint.go:19-109`
`POST /api/v1/endpoints/:key/external?success=true|false[&duration=<go-duration>][&error=<msg>]`
1. `:23-26` require `success` query param = `"true"`/`"false"` (else 400).
2. `:28-35` require `Authorization: Bearer <token>` (else 401).
3. `:36-41` look up `cfg.GetExternalEndpointByKey(key)` (`config/config.go:162-170`) — 404 if unknown. **Key must exist in `config.yaml` at process start.**
4. `:42-45` constant compare `externalEndpoint.Token != token` → 401.
5. `:51-54` if `monitoring.IsPaused(key)` → discard silently, return **200** ("an error status would make every collector log a warning on every sweep").
6. `:56-79` build `endpoint.Result{Timestamp, Success, Duration, Errors}`; `error` query param is recorded even on success (see the amber-warning comment at `:69-76`).
7. `:81-87` `store.Get().InsertEndpointResult(...)`.
8. `:89-103` maintenance-window check, then `watchdog.HandleAlerting(...)`.
9. `:104-106` publish Prometheus metrics if `cfg.Metrics`.

### The custom side-channel routes (all in `api/api.go`)
| Line | Route | Purpose |
|---|---|---|
| 99 | `POST /api/v1/endpoints/:key/external` | upstream push API |
| 102 | `POST /api/v1/phones/:key` | phones inventory push (`SetPhonesInventory`) |
| 105 | `GET /api/v1/phones/sweep-pending` | collector claims force-sweep requests |
| 106 | `POST /api/v1/phones/:key/sweep` | UI requests a force sweep |
| 107–111 | `GET/POST /api/v1/phones/:key[/exclusions|/settings]` | drill-in reads + config |
| 114–116 | `GET/POST /api/v1/monitoring[/:key]` | pause/resume monitoring per key |
| 120–122 | `GET /api/v1/unifi`, `POST/GET /api/v1/unifi/:key` | UniFi snapshots |
| 124–132 | `/api/v1/jira/*` | metrics, issue detail, SSE live, boards |

### Side-channel auth pattern — `api/unifi_inventory.go:44-58` (verbatim; copy this for telemetry)
```go
func SetUniFiSnapshot(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		key := c.Params("key")
		externalEndpoint := cfg.GetExternalEndpointByKey(key)
		if externalEndpoint == nil {
			return c.Status(404).SendString("not found")
		}
		authorizationHeader := string(c.Request().Header.Peek("Authorization"))
		if !strings.HasPrefix(authorizationHeader, "Bearer ") {
			return c.Status(401).SendString("invalid Authorization header")
		}
		token := strings.TrimSpace(strings.TrimPrefix(authorizationHeader, "Bearer "))
		if len(token) == 0 || externalEndpoint.Token != token {
			return c.Status(401).SendString("invalid token")
		}
```
It **reuses the external-endpoint's own token** — no new secret, no new auth mechanism. Storage is an in-memory `map[string]storedUniFi` guarded by a `sync.RWMutex` (`api/unifi_inventory.go:28-40`); snapshots are ephemeral by design (`:25-27`).

### Route-registration gotchas (`api/api.go`)
- **Static routes must be registered BEFORE `:key` routes**, or Fiber swallows them: see `:103-105` (`/v1/phones/sweep-pending` before `GET /v1/phones/:key`) and `:119-120` (`/v1/unifi` before `/v1/unifi/:key`). Same applies to any telemetry routes.
- **SSE routes must be excluded from the compress middleware** — `api/api.go:66-73`:
  ```go
  	app.Use(compress.New(compress.Config{
  		// Never compress the live SSE streams — they must stream unbuffered.
  		Next: func(c *fiber.Ctx) bool {
  			p := c.Path()
  			return strings.HasPrefix(p, "/api/v1/live") || p == "/api/v1/jira/live" ||
  				(strings.HasPrefix(p, "/api/v1/jira/board/") && strings.HasSuffix(p, "/live"))
  		},
  	}))
  ```
  If LL-Telemetry adds an SSE stream, its path **must** be added to this `Next` predicate.
- SPA routes are enumerated explicitly at `:134-138` (`/`, `/endpoints/:key`, `/suites/:key`, `/sites/:name`, `/jira`). A new `/telemetry` page needs a line here **and** a route in `web/app/src/router/index.js`.

---

## 9. ⚠️ LOCAL vs PROD — the line

### Safe / local
| Item | Note |
|---|---|
| `make run` (`Makefile:9`) | `ENVIRONMENT=dev GATUS_CONFIG_PATH=./config.yaml go run main.go` — runs against root config.yaml, no Docker |
| `make test` | `go test ./... -cover` |
| `make frontend-build` | required after any `web/app/` change |
| `docker compose up -d --build` | builds `ll-gatus:local` locally; **no registry push** |
| `docker compose down && up -d` | required to (re)load external-endpoints |
| Editing `docker-compose.yml`, `.env`, `config.yaml` | local files |
| `env-merge.sh` | non-destructive, backs up first |

### 🚫 Prod-targeting — DO NOT RUN / DO NOT TRIGGER
| Item | Why |
|---|---|
| **`update.sh`** (`update.sh:26-28`) | `git fetch && git reset --hard origin/main && git clean -fd` — **destroys all uncommitted work**, then rebuilds and recreates containers. This is the prod box's deploy script. |
| **`DEPLOY.md`** | Documents cloning to and deploying on the prod Ubuntu box (`git clone .../LL-Gatus.git`, `git pull && docker compose up -d --build`). Reference only. |
| **`git push` to `origin`** (`https://github.com/llag-colby/LL-Gatus.git`) | The prod box pulls from `origin/main`. Pushing IS deploying — `update.sh` force-syncs to `origin/main`. |
| `.github/workflows/publish-latest.yml` | Pushes images to Docker Hub + GHCR. Triggers on `test` workflow completing on branch **`master`**. This fork's branch is **`main`**, so it is currently inert — but do not rename the branch or add `main` to that trigger. Also gated on `secrets.DOCKER_USERNAME`/`DOCKER_PASSWORD`. |
| `.github/workflows/publish-release.yml` | Triggers on GitHub release published → Docker Hub + GHCR push. **Do not cut a release.** |
| `.github/workflows/publish-experimental.yml`, `publish-custom.yml` | `workflow_dispatch` only — manual. Do not dispatch. |
| `Makefile:28-37` `docker-build`/`docker-run` | Upstream leftovers tagging `twinproduction/gatus:latest`; conflicts with the compose stack. |
| `env-merge.sh` header (`:13-14`) | Example is "on the prod box". The script is safe, the documented usage context is prod. |

### Also note
- `.env` is **untracked and never committed**; it exists once per box and survives `git clean -fd`. New keys reach prod only by someone hand-adding them or running `env-merge.sh` there — **so any new required var is a manual prod step, and a missing one silently expands to `""`** (`config/config.go:287`). Make LL-Telemetry degrade gracefully when its vars are empty, exactly like `jira/jira.go:236-241`.
- `.env.example` **is** tracked and is 3 keys stale. Adding `TELEMETRY_*` keys there is the right way to signal the new requirement — but that is a tracked-file change, i.e. it lands in a commit that could be pushed. Keep it in the local working tree until the user decides.
- `config.yaml` is tracked despite being gitignored — edits WILL be committed by a blanket `git add`.
- Repo root contains stray local artifacts: `gatus-local.tar` (25 MB), `science-resp.log`, `scratch_*` — all gitignored (`.gitignore:22-25, 38`).

---

## 10. Quick file index

| Path | Role |
|---|---|
| `C:\Users\colby.west\Desktop\Projects\Gatus\config.yaml` | Runtime config: storage, ui, 22 endpoints, 17 external-endpoints |
| `C:\Users\colby.west\Desktop\Projects\Gatus\.env` | All secrets (untracked, 17 keys) |
| `C:\Users\colby.west\Desktop\Projects\Gatus\.env.example` | Tracked template (stale, 3 keys) |
| `C:\Users\colby.west\Desktop\Projects\Gatus\docker-compose.yml` | 3 services: gatus, phone-collector, unifi-collector |
| `C:\Users\colby.west\Desktop\Projects\Gatus\Dockerfile` | 2-stage golang:alpine → scratch; bakes config.yaml |
| `C:\Users\colby.west\Desktop\Projects\Gatus\.dockerignore` | 7 excludes |
| `C:\Users\colby.west\Desktop\Projects\Gatus\Makefile` | Local build/test/frontend targets |
| `C:\Users\colby.west\Desktop\Projects\Gatus\update.sh` | 🚫 PROD deploy, destructive |
| `C:\Users\colby.west\Desktop\Projects\Gatus\env-merge.sh` | Safe additive .env key merge |
| `C:\Users\colby.west\Desktop\Projects\Gatus\DEPLOY.md` | 🚫 PROD deploy doc |
| `C:\Users\colby.west\Desktop\Projects\Gatus\AGENTS.md` | Repo conventions; "Adding a Config Section" at :58-62 |
| `C:\Users\colby.west\Desktop\Projects\Gatus\main.go` | Entry; `start()` :52-58 calls `jira.StartPoller()` :56 |
| `C:\Users\colby.west\Desktop\Projects\Gatus\config\config.go` | Config struct :63-128; `parseAndValidateConfigBytes` :282-346 |
| `C:\Users\colby.west\Desktop\Projects\Gatus\config\key\key.go` | `ConvertGroupAndNameToKey` :6-20 (the slug rule) |
| `C:\Users\colby.west\Desktop\Projects\Gatus\config\endpoint\external_endpoint.go` | `ExternalEndpoint` struct :24-38 |
| `C:\Users\colby.west\Desktop\Projects\Gatus\config\connectivity\connectivity.go` | Simplest `ValidateAndSetDefaults` reference :21-33 |
| `C:\Users\colby.west\Desktop\Projects\Gatus\api\api.go` | All route registration :48-187 |
| `C:\Users\colby.west\Desktop\Projects\Gatus\api\external_endpoint.go` | Push API handler :19-109 |
| `C:\Users\colby.west\Desktop\Projects\Gatus\api\unifi_inventory.go` | Side-channel reference impl :44-115 |
| `C:\Users\colby.west\Desktop\Projects\Gatus\jira\jira.go` | Env-var secret pattern :159-230 |
| `C:\Users\colby.west\Desktop\Projects\Gatus\jira\board.go` | More env knobs :117-140 |
| `C:\Users\colby.west\Desktop\Projects\Gatus\collector\phone_collector.py` | LOCATIONS :44-120; push :304-317 |
| `C:\Users\colby.west\Desktop\Projects\Gatus\collector\unifi_collector.py` | SITES :77-107; env :406-410; push :390,400 |
| `C:\Users\colby.west\Desktop\Projects\Gatus\collector\README.md` | Collector deployment doc |
| `C:\Users\colby.west\Desktop\Projects\Gatus\docs\jira-monitor.md` | Jira env table :93-108 |
| `C:\Users\colby.west\Desktop\Projects\Gatus\docs\unifi-monitor.md` | UniFi env table :166-175 |
