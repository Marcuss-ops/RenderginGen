#!/usr/bin/env bash
#
# Test 15 — idempotency_key: a retry of the same logical request returns the
# same job, never a second one.
#
# The same idempotency_key is submitted twice with two DIFFERENT job ids
# (simulating a producer that retries with a fresh client-side id). The first
# request must create the job (201) and the second must return the SAME
# canonical job (200) — the retry's id is ignored, and no second job exists.
#
#   request 1: id=A, key=K  -> 201 {"id":"A"}
#   request 2: id=B, key=K  -> 200 {"id":"A"}   (NOT "B", no job B created)
#
# DB assertions (PostgreSQL):
#   - exactly 1 row with the idempotency_key
#   - the canonical job id is A; job B does not exist
#   - idempotency_key is stored on job A
#
# Usage:
#   1. Start the stack:   (cd infra/docker && docker compose up --build -d)
#   2. Run:               infra/e2e/run-test15-idempotency.sh
#
# Requires curl, python3, docker. PostgreSQL assertions are auto-detected
# (docker compose `postgres` service).
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
COMPOSE_FILE="${REPO_ROOT}/infra/docker/docker-compose.yaml"

RUN_ID="$(date +%s)"
KEY="test15-key-${RUN_ID}"
JOB_A="test15-${RUN_ID}-a"
JOB_B="test15-${RUN_ID}-b"

WORK_DIR="$(mktemp -d /tmp/test15-idempotency.XXXXXX)"

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
    echo "ERROR: PostgreSQL not reachable — Test 15 requires the database" >&2
    return 1
  fi
}
pg_query() { [[ ${#PG_CMD[@]} -gt 0 ]] || return 2; "${PG_CMD[@]}" "$1"; }

cleanup() {
  if [[ ${#PG_CMD[@]} -gt 0 ]]; then
    pg_query "DELETE FROM render_jobs WHERE id LIKE 'test15-%'" >/dev/null 2>&1 || true
  fi
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

echo "== Test 15: idempotency_key dedup =="

write_payload() { # $1 job id
  cat > "${WORK_DIR}/payload.json" <<EOF
{
  "id": "$1",
  "schema": "renderinggen.job",
  "version": 1,
  "idempotency_key": "${KEY}",
  "render_plan": {
    "schema": "chronon.render-plan",
    "version": 1,
    "job_id": "$1",
    "canvas": { "width": 1280, "height": 720, "fps": 30, "duration_frames": 90 },
    "layers": [ { "id": "bg", "type": "color", "color": [0.08, 0.12, 0.25, 1.0] } ],
    "output": { "path": "result.mp4", "format": "mp4", "codec": "h264" }
  },
  "assets": []
}
EOF
}

# ── 1. First request: creates the job. ─────────────────────────────────────
write_payload "${JOB_A}"
CODE1="$(curl -s -o "${WORK_DIR}/r1.body" -w '%{http_code}' -X POST "${QUEUE_URL}/jobs" \
  -H 'Content-Type: application/json' --data-binary @"${WORK_DIR}/payload.json")"
ID1="$(json_field "$(cat "${WORK_DIR}/r1.body")" id)"
echo "request 1 (id=${JOB_A}): HTTP ${CODE1} → id=${ID1}"
if [ "${CODE1}" != "201" ]; then
  echo "ERROR: first request should return 201, got ${CODE1}" >&2; cat "${WORK_DIR}/r1.body" >&2; exit 1
fi
if [ "${ID1}" != "${JOB_A}" ]; then
  echo "ERROR: first request should return id ${JOB_A}, got ${ID1}" >&2; exit 1
fi

# ── 2. Retry with a DIFFERENT id, SAME key: must return the same job. ──────
write_payload "${JOB_B}"
CODE2="$(curl -s -o "${WORK_DIR}/r2.body" -w '%{http_code}' -X POST "${QUEUE_URL}/jobs" \
  -H 'Content-Type: application/json' --data-binary @"${WORK_DIR}/payload.json")"
ID2="$(json_field "$(cat "${WORK_DIR}/r2.body")" id)"
echo "request 2 (id=${JOB_B}, same key): HTTP ${CODE2} → id=${ID2}"
if [ "${CODE2}" != "200" ]; then
  echo "ERROR: idempotent retry should return 200, got ${CODE2}" >&2; cat "${WORK_DIR}/r2.body" >&2; exit 1
fi
if [ "${ID2}" != "${JOB_A}" ]; then
  echo "ERROR: retry should return the same job ${JOB_A}, got ${ID2}" >&2; exit 1
fi

# ── 3. DB assertions. ─────────────────────────────────────────────────────
if ! pg_init; then exit 1; fi
fails=0
check() { if [ "$2" = "$3" ]; then echo "  PASS ${1}: ${3}";
  else echo "  FAIL ${1}: expected [$2], got [$3]" >&2; fails=$((fails+1)); fi; }

check "jobs with key ${KEY} (want 1)" "1" "$(pg_query "SELECT COUNT(*) FROM render_jobs WHERE idempotency_key='${KEY}'")"
check "job A exists" "1" "$(pg_query "SELECT COUNT(*) FROM render_jobs WHERE id='${JOB_A}'")"
check "job B does NOT exist" "0" "$(pg_query "SELECT COUNT(*) FROM render_jobs WHERE id='${JOB_B}'")"
check "key stored on job A" "${KEY}" "$(pg_query "SELECT COALESCE(idempotency_key,'') FROM render_jobs WHERE id='${JOB_A}'")"

if [ "${fails}" -gt 0 ]; then
  echo "FAIL: ${fails} assertion(s) failed" >&2; exit 1
fi

echo ""
echo "OK: Test 15 passed"
echo "    request 1 (id=A) → 201 created ${JOB_A}"
echo "    request 2 (id=B, same key) → 200 returns ${JOB_A} (retry id ignored, no job B)"
echo "    1 job per idempotency_key, key stored on the canonical job"
