#!/usr/bin/env bash
#
# Test 13 — Google Drive upload (happy path).
#
# A dedicated worker with the in-process mock Drive publisher renders a
# color-only job and publishes the MP4 to "Drive". The four contract checks:
#
#   1. the file is actually present on Drive (mock dir on disk)
#   2. the Drive link is saved in the DB (render_artifacts.drive_link)
#   3. the SHA256 is saved and matches the bytes (object store AND Drive file)
#   4. the artifact is linked to the correct job (render_jobs.artifact_id →
#      render_artifacts.id → job_id)
#
# The SHA256 is triple-checked: DB sha256 == object-store bytes == Drive bytes.
#
# Usage:
#   1. Start the stack:   (cd infra/docker && docker compose up --build -d)
#   2. Run:               infra/e2e/run-test13-drive-upload.sh
#
# Requires curl, python3, sha256sum, docker. PostgreSQL assertions are
# auto-detected (docker compose `postgres` service).
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
STORE_URL="${STORE_URL:-http://localhost:9000}"
OUT_FILE="${OUT_FILE:-/tmp/renderinggen-test13-result.mp4}"
NETWORK="${NETWORK:-docker_default}"
WORKER_IMAGE="${WORKER_IMAGE:-docker-worker:latest}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
COMPOSE_FILE="${REPO_ROOT}/infra/docker/docker-compose.yaml"

JOB_ID="test13-drive-$(date +%s)"
WORK_DIR="$(mktemp -d /tmp/test13-drive.XXXXXX)"
TMP_WORKER="test13-mock-worker"
DRIVE_DIR="/tmp/drive-mock"   # inside the worker container

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
    echo "ERROR: PostgreSQL not reachable — Test 13 requires the database" >&2
    return 1
  fi
}
pg_query() { [[ ${#PG_CMD[@]} -gt 0 ]] || return 2; "${PG_CMD[@]}" "$1"; }

cleanup() {
  docker rm -f "${TMP_WORKER}" >/dev/null 2>&1 || true
  docker compose -f "${COMPOSE_FILE}" start worker worker-b >/dev/null 2>&1 || true
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

echo "== Test 13: Drive upload (happy path) =="

# ── 1. Rebuild the worker image (Drive code + mock publisher). ─────────────
echo "rebuilding worker image (Drive code) ..."
docker compose -f "${COMPOSE_FILE}" build worker >/dev/null

# ── 2. Stop both main workers so only the mock-Drive worker claims. ────────
echo "stopping the main workers ..."
docker compose -f "${COMPOSE_FILE}" stop worker worker-b >/dev/null 2>&1

# ── 3. Mock-Drive worker config (no failure injected). ─────────────────────
cat > "${WORK_DIR}/worker-config.yaml" <<EOF
worker:
  id: renderinggen-mock-drive13
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
  mode: cli
  socket_path: /var/run/chronon3d/chronon.sock
gpu:
  device: 0
health:
  addr: ":8080"
drive:
  enabled: true
  mode: mock
  mock_dir: ${DRIVE_DIR}
EOF

# ── 4. Run the dedicated mock-Drive worker on the compose network. ─────────
echo "starting the mock-Drive worker (${WORKER_IMAGE}) ..."
docker run -d --name "${TMP_WORKER}" \
  --network "${NETWORK}" \
  -v "${WORK_DIR}/worker-config.yaml:/etc/renderinggen/config.yaml:ro" \
  "${WORKER_IMAGE}" >/dev/null
sleep 6   # let the worker reach READY and start claiming

# ── 5. Submit a self-contained color-only job (3s, 1280x720). ─────────────
cat > "${WORK_DIR}/payload.json" <<EOF
{
  "id": "${JOB_ID}",
  "schema": "renderinggen.job",
  "version": 1,
  "render_plan": {
    "schema": "chronon.render-plan",
    "version": 1,
    "job_id": "${JOB_ID}",
    "canvas": { "width": 1280, "height": 720, "fps": 30, "duration_frames": 90 },
    "layers": [
      { "id": "background", "type": "color", "color": [0.08, 0.12, 0.25, 1.0] }
    ],
    "output": { "path": "result.mp4", "format": "mp4", "codec": "h264" }
  },
  "assets": []
}
EOF

echo "submitting job ${JOB_ID} ..."
SUBMIT_CODE="$(curl -s -o "${WORK_DIR}/submit.body" -w '%{http_code}' \
  -X POST "${QUEUE_URL}/jobs" -H 'Content-Type: application/json' \
  --data-binary @"${WORK_DIR}/payload.json")"
if [ "${SUBMIT_CODE}" != "201" ] && [ "${SUBMIT_CODE}" != "200" ]; then
  echo "ERROR: submit returned HTTP ${SUBMIT_CODE}" >&2; cat "${WORK_DIR}/submit.body" >&2; exit 1
fi

# ── 6. Wait for the job to complete. ───────────────────────────────────────
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
  exit 1
fi

# ── 7. PASS only if the artifact bytes are real (object store). ───────────
HASH="$(json_field "${BODY}" artifact.artifact_hash)"
curl -fsS "${STORE_URL}/objects/${HASH}" -o "${OUT_FILE}"
if [ ! -s "${OUT_FILE}" ]; then
  echo "FAIL: status=completed but the artifact file is missing/empty" >&2; exit 1
fi
STORE_HASH="$(sha256sum "${OUT_FILE}" | cut -d' ' -f1)"
echo "artifact: ${OUT_FILE} ($(stat -c%s "${OUT_FILE}") bytes)"

# ── 8. Drive: the file must really be present and byte-identical. ─────────
DRIVE_LS="$(docker exec "${TMP_WORKER}" sh -c "ls -1 ${DRIVE_DIR}/ 2>/dev/null")"
if [ -z "${DRIVE_LS}" ]; then
  echo "FAIL: no file found on Drive (${DRIVE_DIR})" >&2; exit 1
fi
DRIVE_HASH="$(docker exec "${TMP_WORKER}" sh -c "sha256sum ${DRIVE_DIR}/* | cut -d' ' -f1")"
echo "Drive files:"
echo "${DRIVE_LS}" | sed 's/^/  /'
echo "  drive sha256 = ${DRIVE_HASH}"

# ── 9. DB assertions. ─────────────────────────────────────────────────────
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
check "db sha256 == drive bytes sha256" "${DRIVE_HASH}" "${ART_SHA}"

if [[ "${DRIVE_ID}" != mock-* ]]; then
  echo "FAIL: drive_file_id not populated: [${DRIVE_ID}]" >&2; fails=$((fails+1))
else
  echo "  PASS drive_file_id: ${DRIVE_ID}"
fi
if [[ "${DRIVE_LINK}" != *"drive.example.com"* ]]; then
  echo "FAIL: drive_link not populated: [${DRIVE_LINK}]" >&2; fails=$((fails+1))
else
  echo "  PASS drive_link: ${DRIVE_LINK}"
fi

# Artifact ↔ job linkage is bidirectional and exact.
JOB_ART="$(pg_query "SELECT COALESCE(artifact_id,'') FROM render_jobs WHERE id='${JOB_ID}'")"
check "render_jobs.artifact_id == artifact id" "${ART_ID}" "${JOB_ART}"

if [ "${fails}" -gt 0 ]; then
  echo "FAIL: ${fails} assertion(s) failed" >&2; exit 1
fi

echo ""
echo "OK: Test 13 passed"
echo "    file present on Drive + link in DB + sha256 saved"
echo "    sha256(db) == sha256(store) == sha256(drive) = ${ART_SHA:0:12}..."
echo "    artifact ${ART_ID} linked to job ${JOB_ID} (both directions)"
echo "    artifact: ${OUT_FILE}"
