package overlay

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

func TestDocumentaryPhraseHighlightingLowersSemanticSpans(t *testing.T) {
	ids := make([]string, 0, 15)
	for _, id := range motion.Registry.CategoryMotionIDs("documentary_highlighting_v1") {
		if strings.HasPrefix(id, "doc_") {
			ids = append(ids, id)
		}
	}
	if len(ids) != 15 {
		t.Fatalf("documentary highlighting catalog has %d native presets, want 15", len(ids))
	}
	phraseByID := map[string]string{
		"doc_01_yellow_marker_sweep":         "La chiusura delle indagini non equivale a una condanna.",
		"doc_02_multiline_stagger_highlight": "Le parti possono contestare gli accertamenti con consulenze tecniche.",
		"doc_03_documentary_quote_marker":    "Ogni reperto va valutato nel contesto e con verifiche incrociate.",
		"doc_04_archival_source_highlight":   "Le relazioni di stampa vanno distinte dagli atti ufficiali depositati.",
		"doc_05_selective_phrase_tracking":   "La traccia controversa resta oggetto di valutazioni tecniche contrapposte.",
		"doc_06_red_keyword_punch":           "Nessun singolo reperto dimostra da solo una responsabilità.",
		"doc_07_word_impact_reveal":          "Le nuove ipotesi investigative restano da verificare nel contraddittorio.",
		"doc_08_mixed_font_emphasis":         "Le sentenze definitive vanno separate dalle indagini ancora aperte.",
		"doc_09_cinematic_underline_draw":    "La procura può valutare processo, archiviazione o ulteriori integrazioni.",
		"doc_10_selective_contrast_focus":    "Ogni consulenza va letta insieme ai metodi e ai suoi limiti.",
		"doc_11_headline_cinematic_zoom":     "Le memorie difensive aprono una fase, non anticipano una decisione.",
		"doc_12_quote_line_by_line_reveal":   "La presunzione di innocenza accompagna ogni indagato fino alla sentenza definitiva.",
		"doc_13_analog_scanner_highlight":    "La memoria di Chiara Poggi merita un racconto rispettoso e sobrio.",
		"doc_14_dual_highlight_sequence":     "Fonti processuali e cronologia aiutano a distinguere fatti e interpretazioni.",
		"doc_15_documentary_source_focus":    "Ogni affermazione del copione deve poggiare su fonti verificabili.",
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			phraseText := phraseByID[id]
			raw := []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"highlight-%[1]s","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","motion_id":%[2]q,"text":%[3]q,"start_ms":0,"end_ms":5000}]}`, id, id, phraseText))
			result, err := CompileSemantic(raw)
			if err != nil {
				t.Fatalf("compile %q: %v", id, err)
			}
			var phrase *Layer
			for i := range result.Plan.Layers {
				if result.Plan.Layers[i].ID == "phrase" && result.Plan.Layers[i].Type == "text" {
					phrase = &result.Plan.Layers[i]
					break
				}
			}
			if phrase == nil || len(phrase.TextSpans) == 0 || phrase.Animation == nil || len(phrase.Animation.Tracks) == 0 {
				t.Fatalf("%q did not lower exact target spans and a phrase entrance: %+v", id, phrase)
			}
			markerCount := 0
			for _, layer := range result.Plan.Layers {
				if strings.HasPrefix(layer.ID, "phrase-marker-") && layer.Type == "shape" && layer.Animation != nil {
					markerCount++
				}
			}
			if id != "doc_07_word_impact_reveal" && markerCount == 0 {
				t.Fatalf("%q did not lower its animated marker shape", id)
			}
			if id == "doc_07_word_impact_reveal" && len(phrase.TextAnimators) == 0 {
				t.Fatal("word impact did not lower its per-word animator")
			}
			if id == "doc_12_quote_line_by_line_reveal" && len(phrase.TextAnimators) == 0 {
				t.Fatal("line reveal did not lower its line animator")
			}
			wire, err := result.Plan.Marshal()
			if err != nil {
				t.Fatalf("marshal %q: %v", id, err)
			}
			var document struct {
				Layers []struct {
					ID    string            `json:"id"`
					Spans []json.RawMessage `json:"spans"`
				} `json:"layers"`
			}
			if err := json.Unmarshal(wire, &document); err != nil {
				t.Fatalf("decode wire plan: %v", err)
			}
			for _, layer := range document.Layers {
				if layer.ID == "phrase" && len(layer.Spans) == len(phrase.TextSpans) {
					return
				}
			}
			t.Fatalf("%q lost semantic spans in Chronon wire plan", id)
		})
	}
}
