# RG-HYG-01 — Directory `cmd/` vuote e inventario dei probe usa-e-getta

- **Stato:** CLOSED (2026-11-24) — le sette directory vuote sono state rimosse; gli otto probe sono stati classificati e restano come tooling operativo documentato.
- **Priorità:** P2
- **Repo:** RenderingGen

## Evidenza

Sette directory sotto `renderinggen/cmd/` non contengono file:

`assetdecodeab`, `chaincompile`, `chainprobe`, `chainshutdown`, `overlapprobe`,
`p5daemonprobe`, `p5dirtoggle`.

Otto comandi hanno 0 riferimenti in `Makefile`, `.github/workflows` e `infra/`:
`camera-pan-demo`, `chainpancheck`, `chunk-overlap-bench`, `p5idsprobe`,
`postlane-bench`, `two-job-warm-bench`, `vram-probe`, `web-phrase`.
Alcuni sono documentati in ticket recenti (`p5idsprobe` in
`overlay-decoupling-2026-11-21/06-REPO-GREEN-GATES.md`) o hanno un package
interno dedicato (`vram-probe` → `internal/vramprobe`), quindi non sono
automaticamente spazzatura: vanno classificati, non cancellati in blocco.

## Inventario dei probe (2026-11-24)

| Comando | Owner | Scopo | Uso documentato |
|---|---|---|---|
| `camera-pan-demo` | overlay/camera | genera gli otto piani camera-pan per la preview | commento `main.go`, `image_group_camera_pan.go:70` |
| `chainpancheck` | overlay/camera | compila un piano chain-pan e stampa posizioni/track | commento `main.go` |
| `chunk-overlap-bench` | Chronon integration | misura l'overlap di due chunk sullo stesso daemon | commento `main.go` |
| `p5idsprobe` | worker tooling | replay di una workspace catturata sul daemon | `overlay-decoupling-2026-11-21/06-REPO-GREEN-GATES.md` |
| `postlane-bench` | queue/performance | metriche post-lane di un job canonico | commento `main.go` |
| `two-job-warm-bench` | performance | due RENDER_JOB su daemon caldi, sequenziale vs barriera | commento `main.go` |
| `vram-probe` | GPU tooling | working set del device; package `internal/vramprobe` | commento `main.go` + `internal/vramprobe` |
| `web-phrase` | content tooling | genera il lane web paragraph long/short phrase | commento `main.go` |

## Criteri di accettazione

- [x] Le sette directory vuote non esistono più.
- [x] Per ognuno degli otto comandi il ticket registra owner, scopo, uso e
      riferimento; nessuno è rimasto senza owner.
- [x] `go build ./...` e `go vet ./...` restano verdi (gate finale 2026-11-24).
