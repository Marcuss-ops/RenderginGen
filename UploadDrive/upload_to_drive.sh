#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PACK="$ROOT/social_motion_pack_v1"
UPLOADER="$(cd -- "$ROOT/../.." && pwd)/RenderingGen/bin/drive-upload"
CREDENTIALS="${DRIVE_CREDENTIALS:-${HOME}/.config/velox/credentials.json}"
TOKEN="${DRIVE_TOKEN:-${HOME}/.config/velox/token.json}"
DRIVE_FOLDER="1ATL0bnJXijNqFlKkgWye3PEAdAuQa1HI"
DRIVE_SUBFOLDER="social_motion_pack_v1"

for required in "$UPLOADER" "$CREDENTIALS" "$TOKEN"; do
  if [[ ! -r "$required" ]]; then
    printf 'Missing or unreadable upload requirement: %s\n' "$required" >&2
    exit 1
  fi
done

shopt -s nullglob
files=("$PACK"/*.mp4)
if [[ ${#files[@]} -ne 18 ]]; then
  printf 'Expected 18 MP4 files in %s, found %d\n' "$PACK" "${#files[@]}" >&2
  exit 1
fi

for file in "${files[@]}"; do
  digest="$(sha256sum "$file" | cut -d ' ' -f 1)"
  "$UPLOADER" \
    -credentials "$CREDENTIALS" \
    -token "$TOKEN" \
    -folder "$DRIVE_FOLDER" \
    -subfolder "$DRIVE_SUBFOLDER" \
    -file "$file" \
    -name "$(basename -- "$file")" \
    -sha256 "$digest"
done

printf 'Upload complete: %d verified MP4 files.\n' "${#files[@]}"
