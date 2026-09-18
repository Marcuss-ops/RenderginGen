#!/usr/bin/env python3
"""Verify Mike Tyson extraction, overlay timing, and render evidence.

The live API response is intentionally accepted in both the current full-job
shape and the compact result shape.  This keeps the check useful for a saved
fixture as well as for a freshly polled job.

The canonical matrix has three cases — 1+1, 3+3 and 5+5 — and every case is
verified against the requests of record in this checkout's
mike_tyson_overlay_test/ directory.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path


def _nested_result(document: dict) -> dict:
    candidates = [
        document.get("job", {}).get("result", {}).get("result"),
        document.get("result", {}).get("result"),
        document.get("result"),
    ]
    for candidate in candidates:
        if isinstance(candidate, dict) and any(
            key in candidate for key in ("scenes", "overlay_plan", "entities")
        ):
            return candidate
    raise ValueError("risultato generazione non trovato")


def _unique_strings(values: list[object]) -> list[str]:
    return sorted({value.strip() for value in values if isinstance(value, str) and value.strip()})


def _persons(result: dict) -> list[str]:
    direct = [item.get("value") for item in result.get("entities", {}).get("persons", [])]
    annotated = [
        entity.get("canonical_name") or entity.get("name") or entity.get("text")
        for scene in result.get("scenes", [])
        for entity in scene.get("annotations", {}).get("primary_entities", [])
        if entity.get("type") == "PERSON"
    ]
    timeline = [
        entity.get("name")
        for scene in result.get("entity_timeline", {}).get("scenes", [])
        for entity in scene.get("entities", [])
        if entity.get("type") == "PERSON"
    ]
    return _unique_strings(direct + annotated + timeline)


def _phrases(result: dict) -> list[str]:
    values: list[object] = list(result.get("entities", {}).get("important_phrases", []))
    for scene in result.get("scenes", []):
        values += scene.get("annotations", {}).get("important_phrases", [])
    for scene in result.get("segments", []):
        values += scene.get("insights", {}).get("important_phrases", [])
    normalized = []
    for value in values:
        if isinstance(value, dict):
            normalized.append(value.get("text") or value.get("value"))
        else:
            normalized.append(value)
    return _unique_strings(normalized)


def _motion(item: dict) -> str:
    """Resolve the phrase overlay's motion the way the runtime plan carries it.

    The planner writes ``motion_id`` on the plan item; some producers instead
    nest it under ``params.animation``.  Either shape is the same fact.
    """
    direct = item.get("motion_id")
    if isinstance(direct, str) and direct.strip():
        return direct.strip()
    animation = item.get("params", {}).get("animation", {}) if isinstance(item.get("params"), dict) else {}
    if not isinstance(animation, dict):
        return ""
    for key in ("motion_id", "preset"):
        value = animation.get(key)
        if isinstance(value, str) and value.strip():
            return value.strip()
    return ""


def _overlay_items(result: dict) -> list[dict]:
    items = result.get("overlay_plan", {}).get("items", [])
    if items:
        return [item for item in items if isinstance(item, dict)]
    return [item for item in result.get("editing_timeline", {}).get("overlays", []) if isinstance(item, dict)]


def _timing(item: dict) -> tuple[int, int]:
    start_us = item.get("start_us")
    end_us = item.get("end_us")
    if start_us is not None and end_us is not None:
        return int(start_us), int(end_us)
    start_ms = int(item.get("start_ms", 0))
    end_ms = item.get("end_ms")
    if end_ms is None:
        end_ms = start_ms + int(item.get("duration_ms", 0))
    return start_ms * 1000, int(end_ms) * 1000


def verify(path: Path, expected_persons: list[str], expected_phrases: list[str], expected_render: bool) -> dict:
    document = json.loads(path.read_text())
    result = _nested_result(document)
    persons = _persons(result)
    phrases = _phrases(result)
    items = _overlay_items(result)
    # Accepted image kinds mirror the Go runtime gate (entity_image / image, plus
    # the two historical template ids) so both authorities count the same jobs.
    image_items = [
        item
        for item in items
        if item.get("kind") in {"entity_image", "image", "entity_card"}
        or item.get("template_id") in {"image_popup", "IMAGE_OVERLAY"}
    ]
    phrase_items = [item for item in items if item.get("kind") == "text_phrase" or item.get("template_id") == "IMPORTANT_PHRASE"]

    missing_persons = sorted(set(expected_persons) - set(persons))
    missing_phrases = sorted(set(expected_phrases) - set(phrases))
    unexpected_persons = sorted(set(persons) - set(expected_persons))
    unexpected_phrases = sorted(set(phrases) - set(expected_phrases))
    if missing_persons:
        raise AssertionError(f"PERSON mancanti: {missing_persons}; estratte={persons}")
    if missing_phrases:
        raise AssertionError(f"important phrase mancanti: {missing_phrases}; estratte={phrases}")
    if unexpected_persons:
        raise AssertionError(f"PERSON inattese: {unexpected_persons}; attese={expected_persons}")
    if unexpected_phrases:
        raise AssertionError(f"important phrase inattese: {unexpected_phrases}; attese={expected_phrases}")
    if len(image_items) < len(expected_persons):
        raise AssertionError(f"overlay immagine insufficienti: {len(image_items)} < {len(expected_persons)}")
    if len(phrase_items) < len(expected_phrases):
        raise AssertionError(f"overlay frase insufficienti: {len(phrase_items)} < {len(expected_phrases)}")

    # Every phrase overlay must carry its own motion: the production planner
    # assigns a distinct Chronon motion per phrase, and a regression that
    # collapses them onto one animation must fail here rather than in review.
    motions: dict[str, list[str]] = {}
    for item in phrase_items:
        motion = _motion(item)
        if not motion:
            raise AssertionError(f"overlay frase senza motion: {item.get('id')}")
        motions.setdefault(motion, []).append(item.get("id"))
    if len(motions) != len(phrase_items):
        raise AssertionError(
            f"motion distinti insufficienti: {len(motions)} per {len(phrase_items)} frasi ({motions})"
        )

    invalid_timing = []
    for item in items:
        start_us, end_us = _timing(item)
        if start_us < 0 or end_us <= start_us:
            invalid_timing.append((item.get("id"), start_us, end_us))
    if invalid_timing:
        raise AssertionError(f"timing overlay non valido: {invalid_timing}")

    # The plan is the duration authority, while EditingTimeline is the
    # assembly projection. Their starts must remain identical even though an
    # entity image may intentionally keep a five-second display window in the
    # plan and retain only the spoken occurrence in the editing projection.
    timeline_items = {
        item.get("artifact_id"): item
        for item in result.get("editing_timeline", {}).get("overlays", [])
        if isinstance(item, dict)
    }
    for item in result.get("overlay_plan", {}).get("items", []):
        peer = timeline_items.get(item.get("id"))
        if peer is None:
            continue
        plan_start, _ = _timing(item)
        timeline_start, timeline_end = _timing(peer)
        if plan_start != timeline_start or timeline_end <= timeline_start:
            raise AssertionError(
                f"timing non allineato per {item.get('id')}: "
                f"plan_start={plan_start} timeline=[{timeline_start},{timeline_end})"
            )

    render = result.get("overlay_render", {})
    documents = result.get("documents", {})
    it_document = documents.get("it", {}) if isinstance(documents, dict) else {}
    if not it_document.get("link"):
        raise AssertionError("Google Doc nativo it non pubblicato dal job")
    render_config = result.get("render", {})
    if not render_config.get("drive_folder_id") or not render_config.get("drive_subfolder_name"):
        raise AssertionError("destinazione Drive automatica assente nel risultato")
    if expected_render:
        status = str(render.get("status", "")).upper()
        artifact = render.get("artifact", {})
        if status not in {"COMPLETED", "READY", "SUCCEEDED", "SUCCESS"}:
            raise AssertionError(f"overlay render non completato: {status or 'missing'}")
        if not (artifact.get("drive_link") or artifact.get("url")):
            raise AssertionError("overlay render senza artifact URL/Drive")
        if int(artifact.get("frame_count", 0)) <= 0:
            raise AssertionError("overlay render senza frame_count")

    return {
        "file": str(path),
        "persons": persons,
        "important_phrases": phrases,
        "overlay_items": len(items),
        "image_items": len(image_items),
        "phrase_items": len(phrase_items),
        "phrase_motions": sorted(motions),
        "timed_items": len(items) - len(invalid_timing),
        "render_status": render.get("status", "not checked"),
        "google_doc": it_document["link"],
        "drive_subfolder": render_config["drive_subfolder_name"],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--simple", type=Path, required=True)
    parser.add_argument("--extended", type=Path)
    parser.add_argument("--five", type=Path)
    parser.add_argument("--no-render-check", action="store_true")
    args = parser.parse_args()

    # The expected persons and phrases are the ones DECLARED by the requests of
    # record (RenderingGen/mike_tyson_overlay_test/*_generate_request.json) and
    # asserted by the Go runtime gate, including the final punctuation.
    cases = [(args.simple, ["Mike Tyson"], ["Potenza e disciplina"])]
    if args.extended:
        cases.append(
            (
                args.extended,
                ["Mike Tyson", "Cus D'Amato", "Muhammad Ali"],
                [
                    "La velocità apre la distanza.",
                    "La pressione mantiene il controllo.",
                    "La disciplina trasforma la potenza.",
                ],
            )
        )
    if args.five:
        cases.append(
            (
                args.five,
                ["Mike Tyson", "Cus D'Amato", "Muhammad Ali", "Sugar Ray Robinson", "Joe Frazier"],
                [
                    "La velocità apre la distanza.",
                    "La pressione mantiene il controllo.",
                    "La disciplina trasforma la potenza.",
                    "Il ritmo costruisce il vantaggio.",
                    "La tecnica sostiene il coraggio.",
                ],
            )
        )
    summaries = [verify(path, persons, phrases, not args.no_render_check) for path, persons, phrases in cases]
    print(json.dumps({"status": "PASS", "cases": summaries}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (AssertionError, OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        raise SystemExit(1)
