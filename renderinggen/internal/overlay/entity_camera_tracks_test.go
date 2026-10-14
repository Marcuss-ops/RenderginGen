package overlay

import (
	"reflect"
	"testing"
)

// TestEntityCameraTracksMatchLegacyKeyframes pins the catalog-driven camera
// tracks to the keyframes that used to be hard-coded in the switch. cx/cy are
// the canvas centre; the legacy literals were cx+160 etc. at render time.
func TestEntityCameraTracksMatchLegacyKeyframes(t *testing.T) {
	const cx, cy = 960.0, 540.0
	kf := func(f int64, v float64) AnimationKeyframe { return AnimationKeyframe{Frame: f, Value: v} }
	track := func(prop string, kfs ...AnimationKeyframe) CameraTrack {
		return CameraTrack{Property: prop, Easing: "out_cubic", Keyframes: kfs}
	}
	want := map[string][]CameraTrack{
		"camera_dolly_push": {
			track("camera_position_z", kf(0, -1700.0), kf(65, -1400.0), kf(89, -1400.0)),
		},
		"camera_orbit_yaw": {
			track("camera_rotation_y", kf(0, 12.0), kf(55, 0.0), kf(89, 0.0)),
			track("camera_position_x", kf(0, cx+160.0), kf(55, cx), kf(89, cx)),
		},
		"camera_crane_rise": {
			track("camera_position_y", kf(0, cy+120.0), kf(55, cy), kf(89, cy)),
		},
		"camera_dutch_spatial": {
			track("camera_rotation_z", kf(0, -3.5), kf(50, 0.0), kf(89, 0.0)),
			track("camera_position_z", kf(0, -1560.0), kf(50, -1400.0), kf(89, -1400.0)),
		},
		"camera_flyby_parallax": {
			track("camera_position_x", kf(0, cx-110.0), kf(60, cx), kf(89, cx)),
			track("camera_rotation_y", kf(0, -8.5), kf(60, 0.0), kf(89, 0.0)),
			track("camera_position_z", kf(0, -1600.0), kf(60, -1400.0), kf(89, -1400.0)),
		},
	}
	for id, expected := range want {
		got, err := entityCameraTracks(id, cx, cy)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", id, err)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("%s: tracks mismatch\n got: %+v\nwant: %+v", id, got, expected)
		}
	}
	if got, err := entityCameraTracks("camera_unknown_id", cx, cy); err != nil || got != nil {
		t.Errorf("unknown id should resolve to no tracks, got %v err %v", got, err)
	}
}
