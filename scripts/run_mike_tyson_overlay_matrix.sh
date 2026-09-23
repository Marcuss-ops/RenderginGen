#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MATRIX="$ROOT_DIR/mike_tyson_overlay_test/matrix_cases.json"
RESULT_DIR="${MIKE_MATRIX_RESULT_DIR:-$ROOT_DIR/mike_tyson_overlay_test/results/matrix}"

# Validate schema and fixture drift before creating the output directory or
# submitting any external generation jobs.
python3 "$ROOT_DIR/scripts/verify_mike_tyson_overlay_matrix.py" --validate-contract
[[ -n "${VELOX_ADMIN_TOKEN:-}" ]] || { echo "VELOX_ADMIN_TOKEN is required" >&2; exit 2; }
mkdir -p "$RESULT_DIR"

mapfile -t CASES < <(jq -c '.cases[]' "$MATRIX")
((${#CASES[@]} == 3)) || { echo "matrix contract must contain exactly three cases" >&2; exit 2; }
RESULT_ARGS=()

for case_json in "${CASES[@]}"; do
  case_id="$(jq -r '.id' <<<"$case_json")"
  fixture="$(jq -r '.fixture' <<<"$case_json")"
  result_file="$(jq -r '.result_file' <<<"$case_json")"
  request="$ROOT_DIR/mike_tyson_overlay_test/$fixture"
  output="$RESULT_DIR/$result_file"
  [[ "$fixture" != */* && "$result_file" != */* && "$fixture" != *..* && "$result_file" != *..* ]] || { echo "matrix filenames must be plain filenames" >&2; exit 2; }
  [[ -f "$request" ]] || { echo "request fixture missing: $request" >&2; exit 2; }

  # Publication is owned by script.generate; fail before submitting if the
  # fixture disables the documented Drive/Docs/artifact path.
  jq -e '
    (.items | length) == 1 and
    (.items[0].docs.enabled == true) and
    (.items[0].docs.folder_id | type) == "string" and
    (.items[0].output.render.enabled == true) and
    (.items[0].output.render.drive_folder_id | type) == "string" and
    (.items[0].output.render.drive_subfolder_name | type) == "string" and
    (.items[0].media_plan.materialization.upload_to_drive == true) and
    (.items[0].media_plan.extraction.entity_images.upload_to_drive == true)
  ' "$request" >/dev/null || {
    echo "automatic Drive/Google Docs publication is not enabled in $request" >&2
    exit 2
  }

  printf 'Running case=%s fixture=%s result=%s\n' "$case_id" "$fixture" "$output"
  RESULT_FILE="$output" \
  IDEMPOTENCY_KEY="mike-tyson-overlay-${case_id}-$(date -u +%Y%m%dT%H%M%SZ)" \
    "$ROOT_DIR/scripts/generate_mike_tyson_overlay.sh" "$request"
  RESULT_ARGS+=(--result "$case_id=$output")
done

python3 "$ROOT_DIR/scripts/verify_mike_tyson_overlay_matrix.py" "${RESULT_ARGS[@]}"
