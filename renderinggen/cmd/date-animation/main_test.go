package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIListsThirteenStyles(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if lines := strings.Count(strings.TrimSpace(stdout.String()), "\n") + 1; lines != 13 {
		t.Fatalf("listed %d styles, want 13:\n%s", lines, stdout.String())
	}
	if strings.Contains(stdout.String(), "14_mask_reveal") || strings.Contains(stdout.String(), "15_archival_chronology") {
		t.Fatal("removed styles were listed")
	}
}

func TestCLISelectsForMultipleDates(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-dates", "1948,1969,1998,2026", "-plan-id", "p", "-video-id", "v"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "font=") {
		t.Fatalf("missing selected style: %s", stdout.String())
	}
}

func TestCLISelectsFromEditorialText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-text", "Nel 1948 nasce; nel 1969 cambia; nel 1998 arriva il lancio; nel 2026 il presente.",
		"-plan-id", "p", "-video-id", "v",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "font=") {
		t.Fatalf("missing selected style: %s", stdout.String())
	}
}

func TestCLIRejectsDatesAndTextTogether(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-dates", "1948,1969",
		"-text", "Nel 1948 e nel 1969.",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %s", code, stderr.String())
	}
}

func TestCLIWritesMaterializedPlan(t *testing.T) {
	out := filepath.Join(t.TempDir(), "date.plan.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-text", "Nel 2007 parte; nel 2013 cresce; nel 2017 cambia; nel 2024 si chiude.",
		"-plan-id", "p", "-video-id", "v", "-plan-out", out, "-json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	canvas, _ := plan["canvas"].(map[string]any)
	if got, _ := canvas["duration_frames"].(float64); got != 240 {
		t.Fatalf("duration = %v, want 240", canvas["duration_frames"])
	}
	if strings.Contains(string(raw), `"1948"`) {
		t.Fatal("CLI plan still contains sample dates")
	}
	if !strings.Contains(stdout.String(), `"plan"`) || !strings.Contains(stdout.String(), `"dates"`) {
		t.Fatalf("JSON output missing plan/dates: %s", stdout.String())
	}
}

func TestCLIMaterializeRejectsOneDate(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-text", "Nel 2026 il presente.",
		"-plan-out", filepath.Join(t.TempDir(), "one.plan.json"),
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "at least two dates") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}
