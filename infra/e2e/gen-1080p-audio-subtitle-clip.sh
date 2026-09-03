#!/usr/bin/env bash
set -euo pipefail

OUT_DIR="${1:-/tmp/renderinggen-1080p-e2e}"
mkdir -p "${OUT_DIR}"

ffmpeg -hide_banner -loglevel error -y \
  -f lavfi -i "testsrc2=size=1920x1080:rate=30" \
  -f lavfi -i "sine=frequency=880:sample_rate=48000" \
  -t 5 -shortest \
  -c:v libx264 -pix_fmt yuv420p -profile:v high -g 30 \
  -c:a aac -b:a 128k -ar 48000 -ac 2 \
  "${OUT_DIR}/source.mp4"

cat > "${OUT_DIR}/subtitles.ass" <<'EOF'
[Script Info]
ScriptType: v4.00+
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,DejaVu Sans,64,&H00FFFFFF,&H000000FF,&H00000000,&H99000000,-1,0,0,0,100,100,0,0,1,4,2,2,80,80,130,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.50,0:00:02.50,Default,,0,0,0,,SOTTOTITOLO REALE CHRONON
Dialogue: 0,0:00:03.00,0:00:04.70,Default,,0,0,0,,AUDIO E VIDEO OK
EOF

cp /usr/share/fonts/truetype/dejavu/DejaVuSans.ttf "${OUT_DIR}/DejaVuSans.ttf"

sha256sum "${OUT_DIR}/source.mp4" "${OUT_DIR}/subtitles.ass" "${OUT_DIR}/DejaVuSans.ttf" > "${OUT_DIR}/sha256.txt"
echo "Generated: ${OUT_DIR}"
cat "${OUT_DIR}/sha256.txt"
