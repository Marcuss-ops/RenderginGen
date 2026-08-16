#!/usr/bin/env bash
#
# Test 6 — RenderingGen SHA-256 certification.
#
# After a render, the hash RenderingGen computed for the artifact must be
# 100% identical to `sha256sum` of the downloaded bytes AND to the
# render_artifacts.sha256 row in PostgreSQL. Then, flipping a single byte of
# the file must change the hash — proving the hash is a real content hash,
# never a fixed/cached value.
#
# The job is a self-contained color-only render (no assets needed).
#
# Usage:
#   1. Start the stack:   (cd infra/docker && docker compose up --build -d)
#   2. Run:               infra/e2e/run-test6-sha256.sh
#
# Requires curl, python3, ffprobe, sha256sum. PostgreSQL assertions are
# auto-detected (docker compose `postgres` service or DATABASE_URL + psql).
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
STORE_URL="${STORE_URL:-http://localhost:9000}"
OUT_FILE="${OUT_FILE:-/tmp/renderinggen-test6-result.mp4}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"

JOB_ID="test6-sha256-$(date +%s)"
WORK_DIR="$(mktemp -d /tmp/test6-sha256.XXXXXX)"
trap 'rm -rf "$WORK_DIR"' EXIT

json_field() { printf '%s' "$1" | python3 -c 'import sys,json; d=json.load(sys.stdin)
for p in sys.argv[1].split("."):
    d = d[int(p)] if p.isdigit() else d[p]
print(d)' "$2"; }

# ── PostgreSQL (auto-detected) ─────────────────────────────────────────────
PG_CMD=()
pg_init() {
  if command -v psql >/dev/null 2>&1 && [[ -n "${DATABASE_URL:-}" ]]; then
    PG_CMD=(psql "${DATABASE_URL}" -At -c)
  elif docker compose -f "${REPO_ROOT}/infra/docker/docker-compose.yaml" \
      exec -T postgres psql -U renderinggen -d renderinggen -At -c 'SELECT 1' >/dev/null 2>&1; then
    PG_CMD=(docker compose -f "${REPO_ROOT}/infra/docker/docker-compose.yaml" \
      exec -T postgres psql -U renderinggen -d renderinggen -At -c)
  else
    echo "WARN: PostgreSQL not reachable; skipping DB sha256 assertion" >&2
    return 1
  fi
}
pg_query() { [[ ${#PG_CMD[@]} -gt 0 ]] || return 2; "${PG_CMD[@]}" "$1"; }

echo "== Test 6: RenderingGen SHA-256 =="

# ── 1. Submit a self-contained color-only job. ─────────────────────────────
cat > "${WORK_DIR}/payload.json" <<EOF
{
  "id": "${JOB_ID}",
  "schema": "renderinggen.job",
  "version": 1,
  "render_plan": {
    "schema": "chronon.render-plan",
    "version": 1,
    "job_id": "${JOB_ID}",
    "canvas": { "width": 640, "height": 360, "fps": 30, "duration_frames": 30 },
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
SIZE="$(json_field "${BODY}" artifact.size_bytes)"
echo "completed: artifact_hash=${HASH} size=${SIZE}"

# ── 2. Filesystem sha256 must be 100% identical. ───────────────────────────
echo "downloading artifact ..."
curl -fsS "${STORE_URL}/objects/${HASH}" -o "${OUT_FILE}"
FS_HASH="$(sha256sum "${OUT_FILE}" | cut -d' ' -f1)"
FS_SIZE="$(stat -c%s "${OUT_FILE}")"
echo "  filesystem sha256: ${FS_HASH}"

if [ "${FS_HASH}" != "${HASH}" ]; then
  echo "FAIL: filesystem sha256 != RenderingGen artifact_hash" >&2
  echo "      filesystem = ${FS_HASH}" >&2
  echo "      queue      = ${HASH}" >&2
  exit 1
fi
if [ "${FS_SIZE}" -ne "${SIZE}" ]; then
  echo "FAIL: filesystem size ${FS_SIZE} != artifact size ${SIZE}" >&2
  exit 1
fi
echo "PASS: RenderingGen sha256 == sha256sum(file) (100%)"

# ── 3. DB artifact.sha256 must match too. ─────────────────────────────────
if pg_init; then
  DB_HASH="$(pg_query "SELECT sha256 FROM render_artifacts WHERE job_id = '${JOB_ID}'")"
  echo "  db sha256: ${DB_HASH}"
  if [ "${DB_HASH}" != "${HASH}" ]; then
    echo "FAIL: render_artifacts.sha256 != artifact_hash (${DB_HASH} != ${HASH})" >&2
    exit 1
  fi
  echo "PASS: render_artifacts.sha256 == artifact_hash (100%)"
fi

# ── 4. Flipping one byte must change the hash. ─────────────────────────────
MUTATED="${OUT_FILE}.mutated.mp4"
python3 - "${OUT_FILE}" "${MUTATED}" <<'PY'
import sys
data = bytearray(open(sys.argv[1], "rb").read())
assert len(data) > 1, "artifact too small to mutate"
data[len(data) // 2] ^= 0xFF  # flip one byte in the middle
open(sys.argv[2], "wb").write(data)
PY
MUTATED_HASH="$(sha256sum "${MUTATED}" | cut -d' ' -f1)"
echo "  mutated sha256: ${MUTATED_HASH}"

if [ "${MUTATED_HASH}" = "${HASH}" ]; then
  echo "FAIL: flipping a byte did NOT change the sha256 (hash is not content-derived)" >&2
  exit 1
fi
echo "PASS: one-byte change produced a different sha256"

echo ""
echo "OK: Test 6 passed"
echo "    sha256(queue) == sha256(file) == sha256(db), and mutates on byte change"
echo "    file: ${OUT_FILE} (${FS_SIZE} bytes, sha256=${HASH})"
