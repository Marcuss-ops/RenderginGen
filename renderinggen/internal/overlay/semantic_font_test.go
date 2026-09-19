package overlay

import "testing"

func TestOfficialFontPathForLanguage(t *testing.T) {
	tests := map[string]string{
		"en":      officialFontPath,
		"it-IT":   officialFontPath,
		"de":      officialFontPath,
		"ru":      officialCyrillicFontPath,
		"sr-Cyrl": officialCyrillicFontPath,
		"uk_UA":   officialCyrillicFontPath,
		"zh-Hans": officialFontPath,
	}
	for language, want := range tests {
		if got := officialFontPathForLanguage(language); got != want {
			t.Errorf("officialFontPathForLanguage(%q) = %q, want %q", language, got, want)
		}
	}
}

func TestCompileSemanticUsesLocalizedCyrillicFont(t *testing.T) {
	const plan = `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"font-ru","video_id":"v","language":"ru","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"static_text_smoke","text":"Под бдительным оком","start_ms":0,"end_ms":1000}]}`
	result, err := CompileSemantic([]byte(plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plan.Layers) != 1 || result.Plan.Layers[0].Style == nil {
		t.Fatalf("compiled layers = %+v, want one styled text layer", result.Plan.Layers)
	}
	if got := result.Plan.Layers[0].Style.Font; got != officialCyrillicFontPath {
		t.Fatalf("Russian semantic font = %q, want %q", got, officialCyrillicFontPath)
	}
}

func TestCompileSemanticKeepsLatinPresetFont(t *testing.T) {
	const plan = `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"font-en","video_id":"v","language":"en","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"static_text_smoke","text":"Under watchful eyes","start_ms":0,"end_ms":1000}]}`
	result, err := CompileSemantic([]byte(plan))
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Plan.Layers[0].Style.Font; got != officialFontPath {
		t.Fatalf("English semantic font = %q, want %q", got, officialFontPath)
	}
}
