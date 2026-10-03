# UploadDrive

Cartella pronta per il pack Social Motion V1 e per il relativo caricamento su Google Drive.

## Contenuto

- `social_motion_pack_v1/`: 18 MP4 verificati, 18 piani `.plan.json` e 18 file di timing.
- `credentials.json` e `token.json`: copie locali delle credenziali OAuth, con permessi `600` (leggibili solo dall’utente proprietario).
- `upload_to_drive.sh`: carica i 18 MP4 nella cartella Drive configurata e verifica lo SHA-256 di ogni file.

Per ripetere l’upload:

```bash
cd RenderingGen/UploadDrive
./upload_to_drive.sh
```

Le credenziali sono segreti: non condividerle e non aggiungerle a commit Git. Gli originali del pack restano in `ChrononTemplate/out/social_motion_pack_v1/`.
