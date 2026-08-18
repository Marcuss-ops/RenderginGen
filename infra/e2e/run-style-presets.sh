#!/usr/bin/env bash
#
# Render every Chronon "style preset" job in testdata/styles/ through the real
# RenderingGen chain (queue -> worker -> Chronon3d -> artifact store -> Drive).
#
# Each job is a self-contained renderinggen.job envelope whose render_plan is a
# concrete chronon.render-plan.v1 that exercises the full canonical visual
# preset vocabulary (caption_card, active_word_pop, lower_third_safe,
# organization_card, location_card, image_focus_in) over a curated video
# background, varying only the plan's `style_profile` (discovery / young /
# crime). The running worker is Drive-enabled, so each completed render is also
# published into the configured Drive folder.
#
# Usage:
#   infra/e2e/run-style-presets.sh
#
# Requires curl, python3, jq, ffprobe, sha256sum.
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
STORE_URL="${STORE_URL:-http://localhost:9000}"
OUT_DIR="${OUT_DIR:-/tmp/renderinggen-style-presets}"
EXPECTED_WIDTH="${EXPECTED_WIDTH:-1920}"
EXPECTED_HEIGHT="${EXPECTED_HEIGHT:-1080}"
EXPECTED_DURATION="${EXPECTED_DURATION:-12}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
STYLES_DIR="${REPO_ROOT}/testdata/styles"
CHRONON_ASSETS_ROOT="${CHRONON_ASSETS_ROOT:-${REPO_ROOT}/../Chronon3d/assets}"

mkdir -p "${OUT_DIR}"

json_field() { printf '%s' "$1" | jq -r "$2"; }

# Resolve a job's logical asset path to a local source file. Backgrounds live
# in RenderingGen/assets/backgrounds, fonts in the Chronon3d asset tree, and
# everything else in the deterministic golden fixtures.
resolve_asset() {
  local logical="$1"
  case "${logical}" in
    assets/backgrounds/*) printf '%s' "${REPO_ROOT}/assets/backgrounds/$(basename "${logical}")" ;;
    assets/fonts/*)       printf '%s' "${CHRONON_ASSETS_ROOT}/fonts/$(basename "${logical}")" ;;
    *)                    printf '%s' "${REPO_ROOT}/testdata/golden/$(basename "${logical}")" ;;
  esac
}

render_one() {
  local job_file="$1"
  local job_id asset_count i body hash logical local_file
  job_id="$(jq -r '.id' "${job_file}")"
  echo ""
  echo "=== ${job_id} ==="

  # 1. Upload the job's assets to the object store (L3), keyed by content hash.
  asset_count="$(jq '.assets | length' "${job_file}")"
  for i in $(seq 0 $((asset_count - 1))); do
    hash="$(jq -r ".assets[${i}].hash" "${job_file}")"
    logical="$(jq -r ".assets[${i}].logical_path" "${job_file}")"
    local_file="$(resolve_asset "${logical}")"
    if [ ! -f "${local_file}" ]; then
      echo "ERROR: ${job_id}: asset source missing for ${logical} (${local_file})" >&2
      exit 1
    fi
    if [ "$(sha256sum "${local_file}" | cut -d' ' -f1)" != "${hash}" ]; then
      echo "ERROR: ${job_id}: ${local_file} sha256 does not match payload hash ${hash}" >&2
      exit 1
    fi
    curl -fsS -X PUT --data-binary @"${local_file}" \
      -H 'Content-Type: application/octet-stream' \
      "${STORE_URL}/objects/${hash}" >/dev/null
  done
  echo "  uploaded ${asset_count} asset(s)"

  # 2. Submit the job.
  local submit_code submit_body
  submit_body="$(mktemp)"
  submit_code="$(curl -s -o "${submit_body}" -w '%{http_code}' \
    -X POST "${QUEUE_URL}/jobs" -H 'Content-Type: application/json' \
    --data-binary @"${job_file}")"
  case "${submit_code}" in
    201) echo "  submitted (201)" ;;
    200|409) echo "  idempotent success (${submit_code}) — job already exists" ;;
    *)
      echo "ERROR: submit returned HTTP ${submit_code}" >&2
      cat "${submit_body}" >&2
      rm -f "${submit_body}"
      exit 1
      ;;
  esac
  rm -f "${submit_body}"

  # 3. Poll until terminal.
  local state=""
  for _ in $(seq 1 600); do
    body="$(curl -fsS "${QUEUE_URL}/jobs/${job_id}")"
    state="$(json_field "${body}" '.state')"
    if [ "${state}" = "completed" ] || [ "${state}" = "failed" ]; then break; fi
    sleep 1
  done
  if [ "${state}" != "completed" ]; then
    echo "ERROR: ${job_id} ended in state '${state}' (expected completed)" >&2
    printf '%s\n' "${body}" >&2
    exit 1
  fi

  local artifact_hash artifact_size
  artifact_hash="$(json_field "${body}" '.artifact.artifact_hash')"
  artifact_size="$(json_field "${body}" '.artifact.size_bytes')"
  echo "  completed: hash=${artifact_hash} size=${artifact_size}"

  # 4. Download and verify the artifact.
  local out_file="${OUT_DIR}/${job_id}.mp4"
  curl -fsS "${STORE_URL}/objects/${artifact_hash}" -o "${out_file}"
  if [ ! -s "${out_file}" ]; then
    echo "ERROR: ${job_id}: artifact is empty" >&2
    exit 1
  fi
  local probe p_width p_height p_duration
  probe="$(ffprobe -v error -select_streams v:0 \
    -show_entries stream=width,height \
    -show_entries format=duration -of json "${out_file}")"
  p_width="$(json_field "${probe}" '.streams[0].width')"
  p_height="$(json_field "${probe}" '.streams[0].height')"
  p_duration="$(json_field "${probe}" '.format.duration')"
  echo "  artifact: ${out_file} (${p_width}x${p_height}, ${p_duration}s)"

  if [ "${p_width}" != "${EXPECTED_WIDTH}" ] || [ "${p_height}" != "${EXPECTED_HEIGHT}" ]; then
    echo "ERROR: ${job_id}: expected ${EXPECTED_WIDTH}x${EXPECTED_HEIGHT}, got ${p_width}x${p_height}" >&2
    exit 1
  fi
  python3 - "${p_duration}" "${EXPECTED_DURATION}" <<'PY'
import sys
d = float(sys.argv[1]); want = float(sys.argv[2])
if abs(d - want) > 0.4:
    print(f"ERROR: expected ~{want}s duration, got {d}s")
    sys.exit(1)
PY
}

echo "Rendering $(ls "${STYLES_DIR}"/*.json | wc -l) style preset(s) -> ${OUT_DIR}"
for job_file in "${STYLES_DIR}"/*.json; do
  render_one "${job_file}"
done

echo ""
echo "OK: all style presets rendered"
echo "    artifacts: ${OUT_DIR}"
echo "    Drive folder: 1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS (published by the Drive-enabled worker)"
