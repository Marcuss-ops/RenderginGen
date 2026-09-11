#!/usr/bin/env bash
#
# End-to-end smoke test over the real CLI path:
#
#   submit job -> worker claims -> plan.json -> real chronon3d_cli render
#   -> result.mp4 -> artifact store -> queue completed -> download artifact
#
# The job uses a self-contained, asset-free SEMANTIC overlay plan (a background
# color), so no sample media is needed. The worker compiles it to
# chronon.render-plan.v2 — this smoke test used to submit a concrete Chronon
# plan directly, which is not a supported input on any worker path. It proves
# the whole queue -> worker -> compiler -> Chronon3d (software backend) ->
# object store loop.
#
# Usage:
#   1. Start infrastructure: (cd infra/docker && docker compose up -d postgres objectstore)
#      Enable the native Queue, RenderingGen and Chronon systemd services.
#   2. Run:               infra/e2e/run-e2e.sh
#
# Requires curl. python3 is used for JSON parsing (jq is not required).
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
STORE_URL="${STORE_URL:-http://localhost:9000}"
OUT_FILE="${OUT_FILE:-/tmp/renderinggen-e2e-result.mp4}"

JOB_ID="color-smoke-$(date +%s)"

echo "Submitting job ${JOB_ID} ..."
curl -fsS -X POST "${QUEUE_URL}/jobs" \
  -H 'Content-Type: application/json' \
  -d "{
    \"id\": \"${JOB_ID}\",
    \"schema\": \"renderinggen.job\",
    \"version\": 1,
    \"render_plan\": {
      \"schema_version\": \"renderinggen.overlay-plan.v1\",
      \"plan_id\": \"${JOB_ID}\",
      \"video_id\": \"${JOB_ID}\",
      \"width\": 320,
      \"height\": 180,
      \"fps_num\": 1,
      \"fps_den\": 1,
      \"duration_ms\": 1000,
      \"background\": { \"kind\": \"color\", \"color\": [0.08, 0.12, 0.25, 1.0] }
    },
    \"assets\": []
  }"

json_field() { # $1 = body, $2 = dotted path (array index supported)
	printf '%s' "$1" | python3 -c '
import sys, json
d = json.load(sys.stdin)
for part in sys.argv[1].split("."):
    d = d[int(part)] if part.isdigit() else d[part]
print(d)
' "$2"
}

echo "Waiting for completion ..."
for _ in $(seq 1 90); do
  BODY="$(curl -fsS "${QUEUE_URL}/jobs/${JOB_ID}")"
  STATE="$(json_field "${BODY}" state)"
  case "${STATE}" in
    completed)
      HASH="$(json_field "${BODY}" artifact.artifact_hash)"
      echo "completed: artifact_hash=${HASH}"
      echo "Downloading artifact from ${STORE_URL} ..."
      curl -fsS "${STORE_URL}/objects/${HASH}" -o "${OUT_FILE}"
      SIZE="$(stat -c%s "${OUT_FILE}")"
      if [ "${SIZE}" -eq 0 ]; then
        echo "ERROR: artifact ${OUT_FILE} is empty" >&2
        exit 1
      fi
      echo "OK: artifact saved to ${OUT_FILE} (${SIZE} bytes)"
      exit 0
      ;;
    failed)
      echo "ERROR: job failed" >&2
      printf '%s\n' "${BODY}" >&2
      exit 1
      ;;
    *)
      echo "  ... ${STATE}"
      sleep 2
      ;;
  esac
done

echo "ERROR: timed out waiting for job ${JOB_ID}" >&2
exit 1
