// binaries.go owns the single resolution point for the external tools the media
// certification invokes.
//
// Why it exists. The probe, the closed-GOP scan and the visibility check each
// hardcoded the bare command names "ffprobe" and "ffmpeg" at their exec call
// sites. That made the tool choice invisible: a host or image that ships a
// different build (a static ffmpeg, a wrapper script, a pinned version under
// /opt) could not be pointed at without editing three files, and a test could
// not substitute a fake binary without a PATH trick. The worker configuration
// now carries media.ffprobe_binary / media.ffmpeg_binary, and it installs them
// here once at startup.
//
// The default is the historical bare name, so an unconfigured process (every
// existing test, and any caller that never calls Configure) keeps the exact
// PATH-lookup behaviour it had before.
package media

import (
	"strings"
	"sync/atomic"
)

// Default tool command names, used when nothing is configured.
const (
	DefaultFFprobeBinary = "ffprobe"
	DefaultFFmpegBinary  = "ffmpeg"
)

// Binaries names the external certification tools.
type Binaries struct {
	FFprobe string
	FFmpeg  string
}

// configuredBinaries holds the package-wide tool paths. The certification path
// runs on the post-render pool, i.e. concurrently with other finalizers, so the
// value is read atomically and only ever replaced wholesale.
var configuredBinaries atomic.Value

// Configure installs the process-wide tool paths. An empty field keeps the
// default for that tool, so a caller only has to name what it overrides.
func Configure(b Binaries) {
	if strings.TrimSpace(b.FFprobe) == "" {
		b.FFprobe = DefaultFFprobeBinary
	}
	if strings.TrimSpace(b.FFmpeg) == "" {
		b.FFmpeg = DefaultFFmpegBinary
	}
	configuredBinaries.Store(b)
}

// ffprobeBinary returns the command the probe/scan paths must exec.
func ffprobeBinary() string {
	if b, ok := configuredBinaries.Load().(Binaries); ok && b.FFprobe != "" {
		return b.FFprobe
	}
	return DefaultFFprobeBinary
}

// ffmpegBinary returns the command the decode/visibility paths must exec.
func ffmpegBinary() string {
	if b, ok := configuredBinaries.Load().(Binaries); ok && b.FFmpeg != "" {
		return b.FFmpeg
	}
	return DefaultFFmpegBinary
}
