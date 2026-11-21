# OVL-05 — Un solo entry point per `image_backdrop`

**Stato:** PASS · **Priorità:** P2

## Evidenza

`internal/overlay/image_backdrop.go` espone tre entry point quasi identici:

- `addSingleImageBackdrop` (foto singola / entity card),
- `addCompositeImageBackdrops` (un backdrop per member, o uno condiviso se
  `asset_id` esplicito),
- `addBackdropGrid` (griglia full-canvas, chiamata da entrambi).

La scelta single/composite è replicata in `compileImageItem` e
`compileEntityCard`, che decidono in base a `len(Item.ImageLayers)`.

## Problema

Tre porte per una sola regola: "per ogni asset visibile, un backdrop; più la
griglia una volta". Ogni nuova sorgente immagine deve ricordarsi quale porta
chiamare, e il ramo `explicitAsset` è implementato due volte.

## Target

`addImageBackdrops(ri, src, registry, layers) ([]Layer, error)` unico:

- `explicitAsset` → un backdrop per l'asset dichiarato;
- item senza member → backdrop dell'asset singolo;
- item con member → un backdrop per asset distinto, nell'ordine dei layer;
- griglia aggiunta esattamente una volta in coda.

Le due funzioni attuali diventano wrapper temporanei solo se un chiamante
esterno le usa; altrimenti si cancellano.

## Piano

1. Unificare in `addImageBackdrops` con la mappa `seen` già presente.
2. `finalizeImageLayers(backdrops=true)` chiama solo quella.
3. `compileEntityCard` chiama la stessa funzione (oggi chiama la single) e
   `finalizeImageLayers` passa `backdrops=false`.
4. Test: singolo, composito 2 member, asset esplicito, griglia una sola volta,
   ordine backdrop-prima-dei-layer.

## Criteri di accettazione

1. Esiste una sola funzione che appende backdrop.
2. Un item con 2 member e `image_backdrop.grid` produce 2 backdrop + 1 griglia.
3. Un item con `asset_id` esplicito produce 1 backdrop condiviso.
4. I test backdrop esistenti passano senza modifiche di aspettativa.

## Verifica

```
go test ./internal/overlay/ -run 'Backdrop|Composite|Entity' -count=1
```

## Esito

- `addImageBackdrops(ri, src, registry, layers)` è l'unica funzione che appende
  backdrop: asset esplicito → un backdrop condiviso; singolo → il suo backdrop;
  composito → un backdrop per asset distinto; griglia esattamente una volta in
  coda. `addSingleImageBackdrop`, `addCompositeImageBackdrops` e
  `addBackdropGrid` non esistono più.
- `finalizeImageLayers(..., backdrops=true)` e `compileEntityCard` passano
  dalla stessa porta; il secondo usa `backdrops=false` per non duplicare.
- Test: `TestImageBackdropGridIsEmittedOnceForComposite` più i test backdrop
  esistenti invariati (ordine backdrop→layer pinnato).

## Verifica eseguita

```
go test ./internal/overlay/ -run 'Backdrop|Composite|Entity' -count=1
```

## Rischio

L'ordine dei layer è osservabile (il backdrop viene anteposto al layer
fotografico): il test lo pinna.
