# UploadDrive

Cartelle pronte per i pack e il relativo caricamento su Google Drive.

## Contenuto

- `social_motion_pack_v1/`: 18 MP4 verificati, 18 piani `.plan.json` e 18 file di timing.
- `scene_camera_sequencer_v1/`: 9 MP4 verificati (8 transizioni + master 5 stacchi), 9 piani `.plan.json` e 9 file di timing.
- `credentials.json` e `token.json`: copie locali delle credenziali OAuth, con permessi `600` (leggibili solo dall’utente proprietario).
- `upload_to_drive.sh`: carica i 18 MP4 di `social_motion_pack_v1` nella cartella Drive configurata e verifica lo SHA-256 di ogni file.
- `upload_scene_camera_sequencer_v1.sh`: carica i 9 MP4 di `scene_camera_sequencer_v1` (sottocartella `scene_camera_sequencer_v1` su Drive).

Per ripetere l’upload:

```bash
cd RenderingGen/UploadDrive
./upload_to_drive.sh
./upload_scene_camera_sequencer_v1.sh
```

Le credenziali sono segreti: non condividerle e non aggiungerle a commit Git. Gli originali del pack restano in `ChrononTemplate/out/social_motion_pack_v1/`.
