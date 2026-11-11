package config

import (
	"log"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// OptionSet is one option value set from outside the config file: a CLI
// flag, or an option of an embedding program. It goes through
// SetOptionValue, so it is checked as a value sent with set-config is.
type OptionSet struct {
	Path  string
	Value string
}

// Overrides are the values a program lays over the config file for one run:
// the CLI flags, or an embedder's options. They change the settings a
// session starts from, never the loaded config, which the settings page
// saves back to the file.
type Overrides struct {
	// Sets are option values laid over the file before the settings are
	// resolved, in order. See Config.
	Sets []OptionSet

	// ASCIIOnly uses ASCII characters instead of Nerd Font icons.
	ASCIIOnly bool

	// NoAnimations turns every animation off for this run.
	NoAnimations bool

	// ThemeName is the theme to load.
	ThemeName string
}

// Config is cfg with the Sets applied, as a copy, so the caller's config
// keeps the file's values. SetOptionValue allocates every pointer field it
// writes, so the copy shares no value it changes with cfg. A set that
// fails, which a flag cannot do because it is checked when it is parsed, is
// logged and skipped. With no Sets it is cfg itself.
func (ov Overrides) Config(cfg *UserConfig) *UserConfig {
	if len(ov.Sets) == 0 {
		return cfg
	}
	if cfg == nil {
		cfg = DefaultConfig()
	}
	c := *cfg
	for _, set := range ov.Sets {
		if err := SetOptionValue(&c, set.Path, set.Value); err != nil {
			log.Printf("Warning: %v", err)
		}
	}
	return &c
}

// themeFlag is the last --theme name ApplyOverrides applied, or "".
// ApplyAppearanceConfig uses it for a config file that sets no theme.
var themeFlag atomic.Pointer[string]

// FlagTheme is the theme the --theme flag named, or "". An explicit flag wins
// over appearance.theme_light.
func FlagTheme() string { return flagTheme() }

func flagTheme() string {
	if p := themeFlag.Load(); p != nil {
		return *p
	}
	return ""
}

// ApplyOverrides applies the overrides that are not config options: ASCII
// glyphs, no animations and the theme. The Sets are applied before the
// settings are resolved, through Config, so every entrypoint resolves with
// ApplyAppearanceConfig(ov.Config(cfg), s) and then calls this.
func ApplyOverrides(overrides Overrides, s *Settings) {
	if overrides.ASCIIOnly {
		s.ASCIIRequested = true
		s.UseASCIIOnly = true
	}
	if overrides.NoAnimations {
		s.NoAnimationsFlag = true
		s.Motion = MotionNone
	}
	if overrides.ThemeName != "" {
		themeFlag.Store(&overrides.ThemeName)
		if err := theme.Initialize(overrides.ThemeName); err != nil {
			log.Printf("Warning: Failed to load theme '%s': %v", overrides.ThemeName, err)
		}
	}
}

// AppearanceFrom builds the appearance settings a newly served session starts
// from: the built-in defaults, the config file as it is now, then the caller's
// flag overrides.
//
// It is the same order the process globals get once at server startup, run per
// connection instead of once, so a session that connects after an edit follows
// the file rather than the copy the server loaded when it started. A nil cfg is
// an empty one, so the overrides still apply.
func AppearanceFrom(cfg *UserConfig, ov Overrides) Settings {
	s := DefaultSettings()
	if cfg != nil || len(ov.Sets) > 0 {
		ApplyAppearanceConfig(ov.Config(cfg), &s)
	}
	ApplyOverrides(ov, &s)
	return s
}
