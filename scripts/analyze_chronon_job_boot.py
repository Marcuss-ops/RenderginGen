#!/usr/bin/env python3
"""Measure the per-overlay-item Chronon boot/prepare cost from real run results.

Why this exists (2026-09-28): the daemon-job-lifecycle evidence (2026-09-16, §7)
recorded "prepare/pool warm + plan compile ~1100 ms, still per-job (101 boots /
100 jobs)" and listed stopping that per-job cost as the next lever — measured
BEFORE the warm-daemon/IPC migration. The worker now runs `chronon.mode: ipc`
against a long-lived socket, so the question is whether that ~1.1 s is still
paid. Nobody could answer it without re-running a GPU benchmark, although every
script.generate result already carries the per-item Chronon metrics
(`chronon_job_engine_init_ms`, `chronon_job_backend_init_ms`, …).

This tool answers it from artifacts already on disk: give it one or more
script.generate result JSON files and it reports the per-item boot/prepare
distribution and the total wall those phases cost the run.

Exit codes:
  0: report produced (or --self-test passed)
  1: --max-boot-ms given and the median per-item boot exceeds it
  2: no overlay item metrics found in the inputs
"""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any, Iterable

# Metrics that are per-ITEM Chronon costs (one value per overlay render) and the
# ones a boot/prepare question needs first.
BOOT_KEYS = ("chronon_job_engine_init_ms", "chronon_job_backend_init_ms")
EXTRA_KEYS = (
    "chronon_job_ffprobe_ms",
    "chronon_job_encoder_finalize_ms",
    "chronon_job_encoder_backpressure_wait_ms",
)


def _iter_objects(node: Any) -> Iterable[Any]:
    """Yield every dict nested anywhere in node (iterative, cycle-safe)."""
    stack = [node]
    seen: set[int] = set()
    while stack:
        current = stack.pop()
        if isinstance(current, dict):
            if id(current) in seen:
                continue
            seen.add(id(current))
            yield current
            stack.extend(current.values())
        elif isinstance(current, list):
            if id(current) in seen:
                continue
            seen.add(id(current))
            stack.extend(current)


def item_metrics(document: Any) -> list[dict[str, float]]:
    """Return one metrics dict per DISTINCT overlay item.

    A script.generate result nests the same run twice (`result.result...` and
    `job.result.result...`, the run document inside the job envelope), so a naive
    walk counts every item twice and doubles the reported total. Items are
    therefore de-duplicated by their full metric tuple, which is what makes this
    a per-ITEM report rather than a per-object-occurrence one.
    """
    items: list[dict[str, float]] = []
    seen: set[tuple[float, ...]] = set()
    for obj in _iter_objects(document):
        if "chronon_job_engine_init_ms" not in obj:
            continue
        values = {
            key: float(obj[key])
            for key in BOOT_KEYS + EXTRA_KEYS
            if isinstance(obj.get(key), (int, float))
        }
        fingerprint = tuple(values.get(key, -1.0) for key in BOOT_KEYS + EXTRA_KEYS)
        if fingerprint in seen:
            continue
        seen.add(fingerprint)
        items.append(values)
    return items


def _percentile(values: list[float], pct: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    if len(ordered) == 1:
        return ordered[0]
    k = (len(ordered) - 1) * pct
    low, high = int(k), min(int(k) + 1, len(ordered) - 1)
    if low == high:
        return ordered[low]
    return ordered[low] * (high - k) + ordered[high] * (k - low)


def _stats(values: list[float]) -> dict[str, float]:
    if not values:
        return {"n": 0.0, "median": 0.0, "p95": 0.0, "max": 0.0, "sum": 0.0}
    return {
        "n": float(len(values)),
        "median": _percentile(values, 0.50),
        "p95": _percentile(values, 0.95),
        "max": max(values),
        "sum": sum(values),
    }


def summarize(items: list[dict[str, float]]) -> dict[str, Any]:
    boot_per_item = [sum(item.get(key, 0.0) for key in BOOT_KEYS) for item in items]
    phases = {key: _stats([item[key] for item in items if key in item]) for key in BOOT_KEYS + EXTRA_KEYS}
    return {
        "items": len(items),
        "per_item_boot_ms": _stats(boot_per_item),
        "phases": phases,
    }


def render_text(report: dict[str, Any]) -> str:
    boot = report["per_item_boot_ms"]
    lines = [
        f"overlay items with Chronon metrics: {report['items']}",
        f"per-item boot (engine_init + backend_init): median {boot['median']:.1f} ms, "
        f"p95 {boot['p95']:.1f} ms, max {boot['max']:.1f} ms, total {boot['sum']:.1f} ms",
        "",
        f"{'phase':<42}{'n':>5}{'median':>10}{'p95':>10}{'max':>10}{'sum':>12}",
    ]
    for key, stat in report["phases"].items():
        lines.append(
            f"{key:<42}{int(stat['n']):>5}{stat['median']:>10.2f}{stat['p95']:>10.2f}"
            f"{stat['max']:>10.2f}{stat['sum']:>12.1f}"
        )
    return "\n".join(lines)


def self_test() -> int:
    """Prove the extractor and the boot aggregate on an inline fixture."""
    fixture = {
        "items": [
            {"render": {"chronon_job_engine_init_ms": 10.0, "chronon_job_backend_init_ms": 150.0,
                        "chronon_job_ffprobe_ms": 12.0}},
            {"render": {"chronon_job_engine_init_ms": 30.0, "chronon_job_backend_init_ms": 250.0,
                        "chronon_job_ffprobe_ms": 14.0}},
            {"unrelated": {"other": 1.0}},
        ],
        # The real results nest the run twice: the same two items must not be
        # counted again, or every total this tool prints is doubled.
        "job": {"result": {"result": {"overlay_render": {"items": [
            {"artifact": {"metrics": {"chronon_job_engine_init_ms": 10.0,
                                      "chronon_job_backend_init_ms": 150.0,
                                      "chronon_job_ffprobe_ms": 12.0}}},
            {"artifact": {"metrics": {"chronon_job_engine_init_ms": 30.0,
                                      "chronon_job_backend_init_ms": 250.0,
                                      "chronon_job_ffprobe_ms": 14.0}}},
        ]}}}},
    }
    items = item_metrics(fixture)
    if len(items) != 2:
        print(f"self-test failed: extracted {len(items)} items, want 2 after de-duplication", file=sys.stderr)
        return 1
    report = summarize(items)
    boot = report["per_item_boot_ms"]
    if boot["n"] != 2 or boot["median"] != 220.0 or boot["max"] != 280.0:
        print(f"self-test failed: boot stats = {boot}", file=sys.stderr)
        return 1
    if report["phases"]["chronon_job_ffprobe_ms"]["sum"] != 26.0:
        print("self-test failed: extra phase aggregation is wrong", file=sys.stderr)
        return 1
    print("self-test OK: 2 items extracted, per-item boot median 220.0 ms, "
          "max 280.0 ms, ffprobe total 26.0 ms")
    return 0


def _load(paths: list[str]) -> list[Any]:
    documents = []
    for raw in paths:
        path = Path(raw)
        candidates = sorted(path.rglob("*.json")) if path.is_dir() else [path]
        for candidate in candidates:
            try:
                with candidate.open("r", encoding="utf-8") as handle:
                    documents.append(json.load(handle))
            except Exception as exc:  # noqa: BLE001 - a bad input must not hide the rest
                print(f"[WARN] {candidate}: {exc}", file=sys.stderr)
    return documents


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("results", nargs="*", help="script.generate result JSON files or directories")
    parser.add_argument("--max-boot-ms", type=float, default=None,
                        help="fail when the MEDIAN per-item boot exceeds this many ms")
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args(argv)

    if args.self_test:
        return self_test()
    if not args.results:
        parser.error("at least one result file is required (or use --self-test)")

    items: list[dict[str, float]] = []
    for document in _load(args.results):
        items.extend(item_metrics(document))
    if not items:
        print("no overlay item Chronon metrics found in the given inputs", file=sys.stderr)
        return 2

    report = summarize(items)
    if args.json:
        print(json.dumps(report, indent=2, sort_keys=True))
    else:
        print(render_text(report))
    if args.max_boot_ms is not None and report["per_item_boot_ms"]["median"] > args.max_boot_ms:
        print(f"per-item boot median {report['per_item_boot_ms']['median']:.1f} ms exceeds "
              f"--max-boot-ms {args.max_boot_ms:.1f} ms", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
