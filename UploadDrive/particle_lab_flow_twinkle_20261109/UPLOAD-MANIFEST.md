# Chronon3D Particle Lab — flow/twinkle parity refresh

Generated: 2026-11-09
Drive folder: [Chronon3D_Particle_Lab_FlowTwinkle_1920x1080_20261109](https://drive.google.com/drive/folders/1tEtkbDZd_zzVgKua4h_jzG4P-dWqv58-)

## Parity

- Test: `Vulkan GPU particle flow and twinkle match the CPU sampler oracle`
- Result: **PASS**, 14 assertions
- Max absolute GPU/CPU difference: `7.98702e-06` (threshold `2.0e-04`)
- CPU energy: `324.277`; GPU energy: `324.276`
- Scenario: t=7.5 s, seed 60221, 96 particles, lifetime [2.0, 3.0] s, flow [7.0, 0.045, 0.65], twinkle [0.85, 1.1], environment=True

## Clips

| Clip | Resolution | Frames | Duration | SHA-256 | Drive |
|---|---|---:|---:|---|---|
| 01_cinematic_dust | 1920x1080 | 240 | 10.0s | `d4f743e6fe96846ecddf7fdc0e0dea9c36b91c3705c96e2beaeceb4b6af2182e` | [open](https://drive.google.com/file/d/1IQmpndQF4OLX-zRzM3E8daolShJnyOlN/view?usp=drivesdk) |
| 02_embers | 1920x1080 | 240 | 10.0s | `8d114a5dad7a966761da87faa8e4fdac8ef347d2ff4df03864d293981d20bd6d` | [open](https://drive.google.com/file/d/1RsAqeWTqepbDr_KDpLIbn0v8pUHRg4xI/view?usp=drivesdk) |
| 03_ash_fall | 1920x1080 | 240 | 10.0s | `ba20c4baa51c1093607be69bb595f0ee7496efd0862ce7602636ec037b606237` | [open](https://drive.google.com/file/d/11dv54Nc9MWcLj7-P-Mh1gsGlE5JkNRiS/view?usp=drivesdk) |
| 04_bokeh_orbs | 1920x1080 | 240 | 10.0s | `aa5a6e40bb0d085068e55e35e071ab135f46043de27442b5074a0ccc01a2519f` | [open](https://drive.google.com/file/d/1EsZa3TdE0FsaI6jftzwHD0r5h9Z6CrDH/view?usp=drivesdk) |
| 05_chalk_burst | 1920x1080 | 240 | 10.0s | `e7eae33925ba40ea37a09c20977482eae6a7ebad72d9b623a751db436d8b7290` | [open](https://drive.google.com/file/d/1BHiYQZMbLUAKQOR15qf29GVa-jeBF8eS/view?usp=drivesdk) |
| 06_light_motes | 1920x1080 | 240 | 10.0s | `5b3240461915bce0d2749daaa817e82c7b9039d0cc391f824a7cbd58d0b5a3c1` | [open](https://drive.google.com/file/d/1e0ugl6itt6j6THe6FTPfw0hc2QMOpbvG/view?usp=drivesdk) |

## Evidence

- [contact_sheet.png](https://drive.google.com/file/d/12OvHbPzbHX4-ty6h-5BqFVsN6J3pEykn/view?usp=drivesdk) — `b1e647271008927afff052039fe40f9916aeb7ab48edb99fafc8e1a69d56b638`
- [parity-vulkan-flow-twinkle.log](https://drive.google.com/file/d/1IwWvLLGoaeWL1GS-tlj6rVE5WF6R3yzE/view?usp=drivesdk) — `52902d81ba0e7b8ef8808fca49c380889ea0ef67da2f334f81c87769be437654`

## Manifest

- [manifest.json](https://drive.google.com/file/d/1U1T8JtndGy4kJe38rNleKI5gk89fWf7E/view?usp=drivesdk)

The six clips are a deterministic software re-render of the existing Particle Lab flow/twinkle plans. The Vulkan compute emitter parity is certified by the registry test listed above.
