package config

import "testing"

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
