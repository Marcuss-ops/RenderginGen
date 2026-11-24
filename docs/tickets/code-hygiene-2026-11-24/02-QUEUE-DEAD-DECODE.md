# RG-HYG-02 — Funzioni morte in `queue/internal/repository/postgres`

- **Stato:** CLOSED (2026-11-24) — `decodeAssets` e `poisonCorruptClaim` rimosse; i due poison vivi restano invariati. Seconda passata: nessun test DB dedicato ai poison, verifica per ispezione e test del modulo queue verdi.
- **Priorità:** P2
- **Repo:** RenderingGen (`queue` module)

## Evidenza

`queue/internal/repository/postgres/decode.go` definisce:

- `decodeAssets` (riga 48): 0 chiamanti nel repo; il read path usa
  `decodeInputManifest` direttamente.
- `poisonCorruptClaim` (riga 106): 0 chiamanti; il claim usa
  `poisonCorruptJob` da `claim.go`, che è vivo.

Il gate `make deadcode` considera solo il modulo `renderinggen`, quindi queste
funzioni nel modulo `queue` non vengono intercettate.

## Criteri di accettazione

- [x] `decodeAssets` e `poisonCorruptClaim` rimosse.
- [x] `poisonCorruptJob` e `poisonCorruptRead` restano invariati e i loro
      chiamanti non sono cambiati. Il percorso di decode corrotto resta
      fail-closed. I test postgres del modulo sono unit-level: non esiste un
      test DB dedicato ai due poison, quindi questo punto è verificato per
      ispezione, non per esecuzione.
- [x] `go build ./...`, `go vet ./...` e `go test ./...` nel modulo `queue`
      verdi (2026-11-24).

## Seconda passata (2026-11-24)

- `poisonCorruptRead` è un metodo su `*Repository`; il primo pattern di
  ricerca (`func poisonCorruptRead`) non lo catturava. È ancora chiamato da
  `repository.go:684` e `repository.go:688`.
- Il commento che citava `poisonCorruptClaim` è stato aggiornato: la funzione
  rimossa non è più un riferimento nel codice.
- Nessun file `_test.go` del modulo referenzia i due poison. Il test
  `prepared_render_package_test.go` copre `decodeInputManifest` sul percorso
  valido; la rimozione non cambia quel percorso.
