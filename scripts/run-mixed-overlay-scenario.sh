#!/usr/bin/env bash
#
# Mixed overlay runtime scenario — 2 phrase overlays + 2 entity-image overlays
# in ONE plan, rendered by the real chronon3d_cli and certified pixel by pixel.
#
# The scenario itself (windows, presets, sampled frames, thresholds) is owned by
# cmd/mixed-overlay-scenario, not by this script: the script renders and probes
# pixels, but it never restates a window or a frame number, so the two halves
# cannot drift apart.
#
# What it proves, per run:
#   - every one of the four overlays really draws (its mid-window frame differs
#     from the background-only frame per RGB channel, beyond the encode
#     tolerance);
#   - no overlay leaks past its window (the overlay-free gap frames are
#     identical to the background-only frame);
#   - the four windows produce four different pictures (sampled during the
#     entrance, where the two portraits still differ by preset);
#   - the compiled plan carries exactly one background + two text + two image
#     layers, the entity images are centred, and the container contract holds
#     (1920x1080 @ 24/1, >= 240 frames).
#
# apple_v2 (the animated text preset) is deliberately NOT used: the phrases
# render with static_text_smoke, the text preset certified on this lane.
#
# Usage:
#   RenderingGen/scripts/run-mixed-overlay-scenario.sh
#
# Environment:
#   CHRONON_BIN   path to chronon3d_cli (otherwise discovered under ../Chronon3d)
#   LANE          software (default) | vulkan
#   KEEP=1        keep the working directory (plans, frames, artefact) on exit
#   OUT_DIR       directory for the rendered MP4 (default: a temp work dir)
#
# Requires go, ffmpeg, ffprobe, python3.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RENDERINGGEN_ROOT="$(cd "${HERE}/.." && pwd)"
MODULE_ROOT="${RENDERINGGEN_ROOT}/renderinggen"
GOLDEN_DIR="${RENDERINGGEN_ROOT}/testdata/golden"

LANE="${LANE:-software}"
KEEP="${KEEP:-0}"

for tool in go ffmpeg ffprobe python3; do
  if ! command -v "${tool}" >/dev/null 2>&1; then
    echo "ERROR: ${tool} is required" >&2
    exit 1
  fi
done

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

WORK="$(mktemp -d /tmp/mixed-overlay-scenario.XXXXXX)"
cleanup() {
  if [[ "${KEEP}" == "1" ]]; then
    echo "work dir kept: ${WORK}"
  else
    rm -rf "${WORK}"
  fi
}
trap cleanup EXIT

OUT_DIR="${OUT_DIR:-${WORK}}"
mkdir -p "${OUT_DIR}"

# ── Materialize the assets the plan declares ─────────────────────────────
# The two entity portraits and the official font must exist at exactly the
# logical paths the compiled plan references.
mkdir -p "${WORK}/assets/semantic" "${WORK}/assets/fonts"
for id in ent-alpha ent-beta; do
  cp "${GOLDEN_DIR}/gerard_butler.jpg" "${WORK}/assets/semantic/${id}.jpg"
done
cp "${GOLDEN_DIR}/assets/fonts/Poppins-Bold.ttf" "${WORK}/assets/fonts/Poppins-Bold.ttf"

# ── Build the scenario planner and write the plan documents ──────────────
echo "building cmd/mixed-overlay-scenario ..."
( cd "${MODULE_ROOT}" && go build -o "${WORK}/bin/mixed-overlay-scenario" ./cmd/mixed-overlay-scenario )
"${WORK}/bin/mixed-overlay-scenario" -assets-root "${WORK}" -out-dir "${WORK}/plans"

# ── Render ───────────────────────────────────────────────────────────────
OUT_MP4="${OUT_DIR}/mixed-overlay-scenario.mp4"
case "${LANE}" in
  software)
    RENDER_ARGS=(--backend software --encoder-backend pipe --hardware none
      --gpu-hot-path-mode auto --encode-preset ultrafast)
    ;;
  vulkan)
    RENDER_ARGS=(--backend vulkan --encoder-backend native --hardware nvenc
      --gpu-hot-path-mode require_gpu_native --encode-preset p1)
    ;;
  *)
    echo "ERROR: unknown LANE '${LANE}' (software | vulkan)" >&2
    exit 1
    ;;
esac

echo "rendering on LANE=${LANE} ..."
( cd "${WORK}" && "${BIN}" render \
    --plan "${WORK}/plans/render_plan.json" \
    --assets-root "${WORK}" \
    "${RENDER_ARGS[@]}" \
    -o "${OUT_MP4}" )
if [[ ! -s "${OUT_MP4}" ]]; then
  echo "ERROR: render produced no artefact at ${OUT_MP4}" >&2
  exit 1
fi

# ── Certify ──────────────────────────────────────────────────────────────
python3 - "${WORK}/plans/scenario.json" "${WORK}/plans/render_plan.json" "${OUT_MP4}" "${WORK}" <<'PY'
import json
import os
import subprocess
import sys

scenario_path, render_plan_path, video, workdir = sys.argv[1:5]
with open(scenario_path, encoding="utf-8") as f:
    sc = json.load(f)

W, H = sc["width"], sc["height"]
FPS = sc["fps"]
TOL = 16
failures = []


def fail(msg):
    failures.append(msg)
    print("FAIL: " + msg, file=sys.stderr)


# ── The compiled plan really carries the four overlays ────────────────────
with open(render_plan_path, encoding="utf-8") as f:
    plan = json.load(f)
text = sum(1 for l in plan["layers"] if l["type"] == "text")
image = sum(1 for l in plan["layers"] if l["type"] == "image")
background = [l for l in plan["layers"] if l["id"] == "background"]
want_text = sum(1 for i in sc["items"] if i["kind"] == "phrase")
want_image = sum(1 for i in sc["items"] if i["kind"] == "entity_image")
print(f"compiled plan: schema={plan.get('schema')} layers text={text} image={image} background={len(background)}")
if text != want_text or image != want_image:
    fail(f"compiled layers text={text} image={image}, want {want_text}/{want_image}")
if len(background) != 1:
    fail(f"compiled plan carries {len(background)} background layers, want exactly 1")
for layer in plan["layers"]:
    if layer["type"] == "image":
        pos = layer.get("position")
        if not (isinstance(pos, list) and len(pos) == 2 and pos[0] == 0 and pos[1] == 0):
            fail(f"entity image {layer['id']} is not centred: position={pos}")


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


# ── Pixel probes ──────────────────────────────────────────────────────────
# Probes compare RGB channels, not luma: the canonical fixture is white text on
# the near-white Pale Olive plate, where a luma-only probe measures a ~15-level
# delta and would report "nothing drawn". The RGB channels of the same pixels
# differ by 18/25, which is what makes the presence check meaningful. This is
# the same metric the Go certification oracle uses.
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


center = (W // 2 - 330, H // 2 - 330, W // 2 + 330, H // 2 + 330)
reference = frame_rgb(0, "background")

for gap in sc["gap_frames"]:
    leaked = diff(reference, frame_rgb(gap, "gap"))
    print(f"gap frame {gap}: {leaked} px differ from background")
    if leaked != 0:
        fail(f"frame {gap} differs from the background-only frame in {leaked} px: an overlay leaked past its window")

entry_frames = {}
for item in sc["items"]:
    frame = item["mid_frame"]
    region = center if item["center_only"] else None
    changed = diff(reference, frame_rgb(frame, item["name"]), region)
    where = "centre box" if region else "full frame"
    print(f"{item['name']}: preset={item['preset']} frame={frame} changed={changed} px ({where})")
    if changed < item["min_diff_pixels"]:
        fail(f"{item['name']} (preset {item['preset']}): only {changed} px changed at frame {frame}, "
             f"want >= {item['min_diff_pixels']} — the overlay is not visibly drawn")
    entry_frames[item["name"]] = frame_rgb(item["entry_frame"], item["name"] + "-entry")

names = list(entry_frames)
for i in range(len(names)):
    for j in range(i + 1, len(names)):
        if diff(entry_frames[names[i]], entry_frames[names[j]]) == 0:
            fail(f"entrance frames for {names[i]} and {names[j]} are identical: two items rendered the same picture")

if failures:
    print(f"\nFAILED: {len(failures)} assertion(s)", file=sys.stderr)
    sys.exit(1)
print("\nPASS: 2 phrase overlays + 2 entity-image overlays rendered in their own windows, background preserved between them")
PY

echo "artefact: ${OUT_MP4}"
