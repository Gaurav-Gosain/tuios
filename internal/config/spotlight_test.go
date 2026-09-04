package config

import (
	"strconv"
	"testing"
)

// TestSpotlightDefaultsAreTheOnesTheRegistryPublishes. Three things answer for
// a setting nobody wrote down: the section's accessors, the table DefaultConfig
// builds, and the registry an agent reads through list-options. All three have
// to say the same thing, so this compares every one of them against the
// registry rather than against the constants they are all built from. A
// constant changed on its own is the way the three drift apart.
func TestSpotlightDefaultsAreTheOnesTheRegistryPublishes(t *testing.T) {
	var unset SpotlightConfig
	if unset.IsEnabled() {
		t.Error("an unset [spotlight] section starts with the beam on")
	}
	built := DefaultConfig().Spotlight
	for _, tc := range []struct{ path, accessor, table string }{
		{"spotlight.follow", unset.FollowMode(), built.FollowMode()},
		{"spotlight.edge", unset.EdgeStyle(), built.EdgeStyle()},
		{"spotlight.radius", strconv.Itoa(unset.RadiusRows()), strconv.Itoa(built.RadiusRows())},
		{"spotlight.dim", strconv.Itoa(unset.DimPercent()), strconv.Itoa(built.DimPercent())},
	} {
		opt, ok := LookupOption(tc.path)
		if !ok {
			t.Fatalf("%s has no registry entry", tc.path)
		}
		if tc.accessor != opt.Default {
			t.Errorf("%s resolves to %q, the registry publishes %q", tc.path, tc.accessor, opt.Default)
		}
		if tc.table != opt.Default {
			t.Errorf("%s is %q in DefaultConfig, the registry publishes %q", tc.path, tc.table, opt.Default)
		}
	}
}

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

// TestSpotlightOptionsRoundTrip through the path the control protocol and the
// settings page both use.
func TestSpotlightOptionsRoundTrip(t *testing.T) {
	for _, tc := range []struct{ path, set, want string }{
		{"spotlight.enabled", "on", "true"},
		{"spotlight.follow", "mouse", "mouse"},
		{"spotlight.radius", "18", "18"},
		{"spotlight.dim", "80", "80"},
		{"spotlight.edge", "soft", "soft"},
	} {
		cfg := DefaultConfig()
		if err := SetOptionValue(cfg, tc.path, tc.set); err != nil {
			t.Fatalf("set %s=%q: %v", tc.path, tc.set, err)
		}
		got, ok := GetOptionValue(cfg, tc.path)
		if !ok {
			t.Fatalf("get %s: not found after setting it", tc.path)
		}
		if got != tc.want {
			t.Errorf("set %s=%q, read back %q, want %q", tc.path, tc.set, got, tc.want)
		}
	}

	for _, tc := range []struct{ path, set string }{
		{"spotlight.follow", "sideways"},
		{"spotlight.edge", "wobbly"},
		{"spotlight.radius", "100000"},
		{"spotlight.dim", "0"},
	} {
		cfg := DefaultConfig()
		if err := SetOptionValue(cfg, tc.path, tc.set); err == nil {
			t.Errorf("set %s=%q was accepted", tc.path, tc.set)
		}
	}
}
