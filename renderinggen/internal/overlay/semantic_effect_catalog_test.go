package overlay

import "testing"

// TestEffectDefinitionsClipImpliesLayer pins the P4 parity rule of the shared
// catalog: every type the clip adjustment stack accepts must also exist as a
// layer effect, because the image surface lowers through the layer path. A
// clip-only entry would make "clip effect X" replay on an image as an
// unsupported type — exactly the divergence the single catalog exists to
// prevent.
func TestEffectDefinitionsClipImpliesLayer(t *testing.T) {
	for typ, definition := range effectDefinitions {
		if !definition.clip {
			continue
		}
		if !definition.layer {
			t.Errorf("effect %q is clip-only: it must also be accepted on the layer surface", typ)
		}
		if _, ok := effectDefinitionFor(typ, effectSurfaceLayer); !ok {
			t.Errorf("effect %q resolves on the clip surface but not on the layer surface", typ)
		}
	}
}

// TestEffectDefinitionsClipRequiredAreDeclared keeps the required list honest:
// a name that is not in the parameter table would be rejected by every payload
// (no value can supply it), and a required entry with no bound cannot be
// validated.
func TestEffectDefinitionsClipRequiredAreDeclared(t *testing.T) {
	for typ, definition := range effectDefinitions {
		for _, name := range definition.clipRequired {
			if _, ok := definition.params[name]; !ok {
				t.Errorf("effect %q lists required parameter %q that is not declared in params", typ, name)
			}
		}
		if definition.clip && len(definition.clipRequired) == 0 && len(definition.params) > 0 {
			t.Errorf("effect %q accepts clip payloads but requires no parameter", typ)
		}
	}
}

// TestEffectDefinitionsBoundsAreOrdered catches a typo that would make a
// parameter impossible to supply (minimum above maximum).
func TestEffectDefinitionsBoundsAreOrdered(t *testing.T) {
	for typ, definition := range effectDefinitions {
		for name, spec := range definition.params {
			if spec.kind == effectParamBool {
				continue
			}
			if spec.minimum > spec.maximum {
				t.Errorf("effect %q parameter %q has minimum %v above maximum %v", typ, name, spec.minimum, spec.maximum)
			}
		}
	}
}
