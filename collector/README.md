# Phones collector

Push-based phone monitoring for LL-Gatus. Gatus never calls the PBX — this
script does, then POSTs a pass/fail into a Gatus **external endpoint**.

```
cron ──> phone_collector.py ──(HTTPS + Simple Token)──> Wildix PBX
                            └──(POST success=… + Push Token)──> Gatus :8080
```

## What counts as "up"

1. `GET /api/v1/PBX/version/` returns 200 → PBX API reachable.
2. `GET /api/v1/PBX/Users/Sip/Registrations` → count DISTINCT extensions that
   are `online == "1"` **and** whose useragent contains `ForcePro` (desk
   phones; x-bees Web softphones are ignored).
3. Self-baseline: the script remembers the high-water mark of online desk
   phones. Healthy when the current count is **≥ 90%** of that baseline.
   Baseline is stored in `collector/.phones_state.json` (gitignored).

## Deployment: compose sidecar (recommended — no Python on the host)

`docker-compose.yml` runs the collector as a `phone-collector` service
(`python:3-slim`, `LOOP=1`, on the gatus network). Just:

```bash
cd ~/LL-Gatus
# .env holds PHONES_IVORY_TOWER_TOKEN and PHONES_PUSH_TOKEN.
# External endpoints only load on a full recreate (not restart):
docker compose down && docker compose up -d --build
docker logs -f phone-collector      # watch the sweeps
```

Loop cadence is a jittered 15-45s (`SWEEP_MIN`/`SWEEP_MAX` to change).

## Alternative: cron / systemd on the host (if Python 3 is installed)

One sweep then exit — schedule it, or run `LOOP=1 python3 phone_collector.py`
under systemd. The script auto-loads `.env` from the repo root.

## Adding another location

Append a dict to `LOCATIONS` in `phone_collector.py` (its own PBX host + token
env var) and add a matching `external-endpoints:` entry in `config.yaml`
(`name: <Location>`, `group: Phones` — so it lands in that location card's
Phones row; `key` = `phones_<location-slug>`). Recreate the stack to load it.
