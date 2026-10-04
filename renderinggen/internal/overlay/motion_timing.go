package overlay

// entranceFrames is how many frames of a layer the entrance may occupy once the
// exit window is reserved. The authored window is the preferred ceiling, but a
// phrase entrance is stretched to at least one third of its duration whenever
// the non-exit window permits it. A short overlay compresses its entrance to
// layer-minus-exit instead of letting keyframes run past the layer boundary.
func entranceFrames(authored, exitFrames int, duration int64, phraseEntranceFloor bool) int64 {
	if duration <= 0 {
		if authored > 0 {
			return int64(authored)
		}
		return 0
	}
	available := duration
	if exitFrames > 0 && int64(exitFrames) < duration {
		available = duration - int64(exitFrames)
	}
	if phraseEntranceFloor && duration >= 72 && duration <= 144 {
		return available
	}
	target := available
	if authored > 0 && int64(authored) < target {
		target = int64(authored)
	}
	if phraseEntranceFloor {
		minimum := (duration + 2) / 3
		if minimum > available {
			minimum = available
		}
		if target < minimum {
			target = minimum
		}
	}
	if target < 1 {
		target = 1
	}
	return target
}

// appendExitTracks mirrors authored entrance tracks into the reserved exit
// window and guarantees an explicit opacity fade to zero.
func appendExitTracks(tracks []AnimationTrack, exitFrames int, duration int64) []AnimationTrack {
	if exitFrames <= 0 || duration <= 1 || int64(exitFrames) >= duration {
		return tracks
	}
	start, last := duration-int64(exitFrames), lastValidFrame(duration)
	out := make([]AnimationTrack, 0, len(tracks)+1)
	hasOpacity := false
	for _, track := range tracks {
		if len(track.Keyframes) == 0 {
			continue
		}
		if track.Property == "opacity" {
			hasOpacity = true
			track.Keyframes = append(track.Keyframes,
				AnimationKeyframe{Frame: start, Value: 1.0},
				AnimationKeyframe{Frame: last, Value: 0.0})
			out = append(out, track)
			continue
		}
		track.Keyframes = append(track.Keyframes,
			AnimationKeyframe{Frame: start, Value: track.Keyframes[len(track.Keyframes)-1].Value},
			AnimationKeyframe{Frame: last, Value: track.Keyframes[0].Value})
		out = append(out, track)
	}
	if !hasOpacity {
		out = append(out, AnimationTrack{Property: "opacity", Easing: "in_out_sine", Keyframes: []AnimationKeyframe{
			{Frame: 0, Value: 1.0}, {Frame: start, Value: 1.0}, {Frame: last, Value: 0.0},
		}})
	}
	return out
}
