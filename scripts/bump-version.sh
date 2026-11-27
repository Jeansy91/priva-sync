#!/usr/bin/env bash
#
# scripts/bump-version.sh <new_version>
#
# Aktualisiert die Version in allen Client-Manifesten:
#   - apps/desktop/package.json
#   - apps/desktop/src-tauri/tauri.conf.json
#   - apps/desktop/src-tauri/Cargo.toml
#   - apps/web/package.json
#   - package.json (Root, falls vorhanden)
#
# Verwendet vom `make release`-Target.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "Usage: $0 <version>" >&2
  echo "Beispiel: $0 0.2.0" >&2
  exit 1
fi

NEW="$1"

if ! [[ "$NEW" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Fehler: Version muss semver x.y.z entsprechen (erhalten: '$NEW')" >&2
  exit 1
fi

cd "$(dirname "$0")/.."

bump_json() {
  local file="$1"
  if [[ ! -f "$file" ]]; then
    return 0
  fi
  local before
  before=$(grep -m1 -E '^[[:space:]]*"version":[[:space:]]*"[0-9]+\.[0-9]+\.[0-9]+"' "$file" || true)
  if [[ -z "$before" ]]; then
    echo "Warnung: keine Top-Level-Version in $file gefunden — überspringe" >&2
    return 0
  fi
  sed -i -E '0,/^([[:space:]]*)"version":[[:space:]]*"[0-9]+\.[0-9]+\.[0-9]+"/ {
    s//\1"version": "'"$NEW"'"/
  }' "$file"
  local after
  after=$(grep -m1 -E '^[[:space:]]*"version":[[:space:]]*"[0-9]+\.[0-9]+\.[0-9]+"' "$file")
  echo "  $file"
  echo "    vorher:  ${before## }"
  echo "    nachher: ${after## }"
}

bump_cargo() {
  local file="$1"
  if [[ ! -f "$file" ]]; then
    echo "Fehler: $file nicht gefunden" >&2
    exit 1
  fi
  local before
  before=$(grep -m1 -E '^version = "[0-9]+\.[0-9]+\.[0-9]+"' "$file" || true)
  if [[ -z "$before" ]]; then
    echo "Fehler: keine [package]-version in $file" >&2
    exit 1
  fi
  sed -i -E '0,/^version = "[0-9]+\.[0-9]+\.[0-9]+"/ {
    s//version = "'"$NEW"'"/
  }' "$file"
  local after
  after=$(grep -m1 -E '^version = "[0-9]+\.[0-9]+\.[0-9]+"' "$file")
  echo "  $file"
  echo "    vorher:  $before"
  echo "    nachher: $after"
}

echo "Bumpe Version auf $NEW …"
bump_json  "apps/desktop/package.json"
bump_json  "apps/desktop/src-tauri/tauri.conf.json"
bump_cargo "apps/desktop/src-tauri/Cargo.toml"
bump_json  "apps/web/package.json"
bump_json  "package.json"

# Cargo.toml bump allein laesst den Selbst-Referenz-Eintrag von
# privasync-desktop in Cargo.lock auf der alten Version stehen. `cargo
# llvm-cov --locked` im Release-Workflow verweigert das stille Nachziehen
# und bricht die Coverage-Gate ab, bevor irgendein Test laeuft --
# passiert bei v0.2.2 (siehe Commit-Historie). Lockfile hier gleich mit
# aktualisieren, damit das nicht wieder vorkommt.
if command -v cargo >/dev/null 2>&1; then
  (cd apps/desktop/src-tauri && cargo update --workspace --precise "$NEW" privasync-desktop)
else
  echo "Warnung: cargo nicht gefunden -- apps/desktop/src-tauri/Cargo.lock manuell aktualisieren (cargo update -p privasync-desktop --precise $NEW)" >&2
fi

echo "Fertig: Version $NEW in allen Manifesten gesetzt."
