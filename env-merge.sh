#!/usr/bin/env bash
#
# env-merge.sh SOURCE TARGET
#
# Merge SOURCE into TARGET with TARGET as the MASTER: append only the keys that
# TARGET does not already define. Existing TARGET values are NEVER changed or
# reordered — so the prod .env stays authoritative and only picks up new keys
# (e.g. the JIRA_* config) from the source.
#
# Comment/blank lines in SOURCE are ignored. A timestamped backup of TARGET is
# written before any change. Safe to run repeatedly (idempotent).
#
# Example (on the prod box):
#   ./env-merge.sh .env.incoming .env
#
set -euo pipefail

SRC="${1:?usage: env-merge.sh SOURCE TARGET}"
DST="${2:?usage: env-merge.sh SOURCE TARGET}"
[ -f "$SRC" ] || { echo "source not found: $SRC" >&2; exit 1; }
[ -f "$DST" ] || { echo "target (master) not found: $DST" >&2; exit 1; }

# Keys already defined in the target (master).
mapfile -t HAVE < <(grep -oE '^[A-Za-z_][A-Za-z0-9_]*=' "$DST" | sed 's/=$//' | sort -u)
has_key() { local k="$1" h; for h in "${HAVE[@]:-}"; do [ "$h" = "$k" ] && return 0; done; return 1; }

added=()
tmp="$(mktemp)"
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in ''|\#*) continue;; esac                 # skip blanks/comments
  [[ "$line" =~ ^[A-Za-z_][A-Za-z0-9_]*= ]] || continue   # only KEY=VALUE lines
  key="${line%%=*}"
  if has_key "$key"; then
    echo "  keep master: $key"
    continue
  fi
  echo "$line" >> "$tmp"
  added+=("$key")
done < "$SRC"

if [ ! -s "$tmp" ]; then
  echo "Nothing to add — target already defines every key from source."
  rm -f "$tmp"; exit 0
fi

backup="$DST.bak.$(date +%Y%m%d-%H%M%S)"
cp "$DST" "$backup"
{ printf '\n# --- merged from %s on %s ---\n' "$SRC" "$(date -u +%FT%TZ)"; cat "$tmp"; } >> "$DST"
rm -f "$tmp"

echo "Added ${#added[@]} key(s) to $DST: ${added[*]}"
echo "Backup saved: $backup"
