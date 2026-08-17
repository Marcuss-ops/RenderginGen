#!/bin/sh
# Worker entrypoint: warm-render shell.
#
# The RenderingGen worker renders through a persistent Chronon3d daemon (the
# warm engine) instead of spawning a cold CLI subprocess per job. The daemon
# keeps the RenderEngine, font cache, image cache and framebuffer/surface
# pools alive between jobs — the same workload renders faster on every job
# after the first (see daemon_vs_cli_benchmark_integration_test.go).
#
# Lifecycle: start daemon -> wait for socket -> exec worker (SIGTERM reaches
# the worker; the daemon is left to the container's process-1 semantics).
set -eu

SOCKET_PATH="${CHRONON_SOCKET_PATH:-/var/run/chronon3d/chronon.sock}"
DAEMON_BIN="${CHRONON_DAEMON_BIN:-/opt/chronon3d/bin/chronon3d_cli}"
ASSETS_ROOT="${CHRONON_DAEMON_ASSETS_ROOT:-/var/lib/renderinggen/jobs}"
DAEMON_LOG="${CHRONON_DAEMON_LOG:-/tmp/chronon-daemon.log}"

SOCKET_DIR="$(dirname "${SOCKET_PATH}")"
mkdir -p "${SOCKET_DIR}"

# Remove any stale socket left by a previous daemon (e.g. after a
# `docker compose stop/start`).  The readiness check below keys off the socket
# file's existence, so a leftover socket would make the worker start before
# the new daemon is actually listening and fail every render with
# "connection refused" (Test 9).
rm -f "${SOCKET_PATH}"

# Start the warm render shell (persistent caches, fonts and pools). The
# per-job assets root is passed per render request, so the fixed startup root
# only preps the engine.
"${DAEMON_BIN}" daemon -s "${SOCKET_PATH}" -a "${ASSETS_ROOT}" \
  > "${DAEMON_LOG}" 2>&1 &
DAEMON_PID=$!

# Wait for the IPC socket (bounded; the worker must never race the daemon).
i=0
while [ ! -S "${SOCKET_PATH}" ]; do
  i=$((i + 1))
  if [ "${i}" -ge 100 ]; then
    echo "worker-entrypoint: chronon daemon socket ${SOCKET_PATH} did not appear" >&2
    tail -50 "${DAEMON_LOG}" >&2 || true
    kill "${DAEMON_PID}" 2>/dev/null || true
    exit 1
  fi
  sleep 0.2
done

echo "worker-entrypoint: chronon daemon ready (pid ${DAEMON_PID}, socket ${SOCKET_PATH})"

exec /usr/local/bin/renderinggen --config /etc/renderinggen/config.yaml
