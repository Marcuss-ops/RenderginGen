#!/usr/bin/env bash
#
# Real Drive overlay render — renders a substantial text+image overlay and
# publishes the MP4 into the user's Drive folder (drive.parent_folder_id),
# then verifies the whole chain:
#
#   assets -> object store -> render (image bg + phrases + kinetic words
#   + image overlays) -> SHA256 -> Google Drive -> render_artifacts
#
# Defaults to GoldenOverlayJobV1 (image background + 2 texts + apple overlay).
# A video-background job (GoldenOverlayJobV2) needs a runtime image built with
# CHRONON3D_ENABLE_NATIVE_FFMPEG=ON; see docker/chronon-runtime/Dockerfile.
#
# Auth uses the same OAuth2 pair as PipelineGen (credentials.json + token.json,
# copied into infra/docker/ and never committed).
#
# Usage:
#   infra/e2e/run-drive-overlay.sh
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
STORE_URL="${STORE_URL:-http://localhost:9000}"
OUT_FILE="${OUT_FILE:-/tmp/renderinggen-drive-overlay-result.mp4}"
REPORT_OUT_DIR="${REPORT_OUT_DIR:-/tmp/chronon3d-reports}"
NETWORK="${NETWORK:-docker_default}"
WORKER_IMAGE="${WORKER_IMAGE:-docker-worker:latest}"
DRIVE_FOLDER_ID="${DRIVE_FOLDER_ID:-1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS}"
CHRONON_MODE="${CHRONON_MODE:-cli}"
CHRONON_BACKEND="${CHRONON_BACKEND:-software}"
HARDWARE_ENCODER="${HARDWARE_ENCODER:-none}"
EXPECTED_DURATION="${EXPECTED_DURATION:-5}"
EXPECTED_FRAMES="${EXPECTED_FRAMES:-150}"
EXPECTED_WIDTH="${EXPECTED_WIDTH:-1920}"
EXPECTED_HEIGHT="${EXPECTED_HEIGHT:-1080}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
COMPOSE_FILE="${REPO_ROOT}/infra/docker/docker-compose.yaml"
GOLDEN_DIR="${REPO_ROOT}/testdata/golden"
CHRONON_ASSETS_ROOT="${CHRONON_ASSETS_ROOT:-${REPO_ROOT}/../Chronon3d/assets}"
JOB_FILE="${JOB_FILE:-${GOLDEN_DIR}/golden-text-image-overlay-job-v1.json}"
ANIMATION_VARIANT="${ANIMATION_VARIANT:-}"
IMAGE_VARIANT="${IMAGE_VARIANT:-}"

case "${CHRONON_BACKEND}" in
  software) GPU_DOCKER_ARGS=() ;;
  vulkan) GPU_DOCKER_ARGS=(--gpus all) ;;
  *) echo "ERROR: CHRONON_BACKEND must be software or vulkan" >&2; exit 1 ;;
esac

CREDENTIALS_JSON="${CREDENTIALS_JSON:-${REPO_ROOT}/infra/docker/credentials.json}"
TOKEN_JSON="${TOKEN_JSON:-${REPO_ROOT}/infra/docker/token.json}"

if [[ ! -f "${CREDENTIALS_JSON}" ]]; then
  echo "ERROR: credentials.json not found at ${CREDENTIALS_JSON}" >&2; exit 1
fi
if [[ ! -f "${TOKEN_JSON}" ]]; then
  echo "ERROR: token.json not found at ${TOKEN_JSON}" >&2; exit 1
fi

JOB_ID="drive-overlay-$(date +%s)"
WORK_DIR="$(mktemp -d /tmp/drive-overlay.XXXXXX)"
TMP_WORKER="drive-overlay-worker"

# Copy credentials + token into the work dir (world-readable) so the non-root
# `chronon` user inside the container can read them; the token copy also keeps
# a refresh from ever mutating the source file.
cp "${CREDENTIALS_JSON}" "${WORK_DIR}/credentials.json"
cp "${TOKEN_JSON}" "${WORK_DIR}/token.json"
chmod 644 "${WORK_DIR}/credentials.json" "${WORK_DIR}/token.json"

json_field() { printf '%s' "$1" | python3 -c 'import sys,json; d=json.load(sys.stdin)
for p in sys.argv[1].split("."):
    d = d[int(p)] if p.isdigit() else d[p]
print(d)' "$2"; }

PG_CMD=()
pg_init() {
  if command -v psql >/dev/null 2>&1 && [[ -n "${DATABASE_URL:-}" ]]; then
    PG_CMD=(psql "${DATABASE_URL}" -At -F'|' -c)
  elif docker compose -f "${COMPOSE_FILE}" exec -T postgres \
      psql -U renderinggen -d renderinggen -At -F'|' -c 'SELECT 1' >/dev/null 2>&1; then
    PG_CMD=(docker compose -f "${COMPOSE_FILE}" exec -T postgres \
      psql -U renderinggen -d renderinggen -At -F'|' -c)
  else
    echo "ERROR: PostgreSQL not reachable" >&2; return 1
  fi
}
pg_query() { [[ ${#PG_CMD[@]} -gt 0 ]] || return 2; "${PG_CMD[@]}" "$1"; }

cleanup() {
  snapshot_reports
  docker rm -f "${TMP_WORKER}" >/dev/null 2>&1 || true
  docker compose -f "${COMPOSE_FILE}" start worker worker-b >/dev/null 2>&1 || true
  chmod -R a+rwX "${WORK_DIR}" 2>/dev/null || true
  # Chronon writes the mounted job tree as UID 10001. Use the already-built
  # worker image's root user only to normalize permissions before cleanup;
  # this does not alter the render container's production user.
  docker run --rm --user 0 --entrypoint /bin/chmod \
    -v "${WORK_DIR}:/cleanup" "${WORKER_IMAGE}" -R a+rwX /cleanup >/dev/null 2>&1 || true
  rm -rf "${WORK_DIR}"
}

snapshot_reports() {
  if [[ -d "${WORK_DIR:-}" ]]; then
    mkdir -p "${REPORT_OUT_DIR}/${JOB_ID}"
    cp -a "${WORK_DIR}/telemetry" "${REPORT_OUT_DIR}/${JOB_ID}/" 2>/dev/null || true
    cp -a "${WORK_DIR}/chronon-work"/*.log "${REPORT_OUT_DIR}/${JOB_ID}/" 2>/dev/null || true
    cp -a "${WORK_DIR}/jobs" "${REPORT_OUT_DIR}/${JOB_ID}/" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "== Real Drive overlay render (text + images) -> folder ${DRIVE_FOLDER_ID} =="
echo "payload: ${JOB_FILE}"
echo "chronon backend: ${CHRONON_BACKEND}"

# ── 1. Build the submit payload with a unique id + job_id. ─────────────────
ANIMATION_VARIANT="${ANIMATION_VARIANT}" IMAGE_VARIANT="${IMAGE_VARIANT}" EXPECTED_FRAMES="${EXPECTED_FRAMES}" python3 - "${JOB_FILE}" "${JOB_ID}" "${WORK_DIR}/payload.json" "${CHRONON_ASSETS_ROOT}" <<'PY'
import json, sys
import hashlib
import os
with open(sys.argv[1], encoding="utf-8") as f:
    payload = json.load(f)
payload["id"] = sys.argv[2]
payload["job_type"] = "overlay.render"
payload["render_plan"]["job_id"] = sys.argv[2]
variant = os.environ.get("ANIMATION_VARIANT", "")
image_variant = os.environ.get("IMAGE_VARIANT", "")
expected_frames = int(os.environ.get("EXPECTED_FRAMES", "150"))
usable_frames = max(1, expected_frames - 30)
if expected_frames != payload["render_plan"].get("canvas", {}).get("duration_frames"):
    payload["render_plan"].setdefault("canvas", {})["duration_frames"] = expected_frames
if variant and image_variant:
    raise SystemExit("ANIMATION_VARIANT and IMAGE_VARIANT are mutually exclusive")
if image_variant:
    layers = payload["render_plan"].get("layers", [])
    matches = [layer for layer in layers
               if layer.get("type") == "image"
               and layer.get("id") == "image_" + image_variant]
    if len(matches) != 1:
        raise SystemExit(f"image variant {image_variant!r}: expected one image layer, got {len(matches)}")
    image = dict(matches[0])
    image["id"] = "image_" + image_variant
    image["start_frame"] = 30
    image["duration_frames"] = usable_frames
    # Center the image box exactly in the 1920x1080 canvas.
    image["box_width"] = 1000
    image["box_height"] = 600
    image["fit"] = "contain"
    image["position"] = [460, 240]
    payload["render_plan"]["layers"] = [image]
    payload["render_plan"]["canvas"]["width"] = 1920
    payload["render_plan"]["canvas"]["height"] = 1080
    payload["render_plan"]["canvas"]["duration_frames"] = expected_frames
elif variant:
    layers = payload["render_plan"].get("layers", [])
    background = [layer for layer in layers if layer.get("type") != "text"]
    matches = [layer for layer in layers
               if layer.get("type") == "text"
               and layer.get("id") == "phrase_" + variant]
    if len(matches) != 1:
        raise SystemExit(f"animation variant {variant!r}: expected one phrase layer, got {len(matches)}")
    phrase = dict(matches[0])
    phrase["id"] = "phrase_" + variant
    # Keep the phrase on screen for the full usable section: 1s fade-in,
    # long stable hold, and no premature disappearance.
    phrase["start_frame"] = 30
    phrase["duration_frames"] = usable_frames
    # Compensate the font's ink metrics so the visible glyphs, not only the
    # layout box, land on the canvas center at 1920x1080.
    phrase["offset"] = [10, -20]
    for layer in background:
        layer["start_frame"] = 0
        layer["duration_frames"] = expected_frames
    payload["render_plan"]["layers"] = background + [phrase]
    payload["render_plan"]["canvas"]["width"] = 1920
    payload["render_plan"]["canvas"]["height"] = 1080
    payload["render_plan"]["canvas"]["duration_frames"] = expected_frames
# Chronon's visual preset registry owns these font paths. Legacy golden jobs
# predate the registry asset contract and only list DejaVuSans; add the
# canonical Poppins assets to the per-run payload without mutating the golden.
for name in ("Poppins-Regular.ttf", "Poppins-Bold.ttf"):
    path = os.path.join(sys.argv[4], "fonts", name)
    if not os.path.isfile(path):
        raise SystemExit(f"missing Chronon font asset: {path}")
    logical = "assets/fonts/" + name
    if not any(a.get("logical_path") == logical for a in payload.get("assets", [])):
        with open(path, "rb") as font:
            digest = hashlib.sha256(font.read()).hexdigest()
        payload.setdefault("assets", []).append({"hash": digest, "logical_path": logical})
with open(sys.argv[3], "w", encoding="utf-8") as f:
    json.dump(payload, f)
PY

# ── 2. Upload the deterministic assets to the object store (L3). ───────────
ASSET_COUNT="$(python3 - "${WORK_DIR}/payload.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    print(len(json.load(f)["assets"]))
PY
)"
if [ "${ASSET_COUNT}" -gt 0 ]; then
for i in $(seq 0 $((ASSET_COUNT - 1))); do
  BODY="$(cat "${WORK_DIR}/payload.json")"
  HASH="$(json_field "${BODY}" "assets.${i}.hash")"
  LOGICAL="$(json_field "${BODY}" "assets.${i}.logical_path")"
  LOCAL_FILE="${GOLDEN_DIR}/$(basename "${LOGICAL}")"
  if [[ ! -f "${LOCAL_FILE}" && "${LOGICAL}" == assets/fonts/* ]]; then
    LOCAL_FILE="${CHRONON_ASSETS_ROOT}/fonts/$(basename "${LOGICAL}")"
  fi
  if [ "$(sha256sum "${LOCAL_FILE}" | cut -d' ' -f1)" != "${HASH}" ]; then
    echo "ERROR: ${LOCAL_FILE} sha256 does not match payload hash ${HASH}" >&2; exit 1
  fi
  curl -fsS -X PUT --data-binary @"${LOCAL_FILE}" \
    -H 'Content-Type: application/octet-stream' \
    "${STORE_URL}/objects/${HASH}" >/dev/null
  echo "  asset: ${LOGICAL} (${HASH:0:12}...)"
done
fi

# ── 3. Rebuild the worker image. ───────────────────────────────────────────
echo "rebuilding worker image ..."
docker compose -f "${COMPOSE_FILE}" build worker >/dev/null

# The queue state filter is part of the running service binary. Recreate the
# queue container after a build; `docker compose build` alone leaves the old
# binary running and can make the publish loop claim pending jobs.
docker compose -f "${COMPOSE_FILE}" build queue >/dev/null
docker compose -f "${COMPOSE_FILE}" up -d --force-recreate queue objectstore >/dev/null

# ── 4. Stop both main workers so only the Drive worker claims. ─────────────
echo "stopping the main workers ..."
docker compose -f "${COMPOSE_FILE}" stop worker worker-b >/dev/null 2>&1

# ── 5. Dedicated worker config with the real OAuth publisher. ──────────────
cat > "${WORK_DIR}/worker-config.yaml" <<EOF
worker:
  id: renderinggen-drive
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://objectstore:9000
  local_cache_dir: /var/lib/renderinggen/cache
workspace:
  root: /var/lib/renderinggen/jobs
chronon:
  backend: ${CHRONON_BACKEND}
  report: true
  hardware_encoder: ${HARDWARE_ENCODER}
  home: /opt/chronon3d
  mode: ${CHRONON_MODE}
  socket_path: /var/run/chronon3d/chronon.sock
gpu:
  device: 0
health:
  addr: ":8080"
drive:
  enabled: true
  mode: oauth
  credentials_file: /etc/renderinggen/credentials.json
  token_file: /etc/renderinggen/token.json
  parent_folder_id: ${DRIVE_FOLDER_ID}
EOF

# ── 6. Run the Drive worker. ───────────────────────────────────────────────
echo "starting the Drive worker ..."
mkdir -p "${WORK_DIR}/telemetry" "${WORK_DIR}/chronon-work"
mkdir -p "${WORK_DIR}/jobs"
chmod 777 "${WORK_DIR}/telemetry" "${WORK_DIR}/chronon-work"
chmod 777 "${WORK_DIR}/jobs"
docker run -d --name "${TMP_WORKER}" \
  "${GPU_DOCKER_ARGS[@]}" \
  --network "${NETWORK}" \
  -v "${WORK_DIR}/telemetry:/var/lib/renderinggen/telemetry" \
  -v "${WORK_DIR}/chronon-work:/work" \
  -v "${WORK_DIR}/jobs:/var/lib/renderinggen/jobs" \
  -e CHRONON3D_TELEMETRY_PATH=/var/lib/renderinggen/telemetry \
  -e RENDERINGGEN_KEEP_WORKSPACE=1 \
  -v "${WORK_DIR}/worker-config.yaml:/etc/renderinggen/config.yaml:ro" \
  -v "$(readlink -f "${WORK_DIR}/credentials.json"):/etc/renderinggen/credentials.json:ro" \
  -v "$(readlink -f "${WORK_DIR}/token.json"):/etc/renderinggen/token.json" \
  "${WORKER_IMAGE}" >/dev/null
sleep 6   # let the worker reach READY and start claiming

# ── 7. Submit the job. ─────────────────────────────────────────────────────
echo "submitting job ${JOB_ID} ..."
SUBMIT_CODE="$(curl -s -o "${WORK_DIR}/submit.body" -w '%{http_code}' \
  -X POST "${QUEUE_URL}/jobs" -H 'Content-Type: application/json' \
  --data-binary @"${WORK_DIR}/payload.json")"
if [ "${SUBMIT_CODE}" != "201" ] && [ "${SUBMIT_CODE}" != "200" ]; then
  echo "ERROR: submit returned HTTP ${SUBMIT_CODE}" >&2; cat "${WORK_DIR}/submit.body" >&2; exit 1
fi

# ── 8. Wait for completion. ────────────────────────────────────────────────
STATE=""
for _ in $(seq 1 480); do
  BODY="$(curl -fsS "${QUEUE_URL}/jobs/${JOB_ID}")"
  STATE="$(json_field "${BODY}" state)"
  if [ "${STATE}" = "completed" ] || [ "${STATE}" = "failed" ]; then break; fi
  sleep 0.5
done
if [ "${STATE}" != "completed" ]; then
  echo "ERROR: job ended in state '${STATE}' (expected completed)" >&2
  printf '%s\n' "${BODY}" >&2
  docker logs "${TMP_WORKER}" 2>&1 | tail -40 >&2 || true
  exit 1
fi

# ── 9. Artifact bytes + ffprobe geometry. ──────────────────────────────────
HASH="$(json_field "${BODY}" artifact.artifact_hash)"
curl -fsS "${STORE_URL}/objects/${HASH}" -o "${OUT_FILE}"
if [ ! -s "${OUT_FILE}" ]; then
  echo "FAIL: status=completed but the artifact file is missing/empty" >&2; exit 1
fi
STORE_HASH="$(sha256sum "${OUT_FILE}" | cut -d' ' -f1)"
echo "artifact: ${OUT_FILE} ($(stat -c%s "${OUT_FILE}") bytes, sha256 ${STORE_HASH:0:12}...)"

PROBE="$(ffprobe -v error -select_streams v:0 \
  -show_entries stream=width,height,r_frame_rate,nb_frames \
  -show_entries format=duration -of json "${OUT_FILE}" 2>/dev/null || true)"
if [ -n "${PROBE}" ]; then
  P_WIDTH="$(json_field "${PROBE}" streams.0.width)"
  P_HEIGHT="$(json_field "${PROBE}" streams.0.height)"
  P_DURATION="$(json_field "${PROBE}" format.duration)"
  echo "  probe: ${P_WIDTH}x${P_HEIGHT}, duration=${P_DURATION}s"
  if [ "${P_WIDTH}" != "${EXPECTED_WIDTH}" ] || [ "${P_HEIGHT}" != "${EXPECTED_HEIGHT}" ]; then
    echo "FAIL: expected ${EXPECTED_WIDTH}x${EXPECTED_HEIGHT}, got ${P_WIDTH}x${P_HEIGHT}" >&2; exit 1
  fi
  python3 - "$P_DURATION" "$EXPECTED_DURATION" <<'PY'
import sys
d = float(sys.argv[1]); want = float(sys.argv[2])
if abs(d - want) > 0.15:
    print(f"ERROR: expected ~{want}s duration, got {d}s"); sys.exit(1)
PY
else
  echo "WARN: ffprobe not available; skipping geometry checks"
fi

# ── 10. DB assertions. ─────────────────────────────────────────────────────
if ! pg_init; then exit 1; fi
fails=0
check() { if [ "$2" = "$3" ]; then echo "  PASS ${1}: ${3}";
  else echo "  FAIL ${1}: expected [$2], got [$3]" >&2; fails=$((fails+1)); fi; }

ART="$(pg_query "SELECT id, job_id, sha256, COALESCE(drive_file_id,''), COALESCE(drive_link,'') FROM render_artifacts WHERE job_id='${JOB_ID}'")"
echo "render_artifacts: [${ART}]"
IFS='|' read -r ART_ID ART_JOB ART_SHA DRIVE_ID DRIVE_LINK <<< "${ART}"

check "artifact id == job id" "${JOB_ID}" "${ART_ID}"
check "artifact.job_id == job id" "${JOB_ID}" "${ART_JOB}"
check "db sha256 == object store sha256" "${STORE_HASH}" "${ART_SHA}"

if [[ -z "${DRIVE_ID}" || "${DRIVE_ID}" == mock-* ]]; then
  echo "FAIL: drive_file_id not a real Drive id: [${DRIVE_ID}]" >&2; fails=$((fails+1))
else
  echo "  PASS drive_file_id (real): ${DRIVE_ID}"
fi
if [[ "${DRIVE_LINK}" != *"drive.google.com"* ]]; then
  echo "FAIL: drive_link not a real Drive link: [${DRIVE_LINK}]" >&2; fails=$((fails+1))
else
  echo "  PASS drive_link: ${DRIVE_LINK}"
fi

JOB_ART="$(pg_query "SELECT COALESCE(artifact_id,'') FROM render_jobs WHERE id='${JOB_ID}'")"
check "render_jobs.artifact_id == artifact id" "${ART_ID}" "${JOB_ART}"

if [ "${fails}" -gt 0 ]; then
  echo "FAIL: ${fails} assertion(s) failed" >&2; exit 1
fi

echo ""
echo "OK: text+image overlay published to Google Drive"
echo "    folder: ${DRIVE_FOLDER_ID}"
echo "    file  : ${DRIVE_ID}"
echo "    link  : ${DRIVE_LINK}"
echo "    sha256: ${ART_SHA}"
echo "    local : ${OUT_FILE}"

echo ""
echo "Chronon worker report log:"
docker logs "${TMP_WORKER}" 2>&1 | rg -i "report|telemetry|render_ms|gpu_|readback|execution report" | tail -80 || true
snapshot_reports
REPORT_DIR="${REPORT_OUT_DIR}/${JOB_ID}"
TIMING_FILE="$(find "${REPORT_DIR}/jobs" -type f -name '*.timing.json' -print -quit 2>/dev/null || true)"
if [[ -n "${TIMING_FILE}" ]]; then
  echo ""
  echo "Chronon frame timing sidecar: ${TIMING_FILE}"
  python3 - "${TIMING_FILE}" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    d = json.load(f)
print(json.dumps({k: d[k] for k in ("wall_time_ms", "render_ms", "encode_close_ms", "frames_total", "job", "first_frame", "summary") if k in d}, indent=2))
PY
fi
if [[ -f "${REPORT_DIR}/render_history.jsonl" ]]; then
  echo ""
  echo "Chronon telemetry: ${REPORT_DIR}/render_history.jsonl"
  python3 - "${REPORT_DIR}/render_history.jsonl" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    rows = [json.loads(line) for line in f if line.strip()]
if not rows:
    raise SystemExit("telemetry file is empty")
r = rows[-1]
keys = (
    "wall_time_ms", "render_ms", "encode_ms", "process_startup_ms",
    "ffprobe_wall_ms", "sha256_wall_ms", "effective_fps", "frames_total",
    "frames_written", "gpu_execute_ms", "gpu_submit_cpu_ms",
    "gpu_wait_cpu_ms", "gpu_readback_ms", "video_conversion_wall_ms",
    "ffmpeg_pipe_write_wall_ms", "gpu_submissions", "passes_executed",
    "barrier_count", "gpu_readback_bytes",
)
for key in keys:
    if key in r:
        print(f"  {key}={r[key]}")
PY
else
  if [[ -z "${TIMING_FILE}" ]]; then
    echo "WARN: Chronon frame timing sidecar not found at ${REPORT_DIR}" >&2
  fi
fi
