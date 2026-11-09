# Newspaper pages — Linux RenderPlan V3 (2026-11-08)

4 scene "foglio di giornale" ricostruite nativamente in Linux come RenderPlan V3
(ChrononTemplate/tools/newspaper/), senza Blend2D/browser.

Tutti i video: **1920×1080, 30 fps, H.264 yuv420p, ≤10 s**.

| # | File | Durata | Frame |
|---|------|--------|-------|
| 01 | 01_newspaper_front_page_1920x1080_6s.mp4 | 6 s | 180 |
| 02 | 02_newspaper_dossier_1920x1080_5s.mp4 | 5 s | 150 |
| 03 | 03_newspaper_highlighter_1920x1080_5s.mp4 | 5 s | 150 |
| 04 | 04_newspaper_memo_1920x1080_5s.mp4 | 5 s | 150 |

- `00_contact_sheet_1920x1080.png` — 2×2 still a 3,5 s
- `*_still.png` — still singola per video
- `verification.tsv` — manifest codec/risoluzione/frame (da ChrononTemplate/out/newspaper_pages/)
- `upload_manifest.json` — ID/link Drive dopo l'upload
- `upload_newspaper_pages.py` — script di upload (pattern RenderingGen/bin/drive-upload)

Sorgenti: ChrononTemplate/assets/newspaper/pages/*.png, plan in ChrononTemplate/out/newspaper_pages/plans/.
Rigenerare tutto: `bash ChrononTemplate/tools/newspaper/render_newspaper_pages.sh`
