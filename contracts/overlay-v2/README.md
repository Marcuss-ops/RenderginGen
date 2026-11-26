# Overlay contract corpus — RenderingGen vendored snapshot

This directory is the hermetic copy of the cross-repo
`contracts/overlay-v2` corpus used by RenderingGen's unit tests.

- `manifest.json` is the corpus authority for the RenderingGen copy.
- `fixtures/pipelinegen/` contains the PipelineGen post-materialization plans.
- `fixtures/` and `fixtures/brainrust/` contain the synthetic and protocol
  fixtures from the shared corpus.

`TestOverlayContractCorpus` compiles every fixture and pins the exact Chronon
layer digest. It runs against this directory only, so a standalone RenderingGen
CI checkout does not need the PipelineGen or BrainRust siblings.

## Refreshing the snapshot

The workspace copy under `contracts/overlay-v2/` remains the source of truth.
When a producer fixture or a pinned digest changes:

1. Copy the affected fixture bytes from the workspace source into the matching
   path here.
2. Update `layers` / `layers_sha256` in this manifest and in the workspace
   manifest.
3. Run, from `RenderingGen/renderinggen`:

   ```sh
   go test ./internal/overlay -run 'TestOverlayContractCorpus' -count=1
   ```

4. Run the same command with `TestContractCorpusMatchesWorkspaceSources` when
   the full workspace is available. That test fails on stale bytes or metadata
   and names the source and destination paths.

Do not relax `contract_corpus_test.go`'s strict decoder to make a fixture pass.
A producer-only field is either projected by `projectPipelineGenPlan` or
stripped at the producer boundary.
