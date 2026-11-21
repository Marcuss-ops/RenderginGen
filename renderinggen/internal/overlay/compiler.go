// Package overlay owns the RenderingGen-side boundary between PipelineGen and
// Chronon. RenderingGen is an EXECUTION WORKER: it validates a job, materializes
// its assets, writes plan.json, renders with Chronon and publishes the artifact.
// It does not perform NER, entity linking or editorial ranking.
//
// PipelineGen owns the semantic decisions and emits overlay-plan.v1. The
// worker performs the mechanical lowering through the RenderingGen-owned
// official preset catalog, then continues through one asset/render/artifact
// pipeline. Chronon is only the execution engine.
//
// File layout: compiler.go owns the schema identities and the CompileSemantic
// entry point, plan_model.go the concrete Chronon render-plan model,
// semantic_contract_types.go the projection of the PipelineGen document and
// semantic_compile.go the semantic→concrete lowering driver (the single
// resolve → compile pass and the per-kind dispatch), the per-kind compilers in
// semantic_entity_compile.go / semantic_image_compile.go /
// semantic_text_compile.go / semantic_group_compile.go /
// semantic_video_compile.go / semantic_text_accents.go, subtitle_burn.go the
// ASS subtitle lowering and semantic_helpers.go the preset/motion helpers.
package overlay

import (
	"encoding/json"
	"fmt"
)

// SemanticSchema is PipelineGen's semantic overlay contract. RenderingGen
// compiles it mechanically; PipelineGen remains the owner of its decisions.
const SemanticSchema = "renderinggen.overlay-plan.v1"

// RenderPlanSchemaV2/V3 and versions are the canonical concrete Chronon wire
// identities.
const (
	RenderPlanSchemaV2  = "chronon.render-plan.v2"
	RenderPlanVersionV2 = 2
	RenderPlanSchemaV3  = "chronon.render-plan.v3"
	RenderPlanVersionV3 = 3
)

// RenderPlanWireVersion selects the concrete schema required by a plan's features.
// Compilation and all serializers share this selector to prevent contract drift.
func RenderPlanWireVersion(plan *Plan) (string, int) {
	if plan == nil {
		return RenderPlanSchemaV2, RenderPlanVersionV2
	}
	if len(plan.CameraSamples) > 0 || plan.CameraAnimation != nil || plan.Camera != nil && len(plan.Layers) > 0 || plan.Audio != nil {
		return RenderPlanSchemaV3, RenderPlanVersionV3
	}
	for _, layer := range plan.Layers {
		if layer.Type == "adjustment" {
			return RenderPlanSchemaV3, RenderPlanVersionV3
		}
		if layer.Type == "shape" || layer.Shape != nil || layer.FrameStroke != nil || layer.ScreenSpace ||
			len(layer.Effects) > 0 || len(layer.EffectParamTracks) > 0 || layer.Parent != "" ||
			layer.TransitionIn != nil || len(layer.Masks) > 0 || layerAnimationNeedsV3(layer.Animation) {
			return RenderPlanSchemaV3, RenderPlanVersionV3
		}
		if textAnimatorsNeedV3(layer.TextAnimators) {
			return RenderPlanSchemaV3, RenderPlanVersionV3
		}
	}
	return RenderPlanSchemaV2, RenderPlanVersionV2
}

// SemanticSchemaVersion is the integer form of the same contract version. It
// is the SINGLE source for the version the worker advertises (`overlay_schema`
// on /health and in the worker registry): advertising a bare integer that no
// consumer could map back to a document schema was an ambiguous identity fact.
// The advertised value is pinned to SemanticSchema by
// schema_version_test.go.
const SemanticSchemaVersion = 1

func newPlan(jobID string, width, height, fpsNum, fpsDen int, duration int64) *Plan {
	return &Plan{Schema: RenderPlanSchemaV2, Version: RenderPlanVersionV2, JobID: jobID,
		Canvas: Canvas{Width: width, Height: height, FPSNum: fpsNum, FPSDen: fpsDen, DurationFrames: duration},
		Output: Output{Path: "result.mp4", Format: "mp4", Codec: "h264"}}
}

// canonicalProfileColorSpace maps a canonical Velox output profile to the
// concrete Chronon colour contract it declares. Every canonical profile is
// BT.709 media (the assembly contract pins bt709 for V1/V2 and the overlay
// source lane pins bt709 space/primaries). The field must be written into the
// concrete plan: Chronon's decoder defaults an absent output.color_space to
// sRGB, and that default stamps the NVENC parameter sets with an sRGB transfer
// function, which VELOX_ASSEMBLY_READY_V1 rejects after the render. Profiles
// with no pinned colour declaration keep Chronon's own default.
func canonicalProfileColorSpace(profileID string) string {
	switch profileID {
	case "VELOX_ASSEMBLY_READY_V1", "VELOX_ASSEMBLY_READY_V2",
		"VELOX_OVERLAY_VIDEO_ONLY_V1", "VELOX_OVERLAY_VIDEO_ONLY_V2",
		"VELOX_OVERLAY_VIDEO_ONLY_V3":
		return "rec709"
	default:
		return ""
	}
}

// CompileSemantic lowers the PipelineGen semantic contract to the concrete
// Chronon plan consumed by the worker. It returns the plan, its materialized
// assets and the ledger counters produced by the SAME compile pass, so the
// ledger can never drift from the layers actually emitted.
//
// It resolves RenderingGen's official preset catalog; it does not perform NER,
// entity linking or editorial ranking — those decisions arrive in
// kind/template_id/preset_id/text from PipelineGen.
//
// It returns the typed plan so downstream stages (asset-path normalization,
// subtitle burn, metadata extraction, backend gating) mutate ONE in-memory
// object instead of re-decoding JSON; the raw document is decoded exactly once
// (decodeSemanticPlan) and the plan is marshaled exactly once at the Chronon
// boundary via Plan.Marshal. The subtitle contract rides on the result
// (CompileResult.Subtitle / ResolveSubtitleStyle) so the prepare path never
// re-reads the raw bytes after this call.
//
// Fail-closed: the compiler is the single owner of the semantic→concrete
// lowering. Byte-for-byte pass-through of an untyped document bypasses
// validation and style resolution, so anything that is not the semantic
// overlay-plan contract is rejected instead of executed.
//
// CompileSemantic is the BLIND compile: no source image bytes have been
// materialized yet, so a card whose width/aspect derives from its photo keeps
// its transient markers for a later FitImageLayersToSizes pass. The processor
// uses CompileSemanticWithAssetSizes once it has probed the bytes, so the
// emitted plan is already final.
func CompileSemantic(raw []byte) (CompileResult, error) {
	return compileSemanticWithOptions(raw, CompileOptions{})
}

// CompileSemanticWithAssetSizes is the SIZED compile: sizes maps every image's
// logical asset path to its probed pixel dimensions, so entity cards, derived
// widths, linked premium geometry and caption anchoring are resolved inside
// lowering. A layer that awaits a size and has no entry fails the compile
// closed; sizes == nil selects the blind compile.
func CompileSemanticWithAssetSizes(raw []byte, sizes map[string]ImageSize) (CompileResult, error) {
	return compileSemanticWithOptions(raw, CompileOptions{AssetSizes: sizes})
}

// CompileSemanticWithOptions is the dependency-explicit compile. The worker
// passes its native camera baker here instead of mutating package state, so
// concurrent compiles and test doubles cannot rewrite each other's camera
// authority.
func CompileSemanticWithOptions(raw []byte, opts CompileOptions) (CompileResult, error) {
	return compileSemanticWithOptions(raw, opts)
}

func compileSemanticWithOptions(raw []byte, opts CompileOptions) (CompileResult, error) {
	src, err := decodeSemanticPlan(raw)
	if err != nil {
		// Error path only: the historical probes (invalid-JSON check, then
		// schema check) ran BEFORE the strict decode and their precedence is
		// part of the observable contract. They are reproduced here so the
		// success path pays exactly one decode of the document.
		var probe struct {
			SchemaVersion string `json:"schema_version"`
		}
		if perr := json.Unmarshal(raw, &probe); perr != nil {
			return CompileResult{}, fmt.Errorf("overlay: decode plan: %w", perr)
		}
		if probe.SchemaVersion != SemanticSchema {
			return CompileResult{}, fmt.Errorf("overlay: unsupported plan schema %q (semantic %q is the only accepted contract)", probe.SchemaVersion, SemanticSchema)
		}
		return CompileResult{}, fmt.Errorf("overlay: decode semantic plan: %w", err)
	}
	if src.SchemaVersion != SemanticSchema {
		return CompileResult{}, fmt.Errorf("overlay: unsupported plan schema %q (semantic %q is the only accepted contract)", src.SchemaVersion, SemanticSchema)
	}
	plan, assets, stats, unknown, err := compileSemanticDecoded(&src, opts)
	if err != nil {
		return CompileResult{}, err
	}
	// Chronon decodes a text layer's style.background into box_style but never
	// draws that box. Lower the plate to a native shape layer here so a
	// payload's card_background is visible instead of silently dropped.
	liftTextBackgroundPlates(plan)
	// Text lowerers author selector/property keyframes from frame 0, while
	// Chronon samples text animators on the composition clock. Translate once
	// after every layer (and its lifted plate) exists, otherwise every chain
	// card after the first opens with its typewriter selector already elapsed
	// and renders fully revealed.
	shiftTextAnimatorsToCompositionClock(plan)
	if err := ValidateEntityCaptionCollisions(plan.Layers, plan.Canvas.Width, plan.Canvas.Height); err != nil {
		return CompileResult{}, fmt.Errorf("overlay: %w", err)
	}
	// The metadata probe (top-level language) is the same field the strict
	// decode already read; a separate lenient decode could never fail after
	// the strict one succeeded, so its value is taken from the decoded plan.
	prepared, err := buildPreparedPackage(plan, src.Language, assets)
	if err != nil {
		return CompileResult{}, err
	}
	hash, fontHash, burn, present := subtitleAssetFrom(src.Subtitles)
	return CompileResult{
		Plan: plan, Assets: assets, Stats: stats, Prepared: prepared,
		UnknownTemplates: unknown,
		Subtitle:         SubtitleRef{Hash: hash, FontHash: fontHash, Burn: burn, Present: present},
		subtitleStyleRaw: src.Subtitles.styleMap(),
		subtitleWidth:    src.Width,
		subtitleHeight:   src.Height,
	}, nil
}
