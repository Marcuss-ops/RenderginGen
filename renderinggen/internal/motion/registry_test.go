package motion

import "testing"

type testPlugin struct{}

func (testPlugin) ID() string            { return "test_plugin" }
func (testPlugin) Validate(Params) error { return nil }
func (testPlugin) Compile(MotionContext, Params) ([]AnimationTrack, error) {
	return []AnimationTrack{{Property: "opacity", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 4, Value: 1.0}}}}, nil
}

func TestRegistryResolvesDeclarativeAndCustomPlugins(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("test_plugin", testPlugin{}); err != nil {
		t.Fatal(err)
	}
	p, err := r.Resolve("test_plugin")
	if err != nil || p.ID() != "test_plugin" {
		t.Fatalf("resolved plugin=%v err=%v", p, err)
	}
	if err := r.Register("test_plugin", testPlugin{}); err == nil {
		t.Fatal("duplicate registration must fail")
	}
	if _, err := r.Resolve("missing"); err == nil {
		t.Fatal("unknown plugin must fail closed")
	}
}

func TestDeclarativePluginCompilesGenericTracks(t *testing.T) {
	p := DeclarativePlugin{Definition: MotionDefinition{ID: "declarative", Tracks: []TrackDefinition{{
		Property: "scale", Easing: "out_back",
		Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.4}, {Frame: 10, Value: 1.0}},
	}}}}
	tracks, err := p.Compile(MotionContext{DurationFrames: 10}, nil)
	if err != nil || len(tracks) != 1 || tracks[0].Property != "scale" {
		t.Fatalf("tracks=%+v err=%v", tracks, err)
	}
}
