package overlay

// official_presets_image.go owns the IMAGE half of the official preset catalog.
//
// Every image preset shares one geometry — a 480x480 contain box — because the
// variations between them are the anchor and the entrance motion, not the size:
// an image card that changed size per preset would make two presets differ for
// a reason an editor did not choose. The corner radius is the family's only
// per-preset geometry rule and lives in ImagePresetRadius.
const (
	imagePresetBoxWidth  = 480
	imagePresetBoxHeight = 480
	imagePresetEnter     = 8
	imagePresetExit      = 6
)

// imageSpec is the image-family authoring row: where the card is anchored and
// which motion it enters with.
func imageSpec(anchor, anim string) presetSpec {
	return presetSpec{
		family: PresetImage, anchor: anchor, align: "center", anim: anim, unit: "layer",
		enter: imagePresetEnter, exit: imagePresetExit,
		boxW: imagePresetBoxWidth, boxH: imagePresetBoxHeight, fit: FitContain,
	}
}
