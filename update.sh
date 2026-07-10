#!/usr/bin/env bash
#
# LL-Gatus fast update (Ubuntu).
# Force-syncs to the repo (kills any stale/local drift), rebuilds, restarts,
# and verifies the RUNNING build matches the git commit — so you can always
# tell whether it actually updated.
#
# Usage:  ./update.sh
#
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")"

bar() { printf '%s\n' "++++++++++++++++++++++++++++++++++++++++++++++++++++++++"; }
sub() { printf '%s\n' "--------------------------------------------------------"; }

bar
echo "  LL-Gatus UPDATE"
bar

BEFORE="$(git rev-list --count HEAD 2>/dev/null || echo 0)"
echo "current build: #$BEFORE"

sub
echo "  Killing stale code — force-syncing to origin/main"
sub
git fetch --prune origin main
git reset --hard origin/main
git clean -fd            # remove untracked junk (data/ is gitignored, so it's safe)

# Build number = total commit count (auto-increments each commit).
BUILD="$(git rev-list --count HEAD)"
echo "target build:  #$BUILD   \"$(git log -1 --pretty=%s)\""
if [ "$BEFORE" = "$BUILD" ]; then
  echo "(already at latest commit — rebuilding anyway to be certain)"
fi

sub
echo "  Pre-flight checks"
sub
# Secrets live in .env (gitignored — never on GitHub). It must exist on THIS box
# or phones fail (empty token) and Gatus may crash-loop. git clean -fd above does
# NOT delete it (it's ignored), so create it once and it survives every update.
if [ ! -f .env ]; then
  echo "  !! .env NOT FOUND — phones will fail (missing PHONES_* tokens)."
  echo "     Create it once (it is gitignored and persists across updates):"
  echo "       cp .env.example .env && nano .env"
else
  echo "  .env present ✔"
fi
# If gatus/phone-collector exist but were NOT created by compose (e.g. started
# from Docker Desktop's Run button), compose can't recreate them and the update
# silently no-ops. Remove any such non-compose orphan so compose owns it.
for c in gatus phone-collector; do
  if docker inspect "$c" >/dev/null 2>&1; then
    proj="$(docker inspect -f '{{ index .Config.Labels "com.docker.compose.project" }}' "$c" 2>/dev/null || true)"
    if [ -z "$proj" ]; then
      echo "  removing non-compose container: $c"
      docker rm -f "$c" >/dev/null 2>&1 || true
    fi
  fi
done

sub
echo "  Rebuilding + recreating containers (cached layers make this quick)"
sub
export GIT_SHA="$BUILD"
docker compose up -d --build --force-recreate --remove-orphans

sub
echo "  Waiting for Gatus to come up..."
sub
for _ in $(seq 1 60); do
  if curl -fsS http://localhost:8080/health >/dev/null 2>&1; then break; fi
  sleep 1
done

RUNNING="$(curl -fsS http://localhost:8080/api/v1/version 2>/dev/null | sed -E 's/.*"version":"([^"]*)".*/\1/' || echo unknown)"
IP="$(hostname -I 2>/dev/null | awk '{print $1}')"

bar
if [ "$RUNNING" = "$BUILD" ]; then
  echo "  DEPLOYED OK  ✔   running build = #$RUNNING  (matches git)"
else
  echo "  WARNING: running build (#$RUNNING) != git (#$BUILD)"
  echo "  The rebuild may not have taken. Try:  docker compose build --no-cache && docker compose up -d"
fi
echo "  Open:  http://${IP:-<box-ip>}:8080     (Ctrl+Shift+R to bust browser cache)"
bar
