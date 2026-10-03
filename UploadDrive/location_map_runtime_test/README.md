# Location map runtime test

Verified local GPU-composited map clip: Reykjavík, Iceland. The map zooms toward the city and the 3D pin and label remain aligned. Output: 1920×1080, H.264, 24 fps, 2.625 s.

The requested three-location, ~300-word runtime test did not complete. The runtime location planner reported three geocoded/matched candidates but emitted only two map items (Reykjavík and Tokyo; Cape Town was absent from the match diagnostics). The native Vulkan/NVENC renderer then lost its Vulkan device while waiting for the first frame fence, before producing a video. The parent job was cancelled to stop automatic retries. No three-location MP4 was produced or uploaded to Drive.

The single-location clip and contact sheet are staged here for inspection; this folder is local and is not a Drive upload.
