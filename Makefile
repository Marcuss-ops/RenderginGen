# RenderingGen — golden canary gate.
#
# GoldenSemanticOverlayJobV1 (testdata/golden/golden-semantic-overlay-job-v1.json)
# is the permanent end-to-end regression test: the real chain
#
#   queue -> RenderingGen -> Chronon3d -> artifact -> PostgreSQL
#   -> idempotent replay (no new render)
#
# on a 1280x720 @ 30fps / 5s job with a background, an important phrase, an
# important word and an image overlay. Every future change (SoftwareBackend,
# VulkanBackend, CLI, daemon IPC, cold/warm cache, CPU/GPU, new Chronon or
# PipelineGen versions) must keep this job rendering correctly.

CHRONON_RUNTIME ?= ghcr.io/marcuss-ops/chronon3d-runtime:0.1.0

.PHONY: native-build golden-e2e golden-e2e-runtime golden-e2e-reset golden-e2e-down test-architecture conformance test-gofmt test-unit test-module-standalone

# test-architecture — the cross-repo boundary conformance gate.
#
# Fails on any NEW occurrence of a forbidden marker (legacy v1/unversioned
# render plan, module-path typo, hardcoded developer home, template aliases,
# template-based kind inference, a second stats pass, *_partNN.go files) and on
# any STALE ratchet-baseline entry. See CONFORMANCE.md.
#
# `go test ./...` already runs it via the internal/architecture package; this
# target is the explicit local entry point.
test-architecture:
	cd renderinggen && go test ./internal/architecture/... -count=1

# conformance — the same scan, without the Go test framing.
#
# test-architecture runs the whole architecture suite (rule self-tests, ratchet
# baseline, CI-shape checks); this runs only the scan, so a pre-commit hook, a
# review step or a human asking "does this tree still violate a boundary rule?"
# gets a direct answer with a normal exit code. It shares the scan code with the
# gate, so the two can never disagree about what counts as a violation; it does
# NOT own the baseline (see cmd/conformance).
conformance:
	cd renderinggen && go run ./cmd/conformance

# refresh-conformance-baseline — explicit ratchet-down after fixing violations.
# Never run this to silence a NEW violation; remove the violation instead.
refresh-conformance-baseline:
	cd renderinggen && UPDATE_CONFORMANCE_BASELINE=1 go test ./internal/architecture/... -count=1

# test-gofmt — formatting gate over every Go module in this repository.
#
# gofmt ships with the Go toolchain, so this is a zero-dependency gate, and CI
# runs the same check in each module job. A formatting drift is not cosmetic
# here: it marks a file that was edited outside the toolchain (or a hand-merged
# patch), which is exactly where an unreviewed semantic change hides in a
# whitespace-only diff.
test-gofmt:
	@fail=0; for m in renderinggen queue objectstore; do \
	  out=$$(cd $$m && gofmt -l .); \
	  if [ -n "$$out" ]; then echo "gofmt would rewrite:"; echo "$$out"; fail=1; fi; \
	done; \
	if [ $$fail -ne 0 ]; then echo "run 'gofmt -w' on the files above"; exit 1; fi; \
	echo "gofmt: clean"

# test-module-standalone — every module builds with the Go workspace DISABLED.
#
# This repository is three modules in one tree, joined by a go.work in the
# WORKSPACE (one level up, shared with the sibling repositories) and by a
# `replace` in renderinggen/go.mod. That combination has a failure mode nothing
# else here catches: a dependency added to one module's go.mod but only ever
# resolved through the workspace builds for everyone who has go.work and fails
# for everyone who does not — a CI job that checks out one module, a consumer
# that vendors it, or `go install` of a single command.
#
# GOWORK=off makes the resolution use each module's own go.mod exactly as an
# outside consumer would, and GOFLAGS= clears any inherited -mod setting so the
# build is not silently rescued by an environment override. It is a BUILD gate,
# not a test gate: the tests legitimately need the workspace (the cross-repo
# contract tests), and duplicating test-unit's minutes here would buy nothing.
test-module-standalone:
	@fail=0; for m in renderinggen queue objectstore; do \
	  echo "=== standalone build: $$m ==="; \
	  (cd $$m && GOWORK=off GOFLAGS= go build ./...) || { \
	    echo "$$m does not build without the go.work; fix its go.mod (missing dependency or replace)"; fail=1; \
	  }; \
	done; \
	if [ $$fail -ne 0 ]; then exit 1; fi; \
	echo "standalone builds: ok"

# test-unit — every module's tests WITHOUT the real-engine runtime
# certification suite (sub-second gate, no GPU, no ffmpeg, no Chronon).
#
# The split exists because the certification suite (real chronon3d_cli renders,
# decoded and pixel-compared) is discovered automatically when a Chronon
# checkout sits beside this repository. On such a machine a bare
# `go test ./...` is minutes of GPU work, not a unit gate — and it is the
# developer machine where that is least expected. The opt-out
# (RENDERINGGEN_SKIP_GPU_E2E, owned by
# renderinggen/internal/overlay/final_certification_runtime_test.go and pinned
# by TestRuntimeCertificationOptOut) makes the distinction explicit instead of
# relying on the binary happening to be absent:
#
#   make test-unit     fast gate, runs everywhere, must always be green
#   go test ./...      also runs the runtime certification when the engine is
#                      present (opt in with CHRONON_BIN to force it)
#
# This target does not duplicate the CI command: CI owns the -race scope
# (pinned by TestCIRunsRaceEnabledModuleTests), this owns the fast local gate.
test-unit:
	@fail=0; for m in renderinggen queue objectstore; do \
	  echo "=== test-unit: $$m ==="; \
	  (cd $$m && RENDERINGGEN_SKIP_GPU_E2E=1 go test -count=1 ./...) || fail=1; \
	done; \
	exit $$fail

native-build:
	go build -o /usr/local/bin/renderinggen-queue ./queue/cmd/queued
	go build -o /usr/local/bin/renderinggen ./renderinggen/cmd/renderinggen

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

# Boot only the infrastructure containers and run the golden canary against
# the native systemd Queue/worker/Chronon services. Application services must
# already be enabled (see README); Docker is not part of their runtime path.
golden-e2e:
	@set -e; cd infra/docker; \
	  echo "=== booting infrastructure ==="; \
	  docker compose up -d postgres objectstore; \
	  trap 'docker compose down' EXIT; \
	  ../../infra/e2e/run-golden-overlay.sh

# Reset the canary's persisted state (PostgreSQL job rows, object store,
# worker cache). Required after any intentional golden drift or when the
# canary needs to prove a from-scratch render.
golden-e2e-reset:
	cd infra/docker && docker compose down -v

golden-e2e-down:
	cd infra/docker && docker compose down
