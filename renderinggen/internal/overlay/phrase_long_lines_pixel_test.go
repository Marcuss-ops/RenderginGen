package overlay

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// longPhraseText is deliberately far past the six-word ceiling the fragment
// extractor used to impose: at the 25-column hard wrap it becomes a
// seven-line phrase, which is exactly the shape that regressed.
const longPhraseText = "Può contenere una frase intera molto lunga con accenti e parole naturali " +
	"senza perdere una parola, anche se supera le sei parole del limite precedente."

// TestLongPhraseRendersEveryWrappedLine is the pixel regression for the
// "long phrase renders only its first line" bug.
//
// The semantic compiler hard-wraps important phrases every 25 characters
// (`wrapPhraseAt25`), so the compiled text layer carries one `\n` per visual
// line.  Chronon splits a document into one paragraph per `\n`, but its text
// run held exactly one `TextRunLayout` and every consumer took
// `paragraphs.front()` — so a seven-line phrase rendered as its first line
// only, with the other six silently dropped.  No unit test caught it because
// nothing in the text suite rendered pixels.
//
// The test therefore compiles a long phrase, decodes real frames, counts the
// horizontal bands of glyph ink, and requires at least one band per compiled
// line.  Before the fix the count is 1; after it, it matches the phrase.
//
// Opt-in like every other real-engine test in this package: set CHRONON_BIN.
func TestLongPhraseRendersEveryWrappedLine(t *testing.T) {
	bin := chrononBinFor(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not available for pixel check: %v", err)
	}
	assetsRoot := certificationAssetsRoot(t)

	raw := []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"phrase-long-lines","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"background":{"kind":"color","color":[0.08,0.08,0.08,1]},"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","text":%q,"start_ms":0,"end_ms":4000}]}`, longPhraseText))
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile long phrase plan: %v", err)
	}

	// The compiled text is the authority for how many lines must come out:
	// every hard break the semantic compiler injected has to be visible.
	var phraseLayer *Layer
	for i := range result.Plan.Layers {
		if result.Plan.Layers[i].Type == "text" {
			phraseLayer = &result.Plan.Layers[i]
			break
		}
	}
	if phraseLayer == nil {
		t.Fatal("compiled long phrase has no text layer")
	}
	wantLines := strings.Count(phraseLayer.Text, "\n") + 1
	if wantLines < 4 {
		t.Fatalf("phrase wrapped to %d lines (%q); this regression needs at least 4", wantLines, phraseLayer.Text)
	}

	outDir := t.TempDir()
	videoPath := filepath.Join(outDir, "long-phrase.mp4")
	result.Plan.Output.Path = videoPath
	planPath := filepath.Join(outDir, "plan.json")
	planBytes, err := json.Marshal(result.Plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "software", "--encoder-backend", "pipe", "--hardware", "none",
		"--gpu-hot-path-mode", "auto", "--encode-preset", "ultrafast", "-o", videoPath)
	cmd.Dir = assetsRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("render long phrase: %v\n%s", err, tailBytes(out))
	}

	// Sample a few frames past the entrance: whichever frame shows the most
	// line bands is the one that tells the truth about dropped paragraphs,
	// and it keeps the assertion independent of the preset's entry timing.
	best, bestFrame, bestStats := 0, -1, ""
	for _, frame := range []int{40, 60, 80} {
		rgb := frameRGBAt(t, videoPath, frame, 1280, 720)
		bands, stats := inkLineBands(rgb, 1280, 720)
		if bands > best {
			best, bestFrame, bestStats = bands, frame, stats
		}
	}

	if best < wantLines {
		t.Fatalf("visible text line bands = %d (best frame %d, %s), want >= %d — "+
			"the engine dropped part of the hard-wrapped phrase %q",
			best, bestFrame, bestStats, wantLines, phraseLayer.Text)
	}
}


// frameRGBAt decodes one frame of path as raw rgb24 through ffmpeg.  It is the
// width/height-parameterised form of phraseMotionFrame.
func frameRGBAt(t *testing.T, path string, frame, w, h int) []byte {
	t.Helper()
	filter := fmt.Sprintf("select=eq(n\\,%d)", frame)
	out, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-vf", filter,
		"-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil {
		t.Fatalf("decode frame %d: %v", frame, err)
	}
	if len(out) != w*h*3 {
		t.Fatalf("decoded frame %d has %d RGB bytes, want %d", frame, len(out), w*h*3)
	}
	return out
}

// inkLineBands counts the horizontal bands of glyph ink in a rendered frame:
// a row is lit when enough of its pixels are as bright as the text fill, and
// a band is a maximal run of lit rows.  One band per visible text line, so a
// phrase that lost paragraphs to the single-paragraph text run reports far
// fewer bands than the compiled phrase has lines.
//
// The cut is calibrated from the frame itself (70% of its brightest pixel)
// rather than from an absolute colour: on a dark background the preset's glow
// and drop shadow are far enough from the background to count as ink under a
// fixed threshold, and they bridge every gap between lines into one band.
func inkLineBands(rgb []byte, w, h int) (int, string) {
	lum := make([]int, w*h)
	maxLum := 0
	for i := 0; i < w*h; i++ {
		j := i * 3
		v := (int(rgb[j]) + int(rgb[j+1]) + int(rgb[j+2])) / 3
		lum[i] = v
		if v > maxLum {
			maxLum = v
		}
	}
	cut := maxLum * 7 / 10
	if cut < 120 {
		cut = 120
	}

	ink := make([]int, h)
	first, last := -1, -1
	for y := 0; y < h; y++ {
		row := 0
		base := y * w
		for x := 0; x < w; x++ {
			if lum[base+x] > cut {
				row++
			}
		}
		ink[y] = row
		if row > 0 {
			if first < 0 {
				first = y
			}
			last = y
		}
	}

	maxRow := 0
	for _, v := range ink {
		if v > maxRow {
			maxRow = v
		}
	}
	// Antialiasing leaves a handful of lit pixels in the gaps; anything above
	// this floor is a line body.
	floor := maxRow / 10
	if floor < 2 {
		floor = 2
	}

	bands, inBand := 0, false
	for _, v := range ink {
		if v > floor && !inBand {
			bands++
			inBand = true
		} else if v <= floor && inBand {
			inBand = false
		}
	}
	stats := fmt.Sprintf("maxLum=%d cut=%d maxRow=%d floor=%d inkRows=%d..%d",
		maxLum, cut, maxRow, floor, first, last)
	return bands, stats
}
