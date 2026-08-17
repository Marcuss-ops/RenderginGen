#!/usr/bin/env bash
#
# Test 14 — Google Drive publication failure keeps a job "rendered" (not
# completed) and the retry re-runs only the publication, never the Chronon
# render.
#
# The worker's Drive publisher is decoupled from rendering. This smoke runs a
# dedicated worker whose Drive publisher is the in-process Mock configured to
# fail its first upload (mock_fail_first=1). A color-only job is submitted and:
#
#   attempt 1 -> render OK, Drive upload FAILS -> job state "rendered"
#   attempt 2 -> publication-only retry (no render) -> completed
#
# Verified against PostgreSQL:
#   - render_attempts #1 = "rendered" (drive error), #2 = "completed"
#   - render_jobs.attempt_count = 2, state = completed
#   - processing_metrics: #1 has render_ms, #2 has drive_publish_ms but NO
#     render_ms (proof that Chronon did not run again)
#   - render_artifacts.drive_file_id / drive_link are populated
#
# Usage:
#   1. Start the stack:   (cd infra/docker && docker compose up --build -d)
#   2. Run:               infra/e2e/run-test14-drive-retry.sh
#
# The script rebuilds the queue and worker images (to pick up the "rendered"
# state + Drive code), restarts the queue (applies migration 012), and runs a
# dedicated mock-Drive worker container, restoring the main worker afterwards.
#
# Requires curl, python3, sha256sum, docker.
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
STORE_URL="${STORE_URL:-http://localhost:9000}"
OUT_FILE="${OUT_FILE:-/tmp/renderinggen-test14-result.mp4}"
NETWORK="${NETWORK:-docker_default}"
WORKER_IMAGE="${WORKER_IMAGE:-docker-worker:latest}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
COMPOSE_FILE="${REPO_ROOT}/infra/docker/docker-compose.yaml"

JOB_ID="test14-drive-$(date +%s)"
WORK_DIR="$(mktemp -d /tmp/test14-drive.XXXXXX)"
TMP_WORKER="test14-mock-worker"

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
    echo "ERROR: PostgreSQL not reachable — Test 14 requires the database" >&2
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

echo "== Test 14: Drive upload failure → rendered → publication-only retry =="

# ── 1. Rebuild queue + worker (rendered state + Drive code). ──────────────
echo "rebuilding queue + worker images ..."
docker compose -f "${COMPOSE_FILE}" build queue worker >/dev/null

# ── 2. Restart the queue so it applies migration 012. ─────────────────────
echo "restarting queue (applies migration 012: rendered state + drive columns) ..."
docker compose -f "${COMPOSE_FILE}" up -d queue >/dev/null

# ── 3. Stop the main workers; a dedicated mock-Drive worker runs instead. ──
echo "stopping the main workers ..."
docker compose -f "${COMPOSE_FILE}" stop worker worker-b >/dev/null 2>&1

# ── 4. Write a mock-Drive worker config: first upload fails. ──────────────
cat > "${WORK_DIR}/worker-config.yaml" <<EOF
worker:
  id: renderinggen-mock-drive
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
  mock_dir: /tmp/drive-mock
  mock_fail_first: 1
EOF

# ── 5. Run the dedicated mock-Drive worker on the compose network. ────────
echo "starting the mock-Drive worker (${WORKER_IMAGE}) ..."
docker run -d --name "${TMP_WORKER}" \
  --network "${NETWORK}" \
  -v "${WORK_DIR}/worker-config.yaml:/etc/renderinggen/config.yaml:ro" \
  "${WORKER_IMAGE}" >/dev/null
sleep 6   # let the worker reach READY and start claiming

# ── 6. Submit a self-contained color-only job (3s, 1280x720). ─────────────
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

# ── 7. Wait for the job to finish (render → rendered → retry → completed). ─
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

# ── 8. PASS only if the artifact file is really produced. ─────────────────
HASH="$(json_field "${BODY}" artifact.artifact_hash)"
curl -fsS "${STORE_URL}/objects/${HASH}" -o "${OUT_FILE}"
if [ ! -s "${OUT_FILE}" ]; then
  echo "FAIL: status=completed but the artifact file is missing/empty" >&2; exit 1
fi
echo "artifact: ${OUT_FILE} ($(stat -c%s "${OUT_FILE}") bytes)"

# ── 9. DB assertions. ─────────────────────────────────────────────────────
if ! pg_init; then exit 1; fi
fails=0
check() { # $1 label, $2 expected, $3 actual
  if [ "$2" = "$3" ]; then echo "  PASS ${1}: ${3}";
  else echo "  FAIL ${1}: expected [$2], got [$3]" >&2; fails=$((fails+1)); fi
}

ATT1="$(pg_query "SELECT status FROM render_attempts WHERE job_id='${JOB_ID}' AND attempt_number=1")"
ATT2="$(pg_query "SELECT status FROM render_attempts WHERE job_id='${JOB_ID}' AND attempt_number=2")"
echo "attempt statuses: #1=${ATT1}, #2=${ATT2}"
check "attempt 1 status" "rendered" "${ATT1}"
check "attempt 2 status" "completed" "${ATT2}"

ATT1_ERR="$(pg_query "SELECT COALESCE(error_message,'') FROM render_attempts WHERE job_id='${JOB_ID}' AND attempt_number=1")"
if [[ "${ATT1_ERR}" != *"drive"* ]]; then
  echo "FAIL: attempt 1 should carry the Drive error, got [${ATT1_ERR}]" >&2; fails=$((fails+1))
else
  echo "  PASS attempt 1 drive error: ${ATT1_ERR}"
fi

check "render_jobs.attempt_count" "2" "$(pg_query "SELECT attempt_count FROM render_jobs WHERE id='${JOB_ID}'")"
check "render_jobs.state" "completed" "$(pg_query "SELECT state FROM render_jobs WHERE id='${JOB_ID}'")"

# No re-render: attempt 1 measured a render, attempt 2 only published.
M1="$(pg_query "SELECT string_agg(metric_name, ',' ORDER BY metric_name) FROM processing_metrics WHERE job_id='${JOB_ID}' AND attempt_id='${JOB_ID}#1'")"
M2="$(pg_query "SELECT string_agg(metric_name, ',' ORDER BY metric_name) FROM processing_metrics WHERE job_id='${JOB_ID}' AND attempt_id='${JOB_ID}#2'")"
echo "metrics #1: ${M1}"
echo "metrics #2: ${M2}"
if [[ "${M1}" != *"render_ms"* ]]; then
  echo "FAIL: attempt 1 should include render_ms" >&2; fails=$((fails+1))
fi
if [[ "${M2}" == *"render_ms"* ]]; then
  echo "FAIL: attempt 2 re-rendered (render_ms present)" >&2; fails=$((fails+1))
fi
if [[ "${M2}" != *"drive_publish_ms"* ]]; then
  echo "FAIL: attempt 2 should include drive_publish_ms" >&2; fails=$((fails+1))
fi
echo "  PASS no re-render: attempt 2 metrics = [${M2}] (no render_ms)"

DRIVE="$(pg_query "SELECT COALESCE(drive_file_id,''), COALESCE(drive_link,'') FROM render_artifacts WHERE job_id='${JOB_ID}'")"
echo "render_artifacts drive: [${DRIVE}]"
if [[ "${DRIVE}" != *"mock-"* ]]; then
  echo "FAIL: drive_file_id not populated: [${DRIVE}]" >&2; fails=$((fails+1))
fi
if [[ "${DRIVE}" != *"drive.example.com"* ]]; then
  echo "FAIL: drive_link not populated: [${DRIVE}]" >&2; fails=$((fails+1))
fi

if [ "${fails}" -gt 0 ]; then
  echo "FAIL: ${fails} assertion(s) failed" >&2; exit 1
fi

echo ""
echo "OK: Test 14 passed"
echo "    attempt 1 = rendered (Drive upload failed) — job never marked completed early"
echo "    attempt 2 = completed via publication-only retry (no Chronon re-render)"
echo "    render_artifacts.drive_file_id / drive_link populated"
echo "    artifact: ${OUT_FILE}"
