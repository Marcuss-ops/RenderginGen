package prescale

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestDisplayBoundSizePreservesAspectRatio(t *testing.T) {
	cases := []struct {
		width, height, max int
		wantW, wantH       int
	}{
		{800, 600, 2048, 800, 600},
		{4000, 2000, 2048, 2048, 1024},
		{2000, 4000, 2048, 1024, 2048},
		{4000, 1, 2048, 2048, 1},
	}
	for _, tc := range cases {
		gotW, gotH := DisplayBoundSize(tc.width, tc.height, tc.max)
		if gotW != tc.wantW || gotH != tc.wantH {
			t.Errorf("DisplayBoundSize(%d,%d,%d) = %dx%d, want %dx%d", tc.width, tc.height, tc.max, gotW, gotH, tc.wantW, tc.wantH)
		}
	}
}

func TestResizeAreaAverageKeepsOpaqueColor(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	got := ResizeAreaAverage(source, 1, 1)
	if got.NRGBAAt(0, 0) != (color.NRGBA{R: 200, G: 100, B: 50, A: 255}) {
		t.Fatalf("resized pixel = %+v", got.NRGBAAt(0, 0))
	}
}

func TestEncodeFormats(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	for _, format := range []string{"png", "jpeg"} {
		data, ext, err := Encode(source, format)
		if err != nil {
			t.Fatalf("Encode(%s): %v", format, err)
		}
		if len(data) == 0 || ext != Extension(format) {
			t.Fatalf("Encode(%s) = %d bytes, ext %q", format, len(data), ext)
		}
	}
	if _, _, err := Encode(source, "gif"); err == nil {
		t.Fatal("Encode must reject an unsupported format")
	}
}

func TestWriteAtomicFileAndCachePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "derivative.png")
	if err := WriteAtomicFile(path, []byte("bytes")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "bytes" {
		t.Fatalf("read installed file: %q, %v", data, err)
	}
	first := CachePath("/cache", "source", 2048, "png")
	second := CachePath("/cache", "source", 2048, "png")
	if first != second {
		t.Fatalf("CachePath is not deterministic: %q vs %q", first, second)
	}
	if filepath.Ext(first) != ".png" {
		t.Fatalf("CachePath extension = %q", filepath.Ext(first))
	}
}
