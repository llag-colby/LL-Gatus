# Config load, watch and reload — what is actually possible at runtime

Captured 2026-09-17 while scoping the dashboard-customization build.

## Verdict

- **The code supports a full in-process hot-swap**, `external-endpoints`
  included. `main.go:245-271` polls every 30s and, on a real file change, calls
  `stop(cfg)` then `start(newCfg)`, rebuilding the router, the watchdog, storage
  and metrics against the new config.
- **This deployment can never trigger it.** `Dockerfile:29` bakes config.yaml to
  `/config/config.yaml`, `Dockerfile:31` sets `GATUS_CONFIG_PATH=""` so the
  default path resolves there, and `docker-compose.yml` mounts only `./data`.
  The file's mtime is frozen at image build time, so
  `HasLoadedConfigurationBeenModified()` returns false on every tick, forever.
  That applies to `endpoints`, `ui`, `storage` and `alerting` too, not just
  external endpoints.
- The `config.yaml:244` comment about external-endpoints needing a full recreate
  is **true in effect, wrong in its stated reason**. External endpoints are read
  per-request and uncached (`config/config.go:162-170`,
  `api/external_endpoint.go:37`); the recreate is needed because the file cannot
  change, not because the section is boot-only. It is singled out because that
  failure is loud: an unregistered key gets a hard 404.

## Reload hazards, if the watcher were ever enabled

- `stop()` runs at `main.go:250` BEFORE the new config is parsed at `:253`. With
  `skip-invalid-config-update: false` (the current default) an invalid edit
  panics after teardown, exits, and `restart: always` crash-loops. With the flag
  true it is worse: the process lives on with the HTTP server and watchdog dead,
  permanently, which nothing can detect.
- The SIGTERM handler (`main.go:47-53`) closes over the ORIGINAL cfg, so after a
  reload it shuts down stale tunnels and leaks the new ones.
- `jira.StartPoller()` is not idempotent; `newSSEHub()` has no stop channel. Each
  reload leaks one of each.
- `initializeStorage` re-runs and is destructive by design:
  `DeleteAllEndpointStatusesNotInKeys` (`main.go:150`) deletes the history of
  anything no longer in config, across endpoints, external endpoints and suite
  endpoints.

## Config directory support (confirmed)

`config/config.go:220-239` walks a directory, keeps `.yml`/`.yaml`, and folds
files together with `deepmerge.YAML`. Maps deep-merge, **slices append**, and a
primitive defined twice is an ERROR rather than last-wins. So multiple files can
each contribute endpoints. Two traps: the walk uses the raw env value rather than
the resolved path (`:221`), and **file deletion is never detected** (`:180-188`),
so removal has to be done by rewriting a file, not unlinking it.

## Ranked mechanisms for runtime add/remove

1. **External endpoints via a `/data` overlay — lowest risk, ~40 lines.** Have
   `GetExternalEndpointByKey` consult the overlay when the YAML scan misses. Works
   today because the lookup is per-request and uncached, and the SQL store
   auto-creates the endpoint row on first insert. Two musts: fold overlay keys
   into the preserve-list at `main.go:140-142` or the next boot wipes their
   history, and teach `isKnownEndpointKey` (`api/monitoring.go:59-68`) about them.
2. **Probed (ICMP/HTTP) endpoints — medium.** Adding one means spawning
   `monitorEndpoint` against the live ctx plus `ValidateAndSetDefaults()`.
   Removing one needs per-endpoint cancellation, which **does not exist**:
   `watchdog` has a single global `cancelFunc` (`watchdog/watchdog.go:23`). A
   per-endpoint cancel map is the prerequisite.
3. **Write config.yaml and let the watcher reload — do not.** Needs a `/config`
   mount and inherits every hazard above, including the 30s latency and the
   destructive prune.

## Other facts worth keeping

- `parseAndValidateConfigBytes` expands `${VAR}` via `os.ExpandEnv`
  (`config/config.go:285-289`), which is why `${PHONES_PUSH_TOKEN}` resolves —
  and why an `.env` change also needs a process restart.
- Hard gate at `config/config.go:295-296`: zero endpoints AND zero suites is an
  error. `external-endpoints` alone does not satisfy it.
- `update.sh:67` uses `--force-recreate`, which does pick up a new image's
  config. The plain `docker compose up -d --build` in DEPLOY.md only recreates
  when the image digest changes.
