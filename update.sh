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
echo "  Rebuilding + recreating container (cached layers make this quick)"
sub
export GIT_SHA="$BUILD"
docker compose up -d --build --force-recreate

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
