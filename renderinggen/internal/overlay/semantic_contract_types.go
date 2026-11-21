package overlay

// The struct fields below are the compiler's projection of
// contracts/overlay-plan.v1.schema.json. The two declarations must describe
// EXACTLY the same property set in both directions —
// TestContractSchemaMatchesCompilerStructs fails on any drift — so a producer
// cannot send a field the worker silently drops, and the worker cannot accept
// a field the published contract does not declare (which is what made
// motion_id/motion_params unusable for a schema-validating producer before).
// Fields marked "producer metadata" are accepted for contract completeness and
// intentionally play no part in the lowering.
type semanticPlan struct {
	SchemaVersion   string          `json:"schema_version"`
	PlanID          string          `json:"plan_id"`
	VideoID         string          `json:"video_id"`
	ProjectID       string          `json:"project_id,omitempty"`       // producer metadata
	ScriptName      string          `json:"script_name,omitempty"`      // producer delivery metadata
	Language        string          `json:"language,omitempty"`         // producer delivery metadata
	RendererVersion string          `json:"renderer_version,omitempty"` // producer metadata
	Fingerprint     string          `json:"fingerprint,omitempty"`      // producer metadata
	Source          *semanticSource `json:"source,omitempty"`
	ForegroundScale int             `json:"foreground_scale_percent,omitempty"`
	// EntityVariationSeed salts entity style sampling. Empty keeps the
	// plan_id-derived choice, so existing plans compile unchanged.
	EntityVariationSeed string `json:"entity_variation_seed,omitempty"`
	// SourceFrame is the optional card treatment of the source clip (border
	// frame + drop shadow + rounded corners + on-edge stroke). An unresolvable
	// declaration is a compile error instead of a silently dropped style block.
	SourceFrame *semanticSourceFrame `json:"source_frame,omitempty"`
	ClipEffects []semanticClipEffect `json:"clip_effects,omitempty"`
	ClipGrid    *semanticClipGrid    `json:"clip_grid,omitempty"`
	Width       int                  `json:"width"`
	Height      int                  `json:"height"`
	FPSNum      int                  `json:"fps_num"`
	FPSDen      int                  `json:"fps_den"`
	// DurationMS is the explicit clip duration. When provided it seeds the
	// canvas duration before items are processed; items can only extend it.
	// Required when items is empty (clip render without entity overlays).
	DurationMS       int64                    `json:"duration_ms,omitempty"`
	SceneCamera      *SemanticSceneCamera     `json:"scene_camera,omitempty"`
	OutputProfileID  string                   `json:"output_profile_id"`
	MediaContract    string                   `json:"media_contract,omitempty"` // producer media metadata
	Background       *semanticBackground      `json:"background,omitempty"`
	Subtitles        *semanticSubtitles       `json:"subtitles,omitempty"`
	Watermark        *semanticWatermark       `json:"watermark,omitempty"`
	Audio            *semanticAudio           `json:"audio,omitempty"`
	MapCompositionID string                   `json:"map_composition_id,omitempty"`
	Items            []semanticItem           `json:"items"`
	AnimationGroups  []semanticAnimationGroup `json:"animation_groups,omitempty"`
	// LegacyCompositeReplay is the explicit replay marker for sealed
	// pre-group_motion_id composites. New plans must name group_motion_id;
	// without this marker image_layers fail closed instead of silently taking
	// the child-by-child legacy path. It is not emitted by PipelineGen.
	LegacyCompositeReplay bool `json:"legacy_composite_replay,omitempty"`
	// assetSizes carries the probed pixel size of every materialized image this
	// compile must lay out. nil means the blind compile (no I/O yet): cards that
	// await their source aspect keep their transient markers and a later
	// FitImageLayersToSizes pass may finish them. A non-nil map — even empty —
	// is the SIZED compile: every waiting layer must find its size here, or the
	// compile fails closed. Unexported: it is an input to lowering, not part of
	// the published document.
	assetSizes map[string]ImageSize `json:"-"`
	// features is the lowering-time capability record. It is unexported on
	// purpose: it is a compiler output, not part of the published document, and
	// lowerPlan seals it onto the concrete Plan. The json:"-" tag keeps the
	// schema-parity pin (which walks untagged fields) from treating it as a
	// decoded wire field.
	features planFeatures `json:"-"`
}

// semanticAnimationGroup is the V2 heterogeneous animation owner. Members may
// mix important phrases, images, captions and metric/date cards; the group owns
// their shared timeline and one motion per role, while the ordinary per-kind
// lowering still produces the layers.
type semanticAnimationGroup struct {
	ID             string                         `json:"id"`
	StartMS        int64                          `json:"start_ms"`
	EndMS          int64                          `json:"end_ms"`
	ChoreographyID string                         `json:"choreography_id,omitempty"`
	Params         map[string]any                 `json:"params,omitempty"`
	Members        []semanticAnimationGroupMember `json:"members"`
}

type semanticAnimationGroupMember struct {
	ItemID       string         `json:"item_id"`
	Role         string         `json:"role"`
	MotionID     string         `json:"motion_id,omitempty"`
	MotionParams map[string]any `json:"motion_params,omitempty"`
	OffsetMS     *int64         `json:"offset_ms,omitempty"`
	EndOffsetMS  *int64         `json:"end_offset_ms,omitempty"`
}

type semanticClipGrid struct {
	Scope       string   `json:"scope,omitempty"`
	Style       string   `json:"style,omitempty"`
	SpacingPX   float64  `json:"spacing_px"`
	LineWidthPX *float64 `json:"line_width_px,omitempty"`
	DotRadiusPX *float64 `json:"dot_radius_px,omitempty"`
	Color       string   `json:"color"`
	Opacity     float64  `json:"opacity"`
}

type semanticClipEffect struct {
	Type       string   `json:"type"`
	Amount     *float64 `json:"amount,omitempty"`
	Radius     *float64 `json:"radius,omitempty"`
	Softness   *float64 `json:"softness,omitempty"`
	BlockSize  *int     `json:"block_size,omitempty"`
	Size       *int     `json:"size,omitempty"`
	Angle      *float64 `json:"angle,omitempty"`
	Wavelength *float64 `json:"wavelength,omitempty"`
	Seed       *uint32  `json:"seed,omitempty"`
	Animated   *bool    `json:"animated,omitempty"`
	Intensity  *float64 `json:"intensity,omitempty"`
	Curvature  *float64 `json:"curvature,omitempty"`
	Scale      *float64 `json:"scale,omitempty"`
	Threshold  *float64 `json:"threshold,omitempty"`
	Speed      *float64 `json:"speed,omitempty"`
}

type semanticSource struct {
	AssetID string `json:"asset_id"`
	Path    string `json:"path,omitempty"`
	SHA256  string `json:"sha256"`
}

// semanticSourceFrame is the producer-owned card declaration of a media layer
// (the source clip and, per item, a video overlay). Both surfaces share one
// declaration shape: `source_frame` at plan level and `frame` on the item.
//
// The card may declare one shadow (`shadow`) or a stack of up to three
// (`shadows`). The two are mutually exclusive because the renderer's style
// contract is: a plan that declares both cannot say which stack it means.
type semanticSourceFrame struct {
	Border       *semanticFrameBorder  `json:"border,omitempty"`
	Shadow       *semanticFrameShadow  `json:"shadow,omitempty"`
	Shadows      []semanticFrameShadow `json:"shadows,omitempty"`
	Stroke       *semanticFrameStroke  `json:"stroke,omitempty"`
	ClipRadiusPX float64               `json:"clip_radius_px,omitempty"`
}

// semanticFrameStroke is an outline painted over the clip perimeter.
type semanticFrameStroke struct {
	WidthPX float64 `json:"width_px"`
	Color   string  `json:"color"`
}

// semanticFrameBorder is the visible frame around the media box.
// WidthPX is the visible thickness in output pixels; RadiusPX is the frame's
// outer corner radius (the media's own inner radius is derived from it).
type semanticFrameBorder struct {
	WidthPX  float64 `json:"width_px"`
	Color    string  `json:"color"`
	RadiusPX float64 `json:"radius_px,omitempty"`
}

// semanticFrameShadow is the drop shadow of the card (the frame when a border
// exists, the media layer itself otherwise).
type semanticFrameShadow struct {
	Color    string  `json:"color"`
	Opacity  float64 `json:"opacity,omitempty"`
	BlurPX   float64 `json:"blur_px,omitempty"`
	OffsetXP float64 `json:"offset_x_px,omitempty"`
	OffsetYP float64 `json:"offset_y_px,omitempty"`
}

type semanticSubtitles struct {
	AssetRefs []SemanticAssetRef `json:"asset_refs,omitempty"`
	// FontRef is the content-addressed font the burn pass rasterizes with.
	// Required for burn mode: the worker never picks a face on its own.
	FontRef *SemanticAssetRef `json:"font_ref,omitempty"`
	StyleID string            `json:"style_id,omitempty"`
	Mode    string            `json:"mode,omitempty"`
	// Style is the caller's typed visual override. It is REQUIRED for burn
	// mode: the worker never invents subtitle geometry/color/shadow.
	Style map[string]any `json:"style,omitempty"`
}

type semanticWatermark struct {
	Text      string             `json:"text,omitempty"`
	AssetRefs []SemanticAssetRef `json:"asset_refs,omitempty"`
	FontRef   *SemanticAssetRef  `json:"font_ref,omitempty"`
	Position  string             `json:"position,omitempty"`
	Opacity   *float64           `json:"opacity,omitempty"`
	// MarginPX is the requested distance from the canvas edge. Required for
	// text watermarks: the worker never guesses layout.
	MarginPX *int           `json:"margin_px,omitempty"`
	Style    map[string]any `json:"style,omitempty"`
}

type semanticAudio struct {
	Mode       string `json:"mode,omitempty"`
	Codec      string `json:"codec,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
}

type semanticBackground struct {
	Kind      string             `json:"kind"`
	Color     []float64          `json:"color,omitempty"`
	AssetRefs []SemanticAssetRef `json:"asset_refs,omitempty"`
	Fit       string             `json:"fit,omitempty"`
	Opacity   *float64           `json:"opacity,omitempty"`
	Loop      bool               `json:"loop,omitempty"`
	// BlurPX is the gaussian blur radius applied to the background plate in
	// output pixels. It is how a producer expresses the clip-render
	// `blur_source` mode on the wire: the plate is the source video sampled
	// full-frame, and this effect makes it a soft backdrop instead of a sharp
	// second copy of the foreground.
	BlurPX float64 `json:"blur_px,omitempty"`
}

// semanticEntityRef is the producer's WHO-correlation metadata for an overlay
// item. RenderingGen never derives displayed text from it; it is carried so the
// strict plan decoder accepts the production payload and downstream evidence can
// join the rendered layer to the entity the media resolver selected.
type semanticEntityRef struct {
	EntityID          string `json:"entity_id"`
	Type              string `json:"type"`
	Name              string `json:"name"`
	SurfaceText       string `json:"surface_text,omitempty"`
	CanonicalEntityID string `json:"canonical_entity_id,omitempty"`
}

type semanticItem struct {
	ID string `json:"id"`
	// Producer correlation/group metadata. group_id/subgroup_id select
	// plan-level animation policies; the other keys remain metadata only.
	SceneID    string             `json:"scene_id,omitempty"`
	EntityID   string             `json:"entity_id,omitempty"`
	EntityRef  *semanticEntityRef `json:"entity_ref,omitempty"`
	RenderKey  string             `json:"render_key,omitempty"`
	GroupID    string             `json:"group_id,omitempty"`
	SubgroupID string             `json:"subgroup_id,omitempty"`
	// Kind is the semantic SSOT of the item. It is authoritative when
	// PipelineGen sends it; when it is absent the template registry supplies
	// it. A kind that contradicts the template's kind is rejected fail-closed
	// (see TemplateSpec.resolveKind).
	Kind string `json:"kind"`
	// CompositionID is an optional picker composition selector. When present,
	// the compiler validates image and caption cardinality against that catalog
	// composition; omitted values preserve older plans.
	CompositionID string `json:"composition_id,omitempty"`
	// Template is optional for a simple text primitive. A non-empty value
	// selects a reusable composition/template; the displayed text remains
	// producer-owned and is never stored in the template catalog.
	Template string `json:"template_id,omitempty"`
	// StyleID is an opaque Chronon-owned text-style identity. RenderingGen
	// validates its shape and transports it; it never maps a style to a font,
	// color or layout default.
	StyleID string `json:"style_id,omitempty"`
	// TemplateSlots are producer-owned content values for a reusable template
	// instance. They are intentionally opaque to RenderingGen.
	TemplateSlots map[string]any `json:"template_slots,omitempty"`
	// PresetID is the semantic preset selected by PipelineGen (the plan's
	// preset_id contract slot). It is preferred over the template mapping.
	PresetID      string `json:"preset_id"`
	ImagePresetID string `json:"image_preset_id,omitempty"`
	// MotionID selects an animation independently of PresetID (visual style).
	// MotionParams carries that catalog plugin's open parameter set; the
	// compiler additionally validates shared enter_frames/exit_frames controls.
	MotionID      string         `json:"motion_id"`
	MotionParams  map[string]any `json:"motion_params"`
	Text          string         `json:"text"`
	EntityCaption string         `json:"entity_caption,omitempty"`
	// CaptionMotionID is the optional motion override for the entity caption
	// layer. CaptionMotionParams remain independent from the image motion.
	CaptionMotionID     string         `json:"caption_motion_id,omitempty"`
	CaptionMotionParams map[string]any `json:"caption_motion_params,omitempty"`

	// EntityStyleID selects a RenderingGen-owned entity composition. The
	// premium_random_v1 selector deterministically chooses one of 25 variants.
	EntityStyleID string `json:"entity_style_id,omitempty"`
	// CaptionLayout may be "below", "left", or "right"; side layouts keep
	// the name in the free column beside the portrait.
	CaptionLayout     string `json:"caption_layout,omitempty"`
	CaptionFontFamily string `json:"caption_font_family,omitempty"`
	CaptionColor      string `json:"caption_color,omitempty"`
	StartMS           int64  `json:"start_ms"`
	EndMS             int64  `json:"end_ms"`
	// StartUS/DurationUS are the canonical producer timeline. When both are
	// present they own frame conversion; start_ms/end_ms remain a validated
	// compatibility projection for older readers and duration_ms remains an
	// optional legacy projection.
	StartUS    *int64 `json:"start_us,omitempty"`
	DurationUS *int64 `json:"duration_us,omitempty"`
	DurationMS *int64 `json:"duration_ms,omitempty"`
	// Frame is the optional card treatment of this item's video overlay.
	Frame       *semanticSourceFrame `json:"frame,omitempty"`
	Params      map[string]any       `json:"params"`
	Style       map[string]any       `json:"style"`
	Assets      []SemanticAssetRef   `json:"asset_refs"`
	ImageLayers []SemanticImageLayer `json:"image_layers"`
	// GroupMotionID is the single certified choreography of a composite image
	// group, chosen once by PipelineGen. It is pinned to MotionID before any
	// animation policy runs, so no later step can replace it.
	GroupMotionID string       `json:"group_motion_id,omitempty"`
	Map           *SemanticMap `json:"map"`
	// Metric/Date carry the optional structured payload of the two data
	// compositions. They are typed so strict decoding plus schema parity cannot
	// silently discard a declared value, and they are validated against the
	// item's data target in validateSemanticDataBlocks.
	Metric *SemanticMetricData `json:"metric"`
	Date   *SemanticDateData   `json:"date"`
	// SoundEffect attaches one already-selected audio asset to this image's
	// first visible frame. Selection remains producer-owned; the worker only
	// resolves the content-addressed asset and lowers its timing into Chronon.
	SoundEffect *SemanticOverlaySFX `json:"sound_effect,omitempty"`
	// chainSegments is the decoded producer segment list for a chain item. It
	// is populated exactly once, during resolveSemanticItems, from the merged
	// params.chain_segments wire field; the lowering reads this typed list and
	// never re-decodes the blob. Unexported and json:"-" because the producer
	// contract still publishes the segments inside params, not as an item field.
	chainSegments []chainSegment `json:"-"`
}

type SemanticOverlaySFX struct {
	AssetRef      SemanticAssetRef `json:"asset_ref"`
	StartOffsetMS int64            `json:"start_offset_ms,omitempty"`
	DurationMS    int64            `json:"duration_ms,omitempty"`
	GainDB        *float64         `json:"gain_db,omitempty"`
}

// SemanticMap is the worker mirror of the georeferenced map declaration.
// It is deliberately typed so strict decoding, bounds checks and schema parity
// cannot silently discard map geometry or legal attribution.
type SemanticMap struct {
	Provider      string           `json:"provider"`
	SourceID      string           `json:"source_id"`
	SourceLicense string           `json:"source_license"`
	Center        SemanticMapPoint `json:"center"`
	Zoom          int              `json:"zoom"`
	// AreaGlowRadiusKM is producer metadata for the painted area glow. It is
	// accepted so the strict decoder does not reject a production plan, but it
	// plays no part in the lowered map geometry.
	AreaGlowRadiusKM *float64                 `json:"area_glow_radius_km,omitempty"`
	Width            int                      `json:"width"`
	Height           int                      `json:"height"`
	Attribution      string                   `json:"attribution"`
	MotionID         string                   `json:"motion_id,omitempty"`
	Pins             []SemanticMapPin         `json:"pins"`
	LODs             []SemanticMapLOD         `json:"lods,omitempty"`
	CameraMove       *SemanticMapCameraMove   `json:"camera_move,omitempty"`
	FlyToFeature     *SemanticMapFlyToFeature `json:"fly_to_feature,omitempty"`
	Routes           []SemanticMapRoute       `json:"routes,omitempty"`
}

type SemanticMapFlyToFeature struct {
	Feature string  `json:"feature"`
	Road    string  `json:"road,omitempty"`
	Padding float64 `json:"padding"`
}

type SemanticMapRoute struct {
	ID        string             `json:"id"`
	Stops     []SemanticMapPoint `json:"stops"`
	Color     string             `json:"color"`
	WidthPX   float64            `json:"width_px"`
	TrimStart *float64           `json:"trim_start,omitempty"`
	TrimEnd   *float64           `json:"trim_end,omitempty"`
}

type SemanticMapLOD struct {
	AssetID       string           `json:"asset_id"`
	SourceID      string           `json:"source_id"`
	SourceLicense string           `json:"source_license"`
	Attribution   string           `json:"attribution"`
	Center        SemanticMapPoint `json:"center"`
	Zoom          int              `json:"zoom"`
	Width         int              `json:"width"`
	Height        int              `json:"height"`
}

type SemanticMapCameraMove struct {
	From         SemanticMapPoint `json:"from"`
	To           SemanticMapPoint `json:"to"`
	StartZoom    float64          `json:"start_zoom"`
	EndZoom      float64          `json:"end_zoom"`
	StartTiltDeg float64          `json:"start_tilt_deg"`
	EndTiltDeg   float64          `json:"end_tilt_deg"`
	BearingDeg   float64          `json:"bearing_deg"`
}

type SemanticMapPoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type SemanticMapPin struct {
	ID            string                `json:"id"`
	Label         string                `json:"label"`
	Latitude      float64               `json:"latitude"`
	Longitude     float64               `json:"longitude"`
	Color         string                `json:"color"`
	RadiusPX      float64               `json:"radius_px"`
	LabelStyle    *SemanticMapTextStyle `json:"label_style,omitempty"`
	LabelOffsetPX []float64             `json:"label_offset_px,omitempty"`
	LabelPriority int                   `json:"label_priority,omitempty"`
}

type SemanticMapTextStyle struct {
	Preset     string                 `json:"preset,omitempty"`
	FontFamily string                 `json:"font_family,omitempty"`
	FontSizePX *float64               `json:"font_size_px,omitempty"`
	Fill       string                 `json:"fill,omitempty"`
	Stroke     *semanticMapTextStroke `json:"stroke,omitempty"`
	Shadow     *semanticMapTextShadow `json:"shadow,omitempty"`
	Glow       *semanticMapTextGlow   `json:"glow,omitempty"`
	Background *semanticMapTextPlate  `json:"background,omitempty"`
}

// Map label styling uses the exact wire shapes of the layer style blocks.
// Aliases keep one declaration per block; a separate near-identical struct was
// only another place for the JSON field set to drift.
type semanticMapTextStroke = LayerStroke
type semanticMapTextShadow = LayerShadow
type semanticMapTextGlow = LayerGlow

type semanticMapTextPlate struct {
	Color   string    `json:"color"`
	Opacity float64   `json:"opacity"`
	Radius  float64   `json:"radius"`
	Padding []float64 `json:"padding"`
}

// SemanticAssetRef is one content-addressed asset reference of the semantic
// contract (items[].asset_refs[] and watermark.font_ref). It is exported because
// the producer-side writer (internal/renderbatch) transports the very same
// entry: one declaration, so the writer and the worker's decoder cannot describe
// an asset_refs entry with two field sets that drift apart. Its JSON field set
// is pinned to the published contract by TestContractSchemaMatchesCompilerStructs.
type SemanticAssetRef struct {
	ID        string `json:"asset_id"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
	MediaType string `json:"media_type"`
}

// SemanticImageLayer is one independently timed and animated source image in a
// composite image item. Use it only for two or more images; a single image uses
// the parent item's asset_refs, motion_id and motion_params. Assets are declared
// once on the parent item; each child names its asset_id from that declared set
// and may override preset_id, motion_id and motion_params independently. A child
// caption lowers to a separate text layer linked internally to this image ID;
// it shares the child's time window and may select its own motion.
type SemanticImageLayer struct {
	ID             string `json:"id"`
	GroupID        string `json:"group_id,omitempty"`
	SubgroupID     string `json:"subgroup_id,omitempty"`
	AssetID        string `json:"asset_id"`
	StartMS        int64  `json:"start_ms"`
	EndMS          int64  `json:"end_ms"`
	CaptionStartMS int64  `json:"caption_start_ms,omitempty"`
	CaptionEndMS   int64  `json:"caption_end_ms,omitempty"`
	PresetID       string `json:"preset_id"`
	// MotionID/MotionParams are legacy-only for composites. New plans leave
	// them empty because the parent GroupMotionID owns the choreography.
	MotionID            string               `json:"motion_id,omitempty"`
	MotionParams        map[string]any       `json:"motion_params,omitempty"`
	Caption             string               `json:"caption,omitempty"`
	CaptionMotionID     string               `json:"caption_motion_id,omitempty"`
	CaptionMotionParams map[string]any       `json:"caption_motion_params,omitempty"`
	CaptionFontFamily   string               `json:"caption_font_family,omitempty"`
	CaptionColor        string               `json:"caption_color,omitempty"`
	CaptionLayout       string               `json:"caption_layout,omitempty"`
	Params              map[string]any       `json:"params"`
	Frame               *semanticSourceFrame `json:"frame,omitempty"`
}
