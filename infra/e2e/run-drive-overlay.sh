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
NETWORK="${NETWORK:-docker_default}"
WORKER_IMAGE="${WORKER_IMAGE:-docker-worker:latest}"
DRIVE_FOLDER_ID="${DRIVE_FOLDER_ID:-1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS}"
EXPECTED_DURATION="${EXPECTED_DURATION:-5}"
EXPECTED_FRAMES="${EXPECTED_FRAMES:-150}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
COMPOSE_FILE="${REPO_ROOT}/infra/docker/docker-compose.yaml"
GOLDEN_DIR="${REPO_ROOT}/testdata/golden"
JOB_FILE="${JOB_FILE:-${GOLDEN_DIR}/golden-text-image-overlay-job-v1.json}"

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
  docker rm -f "${TMP_WORKER}" >/dev/null 2>&1 || true
  docker compose -f "${COMPOSE_FILE}" start worker worker-b >/dev/null 2>&1 || true
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

echo "== Real Drive overlay render (text + images) -> folder ${DRIVE_FOLDER_ID} =="
echo "payload: ${JOB_FILE}"

# ── 1. Build the submit payload with a unique id + job_id. ─────────────────
python3 - "${JOB_FILE}" "${JOB_ID}" "${WORK_DIR}/payload.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    payload = json.load(f)
payload["id"] = sys.argv[2]
payload["render_plan"]["job_id"] = sys.argv[2]
with open(sys.argv[3], "w", encoding="utf-8") as f:
    json.dump(payload, f)
PY

# ── 2. Upload the deterministic assets to the object store (L3). ───────────
ASSET_COUNT="$(python3 - "${JOB_FILE}" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    print(len(json.load(f)["assets"]))
PY
)"
for i in $(seq 0 $((ASSET_COUNT - 1))); do
  BODY="$(cat "${JOB_FILE}")"
  HASH="$(json_field "${BODY}" "assets.${i}.hash")"
  LOGICAL="$(json_field "${BODY}" "assets.${i}.logical_path")"
  LOCAL_FILE="${GOLDEN_DIR}/$(basename "${LOGICAL}")"
  if [ "$(sha256sum "${LOCAL_FILE}" | cut -d' ' -f1)" != "${HASH}" ]; then
    echo "ERROR: ${LOCAL_FILE} sha256 does not match payload hash ${HASH}" >&2; exit 1
  fi
  curl -fsS -X PUT --data-binary @"${LOCAL_FILE}" \
    -H 'Content-Type: application/octet-stream' \
    "${STORE_URL}/objects/${HASH}" >/dev/null
  echo "  asset: ${LOGICAL} (${HASH:0:12}...)"
done

# ── 3. Rebuild the worker image. ───────────────────────────────────────────
echo "rebuilding worker image ..."
docker compose -f "${COMPOSE_FILE}" build worker >/dev/null

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
  backend: software
  home: /opt/chronon3d
  mode: ipc
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
docker run -d --name "${TMP_WORKER}" \
  --network "${NETWORK}" \
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
  if [ "${STATE}" = "completed" ] || [ "${STATE}" = "failed" ] || [ "${STATE}" = "rendered" ]; then break; fi
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
  if [ "${P_WIDTH}" != "1280" ] || [ "${P_HEIGHT}" != "720" ]; then
    echo "FAIL: expected 1280x720, got ${P_WIDTH}x${P_HEIGHT}" >&2; exit 1
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
