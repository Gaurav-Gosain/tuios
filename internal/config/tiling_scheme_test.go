package config

import "testing"

// TestTilingSchemeDefaultsToSpiral pins DefaultConfig's value, which is what
// every existing "a new workspace always starts with spiral" assumption
// depends on now that the default is a real config value rather than a zero
// value read as a sentinel. See internal/app/tiling_bsp.go.
func TestTilingSchemeDefaultsToSpiral(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Appearance.TilingScheme != TilingSchemeSpiral {
		t.Fatalf("DefaultConfig().Appearance.TilingScheme = %q, want %q", cfg.Appearance.TilingScheme, TilingSchemeSpiral)
	}
}

// TestTilingSchemeValidationWarnsOnATypo mirrors border_style and zen_mode: a
// value outside TilingSchemes is a warning, not a hard error, since it falls
// back to the default rather than refusing to start.
func TestTilingSchemeValidationWarnsOnATypo(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Appearance.TilingScheme = "spyral"
	result := ValidateConfig(cfg)

	found := false
	for _, w := range result.Warnings {
		if w.Key == "tiling_scheme" {
			found = true
		}
	}
	if !found {
		t.Error("an unknown appearance.tiling_scheme value produced no warning")
	}
	if result.HasErrors() {
		t.Error("an unknown appearance.tiling_scheme value was an error, not a warning")
	}

	cfg.Appearance.TilingScheme = ""
	if result := ValidateConfig(cfg); len(result.Warnings) > 0 {
		for _, w := range result.Warnings {
			if w.Key == "tiling_scheme" {
				t.Error("an empty appearance.tiling_scheme (follow the default) was warned about")
			}
		}
	}

	for _, scheme := range TilingSchemes {
		cfg.Appearance.TilingScheme = scheme
		for _, w := range ValidateConfig(cfg).Warnings {
			if w.Key == "tiling_scheme" {
				t.Errorf("a valid scheme %q was warned about: %s", scheme, w.Message)
			}
		}
	}
}

// TestApplyAppearanceConfigTilingScheme covers the same three shapes
// BorderStyle, ZenMode and Links already have in ApplyAppearanceConfig: a
// valid value is taken, a typo falls back to spiral rather than a policy-less
// state, and empty leaves whatever the settings already held (a flag, or a
// previous file).
func TestApplyAppearanceConfigTilingScheme(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Appearance.TilingScheme = TilingSchemeLongestSide
	s := DefaultSettings()
	ApplyAppearanceConfig(cfg, &s)
	if s.TilingScheme != TilingSchemeLongestSide {
		t.Fatalf("a valid tiling_scheme was not applied: got %q", s.TilingScheme)
	}

	cfg.Appearance.TilingScheme = "spyral"
	s = DefaultSettings()
	ApplyAppearanceConfig(cfg, &s)
	if s.TilingScheme != TilingSchemeSpiral {
		t.Fatalf("a typo'd tiling_scheme fell back to %q, want %q", s.TilingScheme, TilingSchemeSpiral)
	}

	cfg.Appearance.TilingScheme = ""
	s = DefaultSettings()
	s.TilingScheme = TilingSchemeAlternate
	ApplyAppearanceConfig(cfg, &s)
	if s.TilingScheme != TilingSchemeAlternate {
		t.Fatalf("an empty tiling_scheme overwrote the running settings: got %q", s.TilingScheme)
	}
}

// TestSetOptionValueTilingScheme exercises the registry path tuios
// set-config and the settings page both use: a valid scheme round-trips, and
// an unknown one is refused by name rather than silently ignored.
func TestSetOptionValueTilingScheme(t *testing.T) {
	cfg := DefaultConfig()
	if err := SetOptionValue(cfg, "appearance.tiling_scheme", "alternate"); err != nil {
		t.Fatalf("set appearance.tiling_scheme=alternate: %v", err)
	}
	got, ok := GetOptionValue(cfg, "appearance.tiling_scheme")
	if !ok || got != "alternate" {
		t.Fatalf("GetOptionValue after set = (%q, %v), want (\"alternate\", true)", got, ok)
	}

	if err := SetOptionValue(cfg, "appearance.tiling_scheme", "diagonal"); err == nil {
		t.Fatal("set appearance.tiling_scheme=diagonal was accepted")
	}
}
