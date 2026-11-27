#!/usr/bin/env bash
# Installs the project pre-commit hook into .git/hooks/.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
hook_src="$repo_root/scripts/pre-commit.sh"
hook_dst="$repo_root/.git/hooks/pre-commit"

if [ ! -f "$hook_src" ]; then
  echo "FEHLER: $hook_src fehlt." >&2
  exit 1
fi

cp "$hook_src" "$hook_dst"
chmod +x "$hook_dst"
echo "pre-commit Hook installiert -> $hook_dst"
