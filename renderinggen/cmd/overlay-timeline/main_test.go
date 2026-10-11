package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeTimelineFile(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "timeline.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestOverlayTimelineFusesStrictWindows proves back-to-back rows share one
// frame boundary with no gap and no overlap.
func TestOverlayTimelineFusesStrictWindows(t *testing.T) {
	path := writeTimelineFile(t, `{"plan_id":"strict","fps_num":30,"fps_den":1,
		"rows":[
			{"duration_ms":2000,"template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","text":"One"},
			{"duration_ms":1500,"template_id":"QUOTE","preset_id":"phrase_default","text":"Two"}]}`)
	out := t.TempDir()
	if err := run(path, out); err != nil {
		t.Fatalf("run timeline: %v", err)
	}
	for _, name := range []string{"semantic_plan.json", "render_plan.json", "timeline.json"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatalf("missing output %s: %v", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(out, "timeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	var descriptor struct {
		Windows []timelineWindow `json:"windows"`
	}
	if err := json.Unmarshal(raw, &descriptor); err != nil {
		t.Fatal(err)
	}
	if len(descriptor.Windows) != 2 {
		t.Fatalf("windows = %d, want 2", len(descriptor.Windows))
	}
	a, b := descriptor.Windows[0], descriptor.Windows[1]
	if a.StartMS != 0 || a.EndMS != 2000 || b.StartMS != 2000 || b.EndMS != 3500 {
		t.Fatalf("windows = %+v / %+v, want [0,2000] [2000,3500]", a, b)
	}
	if b.StartFrame != a.EndFrame {
		t.Fatalf("frames = %d / %d, want shared boundary", a.EndFrame, b.StartFrame)
	}
}

// TestOverlayTimelineGapQuantizesToFrames proves the uniform gap lands on the
// frame grid (500ms @30fps = 15 frames, second row starts at 2500ms).
func TestOverlayTimelineGapQuantizesToFrames(t *testing.T) {
	path := writeTimelineFile(t, `{"plan_id":"gapped","fps_num":30,"fps_den":1,"gap_ms":500,
		"rows":[
			{"duration_ms":2000,"template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","text":"One"},
			{"duration_ms":1000,"template_id":"QUOTE","preset_id":"phrase_default","text":"Two"}]}`)
	out := t.TempDir()
	if err := run(path, out); err != nil {
		t.Fatalf("run timeline: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "timeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	var descriptor struct {
		Windows []timelineWindow `json:"windows"`
	}
	if err := json.Unmarshal(raw, &descriptor); err != nil {
		t.Fatal(err)
	}
	b := descriptor.Windows[1]
	if b.StartMS != 2500 || b.StartFrame != 75 {
		t.Fatalf("gapped start = %dms/%d, want 2500ms/75", b.StartMS, b.StartFrame)
	}
}

// TestOverlayTimelineRejectsBadRows proves fail-closed input handling.
func TestOverlayTimelineRejectsBadRows(t *testing.T) {
	for name, doc := range map[string]string{
		"empty rows":   `{"plan_id":"x","rows":[]}`,
		"no duration":  `{"plan_id":"x","rows":[{"template_id":"IMPORTANT_PHRASE"}]}`,
		"no template":  `{"plan_id":"x","rows":[{"duration_ms":1000}]}`,
		"bad preset":   `{"plan_id":"x","rows":[{"duration_ms":1000,"template_id":"IMPORTANT_PHRASE","preset_id":"nope","text":"Hi"}]}`,
		"missing file": ``,
	} {
		path := ""
		if name != "missing file" {
			path = writeTimelineFile(t, doc)
		} else {
			path = filepath.Join(t.TempDir(), "absent.json")
		}
		if err := run(path, t.TempDir()); err == nil {
			t.Errorf("run(%s) succeeded, want fail-closed", name)
		}
	}
}
