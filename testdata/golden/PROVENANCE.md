# Provenance and licensing of `testdata/golden`

These files are **test fixtures**, not product assets. They are versioned so a
clean checkout and a CI runner can execute the media, font, overlay and map
tests without network access or a sibling ChrononTemplate checkout.

`SHA256SUMS` is the integrity authority: the test
`TestGoldenFixturesMatchChecksumManifest` (in `renderinggen/internal/overlay`)
fails when a listed file changes or a new file appears without a manifest
entry. Update the manifest only for an intentional fixture change.

## Fonts

| File(s) | Upstream | License |
|---|---|---|
| `assets/fonts/Poppins-Bold.ttf`, `Poppins-Regular.ttf`, `Poppins-Bold.ttf` (symlink) | Poppins, Indian Type Foundry / Google Fonts | SIL Open Font License 1.1 |
| `assets/fonts/Inter-Bold.ttf`, `Inter-Regular.ttf`, `Inter.ttf`, `fonts/Inter-Bold.ttf` (symlink) | Inter, Rasmus Andersson / Google Fonts | SIL Open Font License 1.1 |
| `assets/fonts/PlayfairDisplay.ttf`, `PlayfairDisplay-Italic.ttf` | Playfair Display, Claus Eggers Sørensen / Google Fonts | SIL Open Font License 1.1 |
| `assets/fonts/Bricolage-Grotesque.ttf` | Bricolage Grotesque, Mathieu Triay / Google Fonts | SIL Open Font License 1.1 |
| `assets/fonts/DejaVuSans.ttf`, `DejaVuSans.ttf` (symlink) | DejaVu Fonts | DejaVu Fonts License (Bitstream Vera derivative) |

The OFL permits bundling and redistribution of the font files; the reserved
font names must not be used for modified versions. No file here has been
modified from upstream.

## Geographic data

| File | Upstream | License |
|---|---|---|
| `renderinggen/internal/geofeatures/testdata/catalog/ne_50m_admin_0_countries.geojson` | Natural Earth 1:50m Admin 0 Countries | Public domain |

The checked-in extract is trimmed to the features the map tests assert (it
includes both Congos to cover the ambiguity case). It is not a general-purpose
basemap.

## Demo media

| File | Origin | License / status |
|---|---|---|
| `background.mp4` | Generated in-repo test clip (1280x720 H.264, IDR first frame) | Project-owned fixture |
| `background.jpg`, `apple.png`, `logo_pulse.png`, `overlay_chart.png`, `overlay_globe.png` | Generated in-repo stills used by overlay certification | Project-owned fixture |
| `Pale-Olive.mp4` | Audio+video probe fixture (used to prove video-stream attribution) | Project-owned fixture |
| `gerard_butler.jpg` | Third-party press portrait used only as a demo input | **Origin and license not documented.** Replace with a generated portrait, or record the source and permission here, before redistributing this repository or its images. |

## Job documents

`golden-overlay-job-v2.json` and `golden-semantic-overlay-job-v1.json` are
authored fixtures for the deterministic overlay canaries; their Go copies live
in `renderinggen/internal/chronon` and a test keeps both copies identical.

## Intentionally not versioned

- `assets/semantic/` — generated map LODs (~44 MB), rebuilt by the
  certification harness on demand.
- Any rendered clip, timing sidecar or telemetry summary under this tree:
  `.gitignore` excludes the generated-artifact classes.

## Changing a fixture

1. Edit or replace the file.
2. Regenerate the manifest from the tracked set:
   `git ls-files testdata/golden | xargs sha256sum` (from the repository root;
   paths must be relative to `testdata/golden`).
3. Run `go test ./internal/overlay -run TestGoldenFixtures` from
   `renderinggen/`.
4. Update this document if origin or license changed.
