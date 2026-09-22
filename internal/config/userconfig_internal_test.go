package config

import (
	"fmt"
	"strings"
	"testing"
)

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

	orig := Global.NormalFPS
	t.Cleanup(func() { Global.NormalFPS = orig })

	for _, tc := range tests {
		cfg := &UserConfig{}
		cfg.Appearance.MaxFPS = tc.in

		Global.NormalFPS = orig
		ApplyAppearanceConfig(cfg, &Global)
		if Global.NormalFPS != tc.want {
			t.Errorf("max_fps %d: apply gave %d, want %d", tc.in, Global.NormalFPS, tc.want)
		}
	}

	// Unset means "leave the current rate alone".
	Global.NormalFPS = 45
	ApplyAppearanceConfig(&UserConfig{}, &Global)
	if Global.NormalFPS != 45 {
		t.Errorf("an unset max_fps moved the rate to %d, want it left at 45", Global.NormalFPS)
	}
}

// TestMaxFPSDescriptionMatchesClamp. list-options described max_fps as "10 to
// 240" while the code clamps to 120, so a user asking for 240 got 120 with no
// word of it.
func TestMaxFPSDescriptionMatchesClamp(t *testing.T) {
	opt, ok := LookupOption("appearance.max_fps")
	if !ok {
		t.Fatal("appearance.max_fps is not in the option registry")
	}
	want := fmt.Sprintf("The range is %d to %d.", clampMaxFPS(1), clampMaxFPS(1<<20))
	if !strings.Contains(opt.Description, want) {
		t.Errorf("max_fps description %q does not say %q", opt.Description, want)
	}
	if opt.Max != clampMaxFPS(1<<20) {
		t.Errorf("max_fps accepts up to %d, the clamp allows %d", opt.Max, clampMaxFPS(1<<20))
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
