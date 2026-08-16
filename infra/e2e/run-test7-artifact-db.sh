#!/usr/bin/env bash
#
# Test 7 — render_artifacts persistence certification.
#
# After a successful render, the render_artifacts row must contain the full
# artifact record — artifact id, job_id, storage_key, sha256, size_bytes,
# duration, width, height and created_at — and every value must match the
# file actually produced (never trust status=completed alone).
#
# The job is a self-contained color-only render (1280x720 @ 30fps, 3s).
#
# Usage:
#   1. Start the stack:   (cd infra/docker && docker compose up --build -d)
#   2. Run:               infra/e2e/run-test7-artifact-db.sh
#
# Requires curl, python3, ffprobe, sha256sum. PostgreSQL assertions are
# auto-detected (docker compose `postgres` service or DATABASE_URL + psql).
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
STORE_URL="${STORE_URL:-http://localhost:9000}"
OUT_FILE="${OUT_FILE:-/tmp/renderinggen-test7-result.mp4}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"

JOB_ID="test7-artifact-db-$(date +%s)"
WORK_DIR="$(mktemp -d /tmp/test7-artifact-db.XXXXXX)"
trap 'rm -rf "$WORK_DIR"' EXIT

json_field() { printf '%s' "$1" | python3 -c 'import sys,json; d=json.load(sys.stdin)
for p in sys.argv[1].split("."):
    d = d[int(p)] if p.isdigit() else d[p]
print(d)' "$2"; }

# ── PostgreSQL (auto-detected) ─────────────────────────────────────────────
PG_CMD=()
pg_init() {
  if command -v psql >/dev/null 2>&1 && [[ -n "${DATABASE_URL:-}" ]]; then
    PG_CMD=(psql "${DATABASE_URL}" -At -F'|' -c)
  elif docker compose -f "${REPO_ROOT}/infra/docker/docker-compose.yaml" \
      exec -T postgres psql -U renderinggen -d renderinggen -At -F'|' -c 'SELECT 1' >/dev/null 2>&1; then
    PG_CMD=(docker compose -f "${REPO_ROOT}/infra/docker/docker-compose.yaml" \
      exec -T postgres psql -U renderinggen -d renderinggen -At -F'|' -c)
  else
    echo "ERROR: PostgreSQL not reachable — Test 7 requires the database" >&2
    return 1
  fi
}

echo "== Test 7: render_artifacts persistence =="

# ── 1. Submit a self-contained color-only job (3s). ───────────────────────
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
if [ "${SUBMIT_CODE}" != "201" ] && [ "${SUBMIT_CODE}" != "200" ] && [ "${SUBMIT_CODE}" != "409" ]; then
  echo "ERROR: submit returned HTTP ${SUBMIT_CODE}" >&2; cat "${WORK_DIR}/submit.body" >&2; exit 1
fi

for _ in $(seq 1 400); do
  BODY="$(curl -fsS "${QUEUE_URL}/jobs/${JOB_ID}")"
  STATE="$(json_field "${BODY}" state)"
  if [ "${STATE}" = "completed" ] || [ "${STATE}" = "failed" ]; then break; fi
  sleep 0.25
done
if [ "${STATE}" != "completed" ]; then
  echo "ERROR: job ended in state '${STATE}'" >&2; printf '%s\n' "${BODY}" >&2; exit 1
fi

HASH="$(json_field "${BODY}" artifact.artifact_hash)"

# ── 2. Download the artifact and probe the real file. ─────────────────────
curl -fsS "${STORE_URL}/objects/${HASH}" -o "${OUT_FILE}"
FS_HASH="$(sha256sum "${OUT_FILE}" | cut -d' ' -f1)"
FS_SIZE="$(stat -c%s "${OUT_FILE}")"
PROBE="$(ffprobe -v error -select_streams v:0 \
  -show_entries stream=width,height,r_frame_rate \
  -show_entries format=duration -of json "${OUT_FILE}")"
FS_WIDTH="$(json_field "${PROBE}" streams.0.width)"
FS_HEIGHT="$(json_field "${PROBE}" streams.0.height)"
FS_DURATION="$(json_field "${PROBE}" format.duration)"
FS_DURATION_US="$(python3 -c "print(int(round(float('${FS_DURATION}') * 1_000_000)))")"
echo "file: ${FS_WIDTH}x${FS_HEIGHT}, ${FS_DURATION}s, ${FS_SIZE} bytes"

# ── 3. Read the render_artifacts row and verify every field. ──────────────
if ! pg_init; then exit 1; fi
# id|job_id|storage_key|sha256|size_bytes|width|height|duration_us|created_at
ROW="$("${PG_CMD[@]}" "SELECT id, job_id, storage_key, sha256, size_bytes, width, height, duration_us, created_at FROM render_artifacts WHERE job_id = '${JOB_ID}'")"
if [ -z "${ROW}" ]; then
  echo "FAIL: no render_artifacts row for job ${JOB_ID}" >&2
  exit 1
fi

IFS='|' read -r ART_ID ART_JOB STORAGE_KEY ART_SHA ART_SIZE ART_WIDTH ART_HEIGHT ART_DUR_US ART_CREATED <<< "${ROW}"

fails=0
check() { # $1 label, $2 expected, $3 actual
  if [ "$2" = "$3" ]; then
    echo "  PASS ${1}: ${3}"
  else
    echo "  FAIL ${1}: expected [$2], got [$3]" >&2
    fails=$((fails + 1))
  fi
}
check_nonempty() { # $1 label, $2 value
  if [ -n "$2" ]; then
    echo "  PASS ${1}: ${2}"
  else
    echo "  FAIL ${1}: empty" >&2
    fails=$((fails + 1))
  fi
}

check_nonempty "artifact_id" "${ART_ID}"
check "job_id" "${JOB_ID}" "${ART_JOB}"
check "storage_key" "${HASH}" "${STORAGE_KEY}"
check "sha256" "${FS_HASH}" "${ART_SHA}"
check "size_bytes" "${FS_SIZE}" "${ART_SIZE}"
check "width" "${FS_WIDTH}" "${ART_WIDTH}"
check "height" "${FS_HEIGHT}" "${ART_HEIGHT}"
check "duration_us" "${FS_DURATION_US}" "${ART_DUR_US}"
check_nonempty "created_at" "${ART_CREATED}"

if [ "${fails}" -gt 0 ]; then
  echo "FAIL: ${fails} artifact DB assertion(s) failed" >&2
  exit 1
fi

echo ""
echo "OK: Test 7 passed"
echo "    render_artifacts row for ${JOB_ID} matches the produced file exactly"
echo "    artifact_id=${ART_ID}, ${ART_WIDTH}x${ART_HEIGHT}, ${ART_SIZE} bytes, duration_us=${ART_DUR_US}, sha256=${ART_SHA}"
