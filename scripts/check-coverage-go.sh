#!/usr/bin/env bash
# Coverage gate for the Go server. Compares a go-cover profile's
# total statement coverage against a threshold, exits 1 if it falls short.
#
# The gate measures the *testable core*: process entry points and third-party
# binary-format decoders are excluded from the basis (see ADR-0028). Their
# blocks are filtered out of the profile before the total is recomputed, so the
# reported percentage reflects only code that unit tests can meaningfully reach.
set -euo pipefail

PROFILE="${1:?Usage: check-coverage-go.sh <coverage.out> <min-percent>}"
THRESHOLD="${2:?Usage: check-coverage-go.sh <coverage.out> <min-percent>}"

# Excluded from the coverage basis (ADR-0028):
#   cmd/*               -- process entry points, no meaningful unit surface
#   thumbnail_3d.go     -- 3D mesh (STL/OBJ) decoding, needs binary fixtures
#   thumbnail_extra.go  -- HEIC/comic decoding, needs binary fixtures
EXCLUDE_RE='privasync/server/cmd/|internal/api/handler/thumbnail_3d\.go|internal/api/handler/thumbnail_extra\.go'

FILTERED=$(mktemp)
trap 'rm -f "$FILTERED"' EXIT
# Keep the mode header (first line) verbatim; drop excluded blocks.
head -1 "$PROFILE" >"$FILTERED"
grep -vE "$EXCLUDE_RE" "$PROFILE" | tail -n +2 >>"$FILTERED" || true

TOTAL_LINE=$(go tool cover -func="$FILTERED" | tail -1)
TOTAL=$(echo "$TOTAL_LINE" | grep -oE '[0-9]+\.[0-9]+%' | tr -d '%')

echo "Go-Coverage (Kern, ohne cmd/* + Decoder): ${TOTAL}% (Schwellwert: ${THRESHOLD}%)"

if awk -v t="$TOTAL" -v m="$THRESHOLD" 'BEGIN { exit !(t < m) }'; then
  echo "FEHLER: Coverage ${TOTAL}% unterschreitet Schwellwert ${THRESHOLD}%."
  exit 1
fi

echo "OK: Coverage-Schwellwert erreicht."
