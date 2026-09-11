package overlay

import "strings"

// PresetUsage is one row of the plan's render_preset_usage tracking: how many
// layers a (template_id, preset_id) pair produced in a job.
type PresetUsage struct {
	TemplateID string `json:"template_id"`
	PresetID   string `json:"preset_id"`
	Count      int    `json:"count"`
}

// Stats is the small, stable summary the artifact pipeline records for a
// rendered job (plan section "DB metrics"): how many overlays of each semantic
// kind the plan carried and which preset was selected. The worker only counts
// what PipelineGen already decided — it never re-derives importance.
type Stats struct {
	EntityCount        int `json:"entity_count"`
	ImportantPhraseCnt int `json:"important_phrase_count"`
	ImportantWordCnt   int `json:"important_word_count"`
	ImageCount         int `json:"image_count"`
	LightLeakCount     int `json:"light_leak_count"`
	// PresetID is the first preset_id the plan carries (all items are
	// expected to share one per job); empty when the plan is legacy.
	PresetID string `json:"preset_id"`
	// PresetUsage counts layers per (template, preset) pair — the plan's
	// render_preset_usage tracking (section "tracking dei preset").
	PresetUsage []PresetUsage `json:"preset_usage"`
}

// CompileResult is the single output of the semantic compiler: the concrete
// Chronon plan, its materialized assets and the ledger counters for the SAME
// pass. The counters are produced while compiling, so they can never drift
// from the layers that were actually emitted.
type CompileResult struct {
	Plan   *Plan
	Assets []Asset
	Stats  Stats
}

// addResolved books one item of a compile pass: the counters' only entry point
// from the compiler. It reads the SAME resolvedItem the compiler lowered, so
// the ledger can never re-derive a kind, preset or preset family that the
// compiler decided differently.
func (s *Stats) addResolved(ri resolvedItem) {
	if s.PresetID == "" {
		s.PresetID = strings.TrimSpace(ri.PresetID)
	}
	switch ri.Spec.Stat {
	case overlayStatEntity:
		s.EntityCount++
	case overlayStatPhrase:
		s.ImportantPhraseCnt++
	case overlayStatWord:
		s.ImportantWordCnt++
	case overlayStatImage:
		s.ImageCount++
	case overlayStatLightLeak:
		s.LightLeakCount++
	}
	template := strings.ToUpper(strings.TrimSpace(ri.Item.Template))
	if ri.PresetID != "" {
		s.incPreset(template, ri.PresetID)
	}
	// An entity card that carries an asset emits BOTH its text layer and its
	// image layer; both presets were already resolved by the compiler, so the
	// tracking can never drift from the compiled plan.
	if isEntityKind(ri.Kind) && len(ri.Item.Assets) > 0 && ri.ImagePreset.ID != "" {
		s.incPreset(template, ri.ImagePreset.ID)
	}
}

func (s *Stats) incPreset(template, preset string) {
	for i := range s.PresetUsage {
		if s.PresetUsage[i].TemplateID == template && s.PresetUsage[i].PresetID == preset {
			s.PresetUsage[i].Count++
			return
		}
	}
	s.PresetUsage = append(s.PresetUsage, PresetUsage{TemplateID: template, PresetID: preset, Count: 1})
}
