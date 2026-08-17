#!/usr/bin/env bash
#
# Test 11 — Two real workers, no job rendered by both.
#
# Starts two workers with distinct ids (worker = renderinggen-local,
# worker-b = renderinggen-b), submits 20 self-contained color-only jobs, and
# waits for both workers to render them. The atomic claim must partition the
# jobs cleanly: every job is rendered exactly once by exactly one worker —
# never `job07 -> worker-a` *and* `job07 -> worker-b`.
#
# DB assertions (PostgreSQL):
#   - 20 jobs complete, each with exactly one attempt (attempt_number = 1)
#   - 0 jobs with more than one attempt  (no double claim/render)
#   - both workers participate (each rendered >= 1 job)
#   - 20 artifacts with a verifiable sha256 + size
#
# Usage:
#   1. Start the stack:   (cd infra/docker && docker compose up --build -d)
#   2. Run:               infra/e2e/run-test11-two-workers.sh
#
# Requires curl, python3, docker. PostgreSQL assertions are auto-detected
# (docker compose `postgres` service).
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
COMPOSE_FILE="${REPO_ROOT}/infra/docker/docker-compose.yaml"

JOBS="${JOBS:-20}"
RUN_ID="$(date +%s)"
PREFIX="test11-${RUN_ID}"

WORK_DIR="$(mktemp -d /tmp/test11-two-workers.XXXXXX)"

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
    echo "ERROR: PostgreSQL not reachable — Test 11 requires the database" >&2
    return 1
  fi
}
pg_query() { [[ ${#PG_CMD[@]} -gt 0 ]] || return 2; "${PG_CMD[@]}" "$1"; }

echo "== Test 11: two workers, ${JOBS} jobs, no double render =="

# ── 1. Ensure both workers are running (idempotent). ───────────────────────
echo "ensuring worker + worker-b are up ..."
docker compose -f "${COMPOSE_FILE}" up -d worker worker-b >/dev/null 2>&1
sleep 3   # let the worker-b daemon become ready before jobs are submitted

# ── 2. Submit 20 jobs (color-only; renders a real MP4 per job). ────────────
echo "submitting ${JOBS} jobs (${PREFIX}00..$((JOBS-1))) ..."
for i in $(seq 0 $((JOBS - 1))); do
  JID="$(printf '%s%02d' "${PREFIX}" "${i}")"
  cat > "${WORK_DIR}/payload.json" <<EOF
{
  "id": "${JID}",
  "schema": "renderinggen.job",
  "version": 1,
  "render_plan": {
    "schema": "chronon.render-plan",
    "version": 1,
    "job_id": "${JID}",
    "canvas": { "width": 1280, "height": 720, "fps": 30, "duration_frames": 90 },
    "layers": [ { "id": "bg", "type": "color", "color": [0.08, 0.12, 0.25, 1.0] } ],
    "output": { "path": "result.mp4", "format": "mp4", "codec": "h264" }
  },
  "assets": []
}
EOF
  CODE="$(curl -s -o /dev/null -w '%{http_code}' -X POST "${QUEUE_URL}/jobs" \
    -H 'Content-Type: application/json' --data-binary @"${WORK_DIR}/payload.json")"
  if [ "${CODE}" != "201" ] && [ "${CODE}" != "200" ]; then
    echo "ERROR: submit ${JID} returned HTTP ${CODE}" >&2; exit 1
  fi
done
echo "  ${JOBS} jobs submitted"

# ── 3. Wait for all 20 to complete. ────────────────────────────────────────
if ! pg_init; then exit 1; fi
echo "waiting for both workers to render all ${JOBS} jobs ..."
STILL="?"
for _ in $(seq 1 1200); do
  STILL="$(pg_query "SELECT COUNT(*) FROM render_jobs WHERE id LIKE '${PREFIX}%' AND state IN ('pending','running')")"
  if [ "${STILL}" = "0" ]; then break; fi
  sleep 0.5
done

FAILED_CNT="$(pg_query "SELECT COUNT(*) FROM render_jobs WHERE id LIKE '${PREFIX}%' AND state = 'failed'")"
COMPLETED_CNT="$(pg_query "SELECT COUNT(*) FROM render_jobs WHERE id LIKE '${PREFIX}%' AND state = 'completed'")"
echo "  completed = ${COMPLETED_CNT}, failed = ${FAILED_CNT}"
if [ "${COMPLETED_CNT}" != "${JOBS}" ]; then
  echo "ERROR: expected ${JOBS} completed, got ${COMPLETED_CNT} (failed=${FAILED_CNT})" >&2
  exit 1
fi

# ── 4. Assertions. ─────────────────────────────────────────────────────────
fails=0
check() { # $1 label, $2 expected, $3 actual
  if [ "$2" = "$3" ]; then echo "  PASS ${1}: ${3}";
  else echo "  FAIL ${1}: expected [$2], got [$3]" >&2; fails=$((fails+1)); fi
}

# Exactly one attempt per job (attempt_number=1, status=completed).
ATTEMPTS="$(pg_query "SELECT COUNT(*) FROM render_attempts WHERE job_id LIKE '${PREFIX}%'")"
check "total attempts (want ${JOBS})" "${JOBS}" "${ATTEMPTS}"

MULTI="$(pg_query "SELECT COUNT(*) FROM (SELECT job_id FROM render_attempts WHERE job_id LIKE '${PREFIX}%' GROUP BY job_id HAVING COUNT(*) > 1) t")"
check "jobs with >1 attempt (double render)" "0" "${MULTI}"

ALL_ONE="$(pg_query "SELECT COUNT(*) FROM render_attempts WHERE job_id LIKE '${PREFIX}%' AND attempt_number = 1 AND status = 'completed'")"
check "attempts that are #1+completed" "${JOBS}" "${ALL_ONE}"

# Both workers must participate.
DISTRIB="$(pg_query "SELECT worker_id, COUNT(*) FROM render_attempts WHERE job_id LIKE '${PREFIX}%' GROUP BY worker_id ORDER BY worker_id")"
echo "  worker distribution:"
echo "${DISTRIB}" | sed 's/^/    /'
DISTINCT_WORKERS="$(printf '%s\n' "${DISTRIB}" | wc -l)"
check "distinct workers (want 2)" "2" "${DISTINCT_WORKERS}"

for w in renderinggen-local renderinggen-b; do
  N="$(pg_query "SELECT COUNT(*) FROM render_attempts WHERE job_id LIKE '${PREFIX}%' AND worker_id = '${w}'")"
  if [ "${N:-0}" -gt 0 ] 2>/dev/null; then
    echo "  PASS worker ${w} rendered ${N} job(s)"
  else
    echo "  FAIL worker ${w} rendered 0 jobs" >&2; fails=$((fails+1))
  fi
done

# 20 verifiable artifacts (sha256 + size present).
ARTIFACTS="$(pg_query "SELECT COUNT(*) FROM render_artifacts WHERE job_id LIKE '${PREFIX}%'")"
check "artifacts (want ${JOBS})" "${JOBS}" "${ARTIFACTS}"
ARTIFACTS_OK="$(pg_query "SELECT COUNT(*) FROM render_artifacts WHERE job_id LIKE '${PREFIX}%' AND sha256 <> '' AND size_bytes > 0")"
check "artifacts with sha256+size" "${JOBS}" "${ARTIFACTS_OK}"

if [ "${fails}" -gt 0 ]; then
  echo "FAIL: ${fails} assertion(s) failed" >&2; exit 1
fi

echo ""
echo "OK: Test 11 passed"
echo "    ${JOBS} jobs rendered by 2 workers, ${JOBS} attempts (one each)"
echo "    0 jobs rendered twice; both workers participated"
