// gop_probe.go owns the closed-GOP certification probe: it reads the video
// stream's sync-sample (keyframe) packet table with constant memory and
// decides whether every GOP boundary occurs at a strictly regular interval.
package media

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
)

// probeClosedGOP inspects the video stream's sync-sample (keyframe) table and
// certifies a uniform closed-GOP structure. It fails closed: any probe or
// container error yields false, never a guess.
//
// It returns the verdict plus an "uncertifiable" flag that separates the two
// reasons a verdict can be false: the cadence was actually read and is not
// uniform ("invalid"), or the probe never produced a packet table at all
// (ffprobe missing, unreadable file, unsupported flags). Without that flag a
// deployment without ffprobe reports closed_gop=false forever and is
// indistinguishable from a renderer emitting open GOPs — silently disabling
// the copy-only fast path.
//
// The ffprobe packet table is stream-decoded with json.Decoder instead of
// cmd.Output(): -show_packets emits one JSON record per packet for the WHOLE
// clip, and buffering that document in RAM scaled with clip length (MBs of
// JSON for long-form content on every finalize). The decoder keeps memory
// constant while ffprobe streams, and a broken cadence aborts the probe (and
// the ffprobe process) as soon as it is provable.
func probeClosedGOP(ctx context.Context, path string) (closedGOP bool, uncertifiable bool) {
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_packets", "-show_entries", "packet=flags", "-of", "json", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("media: ffprobe %s: closed-GOP probe pipe: %v (closed_gop=false, uncertifiable)", path, err)
		return false, true
	}
	if err := cmd.Start(); err != nil {
		log.Printf("media: ffprobe %s: closed-GOP probe start: %v (closed_gop=false, uncertifiable)", path, err)
		return false, true
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	positions := make([]int, 0, 16)
	step := 0 // keyframe spacing once two keyframes are known (0 until proven)
	packetIndex := 0

	// Stream-decode the packet table with json.Decoder tokens (never
	// cmd.Output, which buffers the whole clip's packet JSON in RAM). The
	// invocation pins each element's shape to {"flags":"..."}, so the token
	// walk is deterministic: object open, key, value, object close.
	dec := json.NewDecoder(stdout)
	// Expected top level: { "packets": [ ...
	if err := expectJSONDelim(dec, '{'); err != nil {
		return failProbe(cancel, waitDone, path, err)
	}
	key, err := dec.Token()
	if err != nil {
		return failProbe(cancel, waitDone, path, err)
	}
	if k, ok := key.(string); !ok || k != "packets" {
		return failProbe(cancel, waitDone, path, fmt.Errorf("unexpected top-level key %v", key))
	}
	if err := expectJSONDelim(dec, '['); err != nil {
		return failProbe(cancel, waitDone, path, err)
	}

	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				err = fmt.Errorf("unterminated packets array")
			}
			return failProbe(cancel, waitDone, path, err)
		}
		d, isDelim := tok.(json.Delim)
		if isDelim && d == ']' {
			break // end of the packet table
		}
		if !isDelim || d != '{' {
			return failProbe(cancel, waitDone, path, fmt.Errorf("unexpected packet-table token %v", tok))
		}
		// One packet object: { "flags" : "..." }
		keyTok, err := dec.Token()
		if err != nil {
			return failProbe(cancel, waitDone, path, err)
		}
		valueTok, err := dec.Token()
		if err != nil {
			return failProbe(cancel, waitDone, path, err)
		}
		closeTok, err := dec.Token()
		if err != nil {
			return failProbe(cancel, waitDone, path, err)
		}
		if k, ok := keyTok.(string); !ok || k != "flags" {
			return failProbe(cancel, waitDone, path, fmt.Errorf("unexpected packet key %v", keyTok))
		}
		flags, ok := valueTok.(string)
		if !ok {
			return failProbe(cancel, waitDone, path, fmt.Errorf("packet flags value is not a string: %v", valueTok))
		}
		if cd, ok := closeTok.(json.Delim); !ok || cd != '}' {
			return failProbe(cancel, waitDone, path, fmt.Errorf("unexpected packet close %v", closeTok))
		}
		if strings.Contains(flags, "K") {
			positions = append(positions, packetIndex)
			// Early refusal: three keyframes already prove the cadence broken,
			// so the probe (and the ffprobe process) can stop early.
			if len(positions) >= 3 {
				if positions[len(positions)-1]-positions[len(positions)-2] != step {
					cancel() // reap the child; we already have the verdict
					<-waitDone
					return false, false // read successfully: the cadence is invalid
				}
			} else if len(positions) == 2 {
				step = positions[1] - positions[0]
				if step <= 0 {
					cancel()
					<-waitDone
					return false, false
				}
			}
		}
		packetIndex++
	}
	if err := <-waitDone; err != nil {
		// A non-cancel wait error means ffprobe itself failed (bad file,
		// unsupported flag): fail closed and say so.
		log.Printf("media: ffprobe %s: closed-GOP probe failed: %v (closed_gop=false, uncertifiable)", path, err)
		return false, true
	}
	return closedGOPPositionsCadence(positions), false
}

// expectJSONDelim asserts the next decoder token is the given delimiter.
func expectJSONDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); ok && d == want {
		return nil
	}
	return fmt.Errorf("expected %q, got %v", want, tok)
}

// failProbe cancels the ffprobe child, drains its exit status and logs the
// failed closed-GOP probe so the degradation is never silent. It always
// reports false + uncertifiable: no packet table was decoded, so the cadence
// was never observed (distinct from a decoded but non-uniform cadence).
func failProbe(cancel context.CancelFunc, waitDone <-chan error, path string, cause error) (bool, bool) {
	cancel()
	<-waitDone
	log.Printf("media: ffprobe %s: closed-GOP probe decode failed: %v (closed_gop=false, uncertifiable)", path, cause)
	return false, true
}

// closedGOPCadence reports whether keyframes occur at strictly uniform packet
// intervals starting at the first packet: positions 0, L, 2L, ... Uniform
// IDR boundaries are the observable signature of closed-GOP encoding (each
// GOP starts an independent IDR at a fixed cadence), while scene-cut or open
// GOP structures break the cadence. Fewer than two keyframes cannot prove a
// cadence, so the function returns false.
func closedGOPCadence(keyframes []bool) bool {
	positions := make([]int, 0, 8)
	for i, kf := range keyframes {
		if kf {
			positions = append(positions, i)
		}
	}
	return closedGOPPositionsCadence(positions)
}

// closedGOPPositionsCadence is the shared cadence decision over keyframe
// packet indices (0-based). Streaming probes collect positions directly and
// never materialize a per-packet flag array.
func closedGOPPositionsCadence(positions []int) bool {
	if len(positions) == 0 || positions[0] != 0 {
		return false
	}
	// Two keyframes (one interval) cannot prove a cadence — any single
	// interval is trivially "uniform". Require at least two full intervals.
	if len(positions) < 3 {
		return false
	}
	expected := positions[1] - positions[0]
	if expected <= 0 {
		return false
	}
	for i := 2; i < len(positions); i++ {
		if positions[i]-positions[i-1] != expected {
			return false
		}
	}
	return true
}
