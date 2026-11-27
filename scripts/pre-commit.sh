#!/usr/bin/env bash
# Regenerates packages/api-client/src/events.gen.ts whenever Go event
# structs change. Stages the regenerated file so the commit stays consistent
# with the new Go source.
set -euo pipefail

# Only act when files in the typed-events source tree are staged.
if ! git diff --cached --name-only | grep -qE '^apps/server/internal/sse/events/'; then
  exit 0
fi

if ! command -v tygo >/dev/null 2>&1; then
  echo "pre-commit: tygo nicht installiert — bitte 'go install github.com/gzuidhof/tygo@latest' ausfuehren." >&2
  exit 1
fi

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root/apps/server"
tygo generate --config ../../tygo.yaml
cd "$repo_root"
git add packages/api-client/src/events.gen.ts
