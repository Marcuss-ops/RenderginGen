package overlay

import (
	"math"
	"testing"
)

func TestAELensPresetsContain50mmDefault(t *testing.T) {
	found50 := false
	for _, p := range AELensPresets() {
		if !IsAELensPreset(p.FocalMM) {
			t.Errorf("preset %v is not recognized by IsAELensPreset", p.FocalMM)
		}
		if p.FocalMM == 50 {
			found50 = true
		}
	}
	if !found50 {
		t.Error("AE preset family has no 50mm default")
	}
	if IsAELensPreset(42) {
		t.Error("42mm must not be an AE preset")
	}
}

func TestFocalFOVRoundTrip(t *testing.T) {
	for _, mm := range []float64{24, 28, 35, 50, 85} {
		fov, err := FOVDegFromFocalMM(mm)
		if err != nil {
			t.Fatalf("FOVDegFromFocalMM(%v): %v", mm, err)
		}
		back, err := FocalMMFromFOVDeg(fov)
		if err != nil {
			t.Fatalf("FocalMMFromFOVDeg(%v): %v", fov, err)
		}
		if math.Abs(back-mm) > 1e-9 {
			t.Errorf("round trip %vmm -> %vdeg -> %vmm", mm, fov, back)
		}
	}
	// 50mm on the full-frame gate is ~27deg vertical.
	fov50, _ := FOVDegFromFocalMM(50)
	if math.Abs(fov50-27) > 1 {
		t.Errorf("50mm fov = %v, want ~27deg", fov50)
	}
	if _, err := FOVDegFromFocalMM(8); err == nil {
		t.Error("8mm must be rejected")
	}
}

func TestDisperseDepths(t *testing.T) {
	got, err := DisperseDepths(3, 3000, 250)
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{3000, 3250, 3500}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("depth[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	if _, err := DisperseDepths(0, 0, 250); err == nil {
		t.Error("zero layers must be rejected")
	}
}

func TestApparentSizeCompensation(t *testing.T) {
	s, err := ScaleToPreserveApparentSize(-1400, -2800)
	if err != nil {
		t.Fatal(err)
	}
	if s != 2 {
		t.Errorf("scale = %v, want 2", s)
	}
	if _, err := ScaleToPreserveApparentSize(0, -100); err == nil {
		t.Error("zero depth must be rejected")
	}
}
