#!/usr/bin/env bash
set -euo pipefail

# The queue binary consumes DATABASE_URL, while the shared PipelineGen
# environment owns the canonical media PostgreSQL DSN under
# PIPELINEGEN_MEDIA_POSTGRES_DSN. Keep the secret in the operator-owned .env;
# this wrapper only maps the names at process start and never copies the value
# into the repository or a world-readable unit file.
set -a
source /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/.env
set +a

: "${PIPELINEGEN_MEDIA_POSTGRES_DSN:?PIPELINEGEN_MEDIA_POSTGRES_DSN is required}"
export DATABASE_URL="${PIPELINEGEN_MEDIA_POSTGRES_DSN}"
exec /usr/local/bin/renderinggen-queue "$@"
