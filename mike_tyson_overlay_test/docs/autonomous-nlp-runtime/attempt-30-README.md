# Mike Tyson autonomous NLP runtime — v25 cache reuse

Runtime job: `job_1789586291389766129_29f0d51e` — SUCCEEDED.

- The cache-aware path reduced new image uploads from 32 in v24 to 1 in v25 (96.875% fewer).
- 28 selected image appearances used 9 unique content hashes; each repeated hash resolved to one Drive link.
- Five entity-image overlays were planned. The renderer completed all 21 overlays and reported no failed items.
- PipelineGen published the MP4 files under the script's `en/overlay` Drive folder. The v25 Google Doc contains 10 per-scene timing JSON links.
- The autonomous NLP produced 9 phrase overlays, including one duplicate phrase; the run therefore remains one short of the earlier target of 10 unique phrases.

The videos are in the automatically populated [script/en/overlay Drive folder](https://drive.google.com/drive/folders/1-SAYr2lss0NpePggqL3g5IBwOxYAQ2LY); the generated [Google Doc](https://docs.google.com/document/d/1TPk70lt1WdDzdmBeBX85vT3nI_-YewnRID63sWNgofg/edit) links to each scene's voiceover and timing JSON/SRT/VTT.

The exact timing, GPU counters, per-overlay artifact links, image hashes and cache comparison are in [attempt-30-timing.json](attempt-30-timing.json). The source job payload and complete runtime response are in `../../results/autonomous-nlp-runtime/attempt-30-request.json` and `../../results/autonomous-nlp-runtime/attempt-30-full.json`.

Verification performed: `go build` succeeded, followed by one live PipelineGen runtime job. No automated test suite was run.
