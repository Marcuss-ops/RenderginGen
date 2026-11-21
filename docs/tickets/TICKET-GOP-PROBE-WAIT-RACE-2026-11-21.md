# GOP probe: `Wait()` concorrente chiude il pipe del decoder

**Stato:** CLOSED (2026-11-21) — fix verificato su tutti i criteri · **Priorità:** P0
**Origine:** `go test ./...` dopo la wave overlay-decoupling; fallimento
riprodotto anche da solo con `-count=5`.

## Evidenza

```
--- FAIL: TestCanonicalizeAssemblyV1EmitsPinnedStreamAndKeepsAudio (3.57s)
    canonical_assembly_test.go:126: canonical output does not satisfy V1:
    profile VELOX_ASSEMBLY_READY_V1: closed GOP uncertifiable
    (the sync-sample table could not be observed)

media: ffprobe /tmp/.../clip.mp4: closed-GOP probe decode failed:
    read |0: file already closed (closed_gop=false, uncertifiable)
```

Il piano era canonico (l'audio e l'avcC passano i controlli successivi): il
gate è caduto solo perché la tabella dei pacchetti non è stata letta.

## Causa

`internal/media/gop_probe.go` avviava `cmd.Wait()` in una goroutine subito dopo
`cmd.Start()` mentre il goroutine chiamante decodificava `cmd.StdoutPipe()`.
Il contratto di `StdoutPipe` vieta esattamente questo: `Wait` chiude il lato di
lettura quando il figlio esce. Su un clip corto ffprobe termina prima che il
decoder abbia drenato il buffer, quindi la prima `dec.Token()` fallisce con
`read |0: file already closed`. L'esito dipende dallo scheduling: il test è
flaky e in produzione un artefatto valido può essere marcato "uncertifiable".

## Fix

- Nessuna goroutine di `Wait`: il decoder drena la tabella, poi si chiama
  `cmd.Wait()`.
- Ogni uscita anticipata (token inatteso, cadenza già invalida) cancella il
  contesto e attende il figlio nello stesso goroutine, senza chiudere il pipe
  mentre lo si legge.
- Il commento sul punto di chiamata registra il contratto per evitare la
  regressione.

## Criteri di accettazione

1. `TestCanonicalizeAssemblyV1EmitsPinnedStreamAndKeepsAudio` passa con
   `-count=10`.
2. `go test ./internal/media/ ./internal/processor/ -count=1` verde.
3. `go test ./... -count=1` verde.
4. Nessuna regressione sulla memoria costante: il decoder resta in streaming.

## Verifica

```
go test ./internal/processor/ -run TestCanonicalizeAssemblyV1EmitsPinnedStreamAndKeepsAudio -count=10
go test ./internal/media/ ./internal/processor/ -count=1
go test ./... -count=1
```

Esito effettivo (2026-11-21): 10/10 PASS sul test canonical (43,1 s),
`media`+`processor` verdi (4,4 s / 53,8 s), `go test ./...` **exit 0**
(40 package con test, 0 FAIL).

Ricontrollo 2026-11-21 (questa sessione): `RenderingGen` è al tree `c515fb5` e
`internal/media/gop_probe.go` è invariato (nessuna goroutine `Wait()`; il
commento di contratto è al punto di chiamata). Il working tree contiene
modifiche non correlate sotto `queue/`, `internal/chronon/` e `docs/tickets/`,
nessuna delle quali tocca il probe. I quattro criteri sono coperti dall'esito
sopra, quindi la scheda è chiusa.
