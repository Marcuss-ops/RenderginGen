package overlay

import "testing"

// TestSemanticTemplateRegistry_CanonicalVocabulary keeps the RenderingGen
// boundary fail-closed for every semantic template PipelineGen may emit.
// The compiler must resolve these IDs through the one concrete template table
// used to build Chronon layers; unknown IDs must never fall back silently.
func TestSemanticTemplateRegistry_CanonicalVocabulary(t *testing.T) {
	want := []string{
		"PERSON",
		"ORGANIZATION",
		"LOCATION",
		"CONCEPT",
		"IMPORTANT_PHRASE",
		"IMPORTANT_WORD",
		"IMAGE_OVERLAY",
	}
	seen := make(map[string]struct{}, len(semanticTemplateRegistry))
	for id := range semanticTemplateRegistry {
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("duplicate template id %q", id)
		}
		seen[id] = struct{}{}
	}
	for _, id := range want {
		spec, ok := semanticTemplateRegistry[id]
		if !ok {
			t.Fatalf("template %q is not registered", id)
		}
		if spec.Type == "" {
			t.Fatalf("template %q has no concrete layer type", id)
		}
	}
}

func TestSemanticTemplateRegistry_UnknownFailsClosed(t *testing.T) {
	if _, ok := semanticTemplateRegistry["DOES_NOT_EXIST"]; ok {
		t.Fatal("unknown template unexpectedly registered")
	}
}
