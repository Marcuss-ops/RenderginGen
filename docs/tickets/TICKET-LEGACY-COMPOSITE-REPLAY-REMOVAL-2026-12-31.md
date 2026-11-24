# TICKET — Ritiro del replay composite legacy

**Stato:** OPEN — programmato
**Priorità:** P2
**Owner:** RenderingGen overlay
**Deadline:** 2026-12-31
**Origine:** [`docs/operations/legacy-composite-replay-window.md`](../operations/legacy-composite-replay-window.md); OVL-04/OVL-08.

## Perché rimuoverlo

`compileLegacyCompositeImageLayers` è l'unico path di replay per i piani sealed
che precedono `group_motion_id`. È ammesso solo con
`legacy_composite_replay=true`; un piano nuovo con `image_layers` e senza
`group_motion_id` fallisce chiuso.

Il census del 2026-11-18 su RenderingGen, PipelineGen e BrainRust ha trovato
**zero** piani in-repo con `image_layers` non vuoto. La finestra dichiarata
scade il **2026-12-31**. Tenerla oltre la scadenza significa mantenere due
semantiche di motion per-child e un marker wire che nessun producer nuovo usa.

## Programma

| Data | Azione |
|---|---|
| 2026-12-01 | Census finale su RenderingGen, PipelineGen, BrainRust e artefatti sealed esterni. Se un piano esterno richiede ancora per-child motion, va documentato qui con owner e nuova data: non si proroga in silenzio. |
| 2026-12-15 | PR di rimozione: codice, contratto, fixture, test e documentazione. |
| 2026-12-31 | Deadline. Dopo questa data il marker non è più accettato. |

## Censimento

```sh
# piani con image_layers nel repository
grep -rn '"image_layers"' --include='*.json' --include='*.go' . | grep -v _test

# marker e simboli del replay
grep -rn 'legacy_composite_replay\|compileLegacyCompositeImageLayers\|LegacyComposite' \
  --include='*.go' --include='*.json' --include='*.md' . | grep -v _test
```

Il census del 2026-11-18 è la baseline; va ripetuto il 2026-12-01 sugli
artefatti sealed esterni, che non sono in questo checkout.

## Cosa rimuovere

### Codice di produzione

- `compileLegacyCompositeImageLayers` in
  `renderinggen/internal/overlay/semantic_image_compile.go` e il dispatch che
  la chiama in `compileMultiImageItem`;
- il campo `LegacyCompositeReplay` del piano
  (`renderinggen/internal/overlay/semantic_contract_types.go`) e i flag
  `LegacyCompositeImage`/`LegacyCompositeReplay` in
  `renderinggen/internal/overlay/semantic_compile.go`;
- i contatori `LegacyCompositeImageCount`/`LegacyCompositeReplayCount` in
  `renderinggen/internal/overlay/stats.go`;
- le metriche `LegacyCompositeImageItems`/`LegacyCompositeImageReplay` in
  `renderinggen/internal/metricnames/metricnames.go` e il loro uso in
  `renderinggen/internal/processor/prepare_stages.go`;
- il campo `legacy_composite_replay` da
  `contracts/overlay-plan.v1.schema.json`;
- la pagina `docs/operations/legacy-composite-replay-window.md`.

### Test e fixture

- `renderinggen/internal/overlay/legacy_composite_replay_test.go` (contatori
  `1/1`) va rimosso con il path che verifica;
- `TestLegacyCompositeStillCompilesPerChildMotion`
  (`group_motion_compile_test.go`) va rimosso o convertito al path
  `group_motion_id`;
- `TestSizedCompileMatchesThePostFitOnACompositeGroup` (`sized_compile_test.go`)
  va **convertito** al path `group_motion_id`, preservando il confronto
  sized/post-fit: è l'unica copertura di quella combinazione;
- i fixture che impostano `legacy_composite_replay: true` solo per admission
  vanno ripuliti: se il caso è moderno, deve dichiarare `group_motion_id`; se
  testa esplicitamente il replay, sparisce con il path. Il censimento attuale
  include almeno `image_effects_runtime_test.go`, `semantic_sfx_test.go`,
  `image_clip_parity_test.go`, `selection_model_test.go`,
  `image_group_camera_pan_test.go`, `image_premium_v1_test.go`,
  `entity_payload_font_precedence_test.go`, `composite_image_runtime_test.go`,
  `chronon_plan_validation_test.go`, `composite_three_image_runtime_test.go`,
  `semantic_image_grid_dedupe_test.go`, `image_backdrop_members_test.go`,
  `image_effects_paths_test.go`, `overlay_v3_catalog_test.go` e
  `group_motion_compile_test.go`.

La rimozione dei fixture è la parte più ampia: va fatta in una PR dedicata, non
insieme a un refactor del compiler.

## Criteri di accettazione

- [ ] `grep -rn 'legacy_composite_replay\|compileLegacyCompositeImageLayers\|LegacyComposite'`
      non trova più riferimenti di produzione; restano solo eventuali test
      negativi che verificano il rifiuto.
- [ ] Un piano con `legacy_composite_replay=true` viene rifiutato con un errore
      esplicito (campo sconosciuto o marker rimosso), non ignorato in silenzio.
- [ ] Schema e contract test aggiornati; i tre mirror restano coerenti.
- [ ] `go build ./...`, `go vet`, `go test ./internal/overlay/ ./internal/processor/`,
      `make test-unit` e `make deadcode` verdi.
- [ ] `docs/operations` aggiornato: la finestra è chiusa, non prorogata.
- [ ] Nessun golden rigenerato senza una motivazione scritta nel commit.

## Rischi

- **Artefatto sealed esterno non censito.** Il render fallisce con un errore che
  nomina il marker; il census del 2026-12-01 è la mitigazione. Non si tiene il
  path “per sicurezza”: sarebbe una proroga di fatto.
- **Fixture diffusi.** Il marker è usato in molti test come scorciatoia di
  admission: la PR deve convertire i casi moderni a `group_motion_id`, non
  limitarsi a cancellare la stringa.
- **Copertura sized/post-fit.** `TestSizedCompileMatchesThePostFitOnACompositeGroup`
  va convertito, non eliminato: senza di lui la parità sized/post-fit del
  composite non è più pinnata.
