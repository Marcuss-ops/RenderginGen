#!/usr/bin/env bash
set -Eeuo pipefail

# Upload the scene_camera_sequencer_v1 pack to the shared Drive render folder,
# verifying the SHA-256 of every MP4. Same mechanism as upload_to_drive.sh
# (RenderingGen/bin/drive-upload + OAuth credentials kept at 0600).

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PACK="$ROOT/scene_camera_sequencer_v1"
UPLOADER="$(cd -- "$ROOT/../.." && pwd)/RenderingGen/bin/drive-upload"
CREDENTIALS="${DRIVE_CREDENTIALS:-$ROOT/credentials.json}"
TOKEN="${DRIVE_TOKEN:-$ROOT/token.json}"
DRIVE_FOLDER="${DRIVE_FOLDER_ID:-1ATL0bnJXijNqFlKkgWye3PEAdAuQa1HI}"
DRIVE_SUBFOLDER="scene_camera_sequencer_v1"

for required in "$UPLOADER" "$CREDENTIALS" "$TOKEN"; do
  if [[ ! -r "$required" ]]; then
    printf 'Missing or unreadable upload requirement: %s\n' "$required" >&2
    exit 1
  fi
done

shopt -s nullglob
files=("$PACK"/*.mp4)
if [[ ${#files[@]} -lt 9 ]]; then
  printf 'Expected at least 9 MP4 files in %s, found %d\n' "$PACK" "${#files[@]}" >&2
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
