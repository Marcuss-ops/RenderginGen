package overlay

import (
	"fmt"
	"strings"
)

// This file owns the structured payload of the two data compositions the
// runtime catalog publishes: metric_stat and timeline_date. The blocks are
// typed for the same reason SemanticMap is — a schema-valid document cannot be
// silently dropped, and DisallowUnknownFields can only reject what the Go
// mirror actually declares.
//
// The block stays OPTIONAL on the wire. A plan that carries no structured value
// (the presentation route, which transports the displayed text only) keeps
// compiling exactly as before, and legacy plans are unaffected. What is
// enforced is the inverse direction: once a producer DECLARES a block it must be
// complete and it must belong to the item's data target, so a half-filled
// metric fails loudly instead of being displayed as if it had been validated.
// That is the guarantee the runtime catalog's data_fields entries advertise.

// SemanticMetricData is the worker mirror of the metric_stat payload.
type SemanticMetricData struct {
	Value     string `json:"value"`
	Unit      string `json:"unit"`
	Label     string `json:"label,omitempty"`
	Delta     string `json:"delta,omitempty"`
	Precision *int   `json:"precision,omitempty"`
}

// SemanticDateData is the worker mirror of the timeline_date payload.
type SemanticDateData struct {
	Value    string `json:"value"`
	End      string `json:"end,omitempty"`
	Format   string `json:"format,omitempty"`
	Timezone string `json:"timezone,omitempty"`
	Ordering *int   `json:"ordering,omitempty"`
}

// validateSemanticDataBlocks enforces the declared metric/date payload against
// the item it belongs to. The target comes from the same resolver the motion
// gate uses (template MotionTarget first, then the semantic kind), so a plan
// cannot smuggle a metric block onto a phrase card or a date block onto a
// metric card and have it accepted by one gate while the other disagrees.
func validateSemanticDataBlocks(item semanticItem, kind ItemKind) error {
	if item.Metric == nil && item.Date == nil {
		return nil
	}
	if item.Metric != nil && item.Date != nil {
		return fmt.Errorf("overlay: item %q declares both metric and date blocks; one card owns one structured payload", item.ID)
	}
	target := semanticTextMotionTarget(item, kind)

	if metric := item.Metric; metric != nil {
		if target != dataMetricTarget {
			return fmt.Errorf("overlay: item %q declares metric but resolves to motion target %q, not %q", item.ID, target, dataMetricTarget)
		}
		if strings.TrimSpace(metric.Value) == "" {
			return fmt.Errorf("overlay: item %q metric.value is required", item.ID)
		}
		if strings.TrimSpace(metric.Unit) == "" {
			return fmt.Errorf("overlay: item %q metric.unit is required", item.ID)
		}
		if precision := metric.Precision; precision != nil && (*precision < 0 || *precision > dataPrecisionLimit) {
			return fmt.Errorf("overlay: item %q metric.precision %d is outside 0..%d", item.ID, *precision, dataPrecisionLimit)
		}
	}

	if date := item.Date; date != nil {
		if target != dataDateTarget {
			return fmt.Errorf("overlay: item %q declares date but resolves to motion target %q, not %q", item.ID, target, dataDateTarget)
		}
		if strings.TrimSpace(date.Value) == "" {
			return fmt.Errorf("overlay: item %q date.value is required", item.ID)
		}
		if ordering := date.Ordering; ordering != nil && (*ordering < 0 || *ordering > dataOrderingLimit) {
			return fmt.Errorf("overlay: item %q date.ordering %d is outside 0..%d", item.ID, *ordering, dataOrderingLimit)
		}
	}
	return nil
}
