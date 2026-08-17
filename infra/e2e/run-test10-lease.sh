#!/usr/bin/env bash
#
# Test 10 — Worker lease: kill Worker A → lease expired → pending → Worker B.
#
# Submits one job with a render long enough to catch mid-flight, lets Worker A
# (renderinggen-local) claim it, then SIGKILLs Worker A mid-render (no graceful
# shutdown, no complete/fail/renew). The queue must then:
#
#     running ──(lease expires)──▶ pending ──▶ Worker B claims ──▶ completed
#
# and preserve the history: attempt 1 = lease_expired, attempt 2 = completed.
#
# DB assertions (PostgreSQL):
#   - render_attempts #1 = lease_expired (renderinggen-local, 'lease expired')
#   - render_attempts #2 = completed   (renderinggen-b)
#   - render_jobs: attempt_count = 2, state = completed
#   - render_events: JOB_CREATED → JOB_CLAIMED(#1,A) → JOB_REQUEUED(#1, lease
#     expired) → JOB_CLAIMED(#2,B) → JOB_COMPLETED(#2,B)
#
# Usage:
#   1. Start the stack:   (cd infra/docker && docker compose up --build -d)
#   2. Run:               infra/e2e/run-test10-lease.sh
#
# Requires curl, python3, docker. PostgreSQL assertions are auto-detected
# (docker compose `postgres` service).
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
COMPOSE_FILE="${REPO_ROOT}/infra/docker/docker-compose.yaml"

JOB_ID="test10-lease-$(date +%s)"
WORKER_A="renderinggen-local"
WORKER_B="renderinggen-b"
CONTAINER_A="docker-worker-1"

WORK_DIR="$(mktemp -d /tmp/test10-lease.XXXXXX)"

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
    echo "ERROR: PostgreSQL not reachable — Test 10 requires the database" >&2
    return 1
  fi
}
pg_query() { [[ ${#PG_CMD[@]} -gt 0 ]] || return 2; "${PG_CMD[@]}" "$1"; }

# ── Cleanup: always restore Worker A and leave both workers running. ───────
cleanup() {
  docker update --restart=unless-stopped "${CONTAINER_A}" >/dev/null 2>&1 || true
  docker compose -f "${COMPOSE_FILE}" start worker worker-b >/dev/null 2>&1 || true
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

echo "== Test 10: worker lease (kill Worker A → lease expiry → Worker B) =="

# ── 1. Prepare: save Worker B (stop it) and disable auto-restart on A so the
#       SIGKILL does not resurrect it and let it steal the requeued job. ─────
echo "preparing: stopping Worker B, disabling auto-restart on Worker A ..."
docker compose -f "${COMPOSE_FILE}" stop worker-b >/dev/null 2>&1
docker update --restart=no "${CONTAINER_A}" >/dev/null 2>&1
sleep 1

# ── 2. Submit one job with a long render (300 frames ≈ 10s) so the kill lands
#       while Worker A is still rendering, never completing. ─────────────────
cat > "${WORK_DIR}/payload.json" <<EOF
{
  "id": "${JOB_ID}",
  "schema": "renderinggen.job",
  "version": 1,
  "render_plan": {
    "schema": "chronon.render-plan",
    "version": 1,
    "job_id": "${JOB_ID}",
    "canvas": { "width": 1280, "height": 720, "fps": 30, "duration_frames": 300 },
    "layers": [ { "id": "bg", "type": "color", "color": [0.10, 0.14, 0.28, 1.0] } ],
    "output": { "path": "result.mp4", "format": "mp4", "codec": "h264" }
  },
  "assets": []
}
EOF
CODE="$(curl -s -o /dev/null -w '%{http_code}' -X POST "${QUEUE_URL}/jobs" \
  -H 'Content-Type: application/json' --data-binary @"${WORK_DIR}/payload.json")"
if [ "${CODE}" != "201" ] && [ "${CODE}" != "200" ]; then
  echo "ERROR: submit returned HTTP ${CODE}" >&2; exit 1
fi
echo "submitted ${JOB_ID}"

# ── 3. Wait for Worker A to claim (state=running, worker=A). ───────────────
echo "waiting for Worker A to claim ..."
CLAIMED=0
for _ in $(seq 1 200); do
  BODY="$(curl -fsS "${QUEUE_URL}/jobs/${JOB_ID}")"
  STATE="$(json_field "${BODY}" state)"
  if [ "${STATE}" = "running" ]; then
    # worker is only present once the job is claimed (omitempty while pending)
    WORKER="$(json_field "${BODY}" worker)"
    if [ "${WORKER}" = "${WORKER_A}" ]; then CLAIMED=1; break; fi
  fi
  sleep 0.2
done
if [ "${CLAIMED}" != "1" ]; then
  echo "ERROR: Worker A never claimed the job" >&2; exit 1
fi
echo "Worker A claimed (attempt 1 = running, lease 30s)"

# ── 4. Brutal kill: SIGKILL, no graceful shutdown. ─────────────────────────
docker kill "${CONTAINER_A}" >/dev/null 2>&1
sleep 1
RUNNING="$(docker inspect -f '{{.State.Running}}' "${CONTAINER_A}" 2>/dev/null || echo false)"
if [ "${RUNNING}" != "false" ]; then
  echo "ERROR: Worker A is still running after SIGKILL" >&2; exit 1
fi
echo "Worker A killed (SIGKILL) — it will never report complete/fail"

if ! pg_init; then exit 1; fi

# ── 5. The job must stay 'running' until the lease expires. ────────────────
ATT1_STATUS="$(pg_query "SELECT status FROM render_attempts WHERE job_id='${JOB_ID}' AND attempt_number=1")"
if [ "${ATT1_STATUS}" != "running" ]; then
  echo "ERROR: expected attempt 1 running before expiry, got '${ATT1_STATUS}'" >&2; exit 1
fi

echo "waiting for the lease to expire (30s lease + 5s scan) ..."
STATE=""
for _ in $(seq 1 300); do
  STATE="$(pg_query "SELECT state FROM render_jobs WHERE id='${JOB_ID}'")"
  if [ "${STATE}" = "pending" ]; then break; fi
  sleep 0.5
done
if [ "${STATE}" != "pending" ]; then
  echo "ERROR: job did not requeue to pending after lease expiry (state=${STATE})" >&2; exit 1
fi

# ── 6. Verify attempt 1 was closed as lease_expired (never lost). ──────────
ATT1="$(pg_query "SELECT status, COALESCE(error_message,'') FROM render_attempts WHERE job_id='${JOB_ID}' AND attempt_number=1")"
echo "attempt 1 after expiry: [${ATT1}]"
if [ "${ATT1}" != "lease_expired|lease expired" ]; then
  echo "ERROR: attempt 1 = [${ATT1}], expected [lease_expired|lease expired]" >&2; exit 1
fi
echo "  PASS: attempt 1 = lease_expired (reason 'lease expired'), job → pending"

# ── 7. Start Worker B → claims attempt 2 and renders to completion. ────────
echo "starting Worker B ..."
docker compose -f "${COMPOSE_FILE}" start worker-b >/dev/null 2>&1
for _ in $(seq 1 600); do
  STATE="$(pg_query "SELECT state FROM render_jobs WHERE id='${JOB_ID}'")"
  if [ "${STATE}" = "completed" ] || [ "${STATE}" = "failed" ]; then break; fi
  sleep 0.5
done
if [ "${STATE}" != "completed" ]; then
  echo "ERROR: job ended in state '${STATE}' (expected completed)" >&2; exit 1
fi
echo "Worker B claimed and completed attempt 2"

# ── 8. Attempt history preserved: #1 lease_expired, #2 completed. ──────────
fails=0
check() { if [ "$2" = "$3" ]; then echo "  PASS ${1}: ${3}";
  else echo "  FAIL ${1}: expected [$2], got [$3]" >&2; fails=$((fails+1)); fi; }

ATTEMPTS="$(pg_query "SELECT attempt_number, status, worker_id FROM render_attempts WHERE job_id='${JOB_ID}' ORDER BY attempt_number")"
echo "render_attempts:"
echo "${ATTEMPTS}" | sed 's/^/  /'
if [ "$(printf '%s\n' "${ATTEMPTS}" | wc -l)" -ne 2 ]; then
  echo "FAIL: expected exactly 2 attempts" >&2; exit 1
fi
IFS=$'\n' read -rd '' -a ATT_LINES <<< "${ATTEMPTS}" || true
check "attempt 1 (num|status|worker)" "1|lease_expired|${WORKER_A}" "${ATT_LINES[0]}"
check "attempt 2 (num|status|worker)" "2|completed|${WORKER_B}" "${ATT_LINES[1]}"
check "render_jobs.attempt_count" "2" "$(pg_query "SELECT attempt_count FROM render_jobs WHERE id='${JOB_ID}'")"
check "render_jobs.state" "completed" "$(pg_query "SELECT state FROM render_jobs WHERE id='${JOB_ID}'")"

EVENTS="$(pg_query "SELECT event_type, COALESCE(worker_id,''), COALESCE(attempt_id,''), COALESCE(payload->>'reason','') FROM render_events WHERE job_id='${JOB_ID}' ORDER BY id")"
echo "render_events:"
echo "${EVENTS}" | sed 's/^/  /'
for ev in "JOB_CREATED|||" "JOB_CLAIMED|${WORKER_A}|${JOB_ID}#1|" "JOB_REQUEUED||${JOB_ID}#1|lease expired" "JOB_CLAIMED|${WORKER_B}|${JOB_ID}#2|" "JOB_COMPLETED|${WORKER_B}|${JOB_ID}#2|"; do
  if ! printf '%s\n' "${EVENTS}" | grep -qF "${ev}"; then
    echo "FAIL: missing event [${ev}]" >&2; fails=$((fails+1))
  fi
done

if [ "${fails}" -gt 0 ]; then
  echo "FAIL: ${fails} assertion(s) failed" >&2; exit 1
fi

echo ""
echo "OK: Test 10 passed"
echo "    Worker A (${WORKER_A}) killed mid-render → lease expired → requeued"
echo "    attempt 1 = lease_expired (preserved), attempt 2 = completed by Worker B (${WORKER_B})"
echo "    attempt_count = 2; events: CLAIMED#1 → REQUEUED(lease expired) → CLAIMED#2 → COMPLETED#2"
