# Base image: Chronon GPU runtime (Vulkan), versioned independently.
FROM ubuntu:24.04

ARG CHRONON_VERSION=0.9.4
ENV CHRONON_HOME=/opt/chronon \
    DEBIAN_FRONTEND=noninteractive

# Vulkan + GPU runtime libraries (no PyTorch/CUDA required).
RUN apt-get update && apt-get install -y --no-install-recommends \
        libvulkan1 \
        vulkan-tools \
        mesa-vulkan-drivers \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY chronon/bin /opt/chronon/bin
COPY chronon/lib /opt/chronon/lib
COPY chronon/shaders /opt/chronon/shaders
COPY chronon/VERSION /opt/chronon/VERSION

LABEL org.opencontainers.image.title="chronon-gpu-runtime" \
      org.opencontainers.image.version="${CHRONON_VERSION}"
