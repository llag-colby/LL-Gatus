# Deploying LL-Gatus (Ubuntu / Docker)

The customized Long Lewis status dashboard. The WAN endpoints and UI live in
`config.yaml`, which is baked into the image at build time. SQLite history
persists in `./data` next to the compose file.

## First deploy

```bash
# Stop any previous Gatus on this box so port 8080 is free
cd ~/gatus 2>/dev/null && docker compose down; cd ~

git clone https://github.com/llag-colby/LL-Gatus.git
cd LL-Gatus
docker compose up -d --build
docker compose logs --tail=25        # expect: "Validated 14 endpoints"
```

Then browse to `http://<box-ip>:8080`.

## Updating after a config or UI change

```bash
cd ~/LL-Gatus
git pull
docker compose up -d --build
```

## Notes

- **ICMP (WAN ping)** needs the `NET_RAW` capability — already set in the compose.
- **Editing endpoints:** change `config.yaml`, commit and push, then on the box
  run `git pull && docker compose up -d --build`.
- **History** is stored in `./data/data.db`; it survives restarts and rebuilds.
- Port 8080 is published by Docker and reachable on the LAN even with `ufw`
  allowing only SSH — that's expected.
