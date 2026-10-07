package overlay

import "testing"

// TestTextRoleEntityCaptionOwnsTheNameplateTreatment pins the V1 seam: the
// entity-caption base treatment lives in the role resolver, not in the
// image compiler. Default and explicit fills must match the values the
// compiler used to patch field by field, so the migration is byte-identical.
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
	if style.Glow == nil || style.Glow.Color != "#F8F5EA" || style.Glow.Radius != 14 || style.Glow.Intensity != 0.24 {
		t.Errorf("glow = %+v, want the warm halo", style.Glow)
	}
	if style.Background != nil {
		t.Errorf("background = %+v, want nil (strict GPU cannot execute text cards)", style.Background)
	}

	custom := &LayerStyle{}
	applyTextRoleBaseStyle(textRoleEntityCaption, custom, 30, "#AABBCC")
	if custom.Fill != "#AABBCC" {
		t.Errorf("explicit fill = %q, want the caller override to win", custom.Fill)
	}
	if custom.Stroke == nil || custom.Shadow == nil || custom.Glow == nil {
		t.Error("explicit fill must not drop the keyline/shadow/glow treatment")
	}
}
