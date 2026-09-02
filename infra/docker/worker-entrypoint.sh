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
# Backend selection for the warm chronon daemon:
#   1) CHRONON_DAEMON_BACKEND (explicit, wins)
#   2) CHRONON_BACKEND (shared env var used by the worker config)
#   3) Auto-detect: vulkan if /dev/nvidia0 is present, else software
#   4) Default: vulkan (matches profile: gpu-vulkan-native in the worker config)
# Previously the default was "software", which silently recorded
# render_artifacts.backend='software' even when the worker's chronon.profile
# was gpu-vulkan-native — i.e. the configured GPU profile never actually ran.
DAEMON_BACKEND="${CHRONON_DAEMON_BACKEND:-${CHRONON_BACKEND:-}}"
if [ -z "${DAEMON_BACKEND}" ]; then
  if [ -e /dev/nvidia0 ]; then
    DAEMON_BACKEND="vulkan"
    DAEMON_BACKEND_SOURCE="auto-detect (/dev/nvidia0)"
  else
    DAEMON_BACKEND="software"
    DAEMON_BACKEND_SOURCE="auto-detect (no /dev/nvidia0)"
  fi
else
  DAEMON_BACKEND_SOURCE="env override (CHRONON_DAEMON_BACKEND or CHRONON_BACKEND)"
fi
DAEMON_LOG="${CHRONON_DAEMON_LOG:-/tmp/chronon-daemon.log}"

# Log the resolved daemon backend so a misconfigured container surfaces the
# problem in `docker logs` immediately instead of silently running the wrong
# backend and recording it in render_artifacts after the fact. The earlier
# silent default ("software") caused render_artifacts.backend='software' even
# when the worker's profile was gpu-vulkan-native.
echo "worker-entrypoint: chronon daemon backend = ${DAEMON_BACKEND} (source: ${DAEMON_BACKEND_SOURCE})"

SOCKET_DIR="$(dirname "${SOCKET_PATH}")"
mkdir -p "${SOCKET_DIR}"

# On dedicated GPU nodes Chronon is a host systemd service. RenderingGen keeps
# container isolation but connects to the already-warm host daemon over IPC.
if [ "${CHRONON_EXTERNAL_DAEMON:-0}" = "1" ]; then
  echo "worker-entrypoint: using external host Chronon daemon at ${SOCKET_PATH}"
  i=0
  while [ ! -S "${SOCKET_PATH}" ]; do
    i=$((i + 1))
    if [ "${i}" -ge 100 ]; then
      echo "worker-entrypoint: external Chronon socket ${SOCKET_PATH} did not appear" >&2
      exit 1
    fi
    sleep 0.2
  done
  echo "worker-entrypoint: external Chronon daemon ready"
  exec /usr/local/bin/renderinggen --config /etc/renderinggen/config.yaml
fi

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
  --backend "${DAEMON_BACKEND}" \
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
