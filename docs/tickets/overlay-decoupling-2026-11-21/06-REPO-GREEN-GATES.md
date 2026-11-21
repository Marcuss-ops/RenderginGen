# OVL-06 — Gate repo-wide: architettura e dead exports

**Stato:** PASS (2026-11-21) · **Priorità:** P1

**Esito:** `cmd/p5idsprobe` legge la workspace da `-source`/`P5_PROBE_SOURCE`
con exit 2 e messaggio esplicito se assente; `assets.Fonts` è referenziato
direttamente (import senza alias) da `internal/processor/builtin_fonts.go`, che
era già il suo consumer reale. L'evidenza nel ticket non cita più un path di
home letterale. Verifiche dopo l'ultima modifica: `go build ./...` e
`go test ./internal/architecture/ -count=1` verdi (24,5 s).

## Evidenza

`go test ./...` in `RenderingGen/renderinggen` fallisce per due cause non
introdotte da questa wave (file non modificati):

1. `internal/architecture` → `TestArchitectureConformance`:
   `cmd/p5idsprobe/main.go:15` contiene un path assoluto di home di
   sviluppatore, vietato a ogni carrier (sorgenti e config).
2. `internal/architecture` → `TestNoDeadExports`:
   `assets/fonts.go: Fonts` (embed.FS) non è referenziato da nessun file del
   modulo; il test ammette sole eccezioni motivate.

## Problema

I gate sono il contratto di igiene del repo: finché falliscono, nessuna wave può
dichiarare `go test ./...` verde e i fallimenti veri si nascondono dietro quelli
noti.

## Target

- Il probe p5 legge la sorgente da `P5_PROBE_SOURCE` (o flag), con default
  documentato e nessun path assoluto di sviluppatore; errore chiaro se assente.
- `assets.Fonts` smette di essere un export morto:
  - se esiste un consumer esterno, va dichiarato in `deadExportExceptions` con
    la motivazione (come gli altri due casi già excepted);
  - altrimenti si unexport o si elimina.

## Piano

1. `cmd/p5idsprobe`: `source := os.Getenv("P5_PROBE_SOURCE")`, fail-fast con
   messaggio se vuoto; aggiornare l'uso nel README del probe se presente.
2. `assets/fonts.go`: verificare i consumer (`grep -rn "assets.Fonts"` +
   contract docs). Se l'embed serve solo a `go:embed` per il packaging,
   rinominare in `fontsFS` (unexported) e adeguare eventuali wrapper.
3. Eseguire il gate di architettura completo.

## Criteri di accettazione

1. `go test ./internal/architecture/ -count=1` verde.
2. Nessun path assoluto di home in sorgenti/config del modulo.
3. Nessun export a zero chiamanti non excepted.
4. `go build ./...` verde.

## Verifica

```
go build ./...
go test ./internal/architecture/ -count=1
```

## Rischio

Se `assets.Fonts` è consumato fuori dal modulo, unexport lo rompe: per questo il
piano parte dalla ricerca consumer e, se esiste, usa l'eccezione motivata.
