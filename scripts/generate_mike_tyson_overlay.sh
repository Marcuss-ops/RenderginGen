#!/usr/bin/env bash
set -euo pipefail

# ROOT_DIR is the RenderingGen checkout that owns the canonical fixtures.
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE_URL="${VELOX_API_BASE_URL:-http://127.0.0.1:8000}"
REQUEST_FILE="${1:-$ROOT_DIR/mike_tyson_overlay_test/mike_tyson_generate_request.json}"
RESULT_FILE="${RESULT_FILE:-$ROOT_DIR/mike_tyson_overlay_test/mike_tyson_generate_result.json}"
TOKEN="${VELOX_ADMIN_TOKEN:-}"
POLL_ATTEMPTS="${POLL_ATTEMPTS:-240}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-3}"
HTTP_TIMEOUT_SECONDS="${HTTP_TIMEOUT_SECONDS:-30}"

if [[ -z "$TOKEN" ]]; then
  echo "VELOX_ADMIN_TOKEN is required" >&2
  exit 2
fi
[[ -f "$REQUEST_FILE" ]] || { echo "request file missing: $REQUEST_FILE" >&2; exit 2; }

IDEMPOTENCY_KEY="${IDEMPOTENCY_KEY:-mike-tyson-overlay-$(date -u +%Y%m%dT%H%M%SZ)}"
response="$(curl -sS --fail-with-body --max-time "$HTTP_TIMEOUT_SECONDS" \
  -X POST "$BASE_URL/api/script/generate" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
  --data-binary "@$REQUEST_FILE")"

job_id="$(jq -r '.job_id // empty' <<<"$response")"
[[ -n "$job_id" ]] || { echo "$response" | jq . >&2; exit 1; }
echo "submitted job=$job_id"

for _ in $(seq 1 "$POLL_ATTEMPTS"); do
  result="$(curl -sS --fail-with-body --max-time "$HTTP_TIMEOUT_SECONDS" \
    -H "Authorization: Bearer $TOKEN" \
    "$BASE_URL/api/jobs/$job_id/full")"
  status="$(jq -r '.status // .job.status // "UNKNOWN"' <<<"$result")"
  stage="$(jq -r '.current_stage // ""' <<<"$result")"
  echo "status=$status stage=$stage"
  case "$status" in
    SUCCEEDED|COMPLETED|SUCCESS|DONE)
      printf '%s\n' "$result" > "$RESULT_FILE"
      jq '{job_id:.id,status,current_stage,documents:.job.result.result.documents,render:.job.result.result.render,drive_links:[.job.result.result.scenes[]?.annotations?.primary_entities[]?.image?.drive_link],overlay_links:[.job.result.result.editing_timeline.overlays[]?.drive_link]}' "$RESULT_FILE"
      exit 0
      ;;
    FAILED|ERROR|CANCELLED|REJECTED)
      printf '%s\n' "$result" > "$RESULT_FILE"
      jq '{job_id:.id,status,current_stage,error}' "$RESULT_FILE" >&2
      exit 1
      ;;
  esac
  sleep "$POLL_INTERVAL_SECONDS"
done

echo "job polling timed out: $job_id" >&2
exit 1
