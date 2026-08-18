package overlay

import (
	"encoding/json"
	"strings"
)

// PresetUsage is one row of the plan's render_preset_usage tracking: how many
// layers a (template_id, preset_id) pair produced in a job, and how long the
// render of that job took (render_us is the job-wide Chronon render time —
// Chronon does not yet report per-preset timing).
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
	EntityCount        int    `json:"entity_count"`
	ImportantPhraseCnt int    `json:"important_phrase_count"`
	ImportantWordCnt   int    `json:"important_word_count"`
	ImageCount         int    `json:"image_count"`
	LightLeakCount     int    `json:"light_leak_count"`
	// PresetID is the first preset_id the plan carries (all items are
	// expected to share one per job); empty when the plan is legacy.
	PresetID string `json:"preset_id"`
	// PresetUsage counts layers per (template, preset) pair — the plan's
	// render_preset_usage tracking (section "tracking dei preset").
	PresetUsage []PresetUsage `json:"preset_usage"`
}

// SemanticStats inspects a render plan and counts the overlay kinds it
// declares. Concrete chronon.render-plan.v1 documents yield the zero Stats —
// the semantic counters only exist in PipelineGen's overlay-plan.v1.
func SemanticStats(raw []byte) (Stats, error) {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return Stats{}, err
	}
	if probe.SchemaVersion == "" {
		return Stats{}, nil // concrete plan: no semantic counters
	}
	if probe.SchemaVersion != SemanticSchema {
		return Stats{}, nil // unknown schema: never fail the artifact on stats
	}
	var src semanticPlan
	if err := json.Unmarshal(raw, &src); err != nil {
		return Stats{}, err
	}
	var stats Stats
	for _, item := range src.Items {
		if stats.PresetID == "" && strings.TrimSpace(item.PresetID) != "" {
			stats.PresetID = strings.TrimSpace(item.PresetID)
		}
		template := strings.ToUpper(item.Template)
		switch template {
		case "PERSON", "ORGANIZATION", "LOCATION":
			stats.EntityCount++
		case "IMPORTANT_PHRASE", "QUOTE", "CONCEPT":
			stats.ImportantPhraseCnt++
		case "IMPORTANT_WORD", "NUMBER", "MONEY", "PERCENT":
			stats.ImportantWordCnt++
		case "IMAGE_OVERLAY", "PRODUCT", "LOGO":
			stats.ImageCount++
		case "LIGHT_LEAK":
			stats.LightLeakCount++
		}
		stats.addPresetUsage(template, item)
	}
	return stats, nil
}

// addPresetUsage records one layer per (template, preset) pair, mirroring the
// layers the compiler will actually emit: an entity template that carries an
// asset produces BOTH its text layer and an image_focus_in layer, exactly as
// compileSemantic does. The preset is resolved through the same presetFor the
// compiler uses, so the tracking can never drift from the compiled plan. A
// preset the compiler would reject is skipped — such a plan fails to compile
// anyway and never produces an artifact.
func (s *Stats) addPresetUsage(template string, item semanticItem) {
	preset, err := presetFor(item)
	if err != nil {
		return
	}
	s.incPreset(template, preset)
	if isEntityTemplate(item.Template) && len(item.Assets) > 0 {
		s.incPreset(template, "image_focus_in")
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
