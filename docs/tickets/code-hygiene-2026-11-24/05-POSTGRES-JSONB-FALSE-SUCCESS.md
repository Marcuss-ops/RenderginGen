# RG-BUG-01 — `jsonFacts`/`jsonProvenance` perdono l'errore di marshal

- **Stato:** CLOSED (2026-11-24) — marshal propagato e coperto da test; il test di regressione rifiuta `ProducerTelemetry` non valido.
- **Priorità:** P1
- **Repo:** RenderingGen (`queue` module)

## Evidenza

`queue/internal/repository/postgres/artifacts.go:61-81`: entrambe le funzioni
restituiscono `nil` se `json.Marshal` fallisce. `insertArtifact` inserisce
`NULL` nella colonna JSONB e dichiara successo: un artifact con certificazione
strutturale o provenienza non serializzabile viene persistito come se fosse un
worker legacy che non ha certificato nulla. Non c'è modo, leggendo la riga, di
distinguere i due casi.

## Fix richiesto

- [x] `jsonFacts` e `jsonProvenance` restituiscono `(any, error)`.
- [x] `insertArtifact` propaga l'errore prima dell'INSERT con il contesto
      dell'artifact.
- [x] Test: `TestJSONProvenanceRejectsInvalidTelemetry` rifiuta un raw message
      non valido; `TestJSONFactsNilStaysNull` e `TestJSONFactsSerializesCertification`
      pinnano il comportamento buono.
- [x] I test esistenti di `insertArtifact`/`Complete` restano verdi (2026-11-24).
