package media

// profile_assembly_contract_test.go owns the ANTI-DRIFT gate between this
// package's assembly-ready profiles and the VeloxEditing assembly contract.
//
// The two surfaces are mirrors: VeloxEditing's
// refactored/internal/kernel/media/assembly_contract.go is the SSOT of the
// stream identity a clip must carry to be assembled by concat -c copy, and the
// VELOX_ASSEMBLY_READY_* profiles here are what the worker certifies at
// finalize. They live in different Go modules, so no compiler can link them;
// this test is the link. It reads the contract source from the workspace
// siblings and fails on any disagreement, so the pair cannot drift apart
// silently (the historical state: RenderingGen said "Main", the contract said
// "high", and nothing noticed).
//
// Like the other cross-repo boundary contracts in this module, the test SKIPS
// when the workspace siblings are absent (a standalone RenderingGen checkout):
// a skip is visible, a false pass is not.

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// findWorkspaceRoot walks up from dir looking for go.work (the workspace root
// that carries the sibling repositories).
func findWorkspaceRoot(dir string) (string, bool) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func workspaceRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate test source; skipping cross-repo contract mirror")
	}
	root, found := findWorkspaceRoot(filepath.Dir(source))
	if !found {
		t.Skip("go.work / sibling repositories not checked out; skipping cross-repo contract mirror (standalone RenderingGen checkout)")
	}
	return root
}

// contractLiteral extracts the `Key: value,` literal fields of a Go function
// body. It is intentionally a shallow parser over the one file this gate
// mirrors; a structural parse would be a heavier dependency than the fact
// being checked.
func contractLiteral(t *testing.T, source, funcName string) map[string]string {
	t.Helper()
	marker := "func " + funcName + "("
	start := strings.Index(source, marker)
	if start < 0 {
		t.Fatalf("contract source does not declare %s", funcName)
	}
	rest := source[start:]
	end := strings.Index(rest, "\n}")
	if end < 0 {
		t.Fatalf("contract source declares %s without a closing body", funcName)
	}
	values := map[string]string{}
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		// Literal fields (the V1 contract's `Key: value,` return literal).
		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			if key != "" && !strings.ContainsAny(key, " \t(") {
				values[key] = strings.TrimSpace(strings.TrimSuffix(line[idx+1:], ","))
				continue
			}
		}
		// Assignment overrides (`c.Key = value`, how V2 derives from V1).
		if idx := strings.Index(line, "= "); idx > 0 {
			key := strings.TrimPrefix(strings.TrimSpace(line[:idx]), "c.")
			if key != "" && !strings.ContainsAny(key, " \t.") {
				values[key] = strings.TrimSpace(line[idx+2:])
			}
		}
	}
	return values
}

var quotedValueRE = regexp.MustCompile(`"([^"]*)"`)

func contractString(t *testing.T, values map[string]string, field string) string {
	t.Helper()
	raw, ok := values[field]
	if !ok {
		t.Fatalf("contract does not declare %s", field)
	}
	m := quotedValueRE.FindStringSubmatch(raw)
	if m == nil {
		t.Fatalf("contract field %s carries no string literal: %q", field, raw)
	}
	return m[1]
}

func contractInt(t *testing.T, values map[string]string, field string) int {
	t.Helper()
	raw, ok := values[field]
	if !ok {
		t.Fatalf("contract does not declare %s", field)
	}
	return literalInt(t, raw, field)
}

func contractRationalInt(t *testing.T, values map[string]string, field, part string) int {
	t.Helper()
	raw, ok := values[field]
	if !ok {
		t.Fatalf("contract does not declare %s", field)
	}
	re := regexp.MustCompile(part + `:\s*(\d+)`)
	m := re.FindStringSubmatch(raw)
	if m == nil {
		t.Fatalf("contract field %s carries no %s: %q", field, part, raw)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("contract field %s.%s is not an integer: %q", field, part, raw)
	}
	return n
}

func literalInt(t *testing.T, raw, field string) int {
	t.Helper()
	re := regexp.MustCompile(`(-?\d+)`)
	m := re.FindStringSubmatch(raw)
	if m == nil {
		t.Fatalf("contract field %s carries no integer: %q", field, raw)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("contract field %s is not an integer: %q", field, raw)
	}
	return n
}

func contractBool(t *testing.T, values map[string]string, field string) bool {
	t.Helper()
	raw, ok := values[field]
	if !ok {
		t.Fatalf("contract does not declare %s", field)
	}
	switch strings.TrimSpace(raw) {
	case "true":
		return true
	case "false":
		return false
	}
	t.Fatalf("contract field %s is not a bool: %q", field, raw)
	return false
}

func assemblyContractValues(t *testing.T, root string, profileID string) map[string]string {
	t.Helper()
	path := filepath.Join(root, "refactored", "internal", "kernel", "media", "assembly_contract.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("VeloxEditing assembly contract not readable at %s: %v", path, err)
	}
	v1 := contractLiteral(t, string(source), "DefaultAssemblyMediaContract")
	if profileID == ProfileVeloxAssemblyReadyV1 {
		return v1
	}
	// V2 inherits V1 and overrides only the declared fields (ID, Version,
	// VideoLevel, AudioBitrate in the contract today).
	merged := map[string]string{}
	for k, v := range v1 {
		merged[k] = v
	}
	for k, v := range contractLiteral(t, string(source), "DefaultAssemblyMediaContractV2") {
		merged[k] = v
	}
	return merged
}

// TestAssemblyReadyProfilesMirrorVeloxContract is the anti-drift gate: every
// dimension this package pins must equal the contract's declared value.
func TestAssemblyReadyProfilesMirrorVeloxContract(t *testing.T) {
	root := workspaceRoot(t)
	for _, id := range []string{ProfileVeloxAssemblyReadyV1, ProfileVeloxAssemblyReadyV2} {
		t.Run(id, func(t *testing.T) {
			profile, err := ResolveProfile(id)
			if err != nil {
				t.Fatalf("profile %s: %v", id, err)
			}
			contract := assemblyContractValues(t, root, id)

			if got, want := profile.Container, contractString(t, contract, "Container"); got != want {
				t.Errorf("Container = %q, contract declares %q", got, want)
			}
			if got, want := profile.Codec, contractString(t, contract, "VideoCodec"); got != want {
				t.Errorf("VideoCodec = %q, contract declares %q", got, want)
			}
			// The contract spells the canonical profile in lowercase (the
			// VeloxEditing vocabulary); ffprobe — and therefore this registry,
			// which validates ffprobe output — spells it "High". Same profile,
			// different vocabulary, compared case-insensitively so the gate
			// catches a REAL drift (Main ≠ High) without flagging the casing.
			if got, want := profile.CodecProfile, contractString(t, contract, "VideoProfile"); !strings.EqualFold(got, want) {
				t.Errorf("CodecProfile = %q, contract declares %q (accepted values are the encoder-lane equivalence, not a replacement for the canonical value)", got, want)
			}
			if got, want := profile.PixelFormat, contractString(t, contract, "PixelFormat"); got != want {
				t.Errorf("PixelFormat = %q, contract declares %q", got, want)
			}
			if got, want := profile.Width, contractInt(t, contract, "Width"); got != want {
				t.Errorf("Width = %d, contract declares %d", got, want)
			}
			if got, want := profile.Height, contractInt(t, contract, "Height"); got != want {
				t.Errorf("Height = %d, contract declares %d", got, want)
			}
			if got, want := profile.FPSNum, contractRationalInt(t, contract, "FPS", "Num"); got != want {
				t.Errorf("FPSNum = %d, contract declares %d", got, want)
			}
			if got, want := profile.FPSDen, contractRationalInt(t, contract, "FPS", "Den"); got != want {
				t.Errorf("FPSDen = %d, contract declares %d", got, want)
			}
			if got, want := profile.VideoLevel, contractString(t, contract, "VideoLevel"); got != want {
				t.Errorf("VideoLevel = %q, contract declares %q", got, want)
			}
			if got, want := profile.KeyframeInterval, contractInt(t, contract, "KeyframeInterval"); got != want {
				t.Errorf("KeyframeInterval = %d, contract declares %d", got, want)
			}
			if want := contractBool(t, contract, "ClosedGOP"); profile.RequireClosedGOP != want {
				t.Errorf("RequireClosedGOP = %t, contract declares closed_gop=%t", profile.RequireClosedGOP, want)
			}
			if got, want := profile.SARNum, contractRationalInt(t, contract, "SAR", "Num"); got != want {
				t.Errorf("SARNum = %d, contract declares %d", got, want)
			}
			if got, want := profile.SARDen, contractRationalInt(t, contract, "SAR", "Den"); got != want {
				t.Errorf("SARDen = %d, contract declares %d", got, want)
			}
			if got, want := profile.ColorRange, contractString(t, contract, "ColorRange"); got != want {
				t.Errorf("ColorRange = %q, contract declares %q", got, want)
			}
			if got, want := profile.ColorSpace, contractString(t, contract, "ColorSpace"); got != want {
				t.Errorf("ColorSpace = %q, contract declares %q", got, want)
			}
			if got, want := profile.ColorTransfer, contractString(t, contract, "ColorTransfer"); got != want {
				t.Errorf("ColorTransfer = %q, contract declares %q", got, want)
			}
			if got, want := profile.ColorPrimaries, contractString(t, contract, "ColorPrimaries"); got != want {
				t.Errorf("ColorPrimaries = %q, contract declares %q", got, want)
			}
			if want := contractInt(t, contract, "StartPTS") == 0; profile.RequireStartPTSZero != want {
				t.Errorf("RequireStartPTSZero = %t, contract declares StartPTS=%d", profile.RequireStartPTSZero, contractInt(t, contract, "StartPTS"))
			}
			if got, want := profile.VideoStreams, contractInt(t, contract, "VideoStreams"); got != want {
				t.Errorf("VideoStreams = %d, contract declares %d", got, want)
			}
			if got, want := profile.AudioStreams, contractInt(t, contract, "AudioStreams"); got != want {
				t.Errorf("AudioStreams = %d, contract declares %d", got, want)
			}
			if got, want := profile.AudioCodec, contractString(t, contract, "AudioCodec"); got != want {
				t.Errorf("AudioCodec = %q, contract declares %q", got, want)
			}
			if got, want := profile.AudioProfile, contractString(t, contract, "AudioProfile"); got != want {
				t.Errorf("AudioProfile = %q, contract declares %q", got, want)
			}
			if got, want := profile.AudioSampleRate, contractInt(t, contract, "AudioSampleRate"); got != want {
				t.Errorf("AudioSampleRate = %d, contract declares %d", got, want)
			}
		})
	}
}

// TestAssemblyReadyProfileGapsStayVisible pins the dimensions the contract
// declares but this package deliberately does NOT pin yet, so the gap cannot be
// forgotten: a native-lane receipt (2026-09-16) certified mono/58524 audio and
// a 1/12288 video timebase, so pinning the contract's audio channels/bitrate or
// its 1/90000 video timebase today would fail closed on legal renders. Each one
// must be closed by certifying a real clip-lane artifact, not by assumption.
func TestAssemblyReadyProfileGapsStayVisible(t *testing.T) {
	root := workspaceRoot(t)
	profile, err := ResolveProfile(ProfileVeloxAssemblyReadyV1)
	if err != nil {
		t.Fatal(err)
	}
	contract := assemblyContractValues(t, root, ProfileVeloxAssemblyReadyV1)

	if contractInt(t, contract, "AudioChannels") == 0 || contractString(t, contract, "AudioBitrate") == "" {
		t.Fatal("the contract must keep declaring the audio channels/bitrate that this profile does not yet pin")
	}
	if contractRationalInt(t, contract, "VideoTimeBase", "Den") != 90000 {
		t.Fatal("the contract must keep declaring the video timebase this profile does not yet pin")
	}
	if profile.AudioStreams != 1 {
		t.Fatal("the stream counts ARE pinned; only the audio channel layout/bitrate and the video timebase are open")
	}
}
