package processor

import (
	"reflect"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
)

// TestOutputFactsFromProbeCertifiesEveryDimension pins the projection: every
// structural dimension the downstream output contract validates must survive
// onto the artifact. Dropping one here re-opens the silent-skip hole the
// certification exists to close.
func TestOutputFactsFromProbeCertifiesEveryDimension(t *testing.T) {
	probe := &media.ProbeResult{
		Container:        "mov,mp4,m4a,3gp,3g2,mj2",
		HasVideo:         true,
		VideoStreams:     1,
		VideoCodec:       "h264",
		CodecProfile:     "High",
		VideoLevel:       "4.1",
		PixelFormat:      "yuv420p",
		Width:            1920,
		Height:           1080,
		FPSNum:           24,
		FPSDen:           1,
		VideoTimeBaseNum: 1,
		VideoTimeBaseDen: 12288,
		AudioTimeBaseNum: 1,
		AudioTimeBaseDen: 48000,
		SARNum:           1,
		SARDen:           1,
		ColorRange:       "tv",
		ColorSpace:       "bt709",
		ColorTransfer:    "bt709",
		ColorPrimaries:   "bt709",
		FieldOrder:       "progressive",
		KeyframeInterval: 48,
		HasAudio:         true,
		AudioStreams:     1,
		AudioCodec:       "aac",
		AudioProfile:     "LC",
		SampleRate:       48000,
		Channels:         2,
		ChannelLayout:    "stereo",
		AudioBitrate:     "128000",
	}
	got := outputFactsFromProbe(probe)
	if got == nil {
		t.Fatal("nil fact set for a fully-populated probe")
	}
	want := map[string]any{
		"VideoProfile":     "High",
		"VideoLevel":       "4.1",
		"VideoTimeBaseNum": 1,
		"VideoTimeBaseDen": 12288,
		"AudioTimeBaseNum": 1,
		"AudioTimeBaseDen": 48000,
		"SARNum":           1,
		"SARDen":           1,
		"ColorRange":       "tv",
		"ColorSpace":       "bt709",
		"ColorTransfer":    "bt709",
		"ColorPrimaries":   "bt709",
		"FieldOrder":       "progressive",
		"KeyframeInterval": 48,
		"HasAudio":         true,
		"AudioProfile":     "LC",
		"SampleRate":       48000,
		"Channels":         2,
		"ChannelLayout":    "stereo",
		"AudioBitrate":     "128000",
		"VideoStreams":     1,
	}
	value := reflect.ValueOf(*got)
	for field, wantValue := range want {
		actual := value.FieldByName(field)
		if !actual.IsValid() {
			t.Fatalf("OutputFacts lost field %s", field)
		}
		if actual.Interface() != wantValue {
			t.Errorf("OutputFacts.%s = %v, want %v", field, actual.Interface(), wantValue)
		}
	}
}

// TestOutputFactsFromProbeFillsEveryDeclaredField is the omission detector the
// dimension table above cannot be. That test asserts the fields it NAMES
// survive; this one asserts every field `queue.OutputFacts` DECLARES is filled
// from the probe, so a certified fact added to the wire contract without a
// matching projection fails here instead of shipping as a silently uncertified
// dimension. The two are complementary: the table catches a WRONG value, this
// catches a MISSING one.
//
// The probe is populated by reflection rather than by hand so a field added to
// media.ProbeResult is exercised automatically; a field kind this fixture does
// not know how to fill fails loudly instead of being silently skipped.
func TestOutputFactsFromProbeFillsEveryDeclaredField(t *testing.T) {
	var probe media.ProbeResult
	pv := reflect.ValueOf(&probe).Elem()
	pt := pv.Type()
	for i := 0; i < pt.NumField(); i++ {
		field := pv.Field(i)
		switch field.Kind() {
		case reflect.String:
			field.SetString("set")
		case reflect.Int, reflect.Int64:
			field.SetInt(7)
		case reflect.Bool:
			field.SetBool(true)
		default:
			t.Fatalf("ProbeResult.%s has kind %s; extend this fixture so the field is exercised", pt.Field(i).Name, field.Kind())
		}
	}

	facts := outputFactsFromProbe(&probe)
	if facts == nil {
		t.Fatal("a fully populated probe must certify a fact set")
	}
	fv := reflect.ValueOf(*facts)
	ft := fv.Type()
	for i := 0; i < ft.NumField(); i++ {
		if fv.Field(i).IsZero() {
			t.Errorf("queue.OutputFacts.%s is never filled by outputFactsFromProbe: the probe certifies it and the projection drops it", ft.Field(i).Name)
		}
	}
}

// TestOutputFactsFromProbeNilCertifiesNothing: a nil probe must produce a nil
// fact set, never an empty object that a consumer could mistake for a
// certification.
func TestOutputFactsFromProbeNilCertifiesNothing(t *testing.T) {
	if got := outputFactsFromProbe(nil); got != nil {
		t.Fatalf("outputFactsFromProbe(nil) = %+v, want nil", got)
	}
}

func TestProbeResultFromReceipt(t *testing.T) {
	var r chronon.MediaReceipt
	r.Media.Container = "mov,mp4,m4a,3gp,3g2,mj2"
	r.Media.Width = 1920
	r.Media.Height = 1080
	r.Media.FPSNum = 9792
	r.Media.FPSDen = 407
	r.Media.RFPSNum = 24
	r.Media.RFPSDen = 1
	r.Media.DurationMS = 2000.0
	r.Media.DurationUS = 2000000
	r.Media.VideoCodec = "h264"
	r.Media.CodecProfile = "High"
	r.Media.VideoLevel = "4.1"
	r.Media.PixelFormat = "yuv420p"
	r.Media.VideoStreams = 1
	r.Media.FrameCount = 48
	r.Media.FirstFrameKeyframe = true
	r.Media.ClosedGOP = true
	r.Media.KeyframeInterval = 48

	if !r.HasCanonicalMedia() {
		t.Fatal("expected HasCanonicalMedia to be true")
	}

	probed := probeResultFromReceipt(r)
	if probed.Width != 1920 || probed.Height != 1080 {
		t.Errorf("got %dx%d, want 1920x1080", probed.Width, probed.Height)
	}
	if probed.VideoCodec != "h264" || probed.CodecProfile != "High" {
		t.Errorf("got %s %s, want h264 High", probed.VideoCodec, probed.CodecProfile)
	}
	if probed.FPSNum != 24 || probed.FPSDen != 1 {
		t.Errorf("got %d/%d fps, want 24/1", probed.FPSNum, probed.FPSDen)
	}
	if !probed.FirstFrameKeyframe || !probed.ClosedGOP || probed.KeyframeInterval != 48 {
		t.Errorf("keyframe facts mismatch: first=%v closed=%v interval=%d",
			probed.FirstFrameKeyframe, probed.ClosedGOP, probed.KeyframeInterval)
	}
	facts := outputFactsFromProbe(&probed)
	if facts == nil || facts.Width != 1920 || facts.KeyframeInterval != 48 {
		t.Fatalf("outputFactsFromProbe failed from canonical receipt probe: %+v", facts)
	}
}
