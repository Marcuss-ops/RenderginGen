#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
remote_url=$(git -C "$repo_root" remote get-url origin)
repo_name=${remote_url##*/}
repo_name=${repo_name%.git}
expected_repo=RenderingGen
expected_module=github.com/Marcuss-ops/RenderingGen

if [[ "$repo_name" != "$expected_repo" ]]; then
  printf 'repository identity mismatch: origin is %q, expected %q\n' "$repo_name" "$expected_repo" >&2
  exit 1
fi

for module in queue renderinggen objectstore; do
  module_path=$(sed -n 's/^module[[:space:]]\+//p' "$repo_root/$module/go.mod" | head -n 1)
  expected_path="$expected_module/$module"
  if [[ "$module_path" != "$expected_path" ]]; then
    printf '%s/go.mod declares %q; expected %q\n' "$module" "$module_path" "$expected_path" >&2
    exit 1
  fi
  if grep -Eq '^replace[[:space:]].*=>[[:space:]]*\.\./queue([[:space:]]|$)' "$repo_root/$module/go.mod"; then
    printf '%s/go.mod contains a local Queue replace; published consumers cannot use it\n' "$module" >&2
    exit 1
  fi
done

printf 'module identity OK: %s and %s/{queue,renderinggen,objectstore}\n' "$remote_url" "$expected_module"
