# Base image: Chronon GPU runtime (Vulkan), versioned independently.
#
# Multi-stage: Chronon is compiled from source inside the builder stage, so the
# image is reproducible and never requires a manual install on the GPU host.

# --- builder: compile Chronon ---
FROM ubuntu:24.04 AS chronon-builder
ARG CHRONON_VERSION=0.9.4
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
        cmake \
        g++ \
        make \
        libvulkan-dev \
    && rm -rf /var/lib/apt/lists/*

COPY chronon/ /src/chronon/
RUN cmake -S /src/chronon -B /src/chronon/build \
        -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_INSTALL_PREFIX=/opt/chronon \
    && cmake --build /src/chronon/build -j"$(nproc)" \
    && cmake --install /src/chronon/build

# --- runtime ---
FROM ubuntu:24.04
ARG CHRONON_VERSION=0.9.4
ENV CHRONON_HOME=/opt/chronon \
    LD_LIBRARY_PATH=/opt/chronon/lib \
    DEBIAN_FRONTEND=noninteractive

# Vulkan + GPU runtime libraries (no PyTorch/CUDA required).
RUN apt-get update && apt-get install -y --no-install-recommends \
        libvulkan1 \
        vulkan-tools \
        mesa-vulkan-drivers \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=chronon-builder /opt/chronon /opt/chronon
COPY chronon/VERSION /opt/chronon/VERSION

LABEL org.opencontainers.image.title="chronon-gpu-runtime" \
      org.opencontainers.image.version="${CHRONON_VERSION}"
