package overlay

import "testing"

func TestRemainingTextRolesResolveIntoSharedSpec(t *testing.T) {
	cases := []struct {
		name       string
		kind       ItemKind
		template   string
		wantRole   textRole
		wantTarget string
		wantEntry  bool
		wantFit    string
	}{
		{name: "phrase", kind: KindPrimitive, wantRole: textRolePhrase, wantTarget: "text"},
		{name: "important phrase", kind: KindImportantPhrase, template: "IMPORTANT_PHRASE", wantRole: textRoleImportantPhrase, wantTarget: "important_phrase", wantEntry: true},
		{name: "metric", kind: KindNumber, template: "METRIC_STAT_CARD", wantRole: textRoleMetric, wantTarget: "metric", wantFit: "none"},
		{name: "date", kind: KindNumber, template: "TIMELINE_DATE_CARD", wantRole: textRoleDate, wantTarget: "date", wantFit: "none"},
		{name: "lower third", kind: KindLowerThird, template: "LOWER_THIRD", wantRole: textRoleLowerThird, wantTarget: "text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := semanticItem{ID: "role-test", Kind: string(tc.kind), Template: tc.template, Text: "A phrase long enough to wrap across multiple lines", StartMS: 0, EndMS: 2000}
			preset, err := resolveOfficialPreset(PhraseDefaultPresetID, string(PresetText))
			if err != nil {
				t.Fatalf("resolve text preset: %v", err)
			}
			ri := resolvedItem{Item: item, Kind: tc.kind, Params: map[string]any{"font_size_px": float64(48)}, Preset: preset, PresetID: preset.ID, Start: 0, End: 48}
			src := &semanticPlan{Width: 1920, Height: 1080}
			if got := resolveTextRole(ri); got != tc.wantRole {
				t.Fatalf("resolved role = %q, want %q", got, tc.wantRole)
			}
			spec, err := resolveTextSpec(ri, src)
			if err != nil {
				t.Fatalf("resolve text spec: %v", err)
			}
			if spec.StylePolicy != tc.wantRole || spec.MotionTarget != tc.wantTarget || spec.EntryExit != tc.wantEntry || spec.FitPolicy != tc.wantFit {
				t.Fatalf("resolved role spec policy=%q target=%q entry_exit=%v fit=%q", spec.StylePolicy, spec.MotionTarget, spec.EntryExit, spec.FitPolicy)
			}
			if spec.Text == "" || spec.BoxWidth != 1920 || spec.BoxHeight <= 0 || len(spec.Position) != 2 {
				t.Fatalf("incomplete resolved spec: %+v", spec)
			}
			layer, err := compileTextLayer(ri, src, "role-test")
			if err != nil {
				t.Fatalf("compile role through shared text lowering: %v", err)
			}
			if layer.Type != "text" || layer.Text != spec.Text || layer.BoxWidth != spec.BoxWidth || layer.BoxHeight != spec.BoxHeight {
				t.Fatalf("compiled layer does not preserve its resolved spec: %+v", layer)
			}
		})
	}
}

// TestTextRoleEntityCaptionOwnsTheNameplateTreatment pins the V1 seam: the
// entity-caption base treatment lives in the role resolver, not in the image
// compiler. Default and explicit fills must match the values the compiler used
// to patch field by field, so the migration is byte-identical.
func TestTextRoleEntityCaptionOwnsTheNameplateTreatment(t *testing.T) {
	style := &LayerStyle{}
	applyTextRoleBaseStyle(textRoleEntityCaption, style, 42, "")
	if style.Fill != "#F8F5EA" {
		t.Errorf("default fill = %q, want the warm-white nameplate", style.Fill)
	}
	if style.FontSize != 42 || style.MinFontSize != 0 || style.MaxFontSize != 0 {
		t.Errorf("font sizes = %v/%v/%v, want fixed 42 with no fit interval", style.FontSize, style.MinFontSize, style.MaxFontSize)
	}
	if style.Stroke == nil || style.Stroke.Color != "#111827" || style.Stroke.Width != 2.0 {
		t.Errorf("stroke = %+v, want the dark keyline", style.Stroke)
	}
	if style.Shadow == nil || style.Shadow.Opacity != 0.78 || style.Shadow.Blur != 9 {
		t.Errorf("shadow = %+v, want the soft drop shadow", style.Shadow)
	}
	if style.Glow != nil {
		t.Errorf("glow = %+v, want no full-frame GPU halo", style.Glow)
	}
	if style.Background != nil {
		t.Errorf("background = %+v, want nil (strict GPU cannot execute text cards)", style.Background)
	}

	custom := &LayerStyle{}
	applyTextRoleBaseStyle(textRoleEntityCaption, custom, 30, "#AABBCC")
	if custom.Fill != "#AABBCC" {
		t.Errorf("explicit fill = %q, want the caller override to win", custom.Fill)
	}
	if custom.Stroke == nil || custom.Shadow == nil {
		t.Error("explicit fill must not drop the keyline/shadow treatment")
	}
}
