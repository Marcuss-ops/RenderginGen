# TICKET — Confine Producer 77 / Master 51 (SUPERSEDED)

- **Stato:** SUPERSEDED il 2026-11-14.
- **Contratto corrente:** [`refactored/docs/operations/PRODUCER-MASTER-BOUNDARY.md`](../../../refactored/docs/operations/PRODUCER-MASTER-BOUNDARY.md)
- **Residui Master/stockpipeline:** [`TICKET-MASTER-STOCK-MATERIALIZATION-2026-11-14.md`](../../../refactored/docs/operations/TICKET-MASTER-STOCK-MATERIALIZATION-2026-11-14.md)
- **Storico completo:** [`archive/TICKET-PRODUCER-MASTER-BOUNDARY-2026-11-04.md`](archive/TICKET-PRODUCER-MASTER-BOUNDARY-2026-11-04.md)

## Perché è stato archiviato

Il ticket ha accumulato decisioni che si sono superate in quattro giorni:
frammenti al Producer (2026-11-05), poi preparazione spostata al Master con
`76e8f49c1` (2026-11-07). Il codice attuale non prepara più frammenti stock nel
Producer, quindi il ticket non è più una fonte di verità.

La regola corrente è una sola: **lo stock non è del Producer**. La stockpipeline
seleziona e certifica il file; il Producer trasporta solo il riferimento e la
finestra nel piano immutabile; il Master materializza e assembla.

Non usare questo file per nuove decisioni. Aggiornare il contratto e, se
necessario, il ticket Master/stockpipeline.
