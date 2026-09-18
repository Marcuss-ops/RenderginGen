#!/usr/bin/env bash
set -Eeuo pipefail

# ROOT_DIR is the RenderingGen checkout that owns the canonical fixtures and
# these three scripts, so the matrix stays inside the repo of record.
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REQUEST_SIMPLE="$ROOT_DIR/mike_tyson_overlay_test/mike_tyson_generate_request.json"
REQUEST_EXTENDED="$ROOT_DIR/mike_tyson_overlay_test/mike_tyson_extended_generate_request.json"
REQUEST_FIVE="$ROOT_DIR/mike_tyson_overlay_test/mike_tyson_five_generate_request.json"
RESULT_DIR="${MIKE_MATRIX_RESULT_DIR:-$ROOT_DIR/mike_tyson_overlay_test/results/matrix}"

[[ -n "${VELOX_ADMIN_TOKEN:-}" ]] || { echo "VELOX_ADMIN_TOKEN is required" >&2; exit 2; }
mkdir -p "$RESULT_DIR"

# Drive/Docs publication is owned by the script.generate job. Fail before
# submission if a fixture accidentally disables the automatic path: the job
# must create/reuse the Mike Tyson subfolder, publish a native Google Doc, and
# upload the selected images/render artifacts itself.
for request in "$REQUEST_SIMPLE" "$REQUEST_EXTENDED" "$REQUEST_FIVE"; do
  jq -e '
    (.items | length) == 1 and
    (.items[0].docs.enabled == true) and
    (.items[0].docs.folder_id | type) == "string" and
    (.items[0].output.drive_folder_id | type) == "string" and
    (.items[0].output.render.enabled == true) and
    (.items[0].output.render.drive_subfolder_name | type) == "string" and
    (.items[0].media_plan.materialization.upload_to_drive == true) and
    (.items[0].media_plan.extraction.entity_images.upload_to_drive == true)
  ' "$request" >/dev/null || {
    echo "automatic Drive/Google Docs publication is not enabled in $request" >&2
    exit 2
  }
done

RESULT_FILE="$RESULT_DIR/simple.json" \
IDEMPOTENCY_KEY="mike-tyson-overlay-simple-$(date -u +%Y%m%dT%H%M%SZ)" \
  "$ROOT_DIR/scripts/generate_mike_tyson_overlay.sh" "$REQUEST_SIMPLE"

RESULT_FILE="$RESULT_DIR/extended-3entities-3phrases.json" \
IDEMPOTENCY_KEY="mike-tyson-overlay-extended-$(date -u +%Y%m%dT%H%M%SZ)" \
  "$ROOT_DIR/scripts/generate_mike_tyson_overlay.sh" "$REQUEST_EXTENDED"

RESULT_FILE="$RESULT_DIR/five-5entities-5phrases.json" \
IDEMPOTENCY_KEY="mike-tyson-overlay-five-$(date -u +%Y%m%dT%H%M%SZ)" \
  "$ROOT_DIR/scripts/generate_mike_tyson_overlay.sh" "$REQUEST_FIVE"

python3 "$ROOT_DIR/scripts/verify_mike_tyson_overlay_matrix.py" \
  --simple "$RESULT_DIR/simple.json" \
  --extended "$RESULT_DIR/extended-3entities-3phrases.json" \
  --five "$RESULT_DIR/five-5entities-5phrases.json"
