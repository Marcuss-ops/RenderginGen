package overlay

// dataCompositionDefinition owns the picker-facing contract for the semantic
// metric and date compositions. The fields below are enforced by the compiler:
// overlay-plan.v1 carries a typed metric/date block, the Go mirror and the
// schema are pinned together, and a declared block must be complete and must
// belong to the item's data target (see validateSemanticDataBlocks). The block
// itself stays optional so a plan that transports only the displayed text keeps
// compiling.
const (
	// dataMetricTarget / dataDateTarget are the canonical motion targets of the
	// two data compositions. The compiler's block validation and the catalog
	// below read this single declaration.
	dataMetricTarget = "metric"
	dataDateTarget   = "date"
	// dataPrecisionLimit / dataOrderingLimit are the published bounds of the
	// two numeric payload fields; the JSON Schema pins the same values.
	dataPrecisionLimit = 12
	dataOrderingLimit  = 1000000
)

type dataCompositionDefinition struct {
	ID           string
	Kind         ItemKind
	Cardinality  string
	Description  string
	MotionTarget string
	Fields       []RuntimeDataField
}

var dataCompositionCatalog = []dataCompositionDefinition{
	{
		ID: "metric_stat", Kind: KindMetricStat, Cardinality: "one metric",
		Description: "Metric motion selected independently from text styling.", MotionTarget: dataMetricTarget,
		Fields: []RuntimeDataField{
			{Name: "value", Required: true, Enforced: true, Description: "The measured number as displayed."},
			{Name: "unit", Required: true, Enforced: true, Description: "The unit suffix the value is shown with."},
			{Name: "label", Enforced: true, Description: "What the metric describes."},
			{Name: "delta", Enforced: true, Description: "Comparison against a previous value."},
			{Name: "precision", Enforced: true, Description: "Declared decimal places; the engine default applies when absent."},
		},
	},
	{
		ID: "timeline_date", Kind: KindTimelineDate, Cardinality: "one date or timeline event",
		Description: "Date and timeline motion selected independently from text styling.", MotionTarget: dataDateTarget,
		Fields: []RuntimeDataField{
			{Name: "value", Required: true, Enforced: true, Description: "The date or instant shown."},
			{Name: "end", Enforced: true, Description: "Interval end; absent means a single date."},
			{Name: "format", Enforced: true, Description: "Display format; the locale default applies when absent."},
			{Name: "timezone", Enforced: true, Description: "Display zone; UTC applies when absent."},
			{Name: "ordering", Enforced: true, Description: "Event order inside a timeline composition."},
		},
	},
}

func dataCompositionFor(id string) *dataCompositionDefinition {
	for index := range dataCompositionCatalog {
		if dataCompositionCatalog[index].ID == id {
			return &dataCompositionCatalog[index]
		}
	}
	return nil
}

func dataCompositionIDs() []string {
	ids := make([]string, len(dataCompositionCatalog))
	for index, definition := range dataCompositionCatalog {
		ids[index] = definition.ID
	}
	return ids
}

func dataCompositionTarget(id string) string {
	if definition := dataCompositionFor(id); definition != nil {
		return definition.MotionTarget
	}
	return ""
}

func runtimeDataFields() []RuntimeDataField {
	var fields []RuntimeDataField
	for _, definition := range dataCompositionCatalog {
		for _, field := range definition.Fields {
			field.CompositionID = definition.ID
			fields = append(fields, field)
		}
	}
	return fields
}
