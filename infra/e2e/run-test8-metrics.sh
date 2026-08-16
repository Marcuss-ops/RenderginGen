#!/usr/bin/env bash
#
# Test 8 — RenderingGen rendering metrics.
#
# After a render, the per-job phase metrics (materialize, plan, render,
# publish, total) must be persisted and satisfy the invariants:
#   - total_ms >= render_ms        (total always covers the render phase)
#   - total_ms >= sum(phases)      (total covers every phase + overhead)
# The queue wait is derived from the job timestamps (started_at - queued_at).
#
# The job is a self-contained color-only render (1280x720 @ 30fps, 3s).
#
# Usage:
#   1. Start the stack:   (cd infra/docker && docker compose up --build -d)
#   2. Run:               infra/e2e/run-test8-metrics.sh
#
# Requires curl, python3. PostgreSQL assertions are auto-detected (docker
# compose `postgres` service or DATABASE_URL + psql).
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"

JOB_ID="test8-metrics-$(date +%s)"
WORK_DIR="$(mktemp -d /tmp/test8-metrics.XXXXXX)"
trap 'rm -rf "$WORK_DIR"' EXIT

json_field() { printf '%s' "$1" | python3 -c 'import sys,json; d=json.load(sys.stdin)
for p in sys.argv[1].split("."):
    d = d[int(p)] if p.isdigit() else d[p]
print(d)' "$2"; }

PG_CMD=()
pg_init() {
  if command -v psql >/dev/null 2>&1 && [[ -n "${DATABASE_URL:-}" ]]; then
    PG_CMD=(psql "${DATABASE_URL}" -At -F'|' -c)
  elif docker compose -f "${REPO_ROOT}/infra/docker/docker-compose.yaml" \
      exec -T postgres psql -U renderinggen -d renderinggen -At -F'|' -c 'SELECT 1' >/dev/null 2>&1; then
    PG_CMD=(docker compose -f "${REPO_ROOT}/infra/docker/docker-compose.yaml" \
      exec -T postgres psql -U renderinggen -d renderinggen -At -F'|' -c)
  else
    echo "ERROR: PostgreSQL not reachable — Test 8 requires the database" >&2
    return 1
  fi
}

echo "== Test 8: rendering metrics =="

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

if ! pg_init; then exit 1; fi

# metric_name|metric_value for this job
METRICS="$("${PG_CMD[@]}" "SELECT metric_name, metric_value FROM processing_metrics WHERE job_id = '${JOB_ID}'")"
QUEUE_WAIT_MS="$("${PG_CMD[@]}" "SELECT EXTRACT(EPOCH FROM (started_at - queued_at)) * 1000 FROM render_jobs WHERE id = '${JOB_ID}'")"

python3 - "${METRICS}" "${QUEUE_WAIT_MS}" <<'PY'
import sys

rows = sys.argv[1].strip().split("\n")
m = {}
for line in rows:
    if not line:
        continue
    name, value = line.split("|", 1)
    m[name.strip()] = float(value.strip())

required = ["materialize_ms", "plan_ms", "render_ms", "publish_ms", "total_ms"]
fails = 0
for name in required:
    v = m.get(name)
    if v is None:
        print(f"  FAIL metric missing: {name}")
        fails += 1
    elif v < 0:
        print(f"  FAIL metric negative: {name}={v}")
        fails += 1
    else:
        print(f"  metric {name:16s} = {v:.3f} ms")

total = m.get("total_ms", 0.0)
render = m.get("render_ms", 0.0)
phase_sum = sum(m.get(n, 0.0) for n in ["materialize_ms", "plan_ms", "render_ms", "publish_ms"])

if total < render:
    print(f"  FAIL total_ms ({total:.3f}) < render_ms ({render:.3f})")
    fails += 1
else:
    print(f"  PASS total_ms ({total:.3f}) >= render_ms ({render:.3f})")

if total + 1e-6 < phase_sum:
    print(f"  FAIL total_ms ({total:.3f}) < sum(phases) ({phase_sum:.3f})")
    fails += 1
else:
    print(f"  PASS total_ms ({total:.3f}) >= sum(phases) ({phase_sum:.3f})")

queue_wait_ms = float(sys.argv[2])
print(f"  queue_wait_ms = {queue_wait_ms:.1f} (started_at - queued_at)")
if queue_wait_ms < 0:
    print("  FAIL queue_wait_ms negative")
    fails += 1
else:
    print("  PASS queue_wait_ms >= 0")

sys.exit(1 if fails else 0)
PY

echo ""
echo "OK: Test 8 passed"
echo "    metrics recorded in processing_metrics, total_ms >= render_ms"
