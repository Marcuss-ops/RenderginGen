# OVL-02 — `chain_segments` tipizzato nel contratto interno

**Stato:** PASS · **Priorità:** P1

## Evidenza

`overlay/chain_segments.go`:

- `decodeChainSegments(params map[string]any, …)` estrae la lista da
  `Params["chain_segments"]` e la decodifica con
  `json.Marshal` → `json.Unmarshal` a ogni compile di chain.
- `chainSequenceCamera` rifà la stessa decode per contare i segmenti camera.
- `segParamsForKind` filtra a mano `runtimeTextStyleKeys` per image/shape/video,
  duplicando la conoscenza di `validateTextRuntimeOverrides`.

Il documento semantic non ha un campo tipizzato: il payload reale
(`ops/jobs/entity_rect_caption_20261119/*-r3-full.json`) mostra che ogni
segmento porta i propri `params`/`style`; il filtro serve perché il producer
scrive chiavi di stile testo anche su segmenti immagine non-captioned.

## Problema

Il tipo esiste già (`chainSegment`) ma il contratto lo tratta come blob. Ogni
consumer re-implementa decode e validazione, e un errore di decode può
comparire in un punto diverso del compile (errore di precedenza instabile).

## Target

`semanticItem.ChainSegments []chainSegment` popolato **una volta** nella fase di
normalizzazione/risoluzione (campo interno `json:"-"`, il wire resta
`params.chain_segments` per non rompere PipelineGen). `compileChainItem`,
`chainSequenceCamera` e i test leggono il campo tipizzato; `decodeChainSegments`
resta solo come fallback di compatibilità chiamato dalla normalizzazione, non
dal lowering.

`segParamsForKind` resta (è compatibilità sul payload del producer) ma smette di
essere duplicazione: si appoggia alla tabella di `validateTextRuntimeOverrides`
invece di rielencare le chiavi.

## Piano

1. Aggiungere `ChainSegments []chainSegment `json:"-"`` a `semanticItem`.
2. In `resolveSemanticItems` (o nello stage di normalizzazione, l'unico mutabile)
   decodificare i segmenti per gli `isChainKind` e validare cardinalità ≥2;
   gli errori mantengono il testo attuale.
3. Sostituire le decode in `compileChainItem` / `chainSequenceCamera`.
4. Estrarre da `runtime_style.go` la funzione che decide se una chiave è
   ammessa per un kind (`textRuntimeKeyAllowedForKind`) e usarla sia nel
   validatore sia in `segParamsForKind`.
5. Test: chain mista con segmento 0 testo e segmento 1 image senza caption
   continua a scartare solo le chiavi vietate; un `chain_segments` malformato
   fallisce con lo stesso errore di prima.

## Criteri di accettazione

1. Nessun `json.Marshal`/`json.Unmarshal` nel percorso di lowering delle chain
   (`grep -n "json.Marshal" chain_segments.go` → solo nel fallback di
   normalizzazione, se resta).
2. La validazione dei segmenti avviene una volta; il lowering non può fallire
   per un decode.
3. La tabella chiavi/kind ha un solo owner.
4. Tutti i test chain (identità, conformance, map, camera-pan, short-hold)
   passano invariati.

## Verifica

```
go test ./internal/overlay/ -run 'Chain|Segment|Runtime' -count=1
go test ./internal/overlay/ ./internal/processor/ -count=1
```

## Esito

- `semanticItem.chainSegments []chainSegment` (`json:"-"`) è l'unico campo;
  `resolveSemanticItems` decodifica una volta con `decodeChainSegments` e lo
  salva (`semantic_compile.go`).
- `chainSegmentsFor` preferisce il campo tipizzato; `decodeChainSegments` resta
  solo per `resolvedItem` costruiti fuori dal pipeline (test/strumenti). Il
  lowering non fa più round-trip JSON.
- `chainSequenceCamera` legge la stessa lista tipizzata.
- `segParamsForKind` usa `textRuntimeKeyAllowedForKind` di `runtime_style.go`:
  un solo owner della tabella chiave/kind.
- Test: `TestChainSegmentsAreDecodedOnceIntoTheItem`,
  `TestChainSegmentsForPrefersTheTypedList`, suite chain/segment/runtime
  invariata.

## Verifica eseguita

```
go test ./internal/overlay/ ./internal/processor/ -count=1
```

## Rischio

La normalizzazione è l'unico stage mutabile dichiarato: se il campo viene
popolato altrove si ricrea il doppio owner. Il ticket lo impone lì.
