#!/usr/bin/env python3
"""Translate the overlay source corpus with local Ollama only.

Existing translations in the input file are deliberately ignored. Every target
language starts from the English ``source`` field, so this command cannot chain
translations or silently reuse Argos output.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from pathlib import Path


LANGUAGES = ["it", "en", "pl", "ru", "de", "es", "pt-BR", "fr", "tr", "id"]
LANGUAGE_NAMES = {
    "it": "Italian",
    "en": "English",
    "pl": "Polish",
    "ru": "Russian",
    "de": "German",
    "es": "Spanish",
    "pt-BR": "Brazilian Portuguese",
    "fr": "French",
    "tr": "Turkish",
    "id": "Indonesian",
}
SYSTEM_PROMPT = (
    "You are a professional translator. Translate faithfully into natural, "
    "idiomatic target-language text for a short on-screen caption. Return "
    "exactly one concise translation. Preserve the source meaning, tone, names, "
    "numbers, punctuation, and capitalization style (all-caps source text must "
    "stay all caps). Use conventional local spelling for proper "
    "names where appropriate. Translate role titles and ordinary words even "
    "when they resemble English cognates. Never return alternatives, notes, "
    "parentheses, explanations, or multiple options. Do not add, omit, or repeat content. "
    "Return only the translation, with no quotes or commentary."
)


def sha256(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def local_endpoint(value: str) -> str:
    endpoint = value.rstrip("/")
    parsed = urllib.parse.urlparse(endpoint)
    if parsed.scheme != "http" or parsed.hostname not in {"localhost", "127.0.0.1", "::1"}:
        raise ValueError("Ollama URL must use local HTTP on localhost, 127.0.0.1, or ::1")
    return endpoint


def ollama_json(url: str, payload: dict | None, timeout: int) -> dict:
    data = None if payload is None else json.dumps(payload, ensure_ascii=False).encode("utf-8")
    request = urllib.request.Request(
        url,
        data=data,
        headers={"Content-Type": "application/json"} if data is not None else {},
    )
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return json.loads(response.read().decode("utf-8"))
    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as exc:
        raise RuntimeError(f"Ollama request failed at {url}: {exc}") from exc


def translate(endpoint: str, model: str, target: str, source: str, entry_id: str, timeout: int) -> tuple[str, int, int]:
    language = LANGUAGE_NAMES[target]
    source_len = len(source)
    num_predict = min(4096, max(512, source_len * 4))
    started = time.monotonic()
    context = ""
    if entry_id == "phrase_02_clean_slide_up":
        context = (
            " Technical meaning: video recorded at a high frame rate for slow-motion playback, not high resolution."
            " In Polish, use the concise technical wording 'wideo o wysokiej liczbie klatek na sekundę';"
            " never translate this as high resolution."
        )
    elif entry_id == "phrase_03_scale_pop" and target == "de":
        context = " The conventional German headline translation is 'Eilmeldung'."
    payload = {
        "model": model,
        "stream": False,
        "messages": [
            {"role": "system", "content": SYSTEM_PROMPT},
            {
                "role": "user",
                "content": f"Translate this English source into {language} as one short overlay caption.{context} Return only one translation.\n\n{source}",
            },
        ],
        "options": {"num_predict": num_predict, "temperature": 0.1},
    }
    text = ""
    calls = 0
    for attempt in range(3):
        result = ollama_json(endpoint + "/api/chat", payload, timeout)
        calls += 1
        text = result.get("message", {}).get("content", "").strip()
        if text:
            break
    if not text:
        raise RuntimeError(f"Ollama returned empty {target} translation after {calls} attempts")
    if source.isupper():
        text = text.upper()
    if text == source and entry_id != "phrase_01_lower_third_safe":
        raise RuntimeError(f"Ollama returned the unchanged English source for {target}")
    if "(" in text or ")" in text or "\n" in text:
        raise RuntimeError(f"Ollama returned commentary or multiple lines for {target}: {text!r}")
    if entry_id == "phrase_02_clean_slide_up" and target == "pl":
        normalized = text.casefold()
        if "klatek" not in normalized or "rozdzielczo" in normalized:
            raise RuntimeError(f"Ollama returned an inaccurate Polish high-speed video caption: {text!r}")
    if entry_id == "phrase_03_scale_pop" and target == "de" and text.casefold() != "eilmeldung":
        raise RuntimeError(f"Ollama returned an inaccurate German breaking-news caption: {text!r}")
    if len(text.split()) > max(8, len(source.split()) * 2):
        raise RuntimeError(f"Ollama returned an overlong overlay caption for {target}: {text!r}")
    return text, round((time.monotonic() - started) * 1000), calls


def atomic_json(path: Path, value: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=path.parent, delete=False) as temp:
        json.dump(value, temp, ensure_ascii=False, indent=2)
        temp.write("\n")
        temp_path = Path(temp.name)
    os.replace(temp_path, path)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, default=Path(__file__).resolve().parents[1] / "translations.json")
    parser.add_argument("--output", type=Path, default=Path(__file__).resolve().parents[1] / "translations.json")
    parser.add_argument("--provenance", type=Path, default=Path(__file__).resolve().parents[1] / "translation_provenance.json")
    parser.add_argument("--ollama-url", default="http://127.0.0.1:11434")
    parser.add_argument("--model", default="gemma4:e4b")
    parser.add_argument("--timeout", type=int, default=600)
    parser.add_argument("--entry", action="append", help="Regenerate only this entry; may be repeated")
    parser.add_argument("--language", action="append", help="Regenerate only this target language; may be repeated")
    args = parser.parse_args()

    try:
        endpoint = local_endpoint(args.ollama_url)
        tags = ollama_json(endpoint + "/api/tags", None, min(args.timeout, 30))
        available = {entry.get("name") for entry in tags.get("models", [])}
        if args.model not in available:
            raise RuntimeError(f"local Ollama model {args.model!r} is not installed")

        source_rows = json.loads(args.input.read_text(encoding="utf-8"))
        if (args.entry is None) != (args.language is None):
            raise ValueError("--entry and --language must be supplied together for a partial regeneration")
        selected_entries = set(args.entry or source_rows.keys())
        selected_languages = set(args.language or (language for language in LANGUAGES if language != "en"))
        if unknown := selected_entries.difference(source_rows):
            raise ValueError(f"unknown entry id(s): {', '.join(sorted(unknown))}")
        if unknown := selected_languages.difference(LANGUAGES):
            raise ValueError(f"unknown language code(s): {', '.join(sorted(unknown))}")
        if "en" in selected_languages:
            raise ValueError("English is copied from the source and is not a target for Ollama translation")

        partial = args.entry is not None
        if partial:
            if not args.output.exists() or not args.provenance.exists():
                raise ValueError("partial regeneration requires the existing translation output and provenance")
            output = json.loads(args.output.read_text(encoding="utf-8"))
            provenance = json.loads(args.provenance.read_text(encoding="utf-8"))
            if provenance.get("provider") != "ollama-local" or provenance.get("argos_used") is not False:
                raise ValueError("partial regeneration requires a verified Ollama-only provenance file")
            if provenance.get("model") != args.model or provenance.get("cache_used") is not False:
                raise ValueError("partial regeneration model/cache policy does not match the existing corpus")
            for key, source_row in source_rows.items():
                source = source_row.get("source", "").strip()
                output_row = output.get(key, {})
                entry_meta = provenance.get("entries", {}).get(key, {})
                if output_row.get("source") != source or entry_meta.get("source_sha256") != sha256(source):
                    raise ValueError(f"partial regeneration source mismatch for {key}")
                for language, translated in output_row.get("translations", {}).items():
                    record = entry_meta.get("translations", {}).get(language, {})
                    expected_provider = "source" if language == "en" else "ollama-local"
                    if record.get("provider") != expected_provider or record.get("translated_sha256") != sha256(translated):
                        raise ValueError(f"partial regeneration cannot trust existing {key}/{language} output")
        else:
            output = {}
            provenance = {
                "schema_version": "velox.ollama-translation-provenance.v1",
                "provider": "ollama-local",
                "model": args.model,
                "source_language": "en",
                "target_languages": LANGUAGES,
                "generated_at_utc": datetime.now(timezone.utc).isoformat(),
                "cache_used": False,
                "argos_used": False,
                "entries": {},
            }
        provenance["generated_at_utc"] = datetime.now(timezone.utc).isoformat()
        request_count = provenance.get("request_count", 0) if partial else 0
        calls_this_run = 0
        for key, row in source_rows.items():
            source = row.get("source", "").strip()
            if not source:
                raise RuntimeError(f"{key} has no English source text")
            if key not in selected_entries:
                if not partial:
                    raise AssertionError("full generation unexpectedly skipped an entry")
                continue
            translations: dict[str, str] = dict(output[key]["translations"]) if partial else {}
            if not partial:
                provenance["entries"][key] = {
                    "source_sha256": sha256(source),
                    "translations": {},
                }
            for language in LANGUAGES:
                if language not in selected_languages and language != "en":
                    continue
                if language == "en":
                    translated, wall_ms = source, 0
                    provider = "source"
                else:
                    translated, wall_ms, calls = translate(endpoint, args.model, language, source, key, args.timeout)
                    request_count += calls
                    calls_this_run += calls
                    provider = "ollama-local"
                translations[language] = translated
                provenance["entries"][key]["translations"][language] = {
                    "provider": provider,
                    "source_language": "en",
                    "target_language": language,
                    "source_sha256": sha256(source),
                    "translated_sha256": sha256(translated),
                    "wall_ms": wall_ms,
                }
                if language != "en":
                    print(f"{key} {language}: {wall_ms} ms")
            output[key] = {"source": source, "translations": translations}

        provenance["request_count"] = request_count
        atomic_json(args.output, output)
        atomic_json(args.provenance, provenance)
        print(json.dumps({"rows": len(output), "calls_this_run": calls_this_run, "total_translation_calls": request_count, "provider": "ollama-local", "model": args.model, "output": str(args.output), "provenance": str(args.provenance)}, ensure_ascii=False))
        return 0
    except (OSError, ValueError, RuntimeError, json.JSONDecodeError) as exc:
        parser.error(str(exc))
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
