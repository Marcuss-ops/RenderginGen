// output_facts.go projects the ffprobe probe onto the queue artifact's
// COMPLETE structural certification (queue.OutputFacts).
//
// This is the certification of record for every clip.render output-contract
// dimension the downstream consumer cannot observe itself: PipelineGen's Rust
// probe reports no video timebase, SAR, colour range/space/transfer/primaries,
// field order, GOP interval, channel layout, audio bitrate or codec profile, so
// those checks used to be silently skipped. The certified probe owns them.
package processor

import (
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// outputFactsFromProbe builds the certified fact set. A nil probe certifies
// nothing (nil, not an empty object): a consumer must then fall back to what it
// can observe rather than trusting an all-zero "certification".
func outputFactsFromProbe(probe *media.ProbeResult) *queue.OutputFacts {
	if probe == nil {
		return nil
	}
	return &queue.OutputFacts{
		Container:        probe.Container,
		HasVideo:         probe.HasVideo,
		VideoStreams:     probe.VideoStreams,
		VideoCodec:       probe.VideoCodec,
		VideoProfile:     probe.CodecProfile,
		VideoLevel:       probe.VideoLevel,
		PixelFormat:      probe.PixelFormat,
		Width:            probe.Width,
		Height:           probe.Height,
		FPSNum:           probe.FPSNum,
		FPSDen:           probe.FPSDen,
		VideoTimeBaseNum: probe.VideoTimeBaseNum,
		VideoTimeBaseDen: probe.VideoTimeBaseDen,
		AudioTimeBaseNum: probe.AudioTimeBaseNum,
		AudioTimeBaseDen: probe.AudioTimeBaseDen,
		SARNum:           probe.SARNum,
		SARDen:           probe.SARDen,
		ColorRange:       probe.ColorRange,
		ColorSpace:       probe.ColorSpace,
		ColorTransfer:    probe.ColorTransfer,
		ColorPrimaries:   probe.ColorPrimaries,
		FieldOrder:       probe.FieldOrder,
		KeyframeInterval: probe.KeyframeInterval,
		StartPTS:         probe.StartPTS,
		HasAudio:         probe.HasAudio,
		AudioStreams:     probe.AudioStreams,
		AudioCodec:       probe.AudioCodec,
		AudioProfile:     probe.AudioProfile,
		SampleRate:       probe.SampleRate,
		Channels:         probe.Channels,
		ChannelLayout:    probe.ChannelLayout,
		AudioBitrate:     probe.AudioBitrate,
	}
}
