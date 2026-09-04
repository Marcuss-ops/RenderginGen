#!/usr/bin/env bash
# Recreate a source clip through RenderingGen -> Chronon3d.
#
# Usage:
#   recreate-clip-with-chronon.sh INPUT.mp4 [JOB.json] [OUTPUT.mp4]
#
# With SUBMIT=1 it also uploads the source to the object store, submits the
# generated job to the queue, waits for completion, downloads the artifact and
# verifies the exact frame rate, frame count and duration. The worker decides
# whether to use Chronon's direct-YUV fast path (plain clip) or composition
# path (clip with overlays); this script never invokes ffmpeg as a renderer.
set -euo pipefail

INPUT="${1:?input clip is required}"
JOB_FILE="${2:-/tmp/chronon-clip-job.json}"
OUTPUT="${3:-/tmp/chronon-clip-output.mp4}"
QUEUE_URL="${QUEUE_URL:-http://localhost:8081}"
STORE_URL="${STORE_URL:-http://localhost:9000}"
JOB_ID="${JOB_ID:-clip-recreate-$(date +%s)}"

command -v ffprobe >/dev/null || { echo "ERROR: ffprobe is required" >&2; exit 1; }
command -v python3 >/dev/null || { echo "ERROR: python3 is required" >&2; exit 1; }
[[ -f "$INPUT" ]] || { echo "ERROR: input does not exist: $INPUT" >&2; exit 1; }

SOURCE_HASH="$(sha256sum "$INPUT" | awk '{print $1}')"
PROBE="$(ffprobe -v error -select_streams v:0 \
  -show_entries stream=width,height,r_frame_rate,nb_frames \
  -show_entries format=duration -of json "$INPUT")"
AUDIO_STREAMS="$(ffprobe -v error -select_streams a \
  -show_entries stream=index -of csv=p=0 "$INPUT" | sed '/^$/d' | wc -l)"

python3 - "$PROBE" "$AUDIO_STREAMS" "$JOB_ID" "$SOURCE_HASH" "$INPUT" "$JOB_FILE" <<'PY'
import json, os, sys

probe = json.loads(sys.argv[1])
audio_streams = int(sys.argv[2])
stream = probe["streams"][0]
fps_num, fps_den = map(int, stream["r_frame_rate"].split("/", 1))
duration = float(probe["format"]["duration"])
frames_raw = stream.get("nb_frames")
frames = int(frames_raw) if frames_raw and frames_raw != "N/A" else round(duration * fps_num / fps_den)
if min(fps_num, fps_den, frames, stream.get("width", 0), stream.get("height", 0)) <= 0:
    raise SystemExit("ERROR: input has incomplete video timing metadata")

job_id, digest, source, out = sys.argv[3:]
asset_path = f"assets/semantic/{job_id}.mp4"
job = {
    "id": job_id,
    "schema": "renderinggen.job",
    "version": 1,
    "job_type": "clip.render",
    "render_plan": {
        "schema_version": "renderinggen.overlay-plan.v1",
        "plan_id": job_id,
        "video_id": job_id,
        "width": stream["width"], "height": stream["height"],
        "fps_num": fps_num, "fps_den": fps_den,
        # Chronon's timeline is frame-exact. Container duration may include a
        # small audio tail, so never derive the video frame count from it.
        "duration_ms": round(frames * 1000 * fps_den / fps_num),
        "source": {"asset_id": job_id, "sha256": digest, "path": asset_path},
        "items": []
    },
    "assets": [{"hash": digest, "logical_path": asset_path}],
}
if audio_streams:
    job["render_plan"]["audio"] = {"mode": "copy_if_compatible"}
with open(out, "w", encoding="utf-8") as f:
    json.dump(job, f, indent=2)
    f.write("\n")
print(f"job={job_id} {stream['width']}x{stream['height']} @{fps_num}/{fps_den} frames={frames} duration={duration:.6f}s")
PY

echo "Generated Chronon semantic job: $JOB_FILE"
echo "source_sha256=$SOURCE_HASH"

if [[ "${SUBMIT:-0}" != "1" ]]; then
  echo "Submission disabled. Set SUBMIT=1 to run the queue/worker/Chronon path."
  exit 0
fi

curl -fsS -X PUT --data-binary @"$INPUT" \
  -H 'Content-Type: video/mp4' "$STORE_URL/objects/$SOURCE_HASH" >/dev/null
curl -fsS -X POST "$QUEUE_URL/jobs" \
  -H 'Content-Type: application/json' --data-binary @"$JOB_FILE" >/dev/null

for _ in $(seq 1 180); do
  BODY="$(curl -fsS "$QUEUE_URL/jobs/$JOB_ID")"
  STATE="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["state"])' <<<"$BODY")"
  case "$STATE" in
    completed) break ;;
    failed) echo "$BODY" >&2; exit 1 ;;
    *) sleep 2 ;;
  esac
done
[[ "${STATE:-}" == completed ]] || { echo "ERROR: timed out waiting for $JOB_ID" >&2; exit 1; }

python3 - "$BODY" "$STORE_URL" "$OUTPUT" "$PROBE" <<'PY'
import json, subprocess, sys
body = json.loads(sys.argv[1])
store = sys.argv[2]
output = sys.argv[3]
source_probe = json.loads(sys.argv[4])
artifact = body.get("artifact") or {}
if not artifact.get("chronon_version"):
    raise SystemExit("ERROR: completed artifact has no Chronon provenance")
url = store.rstrip("/") + "/objects/" + artifact["artifact_hash"]
with open(output, "wb") as f:
    subprocess.run(["curl", "-fsS", url], stdout=f, check=True)
got = json.loads(subprocess.check_output([
    "ffprobe", "-v", "error", "-select_streams", "v:0",
    "-show_entries", "stream=width,height,r_frame_rate,nb_frames",
    "-show_entries", "format=duration", "-of", "json", output]))
want = source_probe
ws, gs = want["streams"][0], got["streams"][0]
for key in ("width", "height", "r_frame_rate", "nb_frames"):
    if str(ws.get(key)) != str(gs.get(key)):
        raise SystemExit(f"ERROR: {key} changed: source={ws.get(key)} output={gs.get(key)}")
if abs(float(want["format"]["duration"]) - float(got["format"]["duration"])) > 0.05:
    raise SystemExit("ERROR: output duration drift exceeds 50 ms")
print(f"PASS: Chronon {artifact['chronon_version']} output={output} timing and frame count match")
PY
