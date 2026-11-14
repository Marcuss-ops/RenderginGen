package overlay

import (
	"image"
	"testing"
)

func TestFitEntityImageBoxToAssetPreservesAspectWithoutMatte(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source image.Point
		want   image.Point
	}{
		{name: "landscape", source: image.Pt(1600, 900), want: image.Pt(480, 270)},
		{name: "portrait", source: image.Pt(900, 1600), want: image.Pt(270, 480)},
		{name: "square", source: image.Pt(800, 800), want: image.Pt(480, 480)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layer := Layer{BoxWidth: 480, BoxHeight: 480, Size: []float64{480, 480}, Fit: FitContain}
			if err := FitEntityImageLayerToAsset(&layer, tc.source.X, tc.source.Y); err != nil {
				t.Fatal(err)
			}
			if got := image.Pt(layer.BoxWidth, layer.BoxHeight); got != tc.want {
				t.Fatalf("box = %v, want %v", got, tc.want)
			}
			if len(layer.Size) != 2 || layer.Size[0] != float64(tc.want.X) || layer.Size[1] != float64(tc.want.Y) {
				t.Fatalf("serialized size = %v, want [%d %d]", layer.Size, tc.want.X, tc.want.Y)
			}
		})
	}
}
