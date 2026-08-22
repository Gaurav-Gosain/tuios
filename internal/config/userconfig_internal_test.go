package config

import "testing"

// TestMaxFPSClamp pins how a configured max_fps reaches the tick loop.
// ApplyAppearanceConfig is the only path that applies it now; ApplyOverrides
// carries CLI flags only.
func TestMaxFPSClamp(t *testing.T) {
	tests := []struct {
		in   int
		want int
	}{
		{in: 1, want: MinConfiguredFPS},
		{in: 10, want: 10},
		{in: 60, want: 60},
		{in: 240, want: MaxFPSCap},
		{in: 10000, want: MaxFPSCap},
	}

	orig := NormalFPS
	t.Cleanup(func() { NormalFPS = orig })

	for _, tc := range tests {
		cfg := &UserConfig{}
		cfg.Appearance.MaxFPS = tc.in

		NormalFPS = orig
		ApplyAppearanceConfig(cfg)
		if NormalFPS != tc.want {
			t.Errorf("max_fps %d: apply gave %d, want %d", tc.in, NormalFPS, tc.want)
		}
	}

	// Unset means "leave the current rate alone".
	NormalFPS = 45
	ApplyAppearanceConfig(&UserConfig{})
	if NormalFPS != 45 {
		t.Errorf("an unset max_fps moved the rate to %d, want it left at 45", NormalFPS)
	}
}

// fillMissingAppearance is where hand-written configs get sanitised. It is
// exercised directly here because the only exported entry point (LoadUserConfig)
// reads the real XDG config directory.
func TestFillMissingAppearance_ScrollLines(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "unset falls back to default", in: 0, want: 3},
		{name: "negative falls back to default", in: -4, want: 3},
		{name: "in range kept", in: 12, want: 12},
		{name: "above range clamped", in: 500, want: 50},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &UserConfig{}
			cfg.Appearance.ScrollLines = tc.in
			fillMissingAppearance(cfg, DefaultConfig())
			if cfg.Appearance.ScrollLines != tc.want {
				t.Errorf("scroll_lines %d became %d, want %d", tc.in, cfg.Appearance.ScrollLines, tc.want)
			}
		})
	}
}
