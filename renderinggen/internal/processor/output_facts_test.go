package processor

import (
	"reflect"
	"testing"

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

// TestOutputFactsFromProbeNilCertifiesNothing: a nil probe must produce a nil
// fact set, never an empty object that a consumer could mistake for a
// certification.
func TestOutputFactsFromProbeNilCertifiesNothing(t *testing.T) {
	if got := outputFactsFromProbe(nil); got != nil {
		t.Fatalf("outputFactsFromProbe(nil) = %+v, want nil", got)
	}
}
