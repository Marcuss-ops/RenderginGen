# Socials structures - 1920x1080

Quattro strutture social autorate in C++ come RenderPlan V3:
X, Reddit, YouTube e TikTok. Superfici SDF native, testo Inter +
simboli DejaVu, animazioni opacity / position / scale / progress.

Drive: https://drive.google.com/drive/u/1/folders/1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS

| # | Struttura | Durata | Clip |
|---|---|---|---|
| x_ | X / post card | 8s | [apri](https://drive.google.com/file/d/1hOFMeXgU212jIJlxzsjF2JryC7PsSqU2/view?usp=drivesdk) |
| re | Reddit / thread cascade | 8s | [apri](https://drive.google.com/file/d/1bkYLpoUDweA-NHAIgPWiZVfMt3ZevSRs/view?usp=drivesdk) |
| yo | YouTube / comment highlight | 8s | [apri](https://drive.google.com/file/d/1z4ajOB8XvlpjEWWhSZiq9pzTjq724NDJ/view?usp=drivesdk) |
| ti | TikTok / vertical overlay | 8s | [apri](https://drive.google.com/file/d/1uTLgZwp3MLoizCMHCP2yu-4PzccCORyH/view?usp=drivesdk) |

## Ricetta

```text
canvas 1920x1080, 30 fps, 8 s, fade finale
X: card out_back 94% -> 100%, due righe a comparsa, progress line
Reddit: cascata a tre livelli, thread line scale_y con origine in alto, upvote pulse
YouTube: play, progress rossa, dimming, comment card out_back in primo piano
TikTok: phone 9:16, disco in rotazione, double-tap heart burst
```

Contenuti fictional, dichiarati ILLUSTRATIVE MOCKUP.
