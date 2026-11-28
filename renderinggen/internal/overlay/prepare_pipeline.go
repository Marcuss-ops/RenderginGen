// prepare_pipeline.go owns the two-phase semantic compile the prepare path
// needs: resolve ONCE before any asset byte exists, lower ONCE after the
// materialized images have been probed.
//
// The historical prepare path compiled the whole document twice (a blind pass
// before materialization and a sized pass after) and, for a job carrying a
// prepared render package, compiled a third time and threw the loaded package
// plan away. The stage split in semantic_plan_pipeline.go already separates
// resolve from lower; this file exposes that split so the processor pays one
// resolve and one lower, and the prepared-package path pays none.
//
// The resolved value is a handle, not a plan: it owns the decoded document, the
// registry and the item resolution, while the concrete plan shell stays
// unlowered until LowerAndSeal receives real source dimensions. Lowering is
// single-shot because lowerPlan appends layers to that shell; a second call is
// a programming error, not a re-render.
package overlay

import (
	"fmt"
	"sort"
)

// PreparedResolve is a semantic plan resolved before asset bytes exist and
// lowered afterwards with the probed source dimensions.
type PreparedResolve struct {
	src      semanticPlan
	resolved resolvedPlan
	opts     CompileOptions
	lowered  bool
}

// ResolveSemanticForPrepare decodes, validates and resolves one semantic plan.
// It performs no lowering: the returned handle still has no layers for its
// items, so a caller can materialize Assets() and then lower exactly once with
// the probed image sizes. The decoded document is owned by the handle, so the
// live document contracts are applied to it in place (no compatibility copy).
//
// The error taxonomy is the same one CompileSemantic has always produced
// (invalid JSON, then schema identity, then the strict semantic decode), so the
// prepare path keeps reporting producer bugs the way it always did.
func ResolveSemanticForPrepare(raw []byte, opts CompileOptions) (*PreparedResolve, error) {
	src, err := decodeSemanticPlanForCompile(raw)
	if err != nil {
		return nil, err
	}
	if err := expandEditorialCompositions(&src); err != nil {
		return nil, err
	}
	validated, err := validateSemanticPlan(&src)
	if err != nil {
		return nil, err
	}
	resolved, err := resolvePlan(validated, opts)
	if err != nil {
		return nil, err
	}
	return &PreparedResolve{src: src, resolved: resolved, opts: opts}, nil
}

// LowerAndSeal lowers the resolved plan with sizes and seals it into the public
// compile tuple. sizes is keyed by the plan's logical asset path (see
// PendingImageAssets); nil falls back to the sizes carried by the resolve
// options, which keeps CompileSemanticWithAssetSizes byte-identical.
func (r *PreparedResolve) LowerAndSeal(sizes map[string]ImageSize) (CompileResult, error) {
	if r == nil {
		return CompileResult{}, fmt.Errorf("overlay: prepared resolve is nil")
	}
	if r.lowered {
		return CompileResult{}, fmt.Errorf("overlay: prepared resolve for plan %q was already lowered", r.src.PlanID)
	}
	r.lowered = true
	if sizes == nil {
		sizes = r.opts.AssetSizes
	}
	resolved := r.resolved
	// The probe belongs to the document being lowered.
	resolved.Document.assetSizes = sizes
	concrete, stats, err := lowerPlan(resolved)
	if err != nil {
		return CompileResult{}, err
	}
	return sealPreparedPlan(concrete, stats, resolved, r.src, r.opts)
}

// PlanShell is the concrete plan before item lowering: canvas, output profile,
// audio policy and the non-item layers. The prepare gates read it to reject an
// unsupported audio mode before any asset is downloaded.
func (r *PreparedResolve) PlanShell() *Plan {
	if r == nil {
		return nil
	}
	return r.resolved.Plan
}

// Assets is the content-addressed manifest the resolve registered. It is the
// same manifest sealPlan returns, available before lowering so the processor
// can materialize the bytes the lower will point at.
func (r *PreparedResolve) Assets() []Asset {
	if r == nil || r.resolved.Registry == nil {
		return nil
	}
	return r.resolved.Registry.Assets()
}

// UnknownTemplates is the resolve pass's honest classification of template_id
// values that matched no registry row.
func (r *PreparedResolve) UnknownTemplates() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.resolved.Unknown...)
}

// DurationFrames is the plan duration known BEFORE lowering (duration_ms). It
// is 0 when the document derives its duration from item end times, which is why
// the processor re-validates the chunk contract against the sealed plan.
func (r *PreparedResolve) DurationFrames() int64 {
	if r == nil || r.resolved.Plan == nil {
		return 0
	}
	return r.resolved.Plan.Canvas.DurationFrames
}

// Lowered reports whether LowerAndSeal already consumed this resolve.
func (r *PreparedResolve) Lowered() bool {
	return r != nil && r.lowered
}

// PendingImageAssets returns the logical paths of the images whose pixel
// dimensions the lowering needs: the assets of image and entity items and the
// assets carried by each segment of a chain. The processor probes exactly
// these files, so the lower settles every derived box from the real bytes
// instead of the blind shape.
//
// A chain container owns no image of its own: the producer fuses N standalone
// overlays into one item and only the segments keep the asset references, so a
// probe that read just the container would leave every chain image with no
// probed size and fail closed at lowering.
//
// A size that is probed but not needed is harmless (lowering only reads the
// paths its layers reference); a pending asset that is missing is not, because
// probedImageSize fails closed on the layer that awaits it.
func (r *PreparedResolve) PendingImageAssets() []string {
	if r == nil || r.resolved.Registry == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(r.resolved.Items))
	var paths []string
	add := func(assetID string) {
		path := r.resolved.Registry.Path(assetID)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for _, item := range r.resolved.Items {
		if isChainKind(item.Kind) {
			for _, seg := range item.Item.chainSegments {
				if seg.AssetID != "" {
					add(seg.AssetID)
				}
				for _, ref := range seg.Assets {
					add(ref.ID)
				}
				for _, layer := range seg.ImageLayers {
					add(layer.AssetID)
				}
			}
			continue
		}
		if !isImageKind(item.Kind) && !isEntityKind(item.Kind) {
			continue
		}
		for _, ref := range item.Item.Assets {
			add(ref.ID)
		}
		for _, layer := range item.Item.ImageLayers {
			add(layer.AssetID)
		}
	}
	sort.Strings(paths)
	return paths
}

// sealPreparedPlan is the single post-lower pipeline: seal, then the stages
// that depend on every layer existing (text plates, animator clock, caption
// collisions) and finally the prepared sidecar derived from the sealed plan.
func sealPreparedPlan(plan *Plan, stats Stats, resolved resolvedPlan, src semanticPlan, opts CompileOptions) (CompileResult, error) {
	plan, assets, stats, unknown, err := sealPlan(plan, stats, resolved, opts)
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
	// The package is rebuilt from the final plan; the renames and the subtitle
	// burn both make a mid-prepare build stale.
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
