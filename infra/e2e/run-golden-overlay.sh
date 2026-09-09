#!/usr/bin/env bash
#
# GoldenSemanticOverlayJobV1 — end-to-end golden canary over the real chain:
#
#   submit queue -> claim RenderingGen -> materialize background.jpg
#   -> materialize apple.png -> plan.json -> chronon3d_cli render
#   -> result.mp4 -> artifact store -> completed -> download + verify
#   -> PostgreSQL certification -> idempotent replay (no new render)
#
# The job is the real RenderingGen semantic workload (not a color smoke):
#
#   background.jpg        full 5s      (f0-149)
#   "QUESTO CAMBIA TUTTO" title_centered (f20-60)
#   "APPLE"               kinetic_word   (f65-95)
#   apple.png             contain, right (f90-135)
#
# The payload is the canonical, immutable
# testdata/golden/golden-semantic-overlay-job-v1.json; the assets (background,
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
# Expected render geometry; defaults match GoldenSemanticOverlayJobV1 (5s @ 30fps =
# 150 frames). GoldenOverlayJobV2 (the universal benchmark) overrides these
# to 8s / 240 frames.
EXPECTED_DURATION="${EXPECTED_DURATION:-5}"
EXPECTED_FRAMES="${EXPECTED_FRAMES:-150}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
GOLDEN_DIR="${REPO_ROOT}/testdata/golden"
JOB_FILE="${JOB_FILE:-${GOLDEN_DIR}/golden-semantic-overlay-job-v1.json}"

JOB_ID="${JOB_ID:-golden-semantic-overlay-v1}"

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

# Build the submit payload once (id + plan identity injected), reuse it
# for the first submission AND the idempotent replay so both carry the
# byte-identical body.
python3 - "${JOB_FILE}" "${JOB_ID}" "${WORK_DIR}/payload.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    payload = json.load(f)
payload["id"] = sys.argv[2]
plan = payload["render_plan"]
if plan.get("schema_version") == "renderinggen.overlay-plan.v1":
    plan["plan_id"] = sys.argv[2]
    plan["video_id"] = sys.argv[2]
else:
    plan["job_id"] = sys.argv[2]
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


# Remaining steps live in run-golden-overlay_part02.sh (sourced below) so this
# canary stays under the per-file LOC budget.
source "$(dirname "${BASH_SOURCE[0]}")/run-golden-overlay_part02.sh"
