#!/usr/bin/env bash
# /tmp/benchfair.sh — 2-job concurrent script.generate benchmark driver.
#
# Modes:
#   submit  submit BOTH payloads back-to-back, persist job ids in $STATE
#   poll    poll each job once; when both are terminal, persist full bodies and
#           print the audio_publish verdict
#
# The metric under test: the `audio_publish` stage wall time (the certified
# final-audio Drive upload). Baseline under cross-job contention was 85 741 ms
# (3 concurrent publication phases); the fair gate must keep it well under 10 s.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${REFACTORED_DIR:-"$SCRIPT_DIR/../../refactored"}"

BASE="http://127.0.0.1:8000"
STATE=/tmp/benchfair_state.json
RES_A=/tmp/benchfair_a_result.json
RES_B=/tmp/benchfair_b_result.json
RUNSTAMP="$(date -u +%Y%m%dT%H%M%SZ)"

submit_one() {
  local payload="$1" key="$2" out="$3"
  ./scripts/with-velox-auth bash -c '
    curl -fsS --max-time 30 -X POST "'"$BASE"'/api/script/generate" \
      -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" \
      -H "Content-Type: application/json" \
      -H "Idempotency-Key: '"$key"'" \
      --data-binary @"'"$payload"'"' >"$out" 2>/tmp/benchfair_submit_err.log
}

case "${1:-}" in
  submit)
    submit_one /tmp/benchfair_a_payload.json "benchfair-$RUNSTAMP-a" /tmp/benchfair_submit_a.json &
    local_a=$!
    submit_one /tmp/benchfair_b_payload.json "benchfair-$RUNSTAMP-b" /tmp/benchfair_submit_b.json &
    local_b=$!
    wait "$local_a" || { echo "SUBMIT_A_FAILED"; cat /tmp/benchfair_submit_err.log; exit 1; }
    wait "$local_b" || { echo "SUBMIT_B_FAILED"; cat /tmp/benchfair_submit_err.log; exit 1; }
    JOB_A=$(jq -r '.job_id // .id // empty' /tmp/benchfair_submit_a.json)
    JOB_B=$(jq -r '.job_id // .id // empty' /tmp/benchfair_submit_b.json)
    [[ -n "$JOB_A" && -n "$JOB_B" ]] || { echo "MISSING_JOB_ID"; cat /tmp/benchfair_submit_a.json /tmp/benchfair_submit_b.json; exit 1; }
    jq -n --arg a "$JOB_A" --arg b "$JOB_B" --arg ts "$RUNSTAMP" \
      '{runstamp:$ts, job_a:$a, job_b:$b}' >"$STATE"
    echo "SUBMITTED job_a=$JOB_A job_b=$JOB_B"
    ;;

  poll)
    [[ -f "$STATE" ]] || { echo "NO_STATE — run submit first"; exit 2; }
    JOB_A=$(jq -r '.job_a' "$STATE")
    JOB_B=$(jq -r '.job_b' "$STATE")
    fetch() {
      ./scripts/with-velox-auth bash -c '
        curl -fsS --max-time 30 -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" \
          "'"$BASE"'/api/jobs/'"$1"'/full"' 2>/dev/null || true
    }
    BODY_A=$(fetch "$JOB_A"); BODY_B=$(fetch "$JOB_B")
    ST_A=$(printf '%s' "$BODY_A" | jq -r '.status // .job.status // "UNKNOWN"')
    ST_B=$(printf '%s' "$BODY_B" | jq -r '.status // .job.status // "UNKNOWN"')
    echo "A=$ST_A B=$ST_B"
    terminal() { case "$1" in SUCCEEDED|COMPLETED|FAILED|CANCELLED|DEAD_LETTER) return 0;; *) return 1;; esac; }
    if terminal "$ST_A" && terminal "$ST_B"; then
      printf '%s' "$BODY_A" >"$RES_A"
      printf '%s' "$BODY_B" >"$RES_B"
      echo "BOTH_TERMINAL"
    else
      echo "WAITING"
    fi
    ;;

  report)
    for x in a b; do
      F="/tmp/benchfair_${x}_result.json"
      [[ -f "$F" ]] || { echo "[$x] missing result $F"; continue; }
      local_status=$(jq -r '.status // .job.status' "$F")
      wall=$(jq -r '.timing.wall_ms // .timing.execution_wall_ms' "$F")
      ap=$(jq -r '[.timing.stages[]? | select(.name=="audio_publish") | .duration_ms] | first // 0' "$F")
      apw=$(jq -r '[.timing.operations[]? | select(.stage=="audio_publish") | .work_ms] | add // 0' "$F")
      apc=$(jq -r '[.timing.operations[]? | select(.stage=="audio_publish") | .calls] | add // 0' "$F")
      waits=$(jq -r '[.timing.waits[]? | select(.kind=="semaphore_wait") | .duration_ms] | add // 0' "$F")
      echo "[$x] status=$local_status wall=${wall}ms audio_publish_stage=${ap}ms audio_publish_work=${apw}ms calls=${apc} semaphore_wait=${waits}ms"
    done
    ;;

  *) echo "usage: $0 {submit|poll|report}"; exit 2;;
esac
