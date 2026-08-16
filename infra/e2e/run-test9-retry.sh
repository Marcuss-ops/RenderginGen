#!/usr/bin/env bash
#
# Test 9 — Attempt history is preserved across a failed render and its retry.
#
# Submits a concrete chronon.render-plan.v1 job whose image overlay references
# an asset that does NOT exist yet in the object store (the "nonexistent image"
# case). The first attempt fails at asset materialization — the same failure a
# real worker reports as `workspace: resolve <hash>: object not found`. Then the
# image is uploaded and the worker retries: attempt 1 = failed, attempt 2 =
# completed, and attempt 1 is never lost.
#
# Why the worker is stopped for attempt 1: the worker retries a failed job
# immediately (no backoff), so to demonstrate a clean "fail once → fix → retry"
# with a single, deterministic script we drive attempt 1 through the queue HTTP
# API (the exact endpoints the worker itself calls) and let the real worker run
# attempt 2 after the fix is uploaded. attempt 2 therefore renders through the
# real chronon3d_cli and publishes a real artifact.
#
# DB assertions (PostgreSQL):
#   - render_attempts has exactly 2 rows: #1 failed (test9-w1, error_message),
#     #2 completed (renderinggen-local)
#   - render_jobs: state=completed, attempt_count=2
#   - render_events: JOB_CREATED, JOB_CLAIMED×2, JOB_REQUEUED, JOB_COMPLETED
#
# Usage:
#   1. Start the stack:   (cd infra/docker && docker compose up --build -d)
#   2. Run:               infra/e2e/run-test9-retry.sh
#
# Requires curl, python3, ffprobe, sha256sum, docker. PostgreSQL assertions are
# auto-detected (docker compose `postgres` service).
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
STORE_URL="${STORE_URL:-http://localhost:9000}"
OUT_FILE="${OUT_FILE:-/tmp/renderinggen-test9-result.mp4}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
COMPOSE_FILE="${REPO_ROOT}/infra/docker/docker-compose.yaml"

JOB_ID="test9-retry-$(date +%s)"
WORKER_W1="test9-w1"          # manual driver for the failing attempt
WORKER_W2="renderinggen-local" # the real worker that runs the retry

WORK_DIR="$(mktemp -d /tmp/test9-retry.XXXXXX)"

json_field() { printf '%s' "$1" | python3 -c 'import sys,json; d=json.load(sys.stdin)
for p in sys.argv[1].split("."):
    d = d[int(p)] if p.isdigit() else d[p]
print(d)' "$2"; }

# ── PostgreSQL (auto-detected) ─────────────────────────────────────────────
PG_CMD=()
pg_init() {
  if command -v psql >/dev/null 2>&1 && [[ -n "${DATABASE_URL:-}" ]]; then
    PG_CMD=(psql "${DATABASE_URL}" -At -F'|' -c)
  elif docker compose -f "${COMPOSE_FILE}" exec -T postgres \
      psql -U renderinggen -d renderinggen -At -F'|' -c 'SELECT 1' >/dev/null 2>&1; then
    PG_CMD=(docker compose -f "${COMPOSE_FILE}" exec -T postgres \
      psql -U renderinggen -d renderinggen -At -F'|' -c)
  else
    echo "ERROR: PostgreSQL not reachable — Test 9 requires the database" >&2
    return 1
  fi
}
pg_query() { [[ ${#PG_CMD[@]} -gt 0 ]] || return 2; "${PG_CMD[@]}" "$1"; }

# ── Worker control (stop for the manual attempt 1, restart before attempt 2) ─
worker_stop() { docker compose -f "${COMPOSE_FILE}" stop worker >/dev/null 2>&1; }
worker_start() { docker compose -f "${COMPOSE_FILE}" start worker >/dev/null 2>&1; }
cleanup() { worker_start || true; rm -rf "${WORK_DIR}"; }
trap cleanup EXIT   # always leave the worker running and drop the temp dir

echo "== Test 9: attempt history preserved across fail → retry =="

# ── 1. Stop the worker so it cannot steal the job before attempt 1. ───────
echo "stopping worker (attempt 1 is driven via the queue HTTP API) ..."
worker_stop
sleep 1

# ── 2. Generate the overlay image and compute its content hash. ───────────
python3 - "${WORK_DIR}" <<'PY'
import sys
from PIL import Image
out = sys.argv[1]
Image.new("RGB", (300, 150), (255, 140, 0)).save(f"{out}/overlay.png")
PY
IMG_HASH="$(sha256sum "${WORK_DIR}/overlay.png" | cut -d' ' -f1)"
echo "overlay image: ${WORK_DIR}/overlay.png (sha256=${IMG_HASH:0:12}... — NOT uploaded yet)"

# ── 3. Submit the job: color bg + image overlay referencing the missing asset.
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
      { "id": "background", "type": "color", "color": [0.03, 0.03, 0.05, 1.0] },
      {
        "id": "image_overlay",
        "type": "image",
        "asset": "assets/overlay.png",
        "box_width": 300,
        "box_height": 300,
        "fit": "contain",
        "position": [0, 0],
        "start_frame": 15,
        "duration_frames": 60
      }
    ],
    "output": { "path": "result.mp4", "format": "mp4", "codec": "h264" }
  },
  "assets": [
    { "hash": "${IMG_HASH}", "logical_path": "assets/overlay.png" }
  ]
}
EOF

echo "submitting job ${JOB_ID} (image asset intentionally absent) ..."
SUBMIT_CODE="$(curl -s -o "${WORK_DIR}/submit.body" -w '%{http_code}' \
  -X POST "${QUEUE_URL}/jobs" -H 'Content-Type: application/json' \
  --data-binary @"${WORK_DIR}/payload.json")"
if [ "${SUBMIT_CODE}" != "201" ] && [ "${SUBMIT_CODE}" != "200" ]; then
  echo "ERROR: submit returned HTTP ${SUBMIT_CODE}" >&2; cat "${WORK_DIR}/submit.body" >&2; exit 1
fi
echo "  submitted (HTTP ${SUBMIT_CODE})"

# ── 4. Claim attempt 1 through the queue API (as the worker would). ───────
CLAIM="$(curl -fsS -X POST "${QUEUE_URL}/jobs/claim" \
  -H 'Content-Type: application/json' -d "{\"worker\":\"${WORKER_W1}\"}")"
CLAIMED_ID="$(json_field "${CLAIM}" id)"
if [ "${CLAIMED_ID}" != "${JOB_ID}" ]; then
  echo "ERROR: expected to claim ${JOB_ID}, got ${CLAIMED_ID}" >&2; exit 1
fi
echo "attempt 1: claimed by ${WORKER_W1} (render_attempt #1 = running)"

# ── 5. Prove the image source genuinely does not exist (the failure premise). ─
HTTP_CODE="$(curl -s -o /dev/null -w '%{http_code}' "${STORE_URL}/objects/${IMG_HASH}")"
if [ "${HTTP_CODE}" != "404" ]; then
  echo "ERROR: expected 404 for missing asset, got HTTP ${HTTP_CODE}" >&2; exit 1
fi
echo "premise: object store returns 404 for the image (${IMG_HASH:0:12}...)"

# ── 6. Fail attempt 1 with the same reason the real worker would report. ──
FAIL_REASON="workspace: resolve ${IMG_HASH}: object not found"
curl -fsS -X POST "${QUEUE_URL}/jobs/${JOB_ID}/fail" \
  -H 'Content-Type: application/json' \
  -d "{\"worker\":\"${WORKER_W1}\",\"data\":{\"reason\":\"${FAIL_REASON}\"}}"
echo "attempt 1: reported failed (reason: ${FAIL_REASON})"

# ── 7. Verify attempt 1 is recorded as failed and the job was requeued. ───
if ! pg_init; then exit 1; fi
ATT1="$(pg_query "SELECT status, worker_id, COALESCE(error_message,'') FROM render_attempts WHERE job_id='${JOB_ID}' AND attempt_number=1")"
JOB_STATE="$(pg_query "SELECT state FROM render_jobs WHERE id='${JOB_ID}'")"
echo "after fail: render_attempt #1 = [${ATT1}] | render_jobs.state = ${JOB_STATE}"
if [[ "${ATT1}" != failed\|${WORKER_W1}\|* ]]; then
  echo "ERROR: attempt 1 was not recorded as failed: [${ATT1}]" >&2; exit 1
fi
if [ "${JOB_STATE}" != "pending" ]; then
  echo "ERROR: expected job to be requeued (pending), got '${JOB_STATE}'" >&2; exit 1
fi
echo "  PASS: attempt 1 = failed, job requeued to pending"

# ── 8. Fix the input: upload the image that was missing. ───────────────────
echo "uploading the image (the fix) ..."
curl -fsS -X PUT --data-binary @"${WORK_DIR}/overlay.png" \
  -H 'Content-Type: application/octet-stream' "${STORE_URL}/objects/${IMG_HASH}" >/dev/null

# ── 9. Restart the real worker to run attempt 2. ──────────────────────────
echo "starting the real worker for attempt 2 ..."
worker_start

echo "waiting for the worker to claim and complete attempt 2 ..."
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
echo "attempt 2: completed by ${WORKER_W2}"

# ── 10. PASS only if the artifact file is really produced. ────────────────
HASH="$(json_field "${BODY}" artifact.artifact_hash)"
curl -fsS "${STORE_URL}/objects/${HASH}" -o "${OUT_FILE}"
if [ ! -s "${OUT_FILE}" ]; then
  echo "FAIL: status=completed but the artifact file is missing/empty" >&2; exit 1
fi
FS_HASH="$(sha256sum "${OUT_FILE}" | cut -d' ' -f1)"
if [ "${FS_HASH}" != "${HASH}" ]; then
  echo "FAIL: downloaded sha256 ${FS_HASH} != artifact_hash ${HASH}" >&2; exit 1
fi
echo "  artifact: ${OUT_FILE} (${FS_HASH:0:12}..., $(stat -c%s "${OUT_FILE}") bytes)"

# ── 11. DB: attempt history is preserved, attempt 1 is not lost. ──────────
fails=0
check() { # $1 label, $2 expected, $3 actual
  if [ "$2" = "$3" ]; then echo "  PASS ${1}: ${3}";
  else echo "  FAIL ${1}: expected [$2], got [$3]" >&2; fails=$((fails+1)); fi
}

ATTEMPTS="$(pg_query "SELECT attempt_number, status, worker_id FROM render_attempts WHERE job_id='${JOB_ID}' ORDER BY attempt_number")"
echo "render_attempts:"
echo "${ATTEMPTS}" | sed 's/^/  /'
if [ "$(printf '%s\n' "${ATTEMPTS}" | wc -l)" -ne 2 ]; then
  echo "FAIL: expected exactly 2 attempts, got:" >&2; echo "${ATTEMPTS}" >&2; exit 1
fi

IFS=$'\n' read -rd '' -a ATT_LINES <<< "${ATTEMPTS}" || true
check "attempt 1 number+status+worker" "1|failed|${WORKER_W1}" "${ATT_LINES[0]}"
check "attempt 2 number+status+worker" "2|completed|${WORKER_W2}" "${ATT_LINES[1]}"

ATT1_ERR="$(pg_query "SELECT COALESCE(error_message,'') FROM render_attempts WHERE job_id='${JOB_ID}' AND attempt_number=1")"
if [ -z "${ATT1_ERR}" ]; then
  echo "FAIL: attempt 1 error_message is empty" >&2; fails=$((fails+1))
else
  echo "  PASS attempt 1 error_message: ${ATT1_ERR}"
fi

check "render_jobs.attempt_count" "2" "$(pg_query "SELECT attempt_count FROM render_jobs WHERE id='${JOB_ID}'")"
check "render_jobs.state" "completed" "$(pg_query "SELECT state FROM render_jobs WHERE id='${JOB_ID}'")"

EVENTS="$(pg_query "SELECT event_type, COALESCE(worker_id,''), COALESCE(attempt_id,'') FROM render_events WHERE job_id='${JOB_ID}' ORDER BY id")"
echo "render_events:"
echo "${EVENTS}" | sed 's/^/  /'
for ev in "JOB_CREATED||" "JOB_CLAIMED|${WORKER_W1}|${JOB_ID}#1" "JOB_REQUEUED|${WORKER_W1}|${JOB_ID}#1" "JOB_CLAIMED|${WORKER_W2}|${JOB_ID}#2" "JOB_COMPLETED|${WORKER_W2}|${JOB_ID}#2"; do
  if ! printf '%s\n' "${EVENTS}" | grep -qF "${ev}"; then
    echo "FAIL: missing event [${ev}]" >&2; fails=$((fails+1))
  fi
done

if [ "${fails}" -gt 0 ]; then
  echo "FAIL: ${fails} attempt-history assertion(s) failed" >&2; exit 1
fi

echo ""
echo "OK: Test 9 passed"
echo "    attempt 1 = failed (${WORKER_W1}, '${ATT1_ERR}') — preserved"
echo "    attempt 2 = completed (${WORKER_W2}) with a real artifact"
echo "    render_jobs.attempt_count = 2, events = JOB_REQUEUED then JOB_COMPLETED"
echo "    artifact: ${OUT_FILE}"
