package processor

import (
	"encoding/json"
	"fmt"
)

const overlayPrepareSchema = "renderinggen.overlay-prepare.v1"

type overlayPrepareEnvelope struct {
	SchemaVersion string `json:"schema_version"`
	PlanID        string `json:"plan_id"`
	VideoID       string `json:"video_id"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	FPSNum        int    `json:"fps_num"`
	FPSDen        int    `json:"fps_den"`
	Intents       []struct {
		TemplateID  string `json:"template_id"`
		TimingState string `json:"timing_state"`
	} `json:"intents"`
}

func isOverlayPrepare(raw []byte) bool {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return false
	}
	return probe.SchemaVersion == overlayPrepareSchema
}

func validateOverlayPrepare(raw []byte) error {
	var p overlayPrepareEnvelope
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("processor: decode overlay.prepare: %w", err)
	}
	if p.SchemaVersion != overlayPrepareSchema || p.PlanID == "" || p.VideoID == "" {
		return fmt.Errorf("processor: overlay.prepare identity/schema is required")
	}
	if p.Width <= 0 || p.Height <= 0 || p.FPSNum <= 0 || p.FPSDen <= 0 || len(p.Intents) == 0 {
		return fmt.Errorf("processor: overlay.prepare canvas and intents are required")
	}
	for i, intent := range p.Intents {
		if intent.TemplateID == "" || intent.TimingState != "PENDING" {
			return fmt.Errorf("processor: overlay.prepare intent[%d] must have template_id and PENDING timing", i)
		}
	}
	return nil
}
