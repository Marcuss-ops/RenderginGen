package overlay

// compileTextLayer lowers a text kind to a single text layer through the
// spec seam: the role resolver owns every semantic decision (wrap discipline,
// preset style, box, fit policy, motion target) and hands this function a
// fully resolved spec, so the lowering below stays semantic-agnostic. Text is
// mandatory: PipelineGen owns the displayed text and RenderingGen never
// invents one (there is no entity_ref fallback).
func compileTextLayer(ri resolvedItem, src *semanticPlan, layerID string) (Layer, error) {
	spec, err := buildResolvedTextSpec(ri, src)
	if err != nil {
		return Layer{}, err
	}
	if err := applyResolvedTextSpecStyle(&spec, ri, src); err != nil {
		return Layer{}, err
	}
	return compileResolvedText(layerID, ri.Start, ri.End, spec)
}
