package config

import (
	"testing"
)

// TestSpotlightClampsWhatTheFileSays. A radius of a thousand rows or a dim of
// 100 percent are both spellable in a file, and both would draw a screen with
// nothing on it.
func TestSpotlightClampsWhatTheFileSays(t *testing.T) {
	wild := SpotlightConfig{Radius: 100000, Dim: 100, Follow: "sideways", Edge: "wobbly"}
	if got := wild.RadiusRows(); got != SpotlightMaxRadius {
		t.Errorf("radius 100000 resolves to %d, want the cap %d", got, SpotlightMaxRadius)
	}
	if got := wild.DimPercent(); got != SpotlightMaxDim {
		t.Errorf("dim 100 resolves to %d, want the cap %d", got, SpotlightMaxDim)
	}
	// A value outside the accepted set falls back rather than being carried
	// into the render path, where it would read as the beam not working.
	if got := wild.FollowMode(); got != SpotlightFollowMouse {
		t.Errorf("follow %q resolves to %q, want %q", wild.Follow, got, SpotlightFollowMouse)
	}
	if got := wild.EdgeStyle(); got != SpotlightEdgeHard {
		t.Errorf("edge %q resolves to %q, want %q", wild.Edge, got, SpotlightEdgeHard)
	}

	small := SpotlightConfig{Radius: -4, Dim: 1}
	if got := small.RadiusRows(); got != SpotlightDefaultRadius {
		t.Errorf("radius -4 resolves to %d, want the default %d", got, SpotlightDefaultRadius)
	}
	if got := small.DimPercent(); got != SpotlightMinDim {
		t.Errorf("dim 1 resolves to %d, want the floor %d", got, SpotlightMinDim)
	}
}
