#!/usr/bin/env python3
"""Generate the multilingual overlay matrix: 10 overlays x 10 languages.

Matrix (agreed scope for 2026-09-16):
  5 phrase overlays  = the 5 texts of the checked-in phrase preset corpus
                       (phrase_preset_videos), rendered through the PRODUCTION
                       preset `apple_v2` with 5 distinct official motions. The
                       legacy corpus preset names (lower_third_safe,
                       clean_slide_up, ...) are not in RenderingGen's official
                       catalog, and the queue accepts only certified presets.
  5 image overlays   = the 5 official image presets, over the corpus image
                       asset (testdata/golden/gerard_butler.jpg).

Each overlay is rendered once per language = 100 `overlay.render` jobs, all on
the production path (queue -> worker -> warm Chronon daemon).

Outputs (next to this script):
  plans/<overlay>__<lang>.json   the semantic overlay-plan.v1 of record
  manifest.json                  renderinggen.batch-manifest.v1 for batch-submit
"""
import argparse
import hashlib
import json
import os
import sys

LANGUAGES = ["it", "en", "pl", "ru", "de", "es", "pt-BR", "fr", "tr", "id"]

CANVAS = {"width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1}
DURATION_MS = 5000
# Same background as the checked-in preset corpus (preset-render-manifest.json),
# so the translated overlays are visually comparable with the originals.
BACKGROUND = {"kind": "color", "color": [0.9333333333333333, 0.9450980392156862, 0.9058823529411765, 1.0]}

# 5 phrases: corpus text + a distinct official motion on the canonical preset.
PHRASE_OVERLAYS = [
    ("phrase_01_lower_third_safe", "liquid_glass_ripple"),
    ("phrase_02_clean_slide_up", "kinetic_stamp_impact"),
    ("phrase_03_scale_pop", "editorial_push_in"),
    ("phrase_04_dm_sans_fade", "neon_flicker_ignite"),
    ("phrase_05_slide_left_punch", "hologram_scanline_build"),
]

# 5 images: the official image presets exercised by the preset corpus.
IMAGE_OVERLAYS = [
    "image_focus_in",
    "image_fade_in",
    "image_scale_in",
    "image_slide_left",
    "image_slide_right",
]

IMAGE_ASSET = {
    "asset_id": "gerard_butler",
    "file": "testdata/golden/gerard_butler.jpg",
    "media_type": "image/jpeg",
    # The compiler's canonical workspace path for this asset.
    "logical_path": "assets/semantic/gerard_butler.jpg",
}

# Every text preset (including the canonical apple_v2) references this font,
# and the worker resolves plan assets relative to the JOB WORKSPACE
# (processor/gpu_run.go: AssetsRoot = prepared.Workspace.Root()), not to the
# daemon's --assets-root. A phrase job that does not declare the font therefore
# fails inside the daemon with "logical asset does not exist under the mounted
# root" — the smoke run of 2026-09-16 hit exactly that.
FONT_ASSET = {
    "file": "testdata/golden/assets/fonts/Poppins-Bold.ttf",
    "logical_path": "assets/fonts/Poppins-Bold.ttf",
}

# Script-coverage companion for the primary font. Chronon3d resolves its
# fallback stack by scanning the PRIMARY FONT'S OWN DIRECTORY
# (scene/builders/text_run_builder.cpp: bundled_font_root_for), so the fonts a
# job does not materialize simply do not exist for the shaper: a translated
# overlay whose text leaves the primary font's coverage (Russian Cyrillic vs
# Poppins-Bold) fails with "[font-fallback] Missing glyph ... no font in stack
# covers all visible codepoints" and then an empty glyph vector. Inter-Bold is
# already part of Chronon3d's canonical font bundle and covers Cyrillic plus
# Latin-extended (tr/pt/pl diacritics), so declaring it here restores full
# rendering for the translated overlays without an engine change.
FALLBACK_FONT_ASSET = {
    "file": "testdata/golden/assets/fonts/Inter-Bold.ttf",
    "logical_path": "assets/fonts/Inter-Bold.ttf",
}


def job_asset(spec: dict, sha: str, url: str) -> dict:
    asset = {"hash": sha, "logical_path": spec["logical_path"]}
    if url:
        asset["source_url"] = url
    return asset


def sha256_file(path: str) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def phrase_plan(overlay_id: str, motion: str, lang: str, text: str) -> dict:
    return {
        "schema_version": "renderinggen.overlay-plan.v1",
        "plan_id": f"{overlay_id}__{lang}",
        "video_id": f"{overlay_id}__{lang}",
        "language": lang,
        "width": CANVAS["width"],
        "height": CANVAS["height"],
        "fps_num": CANVAS["fps_num"],
        "fps_den": CANVAS["fps_den"],
        "duration_ms": DURATION_MS,
        "background": BACKGROUND,
        "items": [
            {
                "id": "item_1",
                "kind": "important_phrase",
                "template_id": "IMPORTANT_PHRASE",
                "preset_id": "apple_v2",
                "motion_id": motion,
                "text": text,
                "start_ms": 0,
                "end_ms": DURATION_MS,
            }
        ],
    }


def image_plan(preset: str, lang: str, asset_ref: dict) -> dict:
    return {
        "schema_version": "renderinggen.overlay-plan.v1",
        "plan_id": f"{preset}__{lang}",
        "video_id": f"{preset}__{lang}",
        "language": lang,
        "width": CANVAS["width"],
        "height": CANVAS["height"],
        "fps_num": CANVAS["fps_num"],
        "fps_den": CANVAS["fps_den"],
        "duration_ms": DURATION_MS,
        "background": BACKGROUND,
        "items": [
            {
                "id": "item_1",
                # The canonical kind vocabulary spells IMAGE_OVERLAY as
                # "entity_image" (overlay/registry.go KindEntityImage); any other
                # spelling is rejected fail-closed at the semantic boundary.
                "kind": "entity_image",
                "template_id": "IMAGE_OVERLAY",
                "preset_id": preset,
                "start_ms": 0,
                "end_ms": DURATION_MS,
                "asset_refs": [asset_ref],
            }
        ],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--batch-id", default="ml-overlays-10x10")
    parser.add_argument("--translations", default="translations.json")
    parser.add_argument(
        "--asset-base-url",
        default="",
        help="http(s) base URL the worker self-heals assets from (path joined with the logical path)",
    )
    parser.add_argument("--langs", default="", help="comma-separated subset of languages (default: all 10)")
    parser.add_argument("--only", default="", help="comma-separated overlay ids (default: the full matrix)")
    parser.add_argument("--manifest", default="manifest.json")
    parser.add_argument("--plans-dir", default="plans")
    parser.add_argument("--jobs-dir", default="jobs")
    parser.add_argument("--repo-root", default="../..", help="RenderingGen checkout root (asset hashing)")
    args = parser.parse_args()

    langs = [x for x in args.langs.split(",") if x] or LANGUAGES
    unknown = [x for x in langs if x not in LANGUAGES]
    if unknown:
        print(f"unknown languages: {unknown}", file=sys.stderr)
        return 2

    translations = json.load(open(args.translations))
    wanted = [x for x in args.only.split(",") if x]

    base = args.asset_base_url.rstrip("/")
    jobs = []
    os.makedirs(args.plans_dir, exist_ok=True)
    os.makedirs(args.jobs_dir, exist_ok=True)

    # --- 5 phrase overlays -------------------------------------------------
    phrase_overlays = [(o, m) for o, m in PHRASE_OVERLAYS if not wanted or o in wanted]
    if phrase_overlays:
        font_hash = sha256_file(os.path.join(args.repo_root, FONT_ASSET["file"]))
        font_asset = job_asset(FONT_ASSET, font_hash, f"{base}/{FONT_ASSET['logical_path']}" if base else "")
        fallback_hash = sha256_file(os.path.join(args.repo_root, FALLBACK_FONT_ASSET["file"]))
        fallback_asset = job_asset(
            FALLBACK_FONT_ASSET,
            fallback_hash,
            f"{base}/{FALLBACK_FONT_ASSET['logical_path']}" if base else "",
        )
        for overlay_id, motion in phrase_overlays:
            row = translations.get(overlay_id)
            if row is None:
                print(f"no translation row for {overlay_id}", file=sys.stderr)
                return 2
            for lang in langs:
                plan = phrase_plan(overlay_id, motion, lang, row["translations"][lang])
                jobs.append({
                    "id": f"{overlay_id}__{lang}",
                    "plan": plan,
                    # Both fonts ride every phrase job: the primary the plan
                    # names, and the coverage companion the fallback scan needs.
                    "assets": [font_asset, fallback_asset],
                })

    # --- 5 image overlays --------------------------------------------------
    image_overlays = [x for x in IMAGE_OVERLAYS if not wanted or x in wanted]
    if image_overlays:
        asset_file = os.path.join(args.repo_root, IMAGE_ASSET["file"])
        asset_hash = sha256_file(asset_file)
        # The image is addressed by its real bytes: the semantic ref carries the
        # sha256 the worker verifies while streaming, so a wrong or truncated
        # download fails closed instead of rendering a corrupt card.
        image_source = f"{base}/{os.path.basename(IMAGE_ASSET['file'])}" if base else asset_file
        asset_ref = {
            "asset_id": IMAGE_ASSET["asset_id"],
            "url": image_source,
            "sha256": asset_hash,
            "media_type": IMAGE_ASSET["media_type"],
        }
        image_job_asset = job_asset(
            IMAGE_ASSET, asset_hash, image_source if image_source.startswith("http") else ""
        )
        for preset in image_overlays:
            for lang in langs:
                plan = image_plan(preset, lang, asset_ref)
                jobs.append({"id": f"{preset}__{lang}", "plan": plan, "assets": [image_job_asset]})

    for job in jobs:
        with open(os.path.join(args.plans_dir, f"{job['id']}.json"), "w") as handle:
            json.dump(job["plan"], handle, ensure_ascii=False, indent=2, sort_keys=True)
            handle.write("\n")

    manifest = {
        "schema_version": "renderinggen.batch-manifest.v1",
        "batch_id": args.batch_id,
        "jobs": [{"id": j["id"], "render_plan": j["plan"], "assets": j["assets"]} for j in jobs],
    }
    with open(args.manifest, "w") as handle:
        json.dump(manifest, handle, ensure_ascii=False, indent=2, sort_keys=True)
        handle.write("\n")

    phrases = sum(1 for j in jobs if "apple_v2" in json.dumps(j["plan"]))
    print(f"jobs={len(jobs)} (phrase={phrases} image={len(jobs) - phrases}) langs={len(langs)}")
    print(f"manifest={args.manifest} plans={args.plans_dir}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
