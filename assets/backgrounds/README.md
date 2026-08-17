# Curated video backgrounds

These six backgrounds were downloaded from the supplied Google Drive files and
normalized for RenderingGen. The original files contained an AAC audio stream;
the checked-in versions are video-only (`-an`) so they are safe for Chronon's
`VIDEO_BACKGROUND` layer and preserve the master voiceover/audio contract.

Every file is 1920×1080, H.264, 30 fps, 15 seconds, and has zero audio streams.
`manifest.json` is the source-of-truth for the original Drive file ID and the
content hash of the normalized asset.

To use one in a semantic plan, reference it as an asset and select the
`VIDEO_BACKGROUND` template:

```json
{
  "id": "background",
  "template_id": "VIDEO_BACKGROUND",
  "start_ms": 0,
  "end_ms": 15000,
  "asset_refs": [
    {
      "asset_id": "drive-background-01",
      "url": "assets/backgrounds/drive-background-01.mp4",
      "sha256": "d4e9d76563c0f17589a0e98795574ceec22b3f3b961f51d44eb0544e95e1639b",
      "media_type": "video/mp4"
    }
  ]
}
```

The worker still materializes assets through the content-addressed object
store. These files are the curated source fixtures; production jobs should
upload the same bytes under their manifest hashes before enqueueing a job.
