#!/usr/bin/env bash
set -euo pipefail

# The queue binary consumes DATABASE_URL, while the shared PipelineGen
# environment owns the canonical media PostgreSQL DSN under
# PIPELINEGEN_MEDIA_POSTGRES_DSN. Keep the secret in the operator-owned .env;
# this wrapper only maps the names at process start and never copies the value
# into the repository or a world-readable unit file.
#
# WHICH .env is operator-deployed state, never a property of this carrier: a
# hardcoded checkout path (the historical `/home/<operator>/.../refactored/.env`)
# is a machine-specific path that only exists on one host, which is why the
# architecture gate `hardcoded_home_path` forbids it in ANY carrier — not only
# Go sources. The unit that starts this wrapper names the file through
# RENDERINGGEN_ENV_FILE (see renderinggen-queue.service); the default below is
# the deployment-owned location, not a developer's home.
env_file="${RENDERINGGEN_ENV_FILE:-/etc/renderinggen/queue.env}"

if [[ ! -r "$env_file" ]]; then
  echo "renderinggen-queue-wrapper: cannot read the environment file: $env_file" >&2
  echo "set RENDERINGGEN_ENV_FILE (or install the file at the default path)" >&2
  exit 1
fi

set -a
# shellcheck source=/dev/null
. "$env_file"
set +a

: "${PIPELINEGEN_MEDIA_POSTGRES_DSN:?PIPELINEGEN_MEDIA_POSTGRES_DSN is required}"
export DATABASE_URL="${PIPELINEGEN_MEDIA_POSTGRES_DSN}"
exec /usr/local/bin/renderinggen-queue "$@"
