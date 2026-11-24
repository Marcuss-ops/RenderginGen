# RG-HYG-04 — Binari, dump, media tracciati e font duplicato

- **Stato:** CLOSED (2026-11-24) — artefatti locali rimossi, media/raw non più tracciati, font duplicato mantenuto con motivazione.
- **Priorità:** P2
- **Repo:** RenderingGen

## Evidenza

- Binari ignorati ma presenti nel working tree: `bin/` (~148 MB),
  `renderinggen/bin/` (~245 MB), `renderinggen/renderinggen` (39 MB),
  `renderinggen/drive-upload` (22 MB), `renderinggen/overlay.test` (12 MB),
  `renderinggen/chunk-overlap-bench` (4,4 MB), `renderinggen/out/` (22 MB),
  `objectstore/objectstore`; log `renderinggen/.p4-*.log`.
- `Upload/` + `UploadDrive/`: 176 file ancora tracciati in Git (il `.gitignore`
  li ignora già); `hdr_bokeh_focus_pull_20261108/` è nella stessa classe.
- Due framebuffer `.raw` tracciati sotto
  `evidence/modernmap1-trim-vulkan-20261122/` (8,3 MB + 524 KB).
- `assets/fonts/Inter-Bold.ttf` e `renderinggen/assets/fonts/Inter-Bold.ttf`
  hanno lo stesso MD5 (`8c9110ec...`).

## Criteri di accettazione

- [x] Gli artefatti locali ignorati sono stati rimossi (`bin/`, `renderinggen/bin/`,
      `overlay.test`, `chunk-overlap-bench`, `out/`, log `.p4-*`,
      `objectstore/objectstore`, `hdr_bokeh_focus_pull_20261108/`).
- [x] I media generati non sono più tracciati (`git ls-files` → 0 per
      `Upload/` e `UploadDrive/`) ma restano sul disco.
- [x] I due `.raw` non sono più tracciati; `.gitignore` ha la regola specifica
      `/evidence/**/*.raw`.
- [x] Il font duplicato resta: i due path sono radici asset distinte usate dal
      modulo e dal repo. Un symlink romperebbe il runtime standalone/Windows;
      la deduplica richiede un cambio di risoluzione asset, tracciato come
      follow-up, non una cancellazione silenziosa.
- [x] `git status` non mostra più artefatti generati non intenzionali.
