#!/usr/bin/env bash
# Publish the recent (2026-09-30) renders to the operator's Drive folder.
# Canonical publisher: bin/drive-upload (OAuth credentials from ~/.config/velox).
# Every upload is verified by the tool itself: sha256 + byte accounting.
set -euo pipefail

CRED="${CRED:-/home/pierone/.config/velox/credentials.json}"
TOK="${TOK:-/home/pierone/.config/velox/token.json}"
FOLDER="${FOLDER:-1ATL0bnJXijNqFlKkgWye3PEAdAuQa1HI}"
OUT="renderinggen/out"

FILES=(
  "$OUT/editorial_v1/renders/editorial_v1_mega_vulkan.mp4"
  "$OUT/editorial_v1/renders/editorial_v1_mega_software.mp4"
  "$OUT/editorial_v1/renders/editorial_v1_mega_vulkan_replay.mp4"
  "$OUT/editorial_v1/renders/editorial_v1_mega_vulkan_fixed.mp4"
  "$OUT/editorial_v1/renders/editorial_v1_mega_vulkan_fixed2.mp4"
  "$OUT/editorial_v1/renders/editorial_v1_mega_vulkan_fixed3.mp4"
  "$OUT/editorial_v1/renders/editorial_v1_mega_vulkan_fixed4.mp4"
  "$OUT/editorial_v1/renders/editorial_v1_mega_vulkan_png_replay.mp4"
  "$OUT/editorial_v1/renders/map_vulkan.mp4"
  "$OUT/editorial_v1/renders/map_camera_flyover_vulkan.mp4"
)

cd "$(dirname "$0")/.."

mkdir -p evidence
MANIFEST="evidence/drive_publication_20261001.json"
rm -f "$MANIFEST.ndjson"

for f in "${FILES[@]}"; do
  if [[ ! -f "$f" ]]; then
    echo "SKIP missing $f" >&2
    continue
  fi
  name="$(basename "$f")"
  echo "== uploading $name"
  line="$(./bin/drive-upload -credentials "$CRED" -token "$TOK" -folder "$FOLDER" -file "$f")"
  echo "$line"
  # Parse DRIVE_UPLOAD_PASS fields into a per-upload JSON line file; the
  # manifest is assembled once at the end (incremental JSON splicing is
  # fragile — this batch run proved it).
  id=$(sed -n 's/.* id=\([^ ]*\) .*/\1/p' <<<"$line")
  link=$(sed -n 's/.* link=\([^ ]*\) .*/\1/p' <<<"$line")
  sha=$(sed -n 's/.* sha256=\([^ ]*\) .*/\1/p' <<<"$line")
  bytes=$(sed -n 's/.* bytes=\([0-9]*\).*/\1/p' <<<"$line")
  printf '{"local_path":"%s","drive_file_id":"%s","drive_link":"%s","parent_folder":"%s","sha256":"%s","bytes":%s,"result":"UPLOADED"}\n' \
    "$f" "$id" "$link" "$FOLDER" "$sha" "$bytes" >> "$MANIFEST.ndjson"
done

python3 - "$MANIFEST.ndjson" "$MANIFEST" <<'PYEOF'
import json, sys
entries = [json.loads(line) for line in open(sys.argv[1]) if line.strip()]
with open(sys.argv[2], "w") as fh:
    json.dump(entries, fh, indent=1)
    fh.write("\n")
print(f"MANIFEST_OK entries={len(entries)} total_bytes={sum(e['bytes'] for e in entries)}")
PYEOF
rm -f "$MANIFEST.ndjson"
