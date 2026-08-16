#!/usr/bin/env bash
#
# Test 5 — RenderingGen worker end-to-end (submitted → pending → running → completed).
#
# Submits a single-segment job whose render_plan is a concrete
# chronon.render-plan.v1 (text "RENDERINGGEN TEST", title_centered + a color
# background, 1280x720 @ 30fps, 3s). The worker claims it, materializes the
# font asset, runs the real chronon3d_cli (software backend) and publishes the
# result. The script observes the state transitions and — crucially — PASSES
# only if the artifact file is really produced and downloadable, never on the
# status=completed value alone.
#
# The font is the vendored DejaVuSans.ttf in testdata/golden/ (its SHA-256 is
# baked into the job payload), so the test is self-contained.
#
# Usage:
#   1. Start the stack:   (cd infra/docker && docker compose up --build -d)
#   2. Run:               infra/e2e/run-test5-worker.sh
#
# Requires curl, python3, ffprobe, sha256sum.
set -euo pipefail

QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
STORE_URL="${STORE_URL:-http://localhost:9000}"
OUT_FILE="${OUT_FILE:-/tmp/renderinggen-test5-result.mp4}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
FONT_FILE="${REPO_ROOT}/testdata/golden/DejaVuSans.ttf"
FONT_HASH="$(sha256sum "${FONT_FILE}" | cut -d' ' -f1)"

JOB_ID="test5-worker-$(date +%s)"

WORK_DIR="$(mktemp -d /tmp/test5-worker.XXXXXX)"
trap 'rm -rf "$WORK_DIR"' EXIT

json_field() { # $1 = body, $2 = dotted path
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

# ── 1. Upload the font asset, addressed by content hash. ──────────────────
echo "== Test 5: worker end-to-end =="
echo "uploading ${FONT_FILE} (${FONT_HASH:0:12}...) -> ${STORE_URL}/objects/${FONT_HASH}"
curl -fsS -X PUT --data-binary @"${FONT_FILE}" \
  -H 'Content-Type: application/octet-stream' \
  "${STORE_URL}/objects/${FONT_HASH}" >/dev/null

# ── 2. Build + submit the job. ─────────────────────────────────────────────
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
      { "id": "background", "type": "color", "color": [0.03, 0.03, 0.05, 1.0] },
      {
        "id": "title",
        "type": "text",
        "text": "RENDERINGGEN TEST",
        "font": "assets/fonts/DejaVuSans.ttf",
        "preset": "title_centered",
        "start_frame": 15,
        "duration_frames": 60
      }
    ],
    "output": { "path": "result.mp4", "format": "mp4", "codec": "h264" }
  },
  "assets": [
    { "hash": "${FONT_HASH}", "logical_path": "assets/fonts/DejaVuSans.ttf" }
  ]
}
EOF

echo "submitting job ${JOB_ID} ..."
SUBMIT_CODE="$(curl -s -o "${WORK_DIR}/submit.body" -w '%{http_code}' \
  -X POST "${QUEUE_URL}/jobs" -H 'Content-Type: application/json' \
  --data-binary @"${WORK_DIR}/payload.json")"
if [ "${SUBMIT_CODE}" != "201" ] && [ "${SUBMIT_CODE}" != "200" ] && [ "${SUBMIT_CODE}" != "409" ]; then
  echo "ERROR: submit returned HTTP ${SUBMIT_CODE}" >&2
  cat "${WORK_DIR}/submit.body" >&2
  exit 1
fi
echo "  submitted (HTTP ${SUBMIT_CODE})"

# ── 3. Observe the state transitions. ─────────────────────────────────────
echo "state transitions:"
PREV=""
for _ in $(seq 1 400); do
  BODY="$(curl -fsS "${QUEUE_URL}/jobs/${JOB_ID}")"
  STATE="$(json_field "${BODY}" state)"
  if [ "${STATE}" != "${PREV}" ]; then
    printf '  %s\n' "${STATE}"
    PREV="${STATE}"
  fi
  if [ "${STATE}" = "completed" ] || [ "${STATE}" = "failed" ]; then
    break
  fi
  sleep 0.25
done

if [ "${STATE}" != "completed" ]; then
  echo "ERROR: job ended in state '${STATE}' (expected completed)" >&2
  printf '%s\n' "${BODY}" >&2
  exit 1
fi

# ── 4. PASS only if the artifact file is really produced. ─────────────────
HASH="$(json_field "${BODY}" artifact.artifact_hash)"
SIZE="$(json_field "${BODY}" artifact.size_bytes)"
WIDTH="$(json_field "${BODY}" artifact.width)"
HEIGHT="$(json_field "${BODY}" artifact.height)"
echo "completed: artifact_hash=${HASH:0:12}... size=${SIZE} ${WIDTH}x${HEIGHT}"

echo "downloading artifact ..."
curl -fsS "${STORE_URL}/objects/${HASH}" -o "${OUT_FILE}"
if [ ! -s "${OUT_FILE}" ]; then
  echo "FAIL: status=completed but the artifact file is missing/empty" >&2
  exit 1
fi

DOWNLOAD_SIZE="$(stat -c%s "${OUT_FILE}")"
DOWNLOAD_HASH="$(sha256sum "${OUT_FILE}" | cut -d' ' -f1)"
if [ "${DOWNLOAD_SIZE}" -ne "${SIZE}" ]; then
  echo "FAIL: downloaded size ${DOWNLOAD_SIZE} != artifact size ${SIZE}" >&2
  exit 1
fi
if [ "${DOWNLOAD_HASH}" != "${HASH}" ]; then
  echo "FAIL: downloaded sha256 ${DOWNLOAD_HASH} != artifact_hash ${HASH}" >&2
  exit 1
fi

PROBE="$(ffprobe -v error -select_streams v:0 \
  -show_entries stream=width,height,r_frame_rate \
  -show_entries format=duration -of json "${OUT_FILE}")"
P_WIDTH="$(json_field "${PROBE}" streams.0.width)"
P_HEIGHT="$(json_field "${PROBE}" streams.0.height)"
P_DURATION="$(json_field "${PROBE}" format.duration)"
echo "  probe: ${P_WIDTH}x${P_HEIGHT} duration=${P_DURATION}s"

if [ "${P_WIDTH}" != "1280" ] || [ "${P_HEIGHT}" != "720" ]; then
  echo "FAIL: expected 1280x720, got ${P_WIDTH}x${P_HEIGHT}" >&2
  exit 1
fi
python3 - "${P_DURATION}" <<'PY'
import sys
d = float(sys.argv[1])
if abs(d - 3.0) > 0.15:
    print(f"FAIL: expected ~3s duration, got {d}s")
    sys.exit(1)
PY

echo ""
echo "OK: Test 5 passed — worker produced a real artifact"
echo "    chain:   submit -> pending -> running -> chronon -> completed -> artifact (${DOWNLOAD_SIZE} bytes)"
echo "    file:    ${OUT_FILE}"
