ChrononMotion3D — Compositing Order Showcase
2026-11-10 · 1920x1080 · 30 fps · 8.0 s

COSA MOSTRA
Una scena con due layer: un oggetto 3D (layer 1, autore per primo) e il testo
CHRONON (layer 2). L'oggetto attraversa il piano del testo due volte, quindi
l'ordine di compositing corretto cambia durante l'animazione.

In modalità Stack (default) l'ordine resta quello di authoring: il testo viene
sempre disegnato sopra l'oggetto.
In modalità Depth l'ordine viene risolto per frame dalla profondità camera:
l'oggetto passa davanti al testo e poi torna dietro.

VIDEO
01_depth_sort_crossing_1080p.mp4
  Depth mode, 8.0 s. Il cubo attraversa il testo due volte. L'ordine risolto
  cambia al frame 55 (1.83 s) e al frame 186 (6.20 s); il badge ORDER FLIP
  segnala i due attraversamenti. In basso sono mostrati ordine e profondità
  reali emessi da CM3D.

02_stack_vs_depth_1080p.mp4
  Split-screen, 8.0 s. Stessa scena e stessa camera: a sinistra Stack (ordine
  autoriale fisso), a destra Depth (ordine camera). Il testo è davanti al cubo
  a sinistra e dietro a destra.

03_text_behind_object_1080p.mp4
  Clean beauty shot, 8.0 s. Depth mode senza pannello dei chip; il testo è
  dietro l'oggetto nella parte centrale dell'animazione.

CONTENUTI
showcase.json         240 campioni per frame: camera, ancore mondo, order_stack,
                      order_depth e profondità, prodotti da resolveCompositeStack.
videos_manifest.json  Dimensioni, durata e SHA-256 dei tre MP4.
contact_sheet.png     Quattro frame di riferimento.

FEATURE
ChrononMotion3D: CompositeMode::Stack (default) e CompositeMode::Depth,
resolveCompositeStack(), composite_order opzionale nel ResolvedFramePlan.
Nessuna modifica a Chronon3d: CM3D risolve spazio, tempo e ordine; i pixel di
questa demo sono una visualizzazione motion-graphics dell'ordine risolto.
