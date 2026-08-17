#!/usr/bin/env bash
#
# Test 12 — Atomic PostgreSQL claim (FOR UPDATE SKIP LOCKED).
#
# Submits 100 jobs, then runs 10 concurrent "workers" that all hammer
# POST /jobs/claim. The claim must be atomic: 100 claims, 100 unique jobs,
# 0 duplicates, and in the database no job may ever have more than one
# attempt with status 'running' at the same time:
#
#     SELECT job_id, COUNT(*)
#     FROM render_attempts
#     WHERE status = 'running'
#     GROUP BY job_id
#     HAVING COUNT(*) > 1;      -- must return 0 rows
#
# Why the real worker is stopped: it would compete for the same jobs, render
# them (~2.4s each, software) and complete them, so the "100 jobs claimed by
# the 10 test workers" accounting would be non-deterministic. Stopping it makes
# the claim-atomicity measurement exact. The test workers only claim (they do
# not render); after the atomicity assertions the test jobs are deleted from
# the database (cascade) so nothing is left lingering in 'running' and the
# worker never re-renders them. A claim-only job cannot be "completed" via the
# API: the queue's completion gate rejects an empty artifact.
#
# DB assertions (PostgreSQL):
#   - 100 running attempts, one per job
#   - HAVING COUNT(*) > 1 over status='running' → 0 rows
#   - 100 distinct jobs claimed by the 10 workers, 0 duplicate claims
#
# Usage:
#   1. Start the stack:   (cd infra/docker && docker compose up --build -d)
#   2. Run:               infra/e2e/run-test12-claim-atomic.sh
#
# Requires curl, python3, docker. PostgreSQL assertions are auto-detected
# (docker compose `postgres` service).
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
COMPOSE_FILE="${REPO_ROOT}/infra/docker/docker-compose.yaml"

JOBS="${JOBS:-100}"
WORKERS="${WORKERS:-10}"
RUN_ID="$(date +%s)"
PREFIX="test12-${RUN_ID}"

WORK_DIR="$(mktemp -d /tmp/test12-claim.XXXXXX)"
mkdir -p "${WORK_DIR}/claims"

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
    echo "ERROR: PostgreSQL not reachable — Test 12 requires the database" >&2
    return 1
  fi
}
pg_query() { [[ ${#PG_CMD[@]} -gt 0 ]] || return 2; "${PG_CMD[@]}" "$1"; }

# ── Worker control ─────────────────────────────────────────────────────────
worker_stop()  { docker compose -f "${COMPOSE_FILE}" stop worker >/dev/null 2>&1; }
worker_start() { docker compose -f "${COMPOSE_FILE}" start worker >/dev/null 2>&1; }
cleanup() { worker_start || true; rm -rf "${WORK_DIR}"; }
trap cleanup EXIT   # always leave the worker running and drop the temp dir

# One concurrent claimer: pull jobs until the queue reports empty, recording
# each claimed job id into its own file. A 204 means "no *available* pending
# job"; any job that is momentarily locked by another in-flight claim is still
# claimed by that other worker, so no job is ever lost (SKIP LOCKED).
run_claimer() {
  local wid="$1" out="$2"
  local retries=0
  while :; do
    local resp code body jid
    resp="$(curl -s -w $'\n%{http_code}' -X POST "${QUEUE_URL}/jobs/claim" \
      -H 'Content-Type: application/json' -d "{\"worker\":\"${wid}\"}")"
    code="${resp##*$'\n'}"
    body="${resp%$'\n'*}"
    case "${code}" in
      204) retries=0; break ;;
      200) jid="$(json_field "${body}" id)"; printf '%s\n' "${jid}" >> "${out}" ;;
      *)
        # Transient error (e.g. connection reset under load): retry a few times.
        retries=$((retries + 1))
        if [ "${retries}" -ge 20 ]; then
          echo "ERROR: worker ${wid} claim failed HTTP ${code} after retries" >&2
          return 1
        fi
        sleep 0.05 ;;
    esac
  done
}

echo "== Test 12: atomic claim (${WORKERS} workers, ${JOBS} jobs) =="

# ── 1. Stop the real worker so only our 10 claimers pull the jobs. ────────
echo "stopping the real worker (claim is driven only by the ${WORKERS} test workers) ..."
worker_stop
sleep 1

# ── 2. Submit 100 jobs (minimal color-only plan; claim never renders). ─────
echo "submitting ${JOBS} jobs (${PREFIX}000..$(printf '%03d' $((JOBS-1)))) ..."
for i in $(seq 0 $((JOBS - 1))); do
  JID="$(printf '%s%03d' "${PREFIX}" "${i}")"
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

# ── 3. Launch 10 concurrent claimers. ─────────────────────────────────────
echo "launching ${WORKERS} concurrent claimers ..."
PIDS=()
for w in $(seq 0 $((WORKERS - 1))); do
  run_claimer "test12-w${w}" "${WORK_DIR}/claims/w${w}.txt" &
  PIDS+=("$!")
done
for pid in "${PIDS[@]}"; do
  wait "${pid}" || { echo "ERROR: a claimer failed" >&2; exit 1; }
done

# ── 4. Aggregate claims: 100 unique, 0 duplicates. ────────────────────────
python3 - "${WORK_DIR}/claims" "${JOBS}" <<'PY'
import sys, os, collections
dirpath, expected = sys.argv[1], int(sys.argv[2])
ids = []
for fn in sorted(os.listdir(dirpath)):
    with open(os.path.join(dirpath, fn)) as f:
        for line in f:
            line = line.strip()
            if line:
                ids.append(line)
counts = collections.Counter(ids)
dups = {k: v for k, v in counts.items() if v > 1}
print(f"  total claim events = {len(ids)}")
print(f"  distinct job ids   = {len(counts)}")
print(f"  duplicate claims   = {len(dups)}")
fails = 0
if len(ids) != expected:
    print(f"  FAIL: expected {expected} claim events, got {len(ids)}")
    fails += 1
if len(counts) != expected:
    print(f"  FAIL: expected {expected} unique jobs, got {len(counts)}")
    fails += 1
if dups:
    print(f"  FAIL: jobs claimed more than once: {list(dups)[:10]} ...")
    fails += 1
else:
    print("  PASS: 100 claims, 100 unique jobs, 0 duplicates")
sys.exit(1 if fails else 0)
PY

# ── 5. DB: no job may have >1 running attempt (the SKIP LOCKED guarantee). ─
if ! pg_init; then exit 1; fi

RUNNING="$(pg_query "SELECT COUNT(*) FROM render_attempts WHERE status='running' AND job_id LIKE '${PREFIX}%'")"
echo "  running attempts (this run) = ${RUNNING}"
if [ "${RUNNING}" != "${JOBS}" ]; then
  echo "FAIL: expected ${JOBS} running attempts, got ${RUNNING}" >&2; exit 1
fi

DUP_ROWS="$(pg_query "SELECT job_id, COUNT(*) FROM render_attempts WHERE status='running' GROUP BY job_id HAVING COUNT(*) > 1")"
if [ -n "${DUP_ROWS}" ]; then
  echo "FAIL: jobs with >1 running attempt:" >&2
  echo "${DUP_ROWS}" >&2
  exit 1
fi
echo "  PASS: 0 rows with COUNT(*) > 1 over status='running' (0 duplicates)"

DISTINCT_RUNNING="$(pg_query "SELECT COUNT(DISTINCT job_id) FROM render_attempts WHERE status='running' AND job_id LIKE '${PREFIX}%'")"
if [ "${DISTINCT_RUNNING}" != "${JOBS}" ]; then
  echo "FAIL: expected ${JOBS} distinct running jobs, got ${DISTINCT_RUNNING}" >&2; exit 1
fi
echo "  PASS: ${DISTINCT_RUNNING} distinct jobs, one running attempt each"

# ── 6. Cleanup: delete the test jobs (cascade) so nothing is left in
#       'running' and the restarted worker never re-renders them. ────────────
echo "deleting the ${JOBS} test jobs from the database (cascade) ..."
DELETED="$(pg_query "WITH d AS (DELETE FROM render_jobs WHERE id LIKE '${PREFIX}%' RETURNING id) SELECT COUNT(*) FROM d")"
if [ "${DELETED}" != "${JOBS}" ]; then
  echo "FAIL: expected to delete ${JOBS} test jobs, deleted ${DELETED}" >&2; exit 1
fi
echo "  ${DELETED} test jobs deleted"

LEFT="$(pg_query "SELECT COUNT(*) FROM render_jobs WHERE id LIKE '${PREFIX}%'")"
if [ "${LEFT}" != "0" ]; then
  echo "FAIL: ${LEFT} test jobs remain after cleanup" >&2; exit 1
fi
echo "  queue clean: 0 test12 jobs remain"

echo ""
echo "OK: Test 12 passed"
echo "    ${JOBS} jobs claimed → ${JOBS} unique, 0 duplicates"
echo "    DB: 0 rows with COUNT(*) > 1 over render_attempts.status='running'"
echo "    FOR UPDATE SKIP LOCKED holds across 10 concurrent workers"
