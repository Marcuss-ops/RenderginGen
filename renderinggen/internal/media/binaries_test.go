package media

import "testing"

// TestBinariesResolution pins the single resolution point the exec call sites
// read: an unconfigured package keeps the historical PATH lookup, a configured
// value is returned verbatim, and an empty field falls back per tool instead of
// blanking both.
func TestBinariesResolution(t *testing.T) {
	// The package-wide value is sticky, so restore the default for the other
	// tests in this package.
	t.Cleanup(func() { Configure(Binaries{}) })

	Configure(Binaries{})
	if got := ffprobeBinary(); got != DefaultFFprobeBinary {
		t.Errorf("unconfigured ffprobe = %q, want the default %q", got, DefaultFFprobeBinary)
	}
	if got := ffmpegBinary(); got != DefaultFFmpegBinary {
		t.Errorf("unconfigured ffmpeg = %q, want the default %q", got, DefaultFFmpegBinary)
	}

	Configure(Binaries{FFprobe: "/opt/tools/ffprobe"})
	if got := ffprobeBinary(); got != "/opt/tools/ffprobe" {
		t.Errorf("configured ffprobe = %q", got)
	}
	if got := ffmpegBinary(); got != DefaultFFmpegBinary {
		t.Errorf("ffmpeg = %q, want the untouched default %q", got, DefaultFFmpegBinary)
	}

	Configure(Binaries{FFmpeg: "/opt/tools/ffmpeg", FFprobe: "   "})
	if got := ffprobeBinary(); got != DefaultFFprobeBinary {
		t.Errorf("whitespace-only ffprobe = %q, want the default %q", got, DefaultFFprobeBinary)
	}
	if got := ffmpegBinary(); got != "/opt/tools/ffmpeg" {
		t.Errorf("configured ffmpeg = %q", got)
	}
}
