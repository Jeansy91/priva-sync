#!/usr/bin/env bash
# Writes the client-release manifest that is embedded into the server image
# (ADR-0088, apps/server/internal/clientrelease/manifest.json).
#
# Usage: scripts/build-client-manifest.sh <version> <sig-dir> [output-file]
#
# <sig-dir> holds the Tauri updater signature files named exactly like the
# GitHub release assets plus ".sig" (e.g. privasync-windows-x86_64-setup.exe.sig).
# The signature text goes into the manifest, the asset name is the .sig name
# without the suffix. <sig-dir> also holds installers.txt, the list of file
# names in the published release (one per line); each installer below must be
# listed there. All platforms below are required: the server image must
# not ship with a client that silently lacks a platform.
#
# Default output is the embedded manifest in the repo working tree.
set -euo pipefail

version="${1:?usage: $0 <version> <sig-dir> [output-file]}"
sig_dir="${2:?usage: $0 <version> <sig-dir> [output-file]}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out="${3:-$repo_root/apps/server/internal/clientrelease/manifest.json}"

# Tauri updater target -> release asset name. Keep in sync with the staging
# step of publish-client-release in .github/workflows/ci.yml.
declare -A assets=(
  [windows-x86_64]="privasync-windows-x86_64-setup.exe"
  [linux-x86_64]="privasync-linux-x86_64.AppImage"
  [darwin-aarch64]="privasync-darwin-aarch64.app.tar.gz"
  [darwin-x86_64]="privasync-darwin-x86_64.app.tar.gz"
)

# Tauri updater target -> installer a human downloads. Same as the updater
# asset on Windows/Linux; on macOS the .dmg (the updater asset is .app.tar.gz).
declare -A installers=(
  [windows-x86_64]="privasync-windows-x86_64-setup.exe"
  [linux-x86_64]="privasync-linux-x86_64.AppImage"
  [darwin-aarch64]="privasync-darwin-aarch64.dmg"
  [darwin-x86_64]="privasync-darwin-x86_64.dmg"
)

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

platforms='{}'
for target in "${!assets[@]}"; do
  asset="${assets[$target]}"
  sig_file="$sig_dir/$asset.sig"
  [ -s "$sig_file" ] || { echo "missing or empty signature: $sig_file" >&2; exit 1; }
  installer="${installers[$target]}"
  grep -qxF "$installer" "$sig_dir/installers.txt" 2>/dev/null \
    || { echo "installer $installer not listed in $sig_dir/installers.txt" >&2; exit 1; }
  # The .sig file is a single base64 line; drop the trailing newline.
  signature="$(tr -d '\r\n' < "$sig_file")"
  platforms="$(jq -c --arg t "$target" --arg a "$asset" --arg s "$signature" --arg i "$installer" \
    '. + {($t): {asset: $a, signature: $s, installer: $i}}' <<<"$platforms")"
done

jq -n --arg v "$version" --arg d "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --argjson p "$platforms" \
  '{version: $v, pub_date: $d, platforms: $p}' > "$out"
echo "wrote $out"
