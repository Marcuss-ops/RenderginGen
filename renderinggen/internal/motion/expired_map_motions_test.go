package motion

import "testing"

// The ten map_image_* rows deprecated on 2026-10-08 were removed instead of
// being kept as dead catalog entries: deprecation kept them resolvable for
// saved plans but unselectable, and their remove_after date had passed. This
// gate prevents a stale source catalog from reintroducing them silently.
func TestExpiredMapMotionsAreRemoved(t *testing.T) {
	for _, id := range []string{
		"map_image_australia_sunset_drift",
		"map_image_brazil_glow_reveal",
		"map_image_china_slow_reveal",
		"map_image_gujarat_detail_push",
		"map_image_india_contour_draw",
		"map_image_iran_gold_focus",
		"map_image_italy_beacon_arrival",
		"map_image_korea_pin_focus",
		"map_image_nigeria_neon_bloom",
		"map_image_usa_sweep_in",
	} {
		if _, err := Registry.Resolve(id); err == nil {
			t.Errorf("expired map motion %q is still registered", id)
		}
	}
}
