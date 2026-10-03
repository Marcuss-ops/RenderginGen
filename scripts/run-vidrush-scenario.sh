#!/usr/bin/env bash
#
# Vidrush-like concatenated overlay scenario — 2 frasi + date + metric + 2
# entità con testo + luoghi/mappa + immagini x2/x3/x4/x5 in ONE plan, rendered
# by the real chronon3d_cli and probed act by act.
#
# The scenario itself (acts, windows, motions, sampled frames, thresholds) is
# owned by cmd/vidrush-scenario, not by this script: the script renders and
# probes pixels, but it never restates a window or a frame number, so the two
# halves cannot drift apart.
#
# What it proves, per run:
#   - every item really draws inside its window (its mid frame differs from its
#     own start frame beyond the encode tolerance, in the item's region);
#   - the timeline is one continuous chain: consecutive items never render the
#     same picture, so no act is a held frame of the previous one;
#   - the container contract holds (1920x1080 @ 24/1, >= 816 frames);
#   - one still per act is written next to the MP4 for a quick visual check.
#
# Usage:
#   RenderingGen/scripts/run-vidrush-scenario.sh
#
# Environment:
#   CHRONON_BIN   path to chronon3d_cli (otherwise discovered under ../Chronon3d)
#   LANE          software (default) | vulkan | vulkan-pipe
#                 software     — CPU raster + pipe encoder (deterministic; slow)
#                 vulkan       — GPU raster + NVENC (production lane)
#                 vulkan-pipe  — GPU raster + pipe encoder: use it when the
#                                build's NVENC probe fails but a GPU is present
#   KEEP=1        keep the working directory (plans, frames, log) on exit
#   OUT_DIR       directory for the rendered MP4 (default:
#                 RenderingGen/out/vidrush-scenario-<YYYYMMDD>)
#
# Requires go, ffmpeg, ffprobe, python3.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RENDERINGGEN_ROOT="$(cd "${HERE}/.." && pwd)"
MODULE_ROOT="${RENDERINGGEN_ROOT}/renderinggen"
GOLDEN_DIR="${RENDERINGGEN_ROOT}/testdata/golden"
FONT="${GOLDEN_DIR}/assets/fonts/Poppins-Bold.ttf"

LANE="${LANE:-software}"
KEEP="${KEEP:-0}"

for tool in go ffmpeg ffprobe python3; do
  if ! command -v "${tool}" >/dev/null 2>&1; then
    echo "ERROR: ${tool} is required" >&2
    exit 1
  fi
done
if [[ ! -f "${FONT}" ]]; then
  echo "ERROR: official font not found at ${FONT}" >&2
  exit 1
fi

# ── Resolve the renderer ─────────────────────────────────────────────────
if [[ -n "${CHRONON_BIN:-}" ]]; then
  BIN="${CHRONON_BIN}"
else
  BIN=""
  for candidate in \
    "${RENDERINGGEN_ROOT}/../Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli" \
    "${RENDERINGGEN_ROOT}/../Chronon3d/build/chronon"/*/apps/chronon3d_cli/chronon3d_cli; do
    if [[ -x "${candidate}" ]]; then
      BIN="${candidate}"
      break
    fi
  done
fi
if [[ -z "${BIN}" || ! -x "${BIN}" ]]; then
  echo "ERROR: chronon3d_cli not found; set CHRONON_BIN to a built binary" >&2
  exit 1
fi
echo "renderer: ${BIN}"

WORK="$(mktemp -d /tmp/vidrush-scenario.XXXXXX)"
cleanup() {
  if [[ "${KEEP}" == "1" ]]; then
    echo "work dir kept: ${WORK}"
  else
    rm -rf "${WORK}"
  fi
}
trap cleanup EXIT

OUT_DIR="${OUT_DIR:-${RENDERINGGEN_ROOT}/out/vidrush-scenario-$(date +%Y%m%d)}"
mkdir -p "${OUT_DIR}/frames"

# ── Build the scenario planner, the fixtures and the plan documents ──────
echo "building cmd/vidrush-scenario ..."
( cd "${MODULE_ROOT}" && go build -o "${WORK}/bin/vidrush-scenario" ./cmd/vidrush-scenario )
"${WORK}/bin/vidrush-scenario" -assets-root "${WORK}" -out-dir "${WORK}/plans" -font "${FONT}"

# ── Render ───────────────────────────────────────────────────────────────
OUT_MP4="${OUT_DIR}/vidrush-scenario.mp4"
case "${LANE}" in
  software)
    RENDER_ARGS=(--backend software --encoder-backend pipe --hardware none
      --gpu-hot-path-mode auto --encode-preset ultrafast)
    ;;
  vulkan)
    RENDER_ARGS=(--backend vulkan --encoder-backend native --hardware nvenc
      --gpu-hot-path-mode require_gpu_native --encode-preset p1)
    ;;
  vulkan-pipe)
    RENDER_ARGS=(--backend vulkan --encoder-backend pipe --hardware none
      --gpu-hot-path-mode auto --encode-preset ultrafast)
    ;;
  *)
    echo "ERROR: unknown LANE '${LANE}' (software | vulkan | vulkan-pipe)" >&2
    exit 1
    ;;
esac

echo "rendering on LANE=${LANE} ..."
( cd "${WORK}" && "${BIN}" render \
    --plan "${WORK}/plans/render_plan.json" \
    --assets-root "${WORK}" \
    "${RENDER_ARGS[@]}" \
    -o "${OUT_MP4}" ) | tee "${OUT_DIR}/render.log"
if [[ ! -s "${OUT_MP4}" ]]; then
  echo "ERROR: render produced no artefact at ${OUT_MP4}" >&2
  exit 1
fi

# ── Certify act by act ───────────────────────────────────────────────────
python3 - "${WORK}/plans/scenario.json" "${OUT_MP4}" "${WORK}" "${OUT_DIR}/frames" <<'PY'
import json
import os
import subprocess
import sys

scenario_path, video, workdir, frames_dir = sys.argv[1:5]
with open(scenario_path, encoding="utf-8") as f:
    sc = json.load(f)

W, H, FPS = sc["width"], sc["height"], sc["fps"]
TOL = 16
failures = []


def fail(msg):
    failures.append(msg)
    print("FAIL: " + msg, file=sys.stderr)


# ── Container contract ────────────────────────────────────────────────────
probe = json.loads(subprocess.run(
    ["ffprobe", "-v", "error", "-select_streams", "v:0",
     "-show_entries", "stream=width,height,r_frame_rate,nb_frames", "-of", "json", video],
    check=True, capture_output=True).stdout)
stream = probe["streams"][0]
frames = int(stream.get("nb_frames") or 0)
want_frames = sc["duration_ms"] * FPS // 1000
print(f"container: {stream['width']}x{stream['height']} @ {stream['r_frame_rate']} frames={frames}")
if (stream["width"], stream["height"]) != (W, H):
    fail(f"geometry {stream['width']}x{stream['height']}, want {W}x{H}")
if stream["r_frame_rate"] != f"{FPS}/1":
    fail(f"fps {stream['r_frame_rate']}, want {FPS}/1")
if frames < want_frames:
    fail(f"frames {frames}, want >= {want_frames}")


# ── Frame extraction and RGB diff ─────────────────────────────────────────
def frame_rgb(n, tag):
    out = os.path.join(workdir, f"frame_{tag}_{n:04d}.rgb")
    subprocess.run(
        ["ffmpeg", "-v", "error", "-i", video,
         "-vf", f"select=eq(n\\,{n})", "-frames:v", "1",
         "-pix_fmt", "rgb24", "-f", "rawvideo", "-y", out],
        check=True)
    data = open(out, "rb").read()
    if len(data) != W * H * 3:
        fail(f"frame {n} ({tag}) produced {len(data)} bytes, want exactly {W * H * 3}")
        sys.exit(1)
    return data


def frame_png(n, path):
    subprocess.run(
        ["ffmpeg", "-v", "error", "-i", video,
         "-vf", f"select=eq(n\\,{n})", "-frames:v", "1", "-y", path],
        check=True)


def diff(a, b, region=None):
    """Count pixels inside region whose largest RGB channel delta exceeds TOL."""
    if region is None:
        count = 0
        for i in range(0, len(a), 3):
            if abs(a[i] - b[i]) > TOL or abs(a[i + 1] - b[i + 1]) > TOL or abs(a[i + 2] - b[i + 2]) > TOL:
                count += 1
        return count
    x0, y0, x1, y1 = region
    count = 0
    width = (x1 - x0) * 3
    for y in range(y0, y1):
        base = (y * W + x0) * 3
        row_a = a[base:base + width]
        row_b = b[base:base + width]
        for i in range(0, width, 3):
            if abs(row_a[i] - row_b[i]) > TOL or abs(row_a[i + 1] - row_b[i + 1]) > TOL or abs(row_a[i + 2] - row_b[i + 2]) > TOL:
                count += 1
    return count


center = (W // 2 - 380, H // 2 - 380, W // 2 + 380, H // 2 + 380)
previous_mid = None
previous_name = None
item_total = 0

for act in sc["acts"]:
    print(f"\n── {act['id']}: {act['title']} [{act['start_ms']},{act['end_ms']}) ms")
    for item in act["items"]:
        item_total += 1
        name = item["name"]
        region = center if item["center_only"] else None
        reference = frame_rgb(item["start_frame"], name + "-start")
        mid = frame_rgb(item["mid_frame"], name + "-mid")
        changed = diff(reference, mid, region)
        where = "centre box" if region else "full frame"
        print(f"  {name:26s} motion={item.get('motion') or '-':24s} start={item['start_frame']:3d} mid={item['mid_frame']:3d} changed={changed:6d} px ({where})")
        if changed < item["min_diff_pixels"]:
            fail(f"{name} (motion {item.get('motion') or '-'}): only {changed} px changed between frames "
                 f"{item['start_frame']} and {item['mid_frame']}, want >= {item['min_diff_pixels']} — the item is not visibly drawn")
        # One still per item for the human eye, sampled mid-window.
        frame_png(item["mid_frame"], os.path.join(frames_dir, f"{act['id']}_{name}.png"))
        # The chain check: consecutive items must never hold the same picture.
        if previous_mid is not None and diff(previous_mid, mid) == 0:
            fail(f"{previous_name} and {name} render the identical mid picture: the timeline is not concatenated")
        previous_mid, previous_name = mid, name

if item_total == 0:
    fail("scenario descriptor declares no items")

if failures:
    print(f"\nFAILED: {len(failures)} assertion(s)", file=sys.stderr)
    sys.exit(1)
print(f"\nPASS: {item_total} animated items across {len(sc['acts'])} concatenated acts — every window really drew")
PY

echo ""
echo "artefact: ${OUT_MP4}"
echo "stills:   ${OUT_DIR}/frames"
