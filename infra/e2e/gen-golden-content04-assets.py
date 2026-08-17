#!/usr/bin/env python3
"""Generate the deterministic light-leak fixtures for GOLDEN 04 (Content04).

Writes:
  testdata/golden/light_leak_01.mp4  (2.0s 1280x720@30 warm sweeping band on black)
  testdata/golden/light_leak_02.mp4  (1.2s 1280x720@30 cool pulsing flare on black)

Every pixel is a pure function of (x, y, frame), so the SHA-256 content hashes
baked into the GOLDEN 04 job stay stable for a given encoder build. The clips
are black-background light streaks intended for Chronon's screen/add blend:
black composites to transparent, so only the bright streaks show over the
background layer.

Requires numpy and ffmpeg (libx264).
"""

import hashlib
import os
import subprocess

import numpy as np

WIDTH, HEIGHT, FPS = 1280, 720, 30

HERE = os.path.dirname(os.path.abspath(__file__))
OUT_DIR = os.path.normpath(os.path.join(HERE, "..", "..", "testdata", "golden"))


def leak01_frame(frame, frames):
    """Warm vertical band sweeping left->right plus a radial glow, on black."""
    x_c = WIDTH * frame / (frames - 1)
    y_c = HEIGHT * 0.45
    ys = np.arange(HEIGHT, dtype=np.float32)[:, None]
    xs = np.arange(WIDTH, dtype=np.float32)[None, :]
    band = np.exp(-((xs - x_c) ** 2) / (2 * 90.0 ** 2))
    glow = np.exp(-((xs - x_c) ** 2 + (ys - y_c) ** 2) / (2 * 220.0 ** 2))
    falloff = np.exp(-((ys - y_c) ** 2) / (2 * 260.0 ** 2))
    intensity = np.clip((band + glow) * falloff * 1.4, 0.0, 1.0)
    return _rgb(intensity, 255.0, 220.0, 160.0)


def leak02_frame(frame, frames):
    """Cool radial flare that expands and fades outward, on black."""
    cx, cy = WIDTH * 0.62, HEIGHT * 0.40
    t = frame / (frames - 1)
    radius = 60.0 + 260.0 * t
    ys = np.arange(HEIGHT, dtype=np.float32)[:, None]
    xs = np.arange(WIDTH, dtype=np.float32)[None, :]
    d2 = (xs - cx) ** 2 + (ys - cy) ** 2
    d = np.sqrt(d2)
    ring = np.exp(-((d - radius) ** 2) / (2 * 40.0 ** 2))
    core = np.exp(-d2 / (2 * 120.0 ** 2))
    fade = 1.0 - 0.35 * t
    intensity = np.clip((ring + core) * fade * 1.2, 0.0, 1.0)
    return _rgb(intensity, 200.0, 220.0, 255.0)


def _rgb(intensity, r, g, b):
    """(H, W) float intensity in [0,1] -> (H, W, 3) uint8 RGB frame."""
    return np.stack(
        [
            (r * intensity).astype(np.uint8),
            (g * intensity).astype(np.uint8),
            (b * intensity).astype(np.uint8),
        ],
        axis=-1,
    )


def encode(name, frames, frame_fn):
    """Encode deterministic frames into name via a rawvideo pipe to ffmpeg."""
    out_path = os.path.join(OUT_DIR, name)
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
    for i in range(frames):
        proc.stdin.write(frame_fn(i, frames).tobytes())
    proc.stdin.close()
    if proc.wait() != 0:
        raise SystemExit(f"ERROR: ffmpeg failed to encode {name}")
    return out_path


def main():
    os.makedirs(OUT_DIR, exist_ok=True)
    leak1 = encode("light_leak_01.mp4", 60, leak01_frame)   # 2.0s
    leak2 = encode("light_leak_02.mp4", 36, leak02_frame)   # 1.2s
    for path in (leak1, leak2):
        data = open(path, "rb").read()
        print(f"{os.path.basename(path):22s} sha256={hashlib.sha256(data).hexdigest()} ({len(data)} bytes)")


if __name__ == "__main__":
    main()
