#!/usr/bin/env python3
"""Generate the deterministic assets for GoldenOverlayJobV2 (the universal
benchmark job).

Writes:
  testdata/golden/background.mp4     (8s 1280x720@30 animated gradient video)
  testdata/golden/overlay_globe.png  (300x300 RGBA stylised globe)
  testdata/golden/overlay_chart.png  (300x300 RGBA stylised rising chart)
  testdata/golden/logo_pulse.png     (180x180 RGBA stylised pulse logo)

Verifies:
  testdata/golden/DejaVuSans.ttf (vendored font used by the text layers)

The generator is fully deterministic (no randomness; every pixel is a pure
function of its coordinates and the frame index), so the SHA-256 content
hashes baked into testdata/golden/golden-overlay-job-v2.json stay stable
across regenerations on the same encoder. Do not change the drawing or
encoding code without regenerating the job file and updating the hashes —
that would break the golden's immutability.

The background is a VIDEO layer (the Video primitive of the benchmark): it
exercises Chronon's video decode path, not just image compositing.

Requires Pillow and ffmpeg (with libx264).
"""

import hashlib
import math
import os
import subprocess
import sys

from PIL import Image, ImageDraw

WIDTH, HEIGHT, FPS, DURATION_SECONDS = 1280, 720, 30, 8
FRAMES = FPS * DURATION_SECONDS  # 240

GLOBE_SIZE = 300
CHART_SIZE = 300
LOGO_SIZE = 180

HERE = os.path.dirname(os.path.abspath(__file__))
OUT_DIR = os.path.normpath(os.path.join(HERE, "..", "..", "testdata", "golden"))

FONT_FILENAME = "DejaVuSans.ttf"
FONT_SHA256 = "690243adfefe0ce154b547db6205794bd30ac4277275179517a90994f4980648"


def background_frame(frame: int) -> Image.Image:
    """One 1280x720 RGB frame of the animated benchmark background.

    Pure function of (x, y, frame): a rotating diagonal hue wash, a drifting
    sine band and a starfield moving one pixel per frame (wrapping), so the
    full clip is deterministic and visibly animated.
    """
    img = Image.new("RGB", (WIDTH, HEIGHT))
    px = img.load()
    phase = 2.0 * math.pi * frame / FRAMES
    for y in range(HEIGHT):
        t = y / (HEIGHT - 1)
        # Rotating hue: base gradient shifts with the frame.
        shift = 0.5 + 0.5 * math.sin(phase + t * 2.0)
        r = int(10 + 90 * shift)
        g = int(14 + 60 * (1 - t) + 30 * shift)
        b = int(30 + 100 * t + 40 * (1 - shift))
        for x in range(WIDTH):
            px[x, y] = (r, g, b)
    draw = ImageDraw.Draw(img)
    # Drifting sine band (a moving "horizon glow").
    for y in range(HEIGHT):
        offset = int(40 * math.sin(phase + y / 40.0))
        x = (WIDTH // 2 + offset) % WIDTH
        draw.ellipse([x - 90, y - 6, x + 90, y + 6], fill=(120, 90, 200))
    # Starfield drifting one pixel per frame (deterministic wrap).
    stars = [
        (120, 90), (310, 60), (520, 130), (760, 50), (980, 110), (1180, 80),
        (200, 250), (640, 210), (900, 260), (60, 380), (1150, 330), (430, 480),
        (700, 520), (100, 600), (880, 610), (1230, 560), (350, 640), (820, 680),
    ]
    for sx, sy in stars:
        x = (sx + frame) % WIDTH
        draw.ellipse([x - 2, sy - 2, x + 2, sy + 2], fill=(225, 225, 240))
    return img


def encode_background_video(frames_dir: str) -> str:
    """Encode the deterministic frames into background.mp4 via a rawvideo
    pipe to ffmpeg (libx264, yuv420p, fixed settings → deterministic bytes
    for a given encoder build). Returns the output path."""
    out_path = os.path.join(OUT_DIR, "background.mp4")
    cmd = [
        "ffmpeg", "-y", "-loglevel", "error",
        "-f", "rawvideo", "-pix_fmt", "rgb24",
        "-s", f"{WIDTH}x{HEIGHT}", "-r", str(FPS), "-i", "-",
        "-c:v", "libx264", "-pix_fmt", "yuv420p",
        "-crf", "18", "-preset", "medium",
        "-movflags", "+faststart",
        out_path,
    ]
    proc = subprocess.Popen(cmd, stdin=subprocess.PIPE)
    for frame in range(FRAMES):
        proc.stdin.write(background_frame(frame).tobytes())
    proc.stdin.close()
    if proc.wait() != 0:
        raise SystemExit("ERROR: ffmpeg failed to encode background.mp4")
    return out_path


def globe_overlay() -> Image.Image:
    """300x300 RGBA: stylised globe (blue sphere, latitude/longitude arcs,
    highlight) on transparency."""
    img = Image.new("RGBA", (GLOBE_SIZE, GLOBE_SIZE), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)
    cx = cy = GLOBE_SIZE // 2
    r = 120
    draw.ellipse([cx - r, cy - r, cx + r, cy + r], fill=(40, 110, 200, 255))
    draw.ellipse([cx - r + 14, cy - r + 14, cx + r - 14, cy + r - 14], fill=(70, 150, 230, 255))
    # Latitude arcs.
    for k in (-2, -1, 0, 1, 2):
        dy = k * 34
        draw.arc([cx - r, cy - r + dy, cx + r, cy + r + dy], 0, 360, fill=(30, 80, 150, 200), width=4)
    # Longitude ellipses.
    for k in (-1, 0, 1):
        shrink = 40 * (1 - abs(k))
        draw.ellipse([cx - r + shrink * k, cy - r + shrink * (1 - abs(k)),
                      cx + r + shrink * k, cy + r - shrink * (1 - abs(k))],
                     outline=(30, 80, 150, 200), width=4)
    # Highlight.
    draw.ellipse([cx - 70, cy - 70, cx - 25, cy - 25], fill=(200, 230, 255, 160))
    return img


def chart_overlay() -> Image.Image:
    """300x300 RGBA: stylised rising bar chart with an arrow, on
    transparency."""
    img = Image.new("RGBA", (CHART_SIZE, CHART_SIZE), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)
    base_y = 230
    bars = [(60, 120, (90, 200, 90)), (110, 150, (60, 170, 220)),
            (160, 90, (240, 200, 60)), (210, 50, (240, 120, 60))]
    for x, h, color in bars:
        draw.rounded_rectangle([x, base_y - h, x + 32, base_y], radius=8, fill=color)
    draw.line([(30, base_y), (270, base_y)], fill=(200, 200, 220, 255), width=5)
    # Upward arrow.
    draw.line([(200, 30), (250, 30)], fill=(255, 255, 255, 255), width=6)
    draw.polygon([(250, 22), (266, 30), (250, 38)], fill=(255, 255, 255, 255))
    return img


def logo_pulse() -> Image.Image:
    """180x180 RGBA: stylised pulse/heartbeat logo on transparency."""
    img = Image.new("RGBA", (LOGO_SIZE, LOGO_SIZE), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)
    cx, cy = LOGO_SIZE // 2, LOGO_SIZE // 2
    # Rounded badge.
    draw.rounded_rectangle([10, 10, LOGO_SIZE - 10, LOGO_SIZE - 10], radius=36, fill=(40, 40, 70, 255))
    # Heartbeat polyline.
    points = [
        (30, cy), (62, cy), (76, cy - 40), (92, cy + 40), (104, cy), (150, cy),
    ]
    draw.line(points, fill=(255, 90, 90, 255), width=10, joint="curve")
    draw.line([(150, cy), (158, cy)], fill=(255, 90, 90, 255), width=10)
    return img


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def main() -> None:
    os.makedirs(OUT_DIR, exist_ok=True)

    bg_path = encode_background_video(OUT_DIR)
    globe_path = os.path.join(OUT_DIR, "overlay_globe.png")
    chart_path = os.path.join(OUT_DIR, "overlay_chart.png")
    logo_path = os.path.join(OUT_DIR, "logo_pulse.png")
    globe_overlay().save(globe_path, "PNG")
    chart_overlay().save(chart_path, "PNG")
    logo_pulse().save(logo_path, "PNG")

    for path in (bg_path, globe_path, chart_path, logo_path):
        data = open(path, "rb").read()
        print(f"{os.path.basename(path):22s} sha256={sha256_bytes(data)} ({len(data)} bytes)")

    font_path = os.path.join(OUT_DIR, FONT_FILENAME)
    if not os.path.isfile(font_path):
        raise SystemExit(f"ERROR: {font_path} missing — the golden payload declares "
                         f"assets/fonts/{FONT_FILENAME}; copy the system DejaVuSans.ttf "
                         f"(sha256 {FONT_SHA256}) into testdata/golden/")
    font_hash = sha256_bytes(open(font_path, "rb").read())
    if font_hash != FONT_SHA256:
        raise SystemExit(f"ERROR: {font_path} sha256={font_hash} != pinned {FONT_SHA256} — "
                         f"do not swap the golden font")
    print(f"{FONT_FILENAME:22s} sha256={font_hash} — vendored, OK")

    print(f"\nwrote fixtures to {OUT_DIR}")
    print("update the hashes in testdata/golden/golden-overlay-job-v2.json "
          "if any of the above changed")


if __name__ == "__main__":
    main()
