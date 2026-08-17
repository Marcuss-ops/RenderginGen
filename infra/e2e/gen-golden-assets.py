#!/usr/bin/env python3
"""Generate the deterministic assets for GoldenOverlayJobV1.

Writes:
  testdata/golden/background.jpg  (1280x720 solid gradient + shapes)
  testdata/golden/apple.png       (300x300 stylised red apple on transparency)

Verifies:
  testdata/golden/DejaVuSans.ttf (vendored font used by the text layers)

The generator is fully deterministic (no randomness, fixed seed where a
random-looking pattern is needed), so the SHA-256 content hashes baked into
testdata/golden/golden-overlay-job-v1.json stay stable across machines and
regenerations. Do not change the drawing code without regenerating the job
file and updating the hashes — that would break the golden's immutability.

The font is NOT generated content: it is the system DejaVuSans (vendored
byte-identical into testdata/golden/) and its hash is pinned here so a
regeneration cannot silently drop or replace it. The text layers of the
canonical payload declare it; Chronon's implicit default (Poppins-Bold) is
not part of the job assets and would fail the render.

Requires Pillow.
"""

import hashlib
import os

from PIL import Image, ImageDraw

WIDTH, HEIGHT = 1280, 720
APPLE_SIZE = 300

HERE = os.path.dirname(os.path.abspath(__file__))
OUT_DIR = os.path.normpath(os.path.join(HERE, "..", "..", "testdata", "golden"))

# Vendored font used by the golden's text layers (see module docstring).
FONT_FILENAME = "DejaVuSans.ttf"
FONT_SHA256 = "690243adfefe0ce154b547db6205794bd30ac4277275179517a90994f4980648"


def gradient_background() -> Image.Image:
    """Dark blue -> purple vertical gradient with a soft horizontal band."""
    img = Image.new("RGB", (WIDTH, HEIGHT))
    px = img.load()
    for y in range(HEIGHT):
        t = y / (HEIGHT - 1)
        # top: (12, 16, 40)  bottom: (58, 26, 92)
        r = int(12 + (58 - 12) * t)
        g = int(16 + (26 - 16) * t)
        b = int(40 + (92 - 40) * t)
        for x in range(WIDTH):
            px[x, y] = (r, g, b)
    draw = ImageDraw.Draw(img)
    # A subtle lighter band across the middle third, like a horizon glow.
    for y in range(HEIGHT // 3, 2 * HEIGHT // 3):
        t = (y - HEIGHT // 3) / (HEIGHT // 3)
        lift = int(18 * (1 - abs(2 * t - 1)))
        for x in range(0, WIDTH, 2):
            r, g, b = img.getpixel((x, y))
            img.putpixel((x, y), (min(255, r + lift), min(255, g + lift), min(255, b + lift)))
    # Deterministic starfield dots (fixed positions, no RNG dependency).
    stars = [
        (120, 90), (310, 60), (520, 130), (760, 50), (980, 110), (1180, 80),
        (200, 250), (640, 210), (900, 260), (60, 380), (1150, 330), (430, 480),
        (700, 520), (100, 600), (880, 610), (1230, 560), (350, 640), (820, 680),
    ]
    for sx, sy in stars:
        draw.ellipse([sx - 2, sy - 2, sx + 2, sy + 2], fill=(220, 220, 235))
    return img


def apple_overlay() -> Image.Image:
    """300x300 RGBA: stylised red apple (body, stem, leaf) on transparency."""
    img = Image.new("RGBA", (APPLE_SIZE, APPLE_SIZE), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)
    cx, cy, radius = APPLE_SIZE // 2, APPLE_SIZE // 2 + 10, 110
    # Body: two overlapping circles + a bottom arc for the classic apple shape.
    draw.ellipse([cx - radius, cy - radius, cx + radius, cy + radius], fill=(200, 40, 40, 255))
    draw.ellipse([cx - radius + 18, cy - radius - 18, cx + radius + 18, cy + radius - 18],
                 fill=(180, 34, 34, 255))
    # Highlight.
    draw.ellipse([cx - 60, cy - 60, cx - 20, cy - 20], fill=(235, 110, 110, 200))
    # Stem.
    draw.line([(cx, cy - radius + 10), (cx + 4, cy - radius - 34)], fill=(90, 60, 30, 255), width=10)
    # Leaf.
    draw.ellipse([cx + 6, cy - radius - 44, cx + 52, cy - radius - 12], fill=(70, 140, 60, 255))
    return img


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def main() -> None:
    os.makedirs(OUT_DIR, exist_ok=True)
    background = gradient_background()
    apple = apple_overlay()

    bg_path = os.path.join(OUT_DIR, "background.jpg")
    apple_path = os.path.join(OUT_DIR, "apple.png")
    background.save(bg_path, "JPEG", quality=90)
    apple.save(apple_path, "PNG")

    bg_hash = sha256_bytes(open(bg_path, "rb").read())
    apple_hash = sha256_bytes(open(apple_path, "rb").read())
    print(f"background.jpg sha256={bg_hash} ({os.path.getsize(bg_path)} bytes)")
    print(f"apple.png      sha256={apple_hash} ({os.path.getsize(apple_path)} bytes)")
    print(f"wrote {bg_path}")
    print(f"wrote {apple_path}")

    # The font is vendored, not regenerated: verify it is present and
    # byte-identical to the pinned hash (never rewrite it silently).
    font_path = os.path.join(OUT_DIR, FONT_FILENAME)
    if not os.path.isfile(font_path):
        raise SystemExit(f"ERROR: {font_path} missing — the golden payload declares "
                         f"assets/fonts/{FONT_FILENAME}; copy the system DejaVuSans.ttf "
                         f"(sha256 {FONT_SHA256}) into testdata/golden/")
    font_hash = sha256_bytes(open(font_path, "rb").read())
    if font_hash != FONT_SHA256:
        raise SystemExit(f"ERROR: {font_path} sha256={font_hash} != pinned {FONT_SHA256} — "
                         f"do not swap the golden font")
    print(f"{FONT_FILENAME:15s} sha256={font_hash} ({os.path.getsize(font_path)} bytes) — vendored, OK")


if __name__ == "__main__":
    main()
