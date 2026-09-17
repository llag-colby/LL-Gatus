# Persisted JSON state in /data — the pattern to copy for a fifth store

Captured 2026-09-17 while scoping the dashboard-customization build. Subagents
run read-only, so this is written up here rather than by the agent itself.

## The four existing stores

| File | Owner | Lock | Atomic write | `:key` validated |
|---|---|---|---|---|
| `/data/monitoring.json` | `monitoring/monitoring.go:30` | RWMutex | No (`os.WriteFile`) | Yes (`isKnownEndpointKey`) |
| `/data/phones_exclusions.json` | `api/phones_inventory.go:32` | RWMutex | No | No |
| `/data/phones_settings.json` | `api/phones_settings.go:29` | RWMutex | No | No |
| `/data/unifi_settings.json` | `api/unifi_settings.go:42` | RWMutex | **Yes** (tmp + rename) | No |

Shared shape: package-level `mu` + data + `loaded bool`, a path const, a file
struct, a lazy `ensureXLoaded()` (nothing is loaded at boot because `/data` only
exists at runtime — `monitoring.go:18-20`), and a `persistX()` contracted with
`// caller holds mu`. There is **no** shared helper; all four roll their own.
`phones_settings.go` and `unifi_settings.go` duplicate the whole
`{global, overrides, effective}` triple and the scope/clear dispatch.

## Copy `api/unifi_settings.go`, then fix what even it gets wrong

It is the only one with the atomic write (`:87-94`) and the only one that clamps
on read as well as write (`:64`, `:68`) because the file is hand-editable in
`./data` on the host.

Pitfalls, in the order they bite:

1. **Non-atomic write.** A truncated file parses as "no file", which silently
   reverts every setting to defaults. Use tmp + rename. (Note: rename-atomic
   against a process crash, not against host power loss — no `f.Sync()`.)
2. **`loaded = true` is set BEFORE the read** in all four. A corrupt file is
   never retried, and the next write overwrites it with defaults, destroying the
   user's config. For a layout store, rename the bad file to `.corrupt` first.
3. **`/data` may not exist.** `FROM scratch` image; the directory exists only
   because compose mounts it. No store calls `os.MkdirAll` — which is why writes
   silently vanish outside Docker.
4. **Validate the key.** Copy `isKnownEndpointKey` (`api/monitoring.go:59-69`),
   which needs the handler to be a closure over `*config.Config`. Fix its two
   flaws while copying: `url.QueryUnescape` the param (as `api/history.go:67`
   does) and `strings.ToLower` before comparing, since the endpoint branch
   compares raw while `GetExternalEndpointByKey` lowercases.
5. **Clamp on read and on write.**
6. **Add a `version` field.** None of the four have one, so any shape change is
   an un-migratable silent reinterpretation. A layout will evolve.
7. **`persist` is contracted, not enforced** — never call it from a read path.
8. **Bound the growth.** Nothing prunes keys when an endpoint leaves
   `config.yaml`; entries accumulate forever.
9. **A 200 currently means "accepted", not "saved"** — all four log and swallow
   write errors. For user-visible config, return 500 on a failed rename.
10. **Never add `-x` to `update.sh`'s `git clean -fd`** — that one flag deletes
    `/data` and `.env` together.
11. **Factor it out.** Four copies is past the threshold; the fifth store should
    extract `persistJSONAtomic(path, v)` and retrofit the three non-atomic
    writers, and a generic `settingsStore[T]` would collapse phones + unifi.

## Why /data is the right home

`docker-compose.yml:12-13` bind-mounts `./data:/data`; `.gitignore:26-27` ignores
`/data/`. `update.sh` runs `git reset --hard` (tracked files only) and
`git clean -fd` **without `-x`**, so it respects `.gitignore` and leaves `data/`,
`.env` and the collector state files alone. A store there survives a restart, a
`--build`, and a prod update.
