#!/usr/bin/env python3
"""Render six 1920x1080 / 30 fps cinematic geometry demos.

Each clip is a renderer-side demonstration of one mechanic from the
ChrononMotion3D acceptance suite.  No text, no external assets: every plate is
procedural and deterministic.  Output is H.264/yuv420p, at most 10 seconds.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import subprocess
import sys
import time
from pathlib import Path

import cv2
import numpy as np
from PIL import Image, ImageDraw

cv2.setNumThreads(max(1, (os.cpu_count() or 4) // 2))

W, H = 1920, 1080
FPS = 30
F = 1500.0  # pinhole focal length in pixels for the 2.5D plates

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[2]
UPLOADER = REPO / "RenderingGen" / "bin" / "drive-upload"
CREDENTIALS = REPO / "RenderingGen" / "UploadDrive" / "credentials.json"
TOKEN = REPO / "RenderingGen" / "UploadDrive" / "token.json"
DRIVE_FOLDER = "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
DRIVE_SUBFOLDER = "ChrononMotion3D_Six_Mechanics_1920x1080"

CLIPS = [
    ("01_2p5d_parallax_1920x1080_8s.mp4", 8.0, "2.5D multiplane parallax + Lissajous hover"),
    ("02_spline_network_1920x1080_8s.mp4", 8.0, "Bezier node network, stroke trim, emission burst"),
    ("03_badge_spring_1920x1080_8s.mp4", 8.0, "Spring badge, bokeh background, soft shadow"),
    ("04_deckled_desk_1920x1080_8s.mp4", 8.0, "Deckled paper, landing photo, contact-shadow collapse"),
    ("05_ink_wipe_1920x1080_6s.mp4", 6.0, "Ink-splash threshold wipe"),
    ("06_dust_field_1920x1080_8s.mp4", 8.0, "350-particle curl-noise dust field with bokeh and wrap"),
]


# ---------------------------------------------------------------- colour/type
def B(hex_or_rgb, alpha: float = 1.0) -> np.ndarray:
    """Hex/RGB triple -> float BGR(A) vector."""
    if isinstance(hex_or_rgb, str):
        h = hex_or_rgb.lstrip("#")
        if len(h) == 3:
            h = "".join(ch * 2 for ch in h)
        r, g, b = (int(h[i:i + 2], 16) / 255.0 for i in (0, 2, 4))
        return np.array([b, g, r, alpha], np.float32)
    if len(hex_or_rgb) == 3:
        return np.array([hex_or_rgb[2], hex_or_rgb[1], hex_or_rgb[0]], np.float32)
    return np.asarray(hex_or_rgb, np.float32)


def clamp01(x):
    return np.clip(x, 0.0, 1.0)


def smoothstep(a: float, b: float, x: float) -> float:
    if b == a:
        return 0.0 if x < a else 1.0
    t = min(1.0, max(0.0, (x - a) / (b - a)))
    return t * t * (3.0 - 2.0 * t)


def ease_out_cubic(x: float) -> float:
    x = min(1.0, max(0.0, x))
    return 1.0 - (1.0 - x) ** 3


def vgrad(top, bottom) -> np.ndarray:
    top, bottom = B(top), B(bottom)
    t = np.linspace(0.0, 1.0, H, dtype=np.float32)[:, None, None]
    img = top[None, None, :3] * (1.0 - t) + bottom[None, None, :3] * t
    return np.broadcast_to(img, (H, W, 3)).astype(np.float32).copy()


def blur_fast(img: np.ndarray, sigma: float) -> np.ndarray:
    if sigma <= 0:
        return img
    h, w = img.shape[:2]
    scale = min(1.0, 520.0 / max(h, w))
    if scale < 1.0:
        small = cv2.resize(img, (max(1, int(w * scale)), max(1, int(h * scale))),
                           interpolation=cv2.INTER_AREA)
        small = cv2.GaussianBlur(small, (0, 0), sigma * scale)
        return cv2.resize(small, (w, h), interpolation=cv2.INTER_LINEAR)
    return cv2.GaussianBlur(img, (0, 0), sigma)


def rounded_mask(w: int, h: int, r: int, ss: int = 3) -> np.ndarray:
    im = Image.new("L", (w * ss, h * ss), 0)
    d = ImageDraw.Draw(im)
    d.rounded_rectangle((0, 0, w * ss - 1, h * ss - 1), radius=max(1, r * ss), fill=255)
    im = im.resize((w, h), Image.LANCZOS)
    return np.asarray(im, np.float32) / 255.0


def make_shadow(rgba: np.ndarray, sigma: float, opacity: float, pad: int) -> np.ndarray:
    h, w = rgba.shape[:2]
    a = blur_fast(rgba[..., 3], sigma)
    out = np.zeros((h + 2 * pad, w + 2 * pad, 4), np.float32)
    out[pad:pad + h, pad:pad + w, 3] = a * opacity
    return out


def put_rgba(canvas: np.ndarray, img: np.ndarray, cx: float, cy: float,
             scale: float = 1.0, angle: float = 0.0, opacity: float = 1.0) -> None:
    if not math.isfinite(scale) or scale <= 0.02 or scale > 8.0:
        return
    h, w = img.shape[:2]
    if abs(scale - 1.0) > 1e-5 or abs(angle) > 1e-5:
        m = cv2.getRotationMatrix2D((w * 0.5, h * 0.5), angle, scale)
        c, s = abs(m[0, 0]), abs(m[0, 1])
        nw, nh = int(math.ceil(w * c + h * s)), int(math.ceil(h * c + w * s))
        m[0, 2] += nw * 0.5 - w * 0.5
        m[1, 2] += nh * 0.5 - h * 0.5
        img = cv2.warpAffine(img, m, (nw, nh), flags=cv2.INTER_LINEAR,
                             borderMode=cv2.BORDER_CONSTANT,
                             borderValue=(0.0, 0.0, 0.0, 0.0))
    h, w = img.shape[:2]
    x0, y0 = int(round(cx - w * 0.5)), int(round(cy - h * 0.5))
    x1, y1 = x0 + w, y0 + h
    sx0, sy0 = max(0, -x0), max(0, -y0)
    dx0, dy0 = max(0, x0), max(0, y0)
    dx1, dy1 = min(W, x1), min(H, y1)
    if dx1 <= dx0 or dy1 <= dy0:
        return
    src = img[sy0:sy0 + (dy1 - dy0), sx0:sx0 + (dx1 - dx0)]
    a = src[..., 3:4] * opacity
    dst = canvas[dy0:dy1, dx0:dx1]
    dst[:] = dst * (1.0 - a) + src[..., :3] * a


def add_layer(canvas: np.ndarray, layer: np.ndarray, alpha: float = 1.0) -> None:
    canvas[:] = clamp01(canvas + layer * alpha)


def draw_polyline(pts, color, width: int, alpha: float, sigma: float = 0.0) -> np.ndarray:
    layer = np.zeros((H, W, 3), np.uint8)
    if len(pts) >= 2:
        arr = np.asarray(pts, np.int32).reshape(-1, 1, 2)
        cv2.polylines(layer, [arr], False,
                      tuple(int(255 * v) for v in B(color)[:3]), max(1, width), cv2.LINE_AA)
    out = layer.astype(np.float32) / 255.0 * alpha
    if sigma > 0:
        out = blur_fast(out, sigma)
    return out


def draw_circle(cx: float, cy: float, radius: float, color, alpha: float,
                thickness: int = -1, sigma: float = 0.0) -> np.ndarray:
    layer = np.zeros((H, W, 3), np.uint8)
    cv2.circle(layer, (int(round(cx)), int(round(cy))), max(1, int(round(radius))),
               tuple(int(255 * v) for v in B(color)[:3]), thickness, cv2.LINE_AA)
    out = layer.astype(np.float32) / 255.0 * alpha
    if sigma > 0:
        out = blur_fast(out, sigma)
    return out


def vignette_mask() -> np.ndarray:
    yy, xx = np.mgrid[0:H, 0:W].astype(np.float32)
    r = np.sqrt(((xx - W * 0.5) / (W * 0.5)) ** 2 + ((yy - H * 0.5) / (H * 0.5)) ** 2)
    return np.clip(1.03 - 0.30 * clamp01((r - 0.30) / 0.70) ** 2, 0.70, 1.03)


VIGNETTE = vignette_mask()


def finish(frame: np.ndarray, rng: np.random.Generator, fade: float = 1.0,
           grain: float = 0.006) -> np.ndarray:
    if grain:
        frame = frame + rng.normal(0.0, grain, frame.shape).astype(np.float32)
    frame = clamp01(frame * VIGNETTE[..., None] * fade)
    return (frame * 255.0 + 0.5).astype(np.uint8)


def fbm1(n: int, rng: np.random.Generator, octaves: int = 4, base: int = 12) -> np.ndarray:
    x = np.linspace(0.0, 1.0, n, endpoint=False)
    out = np.zeros(n, np.float32)
    amp, freq = 1.0, base
    for _ in range(octaves):
        pts = rng.normal(size=freq + 1).astype(np.float32)
        vals = np.interp(x, np.linspace(0.0, 1.0, freq + 1), pts).astype(np.float32)
        out += amp * vals
        amp *= 0.5
        freq *= 2
    return out / max(1e-6, float(np.max(np.abs(out))))


# === ASSET BUILDERS ===
def make_card(w: int, h: int, c1: str, c2: str, kind: int, seed: int):
    """A glass product card with a procedural abstract glyph and a baked shadow."""
    ss = 2
    Wc, Hc = w * ss, h * ss
    rng = np.random.default_rng(seed)
    mask = rounded_mask(Wc, Hc, 28 * ss, ss=2)
    yy, xx = np.mgrid[0:Hc, 0:Wc].astype(np.float32)
    t = np.clip(0.64 * yy / Hc + 0.36 * xx / Wc, 0.0, 1.0)
    a, b = B(c1)[:3], B(c2)[:3]
    base = a[None, None, :] * (1.0 - t[..., None]) + b[None, None, :] * t[..., None]
    sheen = np.exp(-((xx / Wc * 0.9 + yy / Hc * 0.35 - 0.44) / 0.19) ** 2)
    base = base + sheen[..., None] * 0.17
    noise = rng.normal(0.0, 1.0, (Hc // 10 + 1, Wc // 10 + 1)).astype(np.float32)
    noise = cv2.resize(noise, (Wc, Hc), interpolation=cv2.INTER_CUBIC)
    base = base + noise[..., None] * 0.010

    icon = Image.new("L", (Wc, Hc), 0)
    d = ImageDraw.Draw(icon)
    cx, cy, rad = Wc // 2, int(Hc * 0.42), int(min(Wc, Hc) * 0.23)
    lw = max(2, 5 * ss)
    if kind == 0:  # nested orbit
        d.ellipse((cx - rad, cy - rad, cx + rad, cy + rad), outline=255, width=lw)
        d.ellipse((cx - rad // 2, cy - rad // 2, cx + rad // 2, cy + rad // 2), outline=255, width=lw)
        d.ellipse((cx - 4 * ss, cy - 4 * ss, cx + 4 * ss, cy + 4 * ss), fill=255)
    elif kind == 1:  # capsule / product silhouette
        d.rounded_rectangle((cx - rad, cy - rad // 2, cx + rad, cy + rad // 2),
                            radius=rad // 2, outline=255, width=lw)
        d.line((cx, cy - rad // 2, cx, cy + rad // 2), fill=255, width=lw)
    elif kind == 2:  # triangular prism
        d.polygon([(cx, cy - rad), (cx + rad, cy + rad), (cx - rad, cy + rad)],
                  outline=255, width=lw)
        d.line((cx, cy - rad, cx, cy + rad), fill=255, width=lw)
    else:  # wave / fluid mark
        pts = []
        for i in range(9):
            px = cx - rad + int(i * 2 * rad / 8)
            py = cy + int(rad * 0.25 * math.sin(i * 0.9))
            pts.append((px, py))
        d.line(pts, fill=255, width=lw, joint="curve")
        d.ellipse((cx + rad - 6 * ss, cy - rad - 6 * ss,
                   cx + rad + 6 * ss, cy - rad + 6 * ss), fill=255)
    ia = np.asarray(icon, np.float32) / 255.0
    glyph = np.array(B("#f7efdf")[:3], np.float32)
    rgba = np.zeros((Hc, Wc, 4), np.float32)
    rgba[..., :3] = clamp01(base) * (1.0 - ia[..., None] * 0.88) + glyph * ia[..., None] * 0.88
    rgba[..., 3] = mask

    inner = rounded_mask(Wc - 12 * ss, Hc - 12 * ss, 23 * ss, ss=2)
    inner_full = np.zeros_like(mask)
    pad = 6 * ss
    inner_full[pad:pad + inner.shape[0], pad:pad + inner.shape[1]] = inner
    border = clamp01(mask - inner_full)
    rgba[..., :3] = clamp01(rgba[..., :3] + border[..., None] * 0.20)
    rgba = cv2.resize(rgba, (w, h), interpolation=cv2.INTER_AREA)
    return rgba, make_shadow(rgba, 17.0, 0.55, 65)


def make_subject(w: int = 560, h: int = 720):
    """A glass orb on a plinth: the fixed centre of the multiplane scene."""
    ss = 2
    Wc, Hc = w * ss, h * ss
    yy, xx = np.mgrid[0:Hc, 0:Wc].astype(np.float32)
    mask = np.zeros((Hc, Wc), np.uint8)
    cv2.ellipse(mask, (Wc // 2, int(Hc * 0.40)), (int(Wc * 0.29), int(Hc * 0.28)),
                0, 0, 360, 255, -1)
    cv2.rectangle(mask, (int(Wc * 0.43), int(Hc * 0.58)),
                  (int(Wc * 0.57), int(Hc * 0.91)), 255, -1)
    cv2.ellipse(mask, (Wc // 2, int(Hc * 0.91)), (int(Wc * 0.33), int(Hc * 0.045)),
                0, 0, 360, 255, -1)
    maskf = cv2.GaussianBlur(mask.astype(np.float32) / 255.0, (0, 0), 1.2 * ss)

    rr = np.sqrt(((xx - Wc * 0.45) / (Wc * 0.46)) ** 2 +
                 ((yy - Hc * 0.34) / (Hc * 0.46)) ** 2)
    dark = np.array(B("#07131a")[:3], np.float32)
    warm = np.array(B("#d39a52")[:3], np.float32)
    base = dark[None, None, :] + (warm - dark)[None, None, :] * clamp01(1.0 - rr)[..., None]
    base += np.exp(-((xx / Wc - 0.34) ** 2 + (yy / Hc - 0.26) ** 2) / 0.012)[..., None] * 0.40

    ring = np.zeros((Hc, Wc), np.uint8)
    cv2.ellipse(ring, (Wc // 2, int(Hc * 0.42)), (int(Wc * 0.40), int(Hc * 0.16)),
                -18, 0, 360, 255, max(2, 7 * ss))
    ringf = cv2.GaussianBlur(ring.astype(np.float32) / 255.0, (0, 0), 2.0 * ss)
    ringf = cv2.GaussianBlur(ringf, (0, 0), 8.0 * ss)
    gold = np.array(B("#f0c47a")[:3], np.float32)
    base = base * (1.0 - ringf[..., None] * 0.65) + gold * ringf[..., None] * 0.65

    edge = clamp01(maskf - cv2.erode(maskf, np.ones((5 * ss, 5 * ss), np.uint8)))
    base = base + edge[..., None] * np.array(B("#ffdca0")[:3], np.float32) * 0.45
    rgba = np.zeros((Hc, Wc, 4), np.float32)
    rgba[..., :3] = clamp01(base)
    rgba[..., 3] = maskf
    rgba = cv2.resize(rgba, (w, h), interpolation=cv2.INTER_AREA)
    return rgba, make_shadow(rgba, 28.0, 0.50, 90)


def make_badge_assets():
    """Circular emblem + rounded price pill, both with baked shadows."""
    s = 2
    size = 320 * s
    yy, xx = np.mgrid[0:size, 0:size].astype(np.float32)
    mask = np.zeros((size, size), np.uint8)
    cv2.circle(mask, (size // 2, size // 2), int(size * 0.46), 255, -1)
    maskf = cv2.GaussianBlur(mask.astype(np.float32) / 255.0, (0, 0), 1.2 * s)
    r = np.sqrt(((xx - size / 2) / (size / 2)) ** 2 + ((yy - size / 2) / (size / 2)) ** 2)
    c1 = np.array(B("#15262b")[:3], np.float32)
    c2 = np.array(B("#d5a55c")[:3], np.float32)
    disc = c1[None, None, :] + (c2 - c1)[None, None, :] * clamp01(1.0 - r)[..., None]
    rim = np.zeros((size, size), np.uint8)
    cv2.circle(rim, (size // 2, size // 2), int(size * 0.43), 255, max(2, 5 * s))
    rimf = cv2.GaussianBlur(rim.astype(np.float32) / 255.0, (0, 0), 3.0 * s)
    disc = disc * (1.0 - rimf[..., None] * 0.8) + np.array(B("#ffe2a9")[:3]) * rimf[..., None] * 0.8
    glyph = Image.new("L", (size, size), 0)
    d = ImageDraw.Draw(glyph)
    cx = cy = size // 2
    d.polygon([(cx, cy - 78 * s), (cx + 62 * s, cy), (cx, cy + 78 * s),
               (cx - 62 * s, cy)], outline=255, width=5 * s)
    d.ellipse((cx - 12 * s, cy - 12 * s, cx + 12 * s, cy + 12 * s), fill=255)
    ia = np.asarray(glyph, np.float32) / 255.0
    disc = disc * (1.0 - ia[..., None] * 0.85) + np.array(B("#fff4dc")[:3]) * ia[..., None] * 0.85
    icon = np.dstack([clamp01(disc), maskf])
    icon = cv2.resize(icon, (320, 320), interpolation=cv2.INTER_AREA)

    pw, ph = 480, 120
    Wc, Hc = pw * s, ph * s
    pmask = rounded_mask(Wc, Hc, 34 * s, ss=2)
    yy, xx = np.mgrid[0:Hc, 0:Wc].astype(np.float32)
    t = yy / Hc
    p1 = np.array(B("#14252a")[:3], np.float32)
    p2 = np.array(B("#49392b")[:3], np.float32)
    pill = p1[None, None, :] * (1.0 - t[..., None]) + p2[None, None, :] * t[..., None]
    pg = Image.new("L", (Wc, Hc), 0)
    d = ImageDraw.Draw(pg)
    d.ellipse((24 * s, 34 * s, 76 * s, 86 * s), fill=255)
    d.rounded_rectangle((104 * s, 44 * s, 360 * s, 76 * s), radius=12 * s, fill=255)
    pia = np.asarray(pg, np.float32) / 255.0
    pill = pill * (1.0 - pia[..., None] * 0.7) + np.array(B("#f3d59d")[:3]) * pia[..., None] * 0.7
    pill_rgba = np.dstack([clamp01(pill), pmask])
    pill_rgba = cv2.resize(pill_rgba, (pw, ph), interpolation=cv2.INTER_AREA)
    return ((icon, make_shadow(icon, 20.0, 0.5, 60)),
            (pill_rgba, make_shadow(pill_rgba, 18.0, 0.5, 60)))


def make_doc_background():
    """High-contrast editorial page, then defocused into bokeh."""
    rng = np.random.default_rng(77)
    w, h = 1400, 900
    im = Image.new("RGB", (w, h), (228, 222, 207))
    d = ImageDraw.Draw(im)
    for _ in range(28):
        x, y = rng.integers(0, w), rng.integers(0, h)
        bw, bh = rng.integers(80, 460), rng.integers(8, 34)
        col = tuple(int(v) for v in rng.integers(25, 90, 3))
        d.rectangle((x, y, min(w - 1, x + bw), min(h - 1, y + bh)), fill=col)
    for _ in range(16):
        x, y = rng.integers(0, w), rng.integers(0, h)
        col = tuple(int(v) for v in rng.integers(30, 150, 3))
        d.rectangle((x, y, min(w - 1, x + rng.integers(140, 380)),
                     min(h - 1, y + rng.integers(90, 240))), outline=col, width=3)
    for _ in range(8):
        pts = [(int(rng.integers(0, w)), int(rng.integers(0, h))) for _ in range(4)]
        d.line(pts, fill=(35, 55, 72), width=5, joint="curve")
    arr = np.asarray(im, np.float32)[..., [2, 1, 0]] / 255.0
    arr = blur_fast(arr, 23.0)
    arr = cv2.resize(arr, (W, H), interpolation=cv2.INTER_LINEAR)
    arr = arr * 0.72 + vgrad("#10171c", "#25201b") * 0.28
    bokeh = np.zeros((H, W, 3), np.uint8)
    for _ in range(22):
        x, y = int(rng.integers(0, W)), int(rng.integers(0, H))
        rad = int(rng.integers(12, 55))
        col = (int(rng.integers(120, 255)), int(rng.integers(130, 240)),
               int(rng.integers(150, 255)))
        cv2.circle(bokeh, (x, y), rad, col, -1, cv2.LINE_AA)
    add_layer(arr, bokeh.astype(np.float32) / 255.0, 0.16)
    arr = blur_fast(arr, 3.0)
    return arr


def make_bust():
    """Sharp midground silhouette with a warm rim, no facial detail."""
    ss = 2
    w, h = 760, 880
    Wc, Hc = w * ss, h * ss
    yy, xx = np.mgrid[0:Hc, 0:Wc].astype(np.float32)
    mask = np.zeros((Hc, Wc), np.uint8)
    cv2.ellipse(mask, (Wc // 2, int(Hc * 0.35)), (int(Wc * 0.22), int(Hc * 0.27)),
                0, 0, 360, 255, -1)
    cv2.ellipse(mask, (Wc // 2, int(Hc * 0.90)), (int(Wc * 0.42), int(Hc * 0.34)),
                0, 0, 360, 255, -1)
    maskf = cv2.GaussianBlur(mask.astype(np.float32) / 255.0, (0, 0), 1.2 * ss)
    r = np.sqrt(((xx - Wc * 0.42) / (Wc * 0.55)) ** 2 +
                ((yy - Hc * 0.28) / (Hc * 0.60)) ** 2)
    dark = np.array(B("#080b0e")[:3], np.float32)
    warm = np.array(B("#8d6a44")[:3], np.float32)
    base = dark[None, None, :] + (warm - dark)[None, None, :] * clamp01(1.0 - r)[..., None]
    edge = clamp01(maskf - cv2.erode(maskf, np.ones((4 * ss, 4 * ss), np.uint8)))
    base += edge[..., None] * np.array(B("#efc98d")[:3]) * 0.65
    rgba = np.zeros((Hc, Wc, 4), np.float32)
    rgba[..., :3] = clamp01(base)
    rgba[..., 3] = maskf
    rgba = cv2.resize(rgba, (w, h), interpolation=cv2.INTER_AREA)
    return rgba
def make_desk_background():
    """Dark satin wood, seen from a low three-quarter desk angle."""
    rng = np.random.default_rng(41)
    w, h = 2100, 1300
    yy, xx = np.mgrid[0:h, 0:w].astype(np.float32)
    grain = (0.5 + 0.5 * np.sin(xx * 0.007 + 4.0 * np.sin(yy * 0.004 + xx * 0.001)))
    grain += 0.22 * np.sin(xx * 0.031 + yy * 0.002) + 0.12 * rng.normal(0, 1, (h, w))
    grain = cv2.GaussianBlur(grain.astype(np.float32), (0, 0), 1.2)
    wood = (np.array(B("#160f0b")[:3])[None, None, :] * 0.55 +
            np.array(B("#5a3722")[:3])[None, None, :] * clamp01(grain)[..., None] * 0.55)
    plank = 0.5 + 0.5 * np.sin(xx * 0.012 + 1.4)
    wood *= (0.88 + 0.12 * plank)[..., None]
    wood *= (0.82 + 0.18 * clamp01(1.0 - yy / h))[..., None]
    img = np.zeros((H, W, 3), np.float32)
    src = np.float32([[0, 0], [w, 0], [w, h], [0, h]])
    dst = np.float32([[0, 0], [W, 0], [W, H], [0, H]])
    m = cv2.getPerspectiveTransform(src, dst)
    img = cv2.warpPerspective(wood, m, (W, H), flags=cv2.INTER_AREA)
    light = np.exp(-(((xx - w * 0.28) / (w * 0.62)) ** 2 +
                     ((yy - h * 0.22) / (h * 0.70)) ** 2))
    light = cv2.resize(light, (W, H), interpolation=cv2.INTER_LINEAR)
    img *= (0.62 + 0.50 * light[..., None])
    return clamp01(img * VIGNETTE[..., None])


def make_paper():
    """Aged sheet with four independently torn deckled sides and white fibres."""
    rng = np.random.default_rng(29)
    ss = 2
    w, h = 1500, 900
    Wc, Hc = w * ss, h * ss
    inset = 54 * ss
    amp = 22 * ss
    left = inset + fbm1(Hc, rng, 4, 18) * amp
    right = Wc - inset - fbm1(Hc, rng, 4, 18) * amp
    top = inset + fbm1(Wc, rng, 4, 18) * amp
    bottom = Hc - inset - fbm1(Wc, rng, 4, 18) * amp
    xs = np.arange(Wc, dtype=np.float32)[None, :]
    ys = np.arange(Hc, dtype=np.float32)[:, None]
    mask = ((xs >= left[:, None]) & (xs <= right[:, None]) &
            (ys >= top[None, :]) & (ys <= bottom[None, :])).astype(np.float32)
    mask = cv2.GaussianBlur(mask, (0, 0), 0.8 * ss)

    low = rng.normal(0, 1, (Hc // 12 + 1, Wc // 12 + 1)).astype(np.float32)
    low = cv2.resize(low, (Wc, Hc), interpolation=cv2.INTER_CUBIC)
    low = blur_fast(low, 55.0)
    stains = rng.random((Hc // 40 + 1, Wc // 40 + 1)).astype(np.float32)
    stains = cv2.resize(stains, (Wc, Hc), interpolation=cv2.INTER_CUBIC)
    stains = blur_fast(stains, 18 * ss)
    base = np.array(B("#e2d1b0")[:3], np.float32)[None, None, :]
    paper = base * (0.97 + 0.03 * low[..., None]) * (0.97 + 0.05 * stains[..., None])
    paper *= (0.90 + 0.10 * (0.5 + 0.5 * np.sin(xs * 0.002 + ys * 0.001)))[..., None]

    dist = cv2.distanceTransform((mask > 0.5).astype(np.uint8), cv2.DIST_L2, 5)
    edge_dark = np.exp(-dist / (8.0 * ss))
    paper *= (1.0 - edge_dark[..., None] * 0.20)
    fibre = ((dist > 0.0) & (dist < 4.0 * ss)).astype(np.float32)
    fibre *= clamp01((4.0 * ss - dist) / (2.0 * ss))
    paper = paper * (1.0 - fibre[..., None] * 0.72) + np.array(B("#fff6e4")[:3])[None, None, :] * fibre[..., None] * 0.72
    rgba = np.dstack([clamp01(paper), clamp01(mask)])
    rgba = cv2.resize(rgba, (w, h), interpolation=cv2.INTER_AREA)
    return rgba, make_shadow(rgba, 24.0, 0.48, 90)


def make_photo():
    """A warm archival print with a white border and an abstract landscape."""
    ss = 2
    w, h = 580, 740
    Wc, Hc = w * ss, h * ss
    rng = np.random.default_rng(83)
    b = 26 * ss
    yy, xx = np.mgrid[0:Hc, 0:Wc].astype(np.float32)
    t = clamp01((yy - b) / (Hc - 2 * b))
    sky = (np.array(B("#f2b36e")[:3])[None, None, :] * (1.0 - t[..., None]) +
           np.array(B("#4c4c58")[:3])[None, None, :] * t[..., None])
    inner = sky.copy()
    sun = np.exp(-(((xx - Wc * 0.68) / (Wc * 0.12)) ** 2 +
                   ((yy - Hc * 0.34) / (Hc * 0.10)) ** 2))
    inner += sun[..., None] * np.array(B("#ffe6b0")[:3]) * 0.8
    for k, col in enumerate(("#70605a", "#4a4140", "#29292d")):
        pts = np.array([(0, Hc * (0.62 + k * 0.08)),
                        (Wc * 0.24, Hc * (0.48 + k * 0.08)),
                        (Wc * 0.46, Hc * (0.60 + k * 0.08)),
                        (Wc * 0.72, Hc * (0.45 + k * 0.08)),
                        (Wc, Hc * (0.58 + k * 0.08)),
                        (Wc, Hc), (0, Hc)], np.int32)
        cv2.fillPoly(inner, [pts], tuple(float(v) for v in B(col)[:3]))
    inner += rng.normal(0, 0.012, inner.shape).astype(np.float32)
    border_col = np.array(B("#f4efe2")[:3], np.float32)
    canvas = np.ones((Hc, Wc, 3), np.float32) * border_col[None, None, :]
    canvas[b:Hc - b, b:Wc - b] = clamp01(inner[b:Hc - b, b:Wc - b])
    mask = rounded_mask(Wc, Hc, 10 * ss, ss=2)
    rgba = np.dstack([clamp01(canvas), mask])
    rgba = cv2.resize(rgba, (w, h), interpolation=cv2.INTER_AREA)
    return rgba

def make_landscape():
    """Dark cinematic plate used under the ink wipe."""
    rng = np.random.default_rng(101)
    img = vgrad("#13202b", "#050608")
    yy, xx = np.mgrid[0:H, 0:W].astype(np.float32)
    sun = np.exp(-(((xx - W * 0.72) / (W * 0.18)) ** 2 +
                   ((yy - H * 0.28) / (H * 0.16)) ** 2))
    img += sun[..., None] * np.array(B("#c77b3d")[:3]) * 0.55
    for k, col in enumerate(("#1b252d", "#10171d", "#080b0e")):
        pts = np.array([(0, H * (0.56 + k * 0.10)),
                        (W * 0.19, H * (0.44 + k * 0.10)),
                        (W * 0.38, H * (0.57 + k * 0.10)),
                        (W * 0.60, H * (0.40 + k * 0.10)),
                        (W * 0.82, H * (0.55 + k * 0.10)),
                        (W, H * (0.46 + k * 0.10)),
                        (W, H), (0, H)], np.int32)
        layer = np.zeros((H, W), np.uint8)
        cv2.fillPoly(layer, [pts], 255)
        img = img * (1.0 - layer[..., None] * 0.78) + np.array(B(col)[:3])[None, None, :] * layer[..., None] * 0.78
    img += rng.normal(0, 0.010, img.shape).astype(np.float32)
    for _ in range(160):
        x, y = rng.integers(0, W), rng.integers(0, H // 2)
        img[y, x] += rng.uniform(0.15, 0.55)
    return clamp01(img)


def make_solar(base: np.ndarray, ink: np.ndarray) -> np.ndarray:
    """High-key solarized poster with selective red ink accents."""
    g = cv2.cvtColor((clamp01(base) * 255).astype(np.uint8),
                     cv2.COLOR_BGR2GRAY).astype(np.float32) / 255.0
    poster = np.clip((g - 0.30) / 0.28, 0.0, 1.0)
    poster = poster * poster * (3.0 - 2.0 * poster)
    solar = np.repeat(poster[..., None], 3, axis=2) * 0.72 + 0.08
    solar[..., 2] = clamp01(solar[..., 2] + poster * 0.12)
    solar[..., 1] = clamp01(solar[..., 1] + poster * 0.04)
    solar[..., 0] *= 0.92
    red = clamp01((ink - 0.40) * 1.70)
    solar[..., 2] = np.maximum(solar[..., 2], red * 0.72)
    solar[..., 1] *= (1.0 - 0.62 * red)
    solar[..., 0] *= (1.0 - 0.62 * red)
    return clamp01(solar)


def make_ink_map():
    """Radial splash with connected high-gradient filaments and droplets."""
    rng = np.random.default_rng(57)
    yy, xx = np.mgrid[0:H, 0:W].astype(np.float32)
    cx, cy, rx, ry = W * 0.50, H * 0.50, W * 0.46, H * 0.46
    radial = np.exp(-((((xx - cx) / rx) ** 2 + ((yy - cy) / ry) ** 2) * 1.35))
    low = rng.normal(0, 1, (H // 8 + 1, W // 8 + 1)).astype(np.float32)
    low = cv2.resize(low, (W, H), interpolation=cv2.INTER_CUBIC)
    low = blur_fast(low, 1.4)
    low = 0.5 + 0.5 * low / max(1e-6, float(np.max(np.abs(low))))

    branches = np.zeros((H, W), np.uint8)
    for _ in range(62):
        ang = float(rng.uniform(0, 2 * math.pi))
        reach = float(rng.uniform(0.12, 0.54))
        curve = float(rng.uniform(-0.45, 0.45))
        pts = []
        for j in range(16):
            f = j / 15.0
            r = reach * f
            a = ang + curve * f * f
            pts.append((int(cx + math.cos(a) * rx * r),
                        int(cy + math.sin(a) * ry * r)))
        cv2.polylines(branches, [np.asarray(pts, np.int32)], False, 255,
                      int(rng.integers(2, 5)), cv2.LINE_AA)
        cv2.circle(branches, pts[-1], int(rng.integers(3, 14)), 255, -1, cv2.LINE_AA)
    fil = cv2.GaussianBlur(branches.astype(np.float32) / 255.0, (0, 0), 1.15)

    ink = 0.64 * radial + 0.32 * low + 0.86 * fil
    ink += 0.22 * np.exp(-(((xx - cx) / (W * 0.10)) ** 2 +
                           ((yy - cy) / (H * 0.10)) ** 2))
    ink = np.clip(ink, 0.0, None)
    ink *= 1.08 / max(1e-6, float(ink.max()))
    return ink.astype(np.float32)


def make_dust_background():
    """Theatrical dark room with a diagonal blade of light and bokeh."""
    rng = np.random.default_rng(13)
    img = vgrad("#071018", "#020304")
    yy, xx = np.mgrid[0:H, 0:W].astype(np.float32)
    x = (xx - W * 0.5) / W
    y = (yy - H * 0.5) / H
    d = np.abs(x * 0.82 + y * 0.57 - 0.02)
    shaft = np.exp(-(d / 0.23) ** 2)
    shaft *= clamp01(1.0 - 0.55 * (xx / W))
    img += shaft[..., None] * np.array(B("#d9b477")[:3]) * 0.28
    img += np.exp(-((x + 0.42) ** 2 + (y + 0.48) ** 2) / 0.10)[..., None] * np.array(B("#355a6c")[:3]) * 0.30
    bokeh = np.zeros((H, W, 3), np.uint8)
    for _ in range(28):
        px, py = int(rng.integers(0, W)), int(rng.integers(0, H))
        rad = int(rng.integers(18, 86))
        col = (int(rng.integers(100, 220)), int(rng.integers(120, 230)),
               int(rng.integers(150, 255)))
        cv2.circle(bokeh, (px, py), rad, col, -1, cv2.LINE_AA)
    bokeh = bokeh.astype(np.float32) / 255.0
    bokeh = blur_fast(bokeh, 26.0)
    add_layer(img, bokeh, 0.12)
    img += rng.normal(0, 0.005, img.shape).astype(np.float32)
    return clamp01(img)


# === CLIP RENDERERS ===
def make_parallax_bg():
    rng = np.random.default_rng(3)
    yy, xx = np.mgrid[0:H, 0:W].astype(np.float32)
    img = vgrad("#08151d", "#020304")
    glow = np.exp(-(((xx - W * 0.50) / (W * 0.38)) ** 2 +
                    ((yy - H * 0.46) / (H * 0.44)) ** 2))
    img += glow[..., None] * np.array(B("#2b6670")[:3]) * 0.34
    ring = np.exp(-(((np.sqrt(((xx - W * 0.5) / W) ** 2 +
                              ((yy - H * 0.5) / H) ** 2) - 0.28) / 0.012) ** 2))
    img += ring[..., None] * np.array(B("#b98a55")[:3]) * 0.12
    for _ in range(120):
        x, y = int(rng.integers(0, W)), int(rng.integers(0, H))
        img[y, x] += rng.uniform(0.02, 0.10)
    return clamp01(img), glow[..., None] * np.array(B("#e6bb7a")[:3]) * 0.16


def make_spline_bg():
    rng = np.random.default_rng(19)
    yy, xx = np.mgrid[0:H, 0:W].astype(np.float32)
    img = vgrad("#07111d", "#020306")
    grid = np.zeros((H, W, 3), np.uint8)
    for x in range(0, W, 86):
        cv2.line(grid, (x, 0), (x, H), (22, 42, 58), 1)
    for y in range(0, H, 86):
        cv2.line(grid, (0, y), (W, y), (22, 42, 58), 1)
    img += grid.astype(np.float32) / 255.0 * 0.22
    img += np.exp(-(((xx - W * 0.70) / (W * 0.42)) ** 2 +
                    ((yy - H * 0.50) / (H * 0.55)) ** 2))[..., None] * np.array(B("#1c4e69")[:3]) * 0.35
    img += rng.normal(0, 0.004, img.shape).astype(np.float32)
    return clamp01(img)


def bezier(p0, p1, p2, p3, n=240):
    u = np.linspace(0.0, 1.0, n, dtype=np.float32)[:, None]
    p0, p1, p2, p3 = (np.asarray(p, np.float32)[None, :] for p in (p0, p1, p2, p3))
    return ((1 - u) ** 3 * p0 + 3 * (1 - u) ** 2 * u * p1 +
            3 * (1 - u) * u ** 2 * p2 + u ** 3 * p3)


def build_assets_for(name: str):
    """Build only the plates a single clip needs, keeping peak RAM low."""
    a = {}
    if name.startswith("01"):
        a["parallax_bg"], a["parallax_glow"] = make_parallax_bg()
        a["subject"] = make_subject()
        specs = [
            ("#16333d", "#b97742", 0, 600.0, -350.0, -225.0),
            ("#251b31", "#b56b7a", 1, 720.0, 350.0, -230.0),
            ("#182b24", "#b49a59", 2, 840.0, -375.0, 245.0),
            ("#2c1b19", "#c5834c", 3, 960.0, 375.0, 250.0),
        ]
        a["cards"] = []
        for k, (c1, c2, kind, z, x, y) in enumerate(specs):
            rgba, shadow = make_card(300, 420, c1, c2, kind, 10 + k)
            a["cards"].append({"rgba": rgba, "shadow": shadow, "z": z, "x": x,
                               "y": y, "omega": 0.55 + 0.16 * k,
                               "phase": k * math.pi * 0.5})
    elif name.startswith("02"):
        a["spline_bg"] = make_spline_bg()
    elif name.startswith("03"):
        a["doc"] = make_doc_background()
        a["bust"] = make_bust()
        a["badge"], a["pill"] = make_badge_assets()
    elif name.startswith("04"):
        desk = make_desk_background()
        paper, _ = make_paper()
        paper_quad = [(215.0, 115.0), (1690.0, 82.0), (1760.0, 942.0), (170.0, 980.0)]
        paper_full = warp_full(paper, paper_quad)
        shadow_full = np.zeros((H, W, 4), np.float32)
        shadow_full[..., 3] = np.roll(blur_fast(paper_full[..., 3], 28.0), (25, 18), (0, 1)) * 0.55
        desk_paper = desk.copy()
        put_rgba(desk_paper, shadow_full, W * 0.5, H * 0.5)
        put_rgba(desk_paper, paper_full, W * 0.5, H * 0.5)
        a["desk_paper"] = desk_paper
        a["photo"] = make_photo()
    elif name.startswith("05"):
        a["landscape"] = make_landscape()
        a["ink"] = make_ink_map()
        a["solar"] = make_solar(a["landscape"], a["ink"])
    elif name.startswith("06"):
        a["dust_bg"] = make_dust_background()
        a["dust_state"] = make_dust_state()
    return a


def warp_full(rgba: np.ndarray, quad) -> np.ndarray:
    h, w = rgba.shape[:2]
    src = np.float32([[0, 0], [w, 0], [w, h], [0, h]])
    m = cv2.getPerspectiveTransform(src, np.float32(quad))
    return cv2.warpPerspective(rgba, m, (W, H), flags=cv2.INTER_LINEAR,
                               borderMode=cv2.BORDER_CONSTANT,
                               borderValue=(0.0, 0.0, 0.0, 0.0))


def render_parallax(t: float, a) -> np.ndarray:
    move = min(t, 6.0) / 6.0
    camz = 150.0 * smoothstep(0.0, 1.0, move)
    camx = 80.0 * math.sin(2.0 * math.pi * move)
    d = 900.0 - camz
    frame = a["parallax_bg"].copy()
    frame += a["parallax_glow"] * (0.65 + 0.35 * math.sin(t * 0.55))
    sx = W * 0.5 + F * (0.0 - camx) / d
    sy = H * 0.5
    s = 900.0 / d
    sub, sub_sh = a["subject"]
    put_rgba(frame, sub_sh, sx + 22.0 * s, sy + 30.0 * s, s * 0.92)
    put_rgba(frame, sub, sx, sy, s * 0.92)

    for c in sorted(a["cards"], key=lambda q: q["z"], reverse=True):
        z = c["z"]
        px = W * 0.5 + F * (c["x"] - camx) / (z - camz)
        py = H * 0.5 - F * c["y"] / (z - camz)
        px += 12.0 * math.sin(c["omega"] * t + c["phase"])
        py += 18.0 * math.cos(1.31 * c["omega"] * t + c["phase"])
        tilt = 3.5 * math.sin(0.73 * c["omega"] * t)
        sc = 0.93 * 900.0 / (z - camz)
        put_rgba(frame, c["shadow"], px + 20.0, py + 28.0, sc, tilt, 0.95)
        put_rgba(frame, c["rgba"], px, py, sc, tilt, 1.0)

    for i in range(28):
        px = (i * 137.0 + t * 14.0) % W
        py = (i * 211.0 + 19.0 * math.sin(t * 0.4 + i)) % H
        frame += draw_circle(px, py, 1.6 + (i % 3), "#e9c894", 0.18)
    return frame


def render_spline(t: float, a) -> np.ndarray:
    frame = a["spline_bg"].copy()
    nodes_y = [285.0, 540.0, 795.0]
    nodes_x = 355.0
    p3 = np.array([1305.0, 540.0], np.float32)
    colors = ["#69c8ff", "#79e4e0", "#8bb7ff"]
    for k, ny in enumerate(nodes_y):
        p0 = np.array([nodes_x + 70.0, ny], np.float32)
        dx = p3[0] - p0[0]
        p1 = p0 + np.array([dx * 0.45, 0.0], np.float32)
        p2 = p3 - np.array([dx * 0.45, 0.0], np.float32)
        curve = bezier(p0, p1, p2, p3)
        head = smoothstep(0.0, 1.0, clamp01((t - 0.38 - k * 0.07) / 2.65))
        n = int(max(2, head * len(curve)))
        pts = curve[:n]
        col = B(colors[k])[:3]
        frame += draw_polyline(pts, col, 22, 0.09, 18.0)
        frame += draw_polyline(pts, col, 9, 0.42, 4.0)
        frame += draw_polyline(pts, col, 3, 0.95, 0.0)
        if head < 0.999:
            frame += draw_circle(pts[-1][0], pts[-1][1], 7, col, 0.9, sigma=12.0)

    target = np.array([1492.0, 540.0], np.float32)
    pent = np.array([(target[0] + 185 * math.cos(math.radians(90 + i * 72)),
                      target[1] - 185 * math.sin(math.radians(90 + i * 72)))
                     for i in range(5)], np.float32)
    fill = np.zeros((H, W), np.uint8)
    cv2.fillPoly(fill, [pent.astype(np.int32)], 255)
    frame = frame * (1.0 - fill[..., None] * 0.72) + np.array(B("#071018")[:3]) * fill[..., None] * 0.72
    frame += draw_polyline(np.vstack([pent, pent[:1]]), "#d7b06e", 9, 0.82, 6.0)
    frame += draw_polyline(np.vstack([pent, pent[:1]]), "#ffe0a2", 3, 0.90, 0.0)
    pulse = 0.5 + 0.5 * math.sin(t * 2.0)
    frame += draw_circle(target[0], target[1], 110, "#e6b96c", 0.10 + 0.06 * pulse, sigma=30.0)
    frame += draw_circle(target[0], target[1], 60, "#ffe5b2", 0.12, sigma=18.0)

    burst = clamp01((t - 3.15) / 1.15)
    if burst > 0:
        burst_a = (1.0 - burst) ** 2
        rad = 190.0 + 250.0 * burst
        frame += draw_circle(target[0], target[1], rad, "#ffd28a", 0.55 * burst_a,
                             thickness=max(1, int(7 * (1.0 - burst) + 1)), sigma=16.0)
        frame += draw_circle(target[0], target[1], rad * 1.25, "#e8a94e", 0.20 * burst_a, sigma=38.0)

    for k, ny in enumerate(nodes_y):
        col = B(colors[k])[:3]
        frame += draw_circle(nodes_x, ny, 108, col, 0.10, sigma=32.0)
        frame += draw_circle(nodes_x, ny, 63, "#05090f", 0.95)
        frame += draw_circle(nodes_x, ny, 61, col, 0.38, thickness=5, sigma=3.0)
        frame += draw_circle(nodes_x, ny, 43, col, 0.18, sigma=12.0)
        frame += draw_circle(nodes_x, ny, 7, "#eaf7ff", 0.85, sigma=8.0)
    return frame


def spring_scale(tau: float) -> float:
    if tau <= 0.0:
        return 0.0
    zeta, wn = 0.5176, 18.0
    wd = wn * math.sqrt(1.0 - zeta * zeta)
    return 1.0 - math.exp(-zeta * wn * tau) * (
        math.cos(wd * tau) + (zeta * wn / wd) * math.sin(wd * tau))


def render_spring(t: float, a) -> np.ndarray:
    frame = a["doc"].copy()
    drift_x = 7.0 * math.sin(t * 0.35)
    drift_y = 4.0 * math.cos(t * 0.28)
    frame = cv2.warpAffine(frame, np.float32([[1, 0, drift_x], [0, 1, drift_y]]),
                           (W, H), flags=cv2.INTER_LINEAR, borderMode=cv2.BORDER_REFLECT)
    bust = a["bust"][0] if isinstance(a["bust"], tuple) else a["bust"]
    breathe = 1.0 + 0.003 * math.sin(t * 0.7)
    put_rgba(frame, bust, 845.0, 570.0, breathe)
    s1 = max(0.0, spring_scale(t - 0.55))
    s2 = max(0.0, spring_scale(t - 0.70))
    icon, icon_sh = a["badge"]
    pill, pill_sh = a["pill"]
    if s1 > 0.001:
        put_rgba(frame, icon_sh, 1320.0 + 24.0 * s1, 390.0 + 32.0 * s1, s1, 0.0, 0.75)
        put_rgba(frame, icon, 1320.0, 390.0, s1, 0.0, min(1.0, s1 * 1.4))
    if s2 > 0.001:
        put_rgba(frame, pill_sh, 1320.0 + 24.0 * s2, 570.0 + 30.0 * s2, s2, 0.0, 0.75)
        put_rgba(frame, pill, 1320.0, 570.0, s2, 0.0, min(1.0, s2 * 1.4))
    sweep = clamp01((t - 1.2) / 4.0)
    if "parallax_glow" in a:
        frame += a["parallax_glow"] * (0.05 + 0.04 * math.sin(t * 0.8 + sweep * 3.0))
    return frame

def render_desk(t: float, a) -> np.ndarray:
    frame = a["desk_paper"].copy()
    if "parallax_glow" in a:
        frame += a["parallax_glow"] * (0.035 + 0.015 * math.sin(t * 0.45))
    u = (t - 2.0) / 0.9
    if u > 0.0:
        e = ease_out_cubic(u)
        cx = 2110.0 + (1310.0 - 2110.0) * e
        cy = 1260.0 + (560.0 - 1260.0) * e
        ang = 42.0 + (18.0 - 42.0) * e
        sc = 0.72 + 0.28 * e
        h = 0.25 + 259.75 * (1.0 - e)
        hn = (h - 0.25) / 259.75
        photo = a["photo"]
        cloud = make_shadow(photo, 18.0 + 26.0 * hn, 0.20 + 0.24 * hn, 80)
        put_rgba(frame, cloud, cx + 18.0, cy + 26.0, sc, ang, 1.0)

        bottom_x = cx + math.sin(math.radians(ang)) * photo.shape[0] * sc * 0.45
        bottom_y = cy + math.cos(math.radians(ang)) * photo.shape[0] * sc * 0.45
        contact = np.zeros((H, W, 3), np.uint8)
        cv2.ellipse(contact, (int(bottom_x), int(bottom_y + 10)),
                    (int(photo.shape[1] * sc * 0.46), int(7 + 65 * hn)),
                    0, 0, 360, 255, -1, cv2.LINE_AA)
        cf = blur_fast(contact.astype(np.float32) / 255.0, 4.0 + 18.0 * hn)
        frame *= (1.0 - cf * (0.42 + 0.42 * (1.0 - hn)))
        put_rgba(frame, photo, cx, cy, sc, ang, 1.0)
    for i in range(18):
        px = (i * 263.0 + t * 9.0) % W
        py = (i * 149.0 + 17.0 * math.sin(t * 0.35 + i)) % H
        frame += draw_circle(px, py, 1.2 + (i % 2), "#e9c48e", 0.10)
    return frame


def render_ink(t: float, a) -> np.ndarray:
    tau = 1.02 - 1.06 * smoothstep(0.35, 5.15, t)
    matte = (a["ink"] > tau).astype(np.uint8)
    mf = matte.astype(np.float32)
    eroded = cv2.erode(matte, np.ones((3, 3), np.uint8))
    edge = np.clip(mf - eroded.astype(np.float32), 0.0, 1.0)
    edge = blur_fast(edge, 0.6)
    frame = a["landscape"] * (1.0 - mf[..., None]) + a["solar"] * mf[..., None]
    frame += edge[..., None] * np.array(B("#fff2d6")[:3]) * 0.85
    frame += edge[..., None] * np.array(B("#e63a2e")[:3]) * 0.35
    frame += mf[..., None] * a["ink"][..., None] * 0.025
    return frame


def make_dust_state():
    rng = np.random.default_rng(303)
    n = 350
    pos = np.column_stack([
        rng.uniform(-1200.0, 1200.0, n),
        rng.uniform(-720.0, 720.0, n),
        rng.uniform(130.0, 1960.0, n),
    ]).astype(np.float32)
    return {"pos": pos,
            "phase": rng.uniform(0.0, 2.0 * math.pi, n).astype(np.float32),
            "base_r": rng.uniform(1.0, 3.0, n).astype(np.float32),
            "base_o": rng.uniform(0.20, 0.85, n).astype(np.float32)}


def render_dust(t: float, a) -> np.ndarray:
    st = a["dust_state"]
    p = st["pos"]
    x, y, z = p[:, 0], p[:, 1], p[:, 2]
    ph = st["phase"]
    kx, ky, kz = 1.0 / 430.0, 1.0 / 360.0, 1.0 / 500.0
    vx = (-ky * np.sin(kx * x + t * 0.6 + ph) * np.sin(ky * y + t * 0.5) -
          kz * np.cos(kz * z + t * 0.9) * np.cos(kx * x + t * 0.3))
    vy = (-kz * np.sin(ky * y + t * 0.7) * np.sin(kz * z + t * 0.4) -
          kx * np.cos(kx * x + t * 0.6) * np.cos(ky * y + t * 0.5))
    vz = (-kx * np.sin(kz * z + t * 0.9) * np.sin(kx * x + t * 0.3) -
          ky * np.cos(ky * y + t * 0.8) * np.cos(kz * z + t * 0.4))
    p[:, 0] += (vx * 34.0 + 4.0 * np.sin(t * 0.22 + ph)) / FPS
    p[:, 1] += (vy * 26.0) / FPS
    p[:, 2] += (vz * 22.0) / FPS
    p[:, 0] = np.where(p[:, 0] < -1200.0, p[:, 0] + 2400.0,
                       np.where(p[:, 0] > 1200.0, p[:, 0] - 2400.0, p[:, 0]))
    p[:, 1] = np.where(p[:, 1] < -720.0, p[:, 1] + 1440.0,
                       np.where(p[:, 1] > 720.0, p[:, 1] - 1440.0, p[:, 1]))
    p[:, 2] = np.where(p[:, 2] < 130.0, p[:, 2] + 1830.0,
                       np.where(p[:, 2] > 1960.0, p[:, 2] - 1830.0, p[:, 2]))

    layer = np.zeros((H, W, 3), np.uint8)
    order = np.argsort(p[:, 2])[::-1]
    warm = np.array(B("#f4c88b")[:3], np.float32)
    cool = np.array(B("#9fc9e8")[:3], np.float32)
    for i in order:
        zi = max(1.0, float(p[i, 2]))
        sx = W * 0.5 + F * float(p[i, 0]) / zi
        sy = H * 0.5 - F * float(p[i, 1]) / zi
        if sx < -80 or sx > W + 80 or sy < -80 or sy > H + 80:
            continue
        coc = abs(1.0 / zi - 1.0 / 900.0) * 4200.0
        rad = float(np.clip(st["base_r"][i] + coc, 1.0, 58.0))
        zfade = smoothstep(140.0, 300.0, zi) * (1.0 - smoothstep(1740.0, 1950.0, zi))
        efade = (smoothstep(-1200.0, -980.0, p[i, 0]) *
                 (1.0 - smoothstep(980.0, 1200.0, p[i, 0])) *
                 smoothstep(-720.0, -560.0, p[i, 1]) *
                 (1.0 - smoothstep(560.0, 720.0, p[i, 1])))
        alpha = min(0.95, float(st["base_o"][i]) * zfade * efade * 1.30)
        if alpha <= 0.01:
            continue
        mix = float(np.clip((zi - 260.0) / 1500.0, 0.0, 1.0))
        col = warm * (1.0 - mix) + cool * mix
        c = tuple(min(255, int(255 * v * 1.25)) for v in np.clip(col, 0.0, 1.0))
        x0, y0 = int(round(sx)), int(round(sy))
        if rad > 4.0:
            r0 = int(round(rad))
            cv2.circle(layer, (x0, y0), r0,
                       tuple(int(v * 0.08 * alpha) for v in c), -1, cv2.LINE_AA)
            cv2.circle(layer, (x0, y0), r0,
                       tuple(int(v * 0.55 * alpha) for v in c),
                       max(1, int(round(rad * 0.12))), cv2.LINE_AA)
            cv2.circle(layer, (x0, y0), max(1, int(round(rad * 0.52))),
                       tuple(int(v * 0.035 * alpha) for v in c), -1, cv2.LINE_AA)
        else:
            cv2.circle(layer, (x0, y0), 2,
                       tuple(int(v * 0.9 * alpha) for v in c), -1, cv2.LINE_AA)
            cv2.circle(layer, (x0, y0), 5,
                       tuple(int(v * 0.16 * alpha) for v in c), -1, cv2.LINE_AA)
    lf = layer.astype(np.float32) / 255.0
    frame = a["dust_bg"].copy()
    add_layer(frame, lf, 1.0)
    add_layer(frame, blur_fast(lf, 4.0), 0.65)
    return frame


def encode_clip(name: str, duration: float, fn, a) -> Path:
    out = HERE / name
    total = int(round(duration * FPS))
    cmd = ["ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
           "-f", "rawvideo", "-pix_fmt", "bgr24", "-s", f"{W}x{H}",
           "-r", str(FPS), "-i", "-", "-an", "-c:v", "libx264",
           "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p",
           "-profile:v", "high", "-level", "4.1", "-movflags", "+faststart",
           str(out)]
    proc = subprocess.Popen(cmd, stdin=subprocess.PIPE, stderr=subprocess.PIPE)
    rng = np.random.default_rng(abs(hash(name)) % (2 ** 32))
    t0 = time.time()
    try:
        for i in range(total):
            t = i / FPS
            fade = min(1.0, t / 0.42, (duration - t) / 0.42) if duration > 1.0 else 1.0
            frame = finish(fn(t, a), rng, fade=fade, grain=0.005)
            proc.stdin.write(frame.tobytes())
            if i % 30 == 0:
                print(f"  {name}: {i}/{total} frames ({time.time() - t0:.1f}s)", flush=True)
        proc.stdin.close()
        err = proc.stderr.read().decode("utf-8", "replace")
        rc = proc.wait()
    except Exception:
        proc.kill()
        raise
    if rc != 0:
        raise RuntimeError(f"ffmpeg failed for {name}: {err[-2000:]}")
    return out


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def probe(path: Path) -> dict:
    cmd = ["ffprobe", "-v", "error", "-select_streams", "v:0",
           "-show_entries", "stream=width,height,r_frame_rate,nb_frames,pix_fmt",
           "-show_entries", "format=duration,size", "-of", "json", str(path)]
    res = subprocess.run(cmd, capture_output=True, text=True, check=True)
    data = json.loads(res.stdout)
    st = data["streams"][0]
    fmt = data["format"]
    return {"width": int(st["width"]), "height": int(st["height"]),
            "fps": st.get("r_frame_rate", "30/1"),
            "frames": int(st.get("nb_frames", 0)),
            "pix_fmt": st.get("pix_fmt", ""),
            "duration_seconds": float(fmt["duration"]),
            "bytes": int(fmt["size"])}


def build_manifest(names: list[str]) -> dict:
    entries = []
    for name, duration, title in CLIPS:
        if name not in names:
            continue
        path = HERE / name
        if not path.is_file():
            continue
        meta = probe(path)
        meta.update({"name": name, "title": title, "sha256": sha256_file(path),
                     "path": str(path)})
        entries.append(meta)
    return {"date": time.strftime("%Y-%m-%d"),
            "renderer": "procedural OpenCV/numpy 2.5D renderer",
            "drive_folder_id": DRIVE_FOLDER,
            "drive_folder_url": f"https://drive.google.com/drive/u/1/folders/{DRIVE_FOLDER}",
            "notes": "No textual components; all clips are 1920x1080, 30 fps, H.264.",
            "clips": entries}


def write_json(path: Path, obj: dict) -> None:
    path.write_text(json.dumps(obj, indent=2) + "\n")


def make_contact_sheet(names: list[str]) -> Path:
    sheet = np.full((H, W, 3), 10, np.uint8)
    chosen = [c for c in CLIPS if c[0] in names][:6]
    for idx, (name, duration, _) in enumerate(chosen):
        path = HERE / name
        if not path.is_file():
            continue
        cap = cv2.VideoCapture(str(path))
        cap.set(cv2.CAP_PROP_POS_FRAMES, int(round(duration * FPS * 0.62)))
        ok, frame = cap.read()
        cap.release()
        if not ok:
            continue
        frame = cv2.resize(frame, (640, 360), interpolation=cv2.INTER_AREA)
        col, row = idx % 3, idx // 3
        x, y = col * 640, 180 + row * 360
        sheet[y:y + 360, x:x + 640] = frame
        cv2.rectangle(sheet, (x, y), (x + 639, y + 359), (55, 65, 72), 2)
    out = HERE / "00_contact_sheet_1920x1080.png"
    cv2.imwrite(str(out), sheet)
    return out


def upload_clips(manifest: dict) -> bool:
    if not UPLOADER.is_file():
        print(f"UPLOADER_MISSING {UPLOADER}")
        return False
    if not CREDENTIALS.exists() or not TOKEN.exists():
        print("DRIVE_CREDENTIALS_MISSING")
        return False
    logdir = HERE / "upload_logs"
    logdir.mkdir(exist_ok=True)
    for entry in manifest["clips"]:
        path = HERE / entry["name"]
        cmd = [str(UPLOADER), "-credentials", str(CREDENTIALS), "-token", str(TOKEN),
               "-folder", DRIVE_FOLDER, "-subfolder", DRIVE_SUBFOLDER,
               "-file", str(path), "-name", entry["name"], "-sha256", entry["sha256"]]
        print(f"[UPLOAD] {entry['name']} ({entry['bytes'] / 1e6:.2f} MB)", flush=True)
        res = subprocess.run(cmd, capture_output=True, text=True, timeout=300)
        out = (res.stdout + "\n" + res.stderr).strip()
        (logdir / f"upload_{entry['name']}.log").write_text(out + "\n")
        entry["drive_upload_rc"] = res.returncode
        if res.returncode != 0 or "DRIVE_UPLOAD_PASS" not in out:
            print(f"[UPLOAD_FAIL] {entry['name']} rc={res.returncode}\n{out[-1500:]}")
            return False
        for token in out.split():
            if token.startswith("id="):
                entry["drive_file_id"] = token.split("=", 1)[1]
            elif token.startswith("link="):
                entry["drive_url"] = token.split("=", 1)[1]
        print(f"[UPLOAD_PASS] {entry.get('drive_url', '')}", flush=True)
        write_json(HERE / "upload_manifest.json", manifest)
    write_json(HERE / "upload_manifest.json", manifest)
    return True


renderers = {
    "01_2p5d_parallax_1920x1080_8s.mp4": render_parallax,
    "02_spline_network_1920x1080_8s.mp4": render_spline,
    "03_badge_spring_1920x1080_8s.mp4": render_spring,
    "04_deckled_desk_1920x1080_8s.mp4": render_desk,
    "05_ink_wipe_1920x1080_6s.mp4": render_ink,
    "06_dust_field_1920x1080_8s.mp4": render_dust,
}


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--only", default="all", help="clip number prefix, e.g. 01, or all")
    ap.add_argument("--still", action="store_true", help="write one PNG instead of video")
    ap.add_argument("--skip-render", action="store_true")
    ap.add_argument("--upload", action="store_true")
    args = ap.parse_args()
    selected = [c for c in CLIPS if args.only == "all" or c[0].startswith(args.only)]
    if not selected:
        print("no clip selected")
        return 2
    if args.still:
        for name, duration, _ in selected:
            a = build_assets_for(name)
            t = min(duration * 0.62, 4.0)
            frame = finish(renderers[name](t, a), np.random.default_rng(7), grain=0.0)
            out = HERE / (Path(name).stem + "_still.png")
            cv2.imwrite(str(out), frame)
            print(f"STILL {out}")
        return 0
    names = [c[0] for c in selected]
    if not args.skip_render:
        for name, duration, _ in selected:
            a = build_assets_for(name)
            encode_clip(name, duration, renderers[name], a)
            del a
    manifest = build_manifest(names)
    write_json(HERE / "render_manifest.json", manifest)
    make_contact_sheet(names)
    print(f"MANIFEST {HERE / 'render_manifest.json'}")
    if args.upload:
        ok = upload_clips(manifest)
        print("DRIVE_UPLOADS_ALL_PASS" if ok else "DRIVE_UPLOADS_FAILED")
        return 0 if ok else 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())


