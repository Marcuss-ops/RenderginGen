#!/usr/bin/env bash
#
# Regenerate (or verify) the ChrononTemplate catalog embedded by the worker.
#
# ChrononTemplate owns the motion/preset vocabulary; RenderingGen embeds the
# artifact it emits, so the two repositories cannot drift silently:
#
#   ChrononTemplate/catalog/motion_catalog.v1.json   data: ids, keyframes, selections
#   ChrononTemplate/tools/emit_catalog.cpp           validation + the C++-owned lists
#                    │  chronontemplate_emit_catalog
#                    ▼
#   renderinggen/internal/motion/catalog/chronontemplate_catalog.v1.json   ← embedded
#
# Usage:
#   scripts/sync_motion_catalog.sh            regenerate the embedded artifact
#   scripts/sync_motion_catalog.sh --check    fail (exit 1) if it is stale
#
# Environment:
#   CHRONONTEMPLATE_DIR        checkout of ChrononTemplate (default: ../ChrononTemplate)
#   CHRONONTEMPLATE_BUILD_DIR  cmake build tree for the emitter
#                              (default: $CHRONONTEMPLATE_DIR/build/catalog)
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
template_dir="${CHRONONTEMPLATE_DIR:-$repo_root/../ChrononTemplate}"
build_dir="${CHRONONTEMPLATE_BUILD_DIR:-$template_dir/build/catalog}"
target="renderinggen/internal/motion/catalog/chronontemplate_catalog.v1.json"
embedded="$repo_root/$target"

check_only=0
case "${1:-}" in
    "") ;;
    --check) check_only=1 ;;
    *)
        echo "usage: $0 [--check]" >&2
        exit 2
        ;;
esac

if [[ ! -f "$template_dir/CMakeLists.txt" ]]; then
    echo "sync_motion_catalog: no ChrononTemplate checkout at $template_dir" >&2
    echo "  set CHRONONTEMPLATE_DIR to one, or clone it beside this repository." >&2
    exit 2
fi

# The emitter is a development tool: configure without the module's test suite so
# regenerating the catalog never depends on building tests.
if [[ ! -f "$build_dir/CMakeCache.txt" ]]; then
    cmake -S "$template_dir" -B "$build_dir" \
        -DCHRONONTEMPLATE_BUILD_TESTS=OFF \
        -DCHRONONTEMPLATE_BUILD_CATALOG_TOOL=ON >&2
fi
cmake --build "$build_dir" --target chronontemplate_emit_catalog >&2

emitted="$(mktemp)"
trap 'rm -f "$emitted"' EXIT
"$build_dir/chronontemplate_emit_catalog" >"$emitted"

# Compare parsed documents, not bytes: the emitter owns the format and a
# whitespace-only difference is not drift.
normalize() {
    python3 -c 'import json,sys; print(json.dumps(json.load(open(sys.argv[1])), indent=2, sort_keys=True))' "$1"
}

if [[ "$check_only" == "1" ]]; then
    if [[ ! -f "$embedded" ]]; then
        echo "sync_motion_catalog: $target is missing; run $0" >&2
        exit 1
    fi
    if ! diff -u <(normalize "$embedded") <(normalize "$emitted") >&2; then
        echo "sync_motion_catalog: $target is stale; run $0 to refresh it" >&2
        exit 1
    fi
    echo "sync_motion_catalog: $target is in sync with $template_dir"
    exit 0
fi

mkdir -p "$(dirname "$embedded")"
cp "$emitted" "$embedded"
echo "sync_motion_catalog: wrote $target"
