package overlay

import "fmt"

// ValidateEntityCaptionCollisions rejects fitted image-caption boxes that overlap
// in both canvas space and time. Caption positions are layer centers and sizes
// are concrete pixel dimensions after image fitting.
func ValidateEntityCaptionCollisions(layers []Layer, canvas ...int) error {
	type caption struct {
		id         string
		layer      Layer
		startFrame int64
		endFrame   int64
	}
	var captions []caption
	for _, layer := range layers {
		if layer.CaptionForImageID == "" || len(layer.Position) < 2 || len(layer.Size) < 2 {
			continue
		}
		start := layer.StartFrame
		end := start + layer.DurationFrames
		captions = append(captions, caption{id: layer.ID, layer: layer, startFrame: start, endFrame: end})
	}
	for i := 0; i < len(captions); i++ {
		a := captions[i]
		for j := i + 1; j < len(captions); j++ {
			b := captions[j]
			sharedStart := max(a.startFrame, b.startFrame)
			sharedEnd := min(a.endFrame, b.endFrame)
			if sharedStart >= sharedEnd {
				continue
			}
			ax, ay := captionCollisionPosition(a.layer, canvas)
			bx, by := captionCollisionPosition(b.layer, canvas)
			dx := ax - bx
			dy := ay - by
			if 2*absFloat(dx) >= a.layer.Size[0]+b.layer.Size[0] || 2*absFloat(dy) >= a.layer.Size[1]+b.layer.Size[1] {
				continue
			}
			return fmt.Errorf("captions %q and %q overlap in canvas bounds during shared frame window [%d,%d)", a.id, b.id, sharedStart, sharedEnd)
		}
	}
	return nil
}

func captionCollisionPosition(layer Layer, canvas []int) (float64, float64) {
	x, y := layer.Position[0], layer.Position[1]
	if !layer.ScreenSpace && len(canvas) >= 2 {
		x += float64(canvas[0]) / 2
		y += float64(canvas[1]) / 2
	}
	return x, y
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
