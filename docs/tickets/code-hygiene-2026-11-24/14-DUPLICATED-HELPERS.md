# RG-DRY-02 — `buildinfo`, `hashFile`, `copyBufPool`, `finite`, helper CLI

- **Stato:** CLOSED (2026-11-24)
- **Priorità:** P3
- **Repo:** RenderingGen

## Evidenza

1. `objectstore/internal/buildinfo/buildinfo.go` e
   `renderinggen/internal/buildinfo/buildinfo.go` sono cloni 1:1 (`Current`,
   `Runtime`, `SetRuntime`, `vcsStamp`, `executableDigest`, ...).
2. `hashFile` in `objectstore/internal/store/staging.go:265` e
   `hashFileContent` in `renderinggen/internal/storage/l2.go:175` fanno lo
   stesso SHA-256 di file.
3. `copyBufPool` esiste in `hashio` (64 KiB) e `workspace` (256 KiB): buffer
   diversi, non un clone esatto.
4. `finite` è ridefinita in `geofeatures`, `overlay` e `tacticalfill`.
5. `renderPlan`, `resolveAssetsRoot`, `resolveChronon`, `resolveEmitter` sono
   clonati in 4 `cmd/`.

## Criteri di accettazione

- [x] `buildinfo`: parità byte-per-byte pinnata dallo stesso golden JSON in
      `objectstore/internal/buildinfo/wire_golden_test.go` e
      `renderinggen/internal/buildinfo/wire_golden_test.go`, più la parità di
      chiavi e tupla `identity_hash` in `buildinfo_parity_test.go`.
- [x] `hashFile`/`hashFileContent`: `l2.go` usa `hashio.File`; i due moduli
      restano separati e condividono gli stessi vettori SHA-256 in
      `hashio_vectors_test.go` e `staging_hash_test.go`.
- [x] `copyBufPool`: non è un clone. `hashio` usa 64 KiB per lo streaming hash,
      `workspace` 256 KiB per materializzare asset video; il commento in
      `workspace.go` registra il motivo della separazione. `finite` è unificata
      in `internal/numeric.Finite`, con wrapper locali.
- [x] Helper CLI: `resolveAssetsRoot`/`resolveChronon` vivono in
      `renderinggen/internal/cli`; i quattro comandi sono migrati e
      `TestCLIResolversStayShared` impedisce nuove copie.
