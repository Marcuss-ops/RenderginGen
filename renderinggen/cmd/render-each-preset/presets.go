// presets.go owns the render-each-preset catalog: one entry per official
// preset, carrying the same overlay kind (image/text) and entity geometry the
// runtime entity path uses. The canary must exercise the same image class as
// production, so the fixtures are real assets, never synthetic PNGs.
package main

import "github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"

// PresetItem is one preset canary entry: the concrete FastEntityOverlay the
// canary compiles and renders plus the metadata used for reporting.
type PresetItem struct {
	ID          string
	Kind        string // "image" or "text"
	PresetName  string
	Title       string
	Description string
	Overlay     overlay.FastEntityOverlay
}

// defaultPresets returns the fixed preset catalog. fontPath is the
// workspace-relative font asset the text presets reference.
func defaultPresets(fontPath string) []PresetItem {
	return []PresetItem{
		// ── PRESET IMMAGINI ──
		{
			ID:          "preset_image_scale_in",
			Kind:        "image",
			PresetName:  "image_scale_in",
			Title:       "Image Scale In (Pop)",
			Description: "Entrata con scala dinamica morbida da 0.85x a 1.0x",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "scale_drop",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_image_slide_left",
			Kind:        "image",
			PresetName:  "image_slide_left",
			Title:       "Image Slide Left",
			Description: "Entrata laterale fluida da sinistra verso il centro",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "slide_in",
				Opacity:    1.0,
				Translate:  []float64{0, 0},
			},
		},
		{
			ID:          "preset_image_slide_right",
			Kind:        "image",
			PresetName:  "image_slide_right",
			Title:       "Image Slide Right",
			Description: "Entrata laterale da destra verso il centro",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "slide_in",
				Opacity:    1.0,
				Translate:  []float64{0, 0},
			},
		},
		{
			ID:          "preset_image_focus_in",
			Kind:        "image",
			PresetName:  "image_focus_in",
			Title:       "Image Focus In (Zoom)",
			Description: "Zoom progressivo morbido al centro dell'attenzione",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "focus_in",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_modern_rounded_pop",
			Kind:        "image",
			PresetName:  "modern_rounded_pop",
			Title:       "Modern Rounded Pop (SDF)",
			Description: "Card con angoli arrotondati calcolati in tempo reale via SDF CUDA",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "scale_drop",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_bottom_card_rise",
			Kind:        "image",
			PresetName:  "bottom_card_rise",
			Title:       "Bottom Card Rise",
			Description: "Risalita dal bordo inferiore dello schermo (ottimale per grafici/card)",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "reveal_from_bottom",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_image_fade_in",
			Kind:        "image",
			PresetName:  "image_fade_in",
			Title:       "Image Fade In",
			Description: "Dissolvenza classica pulita a centro schermo",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "fade_in",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_image_fast_fade",
			Kind:        "image",
			PresetName:  "image_fast_fade",
			Title:       "Image Fast Fade",
			Description: "Dissolvenza rapida dell'immagine al centro schermo",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "fade_in",
				Opacity:    1.0,
			},
		},

		// ── PRESET FRASI / TESTO ──
		{
			ID:          "preset_lower_third_safe",
			Kind:        "text",
			PresetName:  "lower_third_safe",
			Title:       "Lower Third Safe (Nome Entità)",
			Description: "Didascalia nome/ruolo posizionata nella safe-area inferiore",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   125,
				Text:       "Gerard Butler",
				Font:       fontPath,
				Size:       72,
				Color:      []float64{1.0, 1.0, 1.0, 1.0},
				Position:   "lower_third",
				Animation:  "fade_in",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_caption_card",
			Kind:        "text",
			PresetName:  "caption_card",
			Title:       "Caption Card (Citazione / Didascalia)",
			Description: "Testo informativo a centro schermo con dissolvenza morbida",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   125,
				Text:       "Hollywood Lead Actor & Producer",
				Font:       fontPath,
				Size:       54,
				Color:      []float64{0.95, 0.95, 1.0, 1.0},
				Position:   "center",
				Animation:  "fade_in",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_clean_slide_up",
			Kind:        "text",
			PresetName:  "clean_slide_up",
			Title:       "Clean Slide Up (Headline)",
			Description: "Frase a comparsa dal basso fluida per titoli e sezioni",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   125,
				Text:       "Global Technology Infrastructure",
				Font:       fontPath,
				Size:       58,
				Color:      []float64{1.0, 1.0, 1.0, 1.0},
				Position:   "center",
				Animation:  "reveal_from_bottom",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_phrase_scale_in",
			Kind:        "text",
			PresetName:  "phrase_scale_in",
			Title:       "Phrase Scale In / Snap Scale",
			Description: "Ingresso a scala pop dinamico per dati numerici e callout",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   125,
				Text:       "+450% GPU Rendering Speed",
				Font:       fontPath,
				Size:       64,
				Color:      []float64{1.0, 1.0, 1.0, 1.0},
				Position:   "center",
				Animation:  "scale_drop",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_phrase_fade_in",
			Kind:        "text",
			PresetName:  "phrase_fade_in",
			Title:       "Phrase Fade In",
			Description: "Dissolvenza testo pulita per frasi di chiusura o narrazione",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   125,
				Text:       "Fast Entity Overlay Pipeline — Active",
				Font:       fontPath,
				Size:       52,
				Color:      []float64{1.0, 1.0, 1.0, 1.0},
				Position:   "center",
				Animation:  "fade_in",
				Opacity:    1.0,
			},
		},
	}
}
