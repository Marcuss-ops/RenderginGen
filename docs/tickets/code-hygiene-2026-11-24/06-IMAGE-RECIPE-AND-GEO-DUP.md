# RG-DRY-01 — `imageRecipeEffectID` e `resolveFeature` duplicati

- **Stato:** CLOSED (2026-11-24) — `motion.ImageRecipeEffectID` e `geofeatures.Registry.Resolve` sono l'unica implementazione; i test mirati passano.
- **Priorità:** P2
- **Repo:** RenderingGen (`renderinggen` module)

## Evidenza

1. **SSOT violata:** `imageRecipeEffectID(kind string)` è identica in
   `internal/overlay/premium_image_recipe.go:223` e
   `internal/motion/validate_image_motion.go:89`. `overlay` importa già
   `motion`.
2. **Feature envy:** `resolveFeature(registry, selector)` è identica in
   `internal/overlay/map_camera_fit.go:58` e
   `internal/tacticalfill/tacticalfill.go:304`. La logica è del registry:
   `geofeatures.Registry` deve possedere la risoluzione `country:`/`admin1:`/
   `world` vs nome.

## Fix richiesto

- [x] `motion.ImageRecipeEffectID` esportata; overlay la usa e la copia locale
      è sparita.
- [x] `(*geofeatures.Registry).Resolve(selector string)` implementato; overlay e
      tacticalfill lo chiamano.
- [x] Test: `Resolve` copre ID canonico, `world`, nome e selettore sconosciuto;
      motion/overlay/tacticalfill verdi (2026-11-24).
