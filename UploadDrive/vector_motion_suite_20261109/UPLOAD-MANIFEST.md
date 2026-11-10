# Vector Motion Suite — 5 categorie

Suite vettoriale nativa in RenderPlan V3, generata in C++ da
`chronontemplate_emit_vector_motion_suite` e renderizzata su Vulkan/NVENC.
Drive (root): https://drive.google.com/drive/u/1/folders/1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS

| # | Categoria | Clip | Durata | File |
|---|---|---|---|---|
| 01 | tactical_map_hud | Tactical route & HUD | 10s | [apri](https://drive.google.com/file/d/1RLC1jeT_yMn1ZtEkaibVeHjd2cErG199/view?usp=drivesdk) |
| 02 | network_nodes | Node graph expansion | 8s | [apri](https://drive.google.com/file/d/1oYDPy-UHkYnQA7tPo8zVdgrkMTZOtgdo/view?usp=drivesdk) |
| 03 | validation_badge | Validation burst | 6s | [apri](https://drive.google.com/file/d/1S0KcpIzYOITq5bk_IFg0h2xGQIFxvTef/view?usp=drivesdk) |
| 04 | callout_annotation | Technical callout | 8s | [apri](https://drive.google.com/file/d/1fezkBv1NWO1jR3H9iP6QIVnZ3WsKsnlQ/view?usp=drivesdk) |
| 05 | logo_morph | Wireframe to solid logo | 8s | [apri](https://drive.google.com/file/d/11Ws2CEVvJ2MHFhy_AJmAFkv_BhLrSswE/view?usp=drivesdk) |

## Ricetta

```text
canvas 1920x1080 @ 30 fps, massimo 300 frame per clip
path Bézier cubiche con trim sequenziale e stroke round
marker campionati su lunghezza d'arco (nessun follow_path a runtime)
glow nativo, ring_segment, rounded_rect, vignette, blend screen/multiply
entrate out_cubic/out_back/out_expo, cascate sfalsate, snap con overshoot
```
