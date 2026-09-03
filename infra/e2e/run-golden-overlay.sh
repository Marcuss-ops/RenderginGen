#!/usr/bin/env bash
#
# GoldenOverlayJobV1 — end-to-end golden canary over the real chain:
#
#   submit queue -> claim RenderingGen -> materialize background.jpg
#   -> materialize apple.png -> plan.json -> chronon3d_cli render
#   -> result.mp4 -> artifact store -> completed -> download + verify
#   -> PostgreSQL certification -> idempotent replay (no new render)
#
# The job is the real RenderingGen workload (not a color smoke):
#
#   background.jpg        full 5s      (f0-149)
#   "QUESTO CAMBIA TUTTO" title_centered (f20-60)
#   "APPLE"               kinetic_word   (f65-95)
#   apple.png             contain, right (f90-135)
#
# The payload is the canonical, immutable
# testdata/golden/golden-overlay-job-v1.json; the assets (background,
# apple overlay, vendored DejaVuSans font) are the deterministic fixtures in
# the same directory (hashes baked into the payload, so a regenerate of the
# fixtures without updating the payload fails loudly at the PUT hash check).
#
# Besides the render chain, the canary certifies:
#
#   - PostgreSQL persistence (docker compose stack): render_jobs holds
#     state=completed with attempt_count=1, exactly one render_attempt
#     row, exactly one JOB_CREATED/JOB_CLAIMED/JOB_COMPLETED event, and
#     the render_artifacts row whose sha256 matches the downloaded bytes.
#   - Idempotent replay without a new render: re-submitting the identical
#     job resolves to the existing canonical job (HTTP 200/409), returns
#     the SAME artifact hash, and leaves attempts and render events
#     byte-for-byte unchanged — the pipeline behaves like a real
#     distributed queue, not a script that simply re-runs FFmpeg.
#
# PostgreSQL assertions are auto-detected: they run through
# `docker compose exec postgres psql` when the canonical stack is up, or
# through `psql "$DATABASE_URL"` when DATABASE_URL + psql are available.
# Without either they degrade to a WARN so the script still works against
# remote queue/objectstore endpoints.
#
# Usage:
#   1. Start infrastructure: (cd infra/docker && docker compose up -d postgres objectstore)
#      Enable the native Queue, RenderingGen and Chronon systemd services.
#   2. Run:               infra/e2e/run-golden-overlay.sh
#
# Requires curl, python3, ffprobe, sha256sum.
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
STORE_URL="${STORE_URL:-http://localhost:9000}"
OUT_FILE="${OUT_FILE:-/tmp/renderinggen-golden-overlay-result.mp4}"
# Expected render geometry; defaults match GoldenOverlayJobV1 (5s @ 30fps =
# 150 frames). GoldenOverlayJobV2 (the universal benchmark) overrides these
# to 8s / 240 frames.
EXPECTED_DURATION="${EXPECTED_DURATION:-5}"
EXPECTED_FRAMES="${EXPECTED_FRAMES:-150}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
GOLDEN_DIR="${REPO_ROOT}/testdata/golden"
JOB_FILE="${JOB_FILE:-${GOLDEN_DIR}/golden-overlay-job-v1.json}"

JOB_ID="${JOB_ID:-golden-overlay-v1}"

WORK_DIR="$(mktemp -d /tmp/golden-overlay.XXXXXX)"
trap 'rm -rf "$WORK_DIR"' EXIT

json_field() { # $1 = body, $2 = dotted path (array index supported: a.0.b)
  printf '%s' "$1" | python3 -c '
import sys, json
d = json.load(sys.stdin)
for part in sys.argv[1].split("."):
    if part.isdigit():
        d = d[int(part)]
    else:
        d = d[part]
print(d)
' "$2"
}

# Build the submit payload once (id + render_plan.job_id injected), reuse it
# for the first submission AND the idempotent replay so both carry the
# byte-identical body.
python3 - "${JOB_FILE}" "${JOB_ID}" "${WORK_DIR}/payload.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    payload = json.load(f)
payload["id"] = sys.argv[2]
payload["render_plan"]["job_id"] = sys.argv[2]
with open(sys.argv[3], "w", encoding="utf-8") as f:
    json.dump(payload, f)
PY

# ── PostgreSQL certification (auto-detected) ─────────────────────────────
GOLDEN_PG_CMD=()
golden_pg_init() {
  # 1. Explicit DATABASE_URL + local psql (remote stack / bare metal).
  if command -v psql >/dev/null 2>&1 && [[ -n "${DATABASE_URL:-}" ]]; then
    GOLDEN_PG_CMD=(psql "${DATABASE_URL}" -At -c)
  # 2. Canonical docker compose stack (postgres service, psql inside the
  #    container — no host psql needed).
  elif docker compose -f "${REPO_ROOT}/infra/docker/docker-compose.yaml" \
      exec -T postgres psql -U renderinggen -d renderinggen -At -c 'SELECT 1' >/dev/null 2>&1; then
    GOLDEN_PG_CMD=(docker compose -f "${REPO_ROOT}/infra/docker/docker-compose.yaml" \
      exec -T postgres psql -U renderinggen -d renderinggen -At -c)
  else
    printf 'WARN: PostgreSQL not reachable (start the docker compose stack or set DATABASE_URL + psql); skipping PostgreSQL assertions\n' >&2
    return 1
  fi
}
golden_pg_query() { # $1 = SQL; stdout = rows (one per line)
  [[ ${#GOLDEN_PG_CMD[@]} -gt 0 ]] || return 2
  "${GOLDEN_PG_CMD[@]}" "$1"
}
golden_pg_assert_eq() { # $1 = expected, $2 = actual, $3 = label
  if [[ "$1" != "$2" ]]; then
    printf 'ERROR: PostgreSQL assertion failed: %s — expected [%s], got [%s]\n' "$3" "$1" "$2" >&2
    exit 1
  fi
}

submit_job() { # stdout = HTTP code; body in $WORK_DIR/submit.body
  curl -s -o "$WORK_DIR/submit.body" -w '%{http_code}' -X POST "${QUEUE_URL}/jobs" \
    -H 'Content-Type: application/json' --data-binary @"${WORK_DIR}/payload.json"
}

# Poll until terminal; on completion set STATE/HASH/SIZE/CTYPE/BACKEND/
# CHRONON_VER/ATTEMPTS. On failure dump the body and exit 1.
wait_completed() {
  local body
  for _ in $(seq 1 120); do
    body="$(curl -fsS "${QUEUE_URL}/jobs/${JOB_ID}")"
    STATE="$(json_field "$body" state)"
    case "${STATE}" in
      completed)
        HASH="$(json_field "$body" artifact.artifact_hash)"
        SIZE="$(json_field "$body" artifact.size_bytes)"
        CTYPE="$(json_field "$body" artifact.content_type)"
        BACKEND="$(json_field "$body" artifact.backend)"
        CHRONON_VER="$(json_field "$body" artifact.chronon_version)"
        ATTEMPTS="$(json_field "$body" attempts)"
        return 0
        ;;
      failed)
        echo "ERROR: job failed" >&2
        printf '%s\n' "$body" >&2
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
}

echo "=== GoldenOverlayJobV1 (golden canary) ==="
echo "payload: ${JOB_FILE}"

# 1. Upload the deterministic assets to the artifact store (L3), keyed by
#    their content hashes. The worker resolves them by hash during
#    materialize, so a hash mismatch here means the fixtures drifted.
ASSET_COUNT="$(python3 - "${JOB_FILE}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    print(len(json.load(f)["assets"]))
PY
)"
for i in $(seq 0 $((ASSET_COUNT - 1))); do
  BODY="$(cat "${JOB_FILE}")"
  HASH="$(json_field "${BODY}" "assets.${i}.hash")"
  LOGICAL="$(json_field "${BODY}" "assets.${i}.logical_path")"
  LOCAL_FILE="${GOLDEN_DIR}/$(basename "${LOGICAL}")"
  echo "  uploading ${LOGICAL} (${HASH:0:12}...) -> ${STORE_URL}/objects/${HASH}"
  if [ "$(sha256sum "${LOCAL_FILE}" | cut -d' ' -f1)" != "${HASH}" ]; then
    echo "ERROR: ${LOCAL_FILE} sha256 does not match payload hash ${HASH}" >&2
    echo "       re-run: python3 infra/e2e/gen-golden-assets.py" >&2
    exit 1
  fi
  curl -fsS -X PUT --data-binary @"${LOCAL_FILE}" \
    -H 'Content-Type: application/octet-stream' \
    "${STORE_URL}/objects/${HASH}" >/dev/null
done

# 2. Submit the job (the worker claims it and runs the full pipeline).
#    A pre-existing job (e.g. from a previous canary run against the same
#    PostgreSQL volume) resolves idempotently: 200 = canonical job returned,
#    409 = job already exists — both mean "no second job, no second render".
echo "Submitting job ${JOB_ID} ..."
SUBMIT_CODE="$(submit_job)"
case "${SUBMIT_CODE}" in
  201) echo "  created (201)" ;;
  200|409) echo "  idempotent success (${SUBMIT_CODE}) — job already exists, resolving to the canonical job" ;;
  *)
    echo "ERROR: submit returned HTTP ${SUBMIT_CODE}" >&2
    cat "${WORK_DIR}/submit.body" >&2
    exit 1
    ;;
esac

# 3. Wait for the first completion.
echo "Waiting for completion (first render) ..."
wait_completed
echo "completed: attempts=${ATTEMPTS} artifact_hash=${HASH} size=${SIZE} content_type=${CTYPE} backend=${BACKEND} chronon_version=${CHRONON_VER}"
if [ "${ATTEMPTS}" != "1" ]; then
  echo "ERROR: golden job must render exactly once; attempt_count=${ATTEMPTS}" >&2
  echo "       reset the canary state with: make golden-e2e-reset" >&2
  exit 1
fi
FIRST_HASH="${HASH}"

# 4. Download the artifact from the store.
echo "Downloading artifact from ${STORE_URL} ..."
curl -fsS "${STORE_URL}/objects/${HASH}" -o "${OUT_FILE}"
DOWNLOAD_SIZE="$(stat -c%s "${OUT_FILE}")"
if [ "${DOWNLOAD_SIZE}" -eq 0 ]; then
  echo "ERROR: artifact ${OUT_FILE} is empty" >&2
  exit 1
fi

# 5. Verify: size > 0, decodable, 1280x720, 30fps, 150 frames (5s),
#    byte-identical to the store object (hash matches).
echo "Verifying ${OUT_FILE} ..."
if [ "${DOWNLOAD_SIZE}" -ne "${SIZE}" ]; then
  echo "ERROR: downloaded size ${DOWNLOAD_SIZE} != queue artifact size ${SIZE}" >&2
  exit 1
fi
if [ "$(sha256sum "${OUT_FILE}" | cut -d' ' -f1)" != "${HASH}" ]; then
  echo "ERROR: downloaded sha256 does not match artifact_hash ${HASH}" >&2
  exit 1
fi

PROBE="$(ffprobe -v error -select_streams v:0 \
  -show_entries stream=width,height,r_frame_rate,nb_frames \
  -show_entries format=duration -of json "${OUT_FILE}")"
P_WIDTH="$(json_field "${PROBE}" streams.0.width)"
P_HEIGHT="$(json_field "${PROBE}" streams.0.height)"
P_FRAMES="$(printf '%s' "${PROBE}" | python3 -c 'import json, sys; print(json.load(sys.stdin)["streams"][0].get("nb_frames", "N/A"))')"
P_DURATION="$(json_field "${PROBE}" format.duration)"
echo "  probe: ${P_WIDTH}x${P_HEIGHT} @ ${P_FRAMES} frames, duration=${P_DURATION}s"

if [ "${P_WIDTH}" != "1280" ] || [ "${P_HEIGHT}" != "720" ]; then
  echo "ERROR: expected 1280x720, got ${P_WIDTH}x${P_HEIGHT}" >&2
  exit 1
fi
python3 - "$P_DURATION" "${P_FRAMES}" "${EXPECTED_DURATION}" "${EXPECTED_FRAMES}" <<'PY'
import sys
d = float(sys.argv[1])
frames = sys.argv[2]
want_duration = float(sys.argv[3])
want_frames = int(sys.argv[4])
if abs(d - want_duration) > 0.15:
    print(f"ERROR: expected ~{want_duration}s duration, got {d}s")
    sys.exit(1)
# ffprobe may report N/A for nb_frames on h264 mp4; derive from duration x fps.
expected = round(d * 30)
if frames != "N/A" and int(frames) != want_frames:
    print(f"ERROR: expected ~{want_frames} frames ({want_duration}s @ 30fps), got {frames}")
    sys.exit(1)
PY

# 6. PostgreSQL certification: the job, its single attempt and its artifact
#    must all be durably persisted, and the stored sha256 must match the
#    bytes we just downloaded.
if golden_pg_init; then
  echo "=== PostgreSQL certification ==="
  ROW_STATE="$(golden_pg_query "SELECT state FROM render_jobs WHERE id = '${JOB_ID}'" || true)"
  ROW_ATTEMPTS="$(golden_pg_query "SELECT attempt_count FROM render_jobs WHERE id = '${JOB_ID}'" || true)"
  ATTEMPT_ROWS="$(golden_pg_query "SELECT COUNT(*) FROM render_attempts WHERE job_id = '${JOB_ID}'" || true)"
  ART_SHA="$(golden_pg_query "SELECT sha256 FROM render_artifacts WHERE job_id = '${JOB_ID}'" || true)"
  ART_SIZE="$(golden_pg_query "SELECT size_bytes FROM render_artifacts WHERE job_id = '${JOB_ID}'" || true)"
  EVT_CREATED="$(golden_pg_query "SELECT COUNT(*) FROM render_events WHERE job_id = '${JOB_ID}' AND event_type = 'JOB_CREATED'" || true)"
  EVT_CLAIMED="$(golden_pg_query "SELECT COUNT(*) FROM render_events WHERE job_id = '${JOB_ID}' AND event_type = 'JOB_CLAIMED'" || true)"
  EVT_COMPLETED="$(golden_pg_query "SELECT COUNT(*) FROM render_events WHERE job_id = '${JOB_ID}' AND event_type = 'JOB_COMPLETED'" || true)"
  echo "  render_jobs:      state=${ROW_STATE} attempt_count=${ROW_ATTEMPTS}"
  echo "  render_attempts:  rows=${ATTEMPT_ROWS}"
  echo "  render_artifacts: sha256=${ART_SHA} size=${ART_SIZE}"
  echo "  render_events:    JOB_CREATED=${EVT_CREATED} JOB_CLAIMED=${EVT_CLAIMED} JOB_COMPLETED=${EVT_COMPLETED}"
  golden_pg_assert_eq "completed" "${ROW_STATE}" "render_jobs.state"
  golden_pg_assert_eq "1" "${ROW_ATTEMPTS}" "render_jobs.attempt_count (exactly one render)"
  golden_pg_assert_eq "1" "${ATTEMPT_ROWS}" "render_attempts row count (exactly one attempt)"
  golden_pg_assert_eq "${HASH}" "${ART_SHA}" "render_artifacts.sha256 == queue artifact_hash"
  golden_pg_assert_eq "${SIZE}" "${ART_SIZE}" "render_artifacts.size_bytes == queue artifact size"
  golden_pg_assert_eq "1" "${EVT_CREATED}" "JOB_CREATED event count"
  golden_pg_assert_eq "1" "${EVT_CLAIMED}" "JOB_CLAIMED event count (claimed exactly once)"
  golden_pg_assert_eq "1" "${EVT_COMPLETED}" "JOB_COMPLETED event count"
fi

# 7. Idempotent replay without a new render: re-submitting the byte-identical
#    job must resolve to the existing job and return the same artifact, with
#    no new attempt and no new event — the signature of a distributed queue,
#    not of a script that re-runs the renderer.
echo "=== Idempotent replay (no new render) ==="
PRE_ATTEMPTS="${ATTEMPTS}"
PRE_EVENTS_TOTAL="$(golden_pg_query "SELECT COUNT(*) FROM render_events WHERE job_id = '${JOB_ID}'" || true)"

REPLAY_CODE="$(submit_job)"
echo "  re-submit identical job -> HTTP ${REPLAY_CODE} (200/409 = idempotent success)"
case "${REPLAY_CODE}" in
  200|409) : ;;
  *)
    echo "ERROR: replay submit returned HTTP ${REPLAY_CODE}" >&2
    exit 1
    ;;
esac

echo "Waiting for replay resolution ..."
wait_completed
echo "  replay resolved: attempts=${ATTEMPTS} artifact_hash=${HASH}"

if [ "${ATTEMPTS}" != "${PRE_ATTEMPTS}" ]; then
  echo "ERROR: replay incremented attempts (${PRE_ATTEMPTS} -> ${ATTEMPTS}): a NEW RENDER was started" >&2
  exit 1
fi
if [ "${HASH}" != "${FIRST_HASH}" ]; then
  echo "ERROR: replay returned a different artifact (${FIRST_HASH} -> ${HASH})" >&2
  exit 1
fi
# The stored object must still be reachable under the same content hash.
GET_CODE="$(curl -s -o /dev/null -w '%{http_code}' "${STORE_URL}/objects/${HASH}")"
if [ "${GET_CODE}" != "200" ]; then
  echo "ERROR: artifact no longer reachable at ${STORE_URL}/objects/${HASH} (HTTP ${GET_CODE})" >&2
  exit 1
fi

if [[ ${#GOLDEN_PG_CMD[@]} -gt 0 ]]; then
  POST_EVENTS_TOTAL="$(golden_pg_query "SELECT COUNT(*) FROM render_events WHERE job_id = '${JOB_ID}'" || true)"
  POST_ATTEMPT_ROWS="$(golden_pg_query "SELECT COUNT(*) FROM render_attempts WHERE job_id = '${JOB_ID}'" || true)"
  POST_ROW_ATTEMPTS="$(golden_pg_query "SELECT attempt_count FROM render_jobs WHERE id = '${JOB_ID}'" || true)"
  echo "  after replay: render_events=${POST_EVENTS_TOTAL} render_attempts=${POST_ATTEMPT_ROWS} attempt_count=${POST_ROW_ATTEMPTS}"
  golden_pg_assert_eq "${PRE_EVENTS_TOTAL}" "${POST_EVENTS_TOTAL}" "render_events total unchanged after replay (no new render)"
  golden_pg_assert_eq "${ATTEMPT_ROWS}" "${POST_ATTEMPT_ROWS}" "render_attempts unchanged after replay (no new render)"
  golden_pg_assert_eq "${ROW_ATTEMPTS}" "${POST_ROW_ATTEMPTS}" "render_jobs.attempt_count unchanged after replay (no new render)"
fi

echo
echo "OK: GoldenOverlayJobV1 passed"
echo "    artifact: ${OUT_FILE}, ${DOWNLOAD_SIZE} bytes, ${P_WIDTH}x${P_HEIGHT}, ${P_FRAMES} frames, sha256=${FIRST_HASH}"
echo "    chain:    queue -> RenderingGen -> Chronon (${BACKEND} ${CHRONON_VER}) -> artifact -> PostgreSQL -> replay (idempotent, no new render)"
