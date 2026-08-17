# RenderingGen — golden canary gate.
#
# GoldenOverlayJobV1 (testdata/golden/golden-overlay-job-v1.json) is the
# permanent end-to-end regression test: the real chain
#
#   queue -> RenderingGen -> Chronon3d -> artifact -> PostgreSQL
#   -> idempotent replay (no new render)
#
# on a 1280x720 @ 30fps / 5s job with a background, an important phrase, an
# important word and an image overlay. Every future change (SoftwareBackend,
# VulkanBackend, CLI, daemon IPC, cold/warm cache, CPU/GPU, new Chronon or
# PipelineGen versions) must keep this job rendering correctly.

CHRONON_RUNTIME ?= ghcr.io/marcuss-ops/chronon3d-runtime:0.1.0

.PHONY: golden-e2e golden-e2e-runtime golden-e2e-reset golden-e2e-down

# Build the chronon3d-runtime image from the real Chronon3d source when it is
# not already present locally. RenderingGen never vendors Chronon; the runtime
# is produced by the Chronon3d repo's own Dockerfile.
golden-e2e-runtime:
	@docker image inspect $(CHRONON_RUNTIME) >/dev/null 2>&1 && echo "runtime image present: $(CHRONON_RUNTIME)" || { \
	  echo "runtime image missing: $(CHRONON_RUNTIME)"; \
	  test -d ../Chronon3d || { echo "ERROR: ../Chronon3d checkout not found (clone Marcuss-ops/Chronon3d beside RenderingGen)"; exit 1; }; \
	  echo "building chronon3d-runtime from ../Chronon3d (first time only, takes a while)..."; \
	  docker build -f ../Chronon3d/docker/chronon-runtime/Dockerfile -t $(CHRONON_RUNTIME) ../Chronon3d; \
	}

# Boot the canonical stack (postgres + queue + objectstore + worker) and run
# the golden canary end-to-end. The stack is torn down afterwards, keeping
# volumes so the next run starts warm (worker cache, object store, PG rows).
golden-e2e: golden-e2e-runtime
	@set -e; cd infra/docker; \
	  echo "=== booting stack ==="; \
	  docker compose up -d --build; \
	  trap 'docker compose down' EXIT; \
	  ../../infra/e2e/run-golden-overlay.sh

# Reset the canary's persisted state (PostgreSQL job rows, object store,
# worker cache). Required after any intentional golden drift or when the
# canary needs to prove a from-scratch render.
golden-e2e-reset:
	cd infra/docker && docker compose down -v

golden-e2e-down:
	cd infra/docker && docker compose down
