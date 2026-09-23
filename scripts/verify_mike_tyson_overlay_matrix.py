#!/usr/bin/env python3
"""Verify Mike Tyson extraction, overlay timing, and render evidence.

The matrix contract is shared with PipelineGen's live Go gate. Request
fixtures remain the authority for Drive routing, document languages and phrase
budgets; the shared matrix contract owns the exact expected people and phrases.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any


ROOT_DIR = Path(__file__).resolve().parents[1]
MATRIX_PATH = ROOT_DIR / "mike_tyson_overlay_test" / "matrix_cases.json"
EXPECTED_CASE_IDS = ("simple", "extended", "five")
SUCCESS_STATUSES = {"COMPLETED", "DONE", "READY", "SUCCEEDED", "SUCCESS"}
IMAGE_KINDS = {"entity_image", "image"}
PHRASE_KIND = "text_phrase"


def _nested_result(document: dict[str, Any]) -> dict[str, Any]:
    job = document.get("job")
    job_result = job.get("result") if isinstance(job, dict) else None
    result = document.get("result")
    candidates = [
        job_result.get("result") if isinstance(job_result, dict) else None,
        result.get("result") if isinstance(result, dict) else None,
        result,
    ]
    for candidate in candidates:
        if isinstance(candidate, dict) and any(key in candidate for key in ("scenes", "overlay_plan", "entities")):
            return candidate
    raise ValueError("risultato generazione non trovato")


def _unique_strings(values: list[object]) -> list[str]:
    return sorted({value.strip() for value in values if isinstance(value, str) and value.strip()})


def _persons(result: dict[str, Any]) -> list[str]:
    entities = result.get("entities")
    entities = entities if isinstance(entities, dict) else {}
    direct = [item.get("value") for item in entities.get("persons", []) if isinstance(item, dict)]
    annotated = [
        entity.get("canonical_name") or entity.get("name") or entity.get("text")
        for scene in result.get("scenes", []) if isinstance(scene, dict)
        for entity in (scene.get("annotations", {}).get("primary_entities", []) if isinstance(scene.get("annotations"), dict) else [])
        if isinstance(entity, dict) and entity.get("type") == "PERSON"
    ]
    entity_timeline = result.get("entity_timeline")
    entity_timeline = entity_timeline if isinstance(entity_timeline, dict) else {}
    timeline = [
        entity.get("name")
        for scene in entity_timeline.get("scenes", []) if isinstance(scene, dict)
        for entity in scene.get("entities", []) if isinstance(scene.get("entities"), list)
        if isinstance(entity, dict) and entity.get("type") == "PERSON"
    ]
    return _unique_strings(direct + annotated + timeline)


def _phrases(result: dict[str, Any]) -> list[str]:
    entities = result.get("entities")
    entities = entities if isinstance(entities, dict) else {}
    values: list[object] = list(entities.get("important_phrases", []))
    for scene in result.get("scenes", []):
        annotations = scene.get("annotations") if isinstance(scene, dict) else None
        if isinstance(annotations, dict) and isinstance(annotations.get("important_phrases"), list):
            values.extend(annotations["important_phrases"])
    for segment in result.get("segments", []):
        insights = segment.get("insights") if isinstance(segment, dict) else None
        if isinstance(insights, dict) and isinstance(insights.get("important_phrases"), list):
            values.extend(insights["important_phrases"])
    normalized = [value.get("text") or value.get("value") if isinstance(value, dict) else value for value in values]
    return _unique_strings(normalized)


def _motion(item: dict[str, Any]) -> str:
    direct = item.get("motion_id")
    if isinstance(direct, str) and direct.strip():
        return direct.strip()
    params = item.get("params")
    animation = params.get("animation", {}) if isinstance(params, dict) else {}
    if not isinstance(animation, dict):
        return ""
    for key in ("motion_id", "preset"):
        value = animation.get(key)
        if isinstance(value, str) and value.strip():
            return value.strip()
    return ""


def _overlay_items(result: dict[str, Any]) -> list[dict[str, Any]]:
    plan = result.get("overlay_plan")
    if not isinstance(plan, dict):
        raise AssertionError("result.overlay_plan is missing")
    items = plan.get("items")
    if not isinstance(items, list) or not all(isinstance(item, dict) for item in items):
        raise AssertionError("result.overlay_plan.items is missing or invalid")
    return items


def _expected_from_fixture(fixture_path: Path, persons: list[str]) -> dict[str, Any]:
    request = json.loads(fixture_path.read_text(encoding="utf-8"))
    items = request.get("items") if isinstance(request, dict) else None
    if not isinstance(items, list) or len(items) != 1 or not isinstance(items[0], dict):
        raise ValueError(f"{fixture_path}: expected exactly one request item")
    item = items[0]
    source = item.get("source") if isinstance(item.get("source"), dict) else {}
    params = item.get("script_params") if isinstance(item.get("script_params"), dict) else {}
    request_texts = [item.get("style", ""), source.get("source_text", "")]
    for segment in params.get("segments", []):
        if isinstance(segment, dict):
            request_texts.append(segment.get("source_text", ""))
    request_text = " ".join(value for value in request_texts if isinstance(value, str)).casefold()
    for person in persons:
        if person.casefold() not in request_text:
            raise ValueError(f"{fixture_path}: expected person {person!r} is not named in request instructions/source")

    media_plan = item.get("media_plan") if isinstance(item.get("media_plan"), dict) else {}
    extraction = media_plan.get("extraction") if isinstance(media_plan.get("extraction"), dict) else {}
    phrases = extraction.get("important_phrases")
    if not isinstance(phrases, list) or not all(isinstance(value, str) and value.strip() for value in phrases):
        raise ValueError(f"{fixture_path}: extraction.important_phrases must be a list of non-empty strings")
    if extraction.get("max_entities_per_segment") != len(persons):
        raise ValueError(f"{fixture_path}: max_entities_per_segment must match expected person count")
    phrase_budget = extraction.get("max_important_phrases_per_segment")
    if isinstance(phrase_budget, bool) or not isinstance(phrase_budget, int) or phrase_budget != len(phrases):
        raise ValueError(f"{fixture_path}: phrase budget must match declared phrase count")
    if len(set(phrases)) != len(phrases):
        raise ValueError(f"{fixture_path}: important_phrases must be unique")

    output = item.get("output") if isinstance(item.get("output"), dict) else {}
    render = output.get("render") if isinstance(output.get("render"), dict) else {}
    docs = item.get("docs") if isinstance(item.get("docs"), dict) else {}
    languages = docs.get("languages")
    if not isinstance(languages, list) or not languages or not all(isinstance(value, str) and value.strip() for value in languages) or len(set(languages)) != len(languages):
        raise ValueError(f"{fixture_path}: docs.languages must be non-empty and unique")
    folder, subfolder = render.get("drive_folder_id"), render.get("drive_subfolder_name")
    if not render.get("enabled") or not isinstance(folder, str) or not folder.strip() or not isinstance(subfolder, str) or not subfolder.strip():
        raise ValueError(f"{fixture_path}: render and Drive folder/subfolder must be enabled")
    return {"phrases": phrases, "drive_folder_id": folder, "subfolder": subfolder, "doc_languages": languages}


def _matrix_cases() -> dict[str, dict[str, Any]]:
    matrix = json.loads(MATRIX_PATH.read_text(encoding="utf-8"))
    if not isinstance(matrix, dict) or matrix.get("schema_version") != "mike-tyson-overlay-matrix.v1":
        raise ValueError("unsupported Mike Tyson matrix contract schema")
    cases = matrix.get("cases")
    if not isinstance(cases, list) or len(cases) != len(EXPECTED_CASE_IDS):
        raise ValueError(f"matrix contract must define {len(EXPECTED_CASE_IDS)} cases")
    if [case.get("id") for case in cases if isinstance(case, dict)] != list(EXPECTED_CASE_IDS):
        raise ValueError(f"matrix case ids/order must be {list(EXPECTED_CASE_IDS)}")
    result: dict[str, dict[str, Any]] = {}
    seen_fixtures: set[str] = set()
    seen_results: set[str] = set()
    for index, case in enumerate(cases):
        if not isinstance(case, dict):
            raise ValueError(f"matrix case[{index}] must be an object")
        case_id, fixture, result_file = case.get("id"), case.get("fixture"), case.get("result_file")
        if not isinstance(fixture, str) or not fixture or Path(fixture).name != fixture or ".." in fixture:
            raise ValueError(f"{case_id}: fixture must be a filename")
        if not isinstance(result_file, str) or not result_file or Path(result_file).name != result_file or ".." in result_file:
            raise ValueError(f"{case_id}: result_file must be a filename")
        if fixture in seen_fixtures or result_file in seen_results:
            raise ValueError(f"{case_id}: fixture/result filenames must be unique")
        seen_fixtures.add(fixture)
        seen_results.add(result_file)
        persons, phrases = case.get("persons"), case.get("phrases")
        if not isinstance(persons, list) or not persons or not all(isinstance(value, str) and value.strip() for value in persons):
            raise ValueError(f"{case_id}: persons must be a non-empty list of strings")
        if not isinstance(phrases, list) or not phrases or not all(isinstance(value, str) and value.strip() for value in phrases):
            raise ValueError(f"{case_id}: phrases must be a non-empty list of strings")
        if len(set(persons)) != len(persons) or len(set(phrases)) != len(phrases):
            raise ValueError(f"{case_id}: persons and phrases must be unique")
        fixture_path = ROOT_DIR / "mike_tyson_overlay_test" / fixture
        expectations = _expected_from_fixture(fixture_path, persons)
        if expectations["phrases"] != phrases:
            raise ValueError(f"{case_id}: matrix phrases differ from request fixture")
        result[case_id] = {"fixture": fixture_path, "result_file": result_file, "persons": persons, **expectations}
    return result


def _person_overlays(result: dict[str, Any], image_items: list[dict[str, Any]]) -> list[str]:
    timeline = result.get("entity_timeline")
    if not isinstance(timeline, dict) or not isinstance(timeline.get("scenes"), list):
        raise AssertionError("entity_timeline.scenes is missing or invalid")
    by_id: dict[str, str] = {}
    for scene in timeline["scenes"]:
        if not isinstance(scene, dict) or not isinstance(scene.get("entities"), list):
            raise AssertionError("entity_timeline scene entities are missing or invalid")
        for entity in scene["entities"]:
            if not isinstance(entity, dict) or entity.get("type") != "PERSON":
                continue
            entity_id, name = entity.get("entity_id"), entity.get("name")
            if isinstance(entity_id, str) and entity_id and isinstance(name, str) and name.strip():
                existing = by_id.get(entity_id)
                if existing is not None and existing != name.strip():
                    raise AssertionError(f"PERSON entity_id {entity_id!r} has conflicting names")
                by_id[entity_id] = name.strip()
    names = []
    for item in image_items:
        entity_id = item.get("entity_id")
        name = by_id.get(entity_id) if isinstance(entity_id, str) else None
        if name is None:
            raise AssertionError(f"overlay immagine {item.get('id')!r} is not linked to a PERSON occurrence")
        names.append(name)
    return names


def _timing(item: dict[str, Any]) -> tuple[int, int]:
    start_us, end_us = item.get("start_us"), item.get("end_us")
    if start_us is not None and end_us is not None:
        return int(start_us), int(end_us)
    start_ms = int(item.get("start_ms", 0))
    end_ms = item.get("end_ms")
    if end_ms is None:
        end_ms = start_ms + int(item.get("duration_ms", 0))
    return start_ms * 1000, int(end_ms) * 1000


def _is_sha256(value: object) -> bool:
    if not isinstance(value, str) or len(value) != 64:
        return False
    try:
        int(value, 16)
    except ValueError:
        return False
    return True


def _certified_artifact(artifact: object, description: str) -> dict[str, Any]:
    if not isinstance(artifact, dict) or not _is_sha256(artifact.get("sha256")):
        raise AssertionError(f"{description} artifact is missing a valid SHA-256")
    if int(artifact.get("size_bytes", 0)) <= 0 or int(artifact.get("frame_count", 0)) <= 0:
        raise AssertionError(f"{description} artifact has no positive size/frame_count")
    if not (artifact.get("drive_link") or artifact.get("url")):
        raise AssertionError(f"{description} artifact has no published URL")
    return artifact


def _verify_job_status(document: dict[str, Any]) -> None:
    job = document.get("job")
    top_id = document.get("id")
    job_id = job.get("id") if isinstance(job, dict) else None
    if top_id and job_id and top_id != job_id:
        raise AssertionError(f"job identity mismatch: top-level id={top_id!r}, job.id={job_id!r}")
    status = job.get("status") if isinstance(job, dict) else None
    status = status or document.get("status")
    if status is not None and str(status).strip().upper() not in SUCCESS_STATUSES:
        raise AssertionError(f"job non completato; status={str(status).strip().upper() or 'missing'}")


def verify(path: Path, expected_persons: list[str], expected_phrases: list[str], expected_drive_folder_id: str,
           expected_subfolder: str, expected_doc_languages: list[str], expected_render: bool) -> dict[str, Any]:
    document = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(document, dict):
        raise ValueError(f"{path}: result must be a JSON object")
    _verify_job_status(document)
    result = _nested_result(document)
    persons, phrases = _persons(result), _phrases(result)
    items = _overlay_items(result)
    unknown_kinds = [item.get("kind") for item in items if item.get("kind") not in IMAGE_KINDS | {PHRASE_KIND}]
    if unknown_kinds:
        raise AssertionError(f"overlay plan contains unsupported/unclassified kinds: {unknown_kinds}")
    image_items = [item for item in items if item.get("kind") in IMAGE_KINDS]
    phrase_items = [item for item in items if item.get("kind") == PHRASE_KIND]
    if sorted(persons) != sorted(expected_persons):
        raise AssertionError(f"PERSON extraction={persons}; expected exactly {expected_persons}")
    if sorted(phrases) != sorted(expected_phrases):
        raise AssertionError(f"important phrases={phrases}; expected exactly {expected_phrases}")
    if len(image_items) != len(expected_persons) or len(phrase_items) != len(expected_phrases):
        raise AssertionError(f"overlay counts image/phrase={len(image_items)}/{len(phrase_items)}, expected exactly {len(expected_persons)}/{len(expected_phrases)}")
    rendered_phrases = [item.get("text", "").strip() if isinstance(item.get("text"), str) else "" for item in phrase_items]
    if sorted(rendered_phrases) != sorted(expected_phrases):
        raise AssertionError(f"rendered phrase text={rendered_phrases}; expected exactly {expected_phrases}")
    rendered_people = _person_overlays(result, image_items)
    if sorted(rendered_people) != sorted(expected_persons):
        raise AssertionError(f"image overlays are bound to {rendered_people}; expected {expected_persons}")
    motions = [_motion(item) for item in phrase_items]
    if any(not motion for motion in motions) or len(set(motions)) != len(phrase_items):
        raise AssertionError(f"phrase motions must be present and distinct: {motions}")

    plan = result["overlay_plan"]
    if expected_render:
        if not all(isinstance(plan.get(key), str) and plan[key].strip() for key in ("fingerprint", "video_id")):
            raise AssertionError("overlay plan fingerprint/video identity is missing")
        plan_ids = [item.get("id") for item in items]
        if any(not isinstance(value, str) or not value for value in plan_ids) or len(set(plan_ids)) != len(plan_ids):
            raise AssertionError("overlay plan item ids are missing or duplicated")
        if any(not isinstance(item.get("render_key"), str) or not item["render_key"].strip() for item in items):
            raise AssertionError("overlay plan item render_key is missing")

    for item in items:
        start, end = _timing(item)
        if start < 0 or end <= start:
            raise AssertionError(f"invalid overlay timing for {item.get('id')!r}: [{start},{end})")

    timeline = result.get("editing_timeline")
    raw_timeline_items = timeline.get("overlays") if isinstance(timeline, dict) else None
    if not isinstance(raw_timeline_items, list) or not all(isinstance(item, dict) for item in raw_timeline_items):
        raise AssertionError("editing_timeline.overlays is missing or invalid")
    timeline_items = {}
    for peer in raw_timeline_items:
        artifact_id = peer.get("artifact_id")
        if not isinstance(artifact_id, str) or not artifact_id or artifact_id in timeline_items:
            raise AssertionError(f"editing_timeline artifact_id missing/duplicated: {artifact_id!r}")
        timeline_items[artifact_id] = peer
    if expected_render and set(timeline_items) != set(plan_ids):
        raise AssertionError(f"editing_timeline ids differ from plan ids: {sorted(timeline_items)} != {sorted(plan_ids)}")

    render = result.get("overlay_render")
    item_renders: dict[str, dict[str, Any]] = {}
    if expected_render:
        if not isinstance(render, dict) or render.get("status", "").upper() not in SUCCESS_STATUSES or not render.get("job_id"):
            raise AssertionError("overlay_render is missing, incomplete, or unsuccessful")
        top_artifact = _certified_artifact(render.get("artifact"), "overlay render")
        rendered_items = render.get("items")
        if not isinstance(rendered_items, list) or len(rendered_items) != len(items):
            raise AssertionError(f"per-item render count={len(rendered_items) if isinstance(rendered_items, list) else 'missing'}, expected {len(items)}")
        for index, ref in enumerate(rendered_items):
            if not isinstance(ref, dict) or ref.get("item_id") != plan_ids[index]:
                raise AssertionError(f"render item[{index}] does not match plan order")
            job_id = ref.get("job_id")
            if not isinstance(job_id, str) or not job_id or any(existing["job_id"] == job_id for existing in item_renders.values()):
                raise AssertionError(f"render job missing or reused for {ref.get('item_id')!r}")
            if str(ref.get("status", "")).upper() not in SUCCESS_STATUSES:
                raise AssertionError(f"render for {ref['item_id']!r} did not succeed")
            item_renders[ref["item_id"]] = {"job_id": job_id, "artifact": _certified_artifact(ref.get("artifact"), f"item {ref['item_id']}")}
        first = item_renders[plan_ids[0]]
        if render["job_id"] != first["job_id"] or top_artifact["sha256"] != first["artifact"]["sha256"]:
            raise AssertionError("top-level render is not the first per-item render")

    for item in items:
        item_id = item["id"]
        peer = timeline_items.get(item_id)
        if peer is None:
            continue
        plan_start, _ = _timing(item)
        peer_start, peer_end = _timing(peer)
        if plan_start != peer_start or peer_end <= peer_start:
            raise AssertionError(f"timeline timing differs for {item_id!r}")
        if expected_render:
            reference = item_renders[item_id]
            required = ("render_job_id", "drive_link", "plan_fingerprint", "render_key", "source_video_asset_id")
            if not all(peer.get(key) for key in required) or not _is_sha256(peer.get("sha256")):
                raise AssertionError(f"editing_timeline overlay {item_id!r} lacks certified artifact lineage")
            if peer["render_job_id"] != reference["job_id"] or peer["sha256"] != reference["artifact"]["sha256"]:
                raise AssertionError(f"editing_timeline job/hash mismatch for {item_id!r}")
            if peer["plan_fingerprint"] != plan["fingerprint"] or peer["source_video_asset_id"] != plan["video_id"]:
                raise AssertionError(f"editing_timeline plan/video provenance mismatch for {item_id!r}")
            if peer["render_key"] != item["render_key"]:
                raise AssertionError(f"editing_timeline render key mismatch for {item_id!r}")
            artifact_url = reference["artifact"].get("drive_link") or reference["artifact"].get("url")
            if artifact_url and peer["drive_link"] != artifact_url:
                raise AssertionError(f"editing_timeline Drive URL mismatch for {item_id!r}")

    documents = result.get("documents")
    if not isinstance(documents, dict):
        raise AssertionError("published documents are missing")
    for language in expected_doc_languages:
        if not isinstance(documents.get(language), dict) or not documents[language].get("link"):
            raise AssertionError(f"native Google Doc for {language} is missing")
    if not isinstance(documents.get("it"), dict) or not documents["it"].get("link"):
        raise AssertionError("native Italian Google Doc is missing")
    render_config = result.get("render")
    if not isinstance(render_config, dict) or render_config.get("drive_folder_id") != expected_drive_folder_id or render_config.get("drive_subfolder_name") != expected_subfolder:
        raise AssertionError("unexpected render Drive destination")
    return {"file": str(path), "persons": persons, "important_phrases": phrases, "overlay_items": len(items),
            "image_items": len(image_items), "phrase_items": len(phrase_items), "phrase_motions": sorted(motions),
            "timed_items": len(items), "render_status": render.get("status", "not checked") if isinstance(render, dict) else "not checked",
            "google_doc": documents["it"]["link"], "drive_subfolder": render_config["drive_subfolder_name"]}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--result", action="append", default=[], metavar="CASE=PATH")
    parser.add_argument("--no-render-check", action="store_true")
    parser.add_argument("--validate-contract", action="store_true")
    args = parser.parse_args()
    matrix = _matrix_cases()
    if args.validate_contract:
        print(json.dumps({"status": "PASS", "cases": list(matrix)}, ensure_ascii=False))
        return 0
    selected: dict[str, Path] = {}
    for value in args.result:
        case_id, separator, raw_path = value.partition("=")
        if not separator or not raw_path or case_id not in matrix or case_id in selected:
            raise ValueError(f"invalid or duplicate --result {value!r}; expected one CASE=PATH per declared case")
        selected[case_id] = Path(raw_path)
    if set(selected) != set(matrix):
        raise ValueError(f"results must cover exactly {list(matrix)}; got {sorted(selected)}")
    summaries = []
    for case_id, case in matrix.items():
        path = selected[case_id]
        if path.name != case["result_file"]:
            raise ValueError(f"{case_id}: expected result file {case['result_file']!r}")
        summaries.append(verify(path, case["persons"], case["phrases"], case["drive_folder_id"], case["subfolder"], case["doc_languages"], not args.no_render_check))
    print(json.dumps({"status": "PASS", "cases": summaries}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (AssertionError, OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        raise SystemExit(1)
